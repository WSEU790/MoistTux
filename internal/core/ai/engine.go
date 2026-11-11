package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/WSEU790/MoistTux/internal/core/events"
	"github.com/WSEU790/MoistTux/internal/core/fleet"
	"github.com/WSEU790/MoistTux/internal/core/ollama"
	"github.com/WSEU790/MoistTux/internal/core/store"
)

// Deps are the core services available to tools and the engine.
type Deps struct {
	Store    *store.Store
	Fleet    *fleet.Manager
	Bus      *events.Bus
	Log      *log.Logger
	Registry *Registry
	Policies *PolicyStore
}

// ResolveServer accepts an ID, a display name or a hostname.
func (d *Deps) ResolveServer(ctx context.Context, ref string) (*store.Server, error) {
	ref = strings.TrimSpace(ref)
	if s, err := d.Store.GetServer(ctx, ref); err == nil {
		return s, nil
	}
	servers, err := d.Store.ListServers(ctx)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, s := range servers {
		names = append(names, s.Name)
		if strings.EqualFold(s.Name, ref) || strings.EqualFold(s.Address, ref) || (s.Info != nil && strings.EqualFold(s.Info.Hostname, ref)) {
			return s, nil
		}
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("no servers are enrolled in NEXUS")
	}
	return nil, fmt.Errorf("unknown server %q; managed servers: %s", ref, strings.Join(names, ", "))
}

// Settings are persisted under "ai.settings".
type Settings struct {
	OllamaURL     string            `json:"ollama_url"`
	Temperature   float64           `json:"temperature"`
	NumCtx        int               `json:"num_ctx"`
	MaxToolRounds int               `json:"max_tool_rounds"`
	KeepAlive     string            `json:"keep_alive"`
	Roles         map[string]string `json:"roles"`
	ApprovalTTL   int               `json:"approval_ttl_minutes"`
	InvestPerHour int               `json:"investigations_per_hour"`
}

func DefaultSettings() Settings {
	return Settings{OllamaURL: ollama.DefaultURL, Temperature: 0.2, NumCtx: 16384, MaxToolRounds: 12, KeepAlive: "15m", Roles: map[string]string{}, ApprovalTTL: 60, InvestPerHour: 6}
}

func (s *Settings) Validate() error {
	if _, err := ollama.New(s.OllamaURL); err != nil {
		return err
	}
	if s.Temperature < 0 || s.Temperature > 2 {
		return fmt.Errorf("temperature must be between 0 and 2")
	}
	if s.NumCtx < 2048 || s.NumCtx > 262144 {
		return fmt.Errorf("context window must be between 2048 and 262144 tokens")
	}
	if s.MaxToolRounds < 1 || s.MaxToolRounds > 40 {
		return fmt.Errorf("max tool rounds must be between 1 and 40")
	}
	if s.ApprovalTTL < 5 || s.ApprovalTTL > 24*60 {
		return fmt.Errorf("approval expiry must be between 5 minutes and 24 hours")
	}
	if s.InvestPerHour < 0 || s.InvestPerHour > 60 {
		return fmt.Errorf("investigations per hour must be between 0 and 60")
	}
	valid := map[string]bool{}
	for _, r := range Roles {
		valid[r.ID] = true
	}
	for r := range s.Roles {
		if !valid[r] {
			return fmt.Errorf("unknown role %q", r)
		}
	}
	return nil
}

type Engine struct {
	deps *Deps

	mu       sync.RWMutex
	settings Settings
	client   *ollama.Client
	caps     map[string]capsEntry
	active   map[string]context.CancelFunc
	invMu    sync.Mutex
	invTimes []time.Time
	hw       *Hardware
	hwAt     time.Time
}

type capsEntry struct {
	caps ollama.Capabilities
	at   time.Time
}

func NewEngine(deps *Deps) *Engine {
	e := &Engine{deps: deps, caps: map[string]capsEntry{}, active: map[string]context.CancelFunc{}}
	s := DefaultSettings()
	_, _ = deps.Store.GetSetting(context.Background(), "ai.settings", &s)
	if s.Roles == nil {
		s.Roles = map[string]string{}
	}
	e.apply(s)
	return e
}

func (e *Engine) apply(s Settings) {
	c, err := ollama.New(s.OllamaURL)
	if err != nil {
		c, _ = ollama.New(ollama.DefaultURL)
	}
	e.mu.Lock()
	e.settings, e.client = s, c
	e.caps = map[string]capsEntry{}
	e.mu.Unlock()
}

func (e *Engine) Settings() Settings { e.mu.RLock(); defer e.mu.RUnlock(); return e.settings }
func (e *Engine) Client() *ollama.Client {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.client
}

func (e *Engine) UpdateSettings(ctx context.Context, s Settings, by string) error {
	if err := s.Validate(); err != nil {
		return err
	}
	old := e.Settings()
	if err := e.deps.Store.PutSetting(ctx, "ai.settings", s); err != nil {
		return err
	}
	e.apply(s)
	_ = e.deps.Store.Audit(ctx, by, "ai.settings.update", "ai", map[string]any{"before": old, "after": s})
	return nil
}

// ---------------------------------------------------------------- status & models

type Status struct {
	Reachable bool         `json:"reachable"`
	URL       string       `json:"url"`
	Local     bool         `json:"local"`
	Version   string       `json:"version,omitempty"`
	Error     string       `json:"error,omitempty"`
	Models    int          `json:"models"`
	Running   []ollama.Running `json:"running"`
	Roles     []Resolution `json:"roles"`
	RoleError string       `json:"role_error,omitempty"`
}

func (e *Engine) Status(ctx context.Context) Status {
	c := e.Client()
	st := Status{URL: c.BaseURL(), Local: c.IsLocal(), Running: []ollama.Running{}}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	v, err := c.Version(ctx)
	if err != nil {
		st.Error = err.Error()
		return st
	}
	st.Reachable, st.Version = true, v
	models, err := e.Models(ctx)
	if err != nil {
		st.Error = err.Error()
		return st
	}
	st.Models = len(models)
	if ps, err := c.PS(ctx); err == nil {
		st.Running = ps
	}
	hw := e.Hardware(ctx)
	for _, r := range Roles {
		res, err := Resolve(r.ID, e.Settings().Roles, models, hw.Budget())
		if err != nil {
			st.RoleError = err.Error()
			break
		}
		st.Roles = append(st.Roles, res)
	}
	return st
}

func (e *Engine) capabilities(ctx context.Context, model string) (ollama.Capabilities, error) {
	e.mu.RLock()
	ce, ok := e.caps[model]
	e.mu.RUnlock()
	if ok && time.Since(ce.at) < 10*time.Minute {
		return ce.caps, nil
	}
	show, err := e.Client().Show(ctx, model)
	if err != nil {
		return ollama.Capabilities{}, err
	}
	c := show.Caps()
	e.mu.Lock()
	e.caps[model] = capsEntry{c, time.Now()}
	e.mu.Unlock()
	return c, nil
}

// Models lists installed models with detected capabilities.
func (e *Engine) Models(ctx context.Context) ([]ModelInfo, error) {
	list, err := e.Client().List(ctx)
	if err != nil {
		return nil, err
	}
	hw := e.Hardware(ctx)
	budget := hw.Budget()
	out := make([]ModelInfo, 0, len(list))
	for _, m := range list {
		mi := ModelInfo{Model: m, ParamsB: ParseParams(m.Details.ParameterSize)}
		c, err := e.capabilities(ctx, m.Name)
		if err != nil {
			mi.CapsError = err.Error()
		}
		mi.Caps = c
		if budget > 0 {
			fits := uint64(float64(m.Size)*1.2) <= budget
			mi.FitsMemory = &fits
		}
		out = append(out, mi)
	}
	return out, nil
}

func (e *Engine) Hardware(ctx context.Context) Hardware {
	e.mu.RLock()
	if e.hw != nil && time.Since(e.hwAt) < 5*time.Minute {
		h := *e.hw
		e.mu.RUnlock()
		return h
	}
	e.mu.RUnlock()
	h := DetectHardware(ctx, e.Client().IsLocal())
	e.mu.Lock()
	e.hw, e.hwAt = &h, time.Now()
	e.mu.Unlock()
	return h
}

// Pull downloads a model through Ollama, streaming progress over the bus.
// It only runs when the operator explicitly asks for a specific model.
func (e *Engine) Pull(ctx context.Context, model, by string) error {
	if !regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._/\-]{0,120}(:[a-zA-Z0-9._\-]{1,64})?$`).MatchString(model) {
		return fmt.Errorf("invalid model name %q", model)
	}
	_ = e.deps.Store.Audit(ctx, by, "ai.model.pull", model, nil)
	go func() {
		pctx, cancel := context.WithTimeout(context.Background(), 6*time.Hour)
		defer cancel()
		err := e.Client().Pull(pctx, model, func(p ollama.PullProgress) {
			e.deps.Bus.Publish("model.pull", map[string]any{"model": model, "status": p.Status, "total": p.Total, "completed": p.Completed, "digest": p.Digest})
		})
		ev := map[string]any{"model": model, "done": true}
		if err != nil {
			ev["error"] = err.Error()
		}
		e.deps.Bus.Publish("model.pull", ev)
	}()
	return nil
}

func (e *Engine) DeleteModel(ctx context.Context, model, by string) error {
	if err := e.Client().Delete(ctx, model); err != nil {
		return err
	}
	return e.deps.Store.Audit(ctx, by, "ai.model.delete", model, nil)
}

// ---------------------------------------------------------------- conversations

var ErrBusy = errors.New("this conversation is already working; stop it or wait for it to finish")

func (e *Engine) busy(id string) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	_, ok := e.active[id]
	return ok
}

func (e *Engine) state(convID, state string, extra map[string]any) {
	d := map[string]any{"conversation_id": convID, "state": state}
	for k, v := range extra {
		d[k] = v
	}
	e.deps.Bus.Publish("chat.state", d)
}

// Send records an operator message and starts a turn in the background.
func (e *Engine) Send(ctx context.Context, convID, text string) (*store.Message, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, fmt.Errorf("message is empty")
	}
	if len(text) > 20000 {
		return nil, fmt.Errorf("message is too long")
	}
	conv, err := e.deps.Store.GetConversation(ctx, convID)
	if err != nil {
		return nil, err
	}
	if e.busy(convID) {
		return nil, ErrBusy
	}
	if pending, _ := e.pendingApproval(ctx, convID); pending != nil {
		return nil, fmt.Errorf("an approval is pending in this conversation (%s); approve or deny it first", pending.Summary)
	}
	m := &store.Message{ConversationID: convID, Role: "user", Content: text}
	if err := e.deps.Store.AddMessage(ctx, m); err != nil {
		return nil, err
	}
	e.deps.Bus.Publish("chat.message", m)
	if conv.Title == "" || conv.Title == "New conversation" {
		conv.Title = titleFrom(text)
		_ = e.deps.Store.UpdateConversation(ctx, conv)
		e.deps.Bus.Publish("chat.conversation", conv)
	}
	e.startTurn(conv, classifyRole(text, conv.Origin))
	return m, nil
}

func titleFrom(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 60 {
		s = s[:57] + "…"
	}
	return s
}

func (e *Engine) startTurn(conv *store.Conversation, role string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	e.mu.Lock()
	e.active[conv.ID] = cancel
	e.mu.Unlock()
	go func() {
		defer func() {
			cancel()
			e.mu.Lock()
			delete(e.active, conv.ID)
			e.mu.Unlock()
		}()
		if err := e.runTurn(ctx, conv, role); err != nil {
			msg := err.Error()
			if errors.Is(ctx.Err(), context.Canceled) {
				msg = "stopped by operator"
			}
			ev := &store.Message{ConversationID: conv.ID, Role: "event", Content: msg, Meta: toJSON(map[string]string{"kind": "error"})}
			_ = e.deps.Store.AddMessage(context.Background(), ev)
			e.deps.Bus.Publish("chat.message", ev)
			e.state(conv.ID, "error", map[string]any{"error": msg})
		}
	}()
}

// Stop cancels a running turn.
func (e *Engine) Stop(convID string) bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	if c, ok := e.active[convID]; ok {
		c()
		return true
	}
	return false
}

func (e *Engine) pendingApproval(ctx context.Context, convID string) (*store.Approval, error) {
	aps, err := e.deps.Store.ListApprovals(ctx, "pending", 200)
	if err != nil {
		return nil, err
	}
	for _, a := range aps {
		if a.Execution != nil && a.Execution.ConversationID == convID {
			return a, nil
		}
	}
	return nil, nil
}

var reThink = regexp.MustCompile(`(?s)<think>(.*?)</think>`)

func (e *Engine) runTurn(ctx context.Context, conv *store.Conversation, role string) error {
	s := e.Settings()
	models, err := e.Models(ctx)
	if err != nil {
		return fmt.Errorf("cannot use the local AI: %w", err)
	}
	res, err := Resolve(role, s.Roles, models, e.Hardware(ctx).Budget())
	if err != nil {
		return err
	}
	if res.Notice != "" {
		ev := &store.Message{ConversationID: conv.ID, Role: "event", Content: res.Notice, Meta: toJSON(map[string]string{"kind": "model_notice"})}
		_ = e.deps.Store.AddMessage(ctx, ev)
		e.deps.Bus.Publish("chat.message", ev)
	}
	if conv.Model != res.Model {
		conv.Model = res.Model
		_ = e.deps.Store.UpdateConversation(ctx, conv)
	}
	temp := s.Temperature
	opts := &ollama.Options{Temperature: &temp, NumCtx: s.NumCtx}
	for round := 0; round < s.MaxToolRounds; round++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		e.state(conv.ID, "thinking", map[string]any{"model": res.Model, "role": role, "round": round + 1})
		msgs, err := e.buildMessages(ctx, conv, res.Tools)
		if err != nil {
			return err
		}
		var toolCalls []ollama.ToolCall
		var content string
		meta := map[string]any{"model": res.Model, "role": role}
		if res.Tools {
			req := ollama.ChatRequest{Model: res.Model, Messages: msgs, Tools: e.toolDefs(), Options: opts, KeepAlive: s.KeepAlive}
			out, err := e.Client().Chat(ctx, req, func(c, th string) {
				e.deps.Bus.Publish("chat.delta", map[string]any{"conversation_id": conv.ID, "content": c, "thinking": th})
			})
			if err != nil {
				return fmt.Errorf("model %s: %w", res.Model, err)
			}
			content, toolCalls = out.Message.Content, out.Message.ToolCalls
			if out.Message.Thinking != "" {
				meta["thinking"] = out.Message.Thinking
			}
			meta["tokens"] = map[string]any{"prompt": out.PromptTokens, "output": out.OutputTokens, "tps": out.TokensPerSecond}
		} else {
			content, toolCalls, err = e.jsonRound(ctx, res.Model, msgs, opts, s.KeepAlive)
			if err != nil {
				return err
			}
			meta["protocol"] = "json"
		}
		if m := reThink.FindStringSubmatch(content); m != nil {
			meta["thinking"] = strings.TrimSpace(m[1])
			content = strings.TrimSpace(reThink.ReplaceAllString(content, ""))
		}
		am := &store.Message{ConversationID: conv.ID, Role: "assistant", Content: content, Meta: toJSON(meta)}
		if len(toolCalls) > 0 {
			am.ToolCalls = toJSON(toolCalls)
		}
		if err := e.deps.Store.AddMessage(ctx, am); err != nil {
			return err
		}
		e.deps.Bus.Publish("chat.message", am)
		if len(toolCalls) == 0 {
			e.state(conv.ID, "idle", nil)
			return nil
		}
		for i, tc := range toolCalls {
			waiting := e.invoke(ctx, conv, tc)
			if waiting {
				for _, rest := range toolCalls[i+1:] {
					e.toolMessage(ctx, conv.ID, rest.Function.Name, "", map[string]any{"status": "not_executed", "reason": "an earlier action in this step is awaiting operator approval; call it again afterwards if still needed"})
				}
				e.state(conv.ID, "awaiting_approval", nil)
				return nil
			}
		}
	}
	ev := &store.Message{ConversationID: conv.ID, Role: "event", Content: fmt.Sprintf("Stopped after %d tool rounds (limit set in Settings → AI behaviour). Ask NEXUS to continue if more investigation is needed.", s.MaxToolRounds), Meta: toJSON(map[string]string{"kind": "limit"})}
	_ = e.deps.Store.AddMessage(ctx, ev)
	e.deps.Bus.Publish("chat.message", ev)
	e.state(conv.ID, "idle", nil)
	return nil
}

func (e *Engine) toolDefs() []ollama.Tool {
	var out []ollama.Tool
	for _, t := range e.deps.Registry.All() {
		out = append(out, ollama.Tool{Type: "function", Function: ollama.ToolFunction{Name: t.Name, Description: t.Description + " [risk: " + string(t.Risk) + "]", Parameters: t.Schema()}})
	}
	return out
}

const maxToolResultChars = 12000

// buildMessages maps stored history to Ollama messages within a token budget.
func (e *Engine) buildMessages(ctx context.Context, conv *store.Conversation, nativeTools bool) ([]ollama.Message, error) {
	hist, err := e.deps.Store.ListMessages(ctx, conv.ID)
	if err != nil {
		return nil, err
	}
	sys := systemPrompt + "\n" + e.buildContext(ctx, conv)
	if !nativeTools {
		sys += jsonProtocol + toolCatalogue(e.deps.Registry)
	}
	var msgs []ollama.Message
	for _, m := range hist {
		switch m.Role {
		case "user":
			msgs = append(msgs, ollama.Message{Role: "user", Content: m.Content})
		case "assistant":
			om := ollama.Message{Role: "assistant", Content: m.Content}
			var calls []ollama.ToolCall
			if len(m.ToolCalls) > 0 {
				_ = json.Unmarshal(m.ToolCalls, &calls)
			}
			if nativeTools {
				om.ToolCalls = calls
			} else if len(calls) > 0 {
				om.Content = strings.TrimSpace(om.Content + "\n" + string(toJSON(map[string]any{"action": "tool", "tool": calls[0].Function.Name, "arguments": calls[0].Function.Arguments})))
			}
			msgs = append(msgs, om)
		case "tool":
			c := m.Content
			if len(c) > maxToolResultChars {
				c = c[:maxToolResultChars] + "\n…[truncated; full result stored in execution " + m.ExecutionID + "]"
			}
			if nativeTools {
				msgs = append(msgs, ollama.Message{Role: "tool", ToolName: m.ToolName, Content: c})
			} else {
				msgs = append(msgs, ollama.Message{Role: "user", Content: "TOOL RESULT " + m.ToolName + ":\n" + c})
			}
		}
	}
	// Keep the most recent messages that fit ~60% of the context window,
	// never starting the window on an orphaned tool result.
	budget := e.Settings().NumCtx * 4 * 6 / 10
	used := len(sys)
	start := len(msgs)
	for start > 0 {
		l := len(msgs[start-1].Content) + 200
		if used+l > budget && start < len(msgs) {
			break
		}
		used += l
		start--
	}
	for start < len(msgs) && msgs[start].Role == "tool" {
		start++
	}
	return append([]ollama.Message{{Role: "system", Content: sys}}, msgs[start:]...), nil
}

var jsonSchema = json.RawMessage(`{"type":"object","properties":{"action":{"type":"string","enum":["tool","answer"]},"tool":{"type":"string"},"arguments":{"type":"object"},"answer":{"type":"string"}},"required":["action"]}`)

// jsonRound drives models without native tool calling via constrained JSON.
func (e *Engine) jsonRound(ctx context.Context, model string, msgs []ollama.Message, opts *ollama.Options, keep string) (string, []ollama.ToolCall, error) {
	out, err := e.Client().Chat(ctx, ollama.ChatRequest{Model: model, Messages: msgs, Format: jsonSchema, Options: opts, KeepAlive: keep}, nil)
	if err != nil {
		return "", nil, fmt.Errorf("model %s: %w", model, err)
	}
	var r struct {
		Action    string          `json:"action"`
		Tool      string          `json:"tool"`
		Arguments json.RawMessage `json:"arguments"`
		Answer    string          `json:"answer"`
	}
	if err := json.Unmarshal([]byte(out.Message.Content), &r); err != nil {
		// Not valid JSON despite the constraint: treat as a plain answer.
		return out.Message.Content, nil, nil
	}
	if r.Action == "tool" && r.Tool != "" {
		var tc ollama.ToolCall
		tc.Function.Name = r.Tool
		tc.Function.Arguments = r.Arguments
		if len(tc.Function.Arguments) == 0 {
			tc.Function.Arguments = json.RawMessage("{}")
		}
		return "", []ollama.ToolCall{tc}, nil
	}
	return r.Answer, nil, nil
}

func (e *Engine) toolMessage(ctx context.Context, convID, tool, execID string, result any) {
	m := &store.Message{ConversationID: convID, Role: "tool", ToolName: tool, ExecutionID: execID, Content: string(toJSON(result))}
	_ = e.deps.Store.AddMessage(ctx, m)
	e.deps.Bus.Publish("chat.message", m)
}

// invoke runs one model-requested tool call through validation and policy.
// It returns true when the call is waiting for operator approval.
func (e *Engine) invoke(ctx context.Context, conv *store.Conversation, tc ollama.ToolCall) bool {
	name := tc.Function.Name
	t, ok := e.deps.Registry.Get(name)
	if !ok {
		e.toolMessage(ctx, conv.ID, name, "", map[string]any{"error": "unknown tool " + name})
		return false
	}
	var raw map[string]any
	if len(tc.Function.Arguments) > 0 && string(tc.Function.Arguments) != "null" {
		if err := json.Unmarshal(tc.Function.Arguments, &raw); err != nil {
			// Some models send arguments as a JSON-encoded string.
			var s string
			if json.Unmarshal(tc.Function.Arguments, &s) == nil {
				_ = json.Unmarshal([]byte(s), &raw)
			}
		}
	}
	if raw == nil {
		raw = map[string]any{}
	}
	out, waiting, err := e.Execute(ctx, ExecRequest{Tool: t, Args: raw, Mode: Mode(conv.Mode), ConversationID: conv.ID, DefaultServer: conv.ServerID, RequestedBy: "ai"})
	if err != nil {
		e.toolMessage(ctx, conv.ID, name, out.ExecutionID, map[string]any{"error": err.Error()})
		return false
	}
	if waiting {
		return true
	}
	e.toolMessage(ctx, conv.ID, name, out.ExecutionID, out.Result)
	return false
}

// ExecRequest describes one tool invocation from any origin.
type ExecRequest struct {
	Tool           *Tool
	Args           map[string]any
	Mode           Mode
	ConversationID string
	IncidentID     string
	DefaultServer  string
	RequestedBy    string // ai | operator | automation
	OperatorConfirmed bool
}

type ExecOutcome struct {
	ExecutionID string `json:"execution_id"`
	Status      string `json:"status"`
	Result      any    `json:"result,omitempty"`
	ApprovalID  string `json:"approval_id,omitempty"`
}

// Execute is the single path for running a tool. It validates arguments,
// resolves the target, applies policy, records the execution and audit trail,
// and either runs the tool, blocks it, or files an approval request.
func (e *Engine) Execute(ctx context.Context, r ExecRequest) (ExecOutcome, bool, error) {
	t := r.Tool
	args, err := t.Validate(r.Args)
	if err != nil {
		return ExecOutcome{}, false, fmt.Errorf("invalid arguments: %w", err)
	}
	env := &Env{Deps: e.deps}
	if !t.Fleet {
		ref := str(args, "server")
		if ref == "" {
			ref = r.DefaultServer
		}
		if ref == "" {
			servers, _ := e.deps.Store.ListServers(ctx)
			if len(servers) == 1 {
				ref = servers[0].ID
			} else {
				return ExecOutcome{}, false, fmt.Errorf("specify which server (argument \"server\"); this conversation is not focused on one")
			}
		}
		srv, err := e.deps.ResolveServer(ctx, ref)
		if err != nil {
			return ExecOutcome{}, false, err
		}
		cl, err := e.deps.Fleet.Client(srv.ID)
		if err != nil {
			return ExecOutcome{}, false, fmt.Errorf("server %s has no active connection", srv.Name)
		}
		if t.Capability != "" && srv.Health != nil {
			found := false
			for _, c := range srv.Health.Capabilities {
				if c == t.Capability {
					found = true
				}
			}
			if !found {
				return ExecOutcome{}, false, fmt.Errorf("%s does not support %s (agent capabilities: %s)", srv.Name, t.Capability, strings.Join(srv.Health.Capabilities, ", "))
			}
		}
		env.Server, env.Client = srv, cl
		delete(args, "server")
	}
	serverID := ""
	if env.Server != nil {
		serverID = env.Server.ID
	}
	pol, _ := e.deps.Policies.Effective(ctx, serverID)
	var v Verdict
	if r.RequestedBy == "operator" {
		// The operator acting directly in the UI is the human in the loop.
		// Policy denials still apply; previews are confirmed in the UI.
		v = Decide(ModeAdministrator, t, args, env.Server, pol, time.Now())
		if v.Decision == Approve {
			if !r.OperatorConfirmed && t.Risk != RiskRead {
				return ExecOutcome{}, false, fmt.Errorf("operator confirmation required")
			}
			v = Verdict{Allow, "confirmed by the operator in the NEXUS interface"}
		}
	} else {
		v = Decide(r.Mode, t, args, env.Server, pol, time.Now())
	}
	exe := &store.Execution{
		ConversationID: r.ConversationID, IncidentID: r.IncidentID, ServerID: serverID, Tool: t.Name, Args: toJSON(args),
		Risk: string(t.Risk), RequestedBy: r.RequestedBy, Mode: string(r.Mode), Decision: string(v.Decision), DecisionReason: v.Reason,
	}
	switch v.Decision {
	case Deny:
		exe.Status = "blocked"
	case Approve:
		exe.Status = "pending_approval"
	default:
		exe.Status = "running"
	}
	if err := e.deps.Store.CreateExecution(ctx, exe); err != nil {
		return ExecOutcome{}, false, err
	}
	out := ExecOutcome{ExecutionID: exe.ID, Status: exe.Status}
	if t.Risk != RiskRead {
		_ = e.deps.Store.Audit(ctx, r.RequestedBy, "tool."+string(v.Decision), t.Name, map[string]any{"execution": exe.ID, "server": serverID, "args": args, "reason": v.Reason, "mode": r.Mode})
	}
	e.deps.Bus.Publish("execution", exe)
	switch v.Decision {
	case Deny:
		out.Result = map[string]any{"status": "blocked_by_policy", "reason": v.Reason}
		return out, false, nil
	case Approve:
		ap, err := e.requestApproval(ctx, t, env, args, exe)
		if err != nil {
			_ = e.deps.Store.FinishExecution(ctx, exe.ID, "failed", nil, "could not prepare approval: "+err.Error())
			return out, false, fmt.Errorf("could not prepare the impact preview for approval: %w", err)
		}
		out.ApprovalID = ap.ID
		return out, true, nil
	}
	res, err := e.run(ctx, t, env, args, exe.ID)
	out.Status = "succeeded"
	if err != nil {
		out.Status = "failed"
		out.Result = map[string]any{"error": err.Error()}
		return out, false, nil
	}
	out.Result = res
	return out, false, nil
}

func (e *Engine) run(ctx context.Context, t *Tool, env *Env, args map[string]any, execID string) (any, error) {
	_ = e.deps.Store.StartExecution(ctx, execID)
	tctx, cancel := context.WithTimeout(ctx, t.Timeout)
	defer cancel()
	start := time.Now()
	res, err := t.Exec(tctx, env, args)
	if err != nil {
		if errors.Is(tctx.Err(), context.DeadlineExceeded) {
			err = fmt.Errorf("timed out after %s: %w", t.Timeout, err)
		}
		_ = e.deps.Store.FinishExecution(ctx, execID, "failed", nil, err.Error())
	} else {
		_ = e.deps.Store.FinishExecution(ctx, execID, "succeeded", res, "")
	}
	if t.Risk != RiskRead {
		detail := map[string]any{"execution": execID, "duration_ms": time.Since(start).Milliseconds(), "ok": err == nil}
		if err != nil {
			detail["error"] = err.Error()
		}
		_ = e.deps.Store.Audit(ctx, "nexus", "tool.executed", t.Name, detail)
	}
	if x, gerr := e.deps.Store.GetExecution(ctx, execID); gerr == nil {
		e.deps.Bus.Publish("execution", x)
	}
	return res, err
}

func (e *Engine) requestApproval(ctx context.Context, t *Tool, env *Env, args map[string]any, exe *store.Execution) (*store.Approval, error) {
	var imp *Impact
	if t.Preview != nil {
		pctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		var err error
		imp, err = t.Preview(pctx, env, args)
		cancel()
		if err != nil {
			return nil, err
		}
	} else {
		imp = &Impact{Summary: t.Name}
	}
	if t.Risk == RiskHigh && imp.Confirm == "" {
		imp.Confirm = env.Server.Name
	}
	summary := t.Name
	if t.Describe != nil {
		summary = t.Describe(args)
	}
	if env.Server != nil {
		summary += " on " + env.Server.Name
	}
	ap := &store.Approval{ExecutionID: exe.ID, ServerID: exe.ServerID, Tool: t.Name, Risk: string(t.Risk), Summary: summary, Impact: toJSON(imp), ExpiresAt: time.Now().Add(time.Duration(e.Settings().ApprovalTTL) * time.Minute)}
	if err := e.deps.Store.CreateApproval(ctx, ap); err != nil {
		return nil, err
	}
	if exe.ConversationID != "" {
		m := &store.Message{ConversationID: exe.ConversationID, Role: "event", Content: "Approval required: " + summary, ExecutionID: exe.ID, Meta: toJSON(map[string]any{"kind": "approval", "approval_id": ap.ID, "risk": t.Risk, "reason": exe.DecisionReason})}
		_ = e.deps.Store.AddMessage(ctx, m)
		e.deps.Bus.Publish("chat.message", m)
	}
	e.deps.Bus.Publish("approval", ap)
	_ = e.deps.Store.AddNotification(ctx, &store.Notification{Severity: "warning", Kind: "approval", Title: "Approval needed: " + summary, Body: imp.Summary, ServerID: exe.ServerID, Ref: ap.ID})
	return ap, nil
}

// Decide applies an operator decision to a pending approval, executes the
// action when approved, and resumes the conversation with the real outcome.
func (e *Engine) Decide(ctx context.Context, approvalID string, approve bool, by, reason, confirm string) (*store.Approval, error) {
	ap, err := e.deps.Store.GetApproval(ctx, approvalID)
	if err != nil {
		return nil, err
	}
	if ap.Status != "pending" {
		return nil, fmt.Errorf("approval is already %s", ap.Status)
	}
	var imp Impact
	_ = json.Unmarshal(ap.Impact, &imp)
	if approve && imp.Confirm != "" && strings.TrimSpace(confirm) != imp.Confirm {
		return nil, fmt.Errorf("type %q to confirm this high-risk operation", imp.Confirm)
	}
	status := "denied"
	if approve {
		status = "approved"
	}
	ap, err = e.deps.Store.DecideApproval(ctx, approvalID, status, by, reason)
	if err != nil {
		return nil, err
	}
	_ = e.deps.Store.Audit(ctx, by, "approval."+status, ap.Tool, map[string]any{"approval": ap.ID, "execution": ap.ExecutionID, "server": ap.ServerID, "reason": reason})
	e.deps.Bus.Publish("approval", ap)
	exe := ap.Execution
	if exe == nil {
		return ap, nil
	}
	go e.afterDecision(ap, exe, approve, reason)
	return ap, nil
}

func (e *Engine) afterDecision(ap *store.Approval, exe *store.Execution, approved bool, reason string) {
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Minute)
	defer cancel()
	var result any
	if !approved {
		_ = e.deps.Store.FinishExecution(ctx, exe.ID, "denied", nil, "denied by operator")
		result = map[string]any{"status": "denied_by_operator", "reason": reason}
	} else {
		t, ok := e.deps.Registry.Get(exe.Tool)
		if !ok {
			return
		}
		var args map[string]any
		_ = json.Unmarshal(exe.Args, &args)
		srv, err := e.deps.Store.GetServer(ctx, exe.ServerID)
		env := &Env{Deps: e.deps, Server: srv}
		if err == nil {
			env.Client, err = e.deps.Fleet.Client(srv.ID)
		}
		if err != nil {
			_ = e.deps.Store.FinishExecution(ctx, exe.ID, "failed", nil, "server no longer available: "+err.Error())
			result = map[string]any{"error": "server no longer available"}
		} else {
			// Arguments were validated before the approval was filed; the
			// tool re-validates through the agent as well.
			res, err := e.run(ctx, t, env, args, exe.ID)
			if err != nil {
				result = map[string]any{"status": "failed", "error": err.Error()}
			} else {
				result = map[string]any{"status": "executed_after_approval", "result": res}
			}
		}
	}
	if exe.ConversationID == "" {
		return
	}
	e.toolMessage(ctx, exe.ConversationID, exe.Tool, exe.ID, result)
	conv, err := e.deps.Store.GetConversation(ctx, exe.ConversationID)
	if err != nil || e.busy(conv.ID) {
		return
	}
	e.startTurn(conv, "general")
}

// ExpireApprovals closes overdue approvals and tells affected conversations.
func (e *Engine) ExpireApprovals(ctx context.Context) {
	ids, err := e.deps.Store.ExpireApprovals(ctx)
	if err != nil {
		return
	}
	for _, id := range ids {
		ap, err := e.deps.Store.GetApproval(ctx, id)
		if err != nil {
			continue
		}
		e.deps.Bus.Publish("approval", ap)
		if ap.Execution != nil && ap.Execution.ConversationID != "" {
			e.toolMessage(ctx, ap.Execution.ConversationID, ap.Tool, ap.ExecutionID, map[string]any{"status": "expired", "reason": "the approval request expired without a decision; nothing was executed"})
		}
	}
}

// ---------------------------------------------------------------- investigations

// Investigate runs a read-only AI investigation for an incident. It is rate
// limited and never performs changes (analyst mode) unless the server's
// policy enables autonomous remediation, in which case only pre-authorised
// tools can run and everything else becomes an approval request.
func (e *Engine) Investigate(ctx context.Context, inc *store.Incident) {
	s := e.Settings()
	e.invMu.Lock()
	cut := time.Now().Add(-time.Hour)
	kept := e.invTimes[:0]
	for _, t := range e.invTimes {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	e.invTimes = kept
	if len(e.invTimes) >= s.InvestPerHour {
		e.invMu.Unlock()
		inc.AIStatus = "skipped: hourly investigation limit reached"
		_ = e.deps.Store.UpdateIncident(ctx, inc)
		e.deps.Bus.Publish("incident", inc)
		return
	}
	e.invTimes = append(e.invTimes, time.Now())
	e.invMu.Unlock()

	full, err := e.deps.Store.GetIncident(ctx, inc.ID)
	if err != nil {
		return
	}
	pol, _ := e.deps.Policies.Effective(ctx, full.ServerID)
	mode := ModeAnalyst
	if pol.MonitoringLevel == "auto_remediate" && pol.AutonomousEnabled {
		mode = ModeAutonomous
	}
	conv := &store.Conversation{Title: "Investigation: " + full.Title, ServerID: full.ServerID, Mode: string(mode), Origin: "monitor"}
	if err := e.deps.Store.CreateConversation(ctx, conv); err != nil {
		return
	}
	full.AIStatus, full.AIConvID = "investigating", conv.ID
	_ = e.deps.Store.UpdateIncident(ctx, full)
	e.deps.Bus.Publish("incident", full)
	var b strings.Builder
	b.WriteString("AUTOMATED INVESTIGATION REQUEST from the NEXUS monitoring engine.\n\n")
	fmt.Fprintf(&b, "Incident: %s (severity %s, opened %s)\nAlerts:\n", full.Title, full.Severity, full.OpenedAt.Format(time.RFC3339))
	for _, a := range full.Alerts {
		fmt.Fprintf(&b, "- [%s] %s: %s (first seen %s, %d occurrences)\n", a.Severity, a.Title, a.Detail, a.FirstSeen.Format(time.RFC3339), a.Count)
	}
	b.WriteString("\nInvestigate with tools: confirm the alerts against live data, look for the underlying cause (related alerts often share one cause), check relevant logs, and assess impact. Then produce a report using the significant-operation format. ")
	if mode == ModeAnalyst {
		b.WriteString("Do not attempt changes; recommend them for the operator.")
	} else {
		b.WriteString("You may only perform actions pre-authorised by this server's autonomous policy; anything else must be recommended.")
	}
	m := &store.Message{ConversationID: conv.ID, Role: "user", Content: b.String(), Meta: toJSON(map[string]string{"origin": "monitor", "incident_id": full.ID})}
	_ = e.deps.Store.AddMessage(ctx, m)
	ictx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	e.mu.Lock()
	e.active[conv.ID] = cancel
	e.mu.Unlock()
	err = e.runTurn(ictx, conv, "deep")
	e.mu.Lock()
	delete(e.active, conv.ID)
	e.mu.Unlock()
	full, _ = e.deps.Store.GetIncident(context.Background(), inc.ID)
	if full == nil {
		return
	}
	if err != nil {
		full.AIStatus = "failed: " + err.Error()
	} else {
		msgs, _ := e.deps.Store.ListMessages(context.Background(), conv.ID)
		for i := len(msgs) - 1; i >= 0; i-- {
			if msgs[i].Role == "assistant" && strings.TrimSpace(msgs[i].Content) != "" {
				full.AIReport = msgs[i].Content
				break
			}
		}
		full.AIStatus = "complete"
		if pa, _ := e.pendingApproval(context.Background(), conv.ID); pa != nil {
			full.AIStatus = "awaiting approval"
		}
	}
	_ = e.deps.Store.UpdateIncident(context.Background(), full)
	e.deps.Bus.Publish("incident", full)
	note := &store.Notification{Severity: full.Severity, Kind: "ai_report", Title: "AI diagnostic report: " + full.Title, Body: firstLine(full.AIReport, full.AIStatus), ServerID: full.ServerID, Ref: full.ID}
	_ = e.deps.Store.AddNotification(context.Background(), note)
	e.deps.Bus.Publish("notification", note)
}

func firstLine(s, fallback string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return fallback
	}
	if len(s) > 400 {
		s = s[:400] + "…"
	}
	return s
}
