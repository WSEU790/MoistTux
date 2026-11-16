// Package agentupdate keeps the Linux agents at the version bundled with this
// NEXUS. Upgrades travel over the existing agent connection (direct or SSH
// tunnel): NEXUS uploads the embedded agent with a grant signed by its
// identity key for exactly that binary on that server, and the agent's root
// helper verifies the grant before installing (see internal/agent/upgrade.go).
// Agents that do not trust this NEXUS yet, or predate the feature, need one
// Upgrade/repair over SSH.
package agentupdate

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/WSEU790/MoistTux/internal/core/agentbin"
	"github.com/WSEU790/MoistTux/internal/core/agentclient"
	"github.com/WSEU790/MoistTux/internal/core/events"
	"github.com/WSEU790/MoistTux/internal/core/fleet"
	"github.com/WSEU790/MoistTux/internal/core/store"
	"github.com/WSEU790/MoistTux/internal/protocol"
)

// Settings is the "agents" settings section.
type Settings struct {
	AutoUpgrade bool `json:"auto_upgrade"` // upgrade outdated agents automatically
}

// AgentStatus describes one server's agent.
type AgentStatus struct {
	ServerID string `json:"server_id"`
	Name     string `json:"name"`
	Version  string `json:"version"`
	Latest   string `json:"latest"`
	Outdated bool   `json:"outdated"`
	Bundled  bool   `json:"bundled"` // NEXUS carries an agent for this server's architecture
	State    string `json:"state,omitempty"`
	Message  string `json:"message,omitempty"`
}

type Manager struct {
	fleet   *fleet.Manager
	st      *store.Store
	bus     *events.Bus
	log     *log.Logger
	version string

	mu       sync.Mutex
	settings Settings
	jobs     map[string]*AgentStatus // last upgrade attempt per server
}

func New(f *fleet.Manager, st *store.Store, bus *events.Bus, logger *log.Logger, version string) *Manager {
	m := &Manager{fleet: f, st: st, bus: bus, log: logger, version: version, jobs: map[string]*AgentStatus{}}
	_, _ = st.GetSetting(context.Background(), "agents", &m.settings)
	return m
}

func (m *Manager) Settings() Settings { m.mu.Lock(); defer m.mu.Unlock(); return m.settings }

func (m *Manager) SetSettings(ctx context.Context, s Settings) error {
	if err := m.st.PutSetting(ctx, "agents", s); err != nil {
		return err
	}
	m.mu.Lock()
	m.settings = s
	m.mu.Unlock()
	return nil
}

// Status lists every server's agent version against the bundled one.
func (m *Manager) Status(ctx context.Context) ([]AgentStatus, error) {
	servers, err := m.st.ListServers(ctx)
	if err != nil {
		return nil, err
	}
	out := []AgentStatus{}
	for _, s := range servers {
		a := AgentStatus{ServerID: s.ID, Name: s.Name, Latest: m.version}
		if s.Health != nil {
			a.Version = s.Health.AgentVersion
		}
		if s.Info != nil {
			if arch, err := agentbin.NormalizeArch(s.Info.Arch); err == nil {
				_, err := agentbin.Get(arch)
				a.Bundled = err == nil
			}
		}
		a.Outdated = a.Version != "" && a.Version != m.version && a.Bundled
		m.mu.Lock()
		if j, ok := m.jobs[s.ID]; ok {
			a.State, a.Message = j.State, j.Message
		}
		m.mu.Unlock()
		out = append(out, a)
	}
	return out, nil
}

func (m *Manager) setJob(id, name, state, msg string) {
	m.mu.Lock()
	m.jobs[id] = &AgentStatus{ServerID: id, Name: name, State: state, Message: msg}
	m.mu.Unlock()
	m.bus.Publish("agent.upgrade", map[string]string{"server_id": id, "state": state, "message": msg})
}

// ErrNeedsRepair means the agent cannot be upgraded over its connection.
var ErrNeedsRepair = errors.New("this agent cannot be upgraded over its connection yet; run Upgrade/repair over SSH once from the server's settings")

// Upgrade installs the bundled agent on one server and waits until it
// answers with the new version.
func (m *Manager) Upgrade(ctx context.Context, serverID, by string) error {
	srv, err := m.st.GetServer(ctx, serverID)
	if err != nil {
		return err
	}
	fail := func(err error) error {
		m.setJob(srv.ID, srv.Name, "failed", err.Error())
		_ = m.st.Audit(context.Background(), by, "agent.upgrade.failed", srv.ID, map[string]any{"name": srv.Name, "error": err.Error()})
		return err
	}
	if srv.Info == nil || srv.Info.MachineID == "" {
		return fail(errors.New("the agent has not reported its system information yet"))
	}
	arch, err := agentbin.NormalizeArch(srv.Info.Arch)
	if err != nil {
		return fail(err)
	}
	bin, err := agentbin.Get(arch)
	if err != nil {
		return fail(fmt.Errorf("no bundled agent for %s: %w", arch, err))
	}
	cl, err := m.fleet.Client(srv.ID)
	if err != nil {
		return fail(err)
	}
	from := ""
	if srv.Health != nil {
		from = srv.Health.AgentVersion
	}
	m.setJob(srv.ID, srv.Name, "running", "uploading agent "+m.version)
	nb := make([]byte, 16)
	_, _ = rand.Read(nb)
	g := protocol.UpgradeGrant{V: 1, MachineID: srv.Info.MachineID, SHA256: bin.SHA256, Version: m.version, Nonce: hex.EncodeToString(nb), Expires: time.Now().Add(5 * time.Minute).Unix()}
	raw, _ := json.Marshal(g)
	sig, err := m.fleet.SignGrant(raw)
	if err != nil {
		return fail(err)
	}
	uctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
	defer cancel()
	var res protocol.CommandResult
	err = cl.Post(uctx, "/v1/self-upgrade", protocol.SelfUpgradeRequest{Binary: base64.StdEncoding.EncodeToString(bin.Data), Grant: base64.StdEncoding.EncodeToString(raw), Sig: sig}, &res)
	transportLost := false
	if err != nil {
		var ae *agentclient.Error
		switch {
		case errors.As(err, &ae) && (ae.Status == 404 || ae.Status == 405):
			return fail(fmt.Errorf("%w (agent %s predates one-click upgrades)", ErrNeedsRepair, from))
		case strings.Contains(err.Error(), "not enabled on this server") || strings.Contains(err.Error(), "not signed by a NEXUS installation") || strings.Contains(err.Error(), "privileged helper"):
			return fail(fmt.Errorf("%w: %v", ErrNeedsRepair, err))
		case errors.As(err, &ae):
			return fail(err)
		}
		// The connection dropped (the agent may already be restarting): the
		// version check below decides whether the upgrade happened.
		transportLost = true
		m.log.Printf("agent upgrade %s: response lost (%v); checking the version", srv.Name, err)
	}
	if !transportLost && res.ExitCode != 0 {
		return fail(fmt.Errorf("agent install failed (exit %d): %s", res.ExitCode, lastLines(res.Stdout+"\n"+res.Stderr, 4)))
	}
	m.setJob(srv.ID, srv.Name, "running", "waiting for the agent to restart")
	_ = m.fleet.Reconnect(ctx, srv.ID)
	deadline := time.Now().Add(90 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		time.Sleep(2 * time.Second)
		c, err := m.fleet.Client(srv.ID)
		if err != nil {
			lastErr = err
			continue
		}
		hctx, hc := context.WithTimeout(ctx, 5*time.Second)
		h, err := c.Health(hctx)
		hc()
		if err != nil {
			lastErr = err
			continue
		}
		if h.AgentVersion == m.version {
			m.setJob(srv.ID, srv.Name, "succeeded", fmt.Sprintf("upgraded %s → %s", from, m.version))
			_ = m.st.Audit(context.Background(), by, "agent.upgrade", srv.ID, map[string]any{"name": srv.Name, "from": from, "to": m.version, "sha256": bin.SHA256})
			return nil
		}
		lastErr = fmt.Errorf("agent still reports %s", h.AgentVersion)
	}
	return fail(fmt.Errorf("the agent did not come back with version %s: %v", m.version, lastErr))
}

// UpgradeAll upgrades every outdated agent, one at a time.
func (m *Manager) UpgradeAll(ctx context.Context, by string) (ok, failed int) {
	st, err := m.Status(ctx)
	if err != nil {
		return 0, 0
	}
	for _, a := range st {
		if !a.Outdated {
			continue
		}
		if err := m.Upgrade(ctx, a.ServerID, by); err != nil {
			failed++
			m.log.Printf("agent upgrade %s: %v", a.Name, err)
		} else {
			ok++
		}
	}
	return ok, failed
}

// Run upgrades outdated agents automatically when the setting is on. A
// server whose upgrade failed is not retried until NEXUS restarts, so a
// server needing an SSH repair is not hammered.
func (m *Manager) Run(ctx context.Context, every time.Duration) {
	if strings.Contains(m.version, "-dev") {
		return
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if !m.Settings().AutoUpgrade {
			continue
		}
		st, err := m.Status(ctx)
		if err != nil {
			continue
		}
		for _, a := range st {
			if !a.Outdated || a.State == "failed" || a.State == "running" {
				continue
			}
			if err := m.Upgrade(ctx, a.ServerID, "nexus (automatic agent upgrade)"); err != nil {
				m.log.Printf("automatic agent upgrade %s: %v", a.Name, err)
			}
		}
	}
}

func lastLines(s string, n int) string {
	l := strings.Split(strings.TrimSpace(s), "\n")
	if len(l) > n {
		l = l[len(l)-n:]
	}
	return strings.Join(l, " / ")
}
