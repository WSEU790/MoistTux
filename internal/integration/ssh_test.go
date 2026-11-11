//go:build linux

package integration

import (
	"fmt"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/WSEU790/MoistTux/internal/core/agentbin"
)

// TestSSHDeployment installs the agent on a real, disposable Linux server over
// SSH, exercises systemd service management through it, then uninstalls it.
// It only runs when pointed at a test server:
//
//	NEXUS_SSH_TEST_HOST=10.0.0.5 NEXUS_SSH_TEST_USER=deploy NEXUS_SSH_TEST_PASSWORD=… \
//	  [NEXUS_SSH_TEST_FINGERPRINT=SHA256:…] [NEXUS_SSH_TEST_UNIT=nginx.service] go test ./internal/integration -run SSH
//
// Never point it at a production server: it restarts a service and removes the agent.
func TestSSHDeployment(t *testing.T) {
	host, user, pass := os.Getenv("NEXUS_SSH_TEST_HOST"), os.Getenv("NEXUS_SSH_TEST_USER"), os.Getenv("NEXUS_SSH_TEST_PASSWORD")
	if host == "" || user == "" || pass == "" {
		t.Skip("set NEXUS_SSH_TEST_HOST, NEXUS_SSH_TEST_USER and NEXUS_SSH_TEST_PASSWORD to run against a disposable server")
	}
	if len(agentbin.Available()) == 0 {
		t.Skip("agent binaries not embedded; run `make embed-agents` first")
	}
	unit := os.Getenv("NEXUS_SSH_TEST_UNIT")
	if unit == "" {
		unit = "nginx.service"
	}
	c := startCore(t, t.TempDir())
	defer c.app.Close()

	var scan struct{ Fingerprint, KeyType string }
	if st, body := c.do("POST", "/api/deploy/scan", map[string]any{"host": host, "port": 22}, &scan); st != 200 {
		t.Fatalf("scan: %d %s", st, body)
	}
	if want := os.Getenv("NEXUS_SSH_TEST_FINGERPRINT"); want != "" && want != scan.Fingerprint {
		t.Fatalf("host key %s does not match expected %s", scan.Fingerprint, want)
	}
	runJob := func(body map[string]any) map[string]any {
		var job map[string]any
		if st, b := c.do("POST", "/api/deploy", body, &job); st != 202 {
			t.Fatalf("start: %d %s", st, b)
		}
		id := job["id"].(string)
		eventually(t, 5*time.Minute, "deployment to finish", func() bool {
			c.do("GET", "/api/deploy/"+id, nil, &job)
			return job["state"] != "running"
		})
		return job
	}

	// A forged host key must be refused before any credentials are sent.
	bad := runJob(map[string]any{"host": host, "user": user, "password": pass, "host_key": "SHA256:" + strings.Repeat("A", 43)})
	if bad["state"] != "failed" || !strings.Contains(fmt.Sprint(bad["error"]), "host key changed") {
		t.Fatalf("forged host key accepted: %v", bad)
	}

	job := runJob(map[string]any{"host": host, "user": user, "password": pass, "host_key": scan.Fingerprint, "name": "ssh-it", "environment": "lab"})
	if job["state"] != "succeeded" {
		t.Fatalf("deployment failed: %v — %v\nsteps: %v", job["error"], job["hint"], job["steps"])
	}
	srvID := job["server_id"].(string)
	eventually(t, 30*time.Second, "server online with systemd capability", func() bool {
		var s map[string]any
		c.do("GET", "/api/servers/"+srvID, nil, &s)
		h, _ := s["health"].(map[string]any)
		return s["status"] == "online" && h != nil && strings.Contains(fmt.Sprint(h["capabilities"]), "services") && h["helper_ok"] == true && h["running_as_root"] == false
	})

	// Real systemd management through the least-privilege helper.
	var before, after map[string]any
	if st, b := c.do("GET", "/api/servers/"+srvID+"/agent/services/"+unit, nil, &before); st != 200 {
		t.Skipf("unit %s not present (%d %s); skipping service management", unit, st, b)
	}
	var out map[string]any
	if st, b := c.do("POST", "/api/servers/"+srvID+"/actions", map[string]any{"tool": "restart_service", "args": map[string]any{"unit": unit}, "confirmed": true}, &out); st != 200 || out["status"] != "succeeded" {
		t.Fatalf("restart: %d %s", st, b)
	}
	c.do("GET", "/api/servers/"+srvID+"/agent/services/"+unit, nil, &after)
	if after["active_state"] != "active" || after["main_pid"] == before["main_pid"] {
		t.Fatalf("service was not restarted: before pid %v, after %v (%v)", before["main_pid"], after["main_pid"], after["active_state"])
	}
	var logs struct{ Source string }
	c.do("GET", "/api/servers/"+srvID+"/agent/logs?unit="+unit+"&since=-10m", nil, &logs)
	if logs.Source != "journald" {
		t.Errorf("expected journald logs, got %q", logs.Source)
	}

	// Clean up: unenroll, then uninstall over SSH.
	if st, b := c.do("DELETE", "/api/servers/"+srvID, nil, nil); st != 200 {
		t.Fatalf("remove: %d %s", st, b)
	}
	cl, err := ssh.Dial("tcp", host+":22", &ssh.ClientConfig{User: user, Auth: []ssh.AuthMethod{ssh.Password(pass)}, Timeout: 10 * time.Second,
		HostKeyAlgorithms: []string{ssh.KeyAlgoED25519, ssh.KeyAlgoECDSA256, ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256},
		HostKeyCallback: func(_ string, _ net.Addr, k ssh.PublicKey) error {
			if ssh.FingerprintSHA256(k) != scan.Fingerprint {
				return fmt.Errorf("host key changed during test")
			}
			return nil
		}})
	if err != nil {
		t.Fatalf("ssh for cleanup: %v", err)
	}
	defer cl.Close()
	sess, _ := cl.NewSession()
	sess.Stdin = strings.NewReader(pass + "\n")
	outb, err := sess.CombinedOutput("sudo -S -p '' /usr/local/lib/nexus/nexus-agent uninstall --purge; systemctl is-active nexus-agent || true")
	if err != nil && !strings.Contains(string(outb), "inactive") {
		t.Fatalf("uninstall: %v %s", err, outb)
	}
	if !strings.Contains(string(outb), "inactive") && !strings.Contains(string(outb), "unknown") {
		t.Fatalf("agent still active after uninstall: %s", outb)
	}
}
