//go:build linux

package agent

import (
	"sync"
	"time"

	"github.com/WSEU790/MoistTux/internal/agent/collect"
	"github.com/WSEU790/MoistTux/internal/protocol"
)

// Sampler collects a metrics sample every interval and keeps a bounded ring
// buffer so the core can fetch everything it missed since its last poll.
type Sampler struct {
	h        *collect.Host
	interval time.Duration
	capacity int

	mu      sync.RWMutex
	ring    []protocol.MetricsSample
	seq     uint64
	lastErr string
	lastAt  time.Time

	prevStat *collect.StatSnapshot
	prevDisk map[string]collect.DiskCounters
	prevNet  map[string]collect.NetCounters
	prevAt   time.Time
	fsEvery  int
	fsCache  []protocol.FSUsage
	tick     int
}

func NewSampler(h *collect.Host, interval time.Duration, capacity int) *Sampler {
	if interval < time.Second {
		interval = time.Second
	}
	if capacity < 10 {
		capacity = 10
	}
	// Filesystem statfs is cheap but touches every mount; do it every ~15 s.
	fsEvery := int((15 * time.Second) / interval)
	if fsEvery < 1 {
		fsEvery = 1
	}
	return &Sampler{h: h, interval: interval, capacity: capacity, fsEvery: fsEvery}
}

func (s *Sampler) Interval() time.Duration { return s.interval }

// Run blocks until stop is closed.
func (s *Sampler) Run(stop <-chan struct{}) {
	s.prime()
	t := time.NewTicker(s.interval)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			s.sample()
		}
	}
}

func (s *Sampler) prime() {
	s.prevStat, _ = s.h.ReadStat()
	s.prevDisk, _ = s.h.ReadDiskStats()
	s.prevNet, _ = s.h.ReadNetDev()
	s.prevAt = time.Now()
	s.fsCache = s.h.Filesystems(false)
}

func (s *Sampler) sample() {
	now := time.Now()
	dt := now.Sub(s.prevAt).Seconds()
	st, err := s.h.ReadStat()
	if err != nil {
		s.setErr("read /proc/stat: " + err.Error())
		return
	}
	mem, err := s.h.ReadMem()
	if err != nil {
		s.setErr("read /proc/meminfo: " + err.Error())
		return
	}
	load, threads, _ := s.h.ReadLoad()
	disk, _ := s.h.ReadDiskStats()
	netc, _ := s.h.ReadNetDev()
	up, _ := s.h.ReadUptime()
	s.tick++
	if s.tick%s.fsEvery == 0 {
		s.fsCache = s.h.Filesystems(false)
	}
	smp := protocol.MetricsSample{
		Time:     now.UTC(),
		CPU:      collect.CPUDelta(s.prevStat, st),
		Load:     load,
		Mem:      mem,
		FS:       s.fsCache,
		DiskIO:   collect.DiskDelta(s.prevDisk, disk, dt),
		Net:      collect.NetDelta(s.prevNet, netc, dt),
		Procs:    s.h.ProcCounts(st, threads),
		Uptime:   up,
		Temps:    s.h.ReadTemps(),
		TCPConns: s.h.TCPEstablished(),
	}
	s.prevStat, s.prevDisk, s.prevNet, s.prevAt = st, disk, netc, now

	s.mu.Lock()
	s.seq++
	smp.Seq = s.seq
	if len(s.ring) >= s.capacity {
		copy(s.ring, s.ring[1:])
		s.ring[len(s.ring)-1] = smp
	} else {
		s.ring = append(s.ring, smp)
	}
	s.lastErr = ""
	s.lastAt = now
	s.mu.Unlock()
}

func (s *Sampler) setErr(e string) {
	s.mu.Lock()
	s.lastErr = e
	s.mu.Unlock()
}

// Since returns samples with Seq > since (all buffered samples if since is 0
// or older than the buffer).
func (s *Sampler) Since(since uint64, max int) []protocol.MetricsSample {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []protocol.MetricsSample
	for _, smp := range s.ring {
		if smp.Seq > since {
			out = append(out, smp)
		}
	}
	if max > 0 && len(out) > max {
		out = out[len(out)-max:]
	}
	return out
}

// Latest returns the most recent sample, if any.
func (s *Sampler) Latest() (protocol.MetricsSample, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.ring) == 0 {
		return protocol.MetricsSample{}, false
	}
	return s.ring[len(s.ring)-1], true
}

// Status reports whether sampling is healthy.
func (s *Sampler) Status() (ok bool, errMsg string, seq uint64) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	stale := !s.lastAt.IsZero() && time.Since(s.lastAt) > 3*s.interval
	if stale && s.lastErr == "" {
		return false, "sampler stalled", s.seq
	}
	return s.lastErr == "" && s.seq > 0, s.lastErr, s.seq
}
