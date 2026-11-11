package ai

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/WSEU790/MoistTux/internal/core/store"
	"github.com/WSEU790/MoistTux/internal/protocol"
)

var (
	reUnitArg      = regexp.MustCompile(`^[A-Za-z0-9@._:\\-]{1,200}$`)
	reContainerArg = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.\-]{0,127}$`)
	rePathArg      = regexp.MustCompile(`^/[^\x00]{0,1000}$`)
	reHostArg      = regexp.MustCompile(`^[A-Za-z0-9.\-:\[\]]{1,255}$`)
	reSinceArg     = regexp.MustCompile(`^(-\d{1,5}[smhd]|\d{4}-\d{2}-\d{2}T[\d:.]+(Z|[+-]\d{2}:\d{2}))$`)
	rePkgArg       = regexp.MustCompile(`^[a-z0-9][a-z0-9+.:\- ]*$`)
)

func normUnit(u string) string {
	if !strings.Contains(u, ".") {
		return u + ".service"
	}
	return u
}

func str(a map[string]any, k string) string { s, _ := a[k].(string); return s }
func num(a map[string]any, k string, def int) int {
	if v, ok := a[k].(int); ok {
		return v
	}
	return def
}

func round1(v float64) float64 { return float64(int64(v*10+0.5)) / 10 }

func human(b uint64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := uint64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

// BuildRegistry defines every tool the AI can use. Each maps to one typed
// agent endpoint; there is no generic command execution tool.
func BuildRegistry() *Registry {
	r := NewRegistry()

	// ------------------------------------------------------------ fleet (read)
	r.Add(&Tool{
		Name: "list_servers", Fleet: true, Risk: RiskRead,
		Description: "List every managed server with live status, environment, OS, current CPU/memory/disk usage and firing alert counts.",
		Exec: func(ctx context.Context, env *Env, _ map[string]any) (any, error) {
			servers, err := env.Deps.Store.ListServers(ctx)
			if err != nil {
				return nil, err
			}
			var out []map[string]any
			for _, s := range servers {
				row := map[string]any{"id": s.ID, "name": s.Name, "address": s.Address, "environment": s.Environment, "group": s.Group, "tags": s.Tags, "status": s.Status}
				if s.StatusReason != "" {
					row["status_reason"] = s.StatusReason
				}
				if s.Info != nil {
					row["os"] = s.Info.OSName
					row["kernel"] = s.Info.Kernel
				}
				if s.LastSeen != nil {
					row["last_seen"] = s.LastSeen.Format(time.RFC3339)
				}
				latest, at := env.Deps.Fleet.Latest(s.ID)
				if latest != nil {
					row["cpu_pct"] = latest.CPU.UsagePct
					row["mem_pct"] = latest.Mem.UsedPct
					maxFS := 0.0
					for _, f := range latest.FS {
						if f.UsedPct > maxFS {
							maxFS = f.UsedPct
						}
					}
					row["fullest_fs_pct"] = maxFS
					row["sampled_at"] = at.UTC().Format(time.RFC3339)
				}
				if al, err := env.Deps.Store.FiringForServer(ctx, s.ID); err == nil {
					c := map[string]int{}
					for _, a := range al {
						c[a.Severity]++
					}
					row["firing_alerts"] = c
				}
				out = append(out, row)
			}
			return map[string]any{"servers": out, "count": len(out)}, nil
		},
	})
	r.Add(&Tool{
		Name: "list_alerts", Fleet: true, Risk: RiskRead,
		Description: "List firing alerts (deterministic monitoring rules), optionally for one server, newest first.",
		Params:      []Param{{Name: "server", Type: "string", Description: "Optional server name or ID"}},
		Exec: func(ctx context.Context, env *Env, a map[string]any) (any, error) {
			id := ""
			if s := str(a, "server"); s != "" {
				srv, err := env.Deps.ResolveServer(ctx, s)
				if err != nil {
					return nil, err
				}
				id = srv.ID
			}
			al, err := env.Deps.Store.ListAlerts(ctx, "firing", id, 200)
			if err != nil {
				return nil, err
			}
			var out []map[string]any
			for _, x := range al {
				out = append(out, map[string]any{"server_id": x.ServerID, "rule": x.Rule, "severity": x.Severity, "title": x.Title, "detail": x.Detail, "first_seen": x.FirstSeen, "last_seen": x.LastSeen, "occurrences": x.Count, "incident_id": x.IncidentID})
			}
			return map[string]any{"alerts": out, "count": len(out)}, nil
		},
	})

	// ------------------------------------------------------------ metrics (read)
	r.Add(&Tool{
		Name: "get_system_metrics", Risk: RiskRead,
		Description: "Current system snapshot of a server: CPU, load, memory, swap, filesystems, disk I/O, network throughput, process counts, uptime and temperatures, plus OS facts.",
		Exec: func(ctx context.Context, env *Env, _ map[string]any) (any, error) {
			smp, err := latestSample(ctx, env)
			if err != nil {
				return nil, err
			}
			out := map[string]any{"sampled_at": smp.Time, "cpu": smp.CPU, "load": smp.Load, "memory": memSummary(smp.Mem), "filesystems": fsSummary(smp.FS), "disk_io": smp.DiskIO, "network": smp.Net, "processes": smp.Procs, "uptime_hours": round1(smp.Uptime / 3600), "tcp_established": smp.TCPConns}
			if len(smp.Temps) > 0 {
				out["temperatures"] = smp.Temps
			} else {
				out["temperatures"] = "no temperature sensors exposed by this host"
			}
			if s := env.Server; s.Info != nil {
				out["host"] = map[string]any{"hostname": s.Info.Hostname, "os": s.Info.OSName, "kernel": s.Info.Kernel, "arch": s.Info.Arch, "cpu_model": s.Info.CPUModel, "threads": s.Info.CPUThreads, "mem_total": human(s.Info.MemTotal), "virtualization": s.Info.Virtualization, "boot_time": s.Info.BootTime, "reboot_required": s.Info.RebootRequired}
			}
			return out, nil
		},
	})
	r.Add(&Tool{
		Name: "get_cpu_metrics", Risk: RiskRead,
		Description: "CPU utilisation breakdown (user/system/iowait/steal, per core), load averages and the last hour's CPU statistics.",
		Exec: func(ctx context.Context, env *Env, _ map[string]any) (any, error) {
			smp, err := latestSample(ctx, env)
			if err != nil {
				return nil, err
			}
			hist := historyStats(ctx, env, time.Hour, func(p store.Point) *float64 { return p.CPU })
			return map[string]any{"sampled_at": smp.Time, "cpu": smp.CPU, "load_1_5_15": smp.Load, "threads": smp.CPU.Threads, "last_hour": hist, "procs_running": smp.Procs.Running, "procs_blocked_on_io": smp.Procs.Blocked}, nil
		},
	})
	r.Add(&Tool{
		Name: "get_memory_metrics", Risk: RiskRead,
		Description: "Memory and swap usage now and over the last hour; use list_processes sort=mem to see consumers.",
		Exec: func(ctx context.Context, env *Env, _ map[string]any) (any, error) {
			smp, err := latestSample(ctx, env)
			if err != nil {
				return nil, err
			}
			return map[string]any{"sampled_at": smp.Time, "memory": memSummary(smp.Mem), "last_hour_mem_pct": historyStats(ctx, env, time.Hour, func(p store.Point) *float64 { return p.Mem }), "last_hour_swap_pct": historyStats(ctx, env, time.Hour, func(p store.Point) *float64 { return p.Swap })}, nil
		},
	})
	r.Add(&Tool{
		Name: "get_disk_metrics", Risk: RiskRead,
		Description: "Filesystem capacity and inode usage for every data filesystem, block devices, and current disk I/O rates.",
		Exec: func(ctx context.Context, env *Env, _ map[string]any) (any, error) {
			var ov protocol.StorageOverview
			if err := env.Client.Get(ctx, "/v1/storage", nil, &ov); err != nil {
				return nil, err
			}
			smp, _ := latestSample(ctx, env)
			out := map[string]any{"filesystems": fsSummary(ov.Filesystems), "block_devices": ov.BlockDevices}
			if smp != nil {
				out["disk_io"] = smp.DiskIO
				out["iowait_pct"] = smp.CPU.IOWaitPct
			}
			return out, nil
		},
	})
	r.Add(&Tool{
		Name: "get_network_metrics", Risk: RiskRead,
		Description: "Network interfaces, addresses, routes, DNS servers, per-interface throughput and error/drop counters.",
		Exec: func(ctx context.Context, env *Env, _ map[string]any) (any, error) {
			var ov protocol.NetworkOverview
			if err := env.Client.Get(ctx, "/v1/network", nil, &ov); err != nil {
				return nil, err
			}
			smp, _ := latestSample(ctx, env)
			out := map[string]any{"interfaces": ov.Interfaces, "routes": ov.Routes, "dns_servers": ov.DNS, "tcp_established": ov.Established}
			if smp != nil {
				out["throughput"] = smp.Net
			}
			return out, nil
		},
	})
	r.Add(&Tool{
		Name: "get_metrics_history", Risk: RiskRead,
		Description: "Historical statistics (min/avg/max, first and last value, peak time) for a metric over the past N hours from NEXUS's telemetry store.",
		Params: []Param{
			{Name: "metric", Type: "string", Required: true, Enum: []string{"cpu", "mem", "swap", "load1", "disk_max", "rx_bps", "tx_bps", "read_bps", "write_bps", "iowait", "tcp", "temp_max"}, Description: "Metric name"},
			{Name: "hours", Type: "number", Min: fptr(0.1), Max: fptr(720), Default: 24.0, Description: "Look-back window in hours"},
		},
		Exec: func(ctx context.Context, env *Env, a map[string]any) (any, error) {
			h, _ := a["hours"].(float64)
			metric := str(a, "metric")
			get := pointGetter(metric)
			pts, res, err := env.Deps.Store.QueryTelemetry(ctx, env.Server.ID, time.Now().Add(-time.Duration(h*float64(time.Hour))), time.Now(), 2000)
			if err != nil {
				return nil, err
			}
			st := statsOf(pts, get)
			st["resolution"] = res
			st["metric"] = metric
			st["hours"] = h
			return st, nil
		},
	})

	// ------------------------------------------------------------ processes (read)
	r.Add(&Tool{
		Name: "list_processes", Risk: RiskRead,
		Description: "Top processes by CPU or memory with user, RSS, CPU% measured over a real sampling window, systemd unit and container.",
		Params: []Param{
			{Name: "sort", Type: "string", Enum: []string{"cpu", "mem"}, Default: "cpu", Description: "Sort order"},
			{Name: "limit", Type: "integer", Min: fptr(1), Max: fptr(100), Default: 25, Description: "Number of processes"},
		},
		Exec: func(ctx context.Context, env *Env, a map[string]any) (any, error) {
			var pl protocol.ProcessList
			err := env.Client.Get(ctx, "/v1/processes", url.Values{"sort": {str(a, "sort")}, "limit": {strconv.Itoa(num(a, "limit", 25))}}, &pl)
			if err != nil {
				return nil, err
			}
			rows := make([]map[string]any, 0, len(pl.Processes))
			for _, p := range pl.Processes {
				cmd := p.Cmdline
				if len(cmd) > 160 {
					cmd = cmd[:160] + "…"
				}
				row := map[string]any{"pid": p.PID, "name": p.Name, "user": p.User, "cpu_pct": p.CPUPct, "rss": human(p.RSS), "mem_pct": p.MemPct, "state": p.State, "cmd": cmd, "started": p.StartTime.Format(time.RFC3339)}
				if p.Unit != "" {
					row["unit"] = p.Unit
				}
				if p.Container != "" {
					row["container"] = p.Container
				}
				rows = append(rows, row)
			}
			return map[string]any{"processes": rows, "total_processes": pl.Total, "cpu_window_ms": pl.WindowMs, "note": pl.Partial}, nil
		},
	})
	r.Add(&Tool{
		Name: "inspect_process", Risk: RiskRead,
		Description: "Detailed information for one PID: command line, executable, status fields, open file descriptors, limits, I/O, children, listening sockets, cgroup.",
		Params:      []Param{{Name: "pid", Type: "integer", Required: true, Min: fptr(1), Description: "Process ID"}},
		Exec: func(ctx context.Context, env *Env, a map[string]any) (any, error) {
			var d protocol.ProcessDetail
			return &d, env.Client.Get(ctx, "/v1/processes/"+strconv.Itoa(num(a, "pid", 0)), nil, &d)
		},
	})

	// ------------------------------------------------------------ services
	r.Add(&Tool{
		Name: "list_services", Risk: RiskRead, Capability: "services",
		Description: "systemd services with load/active/sub state and enablement. Filter by state.",
		Params:      []Param{{Name: "state", Type: "string", Enum: []string{"all", "failed", "running", "inactive"}, Default: "all", Description: "Filter"}},
		Exec: func(ctx context.Context, env *Env, a map[string]any) (any, error) {
			var sl protocol.ServiceList
			if err := env.Client.Get(ctx, "/v1/services", nil, &sl); err != nil {
				return nil, err
			}
			if !sl.Available {
				return nil, fmt.Errorf("%s", sl.Unavailable)
			}
			f := str(a, "state")
			var out []protocol.Service
			for _, s := range sl.Services {
				switch {
				case f == "failed" && s.ActiveState != "failed",
					f == "running" && s.SubState != "running",
					f == "inactive" && s.ActiveState != "inactive":
					continue
				}
				out = append(out, s)
			}
			if f == "all" && len(out) > 150 {
				// Keep the model's context focused: drop inactive static units.
				var trimmed []protocol.Service
				for _, s := range out {
					if s.ActiveState != "inactive" || s.UnitFile == "enabled" {
						trimmed = append(trimmed, s)
					}
				}
				out = trimmed
			}
			return map[string]any{"services": out, "failed": sl.Failed, "running": sl.Running, "returned": len(out)}, nil
		},
	})
	r.Add(&Tool{
		Name: "inspect_service", Risk: RiskRead, Capability: "services",
		Description: "Full status of a systemd unit with a health assessment (state, result, restart count, recent error logs, listening sockets), restart policy and dependencies.",
		Params:      []Param{{Name: "unit", Type: "string", Required: true, Pattern: reUnitArg, Description: "Unit name, e.g. nginx or nginx.service"}},
		Exec: func(ctx context.Context, env *Env, a map[string]any) (any, error) {
			var d protocol.ServiceDetail
			if err := env.Client.Get(ctx, "/v1/services/"+url.PathEscape(normUnit(str(a, "unit"))), nil, &d); err != nil {
				return nil, err
			}
			d.Raw = nil
			return &d, nil
		},
	})
	r.Add(&Tool{
		Name: "get_service_logs", Risk: RiskRead,
		Description: "Journal (or syslog) entries for one service, newest first. Priority 3 = errors, 4 = warnings and worse.",
		Params: []Param{
			{Name: "unit", Type: "string", Required: true, Pattern: reUnitArg, Description: "Unit name"},
			{Name: "since", Type: "string", Pattern: reSinceArg, Default: "-24h", Description: "Relative (-30m, -24h, -7d) or RFC3339"},
			{Name: "priority", Type: "integer", Min: fptr(0), Max: fptr(7), Default: 0, Description: "Max priority (0 = any)"},
			{Name: "grep", Type: "string", MaxLen: 200, Description: "Case-insensitive regex filter"},
			{Name: "limit", Type: "integer", Min: fptr(1), Max: fptr(400), Default: 120, Description: "Max entries"},
		},
		Exec: func(ctx context.Context, env *Env, a map[string]any) (any, error) {
			return queryLogs(ctx, env, normUnit(str(a, "unit")), a)
		},
	})
	r.Add(&Tool{
		Name: "search_system_logs", Risk: RiskRead,
		Description: "Search the whole system journal (or syslog) by regex, time window and priority.",
		Params: []Param{
			{Name: "grep", Type: "string", MaxLen: 200, Description: "Case-insensitive regex filter"},
			{Name: "since", Type: "string", Pattern: reSinceArg, Default: "-24h", Description: "Relative (-30m, -24h) or RFC3339"},
			{Name: "priority", Type: "integer", Min: fptr(0), Max: fptr(7), Default: 0, Description: "Max priority (0 = any, 3 = errors)"},
			{Name: "limit", Type: "integer", Min: fptr(1), Max: fptr(400), Default: 150, Description: "Max entries"},
		},
		Exec: func(ctx context.Context, env *Env, a map[string]any) (any, error) { return queryLogs(ctx, env, "", a) },
	})
	serviceAction := func(name, action string, risk Risk, desc string, effects []string, rollback string) {
		r.Add(&Tool{
			Name: name, Risk: risk, Capability: "services", Timeout: 3 * time.Minute,
			Description: desc,
			Params:      []Param{{Name: "unit", Type: "string", Required: true, Pattern: reUnitArg, Description: "Unit name"}},
			Describe:    func(a map[string]any) string { return fmt.Sprintf("%s %s", action, normUnit(str(a, "unit"))) },
			Preview: func(ctx context.Context, env *Env, a map[string]any) (*Impact, error) {
				unit := normUnit(str(a, "unit"))
				var d protocol.ServiceDetail
				if err := env.Client.Get(ctx, "/v1/services/"+url.PathEscape(unit), nil, &d); err != nil {
					return nil, err
				}
				var deps struct {
					RequiredBy []string `json:"required_by"`
				}
				_ = env.Client.Get(ctx, "/v1/services/"+url.PathEscape(unit)+"/dependencies", nil, &deps)
				imp := &Impact{
					Summary:  fmt.Sprintf("%s %s on %s (currently %s/%s, health %s)", strings.Title(action), unit, env.Server.Name, d.ActiveState, d.SubState, d.HealthAssessed),
					Effects:  effects,
					Rollback: rollback,
					Evidence: map[string]any{"active_state": d.ActiveState, "sub_state": d.SubState, "main_pid": d.MainPID, "n_restarts": d.NRestarts, "listening": d.ListeningPorts, "dependents": deps.RequiredBy},
				}
				if len(deps.RequiredBy) > 1 {
					imp.Risks = append(imp.Risks, fmt.Sprintf("%d units depend on %s and may be affected: %s", len(deps.RequiredBy), unit, strings.Join(firstN(deps.RequiredBy, 8), ", ")))
				}
				if len(d.ListeningPorts) > 0 && (action == "restart" || action == "stop") {
					imp.Risks = append(imp.Risks, fmt.Sprintf("connections on %d listening socket(s) will be interrupted", len(d.ListeningPorts)))
				}
				if env.Server.Environment == "production" {
					imp.Risks = append(imp.Risks, "this is a production server")
				}
				return imp, nil
			},
			Exec: func(ctx context.Context, env *Env, a map[string]any) (any, error) {
				var res protocol.ActionResult
				if err := env.Client.Post(ctx, "/v1/services/"+url.PathEscape(normUnit(str(a, "unit")))+"/"+action, nil, &res); err != nil {
					return nil, err
				}
				return verifyService(&res, action), nil
			},
		})
	}
	serviceAction("restart_service", "restart", RiskMedium, "Restart a systemd service and verify its state afterwards.", []string{"the service process is stopped and started again", "in-flight requests may fail during the restart"}, "If the service fails to start, inspect logs; the previous process cannot be restored, but the unit can be started again.")
	serviceAction("start_service", "start", RiskMedium, "Start a stopped systemd service and verify it is running.", []string{"the service starts and may bind ports or consume resources"}, "Stop the service again with stop_service.")
	serviceAction("stop_service", "stop", RiskMedium, "Stop a systemd service and verify it stopped.", []string{"the service becomes unavailable until started again"}, "Start the service again with start_service.")
	serviceAction("reload_service", "reload", RiskLow, "Ask a systemd service to reload its configuration without a full restart (only for units that support reload).", []string{"the service re-reads its configuration; invalid configuration may cause errors"}, "Revert the configuration change and reload again.")

	// ------------------------------------------------------------ docker
	r.Add(&Tool{
		Name: "list_docker_containers", Risk: RiskRead, Capability: "docker",
		Description: "All Docker containers with state, health, image, ports, compose project and networks.",
		Exec: func(ctx context.Context, env *Env, _ map[string]any) (any, error) {
			var cs []protocol.Container
			if err := env.Client.Get(ctx, "/v1/docker/containers", nil, &cs); err != nil {
				return nil, err
			}
			for i := range cs {
				cs[i].Labels = nil
			}
			return map[string]any{"containers": cs, "count": len(cs)}, nil
		},
	})
	r.Add(&Tool{
		Name: "inspect_container", Risk: RiskRead, Capability: "docker",
		Description: "Container details: state, exit code, OOM kill flag, restart count and policy, health-check log, mounts, ports and live resource usage. Environment values are never returned.",
		Params:      []Param{{Name: "container", Type: "string", Required: true, Pattern: reContainerArg, Description: "Container name or ID"}},
		Exec: func(ctx context.Context, env *Env, a map[string]any) (any, error) {
			var d protocol.ContainerDetail
			if err := env.Client.Get(ctx, "/v1/docker/containers/"+str(a, "container"), nil, &d); err != nil {
				return nil, err
			}
			d.Labels = nil
			return &d, nil
		},
	})
	r.Add(&Tool{
		Name: "get_container_logs", Risk: RiskRead, Capability: "docker",
		Description: "Recent stdout/stderr lines from a container.",
		Params: []Param{
			{Name: "container", Type: "string", Required: true, Pattern: reContainerArg, Description: "Container name or ID"},
			{Name: "tail", Type: "integer", Min: fptr(1), Max: fptr(500), Default: 150, Description: "Lines"},
		},
		Exec: func(ctx context.Context, env *Env, a map[string]any) (any, error) {
			var lr protocol.LogResult
			if err := env.Client.Get(ctx, "/v1/docker/containers/"+str(a, "container")+"/logs", url.Values{"tail": {strconv.Itoa(num(a, "tail", 150))}}, &lr); err != nil {
				return nil, err
			}
			return compactLogs(lr, 150), nil
		},
	})
	containerAction := func(name, action string, desc string) {
		r.Add(&Tool{
			Name: name, Risk: RiskMedium, Capability: "docker", Timeout: 2 * time.Minute,
			Description: desc,
			Params:      []Param{{Name: "container", Type: "string", Required: true, Pattern: reContainerArg, Description: "Container name or ID"}},
			Describe:    func(a map[string]any) string { return action + " container " + str(a, "container") },
			Preview: func(ctx context.Context, env *Env, a map[string]any) (*Impact, error) {
				var d protocol.ContainerDetail
				if err := env.Client.Get(ctx, "/v1/docker/containers/"+str(a, "container"), url.Values{"stats": {"0"}}, &d); err != nil {
					return nil, err
				}
				imp := &Impact{
					Summary:  fmt.Sprintf("%s container %s (%s) on %s — currently %s", strings.Title(action), d.Name, d.Image, env.Server.Name, d.State),
					Effects:  []string{"processes inside the container are " + map[string]string{"restart": "stopped (20 s grace) and started again", "start": "started", "stop": "stopped with a 20 s grace period"}[action]},
					Rollback: "Containers keep their configuration and volumes; the opposite action restores the previous running state.",
					Evidence: map[string]any{"state": d.State, "health": d.Health, "restart_count": d.RestartCount, "restart_policy": d.RestartPolicy, "ports": d.Ports, "compose_project": d.ComposeProject},
				}
				if len(d.Ports) > 0 {
					imp.Risks = append(imp.Risks, "published ports will be unavailable while the container is down")
				}
				if d.ComposeProject != "" {
					imp.Risks = append(imp.Risks, "part of compose project "+d.ComposeProject+"; dependent services may be affected")
				}
				return imp, nil
			},
			Exec: func(ctx context.Context, env *Env, a map[string]any) (any, error) {
				var res protocol.ActionResult
				if err := env.Client.Post(ctx, "/v1/docker/containers/"+str(a, "container")+"/"+action, nil, &res); err != nil {
					return nil, err
				}
				return verifyContainer(&res, action), nil
			},
		})
	}
	containerAction("restart_container", "restart", "Restart a Docker container and verify its state afterwards.")
	containerAction("start_container", "start", "Start a stopped Docker container and verify it is running.")
	containerAction("stop_container", "stop", "Stop a running Docker container and verify it stopped.")

	// ------------------------------------------------------------ storage (read)
	r.Add(&Tool{
		Name: "inspect_disk_usage", Risk: RiskRead, Timeout: 2 * time.Minute,
		Description: "Directory size breakdown below a path (does not cross filesystems). Use privileged=true to include root-only directories.",
		Params: []Param{
			{Name: "path", Type: "string", Required: true, Pattern: rePathArg, Description: "Absolute directory path"},
			{Name: "depth", Type: "integer", Min: fptr(0), Max: fptr(3), Default: 1, Description: "Levels of children to return"},
			{Name: "privileged", Type: "boolean", Default: true, Description: "Scan with the privileged helper (read-only)"},
		},
		Exec: func(ctx context.Context, env *Env, a map[string]any) (any, error) {
			q := url.Values{"path": {str(a, "path")}, "depth": {strconv.Itoa(num(a, "depth", 1))}}
			if p, _ := a["privileged"].(bool); p {
				q.Set("privileged", "1")
			}
			var res protocol.DirUsageResult
			if err := env.Client.Get(ctx, "/v1/storage/usage", q, &res); err != nil {
				return nil, err
			}
			return summariseDU(res), nil
		},
	})
	r.Add(&Tool{
		Name: "find_large_files", Risk: RiskRead, Timeout: 2 * time.Minute,
		Description: "Largest regular files under a path. Reporting only — never treat size alone as a reason to delete.",
		Params: []Param{
			{Name: "path", Type: "string", Required: true, Pattern: rePathArg, Description: "Absolute directory path"},
			{Name: "min_mb", Type: "integer", Min: fptr(1), Max: fptr(1 << 20), Default: 100, Description: "Minimum size in MiB"},
			{Name: "limit", Type: "integer", Min: fptr(1), Max: fptr(100), Default: 25, Description: "Max files"},
			{Name: "privileged", Type: "boolean", Default: true, Description: "Scan with the privileged helper (read-only)"},
		},
		Exec: func(ctx context.Context, env *Env, a map[string]any) (any, error) {
			q := url.Values{"path": {str(a, "path")}, "min_mb": {strconv.Itoa(num(a, "min_mb", 100))}, "limit": {strconv.Itoa(num(a, "limit", 25))}}
			if p, _ := a["privileged"].(bool); p {
				q.Set("privileged", "1")
			}
			var res protocol.LargeFilesResult
			if err := env.Client.Get(ctx, "/v1/storage/large-files", q, &res); err != nil {
				return nil, err
			}
			files := make([]map[string]any, 0, len(res.Files))
			for _, f := range res.Files {
				files = append(files, map[string]any{"path": f.Path, "size": human(f.Size), "bytes": f.Size, "modified": f.ModTime.Format("2006-01-02"), "owner": f.Owner})
			}
			return map[string]any{"files": files, "files_scanned": res.Scanned, "errors": res.Errors, "truncated": res.Truncated}, nil
		},
	})

	// ------------------------------------------------------------ packages
	r.Add(&Tool{
		Name: "list_installed_packages", Risk: RiskRead, Capability: "packages:apt",
		Description: "Installed packages, optionally filtered by name/summary substring.",
		Params:      []Param{{Name: "query", Type: "string", MaxLen: 80, Description: "Substring filter"}},
		Exec: func(ctx context.Context, env *Env, a map[string]any) (any, error) {
			var pl protocol.PackageList
			if err := env.Client.Get(ctx, "/v1/packages", url.Values{"q": {str(a, "query")}}, &pl); err != nil {
				return nil, err
			}
			pk := pl.Packages
			if len(pk) > 200 {
				pk = pk[:200]
			}
			return map[string]any{"packages": pk, "matched": len(pl.Packages), "manager": pl.Manager}, nil
		},
	})
	r.Add(&Tool{
		Name: "check_package_updates", Risk: RiskRead, Capability: "packages:apt", Timeout: 2 * time.Minute,
		Description: "Pending package updates from a no-op simulation, with security classification, kernel updates, kept-back packages, package-list age and reboot-required status.",
		Exec: func(ctx context.Context, env *Env, _ map[string]any) (any, error) {
			var u protocol.UpdateCheck
			return &u, env.Client.Get(ctx, "/v1/packages/updates", nil, &u)
		},
	})
	r.Add(&Tool{
		Name: "refresh_package_lists", Risk: RiskLow, Capability: "packages:apt", Timeout: 11 * time.Minute,
		Description: "Download fresh package indexes (apt-get update). Changes no installed software.",
		Describe:    func(map[string]any) string { return "refresh package lists (apt-get update)" },
		Preview: func(ctx context.Context, env *Env, _ map[string]any) (*Impact, error) {
			return &Impact{Summary: "Run apt-get update on " + env.Server.Name, Effects: []string{"package index files under /var/lib/apt/lists are refreshed from the configured repositories"}, Rollback: "Not needed; no installed package changes."}, nil
		},
		Exec: func(ctx context.Context, env *Env, _ map[string]any) (any, error) {
			var res protocol.ActionResult
			if err := env.Client.Post(ctx, "/v1/packages/refresh", nil, &res); err != nil {
				return nil, err
			}
			return trimCommand(&res), nil
		},
	})
	r.Add(&Tool{
		Name: "apply_security_updates", Risk: RiskMedium, Capability: "packages:apt", Timeout: 46 * time.Minute,
		Description: "Install pending updates that come from security repositories, then re-check updates to verify.",
		Describe:    func(map[string]any) string { return "install pending security updates" },
		Preview:     func(ctx context.Context, env *Env, _ map[string]any) (*Impact, error) { return updatePreview(ctx, env, nil, true) },
		Exec: func(ctx context.Context, env *Env, _ map[string]any) (any, error) {
			var res protocol.ActionResult
			if err := env.Client.Post(ctx, "/v1/packages/upgrade", protocol.UpgradeRequest{SecurityOnly: true}, &res); err != nil {
				return nil, err
			}
			return trimCommand(&res), nil
		},
	})
	r.Add(&Tool{
		Name: "apply_package_updates", Risk: RiskMedium, Capability: "packages:apt", Timeout: 46 * time.Minute,
		Description: "Upgrade specific installed packages (space separated names) to their candidate versions, then re-check.",
		Params:      []Param{{Name: "packages", Type: "string", Required: true, Pattern: rePkgArg, MaxLen: 4000, Description: "Space-separated package names"}},
		Describe:    func(a map[string]any) string { return "upgrade packages: " + str(a, "packages") },
		Preview: func(ctx context.Context, env *Env, a map[string]any) (*Impact, error) {
			return updatePreview(ctx, env, strings.Fields(str(a, "packages")), false)
		},
		Exec: func(ctx context.Context, env *Env, a map[string]any) (any, error) {
			var res protocol.ActionResult
			if err := env.Client.Post(ctx, "/v1/packages/upgrade", protocol.UpgradeRequest{Packages: strings.Fields(str(a, "packages"))}, &res); err != nil {
				return nil, err
			}
			return trimCommand(&res), nil
		},
	})

	// ------------------------------------------------------------ network & security (read)
	r.Add(&Tool{
		Name: "inspect_open_ports", Risk: RiskRead,
		Description: "Listening TCP/UDP sockets with owning process and bind scope. Bind scope alone does not prove public exposure; check the firewall too.",
		Exec: func(ctx context.Context, env *Env, _ map[string]any) (any, error) {
			var ov protocol.NetworkOverview
			if err := env.Client.Get(ctx, "/v1/network", nil, &ov); err != nil {
				return nil, err
			}
			return map[string]any{"listening": ov.Listening, "note": strings.TrimSpace(ov.Partial + " 'all-interfaces' means the socket accepts connections on every local address; whether it is reachable from the internet depends on firewall rules and upstream NAT.")}, nil
		},
	})
	r.Add(&Tool{
		Name: "inspect_firewall", Risk: RiskRead,
		Description: "Active host firewall (ufw, nftables or iptables), default policies and rules. Read-only.",
		Exec: func(ctx context.Context, env *Env, _ map[string]any) (any, error) {
			var fw protocol.FirewallStatus
			if err := env.Client.Get(ctx, "/v1/network/firewall", nil, &fw); err != nil {
				return nil, err
			}
			if len(fw.Raw) > 6000 {
				fw.Raw = fw.Raw[:6000] + "\n[truncated]"
			}
			return &fw, nil
		},
	})
	r.Add(&Tool{
		Name: "inspect_system_users", Risk: RiskRead,
		Description: "Local accounts with UID, groups, shell, login capability and sudo/wheel membership.",
		Exec: func(ctx context.Context, env *Env, _ map[string]any) (any, error) {
			var us []protocol.SystemUser
			if err := env.Client.Get(ctx, "/v1/security/users", nil, &us); err != nil {
				return nil, err
			}
			var human, system int
			var keep []protocol.SystemUser
			for _, u := range us {
				if u.System && !u.CanLogin {
					system++
					continue
				}
				human++
				keep = append(keep, u)
			}
			return map[string]any{"accounts": keep, "login_capable_or_regular": human, "system_nologin_accounts_omitted": system}, nil
		},
	})
	r.Add(&Tool{
		Name: "inspect_ssh_configuration", Risk: RiskRead,
		Description: "Effective OpenSSH server configuration (sshd -T) with security findings.",
		Exec: func(ctx context.Context, env *Env, _ map[string]any) (any, error) {
			var c protocol.SSHConfig
			if err := env.Client.Get(ctx, "/v1/security/ssh", nil, &c); err != nil {
				return nil, err
			}
			keep := map[string]string{}
			for _, k := range []string{"port", "listenaddress", "permitrootlogin", "passwordauthentication", "pubkeyauthentication", "kbdinteractiveauthentication", "permitemptypasswords", "maxauthtries", "x11forwarding", "allowusers", "allowgroups", "logingracetime", "usepam", "clientaliveinterval"} {
				if v, ok := c.Settings[k]; ok {
					keep[k] = v
				}
			}
			return map[string]any{"source": c.Source, "settings": keep, "findings": c.Findings}, nil
		},
	})
	r.Add(&Tool{
		Name: "inspect_failed_logins", Risk: RiskRead,
		Description: "SSH authentication failures, successful logins and sudo activity over the past N hours, aggregated by source address and user.",
		Params:      []Param{{Name: "hours", Type: "integer", Min: fptr(1), Max: fptr(720), Default: 24, Description: "Look-back hours"}},
		Exec: func(ctx context.Context, env *Env, a map[string]any) (any, error) {
			var s protocol.AuthSummary
			if err := env.Client.Get(ctx, "/v1/security/auth", url.Values{"hours": {strconv.Itoa(num(a, "hours", 24))}}, &s); err != nil {
				return nil, err
			}
			ev := s.Events
			if len(ev) > 40 {
				ev = ev[:40]
			}
			for i := range ev {
				ev[i].Raw = ""
			}
			return map[string]any{"source": s.Source, "since": s.Since, "failed": s.Failed, "accepted": s.Accepted, "sudo": s.Sudo, "top_failed_sources": topN(s.FailedBySrc, 15), "top_failed_users": topN(s.FailedByUser, 15), "recent_events": ev, "note": s.Note}, nil
		},
	})
	r.Add(&Tool{
		Name: "security_posture", Risk: RiskRead, Timeout: 2 * time.Minute,
		Description: "Evidence-backed security findings (SSH, firewall, privileged accounts, sensitive file permissions, pending security updates, reboot status) and installed security tools.",
		Exec: func(ctx context.Context, env *Env, _ map[string]any) (any, error) {
			var p protocol.SecurityPosture
			return &p, env.Client.Get(ctx, "/v1/security/posture", nil, &p)
		},
	})
	r.Add(&Tool{
		Name: "check_ssl_certificate", Risk: RiskRead,
		Description: "Connect from the server to host:port over TLS and report certificate subject, issuer, expiry and days remaining.",
		Params: []Param{
			{Name: "host", Type: "string", Required: true, Pattern: reHostArg, Description: "Hostname to check (SNI)"},
			{Name: "port", Type: "integer", Min: fptr(1), Max: fptr(65535), Default: 443, Description: "Port"},
		},
		Exec: func(ctx context.Context, env *Env, a map[string]any) (any, error) {
			var pr protocol.ProbeResult
			err := env.Client.Post(ctx, "/v1/probe", protocol.ProbeRequest{Kind: "tls", Target: fmt.Sprintf("%s:%d", str(a, "host"), num(a, "port", 443))}, &pr)
			return &pr, err
		},
	})
	r.Add(&Tool{
		Name: "probe_endpoint", Risk: RiskRead,
		Description: "Active health check from the server itself: TCP connect, or HTTP(S) GET with optional expected status. Use this to confirm a service actually responds.",
		Params: []Param{
			{Name: "kind", Type: "string", Required: true, Enum: []string{"tcp", "http"}, Description: "Probe type"},
			{Name: "target", Type: "string", Required: true, MaxLen: 300, Description: "host:port for tcp, URL for http"},
			{Name: "expect_status", Type: "integer", Min: fptr(100), Max: fptr(599), Description: "Expected HTTP status"},
		},
		Exec: func(ctx context.Context, env *Env, a map[string]any) (any, error) {
			var pr protocol.ProbeResult
			err := env.Client.Post(ctx, "/v1/probe", protocol.ProbeRequest{Kind: str(a, "kind"), Target: str(a, "target"), ExpectStatus: num(a, "expect_status", 0)}, &pr)
			return &pr, err
		},
	})

	// ------------------------------------------------------------ high risk
	r.Add(&Tool{
		Name: "reboot_server", Risk: RiskHigh, Timeout: time.Minute,
		Description: "Reboot the server. Always requires explicit approval with a typed confirmation; only offered when the server's policy allows reboots.",
		Params:      []Param{{Name: "reason", Type: "string", Required: true, MaxLen: 300, Description: "Why the reboot is needed"}},
		Describe:    func(a map[string]any) string { return "reboot the server: " + str(a, "reason") },
		Preview: func(ctx context.Context, env *Env, a map[string]any) (*Impact, error) {
			host := env.Server.Name
			if env.Server.Info != nil {
				host = env.Server.Info.Hostname
			}
			imp := &Impact{Summary: "Reboot " + env.Server.Name + " (" + host + ")", Confirm: host,
				Effects:  []string{"every service and container on the host stops and restarts", "active SSH sessions and client connections are dropped"},
				Rollback: "A reboot cannot be undone. If the host fails to come back, console access through your provider is required."}
			if smp, err := latestSample(ctx, env); err == nil {
				imp.Evidence = map[string]any{"uptime_hours": round1(smp.Uptime / 3600), "tcp_established": smp.TCPConns, "processes": smp.Procs.Total}
			}
			var cs []protocol.Container
			if env.Client.Get(ctx, "/v1/docker/containers", nil, &cs) == nil {
				running := 0
				for _, c := range cs {
					if c.State == "running" {
						running++
					}
				}
				imp.Risks = append(imp.Risks, fmt.Sprintf("%d running containers will restart only if their restart policy says so", running))
			}
			if env.Server.Environment == "production" {
				imp.Risks = append(imp.Risks, "this is a production server")
			}
			return imp, nil
		},
		Exec: func(ctx context.Context, env *Env, _ map[string]any) (any, error) {
			host := env.Server.Name
			if env.Server.Info != nil {
				host = env.Server.Info.Hostname
			}
			var res protocol.ActionResult
			err := env.Client.Post(ctx, "/v1/system/reboot", map[string]string{"confirm": host}, &res)
			return &res, err
		},
	})

	// ------------------------------------------------------------ memory (internal)
	r.Add(&Tool{
		Name: "remember_fact", Fleet: true, Risk: RiskLow, Internal: true,
		Description: "Store a durable fact about the infrastructure (server role, important service, dependency, custom config path, constraint, maintenance window, resolved incident) for future conversations. Do not store transient metrics.",
		Params: []Param{
			{Name: "server", Type: "string", Description: "Server name or ID (omit for fleet-wide facts)"},
			{Name: "kind", Type: "string", Required: true, Enum: []string{"role", "service", "dependency", "config_path", "constraint", "maintenance_window", "incident", "note"}, Description: "Category"},
			{Name: "content", Type: "string", Required: true, MaxLen: 600, Description: "The fact, stated precisely"},
		},
		Exec: func(ctx context.Context, env *Env, a map[string]any) (any, error) {
			m := &store.Memory{Kind: str(a, "kind"), Content: str(a, "content"), Source: "ai"}
			if s := str(a, "server"); s != "" {
				srv, err := env.Deps.ResolveServer(ctx, s)
				if err != nil {
					return nil, err
				}
				m.ServerID = srv.ID
			}
			if err := env.Deps.Store.PutMemory(ctx, m); err != nil {
				return nil, err
			}
			return map[string]any{"stored": true, "id": m.ID, "note": "the operator can review or delete this in Memory"}, nil
		},
	})
	return r
}

// ---------------------------------------------------------------- helpers

func latestSample(ctx context.Context, env *Env) (*protocol.MetricsSample, error) {
	// Always fetch live from the agent so decisions never rest on stale data.
	b, err := env.Client.Metrics(ctx, 0)
	if err != nil {
		return nil, err
	}
	if len(b.Samples) == 0 {
		return nil, fmt.Errorf("the agent has not produced a metrics sample yet")
	}
	s := b.Samples[len(b.Samples)-1]
	return &s, nil
}

func memSummary(m protocol.MemStats) map[string]any {
	return map[string]any{"total": human(m.Total), "used": human(m.Used), "available": human(m.Available), "used_pct": m.UsedPct, "buffers": human(m.Buffers), "cache": human(m.Cached), "swap_total": human(m.SwapTotal), "swap_used": human(m.SwapUsed), "swap_pct": m.SwapPct}
}

func fsSummary(fs []protocol.FSUsage) []map[string]any {
	out := make([]map[string]any, 0, len(fs))
	for _, f := range fs {
		out = append(out, map[string]any{"mount": f.Mount, "device": f.Device, "fstype": f.FSType, "size": human(f.Total), "used": human(f.Used), "free": human(f.Avail), "used_pct": f.UsedPct, "inodes_pct": f.InodesPct, "read_only": f.ReadOnly})
	}
	return out
}

func pointGetter(metric string) func(store.Point) *float64 {
	switch metric {
	case "mem":
		return func(p store.Point) *float64 { return p.Mem }
	case "swap":
		return func(p store.Point) *float64 { return p.Swap }
	case "load1":
		return func(p store.Point) *float64 { return p.Load1 }
	case "disk_max":
		return func(p store.Point) *float64 { return p.DiskMax }
	case "rx_bps":
		return func(p store.Point) *float64 { return p.RxBps }
	case "tx_bps":
		return func(p store.Point) *float64 { return p.TxBps }
	case "read_bps":
		return func(p store.Point) *float64 { return p.ReadBps }
	case "write_bps":
		return func(p store.Point) *float64 { return p.WriteBps }
	case "iowait":
		return func(p store.Point) *float64 { return p.IOWait }
	case "temp_max":
		return func(p store.Point) *float64 { return p.TempMax }
	case "tcp":
		return func(p store.Point) *float64 {
			if p.TCP == nil {
				return nil
			}
			v := float64(*p.TCP)
			return &v
		}
	}
	return func(p store.Point) *float64 { return p.CPU }
}

func statsOf(pts []store.Point, get func(store.Point) *float64) map[string]any {
	var n int
	var sum, minV, maxV float64
	var first, last *float64
	var peakT int64
	for _, p := range pts {
		v := get(p)
		if v == nil {
			continue
		}
		if first == nil {
			first = v
			minV, maxV = *v, *v
		}
		last = v
		n++
		sum += *v
		if *v < minV {
			minV = *v
		}
		if *v >= maxV {
			maxV, peakT = *v, p.T
		}
	}
	if n == 0 {
		return map[string]any{"samples": 0, "note": "no telemetry recorded for this window"}
	}
	return map[string]any{"samples": n, "min": round1(minV), "avg": round1(sum / float64(n)), "max": round1(maxV), "first": round1(*first), "last": round1(*last), "peak_at": time.UnixMilli(peakT).UTC().Format(time.RFC3339)}
}

func historyStats(ctx context.Context, env *Env, d time.Duration, get func(store.Point) *float64) map[string]any {
	pts, _, err := env.Deps.Store.QueryTelemetry(ctx, env.Server.ID, time.Now().Add(-d), time.Now(), 2000)
	if err != nil {
		return map[string]any{"error": err.Error()}
	}
	return statsOf(pts, get)
}

func queryLogs(ctx context.Context, env *Env, unit string, a map[string]any) (any, error) {
	q := url.Values{"since": {str(a, "since")}, "limit": {strconv.Itoa(num(a, "limit", 120))}}
	if unit != "" {
		q.Set("unit", unit)
	}
	if p := num(a, "priority", 0); p > 0 {
		q.Set("priority", strconv.Itoa(p))
	}
	if g := str(a, "grep"); g != "" {
		q.Set("grep", g)
	}
	var lr protocol.LogResult
	if err := env.Client.Get(ctx, "/v1/logs", q, &lr); err != nil {
		return nil, err
	}
	return compactLogs(lr, num(a, "limit", 120)), nil
}

// compactLogs collapses repeated messages so the model sees patterns, not noise.
func compactLogs(lr protocol.LogResult, limit int) map[string]any {
	type agg struct {
		msg        string
		count      int
		first      time.Time
		last       time.Time
		prio       int
		ident      string
	}
	var order []string
	groups := map[string]*agg{}
	normalise := regexp.MustCompile(`\d+`)
	for _, e := range lr.Entries {
		msg := e.Message
		if len(msg) > 400 {
			msg = msg[:400] + "…"
		}
		key := e.Ident + "|" + normalise.ReplaceAllString(msg, "#")
		g, ok := groups[key]
		if !ok {
			g = &agg{msg: msg, first: e.Time, last: e.Time, prio: e.Priority, ident: e.Ident}
			groups[key] = g
			order = append(order, key)
		}
		g.count++
		if e.Time.Before(g.first) {
			g.first = e.Time
		}
		if e.Time.After(g.last) {
			g.last = e.Time
		}
	}
	var out []map[string]any
	for _, k := range order {
		g := groups[k]
		row := map[string]any{"time": g.last.Format(time.RFC3339), "priority": g.prio, "message": g.msg}
		if g.ident != "" {
			row["ident"] = g.ident
		}
		if g.count > 1 {
			row["repeated"] = g.count
			row["first_seen"] = g.first.Format(time.RFC3339)
		}
		out = append(out, row)
		if len(out) >= limit {
			break
		}
	}
	return map[string]any{"source": lr.Source, "entries": out, "raw_entries": len(lr.Entries), "truncated": lr.Truncated, "note": lr.Note}
}

func summariseDU(res protocol.DirUsageResult) map[string]any {
	var walk func(d protocol.DirUsage, depth int) map[string]any
	walk = func(d protocol.DirUsage, depth int) map[string]any {
		m := map[string]any{"path": d.Path, "size": human(d.Size), "bytes": d.Size, "files": d.Files}
		if d.Partial {
			m["partial"] = true
		}
		if len(d.Children) > 0 && depth < 3 {
			var kids []map[string]any
			for i, c := range d.Children {
				if i >= 20 {
					break
				}
				kids = append(kids, walk(c, depth+1))
			}
			m["children"] = kids
		}
		return m
	}
	return map[string]any{"usage": walk(res.Root, 0), "scan_ms": res.ScannedMs, "errors": res.Errors, "truncated": res.Truncated}
}

func firstN(s []string, n int) []string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func topN(m map[string]int, n int) []map[string]any {
	type kv struct {
		k string
		v int
	}
	var l []kv
	for k, v := range m {
		l = append(l, kv{k, v})
	}
	sort.Slice(l, func(i, j int) bool { return l[i].v > l[j].v })
	var out []map[string]any
	for i, x := range l {
		if i >= n {
			break
		}
		out = append(out, map[string]any{"key": x.k, "count": x.v})
	}
	return out
}

func trimCommand(res *protocol.ActionResult) *protocol.ActionResult {
	if res.Command != nil {
		c := *res.Command
		if len(c.Stdout) > 4000 {
			c.Stdout = "…" + c.Stdout[len(c.Stdout)-4000:]
		}
		if len(c.Stderr) > 2000 {
			c.Stderr = "…" + c.Stderr[len(c.Stderr)-2000:]
		}
		res.Command = &c
	}
	return res
}

// verifyService annotates an action result with an explicit verification
// verdict based on the state re-read by the agent after the action.
func verifyService(res *protocol.ActionResult, action string) map[string]any {
	out := map[string]any{"ok": res.OK, "action": res.Action, "unit": res.Target, "message": res.Message}
	if res.Command != nil {
		out["exit_code"] = res.Command.ExitCode
		if res.Command.Stderr != "" {
			out["stderr"] = res.Command.Stderr
		}
	}
	verified := "unverified: the agent could not re-read the unit"
	if m, ok := res.After.(map[string]any); ok {
		as, _ := m["active_state"].(string)
		ss, _ := m["sub_state"].(string)
		health, _ := m["health"].(string)
		out["state_after"] = map[string]any{"active_state": as, "sub_state": ss, "health": health, "health_reasons": m["health_reasons"]}
		switch action {
		case "stop":
			if as == "inactive" || as == "failed" {
				verified = "verified: unit is no longer active"
			} else {
				verified = "NOT verified: unit is still " + as
			}
		default:
			if as == "active" && health == "healthy" {
				verified = "verified: unit is active and the health assessment is healthy"
			} else if as == "active" {
				verified = "partially verified: unit is active but health is " + health + " — check logs and probe the service"
			} else {
				verified = "NOT verified: unit is " + as + "/" + ss
			}
		}
	}
	out["verification"] = verified
	return out
}

func verifyContainer(res *protocol.ActionResult, action string) map[string]any {
	out := map[string]any{"ok": res.OK, "action": res.Action, "container": res.Target, "message": res.Message}
	verified := "unverified: container state could not be re-read"
	if m, ok := res.After.(map[string]any); ok {
		state, _ := m["state"].(string)
		health, _ := m["health"].(string)
		out["state_after"] = map[string]any{"state": state, "health": health, "restart_count": m["restart_count"], "exit_code": m["exit_code"]}
		want := "running"
		if action == "stop" {
			want = "exited"
		}
		switch {
		case state == want && (health == "" || health == "healthy"):
			verified = "verified: container is " + state
		case state == want && health == "starting":
			verified = "partially verified: running, health check still starting — re-check shortly"
		default:
			verified = fmt.Sprintf("NOT verified: container state is %s (health %q)", state, health)
		}
	}
	out["verification"] = verified
	return out
}

func updatePreview(ctx context.Context, env *Env, pkgs []string, securityOnly bool) (*Impact, error) {
	var chk protocol.UpdateCheck
	if err := env.Client.Get(ctx, "/v1/packages/updates", nil, &chk); err != nil {
		return nil, err
	}
	var sel []protocol.PackageUpdate
	want := map[string]bool{}
	for _, p := range pkgs {
		want[p] = true
	}
	for _, u := range chk.Updates {
		if securityOnly && u.Security || !securityOnly && want[u.Name] {
			sel = append(sel, u)
		}
	}
	if len(sel) == 0 {
		return nil, fmt.Errorf("no matching pending updates; run check_package_updates (and refresh_package_lists if the lists are old)")
	}
	var lines []string
	restartHints := map[string]bool{}
	for _, u := range sel {
		lines = append(lines, fmt.Sprintf("%s %s → %s (%s)", u.Name, u.Current, u.Candidate, u.Origin))
		for _, svc := range []string{"nginx", "apache2", "postgresql", "mysql", "mariadb", "redis", "docker", "containerd", "openssh", "systemd", "libc6", "openssl", "libssl"} {
			if strings.HasPrefix(u.Name, svc) {
				restartHints[svc] = true
			}
		}
	}
	imp := &Impact{
		Summary:  fmt.Sprintf("Install %d package update(s) on %s", len(sel), env.Server.Name),
		Effects:  firstN(lines, 60),
		Rollback: "APT cannot reliably roll back upgrades. Downgrading requires the previous versions to still be available in the repositories or the apt cache, and maintainer scripts may already have migrated data. Ensure a verified backup exists.",
		Evidence: map[string]any{"lists_updated_at": chk.ListsUpdatedAt, "kernel_update": chk.KernelUpdate, "reboot_required_now": chk.RebootRequired},
	}
	imp.Risks = append(imp.Risks, "NEXUS has no verified backup record for this server yet (backup verification arrives with the Backup Center); confirm your own backups before approving")
	if chk.KernelUpdate {
		imp.Risks = append(imp.Risks, "includes a kernel update: a reboot is needed to run it; NEXUS will not reboot automatically")
	}
	for s := range restartHints {
		imp.Risks = append(imp.Risks, "updates "+s+"-related packages; dependent services may restart or need a restart")
	}
	if chk.ListsUpdatedAt == nil || time.Since(*chk.ListsUpdatedAt) > 24*time.Hour {
		imp.Risks = append(imp.Risks, "package lists are older than 24 h; consider refresh_package_lists first")
	}
	if env.Server.Environment == "production" {
		imp.Risks = append(imp.Risks, "this is a production server; prefer a maintenance window")
	}
	return imp, nil
}



