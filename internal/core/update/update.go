// Package update keeps the NEXUS desktop app current.
//
// Every push to the release branches builds a GitHub Release containing the
// Windows installer and a manifest signed with the project's ed25519 release
// key. NEXUS trusts an update only if:
//
//   - the manifest signature verifies against the public key compiled into
//     this build (a compromised download host cannot forge it),
//   - the manifest version matches the release tag and is newer than the
//     running version (no downgrades or replays), and
//   - the downloaded installer matches the manifest's size and SHA-256.
//
// Updates download in the background and install on the next start, or
// immediately when the operator chooses "Restart & update".
package update

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Set at build time (-ldflags -X). Without a public key updates are disabled.
var (
	PublicKey = ""                // base64 ed25519 public key
	Repo      = "WSEU790/MoistTux" // GitHub owner/name serving releases
)

const (
	ManifestName  = "nexus-update.json"
	SignatureName = "nexus-update.json.sig"
	maxManifest   = 64 << 10
	maxInstaller  = 512 << 20
)

// Manifest describes one release.
type Manifest struct {
	Product  string           `json:"product"`
	Version  string           `json:"version"`
	Released time.Time        `json:"released"`
	Commit   string           `json:"commit"`
	Notes    string           `json:"notes"`
	Files    map[string]Asset `json:"files"` // platform key → installer
}

type Asset struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}

// Platform is the manifest key for this build.
func Platform() string { return runtime.GOOS + "-" + runtime.GOARCH }

// ---------------------------------------------------------------- versions

var reVer = regexp.MustCompile(`^v?(\d+)(?:\.(\d+))?(?:\.(\d+))?(?:\.(\d+))?(.*)$`)

// Compare returns -1, 0 or 1. Pre-release or dirty suffixes ("-dev",
// "-dirty") sort below the same numeric version.
func Compare(a, b string) int {
	pa, sa := parse(a)
	pb, sb := parse(b)
	for i := 0; i < 4; i++ {
		if pa[i] != pb[i] {
			if pa[i] < pb[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case sa == sb:
		return 0
	case sa == "":
		return 1
	case sb == "":
		return -1
	case sa < sb:
		return -1
	}
	return 1
}

func parse(v string) ([4]int, string) {
	var out [4]int
	m := reVer.FindStringSubmatch(strings.TrimSpace(v))
	if m == nil {
		return out, v
	}
	for i := 0; i < 4; i++ {
		out[i], _ = strconv.Atoi(m[i+1])
	}
	return out, strings.TrimSpace(m[5])
}

// ---------------------------------------------------------------- signatures

// Verify checks a base64 signature over manifest bytes with the given key.
func Verify(pubB64 string, manifest, sigB64 []byte) error {
	pub, err := base64.StdEncoding.DecodeString(strings.TrimSpace(pubB64))
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return errors.New("update: invalid release public key in this build")
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sigB64)))
	if err != nil || len(sig) != ed25519.SignatureSize {
		return errors.New("update: malformed release signature")
	}
	if !ed25519.Verify(ed25519.PublicKey(pub), manifest, sig) {
		return errors.New("update: release signature does not verify — refusing the update")
	}
	return nil
}

// Sign produces the base64 signature for a manifest (used by the release tool).
func Sign(privB64 string, manifest []byte) (string, error) {
	priv, err := base64.StdEncoding.DecodeString(strings.TrimSpace(privB64))
	if err != nil || len(priv) != ed25519.PrivateKeySize {
		return "", errors.New("invalid release signing key")
	}
	return base64.StdEncoding.EncodeToString(ed25519.Sign(ed25519.PrivateKey(priv), manifest)), nil
}

// ---------------------------------------------------------------- state

type Settings struct {
	AutoCheck   bool `json:"auto_check"`
	AutoInstall bool `json:"auto_install"` // install downloaded updates on the next start
}

func DefaultSettings() Settings { return Settings{AutoCheck: true, AutoInstall: true} }

// Status is what the UI shows.
type Status struct {
	Enabled     bool       `json:"enabled"`
	Disabled    string     `json:"disabled_reason,omitempty"`
	Current     string     `json:"current"`
	Latest      string     `json:"latest,omitempty"`
	State       string     `json:"state"` // idle | checking | no_release | up_to_date | available | downloading | ready | error
	Notes       string     `json:"notes,omitempty"`
	Released    *time.Time `json:"released,omitempty"`
	Progress    float64    `json:"progress,omitempty"`
	Error       string     `json:"error,omitempty"`
	LastCheck   *time.Time `json:"last_check,omitempty"`
	CanInstall  bool       `json:"can_install"` // this platform can apply updates itself
	ReleaseURL  string     `json:"release_url,omitempty"`
	Settings    Settings   `json:"settings"`
}

// Pending is written once an installer has been downloaded and verified.
type Pending struct {
	Version string `json:"version"`
	Path    string `json:"path"`
	SHA256  string `json:"sha256"`
}

type Updater struct {
	current string
	dir     string // <data>/updates
	api     string // GitHub API base (overridable in tests)
	pub     string
	hc      *http.Client
	log     *log.Logger
	notify  func(Status)

	mu       sync.Mutex
	st       Status
	manifest *Manifest
	assetURL string
	running  bool
}

func New(current, dataDir string, logger *log.Logger, notify func(Status)) *Updater {
	u := &Updater{current: current, dir: filepath.Join(dataDir, "updates"), api: "https://api.github.com", pub: PublicKey,
		hc: &http.Client{Timeout: 30 * time.Minute}, log: logger, notify: notify}
	// Test hook: point the release lookup at another GitHub-compatible API.
	// Signature verification against the built-in key still applies.
	if v := os.Getenv("NEXUS_UPDATE_API"); strings.HasPrefix(v, "http://127.0.0.1:") || strings.HasPrefix(v, "https://") {
		u.api = strings.TrimRight(v, "/")
	}
	u.st = Status{Current: current, State: "idle", CanInstall: canInstall(), Settings: DefaultSettings()}
	switch {
	case u.pub == "":
		u.st.Disabled = "this is a development build without a release key; updates are disabled"
	case strings.Contains(current, "dev"):
		u.st.Disabled = "development build; install a release build to receive updates"
	default:
		u.st.Enabled = true
	}
	return u
}

// SetAPIForTest points the updater at a fake GitHub.
func (u *Updater) SetAPIForTest(base, pub string) {
	u.api, u.pub = base, pub
	u.st.Enabled, u.st.Disabled, u.st.CanInstall = true, "", true
}

func (u *Updater) SetSettings(s Settings) {
	u.mu.Lock()
	u.st.Settings = s
	u.mu.Unlock()
	u.emit()
}

func (u *Updater) Status() Status {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.st
}

func (u *Updater) set(f func(*Status)) {
	u.mu.Lock()
	f(&u.st)
	u.mu.Unlock()
	u.emit()
}

func (u *Updater) emit() {
	if u.notify != nil {
		u.notify(u.Status())
	}
}

// Run checks shortly after start and then every interval while ctx lives.
func (u *Updater) Run(ctx context.Context, first, every time.Duration) {
	if v, err := time.ParseDuration(os.Getenv("NEXUS_UPDATE_FIRST_CHECK")); err == nil && v > 0 {
		first = v // test hook
	}
	t := time.NewTimer(first)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if s := u.Status(); s.Enabled && s.Settings.AutoCheck {
				if err := u.CheckAndDownload(ctx); err != nil && u.log != nil {
					u.log.Printf("update: %v", err)
				}
			}
			t.Reset(every)
		}
	}
}

// CheckAndDownload looks for a newer signed release and downloads it.
func (u *Updater) CheckAndDownload(ctx context.Context) error {
	u.mu.Lock()
	if !u.st.Enabled {
		u.mu.Unlock()
		return errors.New(u.st.Disabled)
	}
	if u.running {
		u.mu.Unlock()
		return nil
	}
	u.running = true
	u.mu.Unlock()
	defer func() { u.mu.Lock(); u.running = false; u.mu.Unlock() }()

	u.set(func(s *Status) { s.State, s.Error = "checking", "" })
	m, url, releaseURL, err := u.latest(ctx)
	now := time.Now().UTC()
	if errors.Is(err, errNoRelease) {
		u.set(func(s *Status) { s.State, s.LastCheck = "no_release", &now })
		return nil
	}
	if err != nil {
		u.set(func(s *Status) { s.State, s.Error, s.LastCheck = "error", err.Error(), &now })
		return err
	}
	rel := m.Released
	u.set(func(s *Status) {
		s.LastCheck, s.Latest, s.Notes, s.Released, s.ReleaseURL = &now, m.Version, m.Notes, &rel, releaseURL
	})
	if Compare(m.Version, u.current) <= 0 {
		u.set(func(s *Status) { s.State = "up_to_date" })
		u.cleanup("")
		return nil
	}
	if p, err := u.pending(); err == nil && p.Version == m.Version {
		u.set(func(s *Status) { s.State = "ready" })
		return nil
	}
	u.mu.Lock()
	u.manifest, u.assetURL = m, url
	u.mu.Unlock()
	if !u.st.CanInstall {
		u.set(func(s *Status) { s.State = "available" })
		return nil
	}
	return u.download(ctx, m, url)
}

type ghRelease struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
	Draft   bool   `json:"draft"`
	Assets  []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
		Size int64  `json:"size"`
	} `json:"assets"`
}

func (u *Updater) get(ctx context.Context, url string, max int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "NEXUS-Updater/"+u.current)
	req.Header.Set("Accept", "application/vnd.github+json, application/octet-stream")
	resp, err := u.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return nil, errNoRelease
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > max {
		return nil, fmt.Errorf("GET %s: response too large", url)
	}
	return b, nil
}

var errNoRelease = errors.New("no release has been published yet")

func (u *Updater) latest(ctx context.Context) (*Manifest, string, string, error) {
	raw, err := u.get(ctx, fmt.Sprintf("%s/repos/%s/releases/latest", u.api, Repo), 1<<20)
	if err != nil {
		return nil, "", "", fmt.Errorf("checking for updates: %w", err)
	}
	var rel ghRelease
	if err := json.Unmarshal(raw, &rel); err != nil {
		return nil, "", "", fmt.Errorf("unexpected release data: %w", err)
	}
	urls := map[string]string{}
	sizes := map[string]int64{}
	for _, a := range rel.Assets {
		urls[a.Name], sizes[a.Name] = a.URL, a.Size
	}
	if urls[ManifestName] == "" || urls[SignatureName] == "" {
		return nil, "", "", fmt.Errorf("release %s has no signed update manifest", rel.TagName)
	}
	mb, err := u.get(ctx, urls[ManifestName], maxManifest)
	if err != nil {
		return nil, "", "", err
	}
	sb, err := u.get(ctx, urls[SignatureName], 4096)
	if err != nil {
		return nil, "", "", err
	}
	if err := Verify(u.pub, mb, sb); err != nil {
		return nil, "", "", err
	}
	var m Manifest
	if err := json.Unmarshal(mb, &m); err != nil {
		return nil, "", "", fmt.Errorf("invalid manifest: %w", err)
	}
	if m.Product != "NEXUS" || strings.TrimPrefix(rel.TagName, "v") != m.Version {
		return nil, "", "", fmt.Errorf("manifest version %q does not match release %q", m.Version, rel.TagName)
	}
	a, ok := m.Files[Platform()]
	if !ok {
		return &m, "", rel.HTMLURL, nil
	}
	if urls[a.Name] == "" || sizes[a.Name] != a.Size {
		return nil, "", "", fmt.Errorf("release %s is missing %s or its size differs from the signed manifest", rel.TagName, a.Name)
	}
	return &m, urls[a.Name], rel.HTMLURL, nil
}

type countingWriter struct {
	w     io.Writer
	n     int64
	total int64
	tick  func(float64)
	last  time.Time
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	if time.Since(c.last) > 400*time.Millisecond && c.total > 0 {
		c.last = time.Now()
		c.tick(float64(c.n) / float64(c.total))
	}
	return n, err
}

func (u *Updater) download(ctx context.Context, m *Manifest, url string) error {
	a, ok := m.Files[Platform()]
	if !ok || url == "" {
		u.set(func(s *Status) { s.State = "available" })
		return nil
	}
	if a.Size <= 0 || a.Size > maxInstaller || !regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`).MatchString(a.Name) {
		err := errors.New("signed manifest has an invalid installer entry")
		u.set(func(s *Status) { s.State, s.Error = "error", err.Error() })
		return err
	}
	u.set(func(s *Status) { s.State, s.Progress = "downloading", 0 })
	if err := os.MkdirAll(u.dir, 0o700); err != nil {
		return err
	}
	final := filepath.Join(u.dir, a.Name)
	tmp := final + ".part"
	err := func() error {
		req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
		if err != nil {
			return err
		}
		req.Header.Set("User-Agent", "NEXUS-Updater/"+u.current)
		resp, err := u.hc.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return fmt.Errorf("download: HTTP %d", resp.StatusCode)
		}
		f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
		if err != nil {
			return err
		}
		h := sha256.New()
		cw := &countingWriter{w: io.MultiWriter(f, h), total: a.Size, tick: func(p float64) { u.set(func(s *Status) { s.Progress = p }) }}
		_, cerr := io.Copy(cw, io.LimitReader(resp.Body, a.Size+1))
		if err := f.Close(); cerr == nil {
			cerr = err
		}
		if cerr != nil {
			return cerr
		}
		if cw.n != a.Size {
			return fmt.Errorf("download size %d does not match signed size %d", cw.n, a.Size)
		}
		if got := hex.EncodeToString(h.Sum(nil)); !strings.EqualFold(got, a.SHA256) {
			return fmt.Errorf("installer checksum %s does not match the signed manifest", got[:16])
		}
		return os.Rename(tmp, final)
	}()
	if err != nil {
		_ = os.Remove(tmp)
		u.set(func(s *Status) { s.State, s.Error = "error", err.Error() })
		return err
	}
	p := Pending{Version: m.Version, Path: final, SHA256: strings.ToLower(a.SHA256)}
	b, _ := json.Marshal(p)
	if err := os.WriteFile(filepath.Join(u.dir, "pending.json"), b, 0o600); err != nil {
		return err
	}
	u.cleanup(a.Name)
	u.set(func(s *Status) { s.State, s.Progress = "ready", 1 })
	return nil
}

func (u *Updater) pending() (*Pending, error) {
	return ReadPending(filepath.Dir(u.dir))
}

// cleanup removes old installers except keep.
func (u *Updater) cleanup(keep string) {
	entries, _ := os.ReadDir(u.dir)
	for _, e := range entries {
		n := e.Name()
		if n == keep || n == "pending.json" && keep != "" {
			continue
		}
		_ = os.Remove(filepath.Join(u.dir, n))
	}
}

// ReadPending returns a downloaded, verified update for dataDir, if any.
func ReadPending(dataDir string) (*Pending, error) {
	b, err := os.ReadFile(filepath.Join(dataDir, "updates", "pending.json"))
	if err != nil {
		return nil, err
	}
	var p Pending
	if err := json.Unmarshal(b, &p); err != nil {
		return nil, err
	}
	return &p, nil
}

// VerifyFile re-checks a pending installer just before it is executed.
func VerifyFile(p *Pending) error {
	f, err := os.Open(p.Path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	if hex.EncodeToString(h.Sum(nil)) != p.SHA256 {
		return errors.New("pending installer changed on disk since it was verified")
	}
	return nil
}

// TakePending returns the pending update if it is newer than current and
// still intact; stale entries are removed.
func TakePending(dataDir, current string) (*Pending, error) {
	p, err := ReadPending(dataDir)
	if err != nil {
		return nil, err
	}
	if Compare(p.Version, current) <= 0 {
		_ = os.RemoveAll(filepath.Join(dataDir, "updates"))
		return nil, errors.New("pending update already installed")
	}
	if err := VerifyFile(p); err != nil {
		_ = os.RemoveAll(filepath.Join(dataDir, "updates"))
		return nil, err
	}
	return p, nil
}

// Launch starts the installer detached so it can replace the running app
// after it exits. It is implemented on Windows only.
func Launch(p *Pending) error { return launch(p) }
