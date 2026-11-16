//go:build linux

package agent

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	InstallBin = "/usr/local/lib/nexus/nexus-agent"
	// HelperBin is the same program under a name that only ever runs the
	// validated privileged helper (see IsHelperExecutable). sudoers allows
	// this one path with no argument wildcard, which both the classic sudo
	// and sudo-rs (the default sudo from Ubuntu 25.10) accept.
	HelperBin     = "/usr/local/lib/nexus/nexus-helper"
	DefaultState  = "/var/lib/nexus-agent"
	UnitPath      = "/etc/systemd/system/nexus-agent.service"
	SudoersPath   = "/etc/sudoers.d/nexus-agent"
	ServiceUser   = "nexus"
	DefaultListen = ":7443"
)

type InstallOptions struct {
	Listen   string
	Docker   bool // add the service user to the docker group (root-equivalent)
	Interval time.Duration
	Out      io.Writer
	// ShellSigner trusts a NEXUS core's public key for person-approved shell
	// commands (written to ShellSignersPath, root-owned).
	ShellSigner      string
	ShellSignerLabel string
	// Upgrade: restart without waiting (the caller runs inside the service
	// being restarted) and do not issue a new enrollment code.
	Upgrade bool
}

func step(out io.Writer, format string, a ...any) { fmt.Fprintf(out, "  • "+format+"\n", a...) }

// Install sets up the agent as a least-privileged systemd service.
func Install(opts InstallOptions) error {
	out := opts.Out
	if os.Geteuid() != 0 {
		return fmt.Errorf("install must be run as root")
	}
	if opts.Listen == "" {
		opts.Listen = DefaultListen
	}
	if opts.Interval == 0 {
		opts.Interval = 5 * time.Second
	}
	ctx := context.Background()
	if _, err := lookPath("systemctl"); err != nil {
		return fmt.Errorf("systemd is required for `install`; run `nexus-agent run` under your init system instead")
	}
	fmt.Fprintln(out, "Installing NEXUS agent "+Version)

	// 1. Binary, root-owned so the service user cannot replace what sudo runs.
	self, err := os.Executable()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(InstallBin), 0o755); err != nil {
		return err
	}
	if real, _ := filepath.EvalSymlinks(self); real != InstallBin {
		if err := copyFile(self, InstallBin, 0o755); err != nil {
			return fmt.Errorf("copy binary: %w", err)
		}
	}
	_ = os.Chown(InstallBin, 0, 0)
	step(out, "binary installed at %s (root:root 0755)", InstallBin)
	if err := installHelper(); err != nil {
		return fmt.Errorf("install helper: %w", err)
	}
	step(out, "privileged helper at %s (root:root 0755)", HelperBin)

	// 2. Service user.
	if _, err := user.Lookup(ServiceUser); err != nil {
		r := runCmd(ctx, time.Minute, nil, "useradd", "--system", "--no-create-home", "--home-dir", DefaultState, "--shell", "/usr/sbin/nologin", ServiceUser)
		if r.ExitCode != 0 {
			return fmt.Errorf("useradd: %s", strings.TrimSpace(r.Stderr))
		}
		step(out, "created system user %q", ServiceUser)
	} else {
		step(out, "system user %q already exists", ServiceUser)
	}
	groups := []string{"systemd-journal", "adm"}
	if opts.Docker {
		groups = append(groups, "docker")
	}
	for _, g := range groups {
		if _, err := user.LookupGroup(g); err != nil {
			continue
		}
		if r := runCmd(ctx, time.Minute, nil, "usermod", "-aG", g, ServiceUser); r.ExitCode == 0 {
			step(out, "added %s to group %s", ServiceUser, g)
		}
	}
	if opts.Docker {
		step(out, "WARNING: membership of the docker group is equivalent to root on this host")
	}

	// 3. State directory and identity.
	u, err := user.Lookup(ServiceUser)
	if err != nil {
		return err
	}
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)
	st, err := LoadOrCreateState(DefaultState)
	if err != nil {
		return err
	}
	if err := chownTree(DefaultState, uid, gid); err != nil {
		return err
	}
	_ = os.Chmod(DefaultState, 0o700)
	step(out, "state directory %s (0700, %s)", DefaultState, ServiceUser)

	// 4. Sudoers: exactly one command, the root-owned helper, which accepts
	// only allowlisted operations and re-validates every argument.
	sudoers := SudoersRule()
	tmp := SudoersPath + ".tmp"
	if err := os.WriteFile(tmp, []byte(sudoers), 0o440); err != nil {
		return err
	}
	if r := runCmd(ctx, 30*time.Second, nil, "visudo", "-cf", tmp); r.ExitCode != 0 {
		os.Remove(tmp)
		return fmt.Errorf("sudoers validation failed: %s", strings.TrimSpace(r.Stdout+r.Stderr))
	}
	if err := os.Rename(tmp, SudoersPath); err != nil {
		return err
	}
	step(out, "sudoers entry %s (helper only)", SudoersPath)
	if opts.ShellSigner != "" {
		if err := AddShellSigner(opts.ShellSigner, opts.ShellSignerLabel); err != nil {
			return err
		}
		step(out, "trusted NEXUS key for approved shell commands in %s", ShellSignersPath)
	}

	// 5. systemd unit.
	unit := UnitFile(opts.Listen, opts.Interval)
	if err := os.WriteFile(UnitPath, []byte(unit), 0o644); err != nil {
		return err
	}
	step(out, "systemd unit %s", UnitPath)
	steps := [][]string{{"daemon-reload"}, {"enable", "nexus-agent.service"}}
	if !opts.Upgrade {
		steps = append(steps, []string{"restart", "nexus-agent.service"})
	}
	for _, args := range steps {
		if r := runCmd(ctx, time.Minute, nil, "systemctl", args...); r.ExitCode != 0 {
			return fmt.Errorf("systemctl %s: %s", strings.Join(args, " "), strings.TrimSpace(r.Stderr))
		}
	}
	if opts.Upgrade {
		// The upgrade runs inside the service being restarted: restart a few
		// seconds later from a transient timer, so the agent can answer NEXUS
		// and the helper can finish first.
		unit := fmt.Sprintf("nexus-agent-restart-%d", time.Now().Unix())
		r := runCmd(ctx, time.Minute, nil, "systemd-run", "--unit="+unit, "--on-active=3", "--timer-property=AccuracySec=100ms", "--collect", "systemctl", "restart", "nexus-agent.service")
		if r.ExitCode != 0 {
			r = runCmd(ctx, time.Minute, nil, "systemctl", "restart", "--no-block", "nexus-agent.service")
		}
		if r.ExitCode != 0 {
			return fmt.Errorf("scheduling the restart: %s", strings.TrimSpace(r.Stderr))
		}
	}
	step(out, "service enabled and started")
	if opts.Upgrade {
		_ = chownTree(DefaultState, uid, gid)
		return nil
	}

	code, err := st.NewEnrollmentCode(24 * time.Hour)
	if err != nil {
		return err
	}
	_ = chownTree(DefaultState, uid, gid)
	printEnrollment(out, st, code, opts.Listen)
	return nil
}

func printEnrollment(out io.Writer, st *State, code, listen string) {
	host, _ := os.Hostname()
	fmt.Fprintf(out, "\n  Agent ID     %s\n  Host         %s\n  Listening    %s\n  Fingerprint  %s\n\n", st.AgentID, host, listen, formatFP(st.Fingerprint()))
	fmt.Fprintf(out, "  Enrollment code (single use, expires in 24 h):\n\n      %s\n\n", code)
	fmt.Fprintln(out, "  In NEXUS: Fleet → Add server → enter this host's address, port and the code.")
	fmt.Fprintln(out, "  Restrict the agent port to your NEXUS workstation in the firewall where possible.")
}

func formatFP(fp string) string {
	var parts []string
	for i := 0; i+2 <= len(fp); i += 2 {
		parts = append(parts, strings.ToUpper(fp[i:i+2]))
	}
	return strings.Join(parts, ":")
}

// PrintEnrollment issues a new code for an existing installation.
func PrintEnrollment(out io.Writer, dir, listen string) error {
	st, err := LoadOrCreateState(dir)
	if err != nil {
		return err
	}
	code, err := st.NewEnrollmentCode(24 * time.Hour)
	if err != nil {
		return err
	}
	if u, err := user.Lookup(ServiceUser); err == nil && os.Geteuid() == 0 && dir == DefaultState {
		uid, _ := strconv.Atoi(u.Uid)
		gid, _ := strconv.Atoi(u.Gid)
		_ = chownTree(dir, uid, gid)
	}
	printEnrollment(out, st, code, listen)
	return nil
}

// Uninstall removes the service; purge also removes state and the user.
func Uninstall(out io.Writer, purge bool) error {
	if os.Geteuid() != 0 {
		return fmt.Errorf("uninstall must be run as root")
	}
	ctx := context.Background()
	runCmd(ctx, time.Minute, nil, "systemctl", "disable", "--now", "nexus-agent.service")
	if err := os.RemoveAll(filepath.Dir(ShellSignersPath)); err == nil {
		step(out, "removed %s", filepath.Dir(ShellSignersPath))
	}
	_ = os.RemoveAll(shellNonceDir)
	for _, p := range []string{UnitPath, SudoersPath} {
		if err := os.Remove(p); err == nil {
			step(out, "removed %s", p)
		}
	}
	runCmd(ctx, time.Minute, nil, "systemctl", "daemon-reload")
	for _, p := range []string{HelperBin, InstallBin} {
		if err := os.Remove(p); err == nil {
			step(out, "removed %s", p)
		}
	}
	_ = os.Remove(filepath.Dir(InstallBin))
	if purge {
		if err := os.RemoveAll(DefaultState); err == nil {
			step(out, "removed %s (identity and enrolled clients)", DefaultState)
		}
		if r := runCmd(ctx, time.Minute, nil, "userdel", ServiceUser); r.ExitCode == 0 {
			step(out, "removed user %s", ServiceUser)
		}
	} else {
		step(out, "kept %s; re-installing will reuse the same identity (use --purge to remove)", DefaultState)
	}
	return nil
}

// SudoersRule is the complete sudoers file the installer writes. It has no
// Defaults lines and no argument wildcards: sudo-rs rejects both.
func SudoersRule() string {
	return fmt.Sprintf("# Managed by nexus-agent. Allows only the validated privileged helper.\n%s ALL=(root) NOPASSWD: %s\n", ServiceUser, HelperBin)
}

// installHelper puts the helper next to the installed binary as a hard link
// (no extra disk space), or a copy where links are not possible. Both are
// replaced atomically, so an upgrade never leaves a stale helper behind.
func installHelper() error {
	tmp := HelperBin + ".new"
	_ = os.Remove(tmp)
	if err := os.Link(InstallBin, tmp); err == nil {
		if err := os.Rename(tmp, HelperBin); err != nil {
			os.Remove(tmp)
			return err
		}
	} else if err := copyFile(InstallBin, HelperBin, 0o755); err != nil {
		return err
	}
	_ = os.Chown(HelperBin, 0, 0)
	return os.Chmod(HelperBin, 0o755)
}

func copyFile(src, dst string, mode os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".new"
	outF, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(outF, in); err != nil {
		outF.Close()
		return err
	}
	if err := outF.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

func chownTree(dir string, uid, gid int) error {
	return filepath.Walk(dir, func(p string, _ os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		return os.Lchown(p, uid, gid)
	})
}

// UnitFile is the agent's systemd unit.
func UnitFile(listen string, interval time.Duration) string {
	return fmt.Sprintf(`[Unit]
Description=NEXUS infrastructure agent
Documentation=https://github.com/WSEU790/MoistTux
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=%s
Group=%s
ExecStart=%s run --state-dir %s --listen %s --interval %s
Restart=on-failure
RestartSec=5
# Resource limits keep the agent lightweight on small servers.
Nice=5
CPUWeight=50
MemoryMax=512M
TasksMax=256
# The agent's root operations go through sudo (a setuid program), so the
# service must allow privilege escalation. Settings such as
# ProtectKernelModules, LockPersonality, RestrictRealtime, SystemCallFilter
# or PrivateDevices silently imply NoNewPrivileges for a non-root service and
# make every sudo call fail; mount sandboxing (PrivateTmp, ProtectHome)
# would make root commands see a different /tmp and a read-only /home.
# Isolation comes from the unprivileged user and the allowlisted helper.
NoNewPrivileges=no

[Install]
WantedBy=multi-user.target
`, ServiceUser, ServiceUser, InstallBin, DefaultState, listen, interval)
}
