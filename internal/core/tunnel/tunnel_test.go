package tunnel

import (
	"bufio"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestAuthorizedLineAllowsOnlyTheBridge(t *testing.T) {
	k, err := NewKey(7443, "nexus-tunnel:0123456789abcdef")
	if err != nil {
		t.Fatal(err)
	}
	line := k.AuthorizedKey
	want := `restrict,command="/usr/local/lib/nexus/nexus-agent bridge 7443" ssh-ed25519 `
	if !strings.HasPrefix(line, want) {
		t.Fatalf("authorized_keys line %q does not start with %q", line, want)
	}
	// Re-enabling any forwarding would undo "restrict".
	for _, bad := range []string{"port-forwarding", "permitopen", "permitlisten", "pty", "agent-forwarding", "x11", "user-rc", "\n"} {
		if strings.Contains(line, bad) {
			t.Errorf("authorized_keys line contains %q: %s", bad, line)
		}
	}
	if !strings.HasSuffix(line, " nexus-tunnel:0123456789abcdef") {
		t.Errorf("marker missing: %s", line)
	}
	// The line parses as OpenSSH authorized_keys with the same public key.
	pub, comment, opts, _, err := ssh.ParseAuthorizedKey([]byte(line))
	if err != nil {
		t.Fatal(err)
	}
	if comment != "nexus-tunnel:0123456789abcdef" || len(opts) != 2 || opts[0] != "restrict" {
		t.Fatalf("parsed comment %q options %q", comment, opts)
	}
	if string(pub.Marshal()) != string(k.Signer.PublicKey().Marshal()) {
		t.Fatal("authorized key does not match the signer")
	}
	// The sealed form is an OpenSSH private key NEXUS can read back.
	if _, err := ssh.ParsePrivateKey(k.PrivatePEM); err != nil {
		t.Fatal(err)
	}
}

// fakeServer is an SSH server that behaves like sshd with the tunnel key's
// forced command: it accepts only the given key and the pinned host key,
// refuses forwarding, and connects every exec/shell session to backend
// (or exits with an error, as the real bridge does when the agent is down).
type fakeServer struct {
	addr     string
	hostFP   string
	backend  string
	sessions chan string
}

func startFakeServer(t *testing.T, client ssh.PublicKey, backend string) *fakeServer {
	t.Helper()
	_, hk, _ := ed25519.GenerateKey(rand.Reader)
	hs, _ := ssh.NewSignerFromKey(hk)
	cfg := &ssh.ServerConfig{PublicKeyCallback: func(_ ssh.ConnMetadata, k ssh.PublicKey) (*ssh.Permissions, error) {
		if string(k.Marshal()) == string(client.Marshal()) {
			return nil, nil
		}
		return nil, fmt.Errorf("unknown key")
	}}
	cfg.AddHostKey(hs)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	fs := &fakeServer{addr: l.Addr().String(), hostFP: ssh.FingerprintSHA256(hs.PublicKey()), backend: backend, sessions: make(chan string, 16)}
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go fs.serve(c, cfg)
		}
	}()
	return fs
}

func (fs *fakeServer) serve(c net.Conn, cfg *ssh.ServerConfig) {
	_, chans, reqs, err := ssh.NewServerConn(c, cfg)
	if err != nil {
		return
	}
	go ssh.DiscardRequests(reqs)
	for nc := range chans {
		if nc.ChannelType() != "session" {
			_ = nc.Reject(ssh.Prohibited, "restricted key")
			continue
		}
		ch, creqs, err := nc.Accept()
		if err != nil {
			continue
		}
		go func() {
			for r := range creqs {
				if r.Type != "exec" && r.Type != "shell" {
					_ = r.Reply(false, nil)
					continue
				}
				_ = r.Reply(true, nil)
				fs.sessions <- string(r.Payload)
				back, err := net.Dial("tcp", fs.backend)
				if err != nil {
					fmt.Fprintf(ch.Stderr(), "nexus-agent bridge: agent not reachable: %v\n", err)
					_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ C uint32 }{1}))
					ch.Close()
					return
				}
				go func() { _, _ = io.Copy(back, ch); back.(*net.TCPConn).CloseWrite() }()
				_, _ = io.Copy(ch, back)
				back.Close()
				_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ C uint32 }{0}))
				ch.Close()
				return
			}
		}()
	}
}

func echoServer(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); _, _ = io.Copy(c, c) }()
		}
	}()
	return l.Addr().String()
}

func tunnelTo(t *testing.T, fs *fakeServer, k *Key, hostFP string) *Tunnel {
	host, port, _ := net.SplitHostPort(fs.addr)
	p, _ := strconv.Atoi(port)
	tn := New(Config{Host: host, Port: p, User: "deploy", HostKey: hostFP, AgentPort: 7443, Signer: k.Signer})
	t.Cleanup(tn.Close)
	return tn
}

func TestDialCarriesDataThroughTheBridge(t *testing.T) {
	k, _ := NewKey(7443, "nexus-tunnel:test")
	fs := startFakeServer(t, k.Signer.PublicKey(), echoServer(t))
	tn := tunnelTo(t, fs, k, fs.hostFP)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := tn.Check(ctx); err != nil {
		t.Fatalf("check: %v", err)
	}
	if cmd := <-fs.sessions; !strings.Contains(cmd, "nexus-agent bridge 7443") {
		t.Fatalf("session asked to run %q", cmd)
	}
	// Two connections share one SSH connection; each is its own session.
	for i := 0; i < 2; i++ {
		c, err := tn.Dial(ctx, "tcp", "ignored:1")
		if err != nil {
			t.Fatal(err)
		}
		msg := fmt.Sprintf("hello %d\n", i)
		if _, err := c.Write([]byte(msg)); err != nil {
			t.Fatal(err)
		}
		got, err := bufio.NewReader(c).ReadString('\n')
		if err != nil || got != msg {
			t.Fatalf("echo through tunnel: %q, %v", got, err)
		}
		c.Close()
	}
}

func TestCheckReportsTheBridgeError(t *testing.T) {
	k, _ := NewKey(7443, "nexus-tunnel:test")
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	dead := l.Addr().String()
	l.Close() // nothing listens: like an agent that is not running
	fs := startFakeServer(t, k.Signer.PublicKey(), dead)
	tn := tunnelTo(t, fs, k, fs.hostFP)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := tn.Check(ctx)
	if err == nil || !strings.Contains(err.Error(), "agent not reachable") {
		t.Fatalf("expected the bridge's error, got %v", err)
	}
}

func TestPinnedHostKeyAndKeyAreEnforced(t *testing.T) {
	k, _ := NewKey(7443, "nexus-tunnel:test")
	fs := startFakeServer(t, k.Signer.PublicKey(), echoServer(t))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := tunnelTo(t, fs, k, "SHA256:"+strings.Repeat("A", 43)).Check(ctx); err == nil || !strings.Contains(err.Error(), "host key changed") {
		t.Fatalf("a different host key was accepted: %v", err)
	}
	other, _ := NewKey(7443, "nexus-tunnel:other")
	if err := tunnelTo(t, fs, other, fs.hostFP).Check(ctx); err == nil || !strings.Contains(err.Error(), "unable to authenticate") {
		t.Fatalf("an unknown key was accepted: %v", err)
	}
}

func TestReconnectsAfterTheSSHConnectionDrops(t *testing.T) {
	k, _ := NewKey(7443, "nexus-tunnel:test")
	fs := startFakeServer(t, k.Signer.PublicKey(), echoServer(t))
	tn := tunnelTo(t, fs, k, fs.hostFP)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := tn.Check(ctx); err != nil {
		t.Fatal(err)
	}
	tn.mu.Lock()
	first := tn.c
	tn.mu.Unlock()
	first.Close() // the server side went away (sleep, network change)
	c, err := tn.Dial(ctx, "tcp", "")
	if err != nil {
		t.Fatalf("dial after drop: %v", err)
	}
	c.Close()
	tn.mu.Lock()
	second := tn.c
	tn.mu.Unlock()
	if second == nil || second == first {
		t.Fatal("tunnel did not reconnect")
	}
}
