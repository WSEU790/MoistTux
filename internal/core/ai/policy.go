package ai

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/WSEU790/MoistTux/internal/core/store"
)

// Risk classes, per the NEXUS execution safety model.
type Risk string

const (
	RiskRead   Risk = "read"
	RiskLow    Risk = "low"
	RiskMedium Risk = "medium"
	RiskHigh   Risk = "high"
)

// Mode is the operational mode of a conversation.
type Mode string

const (
	ModeAssistant     Mode = "assistant"     // answer and explain; read-only tools
	ModeAnalyst       Mode = "analyst"       // investigate and diagnose; read-only tools
	ModeAdministrator Mode = "administrator" // may request changes; approvals per policy
	ModeAutonomous    Mode = "autonomous"    // may act within an explicit per-server allowlist
)

func ParseMode(s string) (Mode, error) {
	switch Mode(s) {
	case ModeAssistant, ModeAnalyst, ModeAdministrator, ModeAutonomous:
		return Mode(s), nil
	}
	return "", fmt.Errorf("unknown mode %q", s)
}

type Decision string

const (
	Allow   Decision = "allow"
	Approve Decision = "approve"
	Deny    Decision = "deny"
)

// Window is a recurring maintenance window in the operator's local time.
type Window struct {
	Days  []string `json:"days"`  // mon..sun
	Start string   `json:"start"` // HH:MM
	End   string   `json:"end"`   // HH:MM (may wrap past midnight)
}

// Policy is stored per server (scope = server ID) with a global default ("*").
type Policy struct {
	Low    Decision `json:"low"`    // allow | approve | deny
	Medium Decision `json:"medium"` // allow | approve | deny
	High   Decision `json:"high"`   // approve | deny (never allow)

	ToolOverrides map[string]Decision `json:"tool_overrides,omitempty"`

	// Autonomous mode is off unless explicitly enabled for a server and then
	// limited to the tools listed here, optionally within maintenance windows.
	AutonomousEnabled bool     `json:"autonomous_enabled"`
	AutonomousTools   []string `json:"autonomous_tools"`
	Windows           []Window `json:"maintenance_windows"`

	// Units and containers the AI must never touch without explicit approval.
	Protected []string `json:"protected"`

	// Monitoring behaviour: read_only | recommend | auto_maintenance | auto_remediate
	MonitoringLevel string `json:"monitoring_level"`
	AutoInvestigate bool   `json:"auto_investigate"`
	AllowReboot     bool   `json:"allow_reboot"` // even then, reboots always need approval
}

func DefaultPolicy() Policy {
	return Policy{Low: Approve, Medium: Approve, High: Approve, MonitoringLevel: "recommend", AutonomousTools: []string{}, Windows: []Window{}, Protected: []string{}}
}

func (p *Policy) Validate(tools *Registry) error {
	for _, d := range []Decision{p.Low, p.Medium} {
		if d != Allow && d != Approve && d != Deny {
			return fmt.Errorf("low/medium decisions must be allow, approve or deny")
		}
	}
	if p.High != Approve && p.High != Deny {
		return fmt.Errorf("high-risk operations can only be approve or deny; they are never allowed silently")
	}
	for name, d := range p.ToolOverrides {
		t, ok := tools.Get(name)
		if !ok {
			return fmt.Errorf("unknown tool %q in overrides", name)
		}
		if t.Risk == RiskHigh && d == Allow {
			return fmt.Errorf("%s is high risk and cannot be set to allow", name)
		}
		if d != Allow && d != Approve && d != Deny {
			return fmt.Errorf("invalid decision %q for %s", d, name)
		}
	}
	for _, name := range p.AutonomousTools {
		t, ok := tools.Get(name)
		if !ok {
			return fmt.Errorf("unknown tool %q in autonomous allowlist", name)
		}
		if t.Risk == RiskHigh {
			return fmt.Errorf("%s is high risk and cannot run autonomously", name)
		}
	}
	for _, w := range p.Windows {
		if _, err := time.Parse("15:04", w.Start); err != nil {
			return fmt.Errorf("window start %q must be HH:MM", w.Start)
		}
		if _, err := time.Parse("15:04", w.End); err != nil {
			return fmt.Errorf("window end %q must be HH:MM", w.End)
		}
		for _, d := range w.Days {
			if _, ok := weekdays[strings.ToLower(d)]; !ok {
				return fmt.Errorf("invalid day %q", d)
			}
		}
	}
	switch p.MonitoringLevel {
	case "read_only", "recommend", "auto_maintenance", "auto_remediate":
	default:
		return fmt.Errorf("monitoring_level must be read_only, recommend, auto_maintenance or auto_remediate")
	}
	return nil
}

var weekdays = map[string]time.Weekday{"sun": time.Sunday, "mon": time.Monday, "tue": time.Tuesday, "wed": time.Wednesday, "thu": time.Thursday, "fri": time.Friday, "sat": time.Saturday}

// InWindow reports whether t falls in any window (true when none are defined).
func (p *Policy) InWindow(t time.Time) bool {
	if len(p.Windows) == 0 {
		return true
	}
	for _, w := range p.Windows {
		s, _ := time.Parse("15:04", w.Start)
		e, _ := time.Parse("15:04", w.End)
		mins := t.Hour()*60 + t.Minute()
		sm, em := s.Hour()*60+s.Minute(), e.Hour()*60+e.Minute()
		dayOK := func(d time.Weekday) bool {
			if len(w.Days) == 0 {
				return true
			}
			for _, x := range w.Days {
				if weekdays[strings.ToLower(x)] == d {
					return true
				}
			}
			return false
		}
		if sm <= em {
			if dayOK(t.Weekday()) && mins >= sm && mins < em {
				return true
			}
		} else { // wraps midnight
			if dayOK(t.Weekday()) && mins >= sm || dayOK((t.Weekday()+6)%7) && mins < em {
				return true
			}
		}
	}
	return false
}

// PolicyStore loads effective policies (server scope overrides the default).
type PolicyStore struct{ st *store.Store }

func (ps *PolicyStore) Effective(ctx context.Context, serverID string) (Policy, string) {
	p := DefaultPolicy()
	scope := "default"
	if ok, _ := ps.st.GetPolicy(ctx, "*", &p); ok {
		scope = "global"
	}
	if serverID != "" {
		sp := DefaultPolicy()
		if ok, _ := ps.st.GetPolicy(ctx, serverID, &sp); ok {
			return sp, "server"
		}
	}
	return p, scope
}

// Verdict explains a policy decision; it is stored with every execution.
type Verdict struct {
	Decision Decision `json:"decision"`
	Reason   string   `json:"reason"`
}

// Decide is the single choke point between the model and every tool.
func Decide(mode Mode, t *Tool, args map[string]any, srv *store.Server, p Policy, now time.Time) Verdict {
	if t.Risk == RiskRead {
		return Verdict{Allow, "read-only operation"}
	}
	if t.Internal {
		return Verdict{Allow, "internal NEXUS operation (no server change)"}
	}
	if mode == ModeAssistant || mode == ModeAnalyst {
		return Verdict{Deny, fmt.Sprintf("%s mode is read-only; switch the conversation to Administrator mode to request changes", strings.Title(string(mode)))}
	}
	if d, ok := p.ToolOverrides[t.Name]; ok && d == Deny {
		return Verdict{Deny, "denied by policy override for " + t.Name}
	}
	// Protected targets always need a human.
	for _, key := range []string{"unit", "container"} {
		if v, ok := args[key].(string); ok {
			for _, prot := range p.Protected {
				if strings.EqualFold(prot, v) || strings.EqualFold(prot+".service", v) {
					return Verdict{Approve, v + " is marked protected in this server's policy"}
				}
			}
		}
	}
	if t.Risk == RiskHigh {
		if p.High == Deny {
			return Verdict{Deny, "high-risk operations are denied by policy"}
		}
		if t.Name == "reboot_server" && !p.AllowReboot {
			return Verdict{Deny, "reboots are not permitted by this server's policy (enable 'allow reboot' to request one)"}
		}
		return Verdict{Approve, "high-risk operation: explicit approval with impact preview is always required"}
	}
	base := p.Medium
	if t.Risk == RiskLow {
		base = p.Low
	}
	if d, ok := p.ToolOverrides[t.Name]; ok {
		base = d
	}
	if base == Deny {
		return Verdict{Deny, fmt.Sprintf("%s-risk operations are denied by policy", t.Risk)}
	}
	if mode == ModeAutonomous {
		if !p.AutonomousEnabled {
			return Verdict{Approve, "autonomous mode is not enabled for this server; approval required"}
		}
		listed := false
		for _, n := range p.AutonomousTools {
			if n == t.Name {
				listed = true
			}
		}
		if !listed {
			return Verdict{Approve, t.Name + " is not in this server's autonomous allowlist"}
		}
		if !p.InWindow(now) {
			return Verdict{Approve, "outside the configured maintenance windows"}
		}
		if srv != nil && srv.Environment == "production" && t.Risk == RiskMedium && base != Allow {
			return Verdict{Approve, "medium-risk change on a production server; policy requires approval"}
		}
		return Verdict{Allow, "permitted by the autonomous policy for this server"}
	}
	if base == Allow {
		return Verdict{Allow, fmt.Sprintf("%s-risk operations are pre-authorised by policy", t.Risk)}
	}
	return Verdict{Approve, fmt.Sprintf("%s-risk operation requires approval", t.Risk)}
}

// NewPolicyStore returns a policy store backed by st.
func NewPolicyStore(st *store.Store) PolicyStore { return PolicyStore{st: st} }
