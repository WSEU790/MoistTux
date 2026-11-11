//go:build linux

package agent

import (
	"context"
	"crypto/tls"
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/WSEU790/MoistTux/internal/agent/collect"
	"github.com/WSEU790/MoistTux/internal/pki"
	"github.com/WSEU790/MoistTux/internal/protocol"
)

// Version is set at build time with -ldflags "-X .../agent.Version=x.y.z".
var Version = "0.1.0-dev"

type Config struct {
	Listen       string
	StateDir     string
	Interval     time.Duration
	DockerSocket string
	Logger       *log.Logger
}

type Agent struct {
	cfg     Config
	state   *State
	host    *collect.Host
	sampler *Sampler
	procs   *collect.ProcSampler
	docker  *Docker
	priv    *Privileged
	pm      PackageManager
	sd      systemd
	started time.Time
	log     *log.Logger

	certMu  sync.RWMutex
	cert    tls.Certificate

	enrollMu       sync.Mutex
	enrollFailures []time.Time

	srv *http.Server
}

func New(cfg Config) (*Agent, error) {
	if cfg.Interval == 0 {
		cfg.Interval = 5 * time.Second
	}
	if cfg.Logger == nil {
		cfg.Logger = log.New(os.Stderr, "nexus-agent ", log.LstdFlags|log.LUTC)
	}
	st, err := LoadOrCreateState(cfg.StateDir)
	if err != nil {
		return nil, err
	}
	self, err := os.Executable()
	if err != nil {
		return nil, err
	}
	h := collect.Default()
	priv := &Privileged{Self: self, IsRoot: os.Geteuid() == 0}
	a := &Agent{
		cfg: cfg, state: st, host: h, log: cfg.Logger,
		sampler: NewSampler(h, cfg.Interval, int((20*time.Minute)/cfg.Interval)),
		procs:   collect.NewProcSampler(h),
		docker:  NewDocker(cfg.DockerSocket),
		priv:    priv,
		pm:      NewPackageManager(priv),
		started: time.Now().UTC(),
	}
	if err := a.loadCert(); err != nil {
		return nil, err
	}
	return a, nil
}

func (a *Agent) State() *State { return a.state }

func (a *Agent) loadCert() error {
	c, err := a.state.Identity().TLSCertificate()
	if err != nil {
		return err
	}
	a.certMu.Lock()
	a.cert = c
	a.certMu.Unlock()
	return nil
}

// Serve listens until ctx is cancelled.
func (a *Agent) Serve(ctx context.Context) error {
	ln, err := net.Listen("tcp", a.cfg.Listen)
	if err != nil {
		return err
	}
	return a.ServeListener(ctx, ln)
}

func (a *Agent) ServeListener(ctx context.Context, ln net.Listener) error {
	stop := make(chan struct{})
	go a.sampler.Run(stop)
	defer close(stop)

	tlsCfg := &tls.Config{
		MinVersion: tls.VersionTLS13,
		// Certificates are verified by pinned fingerprint in requireClient,
		// not by a CA chain, so the handshake only requests one.
		ClientAuth: tls.RequireAnyClientCert,
		GetCertificate: func(*tls.ClientHelloInfo) (*tls.Certificate, error) {
			a.certMu.RLock()
			defer a.certMu.RUnlock()
			c := a.cert
			return &c, nil
		},
	}
	a.srv = &http.Server{
		Handler:           a.routes(),
		TLSConfig:         tlsCfg,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
		ErrorLog:          log.New(io.Discard, "", 0),
	}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = a.srv.Shutdown(sctx)
	}()
	a.log.Printf("listening on %s (agent %s, fingerprint %s)", ln.Addr(), a.state.AgentID, pki.FormatFingerprint(a.state.Fingerprint())[:47]+"…")
	err := a.srv.ServeTLS(ln, "", "")
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// ---------------------------------------------------------------- plumbing

type ctxKey int

const clientFPKey ctxKey = 1

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code string, err error) {
	writeJSON(w, status, protocol.Error{Error: err.Error(), Code: code})
}

func peerFingerprint(r *http.Request) string {
	if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
		return ""
	}
	return pki.FingerprintDER(r.TLS.PeerCertificates[0].Raw)
}

func (a *Agent) requireClient(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		fp := peerFingerprint(r)
		if fp == "" || !a.state.IsTrusted(fp) {
			writeErr(w, http.StatusUnauthorized, "unauthenticated", errors.New("client certificate is not enrolled with this agent"))
			return
		}
		a.state.TouchClient(fp)
		ctx := context.WithValue(r.Context(), clientFPKey, fp)
		next(w, r.WithContext(ctx))
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(c int) { s.status = c; s.ResponseWriter.WriteHeader(c) }

func (a *Agent) logged(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: 200}
		h.ServeHTTP(rec, r)
		if r.Method != http.MethodGet || rec.status >= 400 {
			a.log.Printf("%s %s %d %s from %s", r.Method, r.URL.Path, rec.status, time.Since(start).Round(time.Millisecond), r.RemoteAddr)
		}
	})
}

func qInt(r *http.Request, k string, def int) int {
	if v, err := strconv.Atoi(r.URL.Query().Get(k)); err == nil {
		return v
	}
	return def
}

func (a *Agent) routes() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("POST /v1/enroll", a.handleEnroll)

	auth := func(pattern string, h http.HandlerFunc) { m.HandleFunc(pattern, a.requireClient(h)) }
	auth("GET /v1/health", a.handleHealth)
	auth("GET /v1/info", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, a.host.SystemInfo()) })
	auth("GET /v1/metrics", a.handleMetrics)
	auth("GET /v1/processes", a.handleProcesses)
	auth("GET /v1/processes/{pid}", a.handleProcess)
	auth("GET /v1/services", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, a.sd.List(r.Context())) })
	auth("GET /v1/services/{unit}", a.handleService)
	auth("GET /v1/services/{unit}/dependencies", a.handleServiceDeps)
	auth("POST /v1/services/{unit}/{action}", a.handleServiceAction)
	auth("GET /v1/logs", a.handleLogs)
	auth("GET /v1/logs/files", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]any{"files": LogFiles(), "journald": journalAvailable()}) })
	auth("GET /v1/docker", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, a.docker.Info(r.Context())) })
	auth("GET /v1/docker/containers", a.dockerList(func(ctx context.Context) (any, error) { return a.docker.Containers(ctx) }))
	auth("GET /v1/docker/images", a.dockerList(func(ctx context.Context) (any, error) { return a.docker.Images(ctx) }))
	auth("GET /v1/docker/volumes", a.dockerList(func(ctx context.Context) (any, error) { return a.docker.Volumes(ctx) }))
	auth("GET /v1/docker/networks", a.dockerList(func(ctx context.Context) (any, error) { return a.docker.Networks(ctx) }))
	auth("GET /v1/docker/compose", a.dockerList(func(ctx context.Context) (any, error) { return a.docker.ComposeProjects(ctx) }))
	auth("GET /v1/docker/containers/{id}", a.handleContainer)
	auth("GET /v1/docker/containers/{id}/logs", a.handleContainerLogs)
	auth("POST /v1/docker/containers/{id}/{action}", a.handleContainerAction)
	auth("GET /v1/storage", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, protocol.StorageOverview{Filesystems: a.host.Filesystems(true), BlockDevices: a.host.BlockDevices()})
	})
	auth("GET /v1/storage/usage", a.handleDirUsage)
	auth("GET /v1/storage/large-files", a.handleLargeFiles)
	auth("GET /v1/network", a.handleNetwork)
	auth("GET /v1/network/firewall", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, Firewall(r.Context(), a.priv)) })
	auth("GET /v1/packages", a.handlePackages)
	auth("GET /v1/packages/updates", a.handleUpdates)
	auth("GET /v1/packages/history", a.handlePkgHistory)
	auth("POST /v1/packages/refresh", a.handlePkgRefresh)
	auth("POST /v1/packages/upgrade", a.handlePkgUpgrade)
	auth("GET /v1/security/ssh", a.handleSSH)
	auth("GET /v1/security/auth", a.handleAuth)
	auth("GET /v1/security/users", a.handleUsers)
	auth("GET /v1/security/posture", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, Posture(r.Context(), a.host, a.priv, a.pm)) })
	auth("POST /v1/probe", a.handleProbe)
	auth("POST /v1/credentials/rotate-client", a.handleRotateClient)
	auth("POST /v1/credentials/rotate-agent", a.handleRotateAgent)
	auth("POST /v1/credentials/activate-agent", a.handleActivateAgent)
	auth("POST /v1/unenroll", a.handleUnenroll)
	auth("POST /v1/system/reboot", a.handleReboot)
	m.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, http.StatusNotFound, "not_found", fmt.Errorf("no such endpoint %s %s", r.Method, r.URL.Path))
	})
	return a.logged(m)
}

// ---------------------------------------------------------------- enrollment

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

func (a *Agent) enrollAllowed() bool {
	a.enrollMu.Lock()
	defer a.enrollMu.Unlock()
	cut := time.Now().Add(-10 * time.Minute)
	kept := a.enrollFailures[:0]
	for _, t := range a.enrollFailures {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	a.enrollFailures = kept
	return len(kept) < 10
}

func (a *Agent) enrollFailed() {
	a.enrollMu.Lock()
	a.enrollFailures = append(a.enrollFailures, time.Now())
	a.enrollMu.Unlock()
	time.Sleep(500 * time.Millisecond) // slow down online guessing
}

func (a *Agent) handleEnroll(w http.ResponseWriter, r *http.Request) {
	if !a.enrollAllowed() {
		writeErr(w, http.StatusTooManyRequests, "rate_limited", errors.New("too many failed enrollment attempts; try again in 10 minutes"))
		return
	}
	var req protocol.EnrollRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&req); err != nil {
		writeErr(w, 400, "bad_request", err)
		return
	}
	peer := peerFingerprint(r)
	if peer == "" {
		a.enrollFailed()
		writeErr(w, 400, "no_client_cert", errors.New("enrollment requires the core to present its client certificate"))
		return
	}
	bodyFP, err := pki.FingerprintPEM([]byte(req.ClientCert))
	if err != nil || !pki.FingerprintsEqual(bodyFP, peer) {
		a.enrollFailed()
		writeErr(w, 400, "cert_mismatch", errors.New("client_cert does not match the TLS client certificate"))
		return
	}
	tok, err := b32.DecodeString(strings.ToUpper(strings.TrimSpace(req.Token)))
	if err != nil {
		a.enrollFailed()
		writeErr(w, 401, "bad_token", errors.New("enrollment token invalid, expired or already used"))
		return
	}
	label := req.Label
	if len(label) > 120 {
		label = label[:120]
	}
	if err := a.state.ConsumeToken(tok, peer, label); err != nil {
		a.enrollFailed()
		writeErr(w, 401, "bad_token", err)
		return
	}
	host, _ := os.Hostname()
	a.log.Printf("enrolled client %q (%s…) from %s", label, peer[:16], r.RemoteAddr)
	writeJSON(w, 200, protocol.EnrollResponse{AgentID: a.state.AgentID, Hostname: host, AgentVersion: Version, Fingerprint: a.state.Fingerprint(), EnrolledAt: time.Now().UTC()})
}

func (a *Agent) handleRotateClient(w http.ResponseWriter, r *http.Request) {
	var req protocol.RotateClientRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&req); err != nil {
		writeErr(w, 400, "bad_request", err)
		return
	}
	newFP, err := pki.FingerprintPEM([]byte(req.NewClientCert))
	if err != nil {
		writeErr(w, 400, "bad_request", err)
		return
	}
	if err := a.state.ReplaceClient(r.Context().Value(clientFPKey).(string), newFP); err != nil {
		writeErr(w, 400, "bad_request", err)
		return
	}
	writeJSON(w, 200, protocol.ActionResult{OK: true, Action: "rotate-client", Target: newFP})
}

func (a *Agent) handleRotateAgent(w http.ResponseWriter, r *http.Request) {
	fp, err := a.state.StageAgentRotation()
	if err != nil {
		writeErr(w, 500, "internal", err)
		return
	}
	writeJSON(w, 200, protocol.RotateAgentResponse{NewFingerprint: fp, ActivatesAfter: "POST /v1/credentials/activate-agent"})
}

func (a *Agent) handleActivateAgent(w http.ResponseWriter, r *http.Request) {
	ok, err := a.state.ActivateRotation()
	if err != nil {
		writeErr(w, 500, "internal", err)
		return
	}
	if !ok {
		writeErr(w, 409, "no_staged_rotation", errors.New("no staged agent certificate; call rotate-agent first"))
		return
	}
	if err := a.loadCert(); err != nil {
		writeErr(w, 500, "internal", err)
		return
	}
	writeJSON(w, 200, protocol.ActionResult{OK: true, Action: "activate-agent", Target: a.state.Fingerprint()})
}

func (a *Agent) handleUnenroll(w http.ResponseWriter, r *http.Request) {
	fp := r.Context().Value(clientFPKey).(string)
	if err := a.state.RemoveClient(fp); err != nil {
		writeErr(w, 500, "internal", err)
		return
	}
	a.log.Printf("client %s… unenrolled itself", fp[:16])
	writeJSON(w, 200, protocol.ActionResult{OK: true, Action: "unenroll", Target: a.state.AgentID})
}

// ---------------------------------------------------------------- handlers

func (a *Agent) handleHealth(w http.ResponseWriter, r *http.Request) {
	ok, msg, seq := a.sampler.Status()
	host, _ := os.Hostname()
	st, _ := a.host.ReadStat()
	h := protocol.Health{
		AgentID: a.state.AgentID, AgentVersion: Version, Hostname: host, BootID: a.host.BootID(),
		BootTime: collect.BootTimeFrom(st), AgentStarted: a.started, Now: time.Now().UTC(),
		SamplerOK: ok, SamplerError: msg, LastSeq: seq, IntervalSec: int(a.sampler.Interval().Seconds()),
		RunningAsRoot: a.priv.IsRoot, AgentRSSBytes: a.host.AgentRSS(),
	}
	if err := a.priv.Check(r.Context()); err != nil {
		h.HelperError = err.Error()
	} else {
		h.HelperOK = true
	}
	caps := []string{"metrics", "processes", "logs", "storage", "network", "security", "probe"}
	if ok, _ := a.sd.available(r.Context()); ok {
		caps = append(caps, "services")
	}
	if di := a.docker.Info(r.Context()); di.Available {
		caps = append(caps, "docker")
	}
	if a.pm.Name() == "apt" {
		caps = append(caps, "packages:apt")
	}
	h.Capabilities = caps
	writeJSON(w, 200, h)
}

func (a *Agent) handleMetrics(w http.ResponseWriter, r *http.Request) {
	since, _ := strconv.ParseUint(r.URL.Query().Get("since"), 10, 64)
	writeJSON(w, 200, protocol.MetricsBatch{
		AgentID: a.state.AgentID, BootID: a.host.BootID(), Samples: a.sampler.Since(since, qInt(r, "max", 0)),
		LastSeq: func() uint64 { _, _, s := a.sampler.Status(); return s }(), Interval: int(a.sampler.Interval().Seconds()),
	})
}

func (a *Agent) memTotalBoot() (uint64, time.Time) {
	m, _ := a.host.ReadMem()
	st, _ := a.host.ReadStat()
	return m.Total, collect.BootTimeFrom(st)
}

func (a *Agent) handleProcesses(w http.ResponseWriter, r *http.Request) {
	mt, bt := a.memTotalBoot()
	limit := qInt(r, "limit", 200)
	if limit <= 0 || limit > 2000 {
		limit = 200
	}
	writeJSON(w, 200, a.procs.List(r.URL.Query().Get("sort"), limit, mt, bt))
}

func (a *Agent) handleProcess(w http.ResponseWriter, r *http.Request) {
	pid, err := strconv.Atoi(r.PathValue("pid"))
	if err != nil || pid <= 0 {
		writeErr(w, 400, "bad_request", errors.New("invalid pid"))
		return
	}
	mt, bt := a.memTotalBoot()
	d, err := a.procs.Detail(pid, mt, bt)
	if err != nil {
		writeErr(w, 404, "not_found", err)
		return
	}
	socks := a.listening(r.Context())
	for _, s := range socks {
		if s.PID == pid {
			d.Listening = append(d.Listening, s)
		}
	}
	writeJSON(w, 200, d)
}

func (a *Agent) listening(ctx context.Context) []protocol.Socket {
	if !a.priv.IsRoot {
		var socks []protocol.Socket
		if err := a.priv.Do(ctx, 20*time.Second, &socks, "sockets"); err == nil {
			return socks
		}
	}
	socks, _ := a.host.ListeningSockets()
	return socks
}

func (a *Agent) serviceDetail(ctx context.Context, unit string) (*protocol.ServiceDetail, error) {
	d, err := a.sd.Detail(ctx, unit)
	if err != nil {
		return nil, err
	}
	errs := 0
	if journalAvailable() {
		if lr, err := queryJournal(ctx, protocol.LogQuery{Unit: unit, Priority: 3, Limit: 500}, time.Now().Add(-time.Hour), time.Time{}, nil); err == nil {
			errs = len(lr.Entries)
		}
	}
	var mine []protocol.Socket
	for _, s := range a.listening(ctx) {
		if s.PID == 0 {
			continue
		}
		if b, err := os.ReadFile(fmt.Sprintf("/proc/%d/cgroup", s.PID)); err == nil && strings.Contains(string(b), "/"+unit) {
			mine = append(mine, s)
		}
	}
	AssessHealth(d, errs, mine)
	return d, nil
}

func (a *Agent) handleService(w http.ResponseWriter, r *http.Request) {
	d, err := a.serviceDetail(r.Context(), r.PathValue("unit"))
	if err != nil {
		writeErr(w, 404, "not_found", err)
		return
	}
	writeJSON(w, 200, d)
}

func (a *Agent) handleServiceDeps(w http.ResponseWriter, r *http.Request) {
	unit := r.PathValue("unit")
	fwd, err := a.sd.Dependencies(r.Context(), unit, false)
	if err != nil {
		writeErr(w, 400, "bad_request", err)
		return
	}
	rev, _ := a.sd.Dependencies(r.Context(), unit, true)
	writeJSON(w, 200, map[string]any{"unit": unit, "requires": fwd, "required_by": rev})
}

func (a *Agent) handleServiceAction(w http.ResponseWriter, r *http.Request) {
	unit, action := r.PathValue("unit"), r.PathValue("action")
	if !systemctlVerbs[action] {
		writeErr(w, 400, "bad_request", fmt.Errorf("unsupported service action %q", action))
		return
	}
	if !reUnit.MatchString(unit) {
		writeErr(w, 400, "bad_request", fmt.Errorf("invalid unit %q", unit))
		return
	}
	var res protocol.CommandResult
	if err := a.priv.Do(r.Context(), 3*time.Minute, &res, "systemctl", action, unit); err != nil {
		writeErr(w, 502, "helper_failed", err)
		return
	}
	out := protocol.ActionResult{OK: res.ExitCode == 0, Action: action, Target: unit, Command: &res}
	if !out.OK {
		out.Message = firstLine(strings.TrimSpace(res.Stderr))
	}
	// Verify: re-read the unit after a short settle period.
	time.Sleep(1500 * time.Millisecond)
	if d, err := a.serviceDetail(r.Context(), unit); err == nil {
		out.After = d
	}
	writeJSON(w, 200, out)
}

func (a *Agent) handleLogs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	lq := protocol.LogQuery{Unit: q.Get("unit"), Since: q.Get("since"), Until: q.Get("until"), Grep: q.Get("grep"), File: q.Get("file"), Priority: qInt(r, "priority", 0), Limit: qInt(r, "limit", 300)}
	res, err := QueryLogs(r.Context(), a.priv, lq)
	if err != nil {
		writeErr(w, 400, "bad_request", err)
		return
	}
	writeJSON(w, 200, res)
}

func (a *Agent) dockerList(f func(context.Context) (any, error)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		v, err := f(r.Context())
		if err != nil {
			writeErr(w, 502, "docker_unavailable", err)
			return
		}
		writeJSON(w, 200, v)
	}
}

func (a *Agent) handleContainer(w http.ResponseWriter, r *http.Request) {
	d, err := a.docker.Inspect(r.Context(), r.PathValue("id"), r.URL.Query().Get("stats") != "0")
	if err != nil {
		var de *dockerAPIError
		if errors.As(err, &de) && de.Status == 404 {
			writeErr(w, 404, "not_found", err)
			return
		}
		writeErr(w, 502, "docker_unavailable", err)
		return
	}
	writeJSON(w, 200, d)
}

func (a *Agent) handleContainerLogs(w http.ResponseWriter, r *http.Request) {
	entries, err := a.docker.Logs(r.Context(), r.PathValue("id"), qInt(r, "tail", 300), r.URL.Query().Get("since"))
	if err != nil {
		writeErr(w, 502, "docker_unavailable", err)
		return
	}
	if entries == nil {
		entries = []protocol.LogEntry{}
	}
	writeJSON(w, 200, protocol.LogResult{Source: "docker:" + r.PathValue("id"), Entries: entries})
}

func (a *Agent) handleContainerAction(w http.ResponseWriter, r *http.Request) {
	id, action := r.PathValue("id"), r.PathValue("action")
	out := protocol.ActionResult{Action: action, Target: id}
	if err := a.docker.Action(r.Context(), id, action); err != nil {
		out.Message = err.Error()
		writeJSON(w, 200, out)
		return
	}
	out.OK = true
	time.Sleep(time.Second)
	if d, err := a.docker.Inspect(r.Context(), id, false); err == nil {
		out.After = d
	}
	writeJSON(w, 200, out)
}

func (a *Agent) handleDirUsage(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		path = "/"
	}
	depth := qInt(r, "depth", 1)
	if err := validateScanPath(path); err != nil {
		writeErr(w, 400, "bad_request", err)
		return
	}
	if depth < 0 || depth > 4 {
		depth = 1
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	if !a.priv.IsRoot && r.URL.Query().Get("privileged") == "1" {
		var res protocol.DirUsageResult
		if err := a.priv.Do(ctx, 2*time.Minute, &res, "du", path, strconv.Itoa(depth)); err != nil {
			writeErr(w, 502, "helper_failed", err)
			return
		}
		writeJSON(w, 200, res)
		return
	}
	writeJSON(w, 200, DirectoryUsage(ctx, path, depth, false))
}

func (a *Agent) handleLargeFiles(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		path = "/"
	}
	if err := validateScanPath(path); err != nil {
		writeErr(w, 400, "bad_request", err)
		return
	}
	minMB := qInt(r, "min_mb", 100)
	if minMB < 1 {
		minMB = 1
	}
	limit := qInt(r, "limit", 50)
	if limit <= 0 || limit > 500 {
		limit = 50
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	if !a.priv.IsRoot && r.URL.Query().Get("privileged") == "1" {
		var res protocol.LargeFilesResult
		if err := a.priv.Do(ctx, 2*time.Minute, &res, "largefiles", path, strconv.FormatUint(uint64(minMB)<<20, 10), strconv.Itoa(limit)); err != nil {
			writeErr(w, 502, "helper_failed", err)
			return
		}
		writeJSON(w, 200, res)
		return
	}
	writeJSON(w, 200, FindLargeFiles(ctx, path, uint64(minMB)<<20, limit, false))
}

func (a *Agent) handleNetwork(w http.ResponseWriter, r *http.Request) {
	socks := a.listening(r.Context())
	dns, search := a.host.DNS()
	ov := protocol.NetworkOverview{Interfaces: a.host.Interfaces(), Routes: a.host.Routes(), DNS: dns, DNSSearch: search, Listening: socks, Established: a.host.TCPEstablished()}
	unresolved := 0
	for _, s := range socks {
		if s.PID == 0 {
			unresolved++
		}
	}
	if unresolved > 0 {
		ov.Partial = fmt.Sprintf("%d listening sockets could not be mapped to a process (insufficient privileges or kernel sockets)", unresolved)
	}
	writeJSON(w, 200, ov)
}

func (a *Agent) handlePackages(w http.ResponseWriter, r *http.Request) {
	l, err := a.pm.Installed(r.Context())
	if err != nil {
		writeErr(w, 501, "unsupported", err)
		return
	}
	if q := strings.ToLower(r.URL.Query().Get("q")); q != "" {
		f := l.Packages[:0]
		for _, p := range l.Packages {
			if strings.Contains(p.Name, q) || strings.Contains(strings.ToLower(p.Summary), q) {
				f = append(f, p)
			}
		}
		l.Packages = f
	}
	writeJSON(w, 200, l)
}

func (a *Agent) handleUpdates(w http.ResponseWriter, r *http.Request) {
	c, err := a.pm.Updates(r.Context())
	if err != nil {
		writeErr(w, 501, "unsupported", err)
		return
	}
	writeJSON(w, 200, c)
}

func (a *Agent) handlePkgHistory(w http.ResponseWriter, r *http.Request) {
	h, err := a.pm.History(qInt(r, "limit", 50))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			writeJSON(w, 200, []protocol.PackageHistoryEntry{})
			return
		}
		writeErr(w, 501, "unsupported", err)
		return
	}
	writeJSON(w, 200, h)
}

func (a *Agent) handlePkgRefresh(w http.ResponseWriter, r *http.Request) {
	res, err := a.pm.Refresh(r.Context())
	if err != nil {
		writeErr(w, 502, "helper_failed", err)
		return
	}
	writeJSON(w, 200, protocol.ActionResult{OK: res.ExitCode == 0, Action: "refresh", Target: a.pm.Name(), Command: &res})
}

func (a *Agent) handlePkgUpgrade(w http.ResponseWriter, r *http.Request) {
	var req protocol.UpgradeRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 256<<10)).Decode(&req); err != nil {
		writeErr(w, 400, "bad_request", err)
		return
	}
	res, err := a.pm.Upgrade(r.Context(), req)
	if err != nil {
		writeErr(w, 502, "helper_failed", err)
		return
	}
	out := protocol.ActionResult{OK: res.ExitCode == 0, Action: "upgrade", Target: strings.Join(req.Packages, " "), Command: &res}
	if !req.DryRun {
		if c, err := a.pm.Updates(r.Context()); err == nil {
			out.After = c
		}
	}
	writeJSON(w, 200, out)
}

func (a *Agent) handleSSH(w http.ResponseWriter, r *http.Request) {
	c, err := SSHConfig(r.Context(), a.priv)
	if err != nil {
		writeErr(w, 502, "unavailable", err)
		return
	}
	writeJSON(w, 200, c)
}

func (a *Agent) handleAuth(w http.ResponseWriter, r *http.Request) {
	hours := qInt(r, "hours", 24)
	if hours <= 0 || hours > 24*31 {
		hours = 24
	}
	s, err := AuthEvents(r.Context(), a.priv, time.Duration(hours)*time.Hour)
	if err != nil {
		writeErr(w, 502, "unavailable", err)
		return
	}
	writeJSON(w, 200, s)
}

func (a *Agent) handleUsers(w http.ResponseWriter, r *http.Request) {
	u, err := Users(a.host)
	if err != nil {
		writeErr(w, 500, "internal", err)
		return
	}
	writeJSON(w, 200, u)
}

func (a *Agent) handleProbe(w http.ResponseWriter, r *http.Request) {
	var req protocol.ProbeRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&req); err != nil {
		writeErr(w, 400, "bad_request", err)
		return
	}
	writeJSON(w, 200, Probe(r.Context(), req))
}

func (a *Agent) handleReboot(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Confirm string `json:"confirm"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&req)
	host, _ := os.Hostname()
	if req.Confirm != host {
		writeErr(w, 400, "confirmation_required", fmt.Errorf("reboot requires confirm=%q", host))
		return
	}
	var res protocol.CommandResult
	if err := a.priv.Do(r.Context(), time.Minute, &res, "reboot"); err != nil {
		writeErr(w, 502, "helper_failed", err)
		return
	}
	a.log.Printf("reboot requested by %s", r.Context().Value(clientFPKey).(string)[:16])
	writeJSON(w, 200, protocol.ActionResult{OK: res.ExitCode == 0, Action: "reboot", Target: host, Command: &res})
}
