package ollama

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ChatGPT plan usage through the OpenAI Responses API
// (https://developers.openai.com/siwc/token-sharing-open-source/models-and-inference).
//
// The bearer token comes from the operator's "Sign in with ChatGPT" session
// (internal/core/chatgpt) and is fetched per request, so refreshes are
// transparent. The flow requires store:false and stream:true, the full
// history in input, instructions instead of system messages, function tools
// grouped in a namespace, and no sampling parameters.

// ToolNamespace groups NEXUS's tools for the Responses API.
const ToolNamespace = "nexus"

// UsageURL is where ChatGPT users review and change their usage limits.
const UsageURL = "https://chatgpt.com/settings/usage"

// NewResponses returns a client that runs chats on the operator's ChatGPT
// plan. token supplies a valid access token; onLimit (optional) is called
// when OpenAI reports that the plan's usage limit was reached.
func NewResponses(base string, token func(context.Context) (string, error), onLimit func()) (*Client, error) {
	c, err := New(base)
	if err != nil {
		return nil, err
	}
	c.kind, c.name, c.tokenFn, c.onLimit = "responses", "ChatGPT", token, onLimit
	c.rs = &rsState{ctx: map[string]int{}}
	return c, nil
}

type rsState struct {
	mu        sync.Mutex
	ctx       map[string]int // context window per model, when the catalog reports it
	namespace atomic.Bool    // the model's calls carry a namespace field: echo it back
}

type rsModel struct {
	Slug          string `json:"slug"`
	ID            string `json:"id"`
	DisplayName   string `json:"display_name"`
	Visibility    string `json:"visibility"`
	ContextWindow int    `json:"context_window"`
	ContextLength int    `json:"context_length"`
}

func (c *Client) rsCatalog(ctx context.Context) ([]rsModel, error) {
	var r struct {
		Models []rsModel `json:"models"`
		Data   []rsModel `json:"data"`
	}
	if err := c.getJSON(ctx, "GET", "/v1/models", nil, &r); err != nil {
		return nil, c.rsError(err)
	}
	var out []rsModel
	if len(r.Models) > 0 {
		for _, m := range r.Models {
			if m.Visibility == "list" && m.Slug != "" { // models intended for display
				out = append(out, m)
			}
		}
	} else { // a plain OpenAI model list
		for _, m := range r.Data {
			m.Slug = m.ID
			out = append(out, m)
		}
	}
	c.rs.mu.Lock()
	for _, m := range out {
		c.rs.ctx[m.Slug] = max(m.ContextWindow, m.ContextLength)
	}
	c.rs.mu.Unlock()
	return out, nil
}

func (c *Client) rsVersion(ctx context.Context) (string, error) {
	if _, err := c.rsCatalog(ctx); err != nil {
		return "", err
	}
	return "ChatGPT plan", nil
}

// rsList keeps the server's ordering (it lists preferred models first).
func (c *Client) rsList(ctx context.Context) ([]Model, error) {
	cat, err := c.rsCatalog(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Model, 0, len(cat))
	for _, m := range cat {
		out = append(out, Model{Name: m.Slug, Model: m.Slug, DisplayName: m.DisplayName, Details: ModelDetails{Family: "chatgpt"}})
	}
	return out, nil
}

func (c *Client) rsShow(ctx context.Context, model string) (*ShowResponse, error) {
	c.rs.mu.Lock()
	n, ok := c.rs.ctx[model]
	c.rs.mu.Unlock()
	if !ok {
		if _, err := c.rsCatalog(ctx); err != nil {
			return nil, err
		}
		c.rs.mu.Lock()
		n = c.rs.ctx[model]
		c.rs.mu.Unlock()
	}
	info := map[string]any{}
	if n > 0 {
		info["chatgpt.context_length"] = float64(n)
	}
	return &ShowResponse{Capabilities: []string{"completion", "tools"}, ModelInfo: info, Details: ModelDetails{Family: "chatgpt"}}, nil
}

// rsInput converts NEXUS's conversation to Responses input items. System
// messages become instructions. Tool calls get IDs matched, in order, to
// the tool results that follow (the stored history has no IDs).
func (c *Client) rsInput(msgs []Message) (instructions string, input []map[string]any) {
	var sys []string
	var pending []string
	n := 0
	ns := c.rs.namespace.Load()
	for _, m := range msgs {
		switch m.Role {
		case "system":
			if strings.TrimSpace(m.Content) != "" {
				sys = append(sys, m.Content)
			}
		case "user":
			input = append(input, map[string]any{"role": "user", "content": m.Content})
		case "assistant":
			if m.Content != "" {
				input = append(input, map[string]any{"role": "assistant", "content": m.Content})
			}
			pending = pending[:0]
			for _, tc := range m.ToolCalls {
				n++
				id := fmt.Sprintf("call_%d", n)
				pending = append(pending, id)
				args := strings.TrimSpace(string(tc.Function.Arguments))
				if args == "" || args == "null" {
					args = "{}"
				}
				item := map[string]any{"type": "function_call", "call_id": id, "name": tc.Function.Name, "arguments": args}
				if ns {
					item["namespace"] = ToolNamespace
				}
				input = append(input, item)
			}
		case "tool":
			id := ""
			if len(pending) > 0 {
				id, pending = pending[0], pending[1:]
			} else {
				// A result without a call (should not happen): keep it as context.
				input = append(input, map[string]any{"role": "user", "content": "Tool result (" + m.ToolName + "):\n" + m.Content})
				continue
			}
			input = append(input, map[string]any{"type": "function_call_output", "call_id": id, "output": m.Content})
		}
	}
	return strings.Join(sys, "\n\n"), input
}

func rsTools(tools []Tool) []map[string]any {
	if len(tools) == 0 {
		return nil
	}
	fns := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		params := t.Function.Parameters
		if params == nil {
			params = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		fns = append(fns, map[string]any{"type": "function", "name": t.Function.Name, "description": t.Function.Description, "parameters": params})
	}
	return []map[string]any{{
		"type":        "namespace",
		"name":        ToolNamespace,
		"description": "NEXUS tools for inspecting and administering the operator's Linux servers. Results are live data from the servers.",
		"tools":       fns,
	}}
}

// rsCallName strips a namespace prefix if the model returns one in the name.
func rsCallName(name string) string {
	for _, sep := range []string{".", "__", "/", ":"} {
		if p := ToolNamespace + sep; strings.HasPrefix(name, p) {
			return name[len(p):]
		}
	}
	return name
}

// Error codes from https://developers.openai.com/siwc/token-sharing-open-source/errors-and-recovery.
func (c *Client) rsCodeError(status int, code, param, msg string) error {
	switch code {
	case "subscription_sharing_usage_limit_exceeded":
		if c.onLimit != nil {
			c.onLimit()
		}
		return fmt.Errorf("ChatGPT usage limit reached (your plan's or the limit you set for NEXUS). NEXUS paused requests that use your ChatGPT plan; review your limits at %s", UsageURL)
	case "subscription_sharing_user_not_eligible":
		return errors.New("ChatGPT plan usage is not available for this account, workspace or policy. Switch the AI engine back to Ollama or Colibri, or link another ChatGPT account")
	case "subscription_sharing_usage_unavailable", "subscription_sharing_user_unavailable":
		return errors.New("ChatGPT could not check your plan's usage right now; try again in a few minutes")
	case "subscription_sharing_unsupported_capability":
		if param != "" {
			return fmt.Errorf("ChatGPT plan usage does not support %q in this request (%s)", param, msg)
		}
		return fmt.Errorf("ChatGPT plan usage does not support part of this request: %s", msg)
	case "subscription_sharing_invalid_user":
		return errors.New("ChatGPT could not validate your subscription for this sign-in; sign in again in Settings → AI engine")
	case "chatpass_v2_scope_not_authorized", "chatpass_v2_invalid_authorization_context":
		return errors.New("this ChatGPT sign-in does not authorize plan usage; choose Enable ChatGPT plan use in Settings → AI engine")
	}
	text := msg
	if code != "" {
		text = code + ": " + msg
	}
	switch status {
	case http.StatusUnauthorized:
		return fmt.Errorf("ChatGPT did not accept the sign-in (%s). Check the linked account in Settings → AI engine", text)
	case http.StatusForbidden:
		return fmt.Errorf("ChatGPT refused the request (%s)", text)
	case http.StatusServiceUnavailable:
		return fmt.Errorf("ChatGPT plan usage is temporarily unavailable (%s); try again shortly", text)
	case http.StatusTooManyRequests:
		return fmt.Errorf("ChatGPT is rate-limiting requests (%s); try again shortly", text)
	}
	if status > 0 {
		return fmt.Errorf("ChatGPT returned HTTP %d: %s", status, text)
	}
	return fmt.Errorf("ChatGPT: %s", text)
}

func (c *Client) rsError(err error) error {
	var ae *APIError
	if errors.As(err, &ae) {
		return c.rsCodeError(ae.Status, ae.Code, ae.Param, ae.Msg)
	}
	return err
}

type rsEvent struct {
	Type     string          `json:"type"`
	Delta    string          `json:"delta"`
	Item     json.RawMessage `json:"item"`
	Response *struct {
		Status string `json:"status"`
		Error  *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		IncompleteDetails *struct {
			Reason string `json:"reason"`
		} `json:"incomplete_details"`
		Usage *struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
		Output []json.RawMessage `json:"output"`
	} `json:"response"`
	// type "error"
	Code    string `json:"code"`
	Message string `json:"message"`
	Param   string `json:"param"`
}

type rsItem struct {
	Type      string `json:"type"`
	ID        string `json:"id"`
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Arguments string `json:"arguments"`
}

func (c *Client) rsChat(ctx context.Context, req ChatRequest, onDelta func(content, thinking string)) (*ChatResult, error) {
	instructions, input := c.rsInput(req.Messages)
	body := map[string]any{
		"model":  req.Model,
		"input":  input,
		"store":  false,
		"stream": true,
	}
	if instructions != "" {
		body["instructions"] = instructions
	}
	if t := rsTools(req.Tools); t != nil {
		body["tools"] = t
	}
	// Temperature, top_p, max_output_tokens and similar fields are not
	// accepted on this route, so req.Options is not sent.
	resp, err := c.req(ctx, "POST", "/v1/responses", body)
	if err != nil {
		return nil, c.rsError(err)
	}
	defer resp.Body.Close()

	res := &ChatResult{Message: Message{Role: "assistant"}}
	var content, thinking strings.Builder
	var calls []rsItem
	seen := map[string]bool{}
	addCall := func(raw json.RawMessage) {
		var it rsItem
		if json.Unmarshal(raw, &it) != nil || it.Type != "function_call" {
			return
		}
		key := it.CallID + "|" + it.ID
		if seen[key] {
			return
		}
		seen[key] = true
		if it.Namespace != "" {
			c.rs.namespace.Store(true)
		}
		calls = append(calls, it)
	}
	var first time.Time
	completed := false
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 256*1024), 16*1024*1024)
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if !bytes.HasPrefix(line, []byte("data:")) {
			continue // blank lines, comments and "event:" lines (the type is in the data)
		}
		data := bytes.TrimSpace(line[5:])
		if string(data) == "[DONE]" {
			break
		}
		var ev rsEvent
		if json.Unmarshal(data, &ev) != nil {
			continue
		}
		switch ev.Type {
		case "response.output_text.delta", "response.reasoning_summary_text.delta", "response.reasoning_text.delta":
			if first.IsZero() {
				first = time.Now()
			}
			text, think := ev.Delta, ""
			if ev.Type != "response.output_text.delta" {
				text, think = "", ev.Delta
			}
			content.WriteString(text)
			thinking.WriteString(think)
			if onDelta != nil {
				onDelta(text, think)
			}
		case "response.output_item.done":
			addCall(ev.Item)
		case "response.completed":
			completed = true
			if r := ev.Response; r != nil {
				for _, o := range r.Output { // in case output_item.done was not streamed
					addCall(o)
				}
				if r.Usage != nil {
					res.PromptTokens, res.OutputTokens = r.Usage.InputTokens, r.Usage.OutputTokens
				}
			}
		case "response.failed":
			code, msg := "", "the response failed"
			if ev.Response != nil && ev.Response.Error != nil {
				code, msg = ev.Response.Error.Code, ev.Response.Error.Message
			}
			return nil, c.rsCodeError(0, code, "", msg)
		case "response.incomplete":
			reason := "unknown"
			if ev.Response != nil && ev.Response.IncompleteDetails != nil {
				reason = ev.Response.IncompleteDetails.Reason
			}
			return nil, fmt.Errorf("ChatGPT stopped before finishing the answer (%s); try again or ask a narrower question", reason)
		case "error":
			return nil, c.rsCodeError(0, ev.Code, ev.Param, ev.Message)
		}
		if completed {
			break
		}
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("the ChatGPT stream was interrupted: %w", err)
	}
	if !completed {
		return nil, errors.New("the ChatGPT stream ended before the response completed; try again")
	}
	for _, it := range calls {
		var tc ToolCall
		tc.Function.Name = rsCallName(it.Name)
		args := strings.TrimSpace(it.Arguments)
		if args == "" {
			args = "{}"
		}
		if !json.Valid([]byte(args)) {
			b, _ := json.Marshal(args) // keep it as a string so validation reports it
			args = string(b)
		}
		tc.Function.Arguments = json.RawMessage(args)
		res.Message.ToolCalls = append(res.Message.ToolCalls, tc)
	}
	res.DoneReason = "stop"
	if len(calls) > 0 {
		res.DoneReason = "tool_calls"
	}
	if res.OutputTokens > 0 && !first.IsZero() {
		if secs := time.Since(first).Seconds(); secs > 0 {
			res.TokensPerSecond = float64(res.OutputTokens) / secs
		}
	}
	res.Message.Content, res.Message.Thinking = content.String(), thinking.String()
	return res, nil
}
