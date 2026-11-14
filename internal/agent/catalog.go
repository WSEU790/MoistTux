//go:build linux

package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/WSEU790/MoistTux/internal/protocol"
)

// handleCatalogRun runs one command from the shared catalog. Arguments are
// validated here and, for commands that need root, again inside the helper.
func (a *Agent) handleCatalogRun(w http.ResponseWriter, r *http.Request) {
	var req protocol.CatalogRunRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 256<<10)).Decode(&req); err != nil {
		writeErr(w, 400, "bad_request", err)
		return
	}
	res, err := runCatalog(r.Context(), a.priv, req)
	if err != nil {
		var ve *catalogError
		if errors.As(err, &ve) {
			writeErr(w, 400, "invalid_command", err)
			return
		}
		writeErr(w, 502, "helper_failed", err)
		return
	}
	writeJSON(w, 200, res)
}

type catalogError struct{ error }

func runCatalog(ctx context.Context, priv *Privileged, req protocol.CatalogRunRequest) (protocol.CommandResult, error) {
	c, ok := protocol.FindCommand(req.ID)
	if !ok {
		return protocol.CommandResult{}, &catalogError{errors.New("unknown catalog command " + req.ID)}
	}
	argv, err := c.Build(req.Args)
	if err != nil {
		return protocol.CommandResult{}, &catalogError{err}
	}
	timeout := time.Duration(c.TimeoutSec) * time.Second
	if !c.Root {
		return runCmd(ctx, timeout, c.Env, argv[0], argv[1:]...), nil
	}
	var res protocol.CommandResult
	err = priv.Do(ctx, timeout+30*time.Second, &res, "catalog", append([]string{c.ID}, protocol.EncodeArgs(req.Args)...)...)
	return res, err
}

// validateCatalogHelper checks a "catalog" helper invocation (as root).
func validateCatalogHelper(args []string) (*protocol.CatalogCommand, []string, error) {
	if len(args) < 1 {
		return nil, nil, helperUsage("catalog")
	}
	c, ok := protocol.FindCommand(args[0])
	if !ok || !c.Root {
		return nil, nil, errors.New("helper: not a root catalog command: " + args[0])
	}
	kv, err := protocol.DecodeArgs(args[1:])
	if err != nil {
		return nil, nil, err
	}
	argv, err := c.Build(kv)
	if err != nil {
		return nil, nil, err
	}
	return c, argv, nil
}
