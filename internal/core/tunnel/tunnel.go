// Package tunnel reaches agents that are not directly reachable from this PC
// (a firewall, a cloud security group or NAT in between) through SSH, which
// is already open because NEXUS installed the agent over it.
//
// The agent then listens on 127.0.0.1 only. NEXUS signs in with a key it
// generated for this purpose. The server's authorized_keys pins that key to
// "restrict" (no port forwarding in either direction, no PTY, no agent or X11
// forwarding, no ~/.ssh/rc) and to a forced command, `nexus-agent bridge
// <port>`, which only pipes the SSH session to the agent's local port. The
// key can run nothing else. SSH port forwarding is not used at all, so this
// also works where sshd has AllowTcpForwarding no. The SSH host key is
// pinned, and the agent's pinned mutual-TLS connection runs inside unchanged.
package tunnel

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/WSEU790/MoistTux/internal/core/secrets"
	"github.com/WSEU790/MoistTux/internal/core/store"
)

// HostKeyAlgorithms prefers ED25519 so the server presents the same host key
// that the operator confirmed during installation.
var HostKeyAlgorithms = []string{
	ssh.KeyAlgoED25519, ssh.KeyAlgoECDSA256, ssh.KeyAlgoECDSA384, ssh.KeyAlgoECDSA521,
	ssh.KeyAlgoRSASHA512, ssh.KeyAlgoRSASHA256,
}

// Config is one tunnel: SSH to Host:Port as User, then forward to
// 127.0.0.1:AgentPort on the server.
type Config struct {
	Host      string
	Port      int
	User      string
	HostKey   string // SHA256:… fingerprint confirmed by the operator
	AgentPort int
	Signer    ssh.Signer
}

// Tunnel keeps one SSH connection and opens a forwarded channel per agent
// connection. It reconnects after the SSH connection drops.
type Tunnel struct {
	cfg Config

	mu     sync.Mutex
	c      *ssh.Client
	closed bool
}

func New(cfg Config) *Tunnel { return &Tunnel{cfg: cfg} }

// AgentBin is where the agent installer puts the agent on every server.
const AgentBin = "/usr/local/lib/nexus/nexus-agent"

// Dial opens a connection to the agent through the tunnel: one SSH session
// running the key's forced bridge command. The address it is given is
// ignored; the only destination is the server's own agent port.
func (t *Tunnel) Dial(ctx context.Context, _, _ string) (net.Conn, error) {
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		c, err := t.client(ctx)
		if err != nil {
			return nil, err
		}
		conn, err := t.session(c)
		if err == nil {
			return conn, nil
		}
		lastErr = err
		t.drop(c) // the SSH connection is broken; reconnect once
	}
	return nil, fmt.Errorf("SSH tunnel to %s: %w", t.addr(), lastErr)
}

func (t *Tunnel) session(c *ssh.Client) (*sessionConn, error) {
	sess, err := c.NewSession()
	if err != nil {
		return nil, err
	}
	in, err := sess.StdinPipe()
	if err != nil {
		sess.Close()
		return nil, err
	}
	out, err := sess.StdoutPipe()
	if err != nil {
		sess.Close()
		return nil, err
	}
	sc := &sessionConn{sess: sess, in: in, out: out, tun: t, exited: make(chan struct{})}
	sess.Stderr = &sc.stderr
	// The server runs its forced command whatever is requested here.
	if err := sess.Start(BridgeCommand(t.cfg.AgentPort)); err != nil {
		sess.Close()
		return nil, err
	}
	go func() { sc.exitErr = sess.Wait(); close(sc.exited) }()
	return sc, nil
}

// Check proves the whole path: SSH login with the tunnel key, the forced
// bridge command, and the agent listening on its local port. A bridge that
// cannot reach the agent exits at once; a working one stays open.
func (t *Tunnel) Check(ctx context.Context) error {
	conn, err := t.Dial(ctx, "tcp", "")
	if err != nil {
		return err
	}
	sc := conn.(*sessionConn)
	defer sc.Close()
	select {
	case <-sc.exited:
		msg := strings.TrimSpace(sc.stderr.String())
		if msg == "" {
			msg = fmt.Sprint(sc.exitErr)
		}
		return fmt.Errorf("SSH tunnel to %s: %s", t.addr(), msg)
	case <-time.After(2 * time.Second):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// BridgeCommand is the forced command of the tunnel key.
func BridgeCommand(agentPort int) string {
	return AgentBin + " bridge " + strconv.Itoa(agentPort)
}

// sessionConn presents one SSH session (the bridge) as a net.Conn.
type sessionConn struct {
	sess    *ssh.Session
	in      io.WriteCloser
	out     io.Reader
	tun     *Tunnel
	stderr  lockedBuffer
	exited  chan struct{}
	exitErr error
	once    sync.Once
}

func (c *sessionConn) Read(p []byte) (int, error) {
	n, err := c.out.Read(p)
	if err == io.EOF && n == 0 {
		// Surface the bridge's own explanation (for example "agent not
		// reachable") instead of a bare EOF.
		select {
		case <-c.exited:
		case <-time.After(500 * time.Millisecond):
		}
		if msg := strings.TrimSpace(c.stderr.String()); msg != "" {
			return 0, fmt.Errorf("SSH tunnel to %s: %s", c.tun.addr(), msg)
		}
	}
	return n, err
}

func (c *sessionConn) Write(p []byte) (int, error) { return c.in.Write(p) }

func (c *sessionConn) Close() error {
	c.once.Do(func() {
		_ = c.in.Close()
		_ = c.sess.Close()
	})
	return nil
}

func (c *sessionConn) LocalAddr() net.Addr  { return tunnelAddr("nexus") }
func (c *sessionConn) RemoteAddr() net.Addr { return tunnelAddr(c.tun.addr()) }

// Deadlines are not supported on SSH channels; callers here use contexts.
func (c *sessionConn) SetDeadline(time.Time) error      { return nil }
func (c *sessionConn) SetReadDeadline(time.Time) error  { return nil }
func (c *sessionConn) SetWriteDeadline(time.Time) error { return nil }

type tunnelAddr string

func (a tunnelAddr) Network() string { return "ssh" }
func (a tunnelAddr) String() string  { return string(a) }

type lockedBuffer struct {
	mu sync.Mutex
	b  []byte
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.b) < 4096 {
		l.b = append(l.b, p...)
	}
	return len(p), nil
}

func (l *lockedBuffer) String() string { l.mu.Lock(); defer l.mu.Unlock(); return string(l.b) }

func (t *Tunnel) addr() string {
	return t.cfg.User + "@" + net.JoinHostPort(t.cfg.Host, strconv.Itoa(t.cfg.Port))
}

func (t *Tunnel) client(ctx context.Context) (*ssh.Client, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.closed {
		return nil, errors.New("SSH tunnel closed")
	}
	if t.c != nil {
		return t.c, nil
	}
	want := t.cfg.HostKey
	cfg := &ssh.ClientConfig{
		User: t.cfg.User, Auth: []ssh.AuthMethod{ssh.PublicKeys(t.cfg.Signer)}, HostKeyAlgorithms: HostKeyAlgorithms,
		Timeout: 15 * time.Second,
		HostKeyCallback: func(_ string, _ net.Addr, key ssh.PublicKey) error {
			if got := ssh.FingerprintSHA256(key); got != want {
				return fmt.Errorf("SSH host key changed: expected %s, server presented %s", want, got)
			}
			return nil
		},
	}
	d := net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}
	raw, err := d.DialContext(ctx, "tcp", net.JoinHostPort(t.cfg.Host, strconv.Itoa(t.cfg.Port)))
	if err != nil {
		return nil, fmt.Errorf("SSH tunnel to %s: %w", t.addr(), err)
	}
	_ = raw.SetDeadline(time.Now().Add(20 * time.Second))
	conn, chans, reqs, err := ssh.NewClientConn(raw, raw.RemoteAddr().String(), cfg)
	if err != nil {
		raw.Close()
		return nil, fmt.Errorf("SSH tunnel to %s: %w", t.addr(), err)
	}
	_ = raw.SetDeadline(time.Time{})
	c := ssh.NewClient(conn, chans, reqs)
	t.c = c
	go t.keepalive(c)
	return c, nil
}

// keepalive detects dead connections (for example after the PC slept), so
// the next request reconnects instead of waiting on a broken socket.
func (t *Tunnel) keepalive(c *ssh.Client) {
	tk := time.NewTicker(30 * time.Second)
	defer tk.Stop()
	for range tk.C {
		res := make(chan error, 1)
		go func() { _, _, err := c.SendRequest("keepalive@openssh.com", true, nil); res <- err }()
		select {
		case err := <-res:
			if err == nil {
				continue
			}
		case <-time.After(15 * time.Second):
		}
		t.drop(c)
		return
	}
}

func (t *Tunnel) drop(c *ssh.Client) {
	t.mu.Lock()
	if t.c == c {
		t.c = nil
	}
	t.mu.Unlock()
	c.Close()
}

// Close ends the tunnel; Dial fails afterwards.
func (t *Tunnel) Close() {
	t.mu.Lock()
	c := t.c
	t.c, t.closed = nil, true
	t.mu.Unlock()
	if c != nil {
		c.Close()
	}
}

// ---------------------------------------------------------------- keys

// Key is a freshly generated tunnel-only key.
type Key struct {
	PrivatePEM    []byte // OpenSSH private key; seal before storing
	AuthorizedKey string // the restricted authorized_keys line for the server
	Signer        ssh.Signer
}

// NewKey generates an ED25519 key whose authorized_keys line allows nothing
// but the bridge to 127.0.0.1:agentPort. marker identifies it (and lets a
// reinstall replace NEXUS's earlier key instead of adding another one).
func NewKey(agentPort int, marker string) (*Key, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	blk, err := ssh.MarshalPrivateKey(priv, marker)
	if err != nil {
		return nil, err
	}
	sp, err := ssh.NewPublicKey(pub)
	if err != nil {
		return nil, err
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		return nil, err
	}
	return &Key{PrivatePEM: pem.EncodeToMemory(blk), AuthorizedKey: AuthorizedLine(sp, agentPort, marker), Signer: signer}, nil
}

// AuthorizedLine is the authorized_keys entry for a tunnel key. "restrict"
// turns off all port forwarding (local and remote), PTY allocation, agent and
// X11 forwarding and ~/.ssh/rc; the forced command replaces whatever the
// client asks to run with the bridge to the agent's local port.
func AuthorizedLine(pub ssh.PublicKey, agentPort int, marker string) string {
	return fmt.Sprintf(`restrict,command="%s" %s %s`,
		BridgeCommand(agentPort), strings.TrimSpace(string(ssh.MarshalAuthorizedKey(pub))), marker)
}

// ---------------------------------------------------------------- manager

// Manager hands the fleet a dialer for each server that uses a tunnel.
type Manager struct {
	st   *store.Store
	prot secrets.Protector
	log  *log.Logger

	mu      sync.Mutex
	tunnels map[string]*Tunnel
}

func NewManager(st *store.Store, prot secrets.Protector, logger *log.Logger) *Manager {
	return &Manager{st: st, prot: prot, log: logger, tunnels: map[string]*Tunnel{}}
}

// Dialer returns the tunnel dialer for a server, or nil when the server is
// reached directly.
func (m *Manager) Dialer(ctx context.Context, serverID string) (func(ctx context.Context, network, addr string) (net.Conn, error), error) {
	m.mu.Lock()
	t, ok := m.tunnels[serverID]
	m.mu.Unlock()
	if ok {
		return t.Dial, nil
	}
	row, err := m.st.GetSSHTunnel(ctx, serverID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	t, err = m.open(row)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	if old, ok := m.tunnels[serverID]; ok {
		t.Close()
		t = old
	} else {
		m.tunnels[serverID] = t
	}
	m.mu.Unlock()
	return t.Dial, nil
}

func (m *Manager) open(row *store.SSHTunnel) (*Tunnel, error) {
	pemKey, err := secrets.Open(m.prot, row.SealedKey)
	if err != nil {
		return nil, fmt.Errorf("cannot unseal the SSH tunnel key: %w", err)
	}
	signer, err := ssh.ParsePrivateKey(pemKey)
	if err != nil {
		return nil, fmt.Errorf("SSH tunnel key is unreadable: %w", err)
	}
	return New(Config{Host: row.Host, Port: row.Port, User: row.User, HostKey: row.HostKey, AgentPort: row.AgentPort, Signer: signer}), nil
}

// Forget closes a server's tunnel (after removal or a new tunnel key).
func (m *Manager) Forget(serverID string) {
	m.mu.Lock()
	t, ok := m.tunnels[serverID]
	delete(m.tunnels, serverID)
	m.mu.Unlock()
	if ok {
		t.Close()
	}
}
