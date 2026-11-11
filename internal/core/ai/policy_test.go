package ai

import (
	"testing"
	"time"

	"github.com/WSEU790/MoistTux/internal/core/ollama"
	"github.com/WSEU790/MoistTux/internal/core/store"
)

type ollamaCaps = ollama.Capabilities

func tool(t *testing.T, r *Registry, name string) *Tool {
	t.Helper()
	x, ok := r.Get(name)
	if !ok {
		t.Fatalf("tool %s missing", name)
	}
	return x
}

func TestDecideModesAndRisks(t *testing.T) {
	r := BuildRegistry()
	p := DefaultPolicy()
	prod := &store.Server{Environment: "production"}
	now := time.Now()
	cases := []struct {
		mode Mode
		tool string
		args map[string]any
		want Decision
	}{
		{ModeAssistant, "list_processes", nil, Allow},
		{ModeAssistant, "restart_service", map[string]any{"unit": "nginx"}, Deny},
		{ModeAnalyst, "apply_security_updates", nil, Deny},
		{ModeAdministrator, "restart_service", map[string]any{"unit": "nginx"}, Approve},
		{ModeAdministrator, "refresh_package_lists", nil, Approve},
		{ModeAdministrator, "reboot_server", map[string]any{"reason": "x"}, Deny}, // reboots not allowed by default
		{ModeAutonomous, "restart_service", map[string]any{"unit": "nginx"}, Approve}, // autonomy disabled
		{ModeAssistant, "remember_fact", nil, Allow},                                   // internal
	}
	for _, c := range cases {
		v := Decide(c.mode, tool(t, r, c.tool), c.args, prod, p, now)
		if v.Decision != c.want {
			t.Errorf("%s/%s: got %s (%s), want %s", c.mode, c.tool, v.Decision, v.Reason, c.want)
		}
	}
}

func TestHighRiskNeverSilent(t *testing.T) {
	r := BuildRegistry()
	p := DefaultPolicy()
	p.AllowReboot = true
	p.AutonomousEnabled = true
	p.ToolOverrides = map[string]Decision{"reboot_server": Allow}
	if err := p.Validate(r); err == nil {
		t.Fatal("policy that allows a high-risk tool must not validate")
	}
	p.ToolOverrides = nil
	p.AutonomousTools = []string{"reboot_server"}
	if err := p.Validate(r); err == nil {
		t.Fatal("high-risk tool in autonomous allowlist must not validate")
	}
	// Even if a bad policy slipped through storage, Decide still requires approval.
	v := Decide(ModeAutonomous, tool(t, r, "reboot_server"), map[string]any{}, &store.Server{}, p, time.Now())
	if v.Decision != Approve {
		t.Fatalf("reboot decision %s, want approve", v.Decision)
	}
}

func TestAutonomousAllowlistAndWindows(t *testing.T) {
	r := BuildRegistry()
	p := DefaultPolicy()
	p.AutonomousEnabled = true
	p.Medium = Allow
	p.AutonomousTools = []string{"restart_container"}
	dev := &store.Server{Environment: "development"}
	rc := tool(t, r, "restart_container")
	if d := Decide(ModeAutonomous, rc, map[string]any{"container": "web"}, dev, p, time.Now()).Decision; d != Allow {
		t.Fatalf("allowlisted tool: %s", d)
	}
	if d := Decide(ModeAutonomous, tool(t, r, "restart_service"), map[string]any{"unit": "x"}, dev, p, time.Now()).Decision; d != Approve {
		t.Fatalf("non-allowlisted tool: %s", d)
	}
	p.Protected = []string{"web"}
	if d := Decide(ModeAutonomous, rc, map[string]any{"container": "web"}, dev, p, time.Now()).Decision; d != Approve {
		t.Fatalf("protected target: %s", d)
	}
	p.Protected = nil
	// Sunday 03:00 inside, Monday 12:00 outside a Sunday 02:00–04:00 window.
	p.Windows = []Window{{Days: []string{"sun"}, Start: "02:00", End: "04:00"}}
	in := time.Date(2026, 11, 15, 3, 0, 0, 0, time.Local)
	out := time.Date(2026, 11, 16, 12, 0, 0, 0, time.Local)
	if d := Decide(ModeAutonomous, rc, map[string]any{"container": "web"}, dev, p, in).Decision; d != Allow {
		t.Fatalf("inside window: %s", d)
	}
	if d := Decide(ModeAutonomous, rc, map[string]any{"container": "web"}, dev, p, out).Decision; d != Approve {
		t.Fatalf("outside window: %s", d)
	}
}

func TestToolArgumentValidation(t *testing.T) {
	r := BuildRegistry()
	rs := tool(t, r, "restart_service")
	if _, err := rs.Validate(map[string]any{"unit": "nginx; rm -rf /"}); err == nil {
		t.Fatal("shell metacharacters must be rejected")
	}
	if _, err := rs.Validate(map[string]any{"unit": "nginx", "extra": 1}); err == nil {
		t.Fatal("unknown arguments must be rejected")
	}
	if _, err := rs.Validate(map[string]any{}); err == nil {
		t.Fatal("missing required argument must be rejected")
	}
	lp := tool(t, r, "list_processes")
	a, err := lp.Validate(map[string]any{"limit": 5.0})
	if err != nil || a["limit"] != 5 || a["sort"] != "cpu" {
		t.Fatalf("defaults/coercion failed: %v %v", a, err)
	}
	if _, err := lp.Validate(map[string]any{"limit": 1000.0}); err == nil {
		t.Fatal("out-of-range value accepted")
	}
}

func TestModelResolution(t *testing.T) {
	models := []ModelInfo{
		{ParamsB: 7.6, Caps: capsTools(true)},
		{ParamsB: 1.5, Caps: capsTools(false)},
	}
	models[0].Name, models[0].Size = "qwen2.5:7b", 4<<30
	models[1].Name, models[1].Size = "tiny:1.5b", 1<<30
	res, err := Resolve("general", map[string]string{}, models, 16<<30)
	if err != nil || res.Model != "qwen2.5:7b" || !res.Automatic {
		t.Fatalf("auto: %+v %v", res, err)
	}
	res, err = Resolve("logs", map[string]string{"logs": "missing:13b"}, models, 16<<30)
	if err != nil || res.Notice == "" {
		t.Fatalf("missing preferred model must be explained: %+v %v", res, err)
	}
	if _, err := Resolve("general", nil, nil, 0); err == nil {
		t.Fatal("no models must be an error")
	}
}

func capsTools(t bool) ollamaCaps { return ollamaCaps{Tools: t, Completion: true} }
