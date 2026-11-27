//go:build linux

package integration

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/WSEU790/MoistTux/internal/core/chatgpt"
)

// fakeOpenAI stands in for auth.openai.com and api.openai.com: OAuth with
// PKCE and an RS256 ID token, then a Responses API that asks for one NEXUS
// tool and answers once it receives the tool's output.
type fakeOpenAI struct {
	srv  *httptest.Server
	key  *rsa.PrivateKey
	mu   sync.Mutex
	pend map[string][2]string // code -> challenge, nonce
	reqs []map[string]any
	auth []string
}

func newFakeOpenAI(t *testing.T) *fakeOpenAI {
	k, _ := rsa.GenerateKey(rand.Reader, 2048)
	f := &fakeOpenAI{key: k, pend: map[string][2]string{}}
	m := http.NewServeMux()
	m.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{"kty": "RSA", "kid": "a", "n": base64.RawURLEncoding.EncodeToString(k.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(k.E)).Bytes())}}})
	})
	m.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		f.mu.Lock()
		p, ok := f.pend[r.Form.Get("code")]
		f.mu.Unlock()
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		if !ok || base64.RawURLEncoding.EncodeToString(sum[:]) != p[0] || r.Form.Get("client_id") != "oaiapp_e2e" {
			w.WriteHeader(400)
			io.WriteString(w, `{"error":"invalid_grant"}`)
			return
		}
		h, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "a"})
		c, _ := json.Marshal(map[string]any{"iss": f.srv.URL, "aud": "oaiapp_e2e", "sub": "s1", "email": "admin@example.com", "nonce": p[1], "exp": time.Now().Add(time.Hour).Unix()})
		in := base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(c)
		d := sha256.Sum256([]byte(in))
		sig, _ := rsa.SignPKCS1v15(rand.Reader, k, crypto.SHA256, d[:])
		json.NewEncoder(w).Encode(map[string]any{"access_token": "access-e2e", "refresh_token": "refresh-e2e", "id_token": in + "." + base64.RawURLEncoding.EncodeToString(sig), "expires_in": 3600, "scope": chatgpt.Scopes})
	})
	m.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"models":[{"slug":"gpt-e2e","display_name":"GPT e2e","visibility":"list"}]}`)
	})
	m.HandleFunc("/v1/responses", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		json.NewDecoder(r.Body).Decode(&req)
		f.mu.Lock()
		f.reqs = append(f.reqs, req)
		f.auth = append(f.auth, r.Header.Get("Authorization"))
		f.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		send := func(ev string) { fmt.Fprintf(w, "data: %s\n\n", ev) }
		in, _ := req["input"].([]any)
		last, _ := in[len(in)-1].(map[string]any)
		if last["type"] == "function_call_output" {
			out, _ := last["output"].(string)
			send(`{"type":"response.output_text.delta","delta":"FACT: list_servers returned "}`)
			b, _ := json.Marshal(map[string]any{"type": "response.output_text.delta", "delta": fmt.Sprintf("%d bytes.", len(out))})
			send(string(b))
		} else {
			send(`{"type":"response.output_item.done","item":{"type":"function_call","id":"fc1","call_id":"call_real","namespace":"nexus","name":"list_servers","arguments":"{}"}}`)
		}
		send(`{"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":100,"output_tokens":10}}}`)
	})
	f.srv = httptest.NewServer(m)
	return f
}

func TestChatGPTPlanEngine(t *testing.T) {
	c := startCore(t, t.TempDir())
	defer c.app.Close()
	f := newFakeOpenAI(t)
	defer f.srv.Close()
	c.app.ChatGPT.SetEndpoints(chatgpt.Endpoints{Authorize: f.srv.URL + "/authorize", Token: f.srv.URL + "/token", JWKS: f.srv.URL + "/jwks", Discovery: f.srv.URL + "/none", Issuer: f.srv.URL, API: f.srv.URL})
	var opened string
	c.app.ChatGPT.OpenBrowser = func(u string) { opened = u }

	// Sign in through the API; the "browser" approves and follows the redirect.
	var start struct {
		AuthURL string `json:"auth_url"`
		Opened  bool   `json:"opened"`
	}
	if st, body := c.do("POST", "/api/ai/chatgpt/signin", map[string]any{}, &start); st != 200 || !start.Opened || opened != start.AuthURL {
		t.Fatalf("signin: %d %s", st, body)
	}
	u, _ := url.Parse(start.AuthURL)
	q := u.Query()
	f.mu.Lock()
	f.pend["code1"] = [2]string{q.Get("code_challenge"), q.Get("nonce")}
	f.mu.Unlock()
	resp, err := http.Get(q.Get("redirect_uri") + "?" + url.Values{"code": {"code1"}, "state": {q.Get("state")}, "client_id": {"oaiapp_e2e"}}.Encode())
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	var st chatgpt.Status
	eventually(t, 10*time.Second, "linked", func() bool { c.do("GET", "/api/ai/chatgpt", nil, &st); return st.Flow == "done" })
	if !st.Linked || !st.PlanEnabled || st.Email != "admin@example.com" {
		t.Fatalf("status: %+v", st)
	}
	if _, body := c.do("GET", "/api/ai/chatgpt", nil, nil); strings.Contains(body, "access-e2e") || strings.Contains(body, "refresh-e2e") {
		t.Fatal("the API exposed tokens")
	}

	// Switch the engine and check status and models.
	settings := map[string]any{}
	c.do("GET", "/api/settings/ai", nil, &settings)
	settings["provider"] = "chatgpt"
	if code, body := c.do("PUT", "/api/settings/ai", settings, nil); code != 200 {
		t.Fatalf("settings: %d %s", code, body)
	}
	var ai map[string]any
	c.do("GET", "/api/ai/status", nil, &ai)
	if ai["reachable"] != true || ai["engine"] != "ChatGPT" || ai["local"] != false {
		t.Fatalf("ai status: %v", ai)
	}

	// A conversation runs a real NEXUS tool through ChatGPT.
	var conv map[string]any
	if code, body := c.do("POST", "/api/conversations", map[string]any{"mode": "analyst"}, &conv); code != 201 {
		t.Fatalf("conversation: %d %s", code, body)
	}
	id := conv["id"].(string)
	if code, body := c.do("POST", "/api/conversations/"+id+"/messages", map[string]any{"content": "which servers do I have?"}, nil); code != 202 {
		t.Fatalf("send: %d %s", code, body)
	}
	answer := func() string {
		var out struct{ Messages []map[string]any }
		c.do("GET", "/api/conversations/"+id, nil, &out)
		for i := len(out.Messages) - 1; i >= 0; i-- {
			if m := out.Messages[i]; m["role"] == "assistant" && m["content"] != "" {
				return m["content"].(string)
			}
		}
		return ""
	}
	eventually(t, 30*time.Second, "answer", func() bool { return strings.Contains(answer(), "list_servers returned") })

	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.reqs) != 2 {
		t.Fatalf("%d Responses requests", len(f.reqs))
	}
	for _, a := range f.auth {
		if a != "Bearer access-e2e" {
			t.Errorf("authorization %q", a)
		}
	}
	first, second := f.reqs[0], f.reqs[1]
	if first["store"] != false || first["stream"] != true || first["model"] != "gpt-e2e" || !strings.Contains(fmt.Sprint(first["instructions"]), "NEXUS") {
		t.Errorf("request: store=%v stream=%v model=%v", first["store"], first["stream"], first["model"])
	}
	if _, bad := first["temperature"]; bad {
		t.Error("temperature sent")
	}
	tools := first["tools"].([]any)[0].(map[string]any)
	if tools["type"] != "namespace" || len(tools["tools"].([]any)) < 20 {
		t.Errorf("tools: type %v, %d functions", tools["type"], len(tools["tools"].([]any)))
	}
	in := second["input"].([]any)
	call, out := in[len(in)-2].(map[string]any), in[len(in)-1].(map[string]any)
	if call["type"] != "function_call" || call["name"] != "list_servers" || call["namespace"] != "nexus" || out["call_id"] != call["call_id"] || !strings.HasPrefix(out["output"].(string), "[") && !strings.HasPrefix(out["output"].(string), "{") {
		t.Fatalf("tool round trip: call %v output %v", call, out)
	}
}
