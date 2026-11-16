//go:build linux

package agent

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/WSEU790/MoistTux/internal/protocol"
)

// Approved shell commands.
//
// NEXUS may run a free-form shell command as root only after a person
// approved that exact command in NEXUS. The core then signs a grant with its
// identity key (ECDSA P-256), binding the command to this machine, a nonce
// and a two-minute expiry. The root helper verifies the signature against the
// keys in ShellSignersPath. That file is root-owned and written only by
// `nexus-agent install` (which NEXUS runs as root over SSH), never by the
// agent's own service account, whose state directory it can write. So a
// compromised agent process still cannot run arbitrary commands as root.

const (
	ShellSignersPath = "/etc/nexus-agent/shell-signers"
	shellNonceDir    = "/var/lib/nexus-helper"
	maxShellTimeout  = 30 * time.Minute
)

// signersOwner is the uid the signers file must belong to (root); tests
// running unprivileged relax it.
var signersOwner = 0

func (a *Agent) handleShellRun(w http.ResponseWriter, r *http.Request) {
	var req protocol.ShellRunRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 128<<10)).Decode(&req); err != nil {
		writeErr(w, 400, "bad_request", err)
		return
	}
	g, _, err := decodeGrant(req.Token)
	if err != nil {
		writeErr(w, 400, "bad_grant", err)
		return
	}
	var res protocol.CommandResult
	timeout := time.Duration(g.TimeoutSec)*time.Second + 30*time.Second
	if err := a.priv.Do(r.Context(), timeout, &res, "shell", req.Token, req.Sig); err != nil {
		writeErr(w, 403, "shell_refused", err)
		return
	}
	writeJSON(w, 200, res)
}

func decodeGrant(token string) (*protocol.ShellGrant, []byte, error) {
	raw, err := base64.StdEncoding.DecodeString(token)
	if err != nil || len(raw) > 48<<10 {
		return nil, nil, errors.New("malformed grant")
	}
	var g protocol.ShellGrant
	if err := json.Unmarshal(raw, &g); err != nil {
		return nil, nil, errors.New("malformed grant")
	}
	if g.V != 1 || g.Command == "" || g.TimeoutSec < 1 || time.Duration(g.TimeoutSec)*time.Second > maxShellTimeout {
		return nil, nil, errors.New("invalid grant")
	}
	return &g, raw, nil
}

// execShellGrant runs inside the root helper.
func execShellGrant(ctx context.Context, token, sig string) (any, error) {
	g, raw, err := decodeGrant(token)
	if err != nil {
		return nil, err
	}
	if err := verifyGrant(g, raw, sig, ShellSignersPath, time.Now()); err != nil {
		return nil, err
	}
	if why := protocol.ShellBlocked(g.Command); why != "" {
		return nil, fmt.Errorf("refused: %s", why)
	}
	if err := consumeNonce(shellNonceDir, g.Nonce, g.Expires); err != nil {
		return nil, err
	}
	env := []string{"HOME=/root", "DEBIAN_FRONTEND=noninteractive", "NEEDRESTART_MODE=l", "SYSTEMD_PAGER=", "PAGER=cat"}
	// sh is resolved through the helper's fixed PATH, like every command.
	return runCmd(ctx, time.Duration(g.TimeoutSec)*time.Second, env, "sh", "-c", g.Command), nil
}

func verifyGrant(g *protocol.ShellGrant, raw []byte, sig, signersPath string, now time.Time) error {
	return verifyGrantFor(g, raw, sig, signersPath, "/etc/machine-id", now)
}

func verifyGrantFor(g *protocol.ShellGrant, raw []byte, sig, signersPath, machineIDPath string, now time.Time) error {
	return verifySigned(raw, sig, g.MachineID, g.Expires, signersPath, machineIDPath, now)
}

// verifySigned checks a grant signed by a trusted NEXUS core: expiry (at most
// ten minutes ahead), the target machine and the signature.
func verifySigned(raw []byte, sig, machineID string, expires int64, signersPath, machineIDPath string, now time.Time) error {
	if now.Unix() > expires {
		return errors.New("refused: the approval grant has expired")
	}
	if expires-now.Unix() > 600 {
		return errors.New("refused: the approval grant is valid for too long")
	}
	mid, err := os.ReadFile(machineIDPath)
	if err != nil || strings.TrimSpace(string(mid)) == "" {
		return errors.New("refused: /etc/machine-id is unavailable, so the grant cannot be bound to this server")
	}
	if strings.TrimSpace(string(mid)) != machineID {
		return errors.New("refused: the grant was issued for a different server")
	}
	keys, err := loadSigners(signersPath)
	if err != nil {
		return err
	}
	sb, err := base64.StdEncoding.DecodeString(sig)
	if err != nil {
		return errors.New("refused: malformed signature")
	}
	sum := sha256.Sum256(raw)
	for _, k := range keys {
		if ecdsa.VerifyASN1(k, sum[:], sb) {
			return nil
		}
	}
	return errors.New("refused: the grant is not signed by a NEXUS installation trusted for shell commands on this server (run Upgrade/repair over SSH from NEXUS to trust it)")
}

// loadSigners reads trusted core keys. The file and its directory must be
// owned by root and not writable by anyone else.
func loadSigners(path string) ([]*ecdsa.PublicKey, error) {
	for _, p := range []string{filepath.Dir(path), path} {
		fi, err := os.Stat(p)
		if err != nil {
			return nil, errors.New("refused: shell commands are not enabled on this server (no trusted NEXUS key; run Upgrade/repair over SSH from NEXUS)")
		}
		st, ok := fi.Sys().(*syscall.Stat_t)
		if !ok || (st.Uid != 0 && signersOwner == 0) || fi.Mode().Perm()&0o022 != 0 {
			return nil, fmt.Errorf("refused: %s must be owned by root and writable only by root", p)
		}
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var keys []*ecdsa.PublicKey
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 || strings.HasPrefix(fields[0], "#") || fields[0] != "ecdsa-p256" {
			continue
		}
		if k, err := ParseSignerKey(fields[1]); err == nil {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return nil, errors.New("refused: no trusted NEXUS key in " + path)
	}
	return keys, nil
}

// ParseSignerKey decodes a base64 PKIX ECDSA P-256 public key.
func ParseSignerKey(b64 string) (*ecdsa.PublicKey, error) {
	der, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, err
	}
	pk, err := x509.ParsePKIXPublicKey(der)
	if err != nil {
		return nil, err
	}
	k, ok := pk.(*ecdsa.PublicKey)
	if !ok || k.Curve.Params().Name != "P-256" {
		return nil, errors.New("not an ECDSA P-256 key")
	}
	return k, nil
}

// consumeNonce records a grant's nonce so it can be used only once.
func consumeNonce(dir, nonce string, exp int64) error {
	if len(nonce) < 16 || len(nonce) > 128 || strings.ContainsAny(nonce, " \n\t") {
		return errors.New("refused: invalid grant nonce")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, "used-grants"), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	now := time.Now().Unix()
	var keep []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		parts := strings.Fields(sc.Text())
		if len(parts) != 2 {
			continue
		}
		e, _ := strconv.ParseInt(parts[1], 10, 64)
		if e < now {
			continue // expired grants can no longer be used anyway
		}
		if parts[0] == nonce {
			return errors.New("refused: this approval grant was already used")
		}
		keep = append(keep, sc.Text())
	}
	keep = append(keep, fmt.Sprintf("%s %d", nonce, exp))
	if err := f.Truncate(0); err != nil {
		return err
	}
	if _, err := f.WriteAt([]byte(strings.Join(keep, "\n")+"\n"), 0); err != nil {
		return err
	}
	return nil
}

// AddShellSigner trusts a NEXUS core's key for shell grants (install, root).
func AddShellSigner(b64, label string) error {
	if _, err := ParseSignerKey(b64); err != nil {
		return fmt.Errorf("invalid --shell-signer key: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(ShellSignersPath), 0o755); err != nil {
		return err
	}
	_ = os.Chown(filepath.Dir(ShellSignersPath), 0, 0)
	_ = os.Chmod(filepath.Dir(ShellSignersPath), 0o755)
	old, _ := os.ReadFile(ShellSignersPath)
	for _, l := range strings.Split(string(old), "\n") {
		if f := strings.Fields(l); len(f) >= 2 && f[1] == b64 {
			return nil // already trusted
		}
	}
	content := string(old)
	if content == "" {
		content = "# NEXUS installations allowed to run person-approved shell commands as root.\n# Managed by nexus-agent install; delete a line to revoke.\n"
	}
	if !strings.HasSuffix(content, "\n") {
		content += "\n"
	}
	label = strings.Map(func(r rune) rune {
		if r < 0x21 || r > 0x7e {
			return '_'
		}
		return r
	}, label)
	content += "ecdsa-p256 " + b64 + " " + label + "\n"
	tmp := ShellSignersPath + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o644); err != nil {
		return err
	}
	_ = os.Chown(tmp, 0, 0)
	return os.Rename(tmp, ShellSignersPath)
}
