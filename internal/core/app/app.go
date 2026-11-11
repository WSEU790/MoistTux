// Package app wires the NEXUS core subsystems together.
package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/WSEU790/MoistTux/internal/core/ai"
	"github.com/WSEU790/MoistTux/internal/core/api"
	"github.com/WSEU790/MoistTux/internal/core/deploy"
	"github.com/WSEU790/MoistTux/internal/core/events"
	"github.com/WSEU790/MoistTux/internal/core/fleet"
	"github.com/WSEU790/MoistTux/internal/core/identity"
	"github.com/WSEU790/MoistTux/internal/core/monitor"
	"github.com/WSEU790/MoistTux/internal/core/notify"
	"github.com/WSEU790/MoistTux/internal/core/secrets"
	"github.com/WSEU790/MoistTux/internal/core/store"
	"github.com/WSEU790/MoistTux/internal/core/webui"
)

// Version is set at build time.
var Version = "0.1.0-dev"

type Options struct {
	DataDir   string
	Listen    string // default 127.0.0.1:0 (random free port)
	DevOrigin string
	LogOutput io.Writer
}

type App struct {
	Store    *store.Store
	Bus      *events.Bus
	Fleet    *fleet.Manager
	Monitor  *monitor.Engine
	Notifier *notify.Notifier
	Deploy   *deploy.Manager
	AI       *ai.Engine
	API      *api.Server
	Log      *log.Logger

	URL      string // launch URL including the one-time token
	Addr     string
	Token    string
	srv      *http.Server
	ln       net.Listener
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	idMu     sync.RWMutex
	ident    *identity.Identity
}

// DefaultDataDir returns %LOCALAPPDATA%\NEXUS on Windows and the XDG data
// directory elsewhere.
func DefaultDataDir() string {
	if runtime.GOOS == "windows" {
		if d := os.Getenv("LOCALAPPDATA"); d != "" {
			return filepath.Join(d, "NEXUS")
		}
	}
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, "nexus")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "nexus")
}

func Start(ctx context.Context, opts Options) (*App, error) {
	if opts.DataDir == "" {
		opts.DataDir = DefaultDataDir()
	}
	if opts.Listen == "" {
		opts.Listen = "127.0.0.1:0"
	}
	if opts.LogOutput == nil {
		opts.LogOutput = os.Stderr
	}
	if host, _, err := net.SplitHostPort(opts.Listen); err != nil || (host != "127.0.0.1" && host != "localhost" && host != "::1") {
		return nil, errors.New("the NEXUS console only listens on the loopback interface")
	}
	if err := os.MkdirAll(opts.DataDir, 0o700); err != nil {
		return nil, err
	}
	logFile, err := os.OpenFile(filepath.Join(opts.DataDir, "nexus.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	logger := log.New(io.MultiWriter(opts.LogOutput, logFile), "nexus ", log.LstdFlags|log.LUTC)

	prot, err := secrets.Default(opts.DataDir)
	if err != nil {
		return nil, fmt.Errorf("secret store: %w", err)
	}
	st, err := store.Open(filepath.Join(opts.DataDir, "nexus.db"))
	if err != nil {
		return nil, fmt.Errorf("database: %w", err)
	}
	host, _ := os.Hostname()
	id, err := identity.LoadOrCreate(opts.DataDir, prot, host)
	if err != nil {
		st.Close()
		return nil, err
	}
	a := &App{Store: st, Bus: events.New(), Log: logger, ident: id}
	identFn := func() *identity.Identity { a.idMu.RLock(); defer a.idMu.RUnlock(); return a.ident }

	if n, err := st.RecoverInterrupted(ctx); err == nil && n > 0 {
		logger.Printf("marked %d interrupted executions as failed (outcome unknown)", n)
		_ = st.Audit(ctx, "nexus", "recovery.interrupted_executions", "executions", map[string]int64{"count": n})
	}

	a.Fleet = fleet.New(st, a.Bus, identFn, fleet.DefaultConfig(), logger)
	a.Notifier = notify.New(st, a.Bus, prot, logger)
	a.Monitor = monitor.New(st, a.Bus, a.Notifier, monitor.DefaultConfig(), logger)
	api.LoadMonitoringSettings(st, a.Monitor, a.Fleet)
	reg := ai.BuildRegistry()
	pol := &ai.PolicyStore{}
	*pol = ai.NewPolicyStore(st)
	deps := &ai.Deps{Store: st, Fleet: a.Fleet, Bus: a.Bus, Log: logger, Registry: reg, Policies: pol}
	a.AI = ai.NewEngine(deps)
	a.Monitor.SetInvestigator(func(ctx context.Context, inc *store.Incident) {
		p, _ := pol.Effective(ctx, inc.ServerID)
		if p.MonitoringLevel == "read_only" {
			return
		}
		a.AI.Investigate(ctx, inc)
	})
	a.Deploy = deploy.NewManager(a.Fleet, st, a.Bus, prot, logger)
	a.Fleet.SetHooks(fleet.Hooks{OnSamples: a.Monitor.OnSamples, OnInventory: a.Monitor.OnInventory, OnStatus: a.Monitor.OnStatus, OnReboot: a.Monitor.OnReboot})

	tok := make([]byte, 32)
	if _, err := rand.Read(tok); err != nil {
		return nil, err
	}
	a.Token = hex.EncodeToString(tok)
	a.API = &api.Server{Store: st, Bus: a.Bus, Fleet: a.Fleet, Monitor: a.Monitor, Notifier: a.Notifier, Deploy: a.Deploy, AI: a.AI, Registry: reg, Policies: pol,
		Identity: identFn, Log: logger, UI: webui.FS(), Token: a.Token, Version: Version, DataDir: opts.DataDir, Started: time.Now().UTC(), DevOrigin: opts.DevOrigin}

	ln, err := net.Listen("tcp", opts.Listen)
	if err != nil {
		return nil, err
	}
	a.ln = ln
	a.Addr = ln.Addr().String()
	a.API.SetListenAddr(a.Addr)
	a.URL = "http://" + a.Addr + "/?token=" + a.Token

	cctx, cancel := context.WithCancel(ctx)
	a.cancel = cancel
	if err := a.Fleet.Start(cctx); err != nil {
		cancel()
		return nil, err
	}
	a.srv = &http.Server{Handler: a.API.Handler(), ReadHeaderTimeout: 10 * time.Second, ErrorLog: log.New(io.Discard, "", 0)}
	a.wg.Add(2)
	go func() { defer a.wg.Done(); _ = a.srv.Serve(ln) }()
	go func() { defer a.wg.Done(); a.maintenance(cctx) }()
	_ = st.Audit(ctx, "nexus", "core.start", host, map[string]string{"version": Version, "fingerprint": id.Fingerprint})
	logger.Printf("NEXUS %s ready on http://%s (data %s)", Version, a.Addr, opts.DataDir)
	return a, nil
}

func (a *App) maintenance(ctx context.Context) {
	fast := time.NewTicker(30 * time.Second)
	slow := time.NewTicker(5 * time.Minute)
	defer fast.Stop()
	defer slow.Stop()
	_ = a.Store.Rollup(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-fast.C:
			a.AI.ExpireApprovals(ctx)
			a.Monitor.Sweep(ctx)
		case <-slow.C:
			if err := a.Store.Rollup(ctx); err != nil {
				a.Log.Printf("telemetry rollup: %v", err)
			}
		}
	}
}

func (a *App) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = a.srv.Shutdown(ctx)
	a.cancel()
	a.wg.Wait()
	return a.Store.Close()
}
