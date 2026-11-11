//go:build linux

package collect

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/WSEU790/MoistTux/internal/protocol"
)

// ListeningSockets parses /proc/net/{tcp,tcp6,udp,udp6} and maps socket
// inodes to owning processes. Without root, processes owned by other users
// cannot be resolved; the second return value reports that.
func (h *Host) ListeningSockets() ([]protocol.Socket, bool) {
	type raw struct {
		proto string
		addr  string
		port  int
		state string
		inode string
	}
	var socks []raw
	for _, proto := range []string{"tcp", "tcp6", "udp", "udp6"} {
		data, err := os.ReadFile(h.proc("net", proto))
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
			f := strings.Fields(sc.Text())
			if len(f) < 10 {
				continue
			}
			st := f[3]
			isTCP := strings.HasPrefix(proto, "tcp")
			if isTCP && st != "0A" { // LISTEN
				continue
			}
			if !isTCP && st != "07" { // UDP "listening" (unconnected)
				continue
			}
			ip, port, ok := parseHexAddr(f[1])
			if !ok {
				continue
			}
			state := "LISTEN"
			if !isTCP {
				state = "UNCONN"
			}
			socks = append(socks, raw{proto, ip, port, state, f[9]})
		}
	}
	// inode -> pid map
	owners := map[string]int{}
	complete := true
	entries, _ := os.ReadDir(h.Proc)
	for _, e := range entries {
		if !isPID(e.Name()) {
			continue
		}
		fdDir := h.proc(e.Name(), "fd")
		fds, err := os.ReadDir(fdDir)
		if err != nil {
			complete = false
			continue
		}
		pid, _ := strconv.Atoi(e.Name())
		for _, fd := range fds {
			l, err := os.Readlink(filepath.Join(fdDir, fd.Name()))
			if err == nil && strings.HasPrefix(l, "socket:[") {
				owners[l[8:len(l)-1]] = pid
			}
		}
	}
	users := newUserCache(h)
	seen := map[string]bool{}
	var out []protocol.Socket
	for _, s := range socks {
		key := s.proto + s.addr + strconv.Itoa(s.port)
		if seen[key] {
			continue
		}
		seen[key] = true
		so := protocol.Socket{Proto: s.proto, LocalAddr: s.addr, LocalPort: s.port, State: s.state, Scope: bindScope(s.addr)}
		if pid, ok := owners[s.inode]; ok {
			so.PID = pid
			if c, err := readTrim(h.proc(strconv.Itoa(pid), "comm")); err == nil {
				so.Process = c
			}
			if b, err := os.ReadFile(h.proc(strconv.Itoa(pid), "status")); err == nil {
				if f := strings.Fields(parseKV(b)["Uid"]); len(f) > 0 {
					uid, _ := strconv.Atoi(f[0])
					so.User = users.name(uid)
				}
			}
		}
		out = append(out, so)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].LocalPort == out[j].LocalPort {
			return out[i].Proto < out[j].Proto
		}
		return out[i].LocalPort < out[j].LocalPort
	})
	return out, complete
}

func bindScope(addr string) string {
	ip := net.ParseIP(addr)
	switch {
	case ip == nil:
		return "specific-address"
	case ip.IsLoopback():
		return "loopback"
	case ip.IsUnspecified():
		return "all-interfaces"
	default:
		return "specific-address"
	}
}

// parseHexAddr decodes "0100007F:0035" (IPv4) or the 32-hex-digit IPv6 form.
func parseHexAddr(s string) (string, int, bool) {
	a, p, ok := strings.Cut(s, ":")
	if !ok {
		return "", 0, false
	}
	port, err := strconv.ParseUint(p, 16, 16)
	if err != nil {
		return "", 0, false
	}
	b, err := hex.DecodeString(a)
	if err != nil {
		return "", 0, false
	}
	var ip net.IP
	switch len(b) {
	case 4:
		ip = net.IPv4(b[3], b[2], b[1], b[0])
	case 16:
		ip = make(net.IP, 16)
		for i := 0; i < 4; i++ {
			w := binary.LittleEndian.Uint32(b[i*4:])
			binary.BigEndian.PutUint32(ip[i*4:], w)
		}
	default:
		return "", 0, false
	}
	return ip.String(), int(port), true
}

// Routes parses /proc/net/route (IPv4) and /proc/net/ipv6_route.
func (h *Host) Routes() []protocol.Route {
	var out []protocol.Route
	if data, err := os.ReadFile(h.proc("net/route")); err == nil {
		sc := bufio.NewScanner(bytes.NewReader(data))
		first := true
		for sc.Scan() {
			if first {
				first = false
				continue
			}
			f := strings.Fields(sc.Text())
			if len(f) < 8 {
				continue
			}
			dst := hexIPv4(f[1])
			gw := hexIPv4(f[2])
			mask := hexIPv4(f[7])
			metric, _ := strconv.Atoi(f[6])
			ones, _ := net.IPMask(net.ParseIP(mask).To4()).Size()
			r := protocol.Route{Destination: dst + "/" + strconv.Itoa(ones), Iface: f[0], Metric: metric, Default: dst == "0.0.0.0" && ones == 0}
			if gw != "0.0.0.0" {
				r.Gateway = gw
			}
			out = append(out, r)
		}
	}
	if data, err := os.ReadFile(h.proc("net/ipv6_route")); err == nil {
		sc := bufio.NewScanner(bytes.NewReader(data))
		for sc.Scan() {
			f := strings.Fields(sc.Text())
			if len(f) < 10 || f[9] == "lo" {
				continue
			}
			dst := hexIPv6(f[0])
			plen, _ := strconv.ParseUint(f[1], 16, 8)
			gw := hexIPv6(f[4])
			metric, _ := strconv.ParseInt(f[5], 16, 64)
			if strings.HasPrefix(dst, "ff00") || strings.HasPrefix(dst, "fe80") && plen == 64 && gw == "::" {
				continue
			}
			r := protocol.Route{Destination: dst + "/" + strconv.Itoa(int(plen)), Iface: f[9], Metric: int(metric), Default: dst == "::" && plen == 0}
			if gw != "::" {
				r.Gateway = gw
			}
			out = append(out, r)
		}
	}
	return out
}

func hexIPv4(s string) string {
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != 4 {
		return ""
	}
	return net.IPv4(b[3], b[2], b[1], b[0]).String()
}

func hexIPv6(s string) string {
	b, err := hex.DecodeString(s)
	if err != nil || len(b) != 16 {
		return ""
	}
	return net.IP(b).String()
}

// DNS reads resolv.conf. When systemd-resolved's stub (127.0.0.53) is in use,
// the upstream servers are read from /run/systemd/resolve/resolv.conf.
func (h *Host) DNS() (servers, search []string) {
	parse := func(p string) ([]string, []string) {
		data, err := os.ReadFile(p)
		if err != nil {
			return nil, nil
		}
		var sv, se []string
		for _, l := range strings.Split(string(data), "\n") {
			f := strings.Fields(l)
			if len(f) >= 2 && f[0] == "nameserver" {
				sv = append(sv, f[1])
			}
			if len(f) >= 2 && (f[0] == "search" || f[0] == "domain") {
				se = append(se, f[1:]...)
			}
		}
		return sv, se
	}
	servers, search = parse(h.etc("resolv.conf"))
	if len(servers) == 1 && servers[0] == "127.0.0.53" {
		if up, s2 := parse(filepath.Join(h.Run, "systemd/resolve/resolv.conf")); len(up) > 0 {
			servers = append(servers, up...)
			if len(search) == 0 {
				search = s2
			}
		}
	}
	return
}
