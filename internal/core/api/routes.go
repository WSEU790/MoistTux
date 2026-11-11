package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/WSEU790/MoistTux/internal/core/agentbin"
	"github.com/WSEU790/MoistTux/internal/core/ai"
	"github.com/WSEU790/MoistTux/internal/core/deploy"
	"github.com/WSEU790/MoistTux/internal/core/fleet"
	"github.com/WSEU790/MoistTux/internal/core/monitor"
	"github.com/WSEU790/MoistTux/internal/core/notify"
	"github.com/WSEU790/MoistTux/internal/core/store"
	"github.com/WSEU790/MoistTux/internal/core/update"
	"github.com/WSEU790/MoistTux/internal/protocol"
)

func (s *Server) routes(m *http.ServeMux) {
	m.HandleFunc("GET /api/bootstrap", s.bootstrap)
	m.HandleFunc("GET /api/events", s.events)
	m.HandleFunc("GET /api/overview", s.overview)
	m.HandleFunc("GET /api/system", s.system)

	m.HandleFunc("GET /api/servers", s.listServers)
	m.HandleFunc("POST /api/servers", s.enroll)
	m.HandleFunc("GET /api/servers/{id}", s.getServer)
	m.HandleFunc("PATCH /api/servers/{id}", s.patchServer)
	m.HandleFunc("DELETE /api/servers/{id}", s.deleteServer)
	m.HandleFunc("GET /api/servers/{id}/telemetry", s.telemetry)
	m.HandleFunc("GET /api/servers/{id}/inventory/{kind}", s.inventory)
	m.HandleFunc("GET /api/servers/{id}/agent/{path...}", s.agentProxy)
	m.HandleFunc("POST /api/servers/{id}/actions/preview", s.actionPreview)
	m.HandleFunc("POST /api/servers/{id}/actions", s.action)
	m.HandleFunc("POST /api/servers/{id}/refresh", s.refresh)
	m.HandleFunc("POST /api/servers/{id}/rotate-certificate", s.rotateCert)

	m.HandleFunc("GET /api/alerts", s.listAlerts)
	m.HandleFunc("POST /api/alerts/{id}/ack", s.ackAlert)
	m.HandleFunc("GET /api/incidents", s.listIncidents)
	m.HandleFunc("GET /api/incidents/{id}", s.getIncident)
	m.HandleFunc("POST /api/incidents/{id}/investigate", s.investigate)
	m.HandleFunc("GET /api/notifications", s.listNotifications)
	m.HandleFunc("POST /api/notifications/read", s.readNotifications)

	m.HandleFunc("GET /api/settings/{section}", s.getSettings)
	m.HandleFunc("PUT /api/settings/{section}", s.putSettings)
	m.HandleFunc("POST /api/settings/notifications/test", s.testNotification)

	m.HandleFunc("GET /api/ai/status", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, s.AI.Status(r.Context())) })
	m.HandleFunc("GET /api/ai/models", s.models)
	m.HandleFunc("POST /api/ai/models/pull", s.pullModel)
	m.HandleFunc("DELETE /api/ai/models/{name...}", s.deleteModel)
	m.HandleFunc("GET /api/ai/hardware", s.hardware)
	m.HandleFunc("GET /api/ai/tools", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, s.Registry.Info()) })
	m.HandleFunc("GET /api/ai/roles", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, ai.Roles) })

	m.HandleFunc("GET /api/conversations", s.listConversations)
	m.HandleFunc("POST /api/conversations", s.createConversation)
	m.HandleFunc("GET /api/conversations/{id}", s.getConversation)
	m.HandleFunc("PATCH /api/conversations/{id}", s.patchConversation)
	m.HandleFunc("DELETE /api/conversations/{id}", s.deleteConversation)
	m.HandleFunc("POST /api/conversations/{id}/messages", s.sendMessage)
	m.HandleFunc("POST /api/conversations/{id}/stop", s.stopConversation)

	m.HandleFunc("GET /api/approvals", s.listApprovals)
	m.HandleFunc("POST /api/approvals/{id}/decide", s.decide)
	m.HandleFunc("GET /api/executions", s.listExecutions)
	m.HandleFunc("GET /api/audit", s.audit)
	m.HandleFunc("GET /api/audit/verify", s.verifyAudit)

	m.HandleFunc("GET /api/memory", s.listMemory)
	m.HandleFunc("POST /api/memory", s.putMemory)
	m.HandleFunc("PUT /api/memory/{id}", s.putMemory)
	m.HandleFunc("DELETE /api/memory/{id}", s.deleteMemory)

	m.HandleFunc("GET /api/policies", s.listPolicies)
	m.HandleFunc("GET /api/policies/{scope}", s.getPolicy)
	m.HandleFunc("PUT /api/policies/{scope}", s.putPolicy)
	m.HandleFunc("DELETE /api/policies/{scope}", s.deletePolicy)

	m.HandleFunc("GET /api/topology", s.topology)

	m.HandleFunc("GET /api/update", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, s.Update.Status()) })
	m.HandleFunc("POST /api/update/check", func(w http.ResponseWriter, r *http.Request) {
		if st := s.Update.Status(); !st.Enabled {
			writeErr(w, 400, errors.New(st.Disabled))
			return
		}
		go func() { _ = s.Update.CheckAndDownload(context.Background()) }()
		writeJSON(w, 202, s.Update.Status())
	})
	m.HandleFunc("POST /api/update/apply", func(w http.ResponseWriter, r *http.Request) {
		if err := s.ApplyUpdate(); err != nil {
			writeErr(w, 400, err)
			return
		}
		writeJSON(w, 202, map[string]bool{"restarting": true})
	})
	m.HandleFunc("GET /api/deploy/agents", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, agentbin.Available()) })
	m.HandleFunc("GET /api/servers/{id}/ssh", func(w http.ResponseWriter, r *http.Request) {
		c, err := s.Deploy.Saved(r.Context(), r.PathValue("id"))
		if err != nil {
			writeJSON(w, 200, map[string]any{"saved": false})
			return
		}
		writeJSON(w, 200, map[string]any{"saved": true, "credential": c})
	})
	m.HandleFunc("DELETE /api/servers/{id}/ssh", func(w http.ResponseWriter, r *http.Request) {
		ok, err := s.Deploy.Forget(r.Context(), r.PathValue("id"), "operator")
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		writeJSON(w, 200, map[string]bool{"forgotten": ok})
	})
	m.HandleFunc("POST /api/deploy/scan", s.deployScan)
	m.HandleFunc("POST /api/deploy", s.deployStart)
	m.HandleFunc("GET /api/deploy/{id}", s.deployGet)
	m.HandleFunc("POST /api/deploy/{id}/retry-enroll", func(w http.ResponseWriter, r *http.Request) {
		j, err := s.Deploy.RetryEnroll(r.PathValue("id"))
		if err != nil {
			writeErr(w, 400, err)
			return
		}
		writeJSON(w, 202, j)
	})
}

// ---------------------------------------------------------------- SSH deployment

func (s *Server) deployScan(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Host string `json:"host"`
		Port int    `json:"port"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, err)
		return
	}
	res, err := deploy.Scan(r.Context(), strings.TrimSpace(req.Host), req.Port)
	if err != nil {
		writeErr(w, 502, err)
		return
	}
	writeJSON(w, 200, res)
}

func (s *Server) deployStart(w http.ResponseWriter, r *http.Request) {
	var req deploy.Request
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, err)
		return
	}
	req.Host = strings.TrimSpace(req.Host)
	j, err := s.Deploy.Start(req)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 202, j)
}

func (s *Server) deployGet(w http.ResponseWriter, r *http.Request) {
	j, ok := s.Deploy.Get(r.PathValue("id"))
	if !ok {
		writeErr(w, 404, errors.New("unknown deployment"))
		return
	}
	writeJSON(w, 200, j)
}

// ---------------------------------------------------------------- core

func (s *Server) bootstrap(w http.ResponseWriter, r *http.Request) {
	var appearance map[string]any
	_, _ = s.Store.GetSetting(r.Context(), "appearance", &appearance)
	id := s.Identity()
	host, _ := os.Hostname()
	writeJSON(w, 200, map[string]any{
		"version": s.Version, "hostname": host, "data_dir": s.DataDir, "schema": s.Store.SchemaVersion(),
		"core_fingerprint": id.Fingerprint, "core_cert_expires": id.NotAfter, "appearance": appearance, "started": s.Started,
	})
}

func (s *Server) system(w http.ResponseWriter, r *http.Request) {
	raw, roll := s.Store.TelemetryStats(r.Context())
	var dbSize int64
	for _, f := range []string{"nexus.db", "nexus.db-wal"} {
		if st, err := os.Stat(filepath.Join(s.DataDir, f)); err == nil {
			dbSize += st.Size()
		}
	}
	writeJSON(w, 200, map[string]any{"uptime_sec": int(time.Since(s.Started).Seconds()), "telemetry_rows": raw, "rollup_rows": roll, "db_bytes": dbSize, "events_dropped": s.Bus.Dropped(), "data_dir": s.DataDir, "version": s.Version})
}

// events streams the bus as Server-Sent Events.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		writeErr(w, 500, errors.New("streaming unsupported"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	ch, cancel := s.Bus.Subscribe(512)
	defer cancel()
	_, _ = fmt.Fprintf(w, "event: hello\ndata: {}\n\n")
	fl.Flush()
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			_, _ = fmt.Fprintf(w, ": ping\n\n")
			fl.Flush()
		case ev, ok := <-ch:
			if !ok {
				return
			}
			b, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			_, _ = fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", ev.ID, ev.Type, b)
			fl.Flush()
		}
	}
}

type serverView struct {
	*store.Server
	Latest      *protocol.MetricsSample `json:"latest,omitempty"`
	LatestAt    *time.Time              `json:"latest_at,omitempty"`
	HealthScore *int                    `json:"health_score"`
	Alerts      map[string]int          `json:"alerts"`
}

func (s *Server) view(ctx context.Context, srv *store.Server) serverView {
	v := serverView{Server: srv, Alerts: map[string]int{}}
	latest, at := s.Fleet.Latest(srv.ID)
	if latest != nil {
		v.Latest, v.LatestAt = latest, &at
	}
	al, _ := s.Store.FiringForServer(ctx, srv.ID)
	for _, a := range al {
		v.Alerts[a.Severity]++
	}
	v.HealthScore = fleet.HealthScore(srv, latest, al)
	return v
}

func (s *Server) listServers(w http.ResponseWriter, r *http.Request) {
	servers, err := s.Store.ListServers(r.Context())
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	out := make([]serverView, 0, len(servers))
	for _, srv := range servers {
		out = append(out, s.view(r.Context(), srv))
	}
	writeJSON(w, 200, out)
}

func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	servers, err := s.Store.ListServers(ctx)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	ov := map[string]any{}
	counts := map[string]int{"total": len(servers)}
	var services, failedServices, containers, runningContainers, updates, security int
	var inventoried = map[string]int{}
	for _, srv := range servers {
		counts[srv.Status]++
		var sl protocol.ServiceList
		if _, err := s.Store.GetInventory(ctx, srv.ID, "services", &sl); err == nil {
			services += sl.Running
			failedServices += sl.Failed
			inventoried["services"]++
		}
		var cs []protocol.Container
		if _, err := s.Store.GetInventory(ctx, srv.ID, "containers", &cs); err == nil {
			containers += len(cs)
			for _, c := range cs {
				if c.State == "running" {
					runningContainers++
				}
			}
			inventoried["containers"]++
		}
		var u protocol.UpdateCheck
		if _, err := s.Store.GetInventory(ctx, srv.ID, "updates", &u); err == nil {
			updates += len(u.Updates)
			security += u.SecurityCount
			inventoried["updates"]++
		}
	}
	alerts, _ := s.Store.ListAlerts(ctx, "firing", "", 2000)
	sev := map[string]int{}
	for _, a := range alerts {
		sev[a.Severity]++
	}
	incidents, _ := s.Store.ListIncidents(ctx, "open", 50)
	pending, _ := s.Store.ListApprovals(ctx, "pending", 100)
	ov["servers"] = counts
	ov["alerts"] = sev
	ov["open_incidents"] = len(incidents)
	ov["pending_approvals"] = len(pending)
	ov["services_running"] = services
	ov["services_failed"] = failedServices
	ov["containers_total"] = containers
	ov["containers_running"] = runningContainers
	ov["updates_pending"] = updates
	ov["security_updates"] = security
	ov["inventoried"] = inventoried
	ov["monitoring"] = map[string]any{"auto_investigate": s.Monitor.Config().AutoInvestigate}
	ov["backups"] = map[string]any{"configured": false, "note": "The Backup Center is scheduled for a later phase; NEXUS does not yet track backups."}
	writeJSON(w, 200, ov)
}

func (s *Server) enroll(w http.ResponseWriter, r *http.Request) {
	var req fleet.EnrollRequest
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, err)
		return
	}
	srv, err := s.Fleet.Enroll(r.Context(), req)
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	writeJSON(w, 201, s.view(r.Context(), srv))
}

func (s *Server) getServer(w http.ResponseWriter, r *http.Request) {
	srv, err := s.Store.GetServer(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	writeJSON(w, 200, s.view(r.Context(), srv))
}

func (s *Server) patchServer(w http.ResponseWriter, r *http.Request) {
	var p store.ServerPatch
	if err := decode(r, &p); err != nil {
		writeErr(w, 400, err)
		return
	}
	if p.Name != nil && strings.TrimSpace(*p.Name) == "" {
		writeErr(w, 400, errors.New("name cannot be empty"))
		return
	}
	if p.Environment != nil {
		switch *p.Environment {
		case "production", "staging", "development", "lab":
		default:
			writeErr(w, 400, errors.New("environment must be production, staging, development or lab"))
			return
		}
	}
	if p.Port != nil && (*p.Port < 1 || *p.Port > 65535) {
		writeErr(w, 400, errors.New("port out of range"))
		return
	}
	id := r.PathValue("id")
	if err := s.Store.PatchServer(r.Context(), id, p); err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	_ = s.Store.Audit(r.Context(), "operator", "server.update", id, p)
	if p.Address != nil || p.Port != nil {
		_ = s.Fleet.Reconnect(r.Context(), id)
	}
	s.Bus.Publish("server.updated", map[string]string{"id": id})
	s.getServer(w, r)
}

func (s *Server) deleteServer(w http.ResponseWriter, r *http.Request) {
	unenroll := r.URL.Query().Get("unenroll") != "0"
	if err := s.Fleet.Remove(r.Context(), r.PathValue("id"), unenroll); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeErr(w, 404, err)
			return
		}
		writeJSON(w, 200, map[string]any{"removed": true, "warning": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"removed": true})
}

var ranges = map[string]time.Duration{"15m": 15 * time.Minute, "1h": time.Hour, "6h": 6 * time.Hour, "24h": 24 * time.Hour, "7d": 7 * 24 * time.Hour, "30d": 30 * 24 * time.Hour}

func (s *Server) telemetry(w http.ResponseWriter, r *http.Request) {
	d, ok := ranges[r.URL.Query().Get("range")]
	if !ok {
		d = time.Hour
	}
	to := time.Now()
	pts, res, err := s.Store.QueryTelemetry(r.Context(), r.PathValue("id"), to.Add(-d), to, qInt(r, "points", 400))
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	if pts == nil {
		pts = []store.Point{}
	}
	writeJSON(w, 200, map[string]any{"points": pts, "resolution": res, "from": to.Add(-d), "to": to})
}

func (s *Server) inventory(w http.ResponseWriter, r *http.Request) {
	inv, err := s.Store.GetInventory(r.Context(), r.PathValue("id"), r.PathValue("kind"), nil)
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	writeJSON(w, 200, inv)
}

// The UI reads live data through this allowlisted, GET-only passthrough.
var proxyAllow = regexp.MustCompile(`^(health|info|metrics|processes(/\d+)?|services(/[A-Za-z0-9@._:\\-]+(/dependencies)?)?|logs(/files)?|docker(/(containers(/[a-zA-Z0-9_.\-]+(/logs)?)?|images|volumes|networks|compose))?|storage(/(usage|large-files))?|network(/firewall)?|packages(/(updates|history))?|security/(ssh|auth|users|posture))$`)

func (s *Server) agentProxy(w http.ResponseWriter, r *http.Request) {
	p := r.PathValue("path")
	if !proxyAllow.MatchString(p) {
		writeErr(w, 404, fmt.Errorf("unknown agent resource %q", p))
		return
	}
	cl, err := s.Fleet.Client(r.PathValue("id"))
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	var raw json.RawMessage
	q := url.Values{}
	for k, v := range r.URL.Query() {
		q[k] = v
	}
	if err := cl.Get(ctx, "/v1/"+p, q, &raw); err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(raw)
}

type actionReq struct {
	Tool      string         `json:"tool"`
	Args      map[string]any `json:"args"`
	Confirmed bool           `json:"confirmed"`
	Confirm   string         `json:"confirm"`
}

func (s *Server) actionPreview(w http.ResponseWriter, r *http.Request) {
	var req actionReq
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, err)
		return
	}
	t, ok := s.Registry.Get(req.Tool)
	if !ok || t.Fleet {
		writeErr(w, 400, fmt.Errorf("unknown server tool %q", req.Tool))
		return
	}
	args, err := t.Validate(req.Args)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	srv, err := s.Store.GetServer(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	cl, err := s.Fleet.Client(srv.ID)
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	pol, scope := s.Policies.Effective(r.Context(), srv.ID)
	v := ai.Decide(ai.ModeAdministrator, t, args, srv, pol, time.Now())
	out := map[string]any{"tool": t.Name, "risk": t.Risk, "policy": v, "policy_scope": scope}
	if t.Preview != nil {
		imp, err := t.Preview(r.Context(), &ai.Env{Server: srv, Client: cl}, args)
		if err != nil {
			writeErr(w, statusFor(err), err)
			return
		}
		out["impact"] = imp
	}
	writeJSON(w, 200, out)
}

func (s *Server) action(w http.ResponseWriter, r *http.Request) {
	var req actionReq
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, err)
		return
	}
	t, ok := s.Registry.Get(req.Tool)
	if !ok || t.Fleet {
		writeErr(w, 400, fmt.Errorf("unknown server tool %q", req.Tool))
		return
	}
	if t.Risk == ai.RiskHigh {
		srv, err := s.Store.GetServer(r.Context(), r.PathValue("id"))
		if err != nil {
			writeErr(w, statusFor(err), err)
			return
		}
		want := srv.Name
		if t.Name == "reboot_server" && srv.Info != nil {
			want = srv.Info.Hostname
		}
		if strings.TrimSpace(req.Confirm) != want {
			writeErr(w, 400, fmt.Errorf("type %q to confirm this high-risk operation", want))
			return
		}
	}
	args := req.Args
	if args == nil {
		args = map[string]any{}
	}
	args["server"] = r.PathValue("id")
	out, _, err := s.AI.Execute(r.Context(), ai.ExecRequest{Tool: t, Args: args, Mode: ai.ModeAdministrator, RequestedBy: "operator", OperatorConfirmed: req.Confirmed})
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	s.Fleet.RefreshNow(r.PathValue("id"), "services")
	s.Fleet.RefreshNow(r.PathValue("id"), "containers")
	writeJSON(w, 200, out)
}

func (s *Server) refresh(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind string `json:"kind"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, err)
		return
	}
	switch req.Kind {
	case "info", "services", "containers", "network", "updates", "posture":
	default:
		writeErr(w, 400, errors.New("kind must be info, services, containers, network, updates or posture"))
		return
	}
	s.Fleet.RefreshNow(r.PathValue("id"), req.Kind)
	writeJSON(w, 202, map[string]string{"scheduled": req.Kind})
}

func (s *Server) rotateCert(w http.ResponseWriter, r *http.Request) {
	fp, err := s.Fleet.RotateAgentCertificate(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	writeJSON(w, 200, map[string]string{"fingerprint": fp})
}

// ---------------------------------------------------------------- alerts

func (s *Server) listAlerts(w http.ResponseWriter, r *http.Request) {
	al, err := s.Store.ListAlerts(r.Context(), r.URL.Query().Get("status"), r.URL.Query().Get("server"), qInt(r, "limit", 500))
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, al)
}

func (s *Server) ackAlert(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.AckAlert(r.Context(), r.PathValue("id")); err != nil {
		writeErr(w, 500, err)
		return
	}
	_ = s.Store.Audit(r.Context(), "operator", "alert.ack", r.PathValue("id"), nil)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) listIncidents(w http.ResponseWriter, r *http.Request) {
	inc, err := s.Store.ListIncidents(r.Context(), r.URL.Query().Get("status"), qInt(r, "limit", 100))
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, inc)
}

func (s *Server) getIncident(w http.ResponseWriter, r *http.Request) {
	inc, err := s.Store.GetIncident(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	writeJSON(w, 200, inc)
}

func (s *Server) investigate(w http.ResponseWriter, r *http.Request) {
	inc, err := s.Store.GetIncident(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	if inc.AIStatus == "investigating" {
		writeErr(w, 409, errors.New("an investigation is already running"))
		return
	}
	if st := s.AI.Status(r.Context()); !st.Reachable {
		writeErr(w, 503, fmt.Errorf("Ollama is not reachable: %s", st.Error))
		return
	}
	_ = s.Store.Audit(r.Context(), "operator", "incident.investigate", inc.ID, nil)
	go s.AI.Investigate(context.Background(), inc)
	writeJSON(w, 202, map[string]string{"status": "investigating"})
}

func (s *Server) listNotifications(w http.ResponseWriter, r *http.Request) {
	n, unread, err := s.Store.ListNotifications(r.Context(), qInt(r, "limit", 100))
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": n, "unread": unread})
}

func (s *Server) readNotifications(w http.ResponseWriter, r *http.Request) {
	_ = s.Store.MarkNotificationsRead(r.Context())
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// ---------------------------------------------------------------- settings

// MonitoringSettings is the UI-facing form of monitor + fleet configuration.
type MonitoringSettings struct {
	CPUWarn, CPUCrit           float64
	MemWarn, MemCrit           float64
	SwapWarn                   float64
	DiskWarn, DiskCrit         float64
	InodeWarn, InodeCrit       float64
	LoadPerThreadWarn          float64
	TempWarn, TempCrit         float64
	SustainSec                 int
	NotifyCooldownSec          int
	IncidentWindowSec          int
	AutoInvestigate            bool
	InvestigateMinSeverity     string
	PollIntervalSec            int
	ServicesIntervalSec        int
	DockerIntervalSec          int
	UpdatesIntervalSec         int
	PostureIntervalSec         int
}

func monitoringFrom(m monitor.Config, f fleet.Config) MonitoringSettings {
	return MonitoringSettings{m.CPUWarn, m.CPUCrit, m.MemWarn, m.MemCrit, m.SwapWarn, m.DiskWarn, m.DiskCrit, m.InodeWarn, m.InodeCrit, m.LoadPerThreadWarn, m.TempWarn, m.TempCrit,
		int(m.Sustain.Seconds()), int(m.NotifyCooldown.Seconds()), int(m.IncidentWindow.Seconds()), m.AutoInvestigate, m.InvestigateMinSeverity,
		int(f.PollInterval.Seconds()), int(f.ServicesInterval.Seconds()), int(f.DockerInterval.Seconds()), int(f.UpdatesInterval.Seconds()), int(f.PostureInterval.Seconds())}
}

func (ms MonitoringSettings) validate() error {
	pairs := [][3]any{{"CPU", ms.CPUWarn, ms.CPUCrit}, {"memory", ms.MemWarn, ms.MemCrit}, {"disk", ms.DiskWarn, ms.DiskCrit}, {"inode", ms.InodeWarn, ms.InodeCrit}, {"temperature", ms.TempWarn, ms.TempCrit}}
	for _, p := range pairs {
		wv, cv := p[1].(float64), p[2].(float64)
		if wv <= 0 || cv > 150 || wv >= cv {
			return fmt.Errorf("%s: warning threshold must be positive and below critical", p[0])
		}
	}
	switch {
	case ms.SustainSec < 30 || ms.SustainSec > 3600:
		return errors.New("sustain window must be 30 s – 1 h")
	case ms.PollIntervalSec < 5 || ms.PollIntervalSec > 300:
		return errors.New("poll interval must be 5–300 s (the agent samples every 5 s)")
	case ms.ServicesIntervalSec < 15, ms.DockerIntervalSec < 10, ms.UpdatesIntervalSec < 600, ms.PostureIntervalSec < 600:
		return errors.New("inventory intervals are too short (services ≥ 15 s, docker ≥ 10 s, updates and posture ≥ 10 min)")
	case ms.NotifyCooldownSec < 60:
		return errors.New("notification cooldown must be at least 60 s")
	case ms.InvestigateMinSeverity != "warning" && ms.InvestigateMinSeverity != "critical":
		return errors.New("investigation severity must be warning or critical")
	case ms.LoadPerThreadWarn <= 0 || ms.SwapWarn <= 0 || ms.SwapWarn > 100:
		return errors.New("load and swap thresholds must be positive")
	}
	return nil
}

func (ms MonitoringSettings) apply(m *monitor.Config, f *fleet.Config) {
	m.CPUWarn, m.CPUCrit, m.MemWarn, m.MemCrit, m.SwapWarn = ms.CPUWarn, ms.CPUCrit, ms.MemWarn, ms.MemCrit, ms.SwapWarn
	m.DiskWarn, m.DiskCrit, m.InodeWarn, m.InodeCrit, m.LoadPerThreadWarn = ms.DiskWarn, ms.DiskCrit, ms.InodeWarn, ms.InodeCrit, ms.LoadPerThreadWarn
	m.TempWarn, m.TempCrit = ms.TempWarn, ms.TempCrit
	m.Sustain, m.NotifyCooldown, m.IncidentWindow = time.Duration(ms.SustainSec)*time.Second, time.Duration(ms.NotifyCooldownSec)*time.Second, time.Duration(ms.IncidentWindowSec)*time.Second
	m.AutoInvestigate, m.InvestigateMinSeverity = ms.AutoInvestigate, ms.InvestigateMinSeverity
	f.PollInterval, f.ServicesInterval, f.DockerInterval = time.Duration(ms.PollIntervalSec)*time.Second, time.Duration(ms.ServicesIntervalSec)*time.Second, time.Duration(ms.DockerIntervalSec)*time.Second
	f.UpdatesInterval, f.PostureInterval = time.Duration(ms.UpdatesIntervalSec)*time.Second, time.Duration(ms.PostureIntervalSec)*time.Second
}

// LoadMonitoringSettings applies persisted monitoring settings at start-up.
func LoadMonitoringSettings(st *store.Store, m *monitor.Engine, f *fleet.Manager) {
	var ms MonitoringSettings
	if ok, _ := st.GetSetting(context.Background(), "monitoring", &ms); ok && ms.validate() == nil {
		mc, fc := m.Config(), f.Config()
		ms.apply(&mc, &fc)
		m.SetConfig(mc)
		f.SetConfig(fc)
	}
}

type notifyForm struct {
	MinSeverity string `json:"min_severity"`
	Discord     struct {
		Enabled bool   `json:"enabled"`
		Webhook string `json:"webhook"` // write-only; "" keeps the stored value
		Set     bool   `json:"set"`
	} `json:"discord"`
	Telegram struct {
		Enabled bool   `json:"enabled"`
		Token   string `json:"token"`
		ChatID  string `json:"chat_id"`
		Set     bool   `json:"set"`
	} `json:"telegram"`
	Email struct {
		Enabled  bool   `json:"enabled"`
		Host     string `json:"host"`
		Port     int    `json:"port"`
		Username string `json:"username"`
		Password string `json:"password"`
		From     string `json:"from"`
		To       string `json:"to"`
		Set      bool   `json:"set"`
	} `json:"email"`
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	switch r.PathValue("section") {
	case "monitoring":
		writeJSON(w, 200, monitoringFrom(s.Monitor.Config(), s.Fleet.Config()))
	case "ai":
		writeJSON(w, 200, s.AI.Settings())
	case "appearance":
		var a map[string]any
		_, _ = s.Store.GetSetting(r.Context(), "appearance", &a)
		writeJSON(w, 200, a)
	case "updates":
		writeJSON(w, 200, s.Update.Status().Settings)
	case "notifications":
		c := s.Notifier.Config()
		var f notifyForm
		f.MinSeverity = c.MinSeverity
		f.Discord.Enabled, f.Discord.Set = c.Discord.Enabled, c.Discord.WebhookSealed != ""
		f.Telegram.Enabled, f.Telegram.ChatID, f.Telegram.Set = c.Telegram.Enabled, c.Telegram.ChatID, c.Telegram.TokenSealed != ""
		f.Email.Enabled, f.Email.Host, f.Email.Port, f.Email.Username, f.Email.From, f.Email.To, f.Email.Set = c.Email.Enabled, c.Email.Host, c.Email.Port, c.Email.Username, c.Email.From, c.Email.To, c.Email.PasswordSealed != ""
		writeJSON(w, 200, f)
	default:
		writeErr(w, 404, errors.New("unknown settings section"))
	}
}

func (s *Server) putSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	switch r.PathValue("section") {
	case "monitoring":
		var ms MonitoringSettings
		if err := decode(r, &ms); err != nil {
			writeErr(w, 400, err)
			return
		}
		if err := ms.validate(); err != nil {
			writeErr(w, 400, err)
			return
		}
		mc, fc := s.Monitor.Config(), s.Fleet.Config()
		before := monitoringFrom(mc, fc)
		ms.apply(&mc, &fc)
		if err := s.Store.PutSetting(ctx, "monitoring", ms); err != nil {
			writeErr(w, 500, err)
			return
		}
		s.Monitor.SetConfig(mc)
		s.Fleet.SetConfig(fc)
		_ = s.Store.Audit(ctx, "operator", "settings.monitoring", "monitoring", map[string]any{"before": before, "after": ms})
		writeJSON(w, 200, ms)
	case "ai":
		var as ai.Settings
		if err := decode(r, &as); err != nil {
			writeErr(w, 400, err)
			return
		}
		if as.Roles == nil {
			as.Roles = map[string]string{}
		}
		if err := s.AI.UpdateSettings(ctx, as, "operator"); err != nil {
			writeErr(w, 400, err)
			return
		}
		writeJSON(w, 200, s.AI.Settings())
	case "appearance":
		var a map[string]any
		if err := json.NewDecoder(r.Body).Decode(&a); err != nil {
			writeErr(w, 400, err)
			return
		}
		allowed := map[string]bool{"theme": true, "accent": true, "density": true, "layout": true, "motion": true, "sidebar": true}
		for k := range a {
			if !allowed[k] {
				writeErr(w, 400, fmt.Errorf("unknown appearance key %q", k))
				return
			}
		}
		if err := s.Store.PutSetting(ctx, "appearance", a); err != nil {
			writeErr(w, 500, err)
			return
		}
		writeJSON(w, 200, a)
	case "updates":
		var us update.Settings
		if err := decode(r, &us); err != nil {
			writeErr(w, 400, err)
			return
		}
		if err := s.Store.PutSetting(ctx, "update.settings", us); err != nil {
			writeErr(w, 500, err)
			return
		}
		s.Update.SetSettings(us)
		_ = s.Store.Audit(ctx, "operator", "settings.updates", "updates", us)
		writeJSON(w, 200, us)
	case "notifications":
		var f notifyForm
		if err := decode(r, &f); err != nil {
			writeErr(w, 400, err)
			return
		}
		c := s.Notifier.Config()
		c.MinSeverity = f.MinSeverity
		seal := func(plain string, keep string) (string, error) {
			if plain == "" {
				return keep, nil
			}
			return s.Notifier.Seal(plain)
		}
		var err error
		c.Discord.Enabled = f.Discord.Enabled
		if c.Discord.WebhookSealed, err = seal(f.Discord.Webhook, c.Discord.WebhookSealed); err != nil {
			writeErr(w, 500, err)
			return
		}
		c.Telegram.Enabled, c.Telegram.ChatID = f.Telegram.Enabled, f.Telegram.ChatID
		if c.Telegram.TokenSealed, err = seal(f.Telegram.Token, c.Telegram.TokenSealed); err != nil {
			writeErr(w, 500, err)
			return
		}
		c.Email.Enabled, c.Email.Host, c.Email.Port, c.Email.Username, c.Email.From, c.Email.To = f.Email.Enabled, f.Email.Host, f.Email.Port, f.Email.Username, f.Email.From, f.Email.To
		if c.Email.PasswordSealed, err = seal(f.Email.Password, c.Email.PasswordSealed); err != nil {
			writeErr(w, 500, err)
			return
		}
		if err := s.Notifier.SetConfig(ctx, c); err != nil {
			writeErr(w, 400, err)
			return
		}
		_ = s.Store.Audit(ctx, "operator", "settings.notifications", "notifications", map[string]any{"discord": c.Discord.Enabled, "telegram": c.Telegram.Enabled, "email": c.Email.Enabled, "min_severity": c.MinSeverity})
		s.getSettings(w, r)
	default:
		writeErr(w, 404, errors.New("unknown settings section"))
	}
}

func (s *Server) testNotification(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Channel string `json:"channel"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, err)
		return
	}
	if err := s.Notifier.Test(r.Context(), req.Channel); err != nil {
		writeErr(w, 502, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

var _ = notify.Config{}

// ---------------------------------------------------------------- AI

func (s *Server) models(w http.ResponseWriter, r *http.Request) {
	m, err := s.AI.Models(r.Context())
	if err != nil {
		writeErr(w, 503, err)
		return
	}
	writeJSON(w, 200, m)
}

func (s *Server) pullModel(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Model string `json:"model"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, err)
		return
	}
	if err := s.AI.Pull(r.Context(), req.Model, "operator"); err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 202, map[string]string{"pulling": req.Model})
}

func (s *Server) deleteModel(w http.ResponseWriter, r *http.Request) {
	if err := s.AI.DeleteModel(r.Context(), r.PathValue("name"), "operator"); err != nil {
		writeErr(w, 502, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) hardware(w http.ResponseWriter, r *http.Request) {
	hw := s.AI.Hardware(r.Context())
	models, _ := s.AI.Models(r.Context())
	writeJSON(w, 200, map[string]any{"hardware": hw, "budget_bytes": hw.Budget(), "recommendations": ai.Recommend(hw, models)})
}

func (s *Server) listConversations(w http.ResponseWriter, r *http.Request) {
	c, err := s.Store.ListConversations(r.Context(), qInt(r, "limit", 100))
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, c)
}

func (s *Server) createConversation(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Title    string `json:"title"`
		ServerID string `json:"server_id"`
		Mode     string `json:"mode"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, err)
		return
	}
	if req.Mode == "" {
		req.Mode = string(ai.ModeAnalyst)
	}
	if _, err := ai.ParseMode(req.Mode); err != nil {
		writeErr(w, 400, err)
		return
	}
	if req.ServerID != "" {
		if _, err := s.Store.GetServer(r.Context(), req.ServerID); err != nil {
			writeErr(w, 400, errors.New("unknown server"))
			return
		}
	}
	if req.Title == "" {
		req.Title = "New conversation"
	}
	c := &store.Conversation{Title: req.Title, ServerID: req.ServerID, Mode: req.Mode}
	if err := s.Store.CreateConversation(r.Context(), c); err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 201, c)
}

func (s *Server) getConversation(w http.ResponseWriter, r *http.Request) {
	c, err := s.Store.GetConversation(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	msgs, err := s.Store.ListMessages(r.Context(), c.ID)
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	exes, _ := s.Store.ListExecutions(r.Context(), "", c.ID, 500)
	writeJSON(w, 200, map[string]any{"conversation": c, "messages": msgs, "executions": exes})
}

func (s *Server) patchConversation(w http.ResponseWriter, r *http.Request) {
	c, err := s.Store.GetConversation(r.Context(), r.PathValue("id"))
	if err != nil {
		writeErr(w, statusFor(err), err)
		return
	}
	var req struct {
		Title    *string `json:"title"`
		ServerID *string `json:"server_id"`
		Mode     *string `json:"mode"`
		Archived *bool   `json:"archived"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, err)
		return
	}
	if req.Mode != nil {
		if _, err := ai.ParseMode(*req.Mode); err != nil {
			writeErr(w, 400, err)
			return
		}
		if *req.Mode != c.Mode {
			_ = s.Store.Audit(r.Context(), "operator", "conversation.mode", c.ID, map[string]string{"from": c.Mode, "to": *req.Mode})
			ev := &store.Message{ConversationID: c.ID, Role: "event", Content: "Mode changed from " + c.Mode + " to " + *req.Mode, Meta: json.RawMessage(`{"kind":"mode"}`)}
			_ = s.Store.AddMessage(r.Context(), ev)
			s.Bus.Publish("chat.message", ev)
		}
		c.Mode = *req.Mode
	}
	if req.Title != nil {
		c.Title = *req.Title
	}
	if req.ServerID != nil {
		if *req.ServerID != "" {
			if _, err := s.Store.GetServer(r.Context(), *req.ServerID); err != nil {
				writeErr(w, 400, errors.New("unknown server"))
				return
			}
		}
		c.ServerID = *req.ServerID
	}
	if req.Archived != nil {
		c.Archived = *req.Archived
	}
	if err := s.Store.UpdateConversation(r.Context(), c); err != nil {
		writeErr(w, 500, err)
		return
	}
	s.Bus.Publish("chat.conversation", c)
	writeJSON(w, 200, c)
}

func (s *Server) deleteConversation(w http.ResponseWriter, r *http.Request) {
	s.AI.Stop(r.PathValue("id"))
	if err := s.Store.DeleteConversation(r.Context(), r.PathValue("id")); err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) sendMessage(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Content string `json:"content"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, err)
		return
	}
	m, err := s.AI.Send(r.Context(), r.PathValue("id"), req.Content)
	if err != nil {
		code := 400
		if errors.Is(err, ai.ErrBusy) {
			code = 409
		}
		writeErr(w, code, err)
		return
	}
	writeJSON(w, 202, m)
}

func (s *Server) stopConversation(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]bool{"stopped": s.AI.Stop(r.PathValue("id"))})
}

func (s *Server) listApprovals(w http.ResponseWriter, r *http.Request) {
	a, err := s.Store.ListApprovals(r.Context(), r.URL.Query().Get("status"), qInt(r, "limit", 100))
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, a)
}

func (s *Server) decide(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Approve bool   `json:"approve"`
		Reason  string `json:"reason"`
		Confirm string `json:"confirm"`
	}
	if err := decode(r, &req); err != nil {
		writeErr(w, 400, err)
		return
	}
	a, err := s.AI.Decide(r.Context(), r.PathValue("id"), req.Approve, "operator", req.Reason, req.Confirm)
	if err != nil {
		writeErr(w, 400, err)
		return
	}
	writeJSON(w, 200, a)
}

func (s *Server) listExecutions(w http.ResponseWriter, r *http.Request) {
	e, err := s.Store.ListExecutions(r.Context(), r.URL.Query().Get("server"), r.URL.Query().Get("conversation"), qInt(r, "limit", 200))
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, e)
}

func (s *Server) audit(w http.ResponseWriter, r *http.Request) {
	a, err := s.Store.ListAudit(r.Context(), qInt(r, "limit", 200), int64(qInt(r, "before", 0)), r.URL.Query().Get("action"))
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, a)
}

func (s *Server) verifyAudit(w http.ResponseWriter, r *http.Request) {
	broken, n, err := s.Store.VerifyAudit(r.Context())
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]any{"intact": broken == 0, "first_broken_id": broken, "verified_records": n})
}

// ---------------------------------------------------------------- memory & policy

func (s *Server) listMemory(w http.ResponseWriter, r *http.Request) {
	m, err := s.Store.ListMemory(r.Context(), r.URL.Query().Get("server"), r.URL.Query().Get("server") == "")
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	writeJSON(w, 200, m)
}

var memKinds = map[string]bool{"role": true, "service": true, "dependency": true, "config_path": true, "constraint": true, "maintenance_window": true, "incident": true, "note": true}

func (s *Server) putMemory(w http.ResponseWriter, r *http.Request) {
	var m store.Memory
	if err := json.NewDecoder(r.Body).Decode(&m); err != nil {
		writeErr(w, 400, err)
		return
	}
	if id := r.PathValue("id"); id != "" {
		m.ID = id
	}
	m.Content = strings.TrimSpace(m.Content)
	if m.Content == "" || len(m.Content) > 2000 || !memKinds[m.Kind] {
		writeErr(w, 400, errors.New("memory needs a kind (role, service, dependency, config_path, constraint, maintenance_window, incident, note) and content up to 2000 characters"))
		return
	}
	m.Source = "operator"
	m.ObservedAt = time.Time{}
	if err := s.Store.PutMemory(r.Context(), &m); err != nil {
		writeErr(w, 500, err)
		return
	}
	_ = s.Store.Audit(r.Context(), "operator", "memory.put", m.ID, map[string]string{"kind": m.Kind, "server": m.ServerID})
	writeJSON(w, 200, m)
}

func (s *Server) deleteMemory(w http.ResponseWriter, r *http.Request) {
	if err := s.Store.DeleteMemory(r.Context(), r.PathValue("id")); err != nil {
		writeErr(w, 500, err)
		return
	}
	_ = s.Store.Audit(r.Context(), "operator", "memory.delete", r.PathValue("id"), nil)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) listPolicies(w http.ResponseWriter, r *http.Request) {
	scopes, err := s.Store.ListPolicyScopes(r.Context())
	if err != nil {
		writeErr(w, 500, err)
		return
	}
	if scopes == nil {
		scopes = []string{}
	}
	writeJSON(w, 200, scopes)
}

func (s *Server) getPolicy(w http.ResponseWriter, r *http.Request) {
	scope := r.PathValue("scope")
	if scope == "*" || scope == "global" {
		p := ai.DefaultPolicy()
		ok, _ := s.Store.GetPolicy(r.Context(), "*", &p)
		writeJSON(w, 200, map[string]any{"policy": p, "scope": "global", "explicit": ok})
		return
	}
	p, src := s.Policies.Effective(r.Context(), scope)
	writeJSON(w, 200, map[string]any{"policy": p, "scope": src, "explicit": src == "server"})
}

func (s *Server) putPolicy(w http.ResponseWriter, r *http.Request) {
	scope := r.PathValue("scope")
	if scope == "global" {
		scope = "*"
	}
	if scope != "*" {
		if _, err := s.Store.GetServer(r.Context(), scope); err != nil {
			writeErr(w, 404, errors.New("unknown server"))
			return
		}
	}
	var p ai.Policy
	if err := decode(r, &p); err != nil {
		writeErr(w, 400, err)
		return
	}
	if err := p.Validate(s.Registry); err != nil {
		writeErr(w, 400, err)
		return
	}
	var before ai.Policy
	had, _ := s.Store.GetPolicy(r.Context(), scope, &before)
	if err := s.Store.PutPolicy(r.Context(), scope, p, "operator"); err != nil {
		writeErr(w, 500, err)
		return
	}
	detail := map[string]any{"after": p}
	if had {
		detail["before"] = before
	}
	_ = s.Store.Audit(r.Context(), "operator", "policy.update", scope, detail)
	s.Bus.Publish("policy", map[string]string{"scope": scope})
	writeJSON(w, 200, p)
}

func (s *Server) deletePolicy(w http.ResponseWriter, r *http.Request) {
	scope := r.PathValue("scope")
	if scope == "global" {
		scope = "*"
	}
	if err := s.Store.DeletePolicy(r.Context(), scope); err != nil {
		writeErr(w, 500, err)
		return
	}
	_ = s.Store.Audit(r.Context(), "operator", "policy.reset", scope, nil)
	s.Bus.Publish("policy", map[string]string{"scope": scope})
	writeJSON(w, 200, map[string]bool{"ok": true})
}
