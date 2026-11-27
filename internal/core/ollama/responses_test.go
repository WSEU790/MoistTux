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

func sse(w http.ResponseWriter, events ...string) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, e := range events {
		var t struct{ Type string }
		_ = json.Unmarshal([]byte(e), &t)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", t.Type, e)
	}
}

func TestResponsesToolCallsAndHistory(t *testing.T) {
	var body map[string]any
	var auth []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = append(auth, r.Header.Get("Authorization"))
		switch r.URL.Path {
		case "/v1/models":
			fmt.Fprint(w, `{"models":[{"slug":"gpt-6.2-sol","display_name":"GPT-6.2 Sol","visibility":"list","context_window":400000},{"slug":"hidden","visibility":"hide"},{"slug":"gpt-6.1-mini","display_name":"GPT-6.1 mini","visibility":"list"}]}`)
		case "/v1/responses":
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, &body)
			sse(w,
				`{"type":"response.created","response":{"status":"in_progress"}}`,
				`{"type":"response.output_text.delta","delta":"Checking "}`,
				`{"type":"response.output_text.delta","delta":"the disk."}`,
				`{"type":"response.output_item.added","item":{"type":"function_call","call_id":"call_x","name":"get_disk_metrics","arguments":""}}`,
				`{"type":"response.function_call_arguments.delta","delta":"{\"server\":"}`,
				`{"type":"response.output_item.done","item":{"type":"function_call","id":"fc_1","call_id":"call_x","namespace":"nexus","name":"get_disk_metrics","arguments":"{\"server\":\"web-01\"}"}}`,
				`{"type":"response.output_item.done","item":{"type":"function_call","id":"fc_2","call_id":"call_y","name":"nexus.list_alerts","arguments":""}}`,
				`{"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":321,"output_tokens":45},"output":[{"type":"function_call","id":"fc_1","call_id":"call_x","name":"get_disk_metrics","arguments":"{\"server\":\"web-01\"}"}]}}`,
			)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	n := 0
	c, err := NewResponses(srv.URL, func(context.Context) (string, error) { n++; return fmt.Sprintf("tok-%d", n), nil }, nil)
	if err != nil {
		t.Fatal(err)
	}
	models, err := c.List(context.Background())
	if err != nil || len(models) != 2 || models[0].Name != "gpt-6.2-sol" || models[0].DisplayName != "GPT-6.2 Sol" || models[1].Name != "gpt-6.1-mini" {
		t.Fatalf("models (server order, listed only): %+v %v", models, err)
	}
	sh, err := c.Show(context.Background(), "gpt-6.2-sol")
	if err != nil || !sh.Caps().Tools || sh.Caps().ContextLength != 400000 {
		t.Fatalf("show %+v %v", sh, err)
	}
	if v, err := c.Version(context.Background()); err != nil || v == "" {
		t.Fatalf("version %q %v", v, err)
	}
	if err := c.Pull(context.Background(), "x", nil); err == nil {
		t.Error("pull should be refused")
	}

	temp := 0.2
	var streamed strings.Builder
	res, err := c.Chat(context.Background(), ChatRequest{
		Model: "gpt-6.2-sol",
		Messages: []Message{
			{Role: "system", Content: "You are NEXUS."},
			{Role: "user", Content: "is the disk ok?"},
			{Role: "assistant", Content: "Looking.", ToolCalls: []ToolCall{tc("list_servers", ``), tc("get_cpu_metrics", `{"server":"a"}`)}},
			{Role: "tool", ToolName: "list_servers", Content: "[]"},
			{Role: "tool", ToolName: "get_cpu_metrics", Content: "{}"},
			{Role: "system", Content: "Policy reminder."},
		},
		Tools:   []Tool{{Type: "function", Function: ToolFunction{Name: "get_disk_metrics", Description: "disk", Parameters: map[string]any{"type": "object"}}}},
		Options: &Options{Temperature: &temp, NumPredict: 100},
	}, func(c, _ string) { streamed.WriteString(c) })
	if err != nil {
		t.Fatal(err)
	}
	if res.Message.Content != "Checking the disk." || streamed.String() != res.Message.Content {
		t.Errorf("content %q", res.Message.Content)
	}
	if len(res.Message.ToolCalls) != 2 || res.Message.ToolCalls[0].Function.Name != "get_disk_metrics" || string(res.Message.ToolCalls[0].Function.Arguments) != `{"server":"web-01"}` ||
		res.Message.ToolCalls[1].Function.Name != "list_alerts" || string(res.Message.ToolCalls[1].Function.Arguments) != `{}` {
		t.Fatalf("tool calls (deduplicated, namespace stripped): %+v", res.Message.ToolCalls)
	}
	if res.PromptTokens != 321 || res.OutputTokens != 45 || res.DoneReason != "tool_calls" {
		t.Errorf("usage %d/%d %q", res.PromptTokens, res.OutputTokens, res.DoneReason)
	}
	if !strings.HasPrefix(auth[len(auth)-1], "Bearer tok-") {
		t.Errorf("auth %v", auth)
	}

	// The request follows the plan-usage rules.
	if body["store"] != false || body["stream"] != true || body["instructions"] != "You are NEXUS.\n\nPolicy reminder." {
		t.Errorf("body flags: %v", body)
	}
	for _, k := range []string{"temperature", "top_p", "max_output_tokens", "previous_response_id", "messages"} {
		if _, ok := body[k]; ok {
			t.Errorf("unsupported field %q sent", k)
		}
	}
	tools := body["tools"].([]any)
	nsTool := tools[0].(map[string]any)
	if nsTool["type"] != "namespace" || nsTool["name"] != "nexus" || nsTool["tools"].([]any)[0].(map[string]any)["name"] != "get_disk_metrics" {
		t.Errorf("tools: %v", tools)
	}
	in := body["input"].([]any)
	if len(in) != 6 {
		t.Fatalf("input items: %v", in)
	}
	for _, it := range in {
		if it.(map[string]any)["role"] == "system" {
			t.Error("a system item was sent")
		}
	}
	call0, call1 := in[2].(map[string]any), in[3].(map[string]any)
	out0, out1 := in[4].(map[string]any), in[5].(map[string]any)
	if call0["type"] != "function_call" || call0["arguments"] != "{}" || in[1].(map[string]any)["content"] != "Looking." {
		t.Errorf("history: %v", in)
	}
	if out0["call_id"] != call0["call_id"] || out1["call_id"] != call1["call_id"] || out0["output"] != "[]" {
		t.Errorf("results not matched to calls: %v", in)
	}

	// The model's calls carried a namespace, so history now echoes it.
	_, in2 := c.rsInput([]Message{{Role: "assistant", ToolCalls: []ToolCall{tc("df", `{}`)}}, {Role: "tool", Content: "ok"}})
	if in2[0]["namespace"] != "nexus" || in2[1]["call_id"] != in2[0]["call_id"] || in2[1]["type"] != "function_call_output" {
		t.Errorf("namespace echo / call ids: %v", in2)
	}
}

func TestResponsesHistoryCallIDs(t *testing.T) {
	c, _ := NewResponses("http://127.0.0.1:1", func(context.Context) (string, error) { return "", nil }, nil)
	_, in := c.rsInput([]Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", ToolCalls: []ToolCall{tc("a", `{}`), tc("b", `{}`)}},
		{Role: "tool", ToolName: "a", Content: "A"},
		{Role: "tool", ToolName: "b", Content: "B"},
	})
	if len(in) != 5 || in[3]["call_id"] != in[1]["call_id"] || in[4]["call_id"] != in[2]["call_id"] || in[1]["call_id"] == in[2]["call_id"] {
		t.Fatalf("ids not matched: %v", in)
	}
	if in[3]["output"] != "A" || in[4]["output"] != "B" {
		t.Errorf("outputs %v", in)
	}
}

func TestResponsesErrors(t *testing.T) {
	limited := 0
	mode := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch mode {
		case "limit-http":
			w.WriteHeader(429)
			fmt.Fprint(w, `{"error":{"code":"subscription_sharing_usage_limit_exceeded","message":"limit"}}`)
		case "limit-stream":
			sse(w, `{"type":"response.output_text.delta","delta":"par"}`, `{"type":"response.failed","response":{"status":"failed","error":{"code":"subscription_sharing_usage_limit_exceeded","message":"limit"}}}`)
		case "ineligible":
			w.WriteHeader(403)
			fmt.Fprint(w, `{"error":{"code":"subscription_sharing_user_not_eligible","message":"no"}}`)
		case "unsupported":
			w.WriteHeader(400)
			fmt.Fprint(w, `{"error":{"code":"subscription_sharing_unsupported_capability","param":"tools[0]","message":"bad tool"}}`)
		case "detail":
			w.WriteHeader(503)
			fmt.Fprint(w, `{"detail":"direct routing unavailable"}`)
		case "cut":
			sse(w, `{"type":"response.output_text.delta","delta":"half"}`)
		case "incomplete":
			sse(w, `{"type":"response.incomplete","response":{"status":"incomplete","incomplete_details":{"reason":"max_output_tokens"}}}`)
		}
	}))
	defer srv.Close()
	c, _ := NewResponses(srv.URL, func(context.Context) (string, error) { return "t", nil }, func() { limited++ })
	chat := func() error {
		_, err := c.Chat(context.Background(), ChatRequest{Model: "m", Messages: []Message{{Role: "user", Content: "x"}}}, nil)
		return err
	}
	for m, want := range map[string]string{
		"limit-http": "chatgpt.com/settings/usage", "limit-stream": "usage limit", "ineligible": "not available for this account",
		"unsupported": "tools[0]", "detail": "direct routing unavailable", "cut": "ended before", "incomplete": "max_output_tokens",
	} {
		mode = m
		if err := chat(); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v (want %q)", m, err, want)
		}
	}
	if limited != 2 {
		t.Errorf("usage-limit callback ran %d times", limited)
	}
	// A missing sign-in surfaces before any request is made.
	c2, _ := NewResponses(srv.URL, func(context.Context) (string, error) { return "", fmt.Errorf("no ChatGPT account is linked") }, nil)
	if _, err := c2.Version(context.Background()); err == nil || !strings.Contains(err.Error(), "linked") {
		t.Errorf("not linked: %v", err)
	}
}
