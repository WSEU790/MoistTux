package monitor

import (
	"context"
	"io"
	"log"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/WSEU790/MoistTux/internal/core/events"
	"github.com/WSEU790/MoistTux/internal/core/store"
	"github.com/WSEU790/MoistTux/internal/protocol"
)

type capture struct {
	mu sync.Mutex
	n  []*store.Notification
}

func (c *capture) Notify(_ context.Context, n *store.Notification) {
	c.mu.Lock()
	c.n = append(c.n, n)
	c.mu.Unlock()
}

func setup(t *testing.T) (*Engine, *store.Store, *store.Server, *capture) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	srv := &store.Server{Name: "db-01", Address: "10.0.0.1", Port: 7443, AgentFingerprint: "ab", Environment: "production", MonitoringEnabled: true}
	if err := st.CreateServer(context.Background(), srv); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.Sustain = 50 * time.Second
	c := &capture{}
	return New(st, events.New(), c, cfg, log.New(io.Discard, "", 0)), st, srv, c
}

func samples(start time.Time, n int, cpu float64) []protocol.MetricsSample {
	var out []protocol.MetricsSample
	for i := 0; i < n; i++ {
		out = append(out, protocol.MetricsSample{Seq: uint64(i + 1), Time: start.Add(time.Duration(i) * 5 * time.Second), CPU: protocol.CPUStats{UsagePct: cpu, Threads: 4}, Mem: protocol.MemStats{UsedPct: 40}})
	}
	return out
}

func TestSustainedCPUAlertLifecycle(t *testing.T) {
	e, st, srv, c := setup(t)
	ctx := context.Background()
	// A short spike does not alert.
	e.OnSamples(srv, samples(time.Now().Add(-10*time.Second), 3, 99))
	if a, _ := st.ListAlerts(ctx, "firing", "", 10); len(a) != 0 {
		t.Fatalf("spike raised %d alerts", len(a))
	}
	// A sustained window does.
	e.OnSamples(srv, samples(time.Now().Add(-55*time.Second), 12, 97))
	a, _ := st.ListAlerts(ctx, "firing", "", 10)
	if len(a) != 1 || a[0].Rule != "cpu_high" || a[0].Severity != "critical" {
		t.Fatalf("alerts = %+v", a)
	}
	if a[0].IncidentID == "" {
		t.Fatal("critical alert not grouped into an incident")
	}
	// Re-evaluation de-duplicates instead of creating new alerts or notifications.
	e.OnSamples(srv, samples(time.Now().Add(-55*time.Second), 12, 97))
	a2, _ := st.ListAlerts(ctx, "firing", "", 10)
	if len(a2) != 1 || a2[0].Count < 2 {
		t.Fatalf("not de-duplicated: %+v", a2)
	}
	if len(c.n) != 1 {
		t.Fatalf("notifications = %d, want 1 (cooldown)", len(c.n))
	}
	// Recovery resolves the alert and its incident.
	e.mu.Lock()
	e.windows = map[string][]sample{}
	e.mu.Unlock()
	e.OnSamples(srv, samples(time.Now().Add(-55*time.Second), 12, 10))
	if a, _ := st.ListAlerts(ctx, "firing", "", 10); len(a) != 0 {
		t.Fatalf("alert not resolved: %+v", a)
	}
	inc, _ := st.GetIncident(ctx, a2[0].IncidentID)
	if inc.Status != "resolved" {
		t.Fatalf("incident status %s", inc.Status)
	}
}

func TestRelatedAlertsShareIncident(t *testing.T) {
	e, st, srv, _ := setup(t)
	ctx := context.Background()
	e.OnInventory(srv, "services", &protocol.ServiceList{Available: true, Services: []protocol.Service{{Unit: "nginx.service", ActiveState: "failed", SubState: "failed"}}})
	cs := []protocol.Container{{Name: "app", Image: "app:1", State: "exited", Status: "Exited (137) 1 minute ago"}}
	e.OnInventory(srv, "containers", &cs)
	a, _ := st.ListAlerts(ctx, "firing", "", 10)
	if len(a) != 2 {
		t.Fatalf("want 2 alerts, got %d", len(a))
	}
	if a[0].IncidentID == "" || a[0].IncidentID != a[1].IncidentID {
		t.Fatalf("alerts were not correlated: %s vs %s", a[0].IncidentID, a[1].IncidentID)
	}
	// Service recovers: only its alert resolves.
	e.OnInventory(srv, "services", &protocol.ServiceList{Available: true, Services: []protocol.Service{{Unit: "nginx.service", ActiveState: "active", SubState: "running"}}})
	a, _ = st.ListAlerts(ctx, "firing", "", 10)
	if len(a) != 1 || a[0].Rule != "container_exited" {
		t.Fatalf("after recovery: %+v", a)
	}
}

func TestOfflineAndReturn(t *testing.T) {
	e, st, srv, _ := setup(t)
	ctx := context.Background()
	e.OnStatus(srv, "online", "offline", "dial tcp: connection refused")
	if a, _ := st.ListAlerts(ctx, "firing", "", 10); len(a) != 1 || a[0].Rule != "agent_offline" {
		t.Fatalf("offline alert missing: %+v", a)
	}
	e.OnStatus(srv, "offline", "online", "")
	if a, _ := st.ListAlerts(ctx, "firing", "", 10); len(a) != 0 {
		t.Fatalf("offline alert not resolved: %+v", a)
	}
}
