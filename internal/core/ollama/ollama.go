// Package ollama is NEXUS's inference client. It speaks the documented Ollama
// REST API (https://github.com/ollama/ollama/blob/main/docs/api.md) and, for
// engines such as Colibri, the OpenAI-compatible chat API (openai.go). Both
// run on the operator's own machines; nothing here talks to a cloud service
// unless the operator points it at one.
package ollama

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const DefaultURL = "http://127.0.0.1:11434"

type Client struct {
	base   string
	hc     *http.Client // no overall timeout: streams can be long
	kind   string       // "ollama" or "openai" (OpenAI-compatible, e.g. Colibri)
	name   string       // engine name shown to the operator
	apiKey string       // optional bearer key (OpenAI-compatible engines)
	// tokenFn supplies a fresh bearer token per request (ChatGPT sign-in).
	tokenFn func(context.Context) (string, error)
	// onLimit is called when the provider reports a usage limit.
	onLimit func()
	rs      *rsState // ChatGPT (Responses API) state
}

// Kind is "ollama", "openai" (OpenAI-compatible) or "responses" (ChatGPT plan).
func (c *Client) Kind() string { return c.kind }

// Name is the engine's display name ("Ollama", "Colibri").
func (c *Client) Name() string { return c.name }

func New(base string) (*Client, error) {
	if base == "" {
		base = DefaultURL
	}
	u, err := url.Parse(strings.TrimRight(base, "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("invalid Ollama URL %q", base)
	}
	return &Client{base: u.String(), kind: "ollama", name: "Ollama", hc: &http.Client{Transport: &http.Transport{ResponseHeaderTimeout: 30 * time.Minute, IdleConnTimeout: 90 * time.Second}}}, nil
}

func (c *Client) BaseURL() string { return c.base }

// IsLocal reports whether the endpoint is on this machine (privacy indicator).
func (c *Client) IsLocal() bool {
	if c.kind == "responses" {
		return false // ChatGPT always runs at OpenAI
	}
	u, _ := url.Parse(c.base)
	h := u.Hostname()
	return h == "127.0.0.1" || h == "localhost" || h == "::1"
}

type APIError struct {
	Status int
	Msg    string
	Code   string // machine-readable error code, when the provider sends one
	Param  string
}

func (e *APIError) Error() string { return fmt.Sprintf("AI engine HTTP %d: %s", e.Status, e.Msg) }

func (c *Client) req(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	r, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return nil, err
	}
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	if c.tokenFn != nil {
		tok, err := c.tokenFn(ctx)
		if err != nil {
			return nil, err
		}
		r.Header.Set("Authorization", "Bearer "+tok)
	} else if c.apiKey != "" {
		r.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	resp, err := c.hc.Do(r)
	if err != nil {
		return nil, fmt.Errorf("%s is not reachable at %s: %w", c.name, c.base, err)
	}
	if resp.StatusCode >= 300 {
		defer resp.Body.Close()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		code, param := errorCode(b)
		return nil, &APIError{Status: resp.StatusCode, Msg: errorText(b), Code: code, Param: param}
	}
	return resp, nil
}

// errorText extracts the message from Ollama ({"error": "..."}) and OpenAI
// ({"error": {"message": "..."}}) error bodies.
func errorText(b []byte) string {
	var e struct {
		Error json.RawMessage `json:"error"`
	}
	if json.Unmarshal(b, &e) == nil && len(e.Error) > 0 {
		var s string
		if json.Unmarshal(e.Error, &s) == nil && s != "" {
			return s
		}
		var o struct {
			Message string `json:"message"`
			Code    any    `json:"code"`
		}
		if json.Unmarshal(e.Error, &o) == nil && o.Message != "" {
			return o.Message
		}
	}
	var d struct {
		Detail string `json:"detail"`
	}
	if json.Unmarshal(b, &d) == nil && d.Detail != "" {
		return d.Detail
	}
	return strings.TrimSpace(string(b))
}

// errorCode extracts error.code and error.param from an OpenAI error body.
func errorCode(b []byte) (code, param string) {
	var e struct {
		Error struct {
			Code  any    `json:"code"`
			Param string `json:"param"`
		} `json:"error"`
	}
	if json.Unmarshal(b, &e) == nil {
		if s, ok := e.Error.Code.(string); ok {
			code = s
		}
		param = e.Error.Param
	}
	return code, param
}

func (c *Client) getJSON(ctx context.Context, method, path string, body, out any) error {
	resp, err := c.req(ctx, method, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// ---------------------------------------------------------------- discovery

func (c *Client) Version(ctx context.Context) (string, error) {
	if c.kind == "responses" {
		return c.rsVersion(ctx)
	}
	if c.kind == "openai" {
		return c.oaVersion(ctx)
	}
	var v struct{ Version string }
	err := c.getJSON(ctx, "GET", "/api/version", nil, &v)
	return v.Version, err
}

type ModelDetails struct {
	Format            string   `json:"format"`
	Family            string   `json:"family"`
	Families          []string `json:"families"`
	ParameterSize     string   `json:"parameter_size"`
	QuantizationLevel string   `json:"quantization_level"`
}

type Model struct {
	Name       string       `json:"name"`
	Model      string       `json:"model"`
	ModifiedAt time.Time    `json:"modified_at"`
	Size       int64        `json:"size"`
	Digest     string       `json:"digest"`
	Details    ModelDetails `json:"details"`
	// DisplayName is the provider's label (ChatGPT models), if any.
	DisplayName string `json:"display_name,omitempty"`
}

func (c *Client) List(ctx context.Context) ([]Model, error) {
	if c.kind == "responses" {
		return c.rsList(ctx)
	}
	if c.kind == "openai" {
		return c.oaList(ctx)
	}
	var r struct{ Models []Model }
	if err := c.getJSON(ctx, "GET", "/api/tags", nil, &r); err != nil {
		return nil, err
	}
	return r.Models, nil
}

type ShowResponse struct {
	Modelfile    string         `json:"modelfile"`
	Parameters   string         `json:"parameters"`
	Template     string         `json:"template"`
	Details      ModelDetails   `json:"details"`
	ModelInfo    map[string]any `json:"model_info"`
	Capabilities []string       `json:"capabilities"`
}

func (c *Client) Show(ctx context.Context, model string) (*ShowResponse, error) {
	if c.kind == "responses" {
		return c.rsShow(ctx, model)
	}
	if c.kind == "openai" {
		return c.oaShow(ctx, model)
	}
	var r ShowResponse
	return &r, c.getJSON(ctx, "POST", "/api/show", map[string]string{"model": model}, &r)
}

// Capabilities derives what a model can do. Newer Ollama reports this
// directly; for older servers tool support is inferred from the chat template.
type Capabilities struct {
	Tools         bool   `json:"tools"`
	Vision        bool   `json:"vision"`
	Thinking      bool   `json:"thinking"`
	Embedding     bool   `json:"embedding"`
	Completion    bool   `json:"completion"`
	ContextLength int    `json:"context_length"`
	Source        string `json:"source"` // reported | inferred
}

func (s *ShowResponse) Caps() Capabilities {
	c := Capabilities{}
	if len(s.Capabilities) > 0 {
		c.Source = "reported"
		for _, x := range s.Capabilities {
			switch x {
			case "tools":
				c.Tools = true
			case "vision":
				c.Vision = true
			case "thinking":
				c.Thinking = true
			case "embedding":
				c.Embedding = true
			case "completion":
				c.Completion = true
			}
		}
	} else {
		c.Source = "inferred"
		c.Tools = strings.Contains(s.Template, ".Tools") || strings.Contains(s.Template, "tool_calls")
		c.Completion = true
	}
	for k, v := range s.ModelInfo {
		if strings.HasSuffix(k, ".context_length") {
			if f, ok := v.(float64); ok {
				c.ContextLength = int(f)
			}
		}
	}
	return c
}

type Running struct {
	Name          string       `json:"name"`
	Model         string       `json:"model"`
	Size          int64        `json:"size"`
	SizeVRAM      int64        `json:"size_vram"`
	ExpiresAt     time.Time    `json:"expires_at"`
	ContextLength int          `json:"context_length"`
	Details       ModelDetails `json:"details"`
}

func (c *Client) PS(ctx context.Context) ([]Running, error) {
	if c.kind != "ollama" {
		return []Running{}, nil
	}
	var r struct{ Models []Running }
	if err := c.getJSON(ctx, "GET", "/api/ps", nil, &r); err != nil {
		return nil, err
	}
	return r.Models, nil
}

func (c *Client) Delete(ctx context.Context, model string) error {
	if c.kind != "ollama" {
		return errManaged(c)
	}
	return c.getJSON(ctx, "DELETE", "/api/delete", map[string]string{"model": model}, nil)
}

// PullProgress is one status line from /api/pull.
type PullProgress struct {
	Status    string `json:"status"`
	Digest    string `json:"digest,omitempty"`
	Total     int64  `json:"total,omitempty"`
	Completed int64  `json:"completed,omitempty"`
	Error     string `json:"error,omitempty"`
}

func (c *Client) Pull(ctx context.Context, model string, fn func(PullProgress)) error {
	if c.kind != "ollama" {
		return errManaged(c)
	}
	resp, err := c.req(ctx, "POST", "/api/pull", map[string]any{"model": model, "stream": true})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		var p PullProgress
		if json.Unmarshal(sc.Bytes(), &p) != nil {
			continue
		}
		if p.Error != "" {
			return errors.New(p.Error)
		}
		fn(p)
		if p.Status == "success" {
			return nil
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return errors.New("pull stream ended without success")
}

// ---------------------------------------------------------------- chat

type ToolFunction struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type Tool struct {
	Type     string       `json:"type"`
	Function ToolFunction `json:"function"`
}

type ToolCall struct {
	Function struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	} `json:"function"`
}

type Message struct {
	Role      string     `json:"role"`
	Content   string     `json:"content"`
	Thinking  string     `json:"thinking,omitempty"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	ToolName  string     `json:"tool_name,omitempty"`
}

type Options struct {
	Temperature *float64 `json:"temperature,omitempty"`
	NumCtx      int      `json:"num_ctx,omitempty"`
	TopP        *float64 `json:"top_p,omitempty"`
	Seed        *int     `json:"seed,omitempty"`
	NumPredict  int      `json:"num_predict,omitempty"`
}

type ChatRequest struct {
	Model     string          `json:"model"`
	Messages  []Message       `json:"messages"`
	Tools     []Tool          `json:"tools,omitempty"`
	Format    json.RawMessage `json:"format,omitempty"`
	Options   *Options        `json:"options,omitempty"`
	Stream    bool            `json:"stream"`
	KeepAlive string          `json:"keep_alive,omitempty"`
}

type ChatChunk struct {
	Model           string  `json:"model"`
	Message         Message `json:"message"`
	Done            bool    `json:"done"`
	DoneReason      string  `json:"done_reason,omitempty"`
	TotalDuration   int64   `json:"total_duration,omitempty"`
	PromptEvalCount int     `json:"prompt_eval_count,omitempty"`
	EvalCount       int     `json:"eval_count,omitempty"`
	EvalDuration    int64   `json:"eval_duration,omitempty"`
}

// ChatResult is the accumulated outcome of a (streamed) chat call.
type ChatResult struct {
	Message         Message
	DoneReason      string
	PromptTokens    int
	OutputTokens    int
	TokensPerSecond float64
}

// Chat streams a chat completion, calling onDelta for each content fragment.
func (c *Client) Chat(ctx context.Context, req ChatRequest, onDelta func(content, thinking string)) (*ChatResult, error) {
	if c.kind == "responses" {
		return c.rsChat(ctx, req, onDelta)
	}
	if c.kind == "openai" {
		return c.oaChat(ctx, req, onDelta)
	}
	req.Stream = true
	resp, err := c.req(ctx, "POST", "/api/chat", req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	res := &ChatResult{Message: Message{Role: "assistant"}}
	var content, thinking strings.Builder
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 256*1024), 16*1024*1024)
	for sc.Scan() {
		var ch ChatChunk
		line := sc.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		if err := json.Unmarshal(line, &ch); err != nil {
			var e struct{ Error string }
			if json.Unmarshal(line, &e) == nil && e.Error != "" {
				return nil, errors.New(e.Error)
			}
			continue
		}
		if ch.Message.Content != "" || ch.Message.Thinking != "" {
			content.WriteString(ch.Message.Content)
			thinking.WriteString(ch.Message.Thinking)
			if onDelta != nil {
				onDelta(ch.Message.Content, ch.Message.Thinking)
			}
		}
		res.Message.ToolCalls = append(res.Message.ToolCalls, ch.Message.ToolCalls...)
		if ch.Done {
			res.DoneReason = ch.DoneReason
			res.PromptTokens, res.OutputTokens = ch.PromptEvalCount, ch.EvalCount
			if ch.EvalDuration > 0 {
				res.TokensPerSecond = float64(ch.EvalCount) / (float64(ch.EvalDuration) / 1e9)
			}
			break
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	res.Message.Content, res.Message.Thinking = content.String(), thinking.String()
	return res, nil
}
