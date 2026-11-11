package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/WSEU790/MoistTux/internal/core/agentclient"
	"github.com/WSEU790/MoistTux/internal/core/store"
)

// Env is what a tool may use. Tools never receive credentials or secrets.
type Env struct {
	Server *store.Server
	Client *agentclient.Client
	Deps   *Deps
}

// Impact is the preview shown to an operator before approving a change.
type Impact struct {
	Summary  string   `json:"summary"`
	Effects  []string `json:"effects"`
	Risks    []string `json:"risks"`
	Rollback string   `json:"rollback"`
	Evidence any      `json:"evidence,omitempty"`
	Confirm  string   `json:"confirm,omitempty"` // text the operator must type (high risk)
}

// Param is one JSON-schema property with validation rules.
type Param struct {
	Name        string
	Type        string // string | integer | number | boolean
	Description string
	Required    bool
	Enum        []string
	Pattern     *regexp.Regexp
	Min, Max    *float64
	MaxLen      int
	Default     any
}

type Tool struct {
	Name        string
	Description string
	Params      []Param
	Risk        Risk
	Timeout     time.Duration
	Fleet       bool   // operates on NEXUS data, not on a specific server
	Internal    bool   // changes only NEXUS state (e.g. memory), audited but not gated
	Capability  string // required agent capability, if any
	Exec        func(ctx context.Context, env *Env, args map[string]any) (any, error)
	Preview     func(ctx context.Context, env *Env, args map[string]any) (*Impact, error)
	Describe    func(args map[string]any) string
}

func fptr(v float64) *float64 { return &v }

// Schema returns the JSON schema exposed to the model. A "server" property is
// added to every server-scoped tool.
func (t *Tool) Schema() map[string]any {
	props := map[string]any{}
	req := []string{}
	if !t.Fleet {
		props["server"] = map[string]any{"type": "string", "description": "Server name or ID. Optional when the conversation is focused on one server."}
	}
	for _, p := range t.Params {
		s := map[string]any{"type": p.Type, "description": p.Description}
		if len(p.Enum) > 0 {
			s["enum"] = p.Enum
		}
		if p.Min != nil {
			s["minimum"] = *p.Min
		}
		if p.Max != nil {
			s["maximum"] = *p.Max
		}
		props[p.Name] = s
		if p.Required {
			req = append(req, p.Name)
		}
	}
	return map[string]any{"type": "object", "properties": props, "required": req}
}

// Validate checks and normalises arguments against the declared schema.
// Unknown arguments are rejected so a model cannot smuggle extra fields.
func (t *Tool) Validate(raw map[string]any) (map[string]any, error) {
	out := map[string]any{}
	known := map[string]*Param{}
	for i := range t.Params {
		known[t.Params[i].Name] = &t.Params[i]
	}
	for k, v := range raw {
		if k == "server" && !t.Fleet {
			s, ok := v.(string)
			if !ok {
				return nil, fmt.Errorf("server must be a string")
			}
			out["server"] = s
			continue
		}
		p, ok := known[k]
		if !ok {
			return nil, fmt.Errorf("unknown argument %q for %s", k, t.Name)
		}
		nv, err := coerce(p, v)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", k, err)
		}
		out[k] = nv
	}
	for _, p := range t.Params {
		if _, ok := out[p.Name]; !ok {
			if p.Required {
				return nil, fmt.Errorf("missing required argument %q", p.Name)
			}
			if p.Default != nil {
				out[p.Name] = p.Default
			}
		}
	}
	return out, nil
}

func coerce(p *Param, v any) (any, error) {
	switch p.Type {
	case "string":
		s, ok := v.(string)
		if !ok {
			if f, isNum := v.(float64); isNum && p.Pattern == nil && len(p.Enum) == 0 {
				s = fmt.Sprint(f)
			} else {
				return nil, fmt.Errorf("must be a string")
			}
		}
		s = strings.TrimSpace(s)
		if p.MaxLen > 0 && len(s) > p.MaxLen {
			return nil, fmt.Errorf("longer than %d characters", p.MaxLen)
		}
		if len(p.Enum) > 0 {
			found := false
			for _, e := range p.Enum {
				if e == s {
					found = true
				}
			}
			if !found {
				return nil, fmt.Errorf("must be one of %s", strings.Join(p.Enum, ", "))
			}
		}
		if p.Pattern != nil && !p.Pattern.MatchString(s) {
			return nil, fmt.Errorf("value %q is not allowed", s)
		}
		return s, nil
	case "integer", "number":
		var f float64
		switch x := v.(type) {
		case float64:
			f = x
		case int:
			f = float64(x)
		case string:
			if _, err := fmt.Sscan(x, &f); err != nil {
				return nil, fmt.Errorf("must be a number")
			}
		default:
			return nil, fmt.Errorf("must be a number")
		}
		if p.Type == "integer" && f != math.Trunc(f) {
			return nil, fmt.Errorf("must be an integer")
		}
		if p.Min != nil && f < *p.Min {
			return nil, fmt.Errorf("must be ≥ %v", *p.Min)
		}
		if p.Max != nil && f > *p.Max {
			return nil, fmt.Errorf("must be ≤ %v", *p.Max)
		}
		if p.Type == "integer" {
			return int(f), nil
		}
		return f, nil
	case "boolean":
		switch x := v.(type) {
		case bool:
			return x, nil
		case string:
			return x == "true", nil
		}
		return nil, fmt.Errorf("must be a boolean")
	}
	return nil, fmt.Errorf("unsupported parameter type %s", p.Type)
}

type Registry struct {
	tools map[string]*Tool
	order []string
}

func NewRegistry() *Registry { return &Registry{tools: map[string]*Tool{}} }

func (r *Registry) Add(t *Tool) {
	if _, dup := r.tools[t.Name]; dup {
		panic("duplicate tool " + t.Name)
	}
	if t.Timeout == 0 {
		t.Timeout = 30 * time.Second
	}
	r.tools[t.Name] = t
	r.order = append(r.order, t.Name)
}

func (r *Registry) Get(name string) (*Tool, bool) { t, ok := r.tools[name]; return t, ok }

func (r *Registry) All() []*Tool {
	out := make([]*Tool, 0, len(r.order))
	for _, n := range r.order {
		out = append(out, r.tools[n])
	}
	return out
}

// ToolInfo is the public description used by the UI's policy editor.
type ToolInfo struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Risk        Risk           `json:"risk"`
	TimeoutSec  int            `json:"timeout_sec"`
	Fleet       bool           `json:"fleet"`
	Internal    bool           `json:"internal"`
	Capability  string         `json:"capability,omitempty"`
	Schema      map[string]any `json:"schema"`
}

func (r *Registry) Info() []ToolInfo {
	var out []ToolInfo
	for _, t := range r.All() {
		out = append(out, ToolInfo{t.Name, t.Description, t.Risk, int(t.Timeout.Seconds()), t.Fleet, t.Internal, t.Capability, t.Schema()})
	}
	sort.SliceStable(out, func(i, j int) bool { return riskOrder(out[i].Risk) < riskOrder(out[j].Risk) })
	return out
}

func riskOrder(r Risk) int {
	switch r {
	case RiskRead:
		return 0
	case RiskLow:
		return 1
	case RiskMedium:
		return 2
	}
	return 3
}

func toJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		return json.RawMessage(`{"error":"unserialisable result"}`)
	}
	return b
}
