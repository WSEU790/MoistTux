package ollama

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"
)

// OpenAI-compatible engines (Colibri and others that serve /v1/chat/completions).
//
// Colibri (https://github.com/JustVugg/colibri) runs large open models such as
// GLM-5.2, Qwen3.6 or DeepSeek V4 locally and serves an OpenAI-compatible API
// at http://127.0.0.1:8000/v1 with OpenAI-style tool calling, a /health
// endpoint and an optional API key (COLI_API_KEY). It manages its own models
// (`coli`), so NEXUS lists them but does not download or delete them.

const DefaultColibriURL = "http://127.0.0.1:8000"

// NewOpenAI returns a client for an OpenAI-compatible engine. base is the
// server root (".../v1" is accepted too); apiKey may be empty.
func NewOpenAI(base, apiKey, name string) (*Client, error) {
	if base == "" {
		base = DefaultColibriURL
	}
	base = strings.TrimSuffix(strings.TrimRight(base, "/"), "/v1")
	c, err := New(base)
	if err != nil {
		return nil, fmt.Errorf("invalid %s URL %q", name, base)
	}
	c.kind, c.name, c.apiKey = "openai", name, apiKey
	return c, nil
}

func errManaged(c *Client) error {
	return fmt.Errorf("%s manages its own models: install or remove them on the machine running %s (for Colibri, with the coli command), then refresh this list", c.name, c.name)
}

func (c *Client) oaVersion(ctx context.Context) (string, error) {
	// /health is Colibri's; other OpenAI-compatible servers answer /v1/models.
	resp, err := c.req(ctx, "GET", "/health", nil)
	if err == nil {
		defer resp.Body.Close()
		var h map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&h)
		if v, ok := h["version"].(string); ok && v != "" {
			return v, nil
		}
		return "ready", nil
	}
	var ae *APIError
	if errors.As(err, &ae) && ae.Status == http.StatusServiceUnavailable {
		return "", fmt.Errorf("%s is starting or restarting (loading the model); try again shortly", c.name)
	}
	if _, lerr := c.oaList(ctx); lerr == nil {
		return "ready", nil
	}
	return "", err
}

type oaModel struct {
	ID            string `json:"id"`
	OwnedBy       string `json:"owned_by"`
	Created       int64  `json:"created"`
	ContextLength int    `json:"context_length"`
	MaxModelLen   int    `json:"max_model_len"`
}

func (c *Client) oaList(ctx context.Context) ([]Model, error) {
	var r struct {
		Data []oaModel `json:"data"`
	}
	if err := c.getJSON(ctx, "GET", "/v1/models", nil, &r); err != nil {
		return nil, err
	}
	out := make([]Model, 0, len(r.Data))
	for _, m := range r.Data {
		mod := Model{Name: m.ID, Model: m.ID, Details: ModelDetails{Family: strings.ToLower(c.name)}}
		if m.Created > 0 {
			mod.ModifiedAt = time.Unix(m.Created, 0).UTC()
		}
		out = append(out, mod)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Models that Colibri runs without tool support (requests with tools get 400).
var noToolModels = []string{"inkling", "olmoe"}

func (c *Client) oaShow(ctx context.Context, model string) (*ShowResponse, error) {
	var m oaModel
	if err := c.getJSON(ctx, "GET", "/v1/models/"+url.PathEscape(model), nil, &m); err != nil {
		var ae *APIError
		if !errors.As(err, &ae) || ae.Status != http.StatusNotFound {
			return nil, err
		}
	}
	caps := []string{"completion", "tools"}
	for _, n := range noToolModels {
		if strings.Contains(strings.ToLower(model), n) {
			caps = []string{"completion"}
		}
	}
	info := map[string]any{}
	if n := max(m.ContextLength, m.MaxModelLen); n > 0 {
		info["openai.context_length"] = float64(n)
	}
	return &ShowResponse{Capabilities: caps, ModelInfo: info, Details: ModelDetails{Family: strings.ToLower(c.name)}}, nil
}

// ---------------------------------------------------------------- chat

type oaToolCall struct {
	Index    *int   `json:"index,omitempty"`
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	} `json:"function"`
}

type oaMessage struct {
	Role       string       `json:"role"`
	Content    string       `json:"content"`
	ToolCalls  []oaToolCall `json:"tool_calls,omitempty"`
	ToolCallID string       `json:"tool_call_id,omitempty"`
	Name       string       `json:"name,omitempty"`
}

type oaChunk struct {
	Choices []struct {
		Delta struct {
			Content          string       `json:"content"`
			ReasoningContent string       `json:"reasoning_content"`
			Reasoning        string       `json:"reasoning"`
			ToolCalls        []oaToolCall `json:"tool_calls"`
		} `json:"delta"`
		Message *struct {
			Content          string       `json:"content"`
			ReasoningContent string       `json:"reasoning_content"`
			ToolCalls        []oaToolCall `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Error json.RawMessage `json:"error"`
}

// toOpenAI converts NEXUS's conversation to OpenAI messages. Ollama-style
// history carries no tool-call IDs, so IDs are assigned here and matched to
// the tool results that follow, in order.
func toOpenAI(msgs []Message) []oaMessage {
	out := make([]oaMessage, 0, len(msgs))
	var pending []string // IDs of calls awaiting results
	n := 0
	for _, m := range msgs {
		om := oaMessage{Role: m.Role, Content: m.Content}
		switch m.Role {
		case "assistant":
			pending = pending[:0]
			for _, tc := range m.ToolCalls {
				n++
				id := fmt.Sprintf("call_%d", n)
				pending = append(pending, id)
				var call oaToolCall
				call.ID, call.Type = id, "function"
				call.Function.Name = tc.Function.Name
				args := strings.TrimSpace(string(tc.Function.Arguments))
				if args == "" || args == "null" {
					args = "{}"
				}
				call.Function.Arguments = args
				om.ToolCalls = append(om.ToolCalls, call)
			}
		case "tool":
			if len(pending) > 0 {
				om.ToolCallID, pending = pending[0], pending[1:]
			} else {
				n++
				om.ToolCallID = fmt.Sprintf("call_%d", n)
			}
			om.Name = m.ToolName
		}
		out = append(out, om)
	}
	return out
}

func (c *Client) oaChat(ctx context.Context, req ChatRequest, onDelta func(content, thinking string)) (*ChatResult, error) {
	body := map[string]any{
		"model":          req.Model,
		"messages":       toOpenAI(req.Messages),
		"stream":         true,
		"stream_options": map[string]any{"include_usage": true},
	}
	if len(req.Tools) > 0 {
		body["tools"] = req.Tools
	}
	if req.Options != nil {
		if req.Options.Temperature != nil {
			body["temperature"] = *req.Options.Temperature
		}
		if req.Options.TopP != nil {
			body["top_p"] = *req.Options.TopP
		}
		if req.Options.NumPredict > 0 {
			body["max_tokens"] = req.Options.NumPredict
		}
	}
	// req.Format (Ollama's JSON schema) has no portable equivalent; the JSON
	// tool protocol in the system prompt still applies.
	resp, err := c.req(ctx, "POST", "/v1/chat/completions", body)
	if err != nil {
		var ae *APIError
		if errors.As(err, &ae) && ae.Status == http.StatusTooManyRequests {
			return nil, fmt.Errorf("%s is busy with other requests (its queue is full); try again in a moment", c.name)
		}
		return nil, err
	}
	defer resp.Body.Close()

	res := &ChatResult{Message: Message{Role: "assistant"}}
	var content, thinking strings.Builder
	calls := map[int]*oaToolCall{}
	var order []int
	addCall := func(i int, d oaToolCall) {
		tc, ok := calls[i]
		if !ok {
			tc = &oaToolCall{}
			calls[i] = tc
			order = append(order, i)
		}
		if d.ID != "" {
			tc.ID = d.ID
		}
		if d.Function.Name != "" {
			tc.Function.Name = d.Function.Name
		}
		tc.Function.Arguments += d.Function.Arguments
	}
	var first time.Time
	handle := func(ch *oaChunk) error {
		if len(ch.Error) > 0 && string(ch.Error) != "null" {
			return fmt.Errorf("%s: %s", c.name, errorText([]byte(`{"error":`+string(ch.Error)+`}`)))
		}
		for _, choice := range ch.Choices {
			d := choice.Delta
			text, think, tcs := d.Content, d.ReasoningContent+d.Reasoning, d.ToolCalls
			if choice.Message != nil { // non-streamed response
				text, think, tcs = choice.Message.Content, choice.Message.ReasoningContent, choice.Message.ToolCalls
			}
			if text != "" || think != "" {
				if first.IsZero() {
					first = time.Now()
				}
				content.WriteString(text)
				thinking.WriteString(think)
				if onDelta != nil {
					onDelta(text, think)
				}
			}
			for k, tc := range tcs {
				i := k
				if tc.Index != nil {
					i = *tc.Index
				}
				addCall(i, tc)
			}
			if choice.FinishReason != "" {
				res.DoneReason = choice.FinishReason
			}
		}
		if ch.Usage != nil {
			res.PromptTokens, res.OutputTokens = ch.Usage.PromptTokens, ch.Usage.CompletionTokens
		}
		return nil
	}

	if strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		// The server answered without streaming.
		var ch oaChunk
		if err := json.NewDecoder(resp.Body).Decode(&ch); err != nil {
			return nil, err
		}
		if err := handle(&ch); err != nil {
			return nil, err
		}
	} else {
		sc := bufio.NewScanner(resp.Body)
		sc.Buffer(make([]byte, 256*1024), 16*1024*1024)
		for sc.Scan() {
			line := bytes.TrimSpace(sc.Bytes())
			if len(line) == 0 || line[0] == ':' { // blank lines and keep-alive comments
				continue
			}
			if !bytes.HasPrefix(line, []byte("data:")) {
				continue
			}
			data := bytes.TrimSpace(line[5:])
			if string(data) == "[DONE]" {
				break
			}
			var ch oaChunk
			if json.Unmarshal(data, &ch) != nil {
				continue
			}
			if err := handle(&ch); err != nil {
				return nil, err
			}
		}
		if err := sc.Err(); err != nil {
			return nil, err
		}
	}
	sort.Ints(order)
	for _, i := range order {
		tc := calls[i]
		var out ToolCall
		out.Function.Name = tc.Function.Name
		args := strings.TrimSpace(tc.Function.Arguments)
		if args == "" {
			args = "{}"
		}
		if !json.Valid([]byte(args)) {
			// Keep it as a JSON string so validation reports a clear error.
			b, _ := json.Marshal(args)
			args = string(b)
		}
		out.Function.Arguments = json.RawMessage(args)
		res.Message.ToolCalls = append(res.Message.ToolCalls, out)
	}
	if res.OutputTokens > 0 && !first.IsZero() {
		if secs := time.Since(first).Seconds(); secs > 0 {
			res.TokensPerSecond = float64(res.OutputTokens) / secs
		}
	}
	res.Message.Content, res.Message.Thinking = content.String(), thinking.String()
	return res, nil
}
