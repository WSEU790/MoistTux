// Package fleet owns the managed-server inventory and the per-server polling
// workers that pull telemetry and inventory from agents.
package fleet

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/WSEU790/MoistTux/internal/core/agentclient"
	"github.com/WSEU790/MoistTux/internal/core/events"
	"github.com/WSEU790/MoistTux/internal/core/identity"
	"github.com/WSEU790/MoistTux/internal/core/store"
	"github.com/WSEU790/MoistTux/internal/protocol"
)

type Config struct {
	PollInterval     time.Duration
	HealthInterval   time.Duration
	InfoInterval     time.Duration
	ServicesInterval time.Duration
	DockerInterval   time.Duration
	UpdatesInterval  time.Duration
	PostureInterval  time.Duration
	OfflineAfter     int
}

func DefaultConfig() Config {
	return Config{
		PollInterval: 10 * time.Second, HealthInterval: 30 * time.Second, InfoInterval: 30 * time.Minute,
		ServicesInterval: time.Minute, DockerInterval: 30 * time.Second, UpdatesInterval: 6 * time.Hour,
		PostureInterval: 6 * time.Hour, OfflineAfter: 3,
	}
}

// Hooks let the monitoring engine observe fleet data without import cycles.
type Hooks struct {
	OnSamples   func(srv *store.Server, samples []protocol.MetricsSample)
	OnInventory func(srv *store.Server, kind string, data any)
	OnStatus    func(srv *store.Server, prev, cur, reason string)
	OnReboot    func(srv *store.Server, prevBoot, newBoot string, bootTime time.Time)
}

type Manager struct {
	st    *store.Store
	bus   *events.Bus
	log   *log.Logger
	ident func() *identity.Identity

	mu      sync.RWMutex
	cfg     Config
	hooks   Hooks
	workers map[string]*worker
	ctx     context.Context
}

func New(st *store.Store, bus *events.Bus, ident func() *identity.Identity, cfg Config, logger *log.Logger) *Manager {
	return &Manager{st: st, bus: bus, ident: ident, cfg: cfg, log: logger, workers: map[string]*worker{}}
}

func (m *Manager) SetHooks(h Hooks) { m.mu.Lock(); m.hooks = h; m.mu.Unlock() }

func (m *Manager) SetConfig(cfg Config) {
	m.mu.Lock()
	m.cfg = cfg
	m.mu.Unlock()
}

func (m *Manager) Config() Config { m.mu.RLock(); defer m.mu.RUnlock(); return m.cfg }

// Start launches workers for all stored servers.
func (m *Manager) Start(ctx context.Context) error {
	m.ctx = ctx
	servers, err := m.st.ListServers(ctx)
	if err != nil {
		return err
	}
	for _, s := range servers {
		m.startWorker(s)
	}
	return nil
}

func (m *Manager) newClient(s *store.Server) *agentclient.Client {
	fps := []string{s.AgentFingerprint}
	if s.PendingFingerprint != "" {
		fps = append(fps, s.PendingFingerprint)
	}
	return agentclient.New(s.Address, s.Port, m.ident().Cert, fps...)
}

func (m *Manager) startWorker(s *store.Server) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if old, ok := m.workers[s.ID]; ok {
		old.stop()
	}
	ctx, cancel := context.WithCancel(m.ctx)
	w := &worker{m: m, id: s.ID, client: m.newClient(s), cancel: cancel, done: make(chan struct{}), lastRun: map[string]time.Time{}}
	m.workers[s.ID] = w
	go w.run(ctx)
}

func (m *Manager) worker(id string) (*worker, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	w, ok := m.workers[id]
	if !ok {
		return nil, store.ErrNotFound
	}
	return w, nil
}

// Client returns the authenticated agent client for a server.
func (m *Manager) Client(id string) (*agentclient.Client, error) {
	w, err := m.worker(id)
	if err != nil {
		return nil, err
	}
	return w.client, nil
}

// Latest returns the most recent metrics sample held in memory.
func (m *Manager) Latest(id string) (*protocol.MetricsSample, time.Time) {
	w, err := m.worker(id)
	if err != nil {
		return nil, time.Time{}
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.latest, w.latestAt
}

// ---------------------------------------------------------------- enrollment

type EnrollRequest struct {
	Name        string   `json:"name"`
	Address     string   `json:"address"`
	Port        int      `json:"port"`
	Code        string   `json:"code"`
	Environment string   `json:"environment"`
	Group       string   `json:"group"`
	Tags        []string `json:"tags"`
}

func (m *Manager) Enroll(ctx context.Context, req EnrollRequest) (*store.Server, error) {
	req.Address = strings.TrimSpace(req.Address)
	if req.Address == "" {
		return nil, errors.New("address is required")
	}
	if strings.ContainsAny(req.Address, "/ ") {
		return nil, errors.New("address must be a hostname or IP address")
	}
	if req.Port == 0 {
		req.Port = 7443
	}
	if req.Port < 1 || req.Port > 65535 {
		return nil, errors.New("port out of range")
	}
	switch req.Environment {
	case "", "production", "staging", "development", "lab":
	default:
		return nil, errors.New("environment must be production, staging, development or lab")
	}
	existing, _ := m.st.ListServers(ctx)
	for _, s := range existing {
		if strings.EqualFold(s.Address, req.Address) && s.Port == req.Port {
			return nil, fmt.Errorf("%s:%d is already managed as %q", req.Address, req.Port, s.Name)
		}
	}
	id := m.ident()
	host, _ := os.Hostname()
	ectx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	res, err := agentclient.Enroll(ectx, req.Address, req.Port, req.Code, id.Cert, id.CertPEM, "NEXUS on "+host)
	if err != nil {
		return nil, err
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = res.Response.Hostname
	}
	now := time.Now().UTC()
	srv := &store.Server{
		Name: name, Address: req.Address, Port: req.Port, AgentID: res.Response.AgentID, AgentFingerprint: res.Fingerprint,
		Environment: req.Environment, Group: req.Group, Tags: req.Tags, MonitoringEnabled: true, EnrolledAt: &now,
	}
	if err := m.st.CreateServer(ctx, srv); err != nil {
		return nil, err
	}
	_ = m.st.Audit(ctx, "operator", "server.enroll", srv.ID, map[string]any{"name": srv.Name, "address": net.JoinHostPort(srv.Address, fmt.Sprint(srv.Port)), "agent_id": srv.AgentID, "fingerprint": srv.AgentFingerprint, "agent_version": res.Response.AgentVersion})
	m.startWorker(srv)
	m.bus.Publish("server.added", srv)
	return srv, nil
}

// Remove stops monitoring a server. With unenroll, the core also asks the
// agent to forget its certificate (best effort when the agent is reachable).
func (m *Manager) Remove(ctx context.Context, id string, unenroll bool) error {
	srv, err := m.st.GetServer(ctx, id)
	if err != nil {
		return err
	}
	var unenrollErr error
	if w, err := m.worker(id); err == nil {
		if unenroll {
			uctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			unenrollErr = w.client.Post(uctx, "/v1/unenroll", nil, nil)
			cancel()
		}
		w.stop()
		m.mu.Lock()
		delete(m.workers, id)
		m.mu.Unlock()
	}
	if err := m.st.DeleteServer(ctx, id); err != nil {
		return err
	}
	detail := map[string]any{"name": srv.Name, "unenroll_requested": unenroll}
	if unenrollErr != nil {
		detail["unenroll_error"] = unenrollErr.Error()
	}
	_ = m.st.Audit(ctx, "operator", "server.remove", id, detail)
	m.bus.Publish("server.removed", map[string]string{"id": id})
	if unenrollErr != nil {
		return fmt.Errorf("server removed from NEXUS, but the agent could not be told to revoke this core (%v); run `nexus-agent revoke --all` on the server", unenrollErr)
	}
	return nil
}

// Reconnect rebuilds the client after address/port edits.
func (m *Manager) Reconnect(ctx context.Context, id string) error {
	srv, err := m.st.GetServer(ctx, id)
	if err != nil {
		return err
	}
	m.startWorker(srv)
	return nil
}

// RotateAgentCertificate asks the agent for a new identity and re-pins it.
func (m *Manager) RotateAgentCertificate(ctx context.Context, id string) (string, error) {
	w, err := m.worker(id)
	if err != nil {
		return "", err
	}
	srv, err := m.st.GetServer(ctx, id)
	if err != nil {
		return "", err
	}
	var rr protocol.RotateAgentResponse
	if err := w.client.Post(ctx, "/v1/credentials/rotate-agent", nil, &rr); err != nil {
		return "", err
	}
	// Accept both identities while the agent switches over.
	if err := m.st.SetFingerprints(ctx, id, srv.AgentFingerprint, rr.NewFingerprint); err != nil {
		return "", err
	}
	w.client.SetPinned(srv.AgentFingerprint, rr.NewFingerprint)
	if err := w.client.Post(ctx, "/v1/credentials/activate-agent", nil, nil); err != nil {
		return "", err
	}
	w.client.SetPinned(rr.NewFingerprint)
	if _, err := w.client.Health(ctx); err != nil {
		return "", fmt.Errorf("agent did not answer with its new certificate: %w", err)
	}
	if err := m.st.SetFingerprints(ctx, id, rr.NewFingerprint, ""); err != nil {
		return "", err
	}
	_ = m.st.Audit(ctx, "operator", "server.rotate_agent_cert", id, map[string]any{"old": srv.AgentFingerprint, "new": rr.NewFingerprint})
	return rr.NewFingerprint, nil
}

// ---------------------------------------------------------------- worker

type worker struct {
	m      *Manager
	id     string
	client *agentclient.Client
	cancel context.CancelFunc
	done   chan struct{}

	mu       sync.Mutex
	latest   *protocol.MetricsSample
	latestAt time.Time
	failures int
	lastRun  map[string]time.Time
}

func (w *worker) stop() {
	w.cancel()
	<-w.done
	w.client.Close()
}

func (w *worker) run(ctx context.Context) {
	defer close(w.done)
	// Stagger start-up so many servers do not poll in lockstep.
	select {
	case <-ctx.Done():
		return
	case <-time.After(time.Duration(rand.Intn(1500)) * time.Millisecond):
	}
	for {
		interval := w.tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

func (w *worker) due(kind string, every time.Duration) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	last, ok := w.lastRun[kind]
	return !ok || time.Since(last) >= every
}

func (w *worker) markRun(kind string) {
	w.mu.Lock()
	w.lastRun[kind] = time.Now()
	w.mu.Unlock()
}

func (w *worker) tick(ctx context.Context) time.Duration {
	m := w.m
	cfg := m.Config()
	srv, err := m.st.GetServer(ctx, w.id)
	if err != nil {
		return cfg.PollInterval
	}
	m.mu.RLock()
	hooks := m.hooks
	m.mu.RUnlock()
	if !srv.MonitoringEnabled {
		w.setStatus(ctx, srv, "paused", "monitoring disabled by operator", false, hooks)
		return cfg.PollInterval
	}
	cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	if w.due("health", cfg.HealthInterval) || srv.Status != "online" && srv.Status != "degraded" {
		h, err := w.client.Health(cctx)
		if err != nil {
			return w.fail(ctx, srv, err, hooks)
		}
		w.markRun("health")
		if srv.BootID != "" && h.BootID != "" && srv.BootID != h.BootID && hooks.OnReboot != nil {
			hooks.OnReboot(srv, srv.BootID, h.BootID, h.BootTime)
		}
		_ = m.st.SetServerHealth(ctx, srv.ID, h)
		srv.Health = h
		srv.BootID = h.BootID
	}

	batch, err := w.client.Metrics(cctx, srv.LastSeq)
	if err != nil {
		return w.fail(ctx, srv, err, hooks)
	}
	if batch.LastSeq < srv.LastSeq {
		// Agent restarted (sequence reset): fetch its whole buffer.
		batch, err = w.client.Metrics(cctx, 0)
		if err != nil {
			return w.fail(ctx, srv, err, hooks)
		}
	}
	if len(batch.Samples) > 0 {
		if err := m.st.InsertSamples(ctx, srv.ID, batch.Samples); err != nil {
			m.log.Printf("store samples for %s: %v", srv.Name, err)
		}
		last := batch.Samples[len(batch.Samples)-1]
		w.mu.Lock()
		w.latest, w.latestAt = &last, time.Now()
		w.mu.Unlock()
		_ = m.st.SetLastSeq(ctx, srv.ID, batch.LastSeq)
		m.bus.Publish("metrics", map[string]any{"server_id": srv.ID, "sample": last})
		if hooks.OnSamples != nil {
			hooks.OnSamples(srv, batch.Samples)
		}
	}
	w.mu.Lock()
	w.failures = 0
	w.mu.Unlock()
	status, reason := "online", ""
	if srv.Health != nil && !srv.Health.SamplerOK && srv.Health.SamplerError != "" {
		status, reason = "degraded", "agent telemetry sampler: "+srv.Health.SamplerError
	} else if srv.Health != nil && !srv.Health.HelperOK {
		reason = "privileged helper unavailable: " + srv.Health.HelperError
	}
	w.setStatus(ctx, srv, status, reason, true, hooks)

	// Slower inventory refreshes, one per tick to spread load.
	switch {
	case w.due("info", cfg.InfoInterval):
		if info, err := w.client.Info(cctx); err == nil {
			_ = m.st.SetServerInfo(ctx, srv.ID, info)
			w.markRun("info")
			m.bus.Publish("server.updated", map[string]string{"id": srv.ID})
		}
	case w.due("services", cfg.ServicesInterval) && hasCap(srv, "services"):
		w.refresh(cctx, srv, "services", "/v1/services", &protocol.ServiceList{}, hooks)
	case w.due("containers", cfg.DockerInterval) && hasCap(srv, "docker"):
		var cs []protocol.Container
		w.refresh(cctx, srv, "containers", "/v1/docker/containers", &cs, hooks)
	case w.due("network", 5*time.Minute):
		w.refresh(cctx, srv, "network", "/v1/network", &protocol.NetworkOverview{}, hooks)
	case w.due("updates", cfg.UpdatesInterval) && hasCap(srv, "packages:apt"):
		w.refresh(cctx, srv, "updates", "/v1/packages/updates", &protocol.UpdateCheck{}, hooks)
	case w.due("posture", cfg.PostureInterval):
		w.refresh(cctx, srv, "posture", "/v1/security/posture", &protocol.SecurityPosture{}, hooks)
	}
	return cfg.PollInterval
}

func hasCap(s *store.Server, c string) bool {
	if s.Health == nil {
		return false
	}
	for _, x := range s.Health.Capabilities {
		if x == c {
			return true
		}
	}
	return false
}

func (w *worker) refresh(ctx context.Context, srv *store.Server, kind, path string, out any, hooks Hooks) {
	w.markRun(kind)
	err := w.client.Get(ctx, path, nil, out)
	msg := ""
	if err != nil {
		msg = err.Error()
		_ = w.m.st.PutInventory(ctx, srv.ID, kind, nil, msg)
		return
	}
	_ = w.m.st.PutInventory(ctx, srv.ID, kind, out, "")
	w.m.bus.Publish("inventory", map[string]string{"server_id": srv.ID, "kind": kind})
	if hooks.OnInventory != nil {
		hooks.OnInventory(srv, kind, out)
	}
}

// RefreshNow forces an inventory kind to refresh on the next tick.
func (m *Manager) RefreshNow(id, kind string) {
	if w, err := m.worker(id); err == nil {
		w.mu.Lock()
		delete(w.lastRun, kind)
		w.mu.Unlock()
	}
}

func (w *worker) fail(ctx context.Context, srv *store.Server, err error, hooks Hooks) time.Duration {
	cfg := w.m.Config()
	w.mu.Lock()
	w.failures++
	n := w.failures
	w.mu.Unlock()
	if errors.Is(err, agentclient.ErrFingerprintMismatch) {
		w.setStatus(ctx, srv, "untrusted", err.Error(), false, hooks)
		return time.Minute
	}
	if n >= cfg.OfflineAfter {
		w.setStatus(ctx, srv, "offline", err.Error(), false, hooks)
		backoff := cfg.PollInterval * time.Duration(1<<min(n-cfg.OfflineAfter, 3))
		if backoff > time.Minute {
			backoff = time.Minute
		}
		return backoff
	}
	return cfg.PollInterval
}

func (w *worker) setStatus(ctx context.Context, srv *store.Server, status, reason string, seen bool, hooks Hooks) {
	prev := srv.Status
	_ = w.m.st.SetServerStatus(ctx, srv.ID, status, reason, seen)
	if prev != status || srv.StatusReason != reason {
		w.m.bus.Publish("server.status", map[string]string{"id": srv.ID, "status": status, "prev": prev, "reason": reason})
		if prev != status && hooks.OnStatus != nil {
			hooks.OnStatus(srv, prev, status, reason)
		}
	}
}

// HealthScore summarises a server 0–100 from live data and firing alerts.
// It returns nil when there is no live data to score.
func HealthScore(srv *store.Server, latest *protocol.MetricsSample, alerts []*store.Alert) *int {
	if srv.Status != "online" && srv.Status != "degraded" || latest == nil {
		return nil
	}
	score := 100
	for _, a := range alerts {
		switch a.Severity {
		case "critical":
			score -= 25
		case "warning":
			score -= 10
		default:
			score -= 2
		}
	}
	penal := func(v, warn, crit float64) {
		if v >= crit {
			score -= 15
		} else if v >= warn {
			score -= 5
		}
	}
	penal(latest.CPU.UsagePct, 80, 95)
	penal(latest.Mem.UsedPct, 85, 95)
	for _, f := range latest.FS {
		penal(f.UsedPct, 85, 95)
	}
	if srv.Status == "degraded" {
		score -= 10
	}
	if score < 0 {
		score = 0
	}
	return &score
}
