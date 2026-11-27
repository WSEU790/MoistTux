package ollama

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeOpenAI imitates an OpenAI-compatible engine such as Colibri.
func fakeOpenAI(t *testing.T, stream bool, gotBody *map[string]any, gotAuth *string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if gotAuth != nil {
			*gotAuth = r.Header.Get("Authorization")
		}
		switch r.URL.Path {
		case "/health":
			fmt.Fprint(w, `{"status":"ok","active":0,"queued":0}`)
		case "/v1/models":
			fmt.Fprint(w, `{"object":"list","data":[{"id":"qwen3.6-colibri","object":"model","context_length":32768},{"id":"glm-5.2-colibri","object":"model"}]}`)
		case "/v1/models/glm-5.2-colibri":
			fmt.Fprint(w, `{"id":"glm-5.2-colibri","context_length":131072}`)
		case "/v1/chat/completions":
			b, _ := io.ReadAll(r.Body)
			if gotBody != nil {
				_ = json.Unmarshal(b, gotBody)
			}
			if !stream {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"choices":[{"message":{"role":"assistant","content":"","tool_calls":[{"id":"x1","type":"function","function":{"name":"df","arguments":"{\"all\":true}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":10,"completion_tokens":5}}`)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			for _, l := range []string{
				`: keepalive`,
				`data: {"choices":[]}`,
				`data: {"choices":[{"delta":{"reasoning_content":"Checking disk. "}}]}`,
				`data: {"choices":[{"delta":{"content":"Let me "}}]}`,
				`data: {"choices":[{"delta":{"content":"check."}}]}`,
				`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_a","type":"function","function":{"name":"get_disk_metrics","arguments":""}}]}}]}`,
				`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"server\":"}}]}}]}`,
				`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"web-01\"}"}}]}}]}`,
				`data: {"choices":[{"delta":{"tool_calls":[{"index":1,"id":"call_b","type":"function","function":{"name":"list_alerts","arguments":"{}"}}]}}]}`,
				`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}`,
				`data: {"choices":[],"usage":{"prompt_tokens":120,"completion_tokens":30}}`,
				`data: [DONE]`,
			} {
				fmt.Fprintln(w, l)
				fmt.Fprintln(w)
			}
		default:
			http.NotFound(w, r)
		}
	}))
}

func TestOpenAIStreamingToolCalls(t *testing.T) {
	var body map[string]any
	var auth string
	srv := fakeOpenAI(t, true, &body, &auth)
	defer srv.Close()
	c, err := NewOpenAI(srv.URL+"/v1", "secret-key", "Colibri")
	if err != nil {
		t.Fatal(err)
	}
	temp := 0.2
	var streamed strings.Builder
	res, err := c.Chat(context.Background(), ChatRequest{
		Model: "glm-5.2-colibri",
		Messages: []Message{
			{Role: "system", Content: "sys"},
			{Role: "user", Content: "is the disk ok?"},
			{Role: "assistant", ToolCalls: []ToolCall{tc("list_servers", `{}`), tc("get_cpu_metrics", `{"server":"a"}`)}},
			{Role: "tool", ToolName: "list_servers", Content: "[]"},
			{Role: "tool", ToolName: "get_cpu_metrics", Content: "{}"},
		},
		Tools:   []Tool{{Type: "function", Function: ToolFunction{Name: "get_disk_metrics", Parameters: map[string]any{"type": "object"}}}},
		Options: &Options{Temperature: &temp},
	}, func(c, th string) { streamed.WriteString(c) })
	if err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer secret-key" {
		t.Errorf("auth header %q", auth)
	}
	if res.Message.Content != "Let me check." || streamed.String() != "Let me check." || res.Message.Thinking != "Checking disk. " {
		t.Errorf("content %q thinking %q", res.Message.Content, res.Message.Thinking)
	}
	if len(res.Message.ToolCalls) != 2 || res.Message.ToolCalls[0].Function.Name != "get_disk_metrics" || string(res.Message.ToolCalls[0].Function.Arguments) != `{"server":"web-01"}` || res.Message.ToolCalls[1].Function.Name != "list_alerts" {
		t.Fatalf("tool calls: %+v", res.Message.ToolCalls)
	}
	if res.PromptTokens != 120 || res.OutputTokens != 30 || res.DoneReason != "tool_calls" {
		t.Errorf("usage %d/%d reason %q", res.PromptTokens, res.OutputTokens, res.DoneReason)
	}
	// The history was converted with matching tool-call IDs.
	msgs := body["messages"].([]any)
	asst := msgs[2].(map[string]any)
	calls := asst["tool_calls"].([]any)
	id0 := calls[0].(map[string]any)["id"]
	if args := calls[1].(map[string]any)["function"].(map[string]any)["arguments"]; args != `{"server":"a"}` {
		t.Errorf("arguments must be a JSON string, got %#v", args)
	}
	if msgs[3].(map[string]any)["tool_call_id"] != id0 || msgs[4].(map[string]any)["tool_call_id"] != calls[1].(map[string]any)["id"] {
		t.Errorf("tool results not matched to calls: %v", msgs)
	}
	if body["stream"] != true || body["temperature"] != 0.2 || body["tools"] == nil {
		t.Errorf("request body %v", body)
	}
}

func tc(name, args string) ToolCall {
	var t ToolCall
	t.Function.Name, t.Function.Arguments = name, json.RawMessage(args)
	return t
}

func TestOpenAINonStreamingAndDiscovery(t *testing.T) {
	srv := fakeOpenAI(t, false, nil, nil)
	defer srv.Close()
	c, _ := NewOpenAI(srv.URL, "", "Colibri")
	res, err := c.Chat(context.Background(), ChatRequest{Model: "m", Messages: []Message{{Role: "user", Content: "x"}}}, nil)
	if err != nil || len(res.Message.ToolCalls) != 1 || string(res.Message.ToolCalls[0].Function.Arguments) != `{"all":true}` {
		t.Fatalf("non-streamed: %+v %v", res, err)
	}
	if v, err := c.Version(context.Background()); err != nil || v == "" {
		t.Fatalf("version: %q %v", v, err)
	}
	models, err := c.List(context.Background())
	if err != nil || len(models) != 2 || models[0].Name != "glm-5.2-colibri" {
		t.Fatalf("models: %+v %v", models, err)
	}
	sh, err := c.Show(context.Background(), "glm-5.2-colibri")
	if err != nil || !sh.Caps().Tools || sh.Caps().ContextLength != 131072 {
		t.Fatalf("show: %+v %v", sh, err)
	}
	if sh, _ := c.Show(context.Background(), "inkling-colibri"); sh.Caps().Tools {
		t.Error("a model without tool support was reported as supporting tools")
	}
	if err := c.Pull(context.Background(), "x", nil); err == nil || !strings.Contains(err.Error(), "coli") {
		t.Errorf("pull: %v", err)
	}
	if ps, err := c.PS(context.Background()); err != nil || len(ps) != 0 {
		t.Errorf("ps: %v %v", ps, err)
	}
}

func TestOpenAIErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(503)
			fmt.Fprint(w, `{"status":"restarting"}`)
		case "/v1/chat/completions":
			w.WriteHeader(429)
			fmt.Fprint(w, `{"error":{"message":"queue full","type":"rate_limit"}}`)
		case "/v1/models":
			w.WriteHeader(503)
		}
	}))
	defer srv.Close()
	c, _ := NewOpenAI(srv.URL, "", "Colibri")
	if _, err := c.Version(context.Background()); err == nil || !strings.Contains(err.Error(), "restarting") {
		t.Errorf("version while loading: %v", err)
	}
	if _, err := c.Chat(context.Background(), ChatRequest{Model: "m"}, nil); err == nil || !strings.Contains(err.Error(), "busy") {
		t.Errorf("busy: %v", err)
	}
}
