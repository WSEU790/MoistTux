package collect

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/WSEU790/MoistTux/internal/protocol"
)

// ---------------------------------------------------------------- CPU

// CPUTimes holds cumulative jiffies for one CPU line of /proc/stat.
type CPUTimes struct {
	User, Nice, System, Idle, IOWait, IRQ, SoftIRQ, Steal uint64
}

func (t CPUTimes) total() uint64 {
	return t.User + t.Nice + t.System + t.Idle + t.IOWait + t.IRQ + t.SoftIRQ + t.Steal
}

// StatSnapshot is the parsed subset of /proc/stat we need.
type StatSnapshot struct {
	All          CPUTimes
	Cores        []CPUTimes
	BootTime     int64
	ProcsRunning int
	ProcsBlocked int
}

func (h *Host) ReadStat() (*StatSnapshot, error) {
	data, err := os.ReadFile(h.proc("stat"))
	if err != nil {
		return nil, err
	}
	s := &StatSnapshot{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 0 {
			continue
		}
		switch {
		case f[0] == "cpu":
			s.All = parseCPUTimes(f[1:])
		case strings.HasPrefix(f[0], "cpu"):
			s.Cores = append(s.Cores, parseCPUTimes(f[1:]))
		case f[0] == "btime" && len(f) > 1:
			s.BootTime, _ = strconv.ParseInt(f[1], 10, 64)
		case f[0] == "procs_running" && len(f) > 1:
			s.ProcsRunning, _ = strconv.Atoi(f[1])
		case f[0] == "procs_blocked" && len(f) > 1:
			s.ProcsBlocked, _ = strconv.Atoi(f[1])
		}
	}
	return s, sc.Err()
}

func parseCPUTimes(f []string) CPUTimes {
	v := make([]uint64, 8)
	for i := 0; i < len(f) && i < 8; i++ {
		v[i], _ = strconv.ParseUint(f[i], 10, 64)
	}
	return CPUTimes{v[0], v[1], v[2], v[3], v[4], v[5], v[6], v[7]}
}

func pct(part, whole float64) float64 {
	if whole <= 0 {
		return 0
	}
	p := part / whole * 100
	if p < 0 {
		return 0
	}
	if p > 100 {
		return 100
	}
	return round2(p)
}

func round2(v float64) float64 { return float64(int64(v*100+0.5)) / 100 }

// CPUDelta computes utilisation between two snapshots.
func CPUDelta(prev, cur *StatSnapshot) protocol.CPUStats {
	out := protocol.CPUStats{Threads: len(cur.Cores)}
	d := func(a, b CPUTimes) (tot, idle, user, sys, iow, steal float64) {
		tot = float64(b.total() - a.total())
		idle = float64((b.Idle + b.IOWait) - (a.Idle + a.IOWait))
		user = float64((b.User + b.Nice) - (a.User + a.Nice))
		sys = float64((b.System + b.IRQ + b.SoftIRQ) - (a.System + a.IRQ + a.SoftIRQ))
		iow = float64(b.IOWait - a.IOWait)
		steal = float64(b.Steal - a.Steal)
		return
	}
	if prev == nil || cur.All.total() < prev.All.total() {
		return out
	}
	tot, idle, user, sys, iow, steal := d(prev.All, cur.All)
	out.UsagePct = pct(tot-idle, tot)
	out.UserPct = pct(user, tot)
	out.SystemPct = pct(sys, tot)
	out.IOWaitPct = pct(iow, tot)
	out.StealPct = pct(steal, tot)
	if len(prev.Cores) == len(cur.Cores) {
		out.PerCore = make([]float64, len(cur.Cores))
		for i := range cur.Cores {
			t, id, _, _, _, _ := d(prev.Cores[i], cur.Cores[i])
			out.PerCore[i] = pct(t-id, t)
		}
	}
	return out
}

// ---------------------------------------------------------------- memory

func (h *Host) ReadMem() (protocol.MemStats, error) {
	data, err := os.ReadFile(h.proc("meminfo"))
	if err != nil {
		return protocol.MemStats{}, err
	}
	kv := parseKV(data)
	m := protocol.MemStats{
		Total:     parseKiB(kv["MemTotal"]),
		Buffers:   parseKiB(kv["Buffers"]),
		Cached:    parseKiB(kv["Cached"]) + parseKiB(kv["SReclaimable"]),
		SwapTotal: parseKiB(kv["SwapTotal"]),
	}
	if v, ok := kv["MemAvailable"]; ok {
		m.Available = parseKiB(v)
	} else { // kernels < 3.14
		m.Available = parseKiB(kv["MemFree"]) + m.Buffers + m.Cached
	}
	if m.Total >= m.Available {
		m.Used = m.Total - m.Available
	}
	swapFree := parseKiB(kv["SwapFree"])
	if m.SwapTotal >= swapFree {
		m.SwapUsed = m.SwapTotal - swapFree
	}
	m.UsedPct = pct(float64(m.Used), float64(m.Total))
	m.SwapPct = pct(float64(m.SwapUsed), float64(m.SwapTotal))
	return m, nil
}

// ---------------------------------------------------------------- load, uptime

func (h *Host) ReadLoad() ([3]float64, int, error) {
	var out [3]float64
	s, err := readTrim(h.proc("loadavg"))
	if err != nil {
		return out, 0, err
	}
	f := strings.Fields(s)
	if len(f) < 4 {
		return out, 0, fmt.Errorf("unexpected loadavg %q", s)
	}
	for i := 0; i < 3; i++ {
		out[i], _ = strconv.ParseFloat(f[i], 64)
	}
	threads := 0
	if _, t, ok := strings.Cut(f[3], "/"); ok {
		threads, _ = strconv.Atoi(t)
	}
	return out, threads, nil
}

func (h *Host) ReadUptime() (float64, error) {
	s, err := readTrim(h.proc("uptime"))
	if err != nil {
		return 0, err
	}
	f := strings.Fields(s)
	if len(f) == 0 {
		return 0, fmt.Errorf("empty uptime")
	}
	return strconv.ParseFloat(f[0], 64)
}

func (h *Host) BootID() string {
	s, _ := readTrim(h.proc("sys/kernel/random/boot_id"))
	return s
}

// ---------------------------------------------------------------- disk IO

type DiskCounters struct {
	Reads, Writes, SectorsRead, SectorsWritten, IOTicksMs uint64
}

// ReadDiskStats returns counters for whole physical/virtual disks (partitions,
// loop and ram devices are excluded to avoid double counting).
func (h *Host) ReadDiskStats() (map[string]DiskCounters, error) {
	data, err := os.ReadFile(h.proc("diskstats"))
	if err != nil {
		return nil, err
	}
	out := map[string]DiskCounters{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 14 {
			continue
		}
		name := f[2]
		if !h.isWholeDisk(name) {
			continue
		}
		u := func(i int) uint64 { v, _ := strconv.ParseUint(f[i], 10, 64); return v }
		out[name] = DiskCounters{Reads: u(3), SectorsRead: u(5), Writes: u(7), SectorsWritten: u(9), IOTicksMs: u(12)}
	}
	return out, sc.Err()
}

func (h *Host) isWholeDisk(name string) bool {
	if strings.HasPrefix(name, "loop") || strings.HasPrefix(name, "ram") || strings.HasPrefix(name, "zram") || strings.HasPrefix(name, "sr") {
		return false
	}
	// /sys/block contains only whole devices (including dm-*, md*).
	if _, err := os.Stat(h.sys("block", name)); err == nil {
		return true
	}
	return false
}

func DiskDelta(prev, cur map[string]DiskCounters, dt float64) []protocol.DiskIO {
	if dt <= 0 {
		return nil
	}
	var out []protocol.DiskIO
	for name, c := range cur {
		p, ok := prev[name]
		if !ok || c.SectorsRead < p.SectorsRead || c.SectorsWritten < p.SectorsWritten {
			continue
		}
		out = append(out, protocol.DiskIO{
			Device:    name,
			ReadBps:   round2(float64(c.SectorsRead-p.SectorsRead) * 512 / dt),
			WriteBps:  round2(float64(c.SectorsWritten-p.SectorsWritten) * 512 / dt),
			ReadIOPS:  round2(float64(c.Reads-p.Reads) / dt),
			WriteIOPS: round2(float64(c.Writes-p.Writes) / dt),
			UtilPct:   pct(float64(c.IOTicksMs-p.IOTicksMs), dt*1000),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Device < out[j].Device })
	return out
}

// ---------------------------------------------------------------- network

type NetCounters struct {
	RxBytes, RxPackets, RxErrs, RxDrop, TxBytes, TxPackets, TxErrs, TxDrop uint64
}

func (h *Host) ReadNetDev() (map[string]NetCounters, error) {
	data, err := os.ReadFile(h.proc("net/dev"))
	if err != nil {
		return nil, err
	}
	out := map[string]NetCounters{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := sc.Text()
		name, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		f := strings.Fields(rest)
		if len(f) < 16 {
			continue
		}
		u := func(i int) uint64 { v, _ := strconv.ParseUint(f[i], 10, 64); return v }
		out[name] = NetCounters{u(0), u(1), u(2), u(3), u(8), u(9), u(10), u(11)}
	}
	return out, sc.Err()
}

func NetDelta(prev, cur map[string]NetCounters, dt float64) []protocol.NetRate {
	if dt <= 0 {
		return nil
	}
	var out []protocol.NetRate
	for name, c := range cur {
		p, ok := prev[name]
		if !ok || c.RxBytes < p.RxBytes || c.TxBytes < p.TxBytes {
			continue
		}
		out = append(out, protocol.NetRate{
			Iface:  name,
			RxBps:  round2(float64(c.RxBytes-p.RxBytes) / dt),
			TxBps:  round2(float64(c.TxBytes-p.TxBytes) / dt),
			RxPps:  round2(float64(c.RxPackets-p.RxPackets) / dt),
			TxPps:  round2(float64(c.TxPackets-p.TxPackets) / dt),
			RxErrs: c.RxErrs, TxErrs: c.TxErrs, RxDrop: c.RxDrop, TxDrop: c.TxDrop,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Iface < out[j].Iface })
	return out
}

// TCPEstablished counts ESTABLISHED sockets in /proc/net/tcp{,6}.
func (h *Host) TCPEstablished() int {
	n := 0
	for _, f := range []string{"net/tcp", "net/tcp6"} {
		data, err := os.ReadFile(h.proc(f))
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(bytes.NewReader(data))
		sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
		first := true
		for sc.Scan() {
			if first {
				first = false
				continue
			}
			fl := strings.Fields(sc.Text())
			if len(fl) > 3 && fl[3] == "01" {
				n++
			}
		}
	}
	return n
}

// ---------------------------------------------------------------- process counts

func (h *Host) ProcCounts(st *StatSnapshot, threads int) protocol.ProcCounts {
	pc := protocol.ProcCounts{Threads: threads}
	if st != nil {
		pc.Running, pc.Blocked = st.ProcsRunning, st.ProcsBlocked
	}
	entries, err := os.ReadDir(h.Proc)
	if err != nil {
		return pc
	}
	for _, e := range entries {
		if !isPID(e.Name()) {
			continue
		}
		pc.Total++
		b, err := os.ReadFile(h.proc(e.Name(), "stat"))
		if err != nil {
			continue
		}
		if i := bytes.LastIndexByte(b, ')'); i > 0 && i+2 < len(b) && b[i+2] == 'Z' {
			pc.Zombie++
		}
	}
	return pc
}

func isPID(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// ---------------------------------------------------------------- temperatures

// ReadTemps reads hwmon and thermal_zone sensors. On many VMs none exist,
// in which case an empty slice is returned (never a placeholder value).
func (h *Host) ReadTemps() []protocol.TempValue {
	var out []protocol.TempValue
	hw, _ := filepath.Glob(h.sys("class/hwmon/hwmon*"))
	for _, dir := range hw {
		name, _ := readTrim(filepath.Join(dir, "name"))
		inputs, _ := filepath.Glob(filepath.Join(dir, "temp*_input"))
		for _, in := range inputs {
			v, err := readInt(in)
			if err != nil || v <= 0 || v > 150000 {
				continue
			}
			label, _ := readTrim(strings.TrimSuffix(in, "_input") + "_label")
			if label == "" {
				label = strings.TrimSuffix(filepath.Base(in), "_input")
			}
			out = append(out, protocol.TempValue{Sensor: name, Label: label, Celsius: round2(float64(v) / 1000)})
		}
	}
	if len(out) == 0 {
		tz, _ := filepath.Glob(h.sys("class/thermal/thermal_zone*"))
		for _, dir := range tz {
			v, err := readInt(filepath.Join(dir, "temp"))
			if err != nil || v <= 0 || v > 150000 {
				continue
			}
			typ, _ := readTrim(filepath.Join(dir, "type"))
			out = append(out, protocol.TempValue{Sensor: "thermal", Label: typ, Celsius: round2(float64(v) / 1000)})
		}
	}
	return out
}

// ---------------------------------------------------------------- helpers

func BootTimeFrom(st *StatSnapshot) time.Time {
	if st == nil || st.BootTime == 0 {
		return time.Time{}
	}
	return time.Unix(st.BootTime, 0).UTC()
}
