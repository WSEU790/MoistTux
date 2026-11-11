// Package collect reads telemetry directly from the Linux kernel interfaces
// (/proc, /sys) and well-known configuration files. Paths are rooted so the
// readers can be exercised against fixture trees in tests.
package collect

import (
	"bufio"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Host locates the kernel and configuration trees.
type Host struct {
	Proc string // normally /proc
	Sys  string // normally /sys
	Etc  string // normally /etc
	Run  string // normally /run
	Var  string // normally /var
}

// Default returns the real host layout.
func Default() *Host {
	return &Host{Proc: "/proc", Sys: "/sys", Etc: "/etc", Run: "/run", Var: "/var"}
}

func (h *Host) proc(p ...string) string { return filepath.Join(append([]string{h.Proc}, p...)...) }
func (h *Host) sys(p ...string) string  { return filepath.Join(append([]string{h.Sys}, p...)...) }
func (h *Host) etc(p ...string) string  { return filepath.Join(append([]string{h.Etc}, p...)...) }

func readTrim(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func readInt(path string) (int64, error) {
	s, err := readTrim(path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(s, 10, 64)
}

// parseKV parses "Key: value unit" files such as /proc/meminfo.
func parseKV(data []byte) map[string]string {
	out := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		i := strings.IndexByte(line, ':')
		if i <= 0 {
			continue
		}
		out[strings.TrimSpace(line[:i])] = strings.TrimSpace(line[i+1:])
	}
	return out
}

func parseKiB(v string) uint64 {
	f := strings.Fields(v)
	if len(f) == 0 {
		return 0
	}
	n, _ := strconv.ParseUint(f[0], 10, 64)
	if len(f) > 1 && strings.EqualFold(f[1], "kB") {
		return n * 1024
	}
	return n
}

// OSRelease parses /etc/os-release (falling back to /usr/lib/os-release).
func (h *Host) OSRelease() (map[string]string, error) {
	data, err := os.ReadFile(h.etc("os-release"))
	if err != nil {
		data, err = os.ReadFile(filepath.Join(filepath.Dir(h.Etc), "usr/lib/os-release"))
		if err != nil {
			return nil, err
		}
	}
	out := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		if uq, err := strconv.Unquote(v); err == nil {
			v = uq
		} else {
			v = strings.Trim(v, `"'`)
		}
		out[k] = v
	}
	return out, sc.Err()
}

// ErrUnavailable marks a source that does not exist on this host.
var ErrUnavailable = errors.New("source unavailable on this host")
