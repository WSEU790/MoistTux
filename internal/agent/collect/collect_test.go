//go:build linux

package collect

import (
	"os"
	"path/filepath"
	"testing"
)

func fixture(t *testing.T, files map[string]string) *Host {
	t.Helper()
	root := t.TempDir()
	for p, c := range files {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return &Host{Proc: filepath.Join(root, "proc"), Sys: filepath.Join(root, "sys"), Etc: filepath.Join(root, "etc"), Run: filepath.Join(root, "run"), Var: filepath.Join(root, "var")}
}

func TestMemInfo(t *testing.T) {
	h := fixture(t, map[string]string{"proc/meminfo": "MemTotal:       16000000 kB\nMemFree:  1000000 kB\nMemAvailable:    4000000 kB\nBuffers: 100 kB\nCached: 2000 kB\nSReclaimable: 1000 kB\nSwapTotal: 2000000 kB\nSwapFree: 1500000 kB\n"})
	m, err := h.ReadMem()
	if err != nil {
		t.Fatal(err)
	}
	if m.Total != 16000000*1024 || m.Available != 4000000*1024 {
		t.Fatalf("totals wrong: %+v", m)
	}
	if m.UsedPct != 75 {
		t.Fatalf("used pct = %v, want 75", m.UsedPct)
	}
	if m.SwapPct != 25 {
		t.Fatalf("swap pct = %v, want 25", m.SwapPct)
	}
}

func TestCPUDelta(t *testing.T) {
	prev := &StatSnapshot{All: CPUTimes{User: 100, System: 100, Idle: 800}}
	cur := &StatSnapshot{All: CPUTimes{User: 150, System: 150, Idle: 900, IOWait: 0}}
	got := CPUDelta(prev, cur)
	// 200 ticks elapsed, 100 idle → 50 % busy.
	if got.UsagePct != 50 {
		t.Fatalf("usage = %v, want 50", got.UsagePct)
	}
	if CPUDelta(nil, cur).UsagePct != 0 {
		t.Fatal("first sample must not invent a value")
	}
}

func TestNetDevAndDelta(t *testing.T) {
	h := fixture(t, map[string]string{"proc/net/dev": "Inter-|   Receive |  Transmit\n face |bytes packets errs drop fifo frame compressed multicast|bytes packets errs drop fifo colls carrier compressed\n  eth0: 1000 10 0 0 0 0 0 0 2000 20 0 0 0 0 0 0\n    lo: 50 1 0 0 0 0 0 0 50 1 0 0 0 0 0 0\n"})
	a, err := h.ReadNetDev()
	if err != nil {
		t.Fatal(err)
	}
	b := map[string]NetCounters{"eth0": {RxBytes: 3000, RxPackets: 30, TxBytes: 2500, TxPackets: 25}}
	rates := NetDelta(a, b, 2)
	if len(rates) != 1 || rates[0].RxBps != 1000 || rates[0].TxBps != 250 {
		t.Fatalf("rates = %+v", rates)
	}
	// Counter reset (reboot, interface re-created) must not produce a negative rate.
	if len(NetDelta(b, a, 2)) != 0 {
		t.Fatal("counter reset produced a rate")
	}
}

func TestHexAddrAndScope(t *testing.T) {
	ip, port, ok := parseHexAddr("0100007F:0035")
	if !ok || ip != "127.0.0.1" || port != 53 {
		t.Fatalf("got %s:%d %v", ip, port, ok)
	}
	ip6, port6, ok := parseHexAddr("00000000000000000000000000000000:1F90")
	if !ok || ip6 != "::" || port6 != 8080 {
		t.Fatalf("got %s:%d %v", ip6, port6, ok)
	}
	if bindScope("127.0.0.1") != "loopback" || bindScope("0.0.0.0") != "all-interfaces" || bindScope("10.0.0.5") != "specific-address" {
		t.Fatal("bind scope classification wrong")
	}
}

func TestMountUnescape(t *testing.T) {
	if got := unescapeMount(`/mnt/my\040disk`); got != "/mnt/my disk" {
		t.Fatalf("got %q", got)
	}
}

func TestCgroupOwner(t *testing.T) {
	u, c := cgroupOwner("0::/system.slice/docker-0123456789abcdef0123456789abcdef.scope\n")
	if c != "0123456789ab" || u != "" {
		t.Fatalf("unit=%q container=%q", u, c)
	}
	u, _ = cgroupOwner("0::/system.slice/nginx.service\n")
	if u != "nginx.service" {
		t.Fatalf("unit=%q", u)
	}
}

// The real host must yield real numbers: this is the agent's ground truth.
func TestRealHost(t *testing.T) {
	h := Default()
	m, err := h.ReadMem()
	if err != nil || m.Total == 0 {
		t.Fatalf("meminfo: %v %+v", err, m)
	}
	st, err := h.ReadStat()
	if err != nil || len(st.Cores) == 0 || st.BootTime == 0 {
		t.Fatalf("stat: %v", err)
	}
	if len(h.Filesystems(false)) == 0 {
		t.Log("no data filesystems detected (container overlay root?)")
	}
	si := h.SystemInfo()
	if si.Kernel == "" || si.Arch == "" || si.CPUThreads == 0 {
		t.Fatalf("system info incomplete: %+v", si)
	}
}
