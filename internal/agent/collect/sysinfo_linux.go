//go:build linux

package collect

import (
	"bufio"
	"bytes"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/WSEU790/MoistTux/internal/protocol"
)

func utsString(a [65]int8) string {
	b := make([]byte, 0, 65)
	for _, c := range a {
		if c == 0 {
			break
		}
		b = append(b, byte(c))
	}
	return string(b)
}

// SystemInfo gathers static host facts. Fields that cannot be determined are
// left empty rather than guessed.
func (h *Host) SystemInfo() protocol.SystemInfo {
	si := protocol.SystemInfo{}
	si.Hostname, _ = os.Hostname()
	if osr, err := h.OSRelease(); err == nil {
		si.OSName = osr["PRETTY_NAME"]
		si.OSID = osr["ID"]
		si.OSVersion = osr["VERSION_ID"]
		si.OSCodename = osr["VERSION_CODENAME"]
		if l := osr["ID_LIKE"]; l != "" {
			si.OSIDLike = strings.Fields(l)
		}
	}
	var uts syscall.Utsname
	if err := syscall.Uname(&uts); err == nil {
		si.Kernel = utsString(uts.Release)
		si.Arch = utsString(uts.Machine)
	}
	si.CPUModel, si.CPUCores, si.CPUThreads = h.cpuInfo()
	if m, err := h.ReadMem(); err == nil {
		si.MemTotal, si.SwapTotal = m.Total, m.SwapTotal
	}
	if st, err := h.ReadStat(); err == nil {
		si.BootTime = BootTimeFrom(st)
	}
	si.Virtualization = h.virtualization()
	si.MachineID, _ = readTrim(h.etc("machine-id"))
	si.Timezone = h.timezone()
	si.Interfaces = h.Interfaces()
	si.BlockDevices = h.BlockDevices()
	si.PackageManager = DetectPackageManager()
	si.InitSystem = h.initSystem()
	if _, err := os.Stat("/var/run/docker.sock"); err == nil {
		si.DockerAvailable = true
	}
	si.LastUpdate = h.lastPackageUpdate()
	si.RebootRequired, si.RebootPkgs = h.RebootRequired()
	return si
}

func (h *Host) cpuInfo() (model string, cores, threads int) {
	data, err := os.ReadFile(h.proc("cpuinfo"))
	if err != nil {
		return "", 0, 0
	}
	type key struct{ phys, core string }
	coreSet := map[key]bool{}
	var phys, core string
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			if strings.TrimSpace(line) == "" && core != "" {
				coreSet[key{phys, core}] = true
				phys, core = "", ""
			}
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		switch k {
		case "processor":
			threads++
		case "model name", "Model", "cpu model":
			if model == "" {
				model = v
			}
		case "physical id":
			phys = v
		case "core id":
			core = v
		}
	}
	if core != "" {
		coreSet[key{phys, core}] = true
	}
	cores = len(coreSet)
	if cores == 0 {
		cores = threads
	}
	return
}

func (h *Host) virtualization() string {
	if p, err := exec.LookPath("systemd-detect-virt"); err == nil {
		out, _ := exec.Command(p).Output()
		v := strings.TrimSpace(string(out))
		if v != "" && v != "none" {
			return v
		}
		if v == "none" {
			return "none"
		}
	}
	vendor, _ := readTrim(h.sys("class/dmi/id/sys_vendor"))
	product, _ := readTrim(h.sys("class/dmi/id/product_name"))
	s := strings.ToLower(vendor + " " + product)
	switch {
	case strings.Contains(s, "kvm"), strings.Contains(s, "qemu"):
		return "kvm"
	case strings.Contains(s, "vmware"):
		return "vmware"
	case strings.Contains(s, "virtualbox"):
		return "oracle"
	case strings.Contains(s, "microsoft"):
		return "microsoft"
	case strings.Contains(s, "xen"):
		return "xen"
	case strings.Contains(s, "amazon ec2"):
		return "amazon"
	case strings.Contains(s, "google"):
		return "google"
	}
	if b, err := os.ReadFile(h.proc("cpuinfo")); err == nil && bytes.Contains(b, []byte(" hypervisor")) {
		return "vm (unknown hypervisor)"
	}
	return ""
}

func (h *Host) timezone() string {
	if s, err := readTrim(h.etc("timezone")); err == nil && s != "" {
		return s
	}
	if l, err := os.Readlink(h.etc("localtime")); err == nil {
		if i := strings.Index(l, "zoneinfo/"); i >= 0 {
			return l[i+len("zoneinfo/"):]
		}
	}
	return ""
}

func (h *Host) initSystem() string {
	comm, _ := readTrim(h.proc("1/comm"))
	if comm == "systemd" {
		return "systemd"
	}
	if _, err := os.Stat(filepath.Join(h.Run, "systemd/system")); err == nil {
		return "systemd"
	}
	if comm == "" {
		return "unknown"
	}
	return comm
}

// Interfaces lists network interfaces with addresses and link state.
func (h *Host) Interfaces() []protocol.NetIface {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []protocol.NetIface
	for _, i := range ifs {
		ni := protocol.NetIface{Name: i.Name, MAC: i.HardwareAddr.String(), MTU: i.MTU, Loopback: i.Flags&net.FlagLoopback != 0}
		ni.OperState, _ = readTrim(h.sys("class/net", i.Name, "operstate"))
		if sp, err := readInt(h.sys("class/net", i.Name, "speed")); err == nil && sp > 0 {
			ni.SpeedMbps = int(sp)
		}
		if l, err := filepath.EvalSymlinks(h.sys("class/net", i.Name)); err == nil && strings.Contains(l, "/devices/virtual/") {
			ni.Virtual = true
		}
		if addrs, err := i.Addrs(); err == nil {
			for _, a := range addrs {
				ni.Addrs = append(ni.Addrs, a.String())
			}
		}
		out = append(out, ni)
	}
	return out
}

// BlockDevices reads /sys/block and maps partitions to mount points.
func (h *Host) BlockDevices() []protocol.BlockDevice {
	mounts, _ := h.Mounts()
	byDev := map[string][]Mount{}
	for _, m := range mounts {
		if strings.HasPrefix(m.Device, "/dev/") {
			real := m.Device
			if r, err := filepath.EvalSymlinks(m.Device); err == nil {
				real = r
			}
			byDev[filepath.Base(real)] = append(byDev[filepath.Base(real)], m)
		}
	}
	entries, err := os.ReadDir(h.sys("block"))
	if err != nil {
		return nil
	}
	var out []protocol.BlockDevice
	for _, e := range entries {
		name := e.Name()
		if strings.HasPrefix(name, "loop") || strings.HasPrefix(name, "ram") {
			continue
		}
		dir := h.sys("block", name)
		bd := h.blockDev(dir, name, byDev)
		if bd.Size == 0 {
			continue
		}
		bd.Type = "disk"
		if strings.HasPrefix(name, "dm-") {
			bd.Type = "dm"
			if n, err := readTrim(filepath.Join(dir, "dm/name")); err == nil {
				bd.Name = "mapper/" + n
			}
		} else if strings.HasPrefix(name, "md") {
			bd.Type = "raid"
		}
		bd.Model, _ = readTrim(filepath.Join(dir, "device/model"))
		bd.Serial, _ = readTrim(filepath.Join(dir, "device/serial"))
		rot, _ := readTrim(filepath.Join(dir, "queue/rotational"))
		bd.Rotational = rot == "1"
		parts, _ := os.ReadDir(dir)
		for _, p := range parts {
			if strings.HasPrefix(p.Name(), name) {
				if _, err := os.Stat(filepath.Join(dir, p.Name(), "partition")); err == nil {
					c := h.blockDev(filepath.Join(dir, p.Name()), p.Name(), byDev)
					c.Type = "part"
					bd.Children = append(bd.Children, c)
				}
			}
		}
		sort.Slice(bd.Children, func(i, j int) bool { return bd.Children[i].Name < bd.Children[j].Name })
		out = append(out, bd)
	}
	return out
}

func (h *Host) blockDev(dir, name string, byDev map[string][]Mount) protocol.BlockDevice {
	bd := protocol.BlockDevice{Name: name}
	if sz, err := readInt(filepath.Join(dir, "size")); err == nil {
		bd.Size = uint64(sz) * 512
	}
	for _, m := range byDev[name] {
		bd.Mounts = append(bd.Mounts, m.Mount)
		bd.FSType = m.FSType
	}
	return bd
}

// DetectPackageManager returns the first supported package manager found.
func DetectPackageManager() string {
	for _, pm := range []string{"apt-get", "dnf", "yum", "zypper", "pacman", "apk"} {
		if _, err := exec.LookPath(pm); err == nil {
			if pm == "apt-get" {
				return "apt"
			}
			return pm
		}
	}
	return ""
}

// RebootRequired checks the Debian/Ubuntu reboot-required marker files.
func (h *Host) RebootRequired() (bool, []string) {
	for _, base := range []string{h.Run, filepath.Join(h.Var, "run")} {
		if _, err := os.Stat(filepath.Join(base, "reboot-required")); err == nil {
			var pkgs []string
			if b, err := os.ReadFile(filepath.Join(base, "reboot-required.pkgs")); err == nil {
				for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
					if l = strings.TrimSpace(l); l != "" {
						pkgs = append(pkgs, l)
					}
				}
			}
			return true, pkgs
		}
	}
	return false, nil
}

// lastPackageUpdate returns the start time of the most recent apt transaction
// that upgraded or installed packages.
func (h *Host) lastPackageUpdate() *time.Time {
	hist, err := h.AptHistory(1)
	if err != nil || len(hist) == 0 {
		return nil
	}
	t := hist[0].Start
	return &t
}

// AptHistory parses /var/log/apt/history.log, newest first.
func (h *Host) AptHistory(limit int) ([]protocol.PackageHistoryEntry, error) {
	data, err := os.ReadFile(filepath.Join(h.Var, "log/apt/history.log"))
	if err != nil {
		return nil, err
	}
	var out []protocol.PackageHistoryEntry
	var cur *protocol.PackageHistoryEntry
	split := func(v string) []string {
		var r []string
		depth := 0
		start := 0
		for i, c := range v {
			switch c {
			case '(':
				depth++
			case ')':
				depth--
			case ',':
				if depth == 0 {
					r = append(r, strings.TrimSpace(v[start:i]))
					start = i + 1
				}
			}
		}
		if s := strings.TrimSpace(v[start:]); s != "" {
			r = append(r, s)
		}
		return r
	}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), ": ")
		if !ok {
			continue
		}
		switch k {
		case "Start-Date":
			t, err := time.ParseInLocation("2006-01-02  15:04:05", strings.TrimSpace(v), time.Local)
			if err != nil {
				t, _ = time.ParseInLocation("2006-01-02 15:04:05", strings.Join(strings.Fields(v), " "), time.Local)
			}
			out = append(out, protocol.PackageHistoryEntry{Start: t})
			cur = &out[len(out)-1]
		case "Commandline":
			if cur != nil {
				cur.CommandLine = v
			}
		case "Requested-By":
			if cur != nil {
				cur.Requested = v
			}
		case "Install":
			if cur != nil {
				cur.Install = split(v)
			}
		case "Upgrade":
			if cur != nil {
				cur.Upgrade = split(v)
			}
		case "Remove", "Purge":
			if cur != nil {
				cur.Remove = append(cur.Remove, split(v)...)
			}
		case "Error":
			if cur != nil {
				cur.Error = v
			}
		}
	}
	// Newest first.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// AgentRSS returns this process's resident set size.
func (h *Host) AgentRSS() uint64 {
	b, err := os.ReadFile(h.proc("self/status"))
	if err != nil {
		return 0
	}
	return parseKiB(parseKV(b)["VmRSS"])
}

// Atoi is a small exported helper used by sibling packages.
func Atoi(s string) int { v, _ := strconv.Atoi(strings.TrimSpace(s)); return v }
