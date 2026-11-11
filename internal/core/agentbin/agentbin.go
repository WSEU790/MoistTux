// Package agentbin provides the nexus-agent Linux binaries that NEXUS uploads
// when it deploys an agent over SSH. Release builds embed them (gzip) via
// `make embed-agents`; otherwise NEXUS looks for them next to its executable
// (linux-amd64/nexus-agent, as in the release zip) or in ./dist.
package agentbin

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

//go:embed all:bin
var bins embed.FS

// Binary is an agent executable ready to upload.
type Binary struct {
	Arch   string
	Data   []byte
	SHA256 string
	Source string // embedded | file path
}

// Supported architectures (Linux kernel `uname -m` → Go arch).
var archMap = map[string]string{"x86_64": "amd64", "amd64": "amd64", "aarch64": "arm64", "arm64": "arm64"}

// NormalizeArch converts `uname -m` output to a supported Go arch.
func NormalizeArch(machine string) (string, error) {
	if a, ok := archMap[machine]; ok {
		return a, nil
	}
	return "", fmt.Errorf("unsupported CPU architecture %q (NEXUS ships agents for x86_64 and aarch64)", machine)
}

func Get(arch string) (*Binary, error) {
	if arch != "amd64" && arch != "arm64" {
		return nil, fmt.Errorf("unsupported architecture %q", arch)
	}
	if gz, err := bins.ReadFile("bin/linux-" + arch + "/nexus-agent.gz"); err == nil {
		zr, err := gzip.NewReader(bytes.NewReader(gz))
		if err != nil {
			return nil, err
		}
		data, err := io.ReadAll(zr)
		if err != nil {
			return nil, fmt.Errorf("embedded agent is corrupt: %w", err)
		}
		return mk(arch, data, "embedded"), nil
	}
	var candidates []string
	if exe, err := os.Executable(); err == nil {
		dir := filepath.Dir(exe)
		candidates = append(candidates, filepath.Join(dir, "linux-"+arch, "nexus-agent"), filepath.Join(dir, "agents", "linux-"+arch, "nexus-agent"))
	}
	candidates = append(candidates, filepath.Join("dist", "linux-"+arch, "nexus-agent"))
	for _, p := range candidates {
		if data, err := os.ReadFile(p); err == nil && len(data) > 1<<20 && bytes.HasPrefix(data, []byte("\x7fELF")) {
			return mk(arch, data, p), nil
		}
	}
	return nil, fmt.Errorf("this NEXUS build does not include the linux-%s agent; place nexus-agent in a linux-%s folder next to NEXUS.exe or rebuild with `make all`", arch, arch)
}

// Available lists which architectures can be deployed by this build.
func Available() map[string]string {
	out := map[string]string{}
	for _, a := range []string{"amd64", "arm64"} {
		if b, err := Get(a); err == nil {
			out[a] = b.Source
		}
	}
	return out
}

func mk(arch string, data []byte, src string) *Binary {
	sum := sha256.Sum256(data)
	return &Binary{Arch: arch, Data: data, SHA256: hex.EncodeToString(sum[:]), Source: src}
}
