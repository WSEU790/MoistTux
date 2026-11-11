//go:build linux

package agent

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/WSEU790/MoistTux/internal/agent/collect"
	"github.com/WSEU790/MoistTux/internal/protocol"
)

// ---------------------------------------------------------------- SSH

func SSHConfig(ctx context.Context, priv *Privileged) (protocol.SSHConfig, error) {
	cfg := protocol.SSHConfig{Settings: map[string]string{}}
	var res protocol.CommandResult
	err := priv.Do(ctx, 30*time.Second, &res, "sshd-config")
	if err == nil && res.ExitCode == 0 {
		cfg.Source = "sshd -T (effective configuration)"
		for _, line := range strings.Split(res.Stdout, "\n") {
			k, v, ok := strings.Cut(strings.TrimSpace(line), " ")
			if ok {
				k = strings.ToLower(k)
				if _, dup := cfg.Settings[k]; dup {
					cfg.Settings[k] += ", " + v
				} else {
					cfg.Settings[k] = v
				}
			}
		}
	} else {
		// Fall back to parsing the files (first value wins, as in sshd).
		files := []string{"/etc/ssh/sshd_config"}
		inc, _ := filepath.Glob("/etc/ssh/sshd_config.d/*.conf")
		files = append(inc, files...)
		read := 0
		for _, f := range files {
			data, ferr := os.ReadFile(f)
			if ferr != nil {
				continue
			}
			read++
			for _, line := range strings.Split(string(data), "\n") {
				line = strings.TrimSpace(line)
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				if strings.HasPrefix(strings.ToLower(line), "match ") {
					break
				}
				f := strings.Fields(line)
				if len(f) >= 2 {
					k := strings.ToLower(f[0])
					if _, ok := cfg.Settings[k]; !ok {
						cfg.Settings[k] = strings.Join(f[1:], " ")
					}
				}
			}
		}
		if read == 0 {
			if err == nil {
				err = fmt.Errorf("sshd -T exited %d: %s", res.ExitCode, firstLine(res.Stderr))
			}
			return cfg, fmt.Errorf("SSH server configuration unavailable: %v", err)
		}
		cfg.Source = "configuration files (sshd -T unavailable; defaults not applied)"
	}
	cfg.Findings = sshFindings(cfg.Settings, strings.HasPrefix(cfg.Source, "sshd -T"))
	return cfg, nil
}

func sshFindings(s map[string]string, effective bool) []protocol.Finding {
	var f []protocol.Finding
	get := func(k, def string) (string, bool) {
		if v, ok := s[k]; ok {
			return strings.ToLower(v), true
		}
		return def, false
	}
	evid := func(k, v string, explicit bool) string {
		if explicit {
			return fmt.Sprintf("%s %s", k, v)
		}
		return fmt.Sprintf("%s not set (OpenSSH default: %s)", k, v)
	}
	if v, ok := get("permitrootlogin", "prohibit-password"); v == "yes" {
		f = append(f, protocol.Finding{ID: "ssh.root_login", Severity: "high", Title: "Root can log in over SSH with a password", Evidence: evid("PermitRootLogin", v, ok), Advice: "Set PermitRootLogin to prohibit-password or no, and log in as an unprivileged user with sudo."})
	}
	if v, ok := get("passwordauthentication", "yes"); v == "yes" {
		f = append(f, protocol.Finding{ID: "ssh.password_auth", Severity: "medium", Title: "SSH password authentication is enabled", Evidence: evid("PasswordAuthentication", v, ok), Advice: "Use key-based authentication and set PasswordAuthentication no once keys are confirmed working for every admin."})
	}
	if v, ok := get("permitemptypasswords", "no"); v == "yes" {
		f = append(f, protocol.Finding{ID: "ssh.empty_passwords", Severity: "critical", Title: "SSH permits empty passwords", Evidence: evid("PermitEmptyPasswords", v, ok), Advice: "Set PermitEmptyPasswords no."})
	}
	if v, ok := get("x11forwarding", "no"); v == "yes" {
		f = append(f, protocol.Finding{ID: "ssh.x11", Severity: "low", Title: "X11 forwarding is enabled", Evidence: evid("X11Forwarding", v, ok), Advice: "Disable X11Forwarding on servers that have no graphical workloads."})
	}
	if v, ok := get("maxauthtries", "6"); ok || effective {
		if n, err := strconv.Atoi(v); err == nil && n > 6 {
			f = append(f, protocol.Finding{ID: "ssh.max_auth_tries", Severity: "low", Title: "High MaxAuthTries", Evidence: evid("MaxAuthTries", v, ok), Advice: "A value of 3–4 slows down online guessing."})
		}
	}
	if v, ok := get("port", "22"); v == "22" {
		f = append(f, protocol.Finding{ID: "ssh.port", Severity: "info", Title: "SSH listens on the default port", Evidence: evid("Port", v, ok), Advice: "Not a vulnerability by itself; restricting source addresses in the firewall is more effective than changing the port."})
	}
	return f
}

// ---------------------------------------------------------------- auth events

var (
	reFailed   = regexp.MustCompile(`Failed (password|publickey|keyboard-interactive/pam) for (invalid user )?(\S+) from (\S+)`)
	reInvalid  = regexp.MustCompile(`Invalid user (\S*) from (\S+)`)
	reAccepted = regexp.MustCompile(`Accepted (\S+) for (\S+) from (\S+)`)
	reSudo     = regexp.MustCompile(`^\s*(\S+) : .*COMMAND=(.*)$`)
	reSudoFail = regexp.MustCompile(`(\S+) : (\d+ incorrect password attempts?|user NOT in sudoers)`)
)

func AuthEvents(ctx context.Context, priv *Privileged, since time.Duration) (protocol.AuthSummary, error) {
	sum := protocol.AuthSummary{Since: time.Now().Add(-since).UTC(), FailedBySrc: map[string]int{}, FailedByUser: map[string]int{}, Events: []protocol.AuthEvent{}}
	q := protocol.LogQuery{Since: "-" + strconv.Itoa(int(since.Hours())) + "h", Limit: 5000}
	if since < time.Hour {
		q.Since = "-" + strconv.Itoa(int(since.Minutes())) + "m"
	}
	var entries []protocol.LogEntry
	if journalAvailable() {
		sum.Source = "journald"
		for _, unit := range []string{"ssh.service", "sshd.service"} {
			q.Unit = unit
			if r, err := queryJournal(ctx, q, sum.Since, time.Time{}, nil); err == nil {
				entries = append(entries, r.Entries...)
			}
		}
		q.Unit = ""
		// sudo logs under its identifier, not a unit.
		r := runCmd(ctx, 30*time.Second, nil, "journalctl", "--no-pager", "-o", "cat", "-t", "sudo", "--since", sum.Since.Local().Format("2006-01-02 15:04:05"))
		for _, l := range strings.Split(r.Stdout, "\n") {
			if l != "" {
				entries = append(entries, protocol.LogEntry{Ident: "sudo", Message: l})
			}
		}
	} else {
		path := ""
		for _, p := range []string{"/var/log/auth.log", "/var/log/secure"} {
			if _, err := os.Stat(p); err == nil {
				path = p
				break
			}
		}
		if path == "" {
			sum.Note = "no journald journal and no /var/log/auth.log or /var/log/secure; authentication events unavailable"
			return sum, nil
		}
		sum.Source = "file:" + path
		q.File = path
		r, err := queryFile(ctx, priv, q, sum.Since, time.Time{}, nil)
		if err != nil {
			return sum, err
		}
		entries = r.Entries
	}
	for _, e := range entries {
		ev := protocol.AuthEvent{Time: e.Time, Raw: e.Message}
		switch {
		case reFailed.MatchString(e.Message):
			m := reFailed.FindStringSubmatch(e.Message)
			ev.Kind, ev.Method, ev.User, ev.Source = "ssh_failed", m[1], m[3], m[4]
			if m[2] != "" {
				ev.Kind = "ssh_invalid_user"
			}
		case reInvalid.MatchString(e.Message):
			m := reInvalid.FindStringSubmatch(e.Message)
			ev.Kind, ev.User, ev.Source = "ssh_invalid_user", m[1], m[2]
		case reAccepted.MatchString(e.Message):
			m := reAccepted.FindStringSubmatch(e.Message)
			ev.Kind, ev.Method, ev.User, ev.Source = "ssh_accepted", m[1], m[2], m[3]
		case e.Ident == "sudo" && reSudoFail.MatchString(e.Message):
			m := reSudoFail.FindStringSubmatch(e.Message)
			ev.Kind, ev.User, ev.Command = "sudo_failed", m[1], m[2]
		case e.Ident == "sudo" && reSudo.MatchString(e.Message):
			m := reSudo.FindStringSubmatch(e.Message)
			ev.Kind, ev.User, ev.Command = "sudo", m[1], m[2]
		default:
			continue
		}
		switch ev.Kind {
		case "ssh_failed", "ssh_invalid_user":
			sum.Failed++
			sum.FailedBySrc[ev.Source]++
			sum.FailedByUser[ev.User]++
		case "ssh_accepted":
			sum.Accepted++
		case "sudo", "sudo_failed":
			sum.Sudo++
		}
		sum.Events = append(sum.Events, ev)
	}
	sort.Slice(sum.Events, func(i, j int) bool { return sum.Events[i].Time.After(sum.Events[j].Time) })
	if len(sum.Events) > 1000 {
		sum.Events = sum.Events[:1000]
	}
	return sum, nil
}

// ---------------------------------------------------------------- users

func Users(h *collect.Host) ([]protocol.SystemUser, error) {
	pw, err := h.Passwd()
	if err != nil {
		return nil, err
	}
	members, byGID, _ := h.Groups()
	userGroups := map[string][]string{}
	for g, ms := range members {
		for _, m := range ms {
			userGroups[m] = append(userGroups[m], g)
		}
	}
	minUID := 1000
	if b, err := os.ReadFile(filepath.Join(h.Etc, "login.defs")); err == nil {
		sc := bufio.NewScanner(strings.NewReader(string(b)))
		for sc.Scan() {
			f := strings.Fields(sc.Text())
			if len(f) == 2 && f[0] == "UID_MIN" {
				minUID, _ = strconv.Atoi(f[1])
			}
		}
	}
	var out []protocol.SystemUser
	for _, u := range pw {
		groups := userGroups[u.Name]
		if g, ok := byGID[u.GID]; ok {
			groups = append([]string{g}, groups...)
		}
		sudo := false
		for _, g := range groups {
			if g == "sudo" || g == "wheel" || g == "admin" {
				sudo = true
			}
		}
		noLogin := strings.HasSuffix(u.Shell, "nologin") || strings.HasSuffix(u.Shell, "/false") || u.Shell == ""
		out = append(out, protocol.SystemUser{
			Name: u.Name, UID: u.UID, GID: u.GID, Home: u.Home, Shell: u.Shell, Groups: groups,
			CanLogin: !noLogin, Sudo: sudo || u.UID == 0, System: u.UID < minUID && u.UID != 0,
		})
	}
	return out, nil
}

// ---------------------------------------------------------------- firewall

func Firewall(ctx context.Context, priv *Privileged) protocol.FirewallStatus {
	fw := protocol.FirewallStatus{Backend: "none", Rules: []string{}}
	for _, b := range []string{"ufw", "nft", "iptables-save"} {
		if _, err := lookPath(b); err == nil {
			fw.Detected = append(fw.Detected, b)
		}
	}
	var res protocol.CommandResult
	has := func(n string) bool {
		for _, d := range fw.Detected {
			if d == n {
				return true
			}
		}
		return false
	}
	if has("ufw") {
		if err := priv.Do(ctx, 30*time.Second, &res, "ufw-status"); err == nil && res.ExitCode == 0 {
			fw.Backend = "ufw"
			fw.Raw = res.Stdout
			for _, line := range strings.Split(res.Stdout, "\n") {
				l := strings.TrimSpace(line)
				switch {
				case strings.HasPrefix(l, "Status:"):
					fw.Active = strings.Contains(l, "active") && !strings.Contains(l, "inactive")
				case strings.HasPrefix(l, "Default:"):
					for _, part := range strings.Split(strings.TrimPrefix(l, "Default:"), ",") {
						part = strings.TrimSpace(part)
						if strings.HasSuffix(part, "(incoming)") {
							fw.DefaultIn = strings.Fields(part)[0]
						} else if strings.HasSuffix(part, "(outgoing)") {
							fw.DefaultOut = strings.Fields(part)[0]
						}
					}
				case strings.Contains(l, "ALLOW") || strings.Contains(l, "DENY") || strings.Contains(l, "REJECT") || strings.Contains(l, "LIMIT"):
					fw.Rules = append(fw.Rules, l)
				}
			}
			if fw.Active {
				return fw
			}
		} else if err != nil {
			fw.Unavailable = err.Error()
		}
	}
	if has("nft") {
		res = protocol.CommandResult{}
		if err := priv.Do(ctx, 30*time.Second, &res, "nft-list"); err == nil && res.ExitCode == 0 {
			rules := 0
			for _, line := range strings.Split(res.Stdout, "\n") {
				l := strings.TrimSpace(line)
				if l != "" && !strings.HasPrefix(l, "table") && !strings.HasPrefix(l, "chain") && l != "}" && !strings.HasPrefix(l, "type ") {
					fw.Rules = append(fw.Rules, l)
					rules++
				}
			}
			if strings.TrimSpace(res.Stdout) != "" {
				fw.Backend, fw.Active, fw.Raw = "nftables", rules > 0, res.Stdout
				return fw
			}
		} else if err != nil && fw.Unavailable == "" {
			fw.Unavailable = err.Error()
		}
	}
	if has("iptables-save") {
		res = protocol.CommandResult{}
		if err := priv.Do(ctx, 30*time.Second, &res, "iptables-save"); err == nil && res.ExitCode == 0 {
			fw.Backend, fw.Raw = "iptables", res.Stdout
			for _, line := range strings.Split(res.Stdout, "\n") {
				if strings.HasPrefix(line, "-A ") {
					fw.Rules = append(fw.Rules, line)
				}
				if strings.HasPrefix(line, ":INPUT ") {
					fw.DefaultIn = strings.Fields(line)[1]
				}
				if strings.HasPrefix(line, ":OUTPUT ") {
					fw.DefaultOut = strings.Fields(line)[1]
				}
			}
			fw.Active = len(fw.Rules) > 0 || (fw.DefaultIn != "" && fw.DefaultIn != "ACCEPT")
		} else if err != nil && fw.Unavailable == "" {
			fw.Unavailable = err.Error()
		}
	}
	return fw
}

// ---------------------------------------------------------------- posture

func Posture(ctx context.Context, h *collect.Host, priv *Privileged, pm PackageManager) protocol.SecurityPosture {
	p := protocol.SecurityPosture{Tools: map[string]bool{}, CheckedAt: time.Now().UTC(), Findings: []protocol.Finding{}}
	for _, t := range []string{"fail2ban-client", "auditctl", "aide", "lynis", "ufw", "nft", "apparmor_status", "sestatus"} {
		_, err := lookPath(t)
		p.Tools[strings.TrimSuffix(strings.TrimSuffix(t, "-client"), "_status")] = err == nil
	}
	if ssh, err := SSHConfig(ctx, priv); err == nil {
		p.Findings = append(p.Findings, ssh.Findings...)
	} else {
		p.Findings = append(p.Findings, protocol.Finding{ID: "ssh.unavailable", Severity: "info", Title: "SSH configuration could not be read", Evidence: err.Error()})
	}
	fw := Firewall(ctx, priv)
	if !fw.Active {
		ev := "no active ufw, nftables or iptables rules detected"
		if fw.Unavailable != "" {
			ev += " (" + fw.Unavailable + ")"
		}
		p.Findings = append(p.Findings, protocol.Finding{ID: "fw.inactive", Severity: "medium", Title: "No active host firewall", Evidence: ev, Advice: "Enable a default-deny inbound policy that allows only required ports. NEXUS will not change firewall rules automatically."})
	} else if fw.DefaultIn != "" && strings.EqualFold(fw.DefaultIn, "allow") || strings.EqualFold(fw.DefaultIn, "ACCEPT") && fw.Backend == "iptables" {
		p.Findings = append(p.Findings, protocol.Finding{ID: "fw.default_allow", Severity: "low", Title: "Firewall default inbound policy allows traffic", Evidence: fw.Backend + " default incoming: " + fw.DefaultIn, Advice: "Prefer a default-deny inbound policy."})
	}
	if users, err := Users(h); err == nil {
		for _, u := range users {
			if u.UID == 0 && u.Name != "root" {
				p.Findings = append(p.Findings, protocol.Finding{ID: "users.uid0." + u.Name, Severity: "critical", Title: "Non-root account with UID 0", Evidence: fmt.Sprintf("%s has UID 0 (shell %s)", u.Name, u.Shell), Advice: "Investigate immediately; UID 0 accounts other than root are a common persistence technique."})
			}
		}
	}
	for path, max := range map[string]os.FileMode{"/etc/shadow": 0o640, "/etc/gshadow": 0o640, "/etc/sudoers": 0o440, "/etc/passwd": 0o644, "/etc/ssh/sshd_config": 0o644} {
		st, err := os.Stat(path)
		if err != nil {
			continue
		}
		if st.Mode().Perm()&^max != 0 {
			p.Findings = append(p.Findings, protocol.Finding{ID: "perm." + path, Severity: "high", Title: "Permissive permissions on " + path, Evidence: fmt.Sprintf("%s mode %04o (expected at most %04o)", path, st.Mode().Perm(), max), Advice: fmt.Sprintf("chmod %o %s", max, path)})
		}
	}
	if chk, err := pm.Updates(ctx); err == nil && chk.SecurityCount > 0 {
		p.Findings = append(p.Findings, protocol.Finding{ID: "pkg.security_updates", Severity: "high", Title: fmt.Sprintf("%d security updates pending", chk.SecurityCount), Evidence: securityList(chk), Advice: "Review and apply through the NEXUS update workflow (backup check, approval, verification)."})
	}
	if ok, pkgs := h.RebootRequired(); ok {
		p.Findings = append(p.Findings, protocol.Finding{ID: "pkg.reboot_required", Severity: "medium", Title: "Reboot required to activate updates", Evidence: "/run/reboot-required present; packages: " + strings.Join(pkgs, ", "), Advice: "Schedule a reboot in a maintenance window. NEXUS never reboots without an approved policy."})
	}
	if !p.Tools["fail2ban"] {
		p.Findings = append(p.Findings, protocol.Finding{ID: "tools.fail2ban", Severity: "info", Title: "Fail2ban is not installed", Evidence: "fail2ban-client not found in PATH", Advice: "Consider fail2ban or sshd rate limiting if SSH is reachable from the internet."})
	}
	return p
}

func securityList(chk protocol.UpdateCheck) string {
	var names []string
	for _, u := range chk.Updates {
		if u.Security {
			names = append(names, u.Name+" "+u.Candidate)
		}
	}
	if len(names) > 15 {
		names = append(names[:15], fmt.Sprintf("… and %d more", len(names)-15))
	}
	return strings.Join(names, ", ")
}
