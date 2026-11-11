//go:build linux

package agent

import (
	"context"
	"errors"
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

// PackageManager is the distro adapter boundary. APT is implemented; other
// managers return ErrPMUnsupported rather than pretending to work.
type PackageManager interface {
	Name() string
	Installed(ctx context.Context) (protocol.PackageList, error)
	Updates(ctx context.Context) (protocol.UpdateCheck, error)
	Refresh(ctx context.Context) (protocol.CommandResult, error)
	Upgrade(ctx context.Context, req protocol.UpgradeRequest) (protocol.CommandResult, error)
	History(limit int) ([]protocol.PackageHistoryEntry, error)
}

var ErrPMUnsupported = errors.New("this package manager is detected but not yet supported by NEXUS")

func NewPackageManager(priv *Privileged) PackageManager {
	switch collect.DetectPackageManager() {
	case "apt":
		return &apt{priv: priv, h: collect.Default()}
	case "":
		return unsupportedPM{name: "none"}
	default:
		return unsupportedPM{name: collect.DetectPackageManager()}
	}
}

type unsupportedPM struct{ name string }

func (u unsupportedPM) Name() string { return u.name }
func (u unsupportedPM) Installed(context.Context) (protocol.PackageList, error) {
	return protocol.PackageList{}, fmt.Errorf("%s: %w", u.name, ErrPMUnsupported)
}
func (u unsupportedPM) Updates(context.Context) (protocol.UpdateCheck, error) {
	return protocol.UpdateCheck{}, fmt.Errorf("%s: %w", u.name, ErrPMUnsupported)
}
func (u unsupportedPM) Refresh(context.Context) (protocol.CommandResult, error) {
	return protocol.CommandResult{}, fmt.Errorf("%s: %w", u.name, ErrPMUnsupported)
}
func (u unsupportedPM) Upgrade(context.Context, protocol.UpgradeRequest) (protocol.CommandResult, error) {
	return protocol.CommandResult{}, fmt.Errorf("%s: %w", u.name, ErrPMUnsupported)
}
func (u unsupportedPM) History(int) ([]protocol.PackageHistoryEntry, error) {
	return nil, fmt.Errorf("%s: %w", u.name, ErrPMUnsupported)
}

type apt struct {
	priv *Privileged
	h    *collect.Host
}

func (a *apt) Name() string { return "apt" }

func (a *apt) Installed(ctx context.Context) (protocol.PackageList, error) {
	r := runCmd(ctx, 60*time.Second, nil, "dpkg-query", "-W", "-f=${db:Status-Abbrev}\t${Package}\t${Version}\t${Architecture}\t${Installed-Size}\t${binary:Summary}\n")
	if r.ExitCode != 0 {
		return protocol.PackageList{}, fmt.Errorf("dpkg-query: %s", firstLine(r.Stderr))
	}
	out := protocol.PackageList{Manager: "apt", Packages: []protocol.Package{}}
	for _, line := range strings.Split(r.Stdout, "\n") {
		f := strings.Split(line, "\t")
		if len(f) < 6 || !strings.HasPrefix(f[0], "ii") {
			continue
		}
		kib, _ := strconv.ParseUint(f[4], 10, 64)
		out.Packages = append(out.Packages, protocol.Package{Name: f[1], Version: f[2], Arch: f[3], InstalledKiB: kib, Summary: f[5]})
	}
	sort.Slice(out.Packages, func(i, j int) bool { return out.Packages[i].Name < out.Packages[j].Name })
	out.Total = len(out.Packages)
	return out, nil
}

// "Inst openssl [3.0.13-0ubuntu3.4] (3.0.13-0ubuntu3.5 Ubuntu:24.04/noble-updates, Ubuntu:24.04/noble-security [amd64])"
var reInst = regexp.MustCompile(`^Inst (\S+) (?:\[([^\]]+)\] )?\((\S+) (.+?)(?: \[([^\]]+)\])?\)`)
var reRemv = regexp.MustCompile(`^Remv (\S+)`)

func isSecurityOrigin(origin string) bool {
	o := strings.ToLower(origin)
	return strings.Contains(o, "-security") || strings.Contains(o, "debian-security") || strings.Contains(o, "/security")
}

// AptSimulate runs `apt-get -s upgrade`, which needs no root and changes nothing.
func AptSimulate(ctx context.Context) (protocol.UpdateCheck, error) {
	r := runCmd(ctx, 2*time.Minute, []string{"DEBIAN_FRONTEND=noninteractive"}, "apt-get", "-s", "-o", "Debug::NoLocking=1", "-o", "APT::Get::Show-Upgraded=true", "upgrade")
	if r.ExitCode != 0 {
		return protocol.UpdateCheck{}, fmt.Errorf("apt-get -s upgrade failed: %s", firstLine(r.Stderr))
	}
	chk := protocol.UpdateCheck{Manager: "apt", Updates: []protocol.PackageUpdate{}}
	keptBack := false
	var kept []string
	for _, line := range strings.Split(r.Stdout, "\n") {
		if strings.HasPrefix(line, "The following packages have been kept back") {
			keptBack = true
			continue
		}
		if keptBack {
			if strings.HasPrefix(line, "  ") {
				kept = append(kept, strings.Fields(line)...)
				continue
			}
			keptBack = false
		}
		if m := reInst.FindStringSubmatch(line); m != nil {
			u := protocol.PackageUpdate{Name: m[1], Current: m[2], Candidate: m[3], Origin: m[4], Arch: m[5], Security: isSecurityOrigin(m[4]), NewInstall: m[2] == ""}
			if u.Security {
				chk.SecurityCount++
			}
			if strings.HasPrefix(u.Name, "linux-image") || strings.HasPrefix(u.Name, "linux-generic") || strings.HasPrefix(u.Name, "linux-modules") {
				chk.KernelUpdate = true
			}
			chk.Updates = append(chk.Updates, u)
		} else if m := reRemv.FindStringSubmatch(line); m != nil {
			chk.Removals = append(chk.Removals, m[1])
		}
	}
	if len(kept) > 0 {
		chk.Note = "kept back (need dist-upgrade, not applied by NEXUS automatically): " + strings.Join(kept, " ")
	}
	return chk, nil
}

func (a *apt) Updates(ctx context.Context) (protocol.UpdateCheck, error) {
	chk, err := AptSimulate(ctx)
	if err != nil {
		return chk, err
	}
	if t := a.listsUpdatedAt(); t != nil {
		chk.ListsUpdatedAt = t
		chk.ListsAge = time.Since(*t).Round(time.Minute).String()
	} else {
		chk.Note = strings.TrimSpace(chk.Note + " package lists have never been refreshed; results may be stale")
	}
	chk.RebootRequired, chk.RebootPkgs = a.h.RebootRequired()
	return chk, nil
}

func (a *apt) listsUpdatedAt() *time.Time {
	var newest time.Time
	for _, p := range []string{"/var/lib/apt/periodic/update-success-stamp", "/var/lib/apt/lists/partial"} {
		if st, err := os.Stat(p); err == nil && st.ModTime().After(newest) {
			newest = st.ModTime()
		}
	}
	files, _ := filepath.Glob("/var/lib/apt/lists/*Release")
	for _, f := range files {
		if st, err := os.Stat(f); err == nil && st.ModTime().After(newest) {
			newest = st.ModTime()
		}
	}
	if newest.IsZero() {
		return nil
	}
	u := newest.UTC()
	return &u
}

func (a *apt) Refresh(ctx context.Context) (protocol.CommandResult, error) {
	var res protocol.CommandResult
	err := a.priv.Do(ctx, 11*time.Minute, &res, "apt-update")
	return res, err
}

func (a *apt) Upgrade(ctx context.Context, req protocol.UpgradeRequest) (protocol.CommandResult, error) {
	var res protocol.CommandResult
	if req.DryRun {
		args := []string{"-s", "-o", "Debug::NoLocking=1"}
		if len(req.Packages) > 0 {
			for _, p := range req.Packages {
				if !rePackage.MatchString(p) {
					return res, fmt.Errorf("invalid package name %q", p)
				}
			}
			args = append(append(args, "install", "--only-upgrade"), req.Packages...)
		} else {
			args = append(args, "upgrade")
		}
		return runCmd(ctx, 2*time.Minute, nil, "apt-get", args...), nil
	}
	var err error
	switch {
	case len(req.Packages) > 0:
		err = a.priv.Do(ctx, 46*time.Minute, &res, "apt-upgrade", append([]string{"packages"}, req.Packages...)...)
	case req.SecurityOnly:
		err = a.priv.Do(ctx, 46*time.Minute, &res, "apt-upgrade", "security")
	default:
		err = a.priv.Do(ctx, 46*time.Minute, &res, "apt-upgrade", "all")
	}
	return res, err
}

func (a *apt) History(limit int) ([]protocol.PackageHistoryEntry, error) {
	return a.h.AptHistory(limit)
}
