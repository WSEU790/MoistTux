//go:build linux

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/WSEU790/MoistTux/internal/agent/collect"
)

// The privileged helper is the ONLY path by which the agent gains root. It
// accepts a closed set of operations, each with strictly validated arguments.
// There is deliberately no operation that runs an arbitrary command or shell.

var (
	reUnit    = regexp.MustCompile(`^[A-Za-z0-9@._:\\-]{1,200}\.(service|socket|timer|target|path|mount)$`)
	rePackage = regexp.MustCompile(`^[a-z0-9][a-z0-9+.\-]{0,127}(:[a-z0-9]+)?$`)
	reJail    = regexp.MustCompile(`^[A-Za-z0-9_.\-]{1,64}$`)
)

var systemctlVerbs = map[string]bool{
	"start": true, "stop": true, "restart": true, "reload": true, "try-reload-or-restart": true,
	"enable": true, "disable": true, "reset-failed": true,
}

// Files the helper may read. Private keys and credential stores are refused
// even when they match an allowed prefix.
var readablePrefixes = []string{"/var/log/", "/etc/ssh/", "/etc/nginx/", "/etc/apache2/", "/etc/httpd/", "/etc/caddy/", "/etc/traefik/", "/etc/letsencrypt/live/", "/etc/fail2ban/", "/etc/ufw/", "/etc/docker/", "/etc/systemd/system/"}
var deniedFragments = []string{"privkey", "_key", ".key", "id_rsa", "id_ed25519", "id_ecdsa", "shadow", "gshadow", ".htpasswd", "secret", "credentials", "token"}

func helperUsage(op string) error { return fmt.Errorf("helper: invalid arguments for %q", op) }

// ValidateHelperArgs is called both before invoking sudo and inside the
// helper process, so a forged invocation through sudo is validated too.
func ValidateHelperArgs(op string, args []string) error {
	switch op {
	case "ping", "apt-update", "ufw-status", "nft-list", "iptables-save", "sshd-config", "sockets", "reboot":
		if len(args) != 0 {
			return helperUsage(op)
		}
	case "systemctl":
		if len(args) != 2 || !systemctlVerbs[args[0]] || !reUnit.MatchString(args[1]) {
			return helperUsage(op)
		}
	case "apt-upgrade":
		if len(args) < 1 {
			return helperUsage(op)
		}
		switch args[0] {
		case "all", "security":
			if len(args) != 1 {
				return helperUsage(op)
			}
		case "packages":
			if len(args) < 2 || len(args) > 500 {
				return helperUsage(op)
			}
			for _, p := range args[1:] {
				if !rePackage.MatchString(p) {
					return fmt.Errorf("helper: invalid package name %q", p)
				}
			}
		default:
			return helperUsage(op)
		}
	case "fail2ban-status":
		if len(args) > 1 || (len(args) == 1 && !reJail.MatchString(args[0])) {
			return helperUsage(op)
		}
	case "read-file":
		if len(args) != 2 {
			return helperUsage(op)
		}
		if err := validateReadable(args[0]); err != nil {
			return err
		}
		if n, err := strconv.Atoi(args[1]); err != nil || n <= 0 || n > 8<<20 {
			return helperUsage(op)
		}
	case "du":
		if len(args) != 2 {
			return helperUsage(op)
		}
		if err := validateScanPath(args[0]); err != nil {
			return err
		}
		if d, err := strconv.Atoi(args[1]); err != nil || d < 0 || d > 4 {
			return helperUsage(op)
		}
	case "largefiles":
		if len(args) != 3 {
			return helperUsage(op)
		}
		if err := validateScanPath(args[0]); err != nil {
			return err
		}
		if n, err := strconv.ParseUint(args[1], 10, 64); err != nil || n < 1<<20 {
			return errors.New("helper: largefiles minimum size must be at least 1 MiB")
		}
		if n, err := strconv.Atoi(args[2]); err != nil || n <= 0 || n > 500 {
			return helperUsage(op)
		}
	default:
		return fmt.Errorf("helper: unknown operation %q", op)
	}
	return nil
}

func validateScanPath(p string) error {
	if !filepath.IsAbs(p) || filepath.Clean(p) != p || strings.ContainsRune(p, 0) {
		return fmt.Errorf("helper: path must be absolute and clean: %q", p)
	}
	for _, bad := range []string{"/proc", "/sys", "/dev"} {
		if p == bad || strings.HasPrefix(p, bad+"/") {
			return fmt.Errorf("helper: refusing to scan pseudo filesystem %s", bad)
		}
	}
	return nil
}

func validateReadable(p string) error {
	if !filepath.IsAbs(p) || filepath.Clean(p) != p || strings.ContainsRune(p, 0) {
		return fmt.Errorf("helper: path must be absolute and clean: %q", p)
	}
	ok := false
	for _, pre := range readablePrefixes {
		if strings.HasPrefix(p, pre) {
			ok = true
			break
		}
	}
	if !ok {
		return fmt.Errorf("helper: %s is outside the readable allowlist", p)
	}
	lp := strings.ToLower(filepath.Base(p))
	for _, frag := range deniedFragments {
		if strings.Contains(lp, frag) {
			return fmt.Errorf("helper: refusing to read %s (may contain secrets)", p)
		}
	}
	// Refuse symlinks that leave the allowlist.
	if real, err := filepath.EvalSymlinks(p); err == nil && real != p {
		return validateReadable(real)
	}
	return nil
}

// ExecHelper performs a validated helper operation. It must run as root.
func ExecHelper(ctx context.Context, op string, args []string) (any, error) {
	if err := ValidateHelperArgs(op, args); err != nil {
		return nil, err
	}
	aptEnv := []string{"DEBIAN_FRONTEND=noninteractive", "NEEDRESTART_MODE=l", "APT_LISTCHANGES_FRONTEND=none"}
	switch op {
	case "ping":
		return "pong", nil
	case "systemctl":
		return runCmd(ctx, 2*time.Minute, nil, "systemctl", "--no-ask-password", args[0], args[1]), nil
	case "apt-update":
		return runCmd(ctx, 10*time.Minute, aptEnv, "apt-get", "update", "-q"), nil
	case "apt-upgrade":
		base := []string{"-y", "-q", "-o", "Dpkg::Options::=--force-confdef", "-o", "Dpkg::Options::=--force-confold"}
		switch args[0] {
		case "all":
			// `upgrade` never removes packages; dist-upgrade is not offered.
			return runCmd(ctx, 45*time.Minute, aptEnv, "apt-get", append([]string{"upgrade"}, base...)...), nil
		case "security":
			chk, err := AptSimulate(ctx)
			if err != nil {
				return nil, err
			}
			var pkgs []string
			for _, u := range chk.Updates {
				if u.Security {
					pkgs = append(pkgs, u.Name)
				}
			}
			if len(pkgs) == 0 {
				return runCmd(ctx, time.Minute, nil, "true"), nil
			}
			return runCmd(ctx, 45*time.Minute, aptEnv, "apt-get", append(append([]string{"install", "--only-upgrade"}, base...), pkgs...)...), nil
		default:
			return runCmd(ctx, 45*time.Minute, aptEnv, "apt-get", append(append([]string{"install", "--only-upgrade"}, base...), args[1:]...)...), nil
		}
	case "ufw-status":
		return runCmd(ctx, 30*time.Second, nil, "ufw", "status", "verbose"), nil
	case "nft-list":
		return runCmd(ctx, 30*time.Second, nil, "nft", "list", "ruleset"), nil
	case "iptables-save":
		return runCmd(ctx, 30*time.Second, nil, "iptables-save"), nil
	case "sshd-config":
		return runCmd(ctx, 30*time.Second, nil, "sshd", "-T"), nil
	case "fail2ban-status":
		return runCmd(ctx, 30*time.Second, nil, "fail2ban-client", append([]string{"status"}, args...)...), nil
	case "read-file":
		max, _ := strconv.Atoi(args[1])
		return readTail(args[0], int64(max))
	case "sockets":
		socks, _ := collect.Default().ListeningSockets()
		return socks, nil
	case "du":
		depth, _ := strconv.Atoi(args[1])
		return DirectoryUsage(ctx, args[0], depth, false), nil
	case "largefiles":
		min, _ := strconv.ParseUint(args[1], 10, 64)
		limit, _ := strconv.Atoi(args[2])
		return FindLargeFiles(ctx, args[0], min, limit, false), nil
	case "reboot":
		return runCmd(ctx, time.Minute, nil, "systemctl", "--no-ask-password", "reboot"), nil
	}
	return nil, fmt.Errorf("helper: unknown operation %q", op)
}

func readTail(path string, max int64) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !st.Mode().IsRegular() {
		return "", fmt.Errorf("%s is not a regular file", path)
	}
	if st.Size() > max {
		if _, err := f.Seek(st.Size()-max, io.SeekStart); err != nil {
			return "", err
		}
	}
	b, err := io.ReadAll(io.LimitReader(f, max))
	return string(b), err
}

// HelperMain is the entry point for `nexus-agent helper <op> [args...]`.
func HelperMain(args []string) int {
	enc := json.NewEncoder(os.Stdout)
	fail := func(err error) int {
		_ = enc.Encode(helperEnvelope{OK: false, Error: err.Error()})
		return 1
	}
	if os.Geteuid() != 0 {
		return fail(errors.New("helper must run as root (via sudo)"))
	}
	if len(args) < 1 {
		return fail(errors.New("helper: missing operation"))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Minute)
	defer cancel()
	res, err := ExecHelper(ctx, args[0], args[1:])
	if err != nil {
		return fail(err)
	}
	raw, err := json.Marshal(res)
	if err != nil {
		return fail(err)
	}
	_ = enc.Encode(helperEnvelope{OK: true, Result: raw})
	return 0
}
