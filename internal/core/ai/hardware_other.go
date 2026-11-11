//go:build !windows

package ai

import (
	"bufio"
	"context"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
)

func hideWindow(*exec.Cmd) {}

func platformHardware(context.Context) Hardware {
	h := Hardware{CPUThreads: runtime.NumCPU()}
	if f, err := os.Open("/proc/meminfo"); err == nil {
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			fl := strings.Fields(sc.Text())
			if len(fl) < 2 {
				continue
			}
			v, _ := strconv.ParseUint(fl[1], 10, 64)
			switch fl[0] {
			case "MemTotal:":
				h.RAMTotal = v * 1024
			case "MemAvailable:":
				h.RAMAvailable = v * 1024
			}
		}
		f.Close()
	}
	if b, err := os.ReadFile("/proc/cpuinfo"); err == nil {
		for _, l := range strings.Split(string(b), "\n") {
			if strings.HasPrefix(l, "model name") {
				if _, v, ok := strings.Cut(l, ":"); ok {
					h.CPUModel = strings.TrimSpace(v)
					break
				}
			}
		}
	}
	return h
}
