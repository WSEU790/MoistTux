package ai

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/WSEU790/MoistTux/internal/core/knowledge"
	"github.com/WSEU790/MoistTux/internal/protocol"

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
		{ModeAdministrator, "reboot_server", map[string]any{"reason": "x"}, Deny},     // reboots not allowed by default
		{ModeAutonomous, "restart_service", map[string]any{"unit": "nginx"}, Approve}, // autonomy disabled
		{ModeAssistant, "remember_fact", nil, Allow},                                  // internal
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

func TestCommandToolsPolicy(t *testing.T) {
	r := BuildRegistry()
	now := time.Now()
	srv := &store.Server{Environment: "lab"}
	run := tool(t, r, "run_admin_command")
	shell := tool(t, r, "run_shell_command")
	cmd := func(id string, a map[string]any) map[string]any {
		return map[string]any{"command": id, "args": a}
	}

	p := DefaultPolicy()
	p.Low, p.Medium = Allow, Allow
	cases := []struct {
		mode Mode
		t    *Tool
		args map[string]any
		want Decision
	}{
		// Read-only catalog commands run in every mode, even read-only ones.
		{ModeAnalyst, run, cmd("df", nil), Allow},
		{ModeAssistant, run, cmd("journal_unit", map[string]any{"unit": "nginx.service"}), Allow},
		// Changes are denied in read-only modes and follow the policy otherwise.
		{ModeAnalyst, run, cmd("apt_install", map[string]any{"packages": []any{"htop"}}), Deny},
		{ModeAdministrator, run, cmd("apt_install", map[string]any{"packages": []any{"htop"}}), Allow},
		// High-risk catalog commands always need approval, whatever the policy says.
		{ModeAdministrator, run, cmd("ufw_enable", nil), Approve},
		{ModeAdministrator, run, cmd("apt_purge", map[string]any{"packages": []any{"apache2"}}), Approve},
		// Shell commands: never automatic, not even in autonomous mode.
		{ModeAdministrator, shell, map[string]any{"command": "apt-get update"}, Approve},
		{ModeAnalyst, shell, map[string]any{"command": "apt-get update"}, Deny},
	}
	for i, c := range cases {
		if got := Decide(c.mode, c.t, c.args, srv, p, now).Decision; got != c.want {
			t.Errorf("case %d (%s %v): got %s, want %s", i, c.t.Name, c.args, got, c.want)
		}
	}

	auto := DefaultPolicy()
	auto.AutonomousEnabled, auto.AutonomousTools = true, []string{"run_admin_command", "run_shell_command"}
	if err := auto.Validate(r); err == nil {
		t.Error("run_shell_command was accepted in the autonomous allowlist")
	}
	auto.AutonomousTools = []string{"run_admin_command"}
	auto.Medium = Allow
	if got := Decide(ModeAutonomous, shell, map[string]any{"command": "uptime"}, srv, auto, now).Decision; got != Approve {
		t.Errorf("autonomous shell command: %s", got)
	}

	// The per-server switch turns shell commands off.
	off := DefaultPolicy()
	off.ShellDisabled = true
	if got := Decide(ModeAdministrator, shell, map[string]any{"command": "uptime"}, srv, off, now).Decision; got != Deny {
		t.Errorf("shell disabled by policy: %s", got)
	}

	// Protected units are caught inside catalog arguments too.
	prot := DefaultPolicy()
	prot.Low, prot.Medium = Allow, Allow
	prot.Protected = []string{"postgresql"}
	if got := Decide(ModeAdministrator, run, cmd("systemctl_disable", map[string]any{"unit": "postgresql.service"}), srv, prot, now).Decision; got != Approve {
		t.Errorf("protected unit via catalog: %s", got)
	}

	// Validation: blocked shell commands and bad catalog arguments never reach approval.
	if _, err := shell.Validate(map[string]any{"command": "rm -rf /", "reason": "x"}); err == nil {
		t.Error("rm -rf / passed validation")
	}
	if _, err := run.Validate(map[string]any{"command": "apt_install", "args": map[string]any{"packages": []any{"-o", "x"}}}); err == nil {
		t.Error("option injection passed validation")
	}
	if a, err := run.Validate(map[string]any{"command": "apt_install", "args": `{"packages": "nginx certbot"}`}); err != nil {
		t.Errorf("JSON-text args with a space-separated list: %v", err)
	} else if got := catalogArgs(a)["packages"]; len(got) != 2 {
		t.Errorf("packages parsed as %q", got)
	}
}

// Every tool and catalog command a runbook names must exist, so the
// knowledge library cannot drift from what NEXUS can actually do.
func TestRunbooksReferenceRealTools(t *testing.T) {
	lib, err := knowledge.Load()
	if err != nil {
		t.Fatal(err)
	}
	r := BuildRegistry()
	re := regexp.MustCompile("`([a-z][a-z0-9_]+)`")
	for _, d := range lib.Docs() {
		full, _ := lib.Get(d.ID)
		for _, s := range full.Sections {
			for _, line := range strings.Split(s.Body, "\n") {
				if !strings.HasPrefix(line, "In NEXUS:") {
					continue
				}
				for _, m := range re.FindAllStringSubmatch(line, -1) {
					name := m[1]
					if _, ok := r.Get(name); ok {
						continue
					}
					if _, ok := protocol.FindCommand(name); ok {
						continue
					}
					t.Errorf("runbook %s mentions %q, which is neither a tool nor a catalog command", d.ID, name)
				}
			}
		}
	}
}
