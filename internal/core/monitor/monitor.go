// Package monitor is the deterministic, event-driven rule engine. It turns
// telemetry and inventory into alerts, de-duplicates them, groups related
// alerts into incidents and decides when a human (or the AI) should look.
// The AI is invoked only for meaningful incidents, never for raw polling.
package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/WSEU790/MoistTux/internal/core/events"
	"github.com/WSEU790/MoistTux/internal/core/store"
	"github.com/WSEU790/MoistTux/internal/protocol"
)

type Config struct {
	CPUWarn, CPUCrit       float64
	MemWarn, MemCrit       float64
	SwapWarn               float64
	DiskWarn, DiskCrit     float64
	InodeWarn, InodeCrit   float64
	LoadPerThreadWarn      float64
	TempWarn, TempCrit     float64
	Sustain                time.Duration
	NotifyCooldown         time.Duration
	IncidentWindow         time.Duration
	AutoInvestigate        bool
	InvestigateMinSeverity string
}

func DefaultConfig() Config {
	return Config{
		CPUWarn: 85, CPUCrit: 95, MemWarn: 90, MemCrit: 97, SwapWarn: 50,
		DiskWarn: 85, DiskCrit: 95, InodeWarn: 85, InodeCrit: 95, LoadPerThreadWarn: 2.0,
		TempWarn: 80, TempCrit: 90, Sustain: 5 * time.Minute, NotifyCooldown: 30 * time.Minute,
		IncidentWindow: 15 * time.Minute, AutoInvestigate: false, InvestigateMinSeverity: "critical",
	}
}

// Notifier delivers alert notifications (desktop + opt-in external channels).
type Notifier interface {
	Notify(ctx context.Context, n *store.Notification)
}

// Investigator is implemented by the AI core.
type Investigator func(ctx context.Context, inc *store.Incident)

type sample struct {
	t   time.Time
	cpu float64
	mem float64
	swp float64
	ld  float64
}

type Engine struct {
	st     *store.Store
	bus    *events.Bus
	log    *log.Logger
	notify Notifier

	mu           sync.Mutex
	cfg          Config
	windows      map[string][]sample
	notified     map[string]time.Time
	investigate  Investigator
	burst        []time.Time
	suppressed   int
}

func New(st *store.Store, bus *events.Bus, n Notifier, cfg Config, logger *log.Logger) *Engine {
	return &Engine{st: st, bus: bus, notify: n, cfg: cfg, log: logger, windows: map[string][]sample{}, notified: map[string]time.Time{}}
}

func (e *Engine) SetConfig(c Config)          { e.mu.Lock(); e.cfg = c; e.mu.Unlock() }
func (e *Engine) Config() Config               { e.mu.Lock(); defer e.mu.Unlock(); return e.cfg }
func (e *Engine) SetInvestigator(i Investigator) { e.mu.Lock(); e.investigate = i; e.mu.Unlock() }

// condition is one evaluated rule outcome.
type condition struct {
	rule, subject, severity, title, detail string
	evidence                               any
}

func sevRank(s string) int {
	switch s {
	case "critical":
		return 3
	case "warning":
		return 2
	case "info":
		return 1
	}
	return 0
}

// ---------------------------------------------------------------- telemetry rules

func (e *Engine) OnSamples(srv *store.Server, samples []protocol.MetricsSample) {
	if len(samples) == 0 {
		return
	}
	cfg := e.Config()
	e.mu.Lock()
	w := e.windows[srv.ID]
	for _, s := range samples {
		ld := 0.0
		if s.CPU.Threads > 0 {
			ld = s.Load[0] / float64(s.CPU.Threads)
		}
		w = append(w, sample{t: s.Time, cpu: s.CPU.UsagePct, mem: s.Mem.UsedPct, swp: s.Mem.SwapPct, ld: ld})
	}
	// Keep the window chronological and free of duplicates even when an
	// agent restart causes its buffer to be re-sent.
	sort.Slice(w, func(a, b int) bool { return w[a].t.Before(w[b].t) })
	dedup := w[:0]
	for _, s := range w {
		if len(dedup) == 0 || !dedup[len(dedup)-1].t.Equal(s.t) {
			dedup = append(dedup, s)
		}
	}
	w = dedup
	cut := time.Now().Add(-cfg.Sustain - time.Minute)
	i := 0
	for i < len(w) && w[i].t.Before(cut) {
		i++
	}
	w = w[i:]
	e.windows[srv.ID] = w
	win := append([]sample(nil), w...)
	e.mu.Unlock()

	latest := samples[len(samples)-1]
	var conds []condition
	sustained := func(get func(sample) float64, thr float64) (bool, float64, float64) {
		from := time.Now().Add(-cfg.Sustain)
		var n int
		minV, maxV := 1e18, -1e18
		var first time.Time
		for _, s := range win {
			if s.t.Before(from) {
				continue
			}
			if first.IsZero() {
				first = s.t
			}
			v := get(s)
			n++
			if v < minV {
				minV = v
			}
			if v > maxV {
				maxV = v
			}
		}
		// Require samples covering at least 80% of the window.
		if n == 0 || time.Since(first) < cfg.Sustain*8/10 {
			return false, 0, 0
		}
		return minV >= thr, minV, maxV
	}
	sustain := cfg.Sustain.Round(time.Second).String()
	if ok, lo, hi := sustained(func(s sample) float64 { return s.cpu }, cfg.CPUCrit); ok {
		conds = append(conds, condition{"cpu_high", "cpu", "critical", "CPU saturated", fmt.Sprintf("CPU usage has stayed between %.1f%% and %.1f%% for %s (threshold %.0f%%)", lo, hi, sustain, cfg.CPUCrit), map[string]any{"min": lo, "max": hi, "window": sustain}})
	} else if ok, lo, hi := sustained(func(s sample) float64 { return s.cpu }, cfg.CPUWarn); ok {
		conds = append(conds, condition{"cpu_high", "cpu", "warning", "High CPU usage", fmt.Sprintf("CPU usage has stayed between %.1f%% and %.1f%% for %s (threshold %.0f%%)", lo, hi, sustain, cfg.CPUWarn), map[string]any{"min": lo, "max": hi, "window": sustain}})
	}
	if ok, lo, hi := sustained(func(s sample) float64 { return s.mem }, cfg.MemCrit); ok {
		conds = append(conds, condition{"mem_high", "memory", "critical", "Memory nearly exhausted", fmt.Sprintf("Memory usage between %.1f%% and %.1f%% for %s; available %s", lo, hi, sustain, bytes(latest.Mem.Available)), map[string]any{"min": lo, "max": hi, "available": latest.Mem.Available}})
	} else if ok, lo, hi := sustained(func(s sample) float64 { return s.mem }, cfg.MemWarn); ok {
		conds = append(conds, condition{"mem_high", "memory", "warning", "High memory usage", fmt.Sprintf("Memory usage between %.1f%% and %.1f%% for %s; available %s", lo, hi, sustain, bytes(latest.Mem.Available)), map[string]any{"min": lo, "max": hi, "available": latest.Mem.Available}})
	}
	if latest.Mem.SwapTotal > 0 {
		if ok, lo, hi := sustained(func(s sample) float64 { return s.swp }, cfg.SwapWarn); ok {
			conds = append(conds, condition{"swap_high", "swap", "warning", "Heavy swap usage", fmt.Sprintf("Swap usage between %.1f%% and %.1f%% for %s", lo, hi, sustain), map[string]any{"min": lo, "max": hi}})
		}
	}
	if ok, lo, _ := sustained(func(s sample) float64 { return s.ld }, cfg.LoadPerThreadWarn); ok {
		conds = append(conds, condition{"load_high", "load", "warning", "Run queue overloaded", fmt.Sprintf("1-minute load per CPU thread has been at least %.2f for %s (load %.2f on %d threads)", lo, sustain, latest.Load[0], latest.CPU.Threads), map[string]any{"load1": latest.Load[0], "threads": latest.CPU.Threads}})
	}
	for _, f := range latest.FS {
		subj := "fs:" + f.Mount
		if f.UsedPct >= cfg.DiskCrit {
			conds = append(conds, condition{"disk_high", subj, "critical", "Filesystem almost full: " + f.Mount, fmt.Sprintf("%s (%s) is %.1f%% used, %s free of %s", f.Mount, f.Device, f.UsedPct, bytes(f.Avail), bytes(f.Total)), f})
		} else if f.UsedPct >= cfg.DiskWarn {
			conds = append(conds, condition{"disk_high", subj, "warning", "Filesystem filling up: " + f.Mount, fmt.Sprintf("%s (%s) is %.1f%% used, %s free of %s", f.Mount, f.Device, f.UsedPct, bytes(f.Avail), bytes(f.Total)), f})
		}
		isubj := "inode:" + f.Mount
		if f.InodesTotal > 0 && f.InodesPct >= cfg.InodeCrit {
			conds = append(conds, condition{"inode_high", isubj, "critical", "Inodes almost exhausted: " + f.Mount, fmt.Sprintf("%s uses %.1f%% of %d inodes", f.Mount, f.InodesPct, f.InodesTotal), f})
		} else if f.InodesTotal > 0 && f.InodesPct >= cfg.InodeWarn {
			conds = append(conds, condition{"inode_high", isubj, "warning", "Inode usage high: " + f.Mount, fmt.Sprintf("%s uses %.1f%% of %d inodes", f.Mount, f.InodesPct, f.InodesTotal), f})
		}
	}
	var hot *protocol.TempValue
	for i := range latest.Temps {
		if hot == nil || latest.Temps[i].Celsius > hot.Celsius {
			hot = &latest.Temps[i]
		}
	}
	if hot != nil && hot.Celsius >= cfg.TempCrit {
		conds = append(conds, condition{"temp_high", "temperature", "critical", "Hardware temperature critical", fmt.Sprintf("%s %s at %.1f°C", hot.Sensor, hot.Label, hot.Celsius), hot})
	} else if hot != nil && hot.Celsius >= cfg.TempWarn {
		conds = append(conds, condition{"temp_high", "temperature", "warning", "Hardware running hot", fmt.Sprintf("%s %s at %.1f°C", hot.Sensor, hot.Label, hot.Celsius), hot})
	}
	e.apply(srv, []string{"cpu_high", "mem_high", "swap_high", "load_high", "disk_high", "inode_high", "temp_high"}, conds)
}

// ---------------------------------------------------------------- inventory rules

var reExited = regexp.MustCompile(`^Exited \((\d+)\)`)

func (e *Engine) OnInventory(srv *store.Server, kind string, data any) {
	var conds []condition
	switch kind {
	case "services":
		sl, ok := data.(*protocol.ServiceList)
		if !ok {
			return
		}
		sev := "warning"
		if srv.Environment == "production" {
			sev = "critical"
		}
		for _, s := range sl.Services {
			if s.ActiveState == "failed" {
				conds = append(conds, condition{"service_failed", s.Unit, sev, "Service failed: " + s.Unit, fmt.Sprintf("systemd reports %s as failed (%s/%s). %s", s.Unit, s.ActiveState, s.SubState, s.Description), s})
			}
		}
		e.apply(srv, []string{"service_failed"}, conds)
	case "containers":
		cs, ok := data.(*[]protocol.Container)
		if !ok {
			return
		}
		for _, c := range *cs {
			switch {
			case c.Health == "unhealthy":
				conds = append(conds, condition{"container_unhealthy", c.Name, "critical", "Container unhealthy: " + c.Name, fmt.Sprintf("Docker health check reports %s (%s) as unhealthy: %s", c.Name, c.Image, c.Status), c})
			case c.State == "restarting":
				conds = append(conds, condition{"container_restarting", c.Name, "warning", "Container restart loop: " + c.Name, fmt.Sprintf("%s (%s) is restarting: %s", c.Name, c.Image, c.Status), c})
			case c.State == "exited":
				if m := reExited.FindStringSubmatch(c.Status); m != nil && m[1] != "0" {
					code, _ := strconv.Atoi(m[1])
					sev := "warning"
					if code == 137 {
						sev = "critical" // SIGKILL, commonly OOM
					}
					conds = append(conds, condition{"container_exited", c.Name, sev, "Container exited with error: " + c.Name, fmt.Sprintf("%s (%s) exited with code %d: %s", c.Name, c.Image, code, c.Status), c})
				}
			}
		}
		e.apply(srv, []string{"container_unhealthy", "container_restarting", "container_exited"}, conds)
	case "updates":
		u, ok := data.(*protocol.UpdateCheck)
		if !ok {
			return
		}
		if u.SecurityCount > 0 {
			conds = append(conds, condition{"security_updates", "packages", "warning", fmt.Sprintf("%d security updates available", u.SecurityCount), fmt.Sprintf("%d of %d pending package updates come from security repositories", u.SecurityCount, len(u.Updates)), map[string]any{"security": u.SecurityCount, "total": len(u.Updates)}})
		}
		if u.RebootRequired {
			conds = append(conds, condition{"reboot_required", "reboot", "info", "Reboot required", "The package system reports a reboot is required (" + strings.Join(u.RebootPkgs, ", ") + ")", u.RebootPkgs})
		}
		e.apply(srv, []string{"security_updates", "reboot_required"}, conds)
	case "posture":
		p, ok := data.(*protocol.SecurityPosture)
		if !ok {
			return
		}
		for _, f := range p.Findings {
			if f.ID == "pkg.security_updates" || f.ID == "pkg.reboot_required" {
				continue // covered by the update rules
			}
			sev := ""
			switch f.Severity {
			case "critical":
				sev = "critical"
			case "high":
				sev = "warning"
			}
			if sev != "" {
				conds = append(conds, condition{"security_finding", f.ID, sev, f.Title, f.Evidence + " — " + f.Advice, f})
			}
		}
		e.apply(srv, []string{"security_finding"}, conds)
	}
}

// ---------------------------------------------------------------- status rules

func (e *Engine) OnStatus(srv *store.Server, prev, cur, reason string) {
	switch cur {
	case "offline":
		e.apply(srv, []string{"agent_offline", "agent_untrusted"}, []condition{{"agent_offline", "agent", "critical", "Server unreachable", "NEXUS cannot reach the agent: " + reason, map[string]string{"previous_status": prev}}})
	case "untrusted":
		e.apply(srv, []string{"agent_offline", "agent_untrusted"}, []condition{{"agent_untrusted", "agent", "critical", "Agent identity mismatch", reason, nil}})
	case "online", "degraded":
		e.apply(srv, []string{"agent_offline", "agent_untrusted"}, nil)
	}
}

func (e *Engine) OnReboot(srv *store.Server, prevBoot, newBoot string, bootTime time.Time) {
	ctx := context.Background()
	detail := fmt.Sprintf("Boot ID changed; the server booted at %s.", bootTime.Format(time.RFC3339))
	sev := "warning"
	// Was it NEXUS's own approved reboot?
	if exes, err := e.st.ListExecutions(ctx, srv.ID, "", 20); err == nil {
		for _, x := range exes {
			if x.Tool == "reboot_server" && x.Status == "succeeded" && x.FinishedAt != nil && time.Since(*x.FinishedAt) < 30*time.Minute {
				sev, detail = "info", detail+" This matches the approved reboot "+x.ID+"."
			}
		}
	}
	e.raise(ctx, srv, condition{"reboot_detected", "boot", sev, "Server rebooted", detail, map[string]string{"previous_boot_id": prevBoot, "boot_id": newBoot}})
}

// ---------------------------------------------------------------- lifecycle

// apply raises the given conditions and resolves firing alerts of the listed
// rules that are no longer present.
func (e *Engine) apply(srv *store.Server, rules []string, conds []condition) {
	ctx := context.Background()
	active := map[string]bool{}
	for _, c := range conds {
		active[fingerprint(srv.ID, c.rule, c.subject)] = true
		e.raise(ctx, srv, c)
	}
	firing, err := e.st.FiringForServer(ctx, srv.ID)
	if err != nil {
		return
	}
	ruleSet := map[string]bool{}
	for _, r := range rules {
		ruleSet[r] = true
	}
	for _, a := range firing {
		if ruleSet[a.Rule] && !active[a.Fingerprint] {
			e.resolve(ctx, srv, a)
		}
	}
}

func fingerprint(serverID, rule, subject string) string { return serverID + "|" + rule + "|" + subject }

func (e *Engine) raise(ctx context.Context, srv *store.Server, c condition) {
	fp := fingerprint(srv.ID, c.rule, c.subject)
	ev, _ := json.Marshal(c.evidence)
	existing, err := e.st.FiringAlert(ctx, fp)
	if err == nil {
		escalated := sevRank(c.severity) > sevRank(existing.Severity)
		_ = e.st.TouchAlert(ctx, existing.ID, c.severity, c.detail, ev)
		if escalated {
			existing.Severity, existing.Detail = c.severity, c.detail
			e.bus.Publish("alert", existing)
			e.attachIncident(ctx, srv, existing, true)
			e.maybeNotify(ctx, srv, existing, true)
		}
		return
	}
	a := &store.Alert{Fingerprint: fp, ServerID: srv.ID, Rule: c.rule, Severity: c.severity, Subject: c.subject, Title: c.title, Detail: c.detail, Evidence: ev}
	if err := e.st.InsertAlert(ctx, a); err != nil {
		e.log.Printf("insert alert: %v", err)
		return
	}
	e.attachIncident(ctx, srv, a, false)
	e.bus.Publish("alert", a)
	e.maybeNotify(ctx, srv, a, false)
}

func (e *Engine) resolve(ctx context.Context, srv *store.Server, a *store.Alert) {
	if err := e.st.ResolveAlert(ctx, a.ID); err != nil {
		return
	}
	a.Status = "resolved"
	e.bus.Publish("alert", a)
	if a.IncidentID != "" && e.st.CountFiringInIncident(ctx, a.IncidentID) == 0 {
		if inc, err := e.st.GetIncident(ctx, a.IncidentID); err == nil && inc.Status == "open" {
			now := time.Now().UTC()
			inc.Status, inc.ResolvedAt = "resolved", &now
			_ = e.st.UpdateIncident(ctx, inc)
			e.bus.Publish("incident", inc)
		}
	}
}

// attachIncident groups alerts on the same server that occur close together:
// one underlying problem typically triggers several rules at once.
func (e *Engine) attachIncident(ctx context.Context, srv *store.Server, a *store.Alert, escalation bool) {
	cfg := e.Config()
	if a.Severity == "info" && !escalation {
		return
	}
	var inc *store.Incident
	isNew := false
	if a.IncidentID != "" {
		inc, _ = e.st.GetIncident(ctx, a.IncidentID)
	}
	if inc == nil {
		inc, _ = e.st.OpenIncidentFor(ctx, srv.ID, cfg.IncidentWindow)
	}
	if inc == nil {
		inc = &store.Incident{ServerID: srv.ID, Title: srv.Name + ": " + a.Title, Severity: a.Severity}
		if err := e.st.CreateIncident(ctx, inc); err != nil {
			return
		}
		isNew = true
	}
	if a.IncidentID != inc.ID {
		a.IncidentID = inc.ID
		_, _ = e.st.DB().ExecContext(ctx, `UPDATE alerts SET incident_id = ? WHERE id = ?`, inc.ID, a.ID)
	}
	escalated := sevRank(a.Severity) > sevRank(inc.Severity)
	if escalated {
		inc.Severity = a.Severity
		inc.Title = srv.Name + ": " + a.Title
	}
	_ = e.st.UpdateIncident(ctx, inc)
	e.bus.Publish("incident", inc)
	if (isNew || escalated) && cfg.AutoInvestigate && sevRank(inc.Severity) >= sevRank(cfg.InvestigateMinSeverity) && inc.AIStatus == "" {
		e.mu.Lock()
		inv := e.investigate
		e.mu.Unlock()
		if inv != nil {
			go inv(context.Background(), inc)
		}
	}
}

// maybeNotify applies per-alert cooldown and a global storm limit.
func (e *Engine) maybeNotify(ctx context.Context, srv *store.Server, a *store.Alert, escalation bool) {
	if e.notify == nil || a.Severity == "info" {
		return
	}
	cfg := e.Config()
	e.mu.Lock()
	last, seen := e.notified[a.Fingerprint]
	if seen && time.Since(last) < cfg.NotifyCooldown && !escalation {
		e.mu.Unlock()
		return
	}
	now := time.Now()
	kept := e.burst[:0]
	for _, t := range e.burst {
		if now.Sub(t) < 5*time.Minute {
			kept = append(kept, t)
		}
	}
	e.burst = kept
	if len(e.burst) >= 10 && a.Severity != "critical" {
		e.suppressed++
		e.mu.Unlock()
		return
	}
	e.burst = append(e.burst, now)
	suppressed := e.suppressed
	e.suppressed = 0
	e.notified[a.Fingerprint] = now
	e.mu.Unlock()
	body := a.Detail
	if suppressed > 0 {
		body += fmt.Sprintf("\n(%d lower-severity notifications were suppressed in the last few minutes to avoid a notification storm.)", suppressed)
	}
	n := &store.Notification{Severity: a.Severity, Kind: "alert", Title: srv.Name + " — " + a.Title, Body: body, ServerID: srv.ID, Ref: a.ID}
	e.notify.Notify(ctx, n)
	_ = e.st.MarkAlertNotified(ctx, a.ID)
}

// Sweep resolves time-bound informational alerts. Call periodically.
func (e *Engine) Sweep(ctx context.Context) {
	firing, err := e.st.ListAlerts(ctx, "firing", "", 2000)
	if err != nil {
		return
	}
	for _, a := range firing {
		if a.Rule == "reboot_detected" && time.Since(a.FirstSeen) > time.Hour {
			if srv, err := e.st.GetServer(ctx, a.ServerID); err == nil {
				e.resolve(ctx, srv, a)
			}
		}
	}
	// Alerts whose server was deleted.
	servers, _ := e.st.ListServers(ctx)
	ids := map[string]bool{}
	for _, s := range servers {
		ids[s.ID] = true
	}
	for _, a := range firing {
		if !ids[a.ServerID] {
			_ = e.st.ResolveAlert(ctx, a.ID)
		}
	}
}

func bytes(b uint64) string {
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
