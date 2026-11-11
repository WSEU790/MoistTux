//go:build linux

package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/WSEU790/MoistTux/internal/protocol"
)

type systemd struct{}

func (systemd) available(ctx context.Context) (bool, string) {
	r := runCmd(ctx, 10*time.Second, nil, "systemctl", "is-system-running")
	out := strings.TrimSpace(r.Stdout + " " + r.Stderr)
	if strings.Contains(out, "not been booted with systemd") || strings.Contains(out, "Failed to connect to bus") || strings.Contains(out, "not installed") {
		return false, firstLine(strings.TrimSpace(r.Stderr + r.Stdout))
	}
	// "offline" means systemctl exists but systemd is not the running init.
	if strings.TrimSpace(r.Stdout) == "offline" {
		return false, "systemctl reports 'offline' (systemd is installed but is not PID 1)"
	}
	return true, ""
}

func (s systemd) List(ctx context.Context) protocol.ServiceList {
	res := protocol.ServiceList{Services: []protocol.Service{}}
	if ok, why := s.available(ctx); !ok {
		res.Unavailable = "systemd is not managing this host: " + why
		return res
	}
	res.Available = true
	r := runCmd(ctx, 20*time.Second, nil, "systemctl", "list-units", "--type=service", "--all", "--no-pager", "--output=json")
	var units []struct {
		Unit, Load, Active, Sub, Description string
	}
	if r.ExitCode == 0 && json.Unmarshal([]byte(r.Stdout), &units) == nil {
		for _, u := range units {
			res.Services = append(res.Services, protocol.Service{Unit: u.Unit, Description: u.Description, LoadState: u.Load, ActiveState: u.Active, SubState: u.Sub})
		}
	} else {
		// Older systemd without --output=json.
		r = runCmd(ctx, 20*time.Second, nil, "systemctl", "list-units", "--type=service", "--all", "--no-pager", "--plain", "--no-legend")
		for _, line := range strings.Split(r.Stdout, "\n") {
			f := strings.Fields(line)
			if len(f) < 4 {
				continue
			}
			res.Services = append(res.Services, protocol.Service{Unit: f[0], LoadState: f[1], ActiveState: f[2], SubState: f[3], Description: strings.Join(f[4:], " ")})
		}
	}
	// Enablement state.
	files := map[string]string{}
	r = runCmd(ctx, 20*time.Second, nil, "systemctl", "list-unit-files", "--type=service", "--no-pager", "--plain", "--no-legend")
	for _, line := range strings.Split(r.Stdout, "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 {
			files[f[0]] = f[1]
		}
	}
	for i := range res.Services {
		res.Services[i].UnitFile = files[res.Services[i].Unit]
		switch res.Services[i].ActiveState {
		case "failed":
			res.Failed++
		case "active":
			if res.Services[i].SubState == "running" {
				res.Running++
			}
		}
	}
	sort.Slice(res.Services, func(i, j int) bool { return res.Services[i].Unit < res.Services[j].Unit })
	return res
}

var showProps = []string{
	"Id", "Description", "LoadState", "ActiveState", "SubState", "UnitFileState", "MainPID", "Result",
	"NRestarts", "Restart", "ActiveEnterTimestamp", "InactiveEnterTimestamp", "ExecMainStatus",
	"MemoryCurrent", "CPUUsageNSec", "TasksCurrent", "FragmentPath", "Requires", "Wants", "After",
	"RequiredBy", "WantedBy", "Type", "User", "RestartUSec", "StartLimitBurst", "WatchdogUSec",
	"ExecMainStartTimestamp", "StatusText", "ProtectSystem", "NoNewPrivileges", "PrivateTmp",
}

func parseSystemdTime(v string) *time.Time {
	v = strings.TrimSpace(v)
	if v == "" || v == "n/a" || v == "0" {
		return nil
	}
	for _, layout := range []string{"Mon 2006-01-02 15:04:05 MST", "Mon 2006-01-02 15:04:05 -0700"} {
		if t, err := time.Parse(layout, v); err == nil {
			u := t.UTC()
			return &u
		}
	}
	return nil
}

func splitList(v string) []string {
	if v == "" {
		return nil
	}
	return strings.Fields(v)
}

func parseU64(v string) uint64 {
	n, err := strconv.ParseUint(strings.TrimSpace(v), 10, 64)
	if err != nil || n == ^uint64(0) { // systemd uses UINT64_MAX for "not set"
		return 0
	}
	return n
}

func (s systemd) Detail(ctx context.Context, unit string) (*protocol.ServiceDetail, error) {
	if !reUnit.MatchString(unit) {
		return nil, fmt.Errorf("invalid unit name %q", unit)
	}
	if ok, why := s.available(ctx); !ok {
		return nil, fmt.Errorf("systemd unavailable: %s", why)
	}
	r := runCmd(ctx, 15*time.Second, nil, "systemctl", "show", unit, "--no-pager", "-p", strings.Join(showProps, ","))
	if r.ExitCode != 0 {
		return nil, fmt.Errorf("systemctl show %s: %s", unit, firstLine(r.Stderr))
	}
	raw := map[string]string{}
	for _, line := range strings.Split(r.Stdout, "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			raw[k] = v
		}
	}
	if raw["LoadState"] == "not-found" {
		return nil, fmt.Errorf("unit %s not found", unit)
	}
	d := &protocol.ServiceDetail{
		Service: protocol.Service{
			Unit: unit, Description: raw["Description"], LoadState: raw["LoadState"],
			ActiveState: raw["ActiveState"], SubState: raw["SubState"], UnitFile: raw["UnitFileState"],
		},
		Result: raw["Result"], Restart: raw["Restart"], FragmentPath: raw["FragmentPath"], Raw: raw,
		Requires: splitList(raw["Requires"]), Wants: splitList(raw["Wants"]), After: splitList(raw["After"]),
		RequiredBy: splitList(raw["RequiredBy"]), WantedBy: splitList(raw["WantedBy"]),
		ActiveEnter: parseSystemdTime(raw["ActiveEnterTimestamp"]), InactiveEnter: parseSystemdTime(raw["InactiveEnterTimestamp"]),
		MemoryCurrent: parseU64(raw["MemoryCurrent"]), CPUUsageNSec: parseU64(raw["CPUUsageNSec"]), TasksCurrent: parseU64(raw["TasksCurrent"]),
	}
	d.MainPID, _ = strconv.Atoi(raw["MainPID"])
	d.NRestarts, _ = strconv.Atoi(raw["NRestarts"])
	d.ExecMainStatus, _ = strconv.Atoi(raw["ExecMainStatus"])
	return d, nil
}

// AssessHealth combines unit state, restart history, recent error logs and
// listening sockets. A running process alone is never reported as healthy.
func AssessHealth(d *protocol.ServiceDetail, errorsLastHour int, listening []protocol.Socket) {
	d.RecentLogErrors = errorsLastHour
	d.ListeningPorts = listening
	var reasons []string
	switch {
	case d.ActiveState == "failed":
		d.HealthAssessed = "failed"
		reasons = append(reasons, fmt.Sprintf("unit is failed (result=%s, exit status %d)", d.Result, d.ExecMainStatus))
	case d.ActiveState == "inactive":
		d.HealthAssessed = "inactive"
		reasons = append(reasons, "unit is not running")
	case d.ActiveState == "activating" || d.ActiveState == "deactivating" || d.ActiveState == "reloading":
		d.HealthAssessed = "degraded"
		reasons = append(reasons, "unit is "+d.ActiveState)
	case d.ActiveState == "active":
		d.HealthAssessed = "healthy"
		reasons = append(reasons, "unit active ("+d.SubState+")")
		if d.NRestarts > 0 {
			d.HealthAssessed = "degraded"
			reasons = append(reasons, fmt.Sprintf("restarted %d times since last start of the unit", d.NRestarts))
		}
		if errorsLastHour > 0 {
			d.HealthAssessed = "degraded"
			reasons = append(reasons, fmt.Sprintf("%d error-level journal entries in the last hour", errorsLastHour))
		}
		if d.Result != "" && d.Result != "success" {
			d.HealthAssessed = "degraded"
			reasons = append(reasons, "last result: "+d.Result)
		}
		if len(listening) > 0 {
			reasons = append(reasons, fmt.Sprintf("owns %d listening socket(s)", len(listening)))
		}
	default:
		d.HealthAssessed = "unknown"
		reasons = append(reasons, "state "+d.ActiveState)
	}
	d.HealthReasons = reasons
}

func (s systemd) Dependencies(ctx context.Context, unit string, reverse bool) ([]string, error) {
	if !reUnit.MatchString(unit) {
		return nil, fmt.Errorf("invalid unit name %q", unit)
	}
	args := []string{"list-dependencies", "--plain", "--no-pager", "--no-legend"}
	if reverse {
		args = append(args, "--reverse")
	}
	r := runCmd(ctx, 15*time.Second, nil, "systemctl", append(args, unit)...)
	if r.ExitCode != 0 {
		return nil, fmt.Errorf("%s", firstLine(r.Stderr))
	}
	var out []string
	for i, line := range strings.Split(r.Stdout, "\n") {
		line = strings.TrimSpace(strings.TrimLeft(line, " │├└─●○"))
		if line == "" || i == 0 && line == unit {
			continue
		}
		out = append(out, line)
	}
	return out, nil
}
