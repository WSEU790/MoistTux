//go:build linux

package agent

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/WSEU790/MoistTux/internal/protocol"
)

// Upgrades over the agent connection.
//
// NEXUS uploads a new agent binary together with an UpgradeGrant signed by
// its identity key (the same trust as approved shell commands): the binary's
// SHA-256, this machine's ID, a nonce and a short expiry. The root helper
// copies the upload into a root-only directory, hashes the copy, verifies the
// grant against /etc/nexus-agent/shell-signers and only then runs the new
// binary's `install` (which rewrites the unit, sudoers rule and helper) with
// a non-blocking restart. The agent's own account can therefore not install
// a binary of its choosing.

const upgradeDir = "/var/lib/nexus-helper/upgrade"

func (a *Agent) handleSelfUpgrade(w http.ResponseWriter, r *http.Request) {
	var req protocol.SelfUpgradeRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 96<<20)).Decode(&req); err != nil {
		writeErr(w, 400, "bad_request", err)
		return
	}
	bin, err := base64.StdEncoding.DecodeString(req.Binary)
	if err != nil || len(bin) < 1<<20 {
		writeErr(w, 400, "bad_request", errors.New("missing or malformed agent binary"))
		return
	}
	f, err := os.CreateTemp(a.cfg.StateDir, "upgrade-*.bin")
	if err != nil {
		writeErr(w, 500, "io", err)
		return
	}
	path := f.Name()
	defer os.Remove(path)
	if _, err := f.Write(bin); err != nil {
		f.Close()
		writeErr(w, 500, "io", err)
		return
	}
	f.Close()
	_ = os.Chmod(path, 0o644) // the root helper reads it
	var res protocol.CommandResult
	if err := a.priv.Do(r.Context(), 3*time.Minute, &res, "self-upgrade", path, req.Grant, req.Sig); err != nil {
		writeErr(w, 403, "upgrade_refused", err)
		return
	}
	writeJSON(w, 200, res)
}

var reListen = regexp.MustCompile(`--listen\s+(\S+)`)
var reInterval = regexp.MustCompile(`--interval\s+(\S+)`)

// execSelfUpgrade runs inside the root helper.
func execSelfUpgrade(ctx context.Context, path, token, sig string) (any, error) {
	raw, err := base64.StdEncoding.DecodeString(token)
	if err != nil || len(raw) > 8<<10 {
		return nil, errors.New("malformed upgrade grant")
	}
	var g protocol.UpgradeGrant
	if err := json.Unmarshal(raw, &g); err != nil || g.V != 1 || len(g.SHA256) != 64 {
		return nil, errors.New("invalid upgrade grant")
	}
	if err := verifySigned(raw, sig, g.MachineID, g.Expires, ShellSignersPath, "/etc/machine-id", time.Now()); err != nil {
		return nil, err
	}
	if err := consumeNonce(shellNonceDir, g.Nonce, g.Expires); err != nil {
		return nil, err
	}
	// Copy first, then hash the copy: the uploaded file stays writable by
	// the agent's account, the copy does not.
	if err := os.MkdirAll(upgradeDir, 0o700); err != nil {
		return nil, err
	}
	if old, err := os.ReadDir(upgradeDir); err == nil { // leftovers of interrupted upgrades
		for _, e := range old {
			_ = os.Remove(filepath.Join(upgradeDir, e.Name()))
		}
	}
	dst := filepath.Join(upgradeDir, "nexus-agent-"+g.Nonce)
	defer os.Remove(dst)
	if err := copyFile(path, dst, 0o700); err != nil {
		return nil, fmt.Errorf("copy upload: %w", err)
	}
	b, err := os.ReadFile(dst)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(b)
	if hex.EncodeToString(sum[:]) != strings.ToLower(g.SHA256) {
		return nil, errors.New("refused: the uploaded agent does not match the signed checksum")
	}
	// Keep the current listen address and interval.
	unit, _ := os.ReadFile(UnitPath)
	args := []string{"install", "--upgrade"}
	if m := reListen.FindSubmatch(unit); m != nil {
		args = append(args, "--listen", string(m[1]))
	}
	if m := reInterval.FindSubmatch(unit); m != nil {
		args = append(args, "--interval", string(m[1]))
	}
	return runCmdPath(ctx, 3*time.Minute, dst, args...), nil
}
