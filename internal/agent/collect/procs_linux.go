//go:build linux

package collect

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/WSEU790/MoistTux/internal/protocol"
)

const userHZ = 100 // USER_HZ is fixed at 100 on all mainstream Linux ABIs

var pageSize = uint64(os.Getpagesize())

type procRaw struct {
	pid, ppid  int
	name       string
	state      string
	utime      uint64
	stime      uint64
	startTicks uint64
	vsize      uint64
	rssPages   uint64
	threads    int
}

func (h *Host) readProcStat(pid int) (*procRaw, error) {
	b, err := os.ReadFile(h.proc(strconv.Itoa(pid), "stat"))
	if err != nil {
		return nil, err
	}
	open := bytes.IndexByte(b, '(')
	close := bytes.LastIndexByte(b, ')')
	if open < 0 || close < open || close+2 >= len(b) {
		return nil, fmt.Errorf("malformed stat for %d", pid)
	}
	f := strings.Fields(string(b[close+2:]))
	if len(f) < 22 {
		return nil, fmt.Errorf("short stat for %d", pid)
	}
	u := func(i int) uint64 { v, _ := strconv.ParseUint(f[i], 10, 64); return v }
	ppid, _ := strconv.Atoi(f[1])
	thr, _ := strconv.Atoi(f[17])
	return &procRaw{
		pid: pid, ppid: ppid, name: string(b[open+1 : close]), state: f[0],
		utime: u(11), stime: u(12), threads: thr, startTicks: u(19), vsize: u(20), rssPages: u(21),
	}, nil
}

// ProcSampler computes per-process CPU usage from consecutive snapshots.
type ProcSampler struct {
	h        *Host
	mu       sync.Mutex
	prev     map[int]uint64
	prevAt   time.Time
	users    *userCache
}

func NewProcSampler(h *Host) *ProcSampler {
	return &ProcSampler{h: h, users: newUserCache(h)}
}

func (ps *ProcSampler) snapshot() (map[int]*procRaw, time.Time, int) {
	entries, _ := os.ReadDir(ps.h.Proc)
	out := make(map[int]*procRaw, len(entries))
	failed := 0
	for _, e := range entries {
		if !isPID(e.Name()) {
			continue
		}
		pid, _ := strconv.Atoi(e.Name())
		r, err := ps.h.readProcStat(pid)
		if err != nil {
			failed++ // process exited between readdir and read
			continue
		}
		out[pid] = r
	}
	return out, time.Now(), failed
}

// List returns processes with CPU% measured over a real time window. If the
// previous snapshot is too old or missing, two snapshots 500 ms apart are taken.
func (ps *ProcSampler) List(sortBy string, limit int, memTotal uint64, bootTime time.Time) protocol.ProcessList {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	if ps.prev == nil || time.Since(ps.prevAt) > 30*time.Second || time.Since(ps.prevAt) < 300*time.Millisecond {
		first, at, _ := ps.snapshot()
		ps.prev = map[int]uint64{}
		for pid, r := range first {
			ps.prev[pid] = r.utime + r.stime
		}
		ps.prevAt = at
		time.Sleep(500 * time.Millisecond)
	}
	cur, at, _ := ps.snapshot()
	window := at.Sub(ps.prevAt).Seconds()
	list := make([]protocol.Process, 0, len(cur))
	for pid, r := range cur {
		p := protocol.Process{
			PID: pid, PPID: r.ppid, Name: r.name, State: r.state,
			RSS: r.rssPages * pageSize, VSize: r.vsize, Threads: r.threads,
		}
		if prevTicks, ok := ps.prev[pid]; ok && window > 0 {
			dt := float64(r.utime+r.stime-prevTicks) / userHZ
			p.CPUPct = round2(dt / window * 100)
		}
		if memTotal > 0 {
			p.MemPct = round2(float64(p.RSS) / float64(memTotal) * 100)
		}
		if !bootTime.IsZero() {
			p.StartTime = bootTime.Add(time.Duration(r.startTicks) * time.Second / userHZ)
		}
		list = append(list, p)
	}
	// Remember this snapshot for the next call.
	ps.prev = make(map[int]uint64, len(cur))
	for pid, r := range cur {
		ps.prev[pid] = r.utime + r.stime
	}
	ps.prevAt = at

	switch sortBy {
	case "mem", "rss":
		sort.Slice(list, func(i, j int) bool { return list[i].RSS > list[j].RSS })
	case "pid":
		sort.Slice(list, func(i, j int) bool { return list[i].PID < list[j].PID })
	case "name":
		sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	default:
		sort.Slice(list, func(i, j int) bool {
			if list[i].CPUPct == list[j].CPUPct {
				return list[i].RSS > list[j].RSS
			}
			return list[i].CPUPct > list[j].CPUPct
		})
	}
	total := len(list)
	if limit > 0 && len(list) > limit {
		list = list[:limit]
	}
	// Enrich only the returned rows (cmdline, user, unit) to keep the call cheap.
	unreadable := 0
	for i := range list {
		pid := strconv.Itoa(list[i].PID)
		if b, err := os.ReadFile(ps.h.proc(pid, "cmdline")); err == nil {
			list[i].Cmdline = strings.TrimSpace(strings.ReplaceAll(string(b), "\x00", " "))
		}
		if b, err := os.ReadFile(ps.h.proc(pid, "status")); err == nil {
			kv := parseKV(b)
			if f := strings.Fields(kv["Uid"]); len(f) > 0 {
				uid, _ := strconv.Atoi(f[0])
				list[i].User = ps.users.name(uid)
			}
		} else {
			unreadable++
		}
		if b, err := os.ReadFile(ps.h.proc(pid, "cgroup")); err == nil {
			list[i].Unit, list[i].Container = cgroupOwner(string(b))
		}
	}
	res := protocol.ProcessList{Processes: list, Total: total, WindowMs: int64(window * 1000)}
	if unreadable > 0 {
		res.Partial = fmt.Sprintf("%d processes exited or were unreadable during collection", unreadable)
	}
	return res
}

var (
	reDockerScope = regexp.MustCompile(`docker[-/]([0-9a-f]{12,64})`)
	reService     = regexp.MustCompile(`/([^/]+\.service)`)
)

func cgroupOwner(cg string) (unit, container string) {
	if m := reDockerScope.FindStringSubmatch(cg); m != nil {
		container = m[1]
		if len(container) > 12 {
			container = container[:12]
		}
	}
	// Use the deepest .service in the path (skip user@ slices).
	ms := reService.FindAllStringSubmatch(cg, -1)
	for i := len(ms) - 1; i >= 0; i-- {
		if !strings.HasPrefix(ms[i][1], "user@") {
			unit = ms[i][1]
			break
		}
	}
	return
}

// Detail returns extended information about a single process.
func (ps *ProcSampler) Detail(pid int, memTotal uint64, bootTime time.Time) (*protocol.ProcessDetail, error) {
	r, err := ps.h.readProcStat(pid)
	if err != nil {
		return nil, fmt.Errorf("process %d not found", pid)
	}
	base := filepath.Join(ps.h.Proc, strconv.Itoa(pid))
	d := &protocol.ProcessDetail{Status: map[string]string{}}
	d.PID, d.PPID, d.Name, d.State, d.Threads = pid, r.ppid, r.name, r.state, r.threads
	d.RSS, d.VSize = r.rssPages*pageSize, r.vsize
	if memTotal > 0 {
		d.MemPct = round2(float64(d.RSS) / float64(memTotal) * 100)
	}
	if !bootTime.IsZero() {
		d.StartTime = bootTime.Add(time.Duration(r.startTicks) * time.Second / userHZ)
	}
	// CPU over a short window for this one process.
	t0 := r.utime + r.stime
	start := time.Now()
	time.Sleep(300 * time.Millisecond)
	if r2, err := ps.h.readProcStat(pid); err == nil {
		d.CPUPct = round2(float64(r2.utime+r2.stime-t0) / userHZ / time.Since(start).Seconds() * 100)
	}
	if b, err := os.ReadFile(filepath.Join(base, "cmdline")); err == nil {
		d.Cmdline = strings.TrimSpace(strings.ReplaceAll(string(b), "\x00", " "))
	}
	if b, err := os.ReadFile(filepath.Join(base, "status")); err == nil {
		kv := parseKV(b)
		for _, k := range []string{"State", "Uid", "Gid", "VmPeak", "VmRSS", "VmSwap", "RssAnon", "RssFile", "Threads", "voluntary_ctxt_switches", "nonvoluntary_ctxt_switches", "CapEff", "Seccomp", "NoNewPrivs"} {
			if v, ok := kv[k]; ok {
				d.Status[k] = v
			}
		}
		if f := strings.Fields(kv["Uid"]); len(f) > 0 {
			uid, _ := strconv.Atoi(f[0])
			d.User = ps.users.name(uid)
		}
	} else {
		d.Unavailable = append(d.Unavailable, "status")
	}
	if s, err := os.Readlink(filepath.Join(base, "exe")); err == nil {
		d.Exe = s
	} else {
		d.Unavailable = append(d.Unavailable, "exe (permission denied)")
	}
	if s, err := os.Readlink(filepath.Join(base, "cwd")); err == nil {
		d.Cwd = s
	}
	if fds, err := os.ReadDir(filepath.Join(base, "fd")); err == nil {
		d.OpenFDs = len(fds)
	} else {
		d.OpenFDs = -1
		d.Unavailable = append(d.Unavailable, "fd (permission denied)")
	}
	if b, err := os.ReadFile(filepath.Join(base, "cgroup")); err == nil {
		d.Cgroup = strings.TrimSpace(string(b))
		d.Unit, d.Container = cgroupOwner(d.Cgroup)
	}
	if v, err := readInt(filepath.Join(base, "oom_score")); err == nil {
		d.OOMScore = int(v)
	}
	if b, err := os.ReadFile(filepath.Join(base, "io")); err == nil {
		kv := parseKV(b)
		d.IOReadBytes, _ = strconv.ParseUint(kv["read_bytes"], 10, 64)
		d.IOWriteBytes, _ = strconv.ParseUint(kv["write_bytes"], 10, 64)
	} else {
		d.Unavailable = append(d.Unavailable, "io (permission denied)")
	}
	if b, err := os.ReadFile(filepath.Join(base, "limits")); err == nil {
		d.Limits = map[string]string{}
		sc := bufio.NewScanner(bytes.NewReader(b))
		for sc.Scan() {
			line := sc.Text()
			if len(line) > 26 && strings.HasPrefix(line, "Max ") {
				name := strings.TrimSpace(line[:26])
				rest := strings.Fields(line[26:])
				if len(rest) >= 2 && (name == "Max open files" || name == "Max processes" || name == "Max locked memory") {
					d.Limits[name] = rest[0] + " / " + rest[1]
				}
			}
		}
	}
	entries, _ := os.ReadDir(ps.h.Proc)
	for _, e := range entries {
		if !isPID(e.Name()) {
			continue
		}
		cp, _ := strconv.Atoi(e.Name())
		if c, err := ps.h.readProcStat(cp); err == nil && c.ppid == pid {
			d.Children = append(d.Children, cp)
		}
	}
	return d, nil
}

// ---------------------------------------------------------------- users

type userCache struct {
	h     *Host
	mu    sync.Mutex
	names map[int]string
	at    time.Time
}

func newUserCache(h *Host) *userCache { return &userCache{h: h} }

func (u *userCache) name(uid int) string {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.names == nil || time.Since(u.at) > time.Minute {
		u.names = map[int]string{}
		if users, err := u.h.Passwd(); err == nil {
			for _, x := range users {
				u.names[x.UID] = x.Name
			}
		}
		u.at = time.Now()
	}
	if n, ok := u.names[uid]; ok {
		return n
	}
	return strconv.Itoa(uid)
}

// PasswdEntry is a line of /etc/passwd.
type PasswdEntry struct {
	Name  string
	UID   int
	GID   int
	Home  string
	Shell string
}

func (h *Host) Passwd() ([]PasswdEntry, error) {
	data, err := os.ReadFile(h.etc("passwd"))
	if err != nil {
		return nil, err
	}
	var out []PasswdEntry
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		f := strings.Split(sc.Text(), ":")
		if len(f) < 7 {
			continue
		}
		uid, _ := strconv.Atoi(f[2])
		gid, _ := strconv.Atoi(f[3])
		out = append(out, PasswdEntry{Name: f[0], UID: uid, GID: gid, Home: f[5], Shell: f[6]})
	}
	return out, sc.Err()
}

// Groups returns group name -> members from /etc/group.
func (h *Host) Groups() (map[string][]string, map[int]string, error) {
	data, err := os.ReadFile(h.etc("group"))
	if err != nil {
		return nil, nil, err
	}
	members := map[string][]string{}
	byGID := map[int]string{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		f := strings.Split(sc.Text(), ":")
		if len(f) < 4 {
			continue
		}
		gid, _ := strconv.Atoi(f[2])
		byGID[gid] = f[0]
		if f[3] != "" {
			members[f[0]] = strings.Split(f[3], ",")
		} else {
			members[f[0]] = nil
		}
	}
	return members, byGID, sc.Err()
}
