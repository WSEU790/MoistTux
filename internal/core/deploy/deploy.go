// Package deploy installs or upgrades nexus-agent on a Linux server over SSH
// and enrolls it. SSH credentials are used for the duration of one job only;
// they are never stored, logged, written to the audit trail or shown to the AI.
//
// Trust model: the operator confirms the server's SSH host-key fingerprint
// (NEXUS also checks the Windows user's known_hosts file). The connection is
// pinned to that exact key, so the enrollment code read back over SSH — which
// itself carries the agent's certificate fingerprint — is authentic.
package deploy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"

	"github.com/WSEU790/MoistTux/internal/core/agentbin"
	"github.com/WSEU790/MoistTux/internal/core/events"
	"github.com/WSEU790/MoistTux/internal/core/fleet"
	"github.com/WSEU790/MoistTux/internal/core/store"
)

// ---------------------------------------------------------------- host key scan

type ScanResult struct {
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Fingerprint string `json:"fingerprint"` // SHA256:…
	KeyType     string `json:"key_type"`
	KnownHosts  string `json:"known_hosts"` // match | mismatch | unknown | unavailable
	KnownFile   string `json:"known_hosts_file,omitempty"`
}

var errScanDone = errors.New("host key captured")

// hostKeyAlgos is used for both the scan and the deployment connection so the
// server presents the same host key both times. ED25519 comes first because
// it is what operators usually compare against.
var hostKeyAlgos = []string{
	ssh.KeyAlgoED25519, ssh.KeyAlgoECDSA256, ssh.KeyAlgoECDSA384, ssh.KeyAlgoECDSA521,
	ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256,
}

func validHost(h string) error {
	if h == "" || len(h) > 253 || strings.ContainsAny(h, " /\\@") {
		return fmt.Errorf("enter a hostname or IP address")
	}
	return nil
}

// Scan performs only the SSH key exchange to read the server's host key. No
// credentials are sent.
func Scan(ctx context.Context, host string, port int) (*ScanResult, error) {
	if err := validHost(host); err != nil {
		return nil, err
	}
	if port == 0 {
		port = 22
	}
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	var captured ssh.PublicKey
	var remote net.Addr
	cfg := &ssh.ClientConfig{
		User:              "nexus-hostkey-scan",
		Timeout:           12 * time.Second,
		HostKeyAlgorithms: hostKeyAlgos,
		HostKeyCallback: func(_ string, r net.Addr, key ssh.PublicKey) error {
			captured, remote = key, r
			return errScanDone
		},
	}
	d := net.Dialer{Timeout: 12 * time.Second}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("cannot reach SSH on %s: %w", addr, err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	_, _, _, err = ssh.NewClientConn(conn, addr, cfg)
	if captured == nil {
		return nil, fmt.Errorf("SSH handshake with %s failed: %v", addr, err)
	}
	res := &ScanResult{Host: host, Port: port, Fingerprint: ssh.FingerprintSHA256(captured), KeyType: captured.Type()}
	res.KnownHosts, res.KnownFile = checkKnownHosts(addr, remote, captured)
	return res, nil
}

func checkKnownHosts(addr string, remote net.Addr, key ssh.PublicKey) (string, string) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "unavailable", ""
	}
	file := filepath.Join(home, ".ssh", "known_hosts")
	if _, err := os.Stat(file); err != nil {
		return "unavailable", ""
	}
	cb, err := knownhosts.New(file)
	if err != nil {
		return "unavailable", file
	}
	err = cb(knownhosts.Normalize(addr), remote, key)
	if err == nil {
		return "match", file
	}
	var ke *knownhosts.KeyError
	if errors.As(err, &ke) {
		// Only a different key of the same type is a real mismatch; a file
		// that merely lists another key type says nothing about this one.
		for _, w := range ke.Want {
			if w.Key.Type() == key.Type() {
				return "mismatch", file
			}
		}
	}
	return "unknown", file
}

// ---------------------------------------------------------------- jobs

type Request struct {
	Mode        string   `json:"mode"` // install | upgrade
	ServerID    string   `json:"server_id,omitempty"`
	Host        string   `json:"host"`
	SSHPort     int      `json:"ssh_port"`
	User        string   `json:"user"`
	Password    string   `json:"password,omitempty"`
	PrivateKey  string   `json:"private_key,omitempty"`
	Passphrase  string   `json:"passphrase,omitempty"`
	SudoPass    string   `json:"sudo_password,omitempty"`
	HostKey     string   `json:"host_key"` // fingerprint confirmed by the operator
	AgentPort   int      `json:"agent_port"`
	Docker      bool     `json:"docker"`
	Name        string   `json:"name"`
	Environment string   `json:"environment"`
	Group       string   `json:"group"`
	Tags        []string `json:"tags"`
}

type Step struct {
	ID       string     `json:"id"`
	Label    string     `json:"label"`
	Status   string     `json:"status"` // pending | running | done | failed | skipped | warning
	Detail   string     `json:"detail,omitempty"`
	Started  *time.Time `json:"started,omitempty"`
	Finished *time.Time `json:"finished,omitempty"`
}

type Job struct {
	ID       string     `json:"id"`
	Mode     string     `json:"mode"`
	Host     string     `json:"host"`
	User     string     `json:"user"`
	State    string     `json:"state"` // running | succeeded | failed
	Error    string     `json:"error,omitempty"`
	Hint     string     `json:"hint,omitempty"`
	ServerID string     `json:"server_id,omitempty"`
	Steps    []*Step    `json:"steps"`
	Facts    map[string]string `json:"facts"`
	Started  time.Time  `json:"started"`
	Finished *time.Time `json:"finished,omitempty"`
	CanRetry bool       `json:"can_retry_enroll"`

	pending *fleet.EnrollRequest // installed but not yet enrolled
}

type Manager struct {
	fleet *fleet.Manager
	st    *store.Store
	bus   *events.Bus
	log   *log.Logger

	mu   sync.Mutex
	jobs map[string]*Job
}

func NewManager(f *fleet.Manager, st *store.Store, bus *events.Bus, logger *log.Logger) *Manager {
	return &Manager{fleet: f, st: st, bus: bus, log: logger, jobs: map[string]*Job{}}
}

var stepDefs = [][2]string{
	{"connect", "Connect over SSH (pinned host key)"},
	{"inspect", "Inspect server (architecture, systemd, privileges)"},
	{"upload", "Upload agent binary and verify checksum"},
	{"install", "Install least-privilege agent service"},
	{"network", "Check agent port and firewall"},
	{"enroll", "Verify agent identity and enroll"},
}

func (m *Manager) Get(id string) (*Job, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.jobs[id]
	if !ok {
		return nil, false
	}
	return j.snapshot(), true
}

func (j *Job) snapshot() *Job {
	c := *j
	c.Steps = make([]*Step, len(j.Steps))
	for i, s := range j.Steps {
		cs := *s
		c.Steps[i] = &cs
	}
	c.Facts = map[string]string{}
	for k, v := range j.Facts {
		c.Facts[k] = v
	}
	return &c
}

var reFP = regexp.MustCompile(`^SHA256:[A-Za-z0-9+/]{43}$`)

// Start validates the request and runs the deployment in the background.
func (m *Manager) Start(req Request) (*Job, error) {
	if req.Mode == "" {
		req.Mode = "install"
	}
	if req.Mode != "install" && req.Mode != "upgrade" {
		return nil, errors.New("mode must be install or upgrade")
	}
	if err := validHost(req.Host); err != nil {
		return nil, err
	}
	if req.SSHPort == 0 {
		req.SSHPort = 22
	}
	if req.AgentPort == 0 {
		req.AgentPort = 7443
	}
	if req.SSHPort < 1 || req.SSHPort > 65535 || req.AgentPort < 1 || req.AgentPort > 65535 {
		return nil, errors.New("port out of range")
	}
	if !regexp.MustCompile(`^[a-z_][a-z0-9_.-]{0,31}$`).MatchString(req.User) {
		return nil, errors.New("enter a valid Linux user name")
	}
	if req.Password == "" && req.PrivateKey == "" {
		return nil, errors.New("provide a password or a private key")
	}
	if !reFP.MatchString(req.HostKey) {
		return nil, errors.New("confirm the server's SSH host key first")
	}
	if req.Mode == "upgrade" {
		if _, err := m.st.GetServer(context.Background(), req.ServerID); err != nil {
			return nil, errors.New("unknown server to upgrade")
		}
	} else {
		switch req.Environment {
		case "", "production", "staging", "development", "lab":
		default:
			return nil, errors.New("invalid environment")
		}
	}
	j := &Job{ID: store.NewID("dep"), Mode: req.Mode, Host: req.Host, User: req.User, State: "running", Started: time.Now().UTC(), Facts: map[string]string{}, ServerID: req.ServerID}
	for _, d := range stepDefs {
		if req.Mode == "upgrade" && d[0] == "enroll" {
			d[1] = "Reconnect and verify the upgraded agent"
		}
		j.Steps = append(j.Steps, &Step{ID: d[0], Label: d[1], Status: "pending"})
	}
	m.mu.Lock()
	m.jobs[j.ID] = j
	m.mu.Unlock()
	_ = m.st.Audit(context.Background(), "operator", "deploy."+req.Mode+".start", req.Host, map[string]any{"job": j.ID, "ssh_user": req.User, "ssh_port": req.SSHPort, "host_key": req.HostKey, "agent_port": req.AgentPort, "docker": req.Docker, "auth": authKind(req)})
	go m.run(j, req)
	return j.snapshot(), nil
}

func authKind(r Request) string {
	if r.PrivateKey != "" {
		return "private key"
	}
	return "password"
}

func (m *Manager) publish(j *Job) {
	m.bus.Publish("deploy", j.snapshot())
}

func (m *Manager) step(j *Job, id, status, detail string) {
	m.mu.Lock()
	now := time.Now().UTC()
	for _, s := range j.Steps {
		if s.ID == id {
			if status == "running" {
				s.Started = &now
			} else {
				s.Finished = &now
			}
			s.Status = status
			if detail != "" {
				s.Detail = detail
			}
		}
	}
	m.mu.Unlock()
	m.publish(j)
}

func (m *Manager) fact(j *Job, k, v string) {
	m.mu.Lock()
	j.Facts[k] = v
	m.mu.Unlock()
}

func (m *Manager) finish(j *Job, err error, hint string) {
	m.mu.Lock()
	now := time.Now().UTC()
	j.Finished = &now
	if err != nil {
		j.State, j.Error, j.Hint = "failed", err.Error(), hint
		for _, s := range j.Steps {
			if s.Status == "pending" {
				s.Status = "skipped"
			}
		}
	} else {
		j.State = "succeeded"
	}
	snap := j.snapshot()
	m.mu.Unlock()
	detail := map[string]any{"job": j.ID, "state": snap.State, "server": snap.ServerID, "facts": snap.Facts}
	if err != nil {
		detail["error"] = err.Error()
	}
	_ = m.st.Audit(context.Background(), "nexus", "deploy."+j.Mode+".finish", j.Host, detail)
	m.bus.Publish("deploy", snap)
}

// ---------------------------------------------------------------- the deployment

type remote struct {
	c        *ssh.Client
	root     bool
	sudoMode string // none | nopasswd | password
	sudoPass string
}

// run executes a command. Passwords are written to stdin only, never placed
// on the command line (which would be visible in the remote process list).
func (r *remote) run(ctx context.Context, cmd string, stdin []byte, privileged bool, timeout time.Duration) (string, string, int, error) {
	sess, err := r.c.NewSession()
	if err != nil {
		return "", "", -1, err
	}
	defer sess.Close()
	var so, se bytes.Buffer
	sess.Stdout, sess.Stderr = &so, &se
	var in []byte
	full := cmd
	if privileged && !r.root {
		switch r.sudoMode {
		case "nopasswd":
			full = "sudo -n " + cmd
		case "password":
			full = "sudo -S -p '' " + cmd
			in = append([]byte(r.sudoPass+"\n"), stdin...)
		}
	} else {
		in = stdin
	}
	if in != nil {
		sess.Stdin = bytes.NewReader(in)
	}
	done := make(chan error, 1)
	go func() { done <- sess.Run(full) }()
	select {
	case err = <-done:
	case <-time.After(timeout):
		_ = sess.Signal(ssh.SIGKILL)
		return so.String(), se.String(), -1, fmt.Errorf("timed out after %s: %s", timeout, cmd)
	case <-ctx.Done():
		return so.String(), se.String(), -1, ctx.Err()
	}
	code := 0
	if err != nil {
		var ee *ssh.ExitError
		if errors.As(err, &ee) {
			code = ee.ExitStatus()
			err = nil
		} else {
			return so.String(), se.String(), -1, err
		}
	}
	return so.String(), se.String(), code, nil
}

var (
	reCode = regexp.MustCompile(`NX1-[A-Z2-7]{26}-[A-Z2-7]{26}`)
	reTmp  = regexp.MustCompile(`^/tmp/nexus-agent\.[A-Za-z0-9]{6,16}$`)
)

func tail(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func (m *Manager) run(j *Job, req Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	// Credentials live only in this function's request copy.
	defer func() { req.Password, req.PrivateKey, req.Passphrase, req.SudoPass = "", "", "", "" }()

	// 1. connect
	m.step(j, "connect", "running", "")
	var auths []ssh.AuthMethod
	if req.PrivateKey != "" {
		var signer ssh.Signer
		var err error
		if req.Passphrase != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase([]byte(req.PrivateKey), []byte(req.Passphrase))
		} else {
			signer, err = ssh.ParsePrivateKey([]byte(req.PrivateKey))
		}
		if err != nil {
			var pe *ssh.PassphraseMissingError
			hint := ""
			if errors.As(err, &pe) {
				hint = "The private key is encrypted; enter its passphrase."
			}
			m.step(j, "connect", "failed", "could not read the private key: "+err.Error())
			m.finish(j, fmt.Errorf("invalid private key: %w", err), hint)
			return
		}
		auths = append(auths, ssh.PublicKeys(signer))
	}
	if req.Password != "" {
		pw := req.Password
		auths = append(auths, ssh.Password(pw), ssh.KeyboardInteractive(func(_, _ string, qs []string, _ []bool) ([]string, error) {
			ans := make([]string, len(qs))
			for i := range qs {
				ans[i] = pw
			}
			return ans, nil
		}))
	}
	want := req.HostKey
	cfg := &ssh.ClientConfig{
		User: req.User, Auth: auths, Timeout: 15 * time.Second, HostKeyAlgorithms: hostKeyAlgos,
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			if got := ssh.FingerprintSHA256(key); got != want {
				return fmt.Errorf("SSH host key changed: expected %s, server presented %s", want, got)
			}
			return nil
		},
	}
	addr := net.JoinHostPort(req.Host, strconv.Itoa(req.SSHPort))
	client, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		hint := "Check the user name and password or key. Password logins may be disabled on the server (PasswordAuthentication no); use a key instead."
		if strings.Contains(err.Error(), "host key changed") {
			hint = "The server is not presenting the key you confirmed. Do not continue unless you know why the key changed."
		} else if strings.Contains(err.Error(), "connection refused") || strings.Contains(err.Error(), "i/o timeout") {
			hint = "SSH is not reachable on that address and port from this PC."
		}
		m.step(j, "connect", "failed", err.Error())
		m.finish(j, fmt.Errorf("SSH connection failed: %w", err), hint)
		return
	}
	defer client.Close()
	localIP := ""
	if ta, ok := client.LocalAddr().(*net.TCPAddr); ok {
		localIP = ta.IP.String()
	}
	m.fact(j, "workstation_ip", localIP)
	m.fact(j, "ssh_server", string(client.ServerVersion()))
	m.step(j, "connect", "done", fmt.Sprintf("Authenticated as %s; host key %s verified", req.User, want))
	r := &remote{c: client}

	// 2. inspect
	m.step(j, "inspect", "running", "")
	out, _, _, err := r.run(ctx, `uname -m; id -u; (. /etc/os-release 2>/dev/null && echo "$PRETTY_NAME") || echo unknown; if [ -d /run/systemd/system ]; then echo systemd; else echo nosystemd; fi; hostname`, nil, false, 30*time.Second)
	if err != nil {
		m.step(j, "inspect", "failed", err.Error())
		m.finish(j, err, "")
		return
	}
	f := strings.Split(strings.TrimSpace(out), "\n")
	if len(f) < 5 {
		m.step(j, "inspect", "failed", "unexpected output: "+out)
		m.finish(j, errors.New("could not inspect the server (is the login shell a POSIX shell?)"), "")
		return
	}
	arch, err := agentbin.NormalizeArch(strings.TrimSpace(f[0]))
	if err != nil {
		m.step(j, "inspect", "failed", err.Error())
		m.finish(j, err, "")
		return
	}
	r.root = strings.TrimSpace(f[1]) == "0"
	osName, initSys, hostname := strings.TrimSpace(f[2]), strings.TrimSpace(f[3]), strings.TrimSpace(f[4])
	m.fact(j, "arch", arch)
	m.fact(j, "os", osName)
	m.fact(j, "hostname", hostname)
	if initSys != "systemd" {
		m.step(j, "inspect", "failed", osName+" is not running systemd")
		m.finish(j, errors.New("the agent installer requires systemd"), "On non-systemd hosts, copy nexus-agent manually and run `nexus-agent run` under your init system, then enroll with its code.")
		return
	}
	privilege := "root login"
	if !r.root {
		if _, _, code, _ := r.run(ctx, "sudo -n true", nil, false, 20*time.Second); code == 0 {
			r.sudoMode, privilege = "nopasswd", "passwordless sudo"
		} else {
			pass := req.SudoPass
			if pass == "" {
				pass = req.Password
			}
			if pass == "" {
				m.step(j, "inspect", "failed", "sudo requires a password")
				m.finish(j, errors.New("sudo on the server requires a password"), "Enter the sudo password (usually the user's login password).")
				return
			}
			r.sudoMode, r.sudoPass = "password", pass
			if _, se, code, _ := r.run(ctx, "-k true", nil, true, 20*time.Second); code != 0 {
				m.step(j, "inspect", "failed", "sudo refused: "+tail(se, 2))
				m.finish(j, errors.New("sudo rejected the password or the user is not allowed to use sudo"), "The SSH user must be root or in the sudo group.")
				return
			}
			privilege = "sudo with password"
		}
	}
	m.fact(j, "privilege", privilege)
	m.step(j, "inspect", "done", fmt.Sprintf("%s · %s · %s · systemd · %s", hostname, osName, arch, privilege))

	// 3. upload
	m.step(j, "upload", "running", "")
	bin, err := agentbin.Get(arch)
	if err != nil {
		m.step(j, "upload", "failed", err.Error())
		m.finish(j, err, "")
		return
	}
	tmpOut, se, code, err := r.run(ctx, "mktemp /tmp/nexus-agent.XXXXXXXXXX", nil, false, 20*time.Second)
	tmp := strings.TrimSpace(tmpOut)
	if err != nil || code != 0 || !reTmp.MatchString(tmp) {
		m.step(j, "upload", "failed", fmt.Sprintf("mktemp failed: %v %s", err, tail(se, 2)))
		m.finish(j, errors.New("could not create a temporary file on the server"), "")
		return
	}
	defer r.run(context.Background(), "rm -f "+tmp, nil, false, 20*time.Second) //nolint:errcheck
	start := time.Now()
	if _, se, code, err := r.run(ctx, "cat > "+tmp+" && chmod 700 "+tmp, bin.Data, false, 10*time.Minute); err != nil || code != 0 {
		m.step(j, "upload", "failed", fmt.Sprintf("%v %s", err, tail(se, 2)))
		m.finish(j, errors.New("upload failed"), "Check free space in /tmp on the server.")
		return
	}
	sumOut, _, _, _ := r.run(ctx, "sha256sum "+tmp, nil, false, 60*time.Second)
	if fields := strings.Fields(sumOut); len(fields) == 0 || fields[0] != bin.SHA256 {
		m.step(j, "upload", "failed", "checksum mismatch after upload")
		m.finish(j, errors.New("the uploaded agent does not match the expected checksum"), "")
		return
	}
	m.step(j, "upload", "done", fmt.Sprintf("%.1f MiB in %s · sha256 %s… verified", float64(len(bin.Data))/(1<<20), time.Since(start).Round(100*time.Millisecond), bin.SHA256[:16]))

	// 4. install
	m.step(j, "install", "running", "")
	cmd := fmt.Sprintf("%s install --listen :%d", tmp, req.AgentPort)
	if req.Docker {
		cmd += " --docker"
	}
	iout, ierr, code, err := r.run(ctx, cmd, nil, true, 5*time.Minute)
	if err != nil || code != 0 {
		detail := tail(iout+"\n"+ierr, 6)
		m.step(j, "install", "failed", detail)
		m.finish(j, fmt.Errorf("agent installation failed (exit %d)", code), "")
		return
	}
	enrollCode := reCode.FindString(iout)
	if req.Mode == "install" && enrollCode == "" {
		m.step(j, "install", "failed", "installer did not print an enrollment code:\n"+tail(iout, 6))
		m.finish(j, errors.New("no enrollment code returned"), "")
		return
	}
	m.step(j, "install", "done", "service user nexus · sudoers helper rule · systemd unit nexus-agent.service enabled and started")

	// 5. network
	m.step(j, "network", "running", "")
	note := fmt.Sprintf("agent listening on :%d", req.AgentPort)
	status := "done"
	if fw, _, _, _ := r.run(ctx, "ufw status", nil, true, 20*time.Second); strings.Contains(fw, "Status: active") {
		portRe := regexp.MustCompile(`(?m)^\s*` + strconv.Itoa(req.AgentPort) + `(/tcp)?\s+ALLOW`)
		if !portRe.MatchString(fw) {
			status = "warning"
			note = fmt.Sprintf("ufw is active and has no rule for port %d. NEXUS does not change firewalls; to allow this PC run: sudo ufw allow from %s to any port %d proto tcp", req.AgentPort, localIP, req.AgentPort)
		}
	}
	m.step(j, "network", status, note)

	// 6. enroll or reconnect
	m.step(j, "enroll", "running", "")
	if req.Mode == "upgrade" {
		_ = m.fleet.Reconnect(ctx, req.ServerID)
		var lastErr error
		for i := 0; i < 20; i++ {
			cl, err := m.fleet.Client(req.ServerID)
			if err == nil {
				hctx, hc := context.WithTimeout(ctx, 5*time.Second)
				h, err := cl.Health(hctx)
				hc()
				if err == nil {
					m.fact(j, "agent_version", h.AgentVersion)
					m.step(j, "enroll", "done", "agent "+h.AgentVersion+" answered with its pinned identity")
					m.finish(j, nil, "")
					return
				}
				lastErr = err
			}
			time.Sleep(1500 * time.Millisecond)
		}
		m.step(j, "enroll", "failed", fmt.Sprint(lastErr))
		m.finish(j, fmt.Errorf("upgraded agent did not answer: %v", lastErr), "")
		return
	}
	er := &fleet.EnrollRequest{Name: req.Name, Address: req.Host, Port: req.AgentPort, Code: enrollCode, Environment: req.Environment, Group: req.Group, Tags: req.Tags}
	m.enroll(ctx, j, er, localIP)
}

// reachable probes the agent port; refused connections are retried because
// the service may still be starting, silent drops fail fast (firewall).
func reachable(ctx context.Context, host string, port int) error {
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	var err error
	for i := 0; i < 8; i++ {
		var c net.Conn
		d := net.Dialer{Timeout: 4 * time.Second}
		c, err = d.DialContext(ctx, "tcp", addr)
		if err == nil {
			c.Close()
			return nil
		}
		if !strings.Contains(err.Error(), "refused") && i >= 1 {
			return err
		}
		time.Sleep(1500 * time.Millisecond)
	}
	return err
}

func (m *Manager) enroll(ctx context.Context, j *Job, er *fleet.EnrollRequest, localIP string) {
	if err := reachable(ctx, er.Address, er.Port); err != nil {
		hint := fmt.Sprintf("The agent is installed and running on the server, but this PC cannot connect to %s:%d (%v). Allow that port from this PC in the server firewall and any cloud security group — for ufw: sudo ufw allow from %s to any port %d proto tcp — then choose Retry enrollment.", er.Address, er.Port, err, localIP, er.Port)
		m.mu.Lock()
		j.pending, j.CanRetry = er, true
		m.mu.Unlock()
		m.step(j, "enroll", "failed", fmt.Sprintf("cannot reach agent port %d from this PC", er.Port))
		m.finish(j, errors.New("the agent port is not reachable from this PC"), hint)
		return
	}
	srv, err := m.fleet.Enroll(ctx, *er)
	if err != nil {
		retry := strings.Contains(err.Error(), "could not reach")
		m.mu.Lock()
		if retry {
			j.pending, j.CanRetry = er, true
		}
		m.mu.Unlock()
		m.step(j, "enroll", "failed", err.Error())
		m.finish(j, err, "")
		return
	}
	m.mu.Lock()
	j.ServerID, j.pending, j.CanRetry = srv.ID, nil, false
	m.mu.Unlock()
	m.step(j, "enroll", "done", fmt.Sprintf("enrolled as %s · agent fingerprint %s… matched", srv.Name, srv.AgentFingerprint[:16]))
	m.finish(j, nil, "")
}

// RetryEnroll re-attempts only the enrollment of an installed agent (for
// example after the operator opened the firewall). No SSH is involved.
func (m *Manager) RetryEnroll(id string) (*Job, error) {
	m.mu.Lock()
	j, ok := m.jobs[id]
	if !ok {
		m.mu.Unlock()
		return nil, errors.New("unknown deployment")
	}
	if j.State == "running" || j.pending == nil {
		m.mu.Unlock()
		return nil, errors.New("this deployment has nothing to retry")
	}
	er := j.pending
	j.State, j.Error, j.Hint, j.Finished, j.CanRetry = "running", "", "", nil, false
	m.mu.Unlock()
	localIP := j.Facts["workstation_ip"]
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		m.step(j, "enroll", "running", "retrying")
		m.enroll(ctx, j, er, localIP)
	}()
	return j.snapshot(), nil
}

var _ = io.Discard
