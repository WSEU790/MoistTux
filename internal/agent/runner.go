//go:build linux

package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/WSEU790/MoistTux/internal/protocol"
)

// safePATH is used for every child process; the agent never inherits PATH.
const safePATH = "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"

const maxOutput = 4 << 20 // 4 MiB per stream

type limitedBuffer struct {
	bytes.Buffer
	truncated bool
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if room := maxOutput - l.Len(); room < len(p) {
		if room > 0 {
			l.Buffer.Write(p[:room])
		}
		l.truncated = true
		return len(p), nil
	}
	return l.Buffer.Write(p)
}

// lookPath resolves a binary only inside safePATH.
func lookPath(name string) (string, error) {
	for _, dir := range strings.Split(safePATH, ":") {
		p := dir + "/" + name
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return p, nil
		}
	}
	return "", fmt.Errorf("%s: not installed", name)
}

// runCmd executes a fixed binary with discrete arguments (never a shell).
func runCmd(ctx context.Context, timeout time.Duration, env []string, name string, args ...string) protocol.CommandResult {
	res := protocol.CommandResult{Command: name + " " + strings.Join(args, " "), ExitCode: -1}
	path, err := lookPath(name)
	if err != nil {
		res.Stderr = err.Error()
		return res
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = append([]string{"PATH=" + safePATH, "LANG=C.UTF-8", "LC_ALL=C.UTF-8"}, env...)
	var so, se limitedBuffer
	cmd.Stdout, cmd.Stderr = &so, &se
	start := time.Now()
	err = cmd.Run()
	res.Duration = time.Since(start).Milliseconds()
	res.Stdout, res.Stderr = so.String(), se.String()
	if so.truncated {
		res.Stdout += "\n[output truncated]"
	}
	if err == nil {
		res.ExitCode = 0
		return res
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		res.ExitCode = ee.ExitCode()
	}
	if ctx.Err() == context.DeadlineExceeded {
		res.Stderr += fmt.Sprintf("\n[timed out after %s]", timeout)
	}
	return res
}

// helperEnvelope is the JSON printed by `nexus-agent helper ...`.
type helperEnvelope struct {
	OK     bool            `json:"ok"`
	Error  string          `json:"error,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
}

// Privileged runs an allowlisted helper operation with root rights. When the
// agent itself is root the helper runs in-process; otherwise it is invoked via
// `sudo -n` against the agent binary, which sudoers permits for this purpose only.
type Privileged struct {
	Self    string // absolute path of the agent binary
	Helper  string // dedicated helper binary (HelperBin) when installed
	IsRoot  bool
	Timeout time.Duration
}

// NewPrivileged prefers the dedicated helper installed next to the agent and
// falls back to `<agent> helper`, which older sudoers rules allowed.
func NewPrivileged(self string) *Privileged {
	p := &Privileged{Self: self, IsRoot: os.Geteuid() == 0}
	h := filepath.Join(filepath.Dir(self), filepath.Base(HelperBin))
	if fi, err := os.Stat(h); err == nil && fi.Mode().IsRegular() && h != self {
		p.Helper = h
	}
	return p
}

// IsHelperExecutable reports whether this process was started as the
// dedicated helper. That binary does nothing but run HelperMain, so the
// sudoers rule for it can allow any arguments without allowing `install`,
// `run` or any other subcommand as root.
func IsHelperExecutable() bool {
	exe, err := os.Executable()
	return err == nil && filepath.Base(exe) == filepath.Base(HelperBin)
}

// sudoDenied matches the refusal messages of the classic sudo and sudo-rs.
func sudoDenied(msg string) bool {
	for _, s := range []string{"a password is required", "not allowed", "I'm afraid I can't do that", "authentication failed", "interactive authentication is required", "not in the sudoers file"} {
		if strings.Contains(msg, s) {
			return true
		}
	}
	return false
}

func (p *Privileged) Do(ctx context.Context, timeout time.Duration, out any, op string, args ...string) error {
	if err := ValidateHelperArgs(op, args); err != nil {
		return err
	}
	var raw []byte
	if p.IsRoot {
		res, err := ExecHelper(ctx, op, args)
		if err != nil {
			return err
		}
		raw, err = json.Marshal(res)
		if err != nil {
			return err
		}
	} else {
		argv := append([]string{"-n", p.Self, "helper", op}, args...)
		if p.Helper != "" {
			argv = append([]string{"-n", p.Helper, op}, args...)
		}
		r := runCmd(ctx, timeout, nil, "sudo", argv...)
		var env helperEnvelope
		if err := json.Unmarshal([]byte(strings.TrimSpace(r.Stdout)), &env); err != nil {
			msg := strings.TrimSpace(r.Stderr)
			if sudoDenied(msg) {
				return fmt.Errorf("privileged helper not authorised by sudoers (re-run `nexus-agent install` as root): %s", msg)
			}
			return fmt.Errorf("privileged helper failed (exit %d): %s", r.ExitCode, firstLine(msg))
		}
		if !env.OK {
			return errors.New(env.Error)
		}
		raw = env.Result
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(raw, out)
}

// Check verifies the helper can be reached (used by /v1/health).
func (p *Privileged) Check(ctx context.Context) error {
	var s string
	return p.Do(ctx, 10*time.Second, &s, "ping")
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
