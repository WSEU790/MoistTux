//go:build linux

// Package integration runs the real agent and the real core in-process on
// this Linux host and drives them through the core's HTTP API. Telemetry,
// processes, Docker and packages are real. The only simulated component is
// Ollama, replaced by a scripted server that speaks the documented
// /api/chat protocol — a test fixture that never ships in production.
package integration

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/WSEU790/MoistTux/internal/agent"
	"github.com/WSEU790/MoistTux/internal/core/app"
	"github.com/WSEU790/MoistTux/internal/core/fleet"
	"github.com/WSEU790/MoistTux/internal/pki"
)

// ---------------------------------------------------------------- harness

type testAgent struct {
	a      *agent.Agent
	addr   *net.TCPAddr
	cancel context.CancelFunc
	done   chan struct{}
	dir    string
}

func startAgent(t *testing.T, dir string, port int) *testAgent {
	t.Helper()
	a, err := agent.New(agent.Config{StateDir: dir, Interval: time.Second, Logger: log.New(io.Discard, "", 0)})
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	ta := &testAgent{a: a, addr: ln.Addr().(*net.TCPAddr), cancel: cancel, done: make(chan struct{}), dir: dir}
	go func() { _ = a.ServeListener(ctx, ln); close(ta.done) }()
	return ta
}

func (ta *testAgent) stop() { ta.cancel(); <-ta.done }

type core struct {
	t   *testing.T
	app *app.App
	url string
	hc  *http.Client
}

func startCore(t *testing.T, dir string) *core {
	t.Helper()
	a, err := app.Start(context.Background(), app.Options{DataDir: dir, Listen: "127.0.0.1:0", LogOutput: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	cfg := a.Fleet.Config()
	cfg.PollInterval, cfg.HealthInterval, cfg.ServicesInterval, cfg.DockerInterval = 300*time.Millisecond, 300*time.Millisecond, time.Second, time.Second
	cfg.OfflineAfter = 2
	a.Fleet.SetConfig(cfg)
	return &core{t: t, app: a, url: "http://" + a.Addr, hc: &http.Client{Timeout: 3 * time.Minute}}
}

func (c *core) do(method, path string, body, out any) (int, string) {
	c.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, c.url+path, rd)
	req.Header.Set("Authorization", "Bearer "+c.app.Token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		c.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if out != nil && resp.StatusCode < 300 {
		if err := json.Unmarshal(raw, out); err != nil {
			c.t.Fatalf("%s %s: decode %v: %s", method, path, err, raw)
		}
	}
	return resp.StatusCode, string(raw)
}

func eventually(t *testing.T, d time.Duration, what string, f func() bool) {
	t.Helper()
	eventuallyOr(t, d, what, f, nil)
}

// eventuallyOr is eventually with a description of the state on timeout, so
// a CI failure says where things stopped instead of only that they did.
func eventuallyOr(t *testing.T, d time.Duration, what string, f func() bool, state func() string) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		time.Sleep(150 * time.Millisecond)
	}
	if state != nil {
		t.Fatalf("timed out waiting for %s; state: %s", what, state())
	}
	t.Fatalf("timed out waiting for %s", what)
}

// probeSudo reports how the privileged helper behaves through sudo for a
// non-root test run: how long `sudo -n <test binary> helper ping` takes and
// what it prints, which separates sudo problems from slow operations.
func probeSudo(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		return
	}
	ver, _ := exec.Command("sudo", "--version").CombinedOutput()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	start := time.Now()
	cmd := exec.CommandContext(ctx, "sudo", "-n", self, "helper", "ping")
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	err = cmd.Run()
	first, _, _ := strings.Cut(string(ver), "\n")
	t.Logf("sudo probe: %s; helper ping took %s, err=%v, stdout=%q, stderr=%.200q", first, time.Since(start).Round(time.Millisecond), err, strings.TrimSpace(so.String()), se.String())
}

// ---------------------------------------------------------------- fake Ollama (test fixture)

type fakeOllama struct {
	mu       sync.Mutex
	requests []map[string]any
}

func (f *fakeOllama) handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("/api/version", func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, `{"version":"0.0.0-test"}`) })
	m.HandleFunc("/api/tags", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"models":[{"name":"scripted-tools:latest","model":"scripted-tools:latest","size":1000000,"details":{"family":"test","parameter_size":"7B","quantization_level":"Q4_K_M"}}]}`)
	})
	m.HandleFunc("/api/ps", func(w http.ResponseWriter, _ *http.Request) { io.WriteString(w, `{"models":[]}`) })
	m.HandleFunc("/api/show", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"capabilities":["completion","tools"],"model_info":{"test.context_length":32768}}`)
	})
	m.HandleFunc("/api/chat", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		f.mu.Lock()
		f.requests = append(f.requests, req)
		f.mu.Unlock()
		msgs, _ := req["messages"].([]any)
		last, _ := msgs[len(msgs)-1].(map[string]any)
		role, _ := last["role"].(string)
		content, _ := last["content"].(string)
		enc := json.NewEncoder(w)
		call := func(name string, args map[string]any) {
			_ = enc.Encode(map[string]any{"model": "scripted-tools:latest", "message": map[string]any{"role": "assistant", "content": "", "tool_calls": []any{map[string]any{"function": map[string]any{"name": name, "arguments": args}}}}, "done": false})
			_ = enc.Encode(map[string]any{"model": "scripted-tools:latest", "message": map[string]any{"role": "assistant", "content": ""}, "done": true, "done_reason": "stop", "eval_count": 5, "eval_duration": 1e8})
		}
		answer := func(text string) {
			for _, part := range strings.SplitAfter(text, " ") {
				_ = enc.Encode(map[string]any{"model": "scripted-tools:latest", "message": map[string]any{"role": "assistant", "content": part}, "done": false})
			}
			_ = enc.Encode(map[string]any{"model": "scripted-tools:latest", "message": map[string]any{"role": "assistant", "content": ""}, "done": true, "done_reason": "stop", "eval_count": 20, "eval_duration": 2e8})
		}
		switch {
		case role == "tool":
			answer("**Investigated** with " + fmt.Sprint(last["tool_name"]) + ". FACT: result received (" + fmt.Sprint(len(content)) + " bytes).")
		case strings.Contains(content, "metrics"):
			call("get_system_metrics", map[string]any{})
		case strings.Contains(content, "refresh"):
			call("refresh_package_lists", map[string]any{})
		case strings.Contains(content, "reboot"):
			call("reboot_server", map[string]any{"reason": "kernel update"})
		case strings.Contains(content, "inject"):
			call("restart_service", map[string]any{"unit": "nginx; rm -rf /"})
		default:
			answer("Hello from the scripted test model.")
		}
	})
	return m
}

func (f *fakeOllama) lastRequest() map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.requests) == 0 {
		return nil
	}
	return f.requests[len(f.requests)-1]
}

// ---------------------------------------------------------------- tests

func TestEndToEnd(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Log("not root: privileged helper calls go through sudo -n and fail where it needs a password (expected)")
		probeSudo(t)
	}
	agentDir := t.TempDir()
	ag := startAgent(t, agentDir, 0)
	defer func() { ag.stop() }()
	coreDir := t.TempDir()
	c := startCore(t, coreDir)
	defer func() { c.app.Close() }()

	code, err := ag.a.State().NewEnrollmentCode(time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("UnauthenticatedClientsRejected", func(t *testing.T) {
		// No client certificate: the TLS handshake itself must fail.
		tr := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
		_, err := (&http.Client{Transport: tr, Timeout: 5 * time.Second}).Get(fmt.Sprintf("https://%s/v1/health", ag.addr))
		if err == nil {
			t.Fatal("agent accepted a client without a certificate")
		}
		// A certificate that is not enrolled is rejected with 401.
		id, _ := pki.NewIdentity("intruder", nil, time.Hour, false, true)
		cert, _ := id.TLSCertificate()
		tr2 := &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true, Certificates: []tls.Certificate{cert}}}
		resp, err := (&http.Client{Transport: tr2, Timeout: 5 * time.Second}).Get(fmt.Sprintf("https://%s/v1/health", ag.addr))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 401 {
			t.Fatalf("unenrolled certificate got %d", resp.StatusCode)
		}
		// The API refuses requests without the session token.
		r, _ := http.Get(c.url + "/api/servers")
		if r.StatusCode != 401 {
			t.Fatalf("API without token: %d", r.StatusCode)
		}
		r.Body.Close()
		// And requests whose Host header is not the loopback origin (DNS rebinding).
		req, _ := http.NewRequest("GET", c.url+"/api/servers", nil)
		req.Host = "evil.example:80"
		req.Header.Set("Authorization", "Bearer "+c.app.Token)
		r2, _ := http.DefaultClient.Do(req)
		if r2.StatusCode != 403 {
			t.Fatalf("foreign Host header: %d", r2.StatusCode)
		}
		r2.Body.Close()
	})

	t.Run("EnrollmentRejectsWrongFingerprint", func(t *testing.T) {
		other := startAgent(t, t.TempDir(), 0)
		defer other.stop()
		// Code from agent A presented to agent B: B's certificate does not match.
		st, body := c.do("POST", "/api/servers", fleet.EnrollRequest{Address: "127.0.0.1", Port: other.addr.Port, Code: code}, nil)
		if st < 400 || !strings.Contains(body, "fingerprint") {
			t.Fatalf("mismatched enrollment: %d %s", st, body)
		}
	})

	var srvID string
	t.Run("Enroll", func(t *testing.T) {
		var out map[string]any
		st, body := c.do("POST", "/api/servers", fleet.EnrollRequest{Name: "it-host", Address: "127.0.0.1", Port: ag.addr.Port, Code: code, Environment: "lab"}, &out)
		if st != 201 {
			t.Fatalf("enroll: %d %s", st, body)
		}
		srvID = out["id"].(string)
		// The token is single use.
		st, _ = c.do("POST", "/api/servers", fleet.EnrollRequest{Name: "again", Address: "localhost", Port: ag.addr.Port, Code: code}, nil)
		if st < 400 {
			t.Fatal("enrollment code was accepted twice")
		}
	})

	t.Run("RealTelemetryFlows", func(t *testing.T) {
		eventually(t, 20*time.Second, "telemetry", func() bool {
			var servers []map[string]any
			c.do("GET", "/api/servers", nil, &servers)
			if len(servers) != 1 || servers[0]["status"] != "online" || servers[0]["latest"] == nil {
				return false
			}
			l := servers[0]["latest"].(map[string]any)
			mem := l["mem"].(map[string]any)
			return mem["total"].(float64) > 0
		})
		eventually(t, 20*time.Second, "stored telemetry", func() bool {
			var tel struct{ Points []map[string]any }
			c.do("GET", "/api/servers/"+srvID+"/telemetry?range=15m", nil, &tel)
			return len(tel.Points) >= 2
		})
		var procs struct {
			Processes []map[string]any
			Total     int
		}
		if st, body := c.do("GET", "/api/servers/"+srvID+"/agent/processes?limit=5", nil, &procs); st != 200 || procs.Total == 0 {
			t.Fatalf("processes: %d %s", st, body)
		}
		// Only allowlisted read paths are proxied.
		if st, _ := c.do("GET", "/api/servers/"+srvID+"/agent/system/reboot", nil, nil); st != 404 {
			t.Fatalf("proxy allowed a non-allowlisted path: %d", st)
		}
	})

	t.Run("DockerInventoryWhenAvailable", func(t *testing.T) {
		if _, err := os.Stat("/var/run/docker.sock"); err != nil {
			t.Skip("no Docker engine on this host")
		}
		if conn, err := net.Dial("unix", "/var/run/docker.sock"); err != nil {
			t.Skipf("the Docker socket is not usable by this user (%v); the agent reports that as unavailable", err)
		} else {
			conn.Close()
		}
		var info map[string]any
		c.do("GET", "/api/servers/"+srvID+"/agent/docker", nil, &info)
		if info["available"] != true {
			t.Fatalf("docker info: %v", info)
		}
	})

	fo := &fakeOllama{}
	ollamaSrv := httptest.NewServer(fo.handler())
	defer ollamaSrv.Close()
	settings := map[string]any{}
	c.do("GET", "/api/settings/ai", nil, &settings)
	settings["ollama_url"] = ollamaSrv.URL
	if st, body := c.do("PUT", "/api/settings/ai", settings, nil); st != 200 {
		t.Fatalf("ai settings: %d %s", st, body)
	}

	newConv := func(mode string) string {
		var conv map[string]any
		if st, body := c.do("POST", "/api/conversations", map[string]any{"mode": mode, "server_id": srvID}, &conv); st != 201 {
			t.Fatalf("conversation: %d %s", st, body)
		}
		return conv["id"].(string)
	}
	thread := func(id string) (msgs []map[string]any, exes []map[string]any) {
		var out struct {
			Messages   []map[string]any
			Executions []map[string]any
		}
		c.do("GET", "/api/conversations/"+id, nil, &out)
		return out.Messages, out.Executions
	}
	finalAnswer := func(id string) string {
		msgs, _ := thread(id)
		for i := len(msgs) - 1; i >= 0; i-- {
			if msgs[i]["role"] == "assistant" && msgs[i]["content"] != "" {
				return msgs[i]["content"].(string)
			}
		}
		return ""
	}

	t.Run("AIInvestigatesWithRealTools", func(t *testing.T) {
		id := newConv("analyst")
		if st, body := c.do("POST", "/api/conversations/"+id+"/messages", map[string]any{"content": "check system metrics"}, nil); st != 202 {
			t.Fatalf("send: %d %s", st, body)
		}
		eventually(t, 30*time.Second, "AI answer", func() bool { return strings.Contains(finalAnswer(id), "get_system_metrics") })
		req := fo.lastRequest()
		if tools, _ := req["tools"].([]any); len(tools) < 20 {
			t.Fatalf("model received %d tools", len(tools))
		}
		msgs, _ := req["messages"].([]any)
		sys := msgs[0].(map[string]any)["content"].(string)
		if !strings.Contains(sys, "it-host") || !strings.Contains(sys, "FOCUSED SERVER") {
			t.Fatal("system prompt lacks infrastructure context")
		}
		var toolMsg map[string]any
		for _, m := range msgs {
			if mm := m.(map[string]any); mm["role"] == "tool" {
				toolMsg = mm
			}
		}
		if toolMsg == nil || !strings.Contains(toolMsg["content"].(string), "\"memory\"") {
			t.Fatalf("real tool result not passed to the model: %v", toolMsg)
		}
		_, exes := thread(id)
		if len(exes) != 1 || exes[0]["status"] != "succeeded" || exes[0]["decision"] != "allow" {
			t.Fatalf("execution record: %v", exes)
		}
	})

	t.Run("ReadOnlyModesBlockChanges", func(t *testing.T) {
		id := newConv("analyst")
		c.do("POST", "/api/conversations/"+id+"/messages", map[string]any{"content": "please refresh package lists"}, nil)
		eventually(t, 30*time.Second, "blocked answer", func() bool { return finalAnswer(id) != "" })
		_, exes := thread(id)
		if len(exes) != 1 || exes[0]["status"] != "blocked" || exes[0]["decision"] != "deny" {
			t.Fatalf("analyst mode did not block: %v", exes)
		}
	})

	t.Run("InvalidArgumentsNeverReachTheAgent", func(t *testing.T) {
		id := newConv("administrator")
		c.do("POST", "/api/conversations/"+id+"/messages", map[string]any{"content": "inject"}, nil)
		eventually(t, 30*time.Second, "answer", func() bool { return finalAnswer(id) != "" })
		msgs, exes := thread(id)
		if len(exes) != 0 {
			t.Fatalf("an execution was recorded for invalid arguments: %v", exes)
		}
		found := false
		for _, m := range msgs {
			if m["role"] == "tool" && strings.Contains(m["content"].(string), "not allowed") {
				found = true
			}
		}
		if !found {
			t.Fatal("validation error was not returned to the model")
		}
	})

	t.Run("HighRiskDeniedByDefaultPolicy", func(t *testing.T) {
		id := newConv("administrator")
		c.do("POST", "/api/conversations/"+id+"/messages", map[string]any{"content": "reboot it"}, nil)
		eventually(t, 30*time.Second, "answer", func() bool { return finalAnswer(id) != "" })
		_, exes := thread(id)
		if len(exes) != 1 || exes[0]["tool"] != "reboot_server" || exes[0]["status"] != "blocked" {
			t.Fatalf("reboot was not blocked: %v", exes)
		}
	})

	t.Run("ApprovalWorkflow", func(t *testing.T) {
		var health map[string]any
		c.do("GET", "/api/servers/"+srvID+"/agent/health", nil, &health)
		caps := fmt.Sprint(health["capabilities"])
		if !strings.Contains(caps, "packages:apt") {
			t.Skip("approval workflow test uses refresh_package_lists, which needs APT")
		}
		id := newConv("administrator")
		c.do("POST", "/api/conversations/"+id+"/messages", map[string]any{"content": "refresh the package lists"}, nil)
		var approvals []map[string]any
		eventually(t, 30*time.Second, "approval request", func() bool {
			approvals = nil
			c.do("GET", "/api/approvals?status=pending", nil, &approvals)
			return len(approvals) == 1
		})
		ap := approvals[0]
		if ap["tool"] != "refresh_package_lists" || ap["impact"] == nil {
			t.Fatalf("approval: %v", ap)
		}
		// The conversation cannot continue while the approval is pending.
		if st, _ := c.do("POST", "/api/conversations/"+id+"/messages", map[string]any{"content": "hello"}, nil); st != 400 {
			t.Fatalf("message accepted while approval pending: %d", st)
		}
		if st, body := c.do("POST", "/api/approvals/"+ap["id"].(string)+"/decide", map[string]any{"approve": true, "reason": "integration test"}, nil); st != 200 {
			t.Fatalf("decide: %d %s", st, body)
		}
		// A decided approval cannot be decided again.
		if st, _ := c.do("POST", "/api/approvals/"+ap["id"].(string)+"/decide", map[string]any{"approve": false}, nil); st == 200 {
			t.Fatal("approval decided twice")
		}
		// apt-get update talks to the host's real repositories, which can be
		// slow on CI runners. The operation is bounded by the tool timeout
		// (11 min); either outcome must reach the conversation.
		state := func() string {
			msgs, exes := thread(id)
			var b strings.Builder
			for _, e := range exes {
				fmt.Fprintf(&b, "execution %v status=%v decision=%v error=%v; ", e["tool"], e["status"], e["decision"], e["error"])
			}
			for i := max(0, len(msgs)-4); i < len(msgs); i++ {
				fmt.Fprintf(&b, "[%v] %.160v; ", msgs[i]["role"], msgs[i]["content"])
			}
			out, _ := exec.Command("ps", "-eo", "pid,etime,args", "--forest").Output()
			for _, l := range strings.Split(string(out), "\n") {
				if strings.Contains(l, "helper") || strings.Contains(l, "apt") || strings.Contains(l, "sudo") {
					fmt.Fprintf(&b, "\n  ps: %.200s", l)
				}
			}
			return b.String()
		}
		approvedAt, slowLogged := time.Now(), false
		eventuallyOr(t, 12*time.Minute, "execution after approval and resumed answer", func() bool {
			_, exes := thread(id)
			done := len(exes) == 1 && (exes[0]["status"] == "succeeded" || exes[0]["status"] == "failed") && strings.Contains(finalAnswer(id), "refresh_package_lists")
			if !done && !slowLogged && time.Since(approvedAt) > 90*time.Second {
				slowLogged = true
				t.Logf("approved operation still running after 90s: %s", state())
			}
			return done
		}, state)
		t.Logf("approved operation and resumed answer took %s", time.Since(approvedAt).Round(time.Millisecond))
		_, exes := thread(id)
		res, _ := json.Marshal(exes[0]["result"])
		t.Logf("approved operation finished with status %v: %.300s", exes[0]["status"], res)
		if exes[0]["requested_by"] != "ai" || exes[0]["decision"] != "approve" {
			t.Fatalf("execution provenance: %v", exes[0])
		}
		var audit []map[string]any
		c.do("GET", "/api/audit?action=approval.", nil, &audit)
		if len(audit) == 0 {
			t.Fatal("approval decision missing from audit log")
		}
		var v map[string]any
		c.do("GET", "/api/audit/verify", nil, &v)
		if v["intact"] != true {
			t.Fatalf("audit chain: %v", v)
		}
	})

	t.Run("AgentDisconnectAndRecovery", func(t *testing.T) {
		port := ag.addr.Port
		ag.stop()
		eventually(t, 20*time.Second, "offline status and alert", func() bool {
			var s map[string]any
			c.do("GET", "/api/servers/"+srvID, nil, &s)
			var al []map[string]any
			c.do("GET", "/api/alerts?status=firing", nil, &al)
			hasAlert := false
			for _, a := range al {
				if a["rule"] == "agent_offline" {
					hasAlert = true
				}
			}
			return s["status"] == "offline" && hasAlert
		})
		ag = startAgent(t, agentDir, port) // same identity, same port
		eventually(t, 70*time.Second, "back online", func() bool {
			var s map[string]any
			c.do("GET", "/api/servers/"+srvID, nil, &s)
			var al []map[string]any
			c.do("GET", "/api/alerts?status=firing", nil, &al)
			for _, a := range al {
				if a["rule"] == "agent_offline" {
					return false
				}
			}
			return s["status"] == "online"
		})
	})

	t.Run("RecoveryAfterCoreRestart", func(t *testing.T) {
		// Simulate a crash during an operation, then restart the core.
		_, err := c.app.Store.DB().Exec(`INSERT INTO executions(id, server_id, tool, args, risk, requested_by, mode, decision, status, created_at, started_at) VALUES ('exe_crash', ?, 'restart_service', '{}', 'medium', 'ai', 'administrator', 'allow', 'running', ?, ?)`, srvID, time.Now().UnixMilli(), time.Now().UnixMilli())
		if err != nil {
			t.Fatal(err)
		}
		c.app.Close()
		restarted := time.Now()
		c = startCore(t, coreDir)
		var exes []map[string]any
		c.do("GET", "/api/executions?limit=500", nil, &exes)
		found := false
		for _, e := range exes {
			if e["id"] == "exe_crash" {
				found = true
				if e["status"] != "failed" || !strings.Contains(e["error"].(string), "outcome unknown") {
					t.Fatalf("interrupted execution not recovered: %v", e)
				}
			}
		}
		if !found {
			t.Fatal("interrupted execution missing after restart")
		}
		eventually(t, 20*time.Second, "server polled again after restart", func() bool {
			var s map[string]any
			c.do("GET", "/api/servers/"+srvID, nil, &s)
			seen, _ := time.Parse(time.RFC3339Nano, fmt.Sprint(s["last_seen"]))
			return s["status"] == "online" && seen.After(restarted) && s["latest"] != nil
		})
	})

	t.Run("CertificateRotation", func(t *testing.T) {
		var before map[string]any
		c.do("GET", "/api/servers/"+srvID, nil, &before)
		var out map[string]string
		if st, body := c.do("POST", "/api/servers/"+srvID+"/rotate-certificate", nil, &out); st != 200 {
			t.Fatalf("rotate: %d %s", st, body)
		}
		if out["fingerprint"] == before["agent_fingerprint"] {
			t.Fatal("fingerprint unchanged")
		}
		eventually(t, 10*time.Second, "online with new identity", func() bool {
			var s map[string]any
			c.do("GET", "/api/servers/"+srvID, nil, &s)
			return s["status"] == "online" && s["agent_fingerprint"] == out["fingerprint"]
		})
	})

	t.Run("UnenrollRevokesCore", func(t *testing.T) {
		if st, body := c.do("DELETE", "/api/servers/"+srvID, nil, nil); st != 200 || strings.Contains(body, "warning") {
			t.Fatalf("remove: %d %s", st, body)
		}
		if n := len(ag.a.State().Clients()); n != 0 {
			t.Fatalf("agent still trusts %d clients", n)
		}
	})
	_ = filepath.Join
	_ = bufio.NewReader
}
