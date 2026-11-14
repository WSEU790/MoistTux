//go:build linux

package agent

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/WSEU790/MoistTux/internal/protocol"
)

type grantFixture struct {
	key     *ecdsa.PrivateKey
	signers string
	machine string
}

func newGrantFixture(t *testing.T) *grantFixture {
	t.Helper()
	if os.Geteuid() != 0 {
		signersOwner = -1 // files created by an unprivileged test user
		t.Cleanup(func() { signersOwner = 0 })
	}
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	der, _ := x509.MarshalPKIXPublicKey(&k.PublicKey)
	dir := filepath.Join(t.TempDir(), "nexus-agent")
	os.Mkdir(dir, 0o755)
	signers := filepath.Join(dir, "shell-signers")
	os.WriteFile(signers, []byte("# comment\necdsa-p256 "+base64.StdEncoding.EncodeToString(der)+" nexus@pc\n"), 0o644)
	machine := filepath.Join(t.TempDir(), "machine-id")
	os.WriteFile(machine, []byte("0123456789abcdef0123456789abcdef\n"), 0o444)
	return &grantFixture{key: k, signers: signers, machine: machine}
}

func (f *grantFixture) grant(t *testing.T, g protocol.ShellGrant, key *ecdsa.PrivateKey) (*protocol.ShellGrant, []byte, string) {
	raw, _ := json.Marshal(g)
	sum := sha256.Sum256(raw)
	sig, _ := ecdsa.SignASN1(rand.Reader, key, sum[:])
	dg, draw, err := decodeGrant(base64.StdEncoding.EncodeToString(raw))
	if err != nil {
		t.Fatal(err)
	}
	return dg, draw, base64.StdEncoding.EncodeToString(sig)
}

func TestShellGrantVerification(t *testing.T) {
	f := newGrantFixture(t)
	now := time.Now()
	good := protocol.ShellGrant{V: 1, MachineID: "0123456789abcdef0123456789abcdef", Command: "apt-get update", Nonce: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Expires: now.Add(2 * time.Minute).Unix(), TimeoutSec: 60}
	g, raw, sig := f.grant(t, good, f.key)
	if err := verifyGrantFor(g, raw, sig, f.signers, f.machine, now); err != nil {
		t.Fatalf("valid grant refused: %v", err)
	}

	// The signature covers the exact bytes: a changed command fails.
	tampered := strings.Replace(string(raw), "apt-get update", "apt-get purge", 1)
	if err := verifyGrantFor(g, []byte(tampered), sig, f.signers, f.machine, now); err == nil {
		t.Fatal("tampered command accepted")
	}
	other, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	g2, raw2, sig2 := f.grant(t, good, other)
	if err := verifyGrantFor(g2, raw2, sig2, f.signers, f.machine, now); err == nil || !strings.Contains(err.Error(), "not signed") {
		t.Fatalf("untrusted key accepted: %v", err)
	}
	wrongHost := good
	wrongHost.MachineID = "ffffffffffffffffffffffffffffffff"
	g3, raw3, sig3 := f.grant(t, wrongHost, f.key)
	if err := verifyGrantFor(g3, raw3, sig3, f.signers, f.machine, now); err == nil || !strings.Contains(err.Error(), "different server") {
		t.Fatalf("grant for another server accepted: %v", err)
	}
	expired := good
	expired.Expires = now.Add(-time.Second).Unix()
	g4, raw4, sig4 := f.grant(t, expired, f.key)
	if err := verifyGrantFor(g4, raw4, sig4, f.signers, f.machine, now); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expired grant accepted: %v", err)
	}
	long := good
	long.Expires = now.Add(time.Hour).Unix()
	g5, raw5, sig5 := f.grant(t, long, f.key)
	if err := verifyGrantFor(g5, raw5, sig5, f.signers, f.machine, now); err == nil {
		t.Fatal("grant valid for an hour accepted")
	}
	// A signers file others can write is not trusted.
	os.Chmod(f.signers, 0o666)
	if err := verifyGrantFor(g, raw, sig, f.signers, f.machine, now); err == nil || !strings.Contains(err.Error(), "writable only by root") {
		t.Fatalf("world-writable signers file trusted: %v", err)
	}
	os.Chmod(f.signers, 0o644)
	if err := verifyGrantFor(g, raw, sig, filepath.Join(t.TempDir(), "missing"), f.machine, now); err == nil || !strings.Contains(err.Error(), "not enabled") {
		t.Fatalf("missing signers file: %v", err)
	}
}

func TestShellGrantNonceIsSingleUse(t *testing.T) {
	dir := t.TempDir()
	exp := time.Now().Add(time.Minute).Unix()
	if err := consumeNonce(dir, "0123456789abcdef0123", exp); err != nil {
		t.Fatal(err)
	}
	if err := consumeNonce(dir, "0123456789abcdef0123", exp); err == nil || !strings.Contains(err.Error(), "already used") {
		t.Fatalf("replayed grant accepted: %v", err)
	}
	if err := consumeNonce(dir, "fedcba9876543210fedc", exp); err != nil {
		t.Fatalf("second grant refused: %v", err)
	}
	// Expired entries are pruned.
	if err := consumeNonce(dir, "old0123456789abcdef0", time.Now().Add(-time.Minute).Unix()); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "used-grants"))
	if strings.Count(string(b), "\n") != 3 {
		t.Fatalf("nonce file: %q", b)
	}
	if err := consumeNonce(dir, "short", exp); err == nil {
		t.Fatal("short nonce accepted")
	}
}

func TestDecodeGrantLimits(t *testing.T) {
	for _, g := range []protocol.ShellGrant{
		{V: 2, Command: "x", TimeoutSec: 5},
		{V: 1, Command: "", TimeoutSec: 5},
		{V: 1, Command: "x", TimeoutSec: 0},
		{V: 1, Command: "x", TimeoutSec: 3600},
	} {
		raw, _ := json.Marshal(g)
		if _, _, err := decodeGrant(base64.StdEncoding.EncodeToString(raw)); err == nil {
			t.Errorf("accepted %+v", g)
		}
	}
}
