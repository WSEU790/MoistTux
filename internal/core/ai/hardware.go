package ai

import (
	"context"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// nvidiaGPUs queries nvidia-smi when installed (Windows and Linux).
func nvidiaGPUs(ctx context.Context) []GPU {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	name := "nvidia-smi"
	if runtime.GOOS == "windows" {
		name = "nvidia-smi.exe"
	}
	p, err := exec.LookPath(name)
	if err != nil {
		return nil
	}
	cmd := exec.CommandContext(ctx, p, "--query-gpu=name,memory.total", "--format=csv,noheader,nounits")
	hideWindow(cmd)
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var gpus []GPU
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		f := strings.Split(line, ",")
		if len(f) < 2 {
			continue
		}
		mib, _ := strconv.ParseUint(strings.TrimSpace(f[1]), 10, 64)
		gpus = append(gpus, GPU{Name: strings.TrimSpace(f[0]), VRAMBytes: mib << 20, Source: "nvidia-smi"})
	}
	return gpus
}

// DetectHardware describes the machine running NEXUS (and, when Ollama is
// local, the machine running inference).
func DetectHardware(ctx context.Context, ollamaLocal bool) Hardware {
	h := platformHardware(ctx)
	h.OllamaLocal = ollamaLocal
	if nv := nvidiaGPUs(ctx); len(nv) > 0 {
		// nvidia-smi is authoritative for NVIDIA VRAM; keep other adapters.
		var rest []GPU
		for _, g := range h.GPUs {
			if !strings.Contains(strings.ToLower(g.Name), "nvidia") {
				rest = append(rest, g)
			}
		}
		h.GPUs = append(nv, rest...)
	}
	if !ollamaLocal {
		h.Notes = append(h.Notes, "Ollama runs on another machine; these figures describe this workstation, not the inference host.")
	}
	return h
}
