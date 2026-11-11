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

	"encoding/json"

	"github.com/WSEU790/MoistTux/internal/core/agentbin"
	"github.com/WSEU790/MoistTux/internal/core/agentclient"
	"github.com/WSEU790/MoistTux/internal/core/secrets"
	"github.com/WSEU790/MoistTux/internal/core/events"
	"github.com/WSEU790/MoistTux/internal/core/fleet"
	"github.com/WSEU790/MoistTux/internal/core/store"
	"github.com/WSEU790/MoistTux/internal/core/tunnel"
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
	Remember    bool     `json:"remember"`  // save this login (sealed) for later upgrades and repairs
	UseSaved    bool     `json:"use_saved"` // upgrade with the login saved for ServerID
	// Connection: "auto" (direct when the agent port is reachable from this
	// PC, otherwise through SSH), "direct" or "ssh". Using SSH adds a
	// tunnel-only key to the SSH user's authorized_keys, so the operator
	// chooses it explicitly in the install form.
	Connection string `json:"connection"`
}

// secretBundle is what gets sealed when an operator chooses to remember a login.
type secretBundle struct {
	Password   string `json:"password,omitempty"`
	PrivateKey string `json:"private_key,omitempty"`
	Passphrase string `json:"passphrase,omitempty"`
	SudoPass   string `json:"sudo_password,omitempty"`
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
	Remembered string             `json:"remembered,omitempty"` // saved | not saved: <reason>
	toSave     *store.SSHCredential // sealed login to persist once the server is enrolled
}

type Manager struct {
	prot  secrets.Protector
	fleet *fleet.Manager
	st    *store.Store
	bus   *events.Bus
	log   *log.Logger

	mu   sync.Mutex
	jobs map[string]*Job
}

func NewManager(f *fleet.Manager, st *store.Store, bus *events.Bus, prot secrets.Protector, logger *log.Logger) *Manager {
	return &Manager{fleet: f, st: st, bus: bus, prot: prot, log: logger, jobs: map[string]*Job{}}
}

// Saved returns the metadata of a server's saved SSH login (never the secret).
func (m *Manager) Saved(ctx context.Context, serverID string) (*store.SSHCredential, error) {
	return m.st.GetSSHCredential(ctx, serverID)
}

// Forget deletes a saved login.
func (m *Manager) Forget(ctx context.Context, serverID, by string) (bool, error) {
	ok, err := m.st.DeleteSSHCredential(ctx, serverID)
	if err == nil && ok {
		_ = m.st.Audit(ctx, by, "ssh.credentials.forget", serverID, nil)
	}
	return ok, err
}

// loadSaved fills a request from the sealed login saved for req.ServerID.
func (m *Manager) loadSaved(ctx context.Context, req *Request) error {
	c, err := m.st.GetSSHCredential(ctx, req.ServerID)
	if err != nil {
		return errors.New("no SSH login is saved for this server")
	}
	raw, err := secrets.Open(m.prot, c.Sealed)
	if err != nil {
		return fmt.Errorf("the saved SSH login cannot be decrypted on this Windows account (%v); forget it and enter the login again", err)
	}
	var b secretBundle
	if err := json.Unmarshal(raw, &b); err != nil {
		return errors.New("the saved SSH login is corrupt; forget it and enter the login again")
	}
	req.Host, req.SSHPort, req.User, req.HostKey = c.Host, c.Port, c.User, c.HostKey
	req.Password, req.PrivateKey, req.Passphrase, req.SudoPass = b.Password, b.PrivateKey, b.Passphrase, b.SudoPass
	m.st.TouchSSHCredential(ctx, req.ServerID)
	return nil
}

func (m *Manager) sealFor(req Request) (*store.SSHCredential, error) {
	b, _ := json.Marshal(secretBundle{Password: req.Password, PrivateKey: req.PrivateKey, Passphrase: req.Passphrase, SudoPass: req.SudoPass})
	sealed, err := secrets.Seal(m.prot, b)
	if err != nil {
		return nil, err
	}
	return &store.SSHCredential{Host: req.Host, Port: req.SSHPort, User: req.User, AuthKind: authKindShort(req), HostKey: req.HostKey, Sealed: sealed}, nil
}

func authKindShort(r Request) string {
	if r.PrivateKey != "" {
		return "key"
	}
	return "password"
}

// persistSaved stores the sealed login once the server ID is known.
func (m *Manager) persistSaved(j *Job, serverID string) {
	m.mu.Lock()
	c := j.toSave
	j.toSave = nil
	m.mu.Unlock()
	if c == nil {
		return
	}
	c.ServerID = serverID
	status := "saved"
	if err := m.st.PutSSHCredential(context.Background(), c); err != nil {
		status = "not saved: " + err.Error()
	} else {
		_ = m.st.Audit(context.Background(), "operator", "ssh.credentials.save", serverID, map[string]string{"user": c.User, "auth": c.AuthKind, "host_key": c.HostKey})
	}
	m.mu.Lock()
	j.Remembered = status
	m.mu.Unlock()
}

var stepDefs = [][2]string{
	{"connect", "Connect over SSH (pinned host key)"},
	{"inspect", "Inspect server (architecture, systemd, privileges)"},
	{"upload", "Upload agent binary and verify checksum"},
	{"install", "Install least-privilege agent service"},
	{"network", "Choose connection (direct or SSH tunnel)"},
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
	if req.UseSaved {
		if req.Mode != "upgrade" {
			return nil, errors.New("a saved login can only be used for an existing server")
		}
		if err := m.loadSaved(context.Background(), &req); err != nil {
			return nil, err
		}
		req.Remember = false // already saved
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
	switch req.Connection {
	case "":
		req.Connection = "auto"
	case "auto", "direct", "ssh":
	default:
		return nil, errors.New("connection must be auto, direct or ssh")
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
	_ = m.st.Audit(context.Background(), "operator", "deploy."+req.Mode+".start", req.Host, map[string]any{"job": j.ID, "ssh_user": req.User, "ssh_port": req.SSHPort, "host_key": req.HostKey, "agent_port": req.AgentPort, "docker": req.Docker, "auth": authKind(req), "saved_login": req.UseSaved, "remember": req.Remember, "connection": req.Connection})
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
	if req.Remember {
		// Seal now (credentials are proven to work) and persist on success.
		if c, err := m.sealFor(req); err == nil {
			m.mu.Lock()
			j.toSave = c
			m.mu.Unlock()
		} else {
			m.mu.Lock()
			j.Remembered = "not saved: " + err.Error()
			m.mu.Unlock()
		}
	}
	r := &remote{c: client}

	// 2. inspect
	m.step(j, "inspect", "running", "")
	out, _, _, err := r.run(ctx, `uname -m; id -u; (. /etc/os-release 2>/dev/null && echo "$PRETTY_NAME") || echo unknown; if [ -d /run/systemd/system ]; then echo systemd; else echo nosystemd; fi; hostname; echo "${SSH_CONNECTION%% *}"`, nil, false, 30*time.Second)
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
	// The address the server sees for this PC (after NAT), which is what a
	// firewall rule must allow; the local address is only a fallback.
	seenIP := localIP
	if len(f) > 5 {
		if ip := net.ParseIP(strings.TrimSpace(f[5])); ip != nil {
			seenIP = ip.String()
		}
	}
	m.fact(j, "seen_ip", seenIP)
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
	var existing *store.SSHTunnel
	if req.Mode == "upgrade" {
		existing, _ = m.st.GetSSHTunnel(ctx, req.ServerID)
	}
	installCmd := func(loopback bool) string {
		listen := fmt.Sprintf(":%d", req.AgentPort)
		if loopback {
			listen = fmt.Sprintf("127.0.0.1:%d", req.AgentPort)
		}
		c := fmt.Sprintf("%s install --listen %s", tmp, listen)
		if req.Docker {
			c += " --docker"
		}
		return c
	}
	loopback := existing != nil || (req.Mode == "install" && req.Connection == "ssh")
	iout, ierr, code, err := r.run(ctx, installCmd(loopback), nil, true, 5*time.Minute)
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

	// 5. connection: direct when this PC can reach the agent port, otherwise
	// (when the operator allowed it) through an SSH tunnel.
	m.step(j, "network", "running", "")
	var tun *tunnel.Tunnel
	var tunRow *store.SSHTunnel
	switch {
	case existing != nil:
		// Upgrade of a tunneled server: keep the agent on 127.0.0.1 and make
		// sure the tunnel key is still authorized (repair).
		if err := ensureAuthorizedKey(ctx, r, existing.AuthorizedKey, keyMarker(existing.AuthorizedKey)); err != nil {
			m.step(j, "network", "failed", err.Error())
			m.finish(j, err, "")
			return
		}
		m.step(j, "network", "done", fmt.Sprintf("SSH tunnel via %s@%s:%d · agent listens on 127.0.0.1:%d only · tunnel key present", existing.User, existing.Host, existing.Port, existing.AgentPort))
	case req.Mode == "upgrade":
		status, note := m.firewallNote(ctx, r, req.AgentPort, seenIP)
		m.step(j, "network", status, note)
	default:
		why := ""
		if !loopback {
			err := reachable(ctx, req.Host, req.AgentPort)
			switch {
			case err == nil:
				m.step(j, "network", "done", fmt.Sprintf("direct: this PC reaches the agent on %s:%d", req.Host, req.AgentPort))
			case req.Connection == "direct":
				status, note := m.firewallNote(ctx, r, req.AgentPort, seenIP)
				if status == "done" {
					status, note = "warning", fmt.Sprintf("this PC cannot reach port %d (%s)", req.AgentPort, shortNetErr(err))
				}
				m.step(j, "network", status, note)
			default:
				why = fmt.Sprintf("port %d is not reachable from this PC (%s), so ", req.AgentPort, shortNetErr(err))
				// Move the agent to 127.0.0.1: nothing stays exposed.
				out, se, code, err := r.run(ctx, installCmd(true), nil, true, 5*time.Minute)
				if err != nil || code != 0 {
					m.step(j, "network", "failed", "could not switch the agent to 127.0.0.1: "+tail(out+"\n"+se, 4))
					m.finish(j, errors.New("could not reconfigure the agent for the SSH tunnel"), "")
					return
				}
				if c := reCode.FindString(out); c != "" {
					enrollCode = c
				}
				loopback = true
			}
		}
		if loopback {
			var err error
			tun, tunRow, err = m.setupTunnel(ctx, r, req)
			if err != nil {
				m.step(j, "network", "failed", err.Error())
				m.finish(j, fmt.Errorf("SSH tunnel could not be set up: %w", err), fmt.Sprintf("NEXUS signs in with a tunnel-only key that it adds to %s's ~/.ssh/authorized_keys. That needs public-key logins for this user in sshd (PubkeyAuthentication yes, and the default AuthorizedKeysFile) and no ForceCommand overriding the key's command. Alternatively, allow port %d from %s in the server firewall and choose the direct connection.", req.User, req.AgentPort, seenIP))
				return
			}
			defer tun.Close()
			m.step(j, "network", "done", fmt.Sprintf("%susing an encrypted SSH tunnel via %s@%s:%d · agent listens on 127.0.0.1:%d only · no firewall change needed", why, req.User, req.Host, req.SSHPort, req.AgentPort))
		}
	}

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
					m.persistSaved(j, req.ServerID)
					m.step(j, "enroll", "done", "agent "+h.AgentVersion+" answered with its pinned identity")
					m.finish(j, nil, "")
					return
				}
				lastErr = err
			}
			time.Sleep(1500 * time.Millisecond)
		}
		m.step(j, "enroll", "failed", fmt.Sprint(lastErr))
		hint := ""
		if errors.Is(lastErr, agentclient.ErrFingerprintMismatch) {
			hint = "The agent on the server now has a different identity — its state was removed (for example with uninstall --purge), so it is effectively a new agent. NEXUS will not trust it automatically: remove this server from NEXUS and add it again."
		}
		m.finish(j, fmt.Errorf("upgraded agent did not answer with its pinned identity: %v", lastErr), hint)
		return
	}
	er := &fleet.EnrollRequest{Name: req.Name, Address: req.Host, Port: req.AgentPort, Code: enrollCode, Environment: req.Environment, Group: req.Group, Tags: req.Tags}
	if tun != nil {
		er.Dial, er.Tunnel = tun.Dial, tunRow
	}
	m.enroll(ctx, j, er, seenIP)
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

func (m *Manager) enroll(ctx context.Context, j *Job, er *fleet.EnrollRequest, seenIP string) {
	if er.Dial != nil {
		srv, err := m.fleet.Enroll(ctx, *er)
		if err != nil {
			m.step(j, "enroll", "failed", err.Error())
			m.finish(j, err, "")
			return
		}
		m.mu.Lock()
		j.ServerID = srv.ID
		m.mu.Unlock()
		m.persistSaved(j, srv.ID)
		m.step(j, "enroll", "done", fmt.Sprintf("enrolled as %s through the SSH tunnel · agent fingerprint %s… matched", srv.Name, srv.AgentFingerprint[:16]))
		m.finish(j, nil, "")
		return
	}
	if err := reachable(ctx, er.Address, er.Port); err != nil {
		hint := fmt.Sprintf("The agent is installed and running on the server, but this PC cannot connect to %s:%d (%s). Either install again with the connection set to Automatic, which uses an SSH tunnel and needs no firewall change, or allow the port for this PC (the server sees it as %s) in the server firewall and any cloud security group — for ufw: sudo ufw allow from %s to any port %d proto tcp — then choose Retry enrollment.", er.Address, er.Port, shortNetErr(err), seenIP, seenIP, er.Port)
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
	m.persistSaved(j, srv.ID)
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
	seenIP := j.Facts["seen_ip"]
	if seenIP == "" {
		seenIP = j.Facts["workstation_ip"]
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		m.step(j, "enroll", "running", "retrying")
		m.enroll(ctx, j, er, seenIP)
	}()
	return j.snapshot(), nil
}

// firewallNote reports whether ufw on the server allows the agent port.
func (m *Manager) firewallNote(ctx context.Context, r *remote, port int, seenIP string) (string, string) {
	if fw, _, _, _ := r.run(ctx, "ufw status", nil, true, 20*time.Second); strings.Contains(fw, "Status: active") {
		portRe := regexp.MustCompile(`(?m)^\s*` + strconv.Itoa(port) + `(/tcp)?\s+ALLOW`)
		if !portRe.MatchString(fw) {
			return "warning", fmt.Sprintf("ufw is active and has no rule for port %d. NEXUS does not change firewalls; to allow this PC (seen by the server as %s) run: sudo ufw allow from %s to any port %d proto tcp", port, seenIP, seenIP, port)
		}
	}
	return "done", fmt.Sprintf("agent listening on :%d", port)
}

func shortNetErr(err error) string {
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return "connection timed out: a firewall is dropping it"
	}
	if strings.Contains(err.Error(), "refused") {
		return "connection refused"
	}
	return err.Error()
}

var reMarker = regexp.MustCompile(`nexus-tunnel:[A-Za-z0-9]{8,64}$`)

func keyMarker(authorizedLine string) string { return reMarker.FindString(strings.TrimSpace(authorizedLine)) }

// ensureAuthorizedKey writes the tunnel key line into the SSH user's
// authorized_keys, replacing an earlier key from this NEXUS (same marker)
// and leaving every other line untouched. The line arrives on stdin.
func ensureAuthorizedKey(ctx context.Context, r *remote, line, marker string) error {
	if marker == "" || !reMarker.MatchString(marker) {
		return errors.New("invalid tunnel key marker")
	}
	script := `sh -c 'umask 077; d="$HOME/.ssh"; f="$d/authorized_keys"; IFS= read -r l || exit 3; mkdir -p "$d" && touch "$f" && { grep -v -F -e "$1" "$f" > "$f.nexus-tmp"; printf "%s\n" "$l" >> "$f.nexus-tmp"; } && cat "$f.nexus-tmp" > "$f" && rm -f "$f.nexus-tmp" && grep -c -F -e "$1" "$f"' nexus ` + marker
	out, se, code, err := r.run(ctx, script, []byte(line+"\n"), false, 30*time.Second)
	if err != nil || code != 0 || strings.TrimSpace(out) != "1" {
		return fmt.Errorf("could not update ~/.ssh/authorized_keys (exit %d): %v %s", code, err, tail(se, 2))
	}
	return nil
}

// setupTunnel creates a tunnel-only key, authorizes it for the SSH user and
// proves it works by opening the tunnel to the agent with it.
func (m *Manager) setupTunnel(ctx context.Context, r *remote, req Request) (*tunnel.Tunnel, *store.SSHTunnel, error) {
	fp := m.fleet.CoreFingerprint()
	if len(fp) > 16 {
		fp = fp[:16]
	}
	marker := "nexus-tunnel:" + regexp.MustCompile(`[^A-Za-z0-9]`).ReplaceAllString(fp, "")
	key, err := tunnel.NewKey(req.AgentPort, marker)
	if err != nil {
		return nil, nil, err
	}
	sealed, err := secrets.Seal(m.prot, key.PrivatePEM)
	if err != nil {
		return nil, nil, fmt.Errorf("could not protect the tunnel key: %w", err)
	}
	if err := ensureAuthorizedKey(ctx, r, key.AuthorizedKey, marker); err != nil {
		return nil, nil, err
	}
	t := tunnel.New(tunnel.Config{Host: req.Host, Port: req.SSHPort, User: req.User, HostKey: req.HostKey, AgentPort: req.AgentPort, Signer: key.Signer})
	var lastErr error
	for i := 0; i < 12; i++ { // the agent restarts after reinstalling
		dctx, cancel := context.WithTimeout(ctx, 25*time.Second)
		err := t.Check(dctx)
		cancel()
		if err == nil {
			row := &store.SSHTunnel{Host: req.Host, Port: req.SSHPort, User: req.User, HostKey: req.HostKey, AgentPort: req.AgentPort, AuthorizedKey: key.AuthorizedKey, SealedKey: sealed}
			return t, row, nil
		}
		lastErr = err
		if strings.Contains(err.Error(), "unable to authenticate") || strings.Contains(err.Error(), "host key") {
			break
		}
		time.Sleep(1500 * time.Millisecond)
	}
	t.Close()
	return nil, nil, lastErr
}
