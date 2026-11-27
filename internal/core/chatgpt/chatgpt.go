// Package chatgpt links the operator's ChatGPT subscription to NEXUS with
// "Sign in with ChatGPT" plan usage, so the AI can run on ChatGPT models
// through the Responses API instead of a local engine.
//
// It implements OpenAI's flow for open-source, locally hosted apps
// (https://developers.openai.com/siwc/token-sharing-open-source):
//
//   - a stable per-installation host ID (ext_agent_host_id, urn:uuid:…);
//   - OAuth 2.0 authorization code with PKCE (S256) in the system browser,
//     with a one-shot loopback listener on http://127.0.0.1:<port>/auth/callback;
//   - dynamic client registration on the first sign-in (client_id
//     dynamic_agent_client), then the issued oaiapp_… client ID;
//   - ID-token validation against OpenAI's JWKS (issuer, audience, expiry,
//     nonce) and a check that chatgpt.tokens.use.direct was granted;
//   - serialized refresh of the rotating refresh token, and revocation on
//     sign-out.
//
// Tokens are sealed with the secret protector (DPAPI on Windows) and never
// logged, returned by the API or shown to the model.
package chatgpt

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/WSEU790/MoistTux/internal/core/secrets"
	"github.com/WSEU790/MoistTux/internal/core/store"
)

const (
	Issuer    = "https://auth.openai.com"
	Resource  = "https://api.openai.com/v1"
	APIBase   = "https://api.openai.com"
	PlanScope = "chatgpt.tokens.use.direct"
	Scopes    = "openid profile email offline_access resource.invoke " + PlanScope
	AgentName = "NEXUS"
	UsageURL  = "https://chatgpt.com/settings/usage"

	dynamicClient = "dynamic_agent_client"
	settingKey    = "ai.chatgpt"
	callbackPath  = "/auth/callback"
	preferredPort = 1455
	attemptTTL    = 10 * time.Minute
	limitPause    = 10 * time.Minute
)

// Endpoints are OpenAI's; tests point them at a stand-in.
type Endpoints struct {
	Authorize  string
	Token      string
	JWKS       string
	Discovery  string
	Issuer     string
	RevokeFall string // used when discovery is unreachable
	API        string // Responses API base
}

func DefaultEndpoints() Endpoints {
	return Endpoints{
		Authorize: Issuer + "/api/accounts/authorize",
		Token:     Issuer + "/api/accounts/oauth/token",
		JWKS:      Issuer + "/.well-known/jwks.json",
		Discovery: Issuer + "/.well-known/openid-configuration",
		Issuer:    Issuer,
		API:       APIBase,
	}
}

// ErrNotLinked means no ChatGPT account is signed in.
var ErrNotLinked = errors.New("no ChatGPT account is linked: open Settings → AI engine and choose Continue with ChatGPT")

// record is persisted under "ai.chatgpt". Tokens are sealed.
type record struct {
	HostID      string    `json:"host_id"`
	ClientID    string    `json:"client_id,omitempty"` // issued oaiapp_… ID
	Subject     string    `json:"subject,omitempty"`
	Email       string    `json:"email,omitempty"`
	Name        string    `json:"name,omitempty"`
	Scopes      []string  `json:"scopes,omitempty"`
	Sealed      string    `json:"tokens_sealed,omitempty"`
	LinkedAt    time.Time `json:"linked_at,omitzero"`
	WelcomeSeen bool      `json:"welcome_seen,omitempty"`
	LastError   string    `json:"last_error,omitempty"`
}

type tokens struct {
	Access  string    `json:"access_token"`
	Refresh string    `json:"refresh_token"`
	ID      string    `json:"id_token"`
	Expiry  time.Time `json:"expiry"`
}

type attempt struct {
	state, nonce, verifier, redirect string
	clientID                         string // dynamic_agent_client or the issued ID
	authURL                          string
	newReg                           bool
	srv                              *http.Server
	expires                          time.Time
}

// Manager owns the ChatGPT sign-in for this installation.
type Manager struct {
	st   *store.Store
	prot secrets.Protector
	log  *log.Logger
	ep   Endpoints
	hc   *http.Client
	now  func() time.Time

	// OpenBrowser opens a URL in the system browser (set by the desktop
	// app). When nil, the UI shows the link for the operator to open.
	OpenBrowser func(string)
	// OnChange is called when the sign-in state changes.
	OnChange func()

	mu         sync.Mutex
	rec        record
	tok        *tokens
	pending    *attempt
	flowState  string // "", waiting, done, declined, failed
	flowErr    string
	limitedAt  time.Time
	refreshMu  sync.Mutex
	jwksMu     sync.Mutex
	jwks       map[string]jwk
	jwksAt     time.Time
	listenHost string // for tests; always 127.0.0.1 in production
}

func New(st *store.Store, prot secrets.Protector, logger *log.Logger) *Manager {
	m := &Manager{st: st, prot: prot, log: logger, ep: DefaultEndpoints(), hc: &http.Client{Timeout: 30 * time.Second}, now: time.Now, listenHost: "127.0.0.1"}
	_, _ = st.GetSetting(context.Background(), settingKey, &m.rec)
	if m.rec.Sealed != "" && prot != nil {
		if b, err := secrets.Open(prot, m.rec.Sealed); err == nil {
			var t tokens
			if json.Unmarshal(b, &t) == nil {
				m.tok = &t
			}
		}
	}
	return m
}

// SetEndpoints is for tests.
func (m *Manager) SetEndpoints(ep Endpoints) {
	if ep.API == "" {
		ep.API = APIBase
	}
	m.ep = ep
}

// APIBase is the Responses API base URL the access tokens are for.
func (m *Manager) APIBase() string { return m.ep.API }

// ---------------------------------------------------------------- status

type Status struct {
	Linked      bool      `json:"linked"`       // a usable sign-in exists
	PlanEnabled bool      `json:"plan_enabled"` // chatgpt.tokens.use.direct was granted
	Email       string    `json:"email,omitempty"`
	Name        string    `json:"name,omitempty"`
	Registered  bool      `json:"registered"` // NEXUS has an issued client for this account
	LinkedAt    time.Time `json:"linked_at,omitzero"`
	WelcomeSeen bool      `json:"welcome_seen"`
	Flow        string    `json:"flow,omitempty"` // waiting | done | declined | failed
	FlowError   string    `json:"flow_error,omitempty"`
	AuthURL     string    `json:"auth_url,omitempty"` // while waiting, for opening it manually
	LastError   string    `json:"last_error,omitempty"`
	Limited     bool      `json:"usage_limited"`
	UsageURL    string    `json:"usage_url"`
	CanSeal     bool      `json:"can_store_credentials"`
}

func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := Status{
		Linked: m.tok != nil && m.tok.Refresh != "", Email: m.rec.Email, Name: m.rec.Name, Registered: m.rec.ClientID != "",
		LinkedAt: m.rec.LinkedAt, WelcomeSeen: m.rec.WelcomeSeen, Flow: m.flowState, FlowError: m.flowErr,
		LastError: m.rec.LastError, UsageURL: UsageURL, CanSeal: m.prot != nil,
		Limited: !m.limitedAt.IsZero() && m.now().Sub(m.limitedAt) < limitPause,
	}
	if m.pending != nil {
		s.AuthURL = m.pending.authURL
	}
	s.PlanEnabled = s.Linked && slices.Contains(m.rec.Scopes, PlanScope)
	return s
}

// Linked reports whether inference can be attempted.
func (m *Manager) Linked() bool { st := m.Status(); return st.Linked && st.PlanEnabled }

func (m *Manager) changed() {
	if m.OnChange != nil {
		m.OnChange()
	}
}

// save persists the record; the caller holds m.mu.
func (m *Manager) saveLocked(ctx context.Context) error {
	if m.tok == nil {
		m.rec.Sealed = ""
	} else {
		if m.prot == nil {
			return errors.New("cannot store ChatGPT credentials: no secret protector is available")
		}
		b, _ := json.Marshal(m.tok)
		sealed, err := secrets.Seal(m.prot, b)
		if err != nil {
			return err
		}
		m.rec.Sealed = sealed
	}
	return m.st.PutSetting(ctx, settingKey, m.rec)
}

// MarkWelcomeSeen records that the first-use notice was shown.
func (m *Manager) MarkWelcomeSeen(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rec.WelcomeSeen = true
	return m.saveLocked(ctx)
}

// ClearLimit lets the operator resume after raising their usage limit.
func (m *Manager) ClearLimit() {
	m.mu.Lock()
	m.limitedAt = time.Time{}
	m.mu.Unlock()
	m.changed()
}

// NoteUsageLimit pauses automatic use after a usage-limit error.
func (m *Manager) NoteUsageLimit() {
	m.mu.Lock()
	m.limitedAt = m.now()
	m.mu.Unlock()
	m.changed()
}

// ---------------------------------------------------------------- sign-in

func randToken(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func newHostID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = b[6]&0x0f | 0x40 // version 4
	b[8] = b[8]&0x3f | 0x80 // RFC 9562 variant
	return fmt.Sprintf("urn:uuid:%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// BeginOptions select the kind of sign-in.
type BeginOptions struct {
	NewAccount bool // register another ChatGPT account (replaces the current one once validated)
	Reconsent  bool // ask again for ChatGPT plan permission after it was declined
}

// Begin starts a sign-in: it opens a loopback listener, then returns the
// authorization URL (and opens it in the system browser when possible).
func (m *Manager) Begin(ctx context.Context, opt BeginOptions) (string, error) {
	if m.prot == nil {
		return "", errors.New("cannot store ChatGPT credentials securely on this system")
	}
	m.mu.Lock()
	if m.rec.HostID == "" {
		m.rec.HostID = newHostID() // persisted before the first sign-in
		if err := m.saveLocked(ctx); err != nil {
			m.mu.Unlock()
			return "", err
		}
	}
	m.stopPendingLocked()
	ln, err := net.Listen("tcp", net.JoinHostPort(m.listenHost, fmt.Sprint(preferredPort)))
	if err != nil {
		ln, err = net.Listen("tcp", net.JoinHostPort(m.listenHost, "0"))
	}
	if err != nil {
		m.mu.Unlock()
		return "", fmt.Errorf("cannot open the sign-in callback listener: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	a := &attempt{
		state: randToken(32), nonce: randToken(32), verifier: randToken(48),
		redirect: fmt.Sprintf("http://127.0.0.1:%d%s", port, callbackPath),
		clientID: m.rec.ClientID, expires: m.now().Add(attemptTTL),
	}
	if a.clientID == "" || opt.NewAccount {
		a.clientID, a.newReg = dynamicClient, true
	}
	sum := sha256.Sum256([]byte(a.verifier))
	q := url.Values{
		"client_id": {a.clientID}, "ext_agent_host_id": {m.rec.HostID}, "response_type": {"code"},
		"redirect_uri": {a.redirect}, "scope": {Scopes}, "resource": {Resource},
		"state": {a.state}, "nonce": {a.nonce},
		"code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])},
	}
	if a.newReg {
		q.Set("agent_name_hint", AgentName)
	} else {
		if m.tok != nil && m.tok.ID != "" {
			q.Set("id_token_hint", m.tok.ID)
		}
		if m.rec.Email != "" {
			q.Set("login_hint", m.rec.Email)
		}
	}
	if opt.Reconsent {
		q.Set("prompt", "consent")
	}
	authURL := m.ep.Authorize + "?" + q.Encode()
	a.authURL = authURL

	mux := http.NewServeMux()
	a.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	mux.HandleFunc(callbackPath, func(w http.ResponseWriter, r *http.Request) { m.callback(w, r, a) })
	m.pending, m.flowState, m.flowErr = a, "waiting", ""
	m.mu.Unlock()

	go func() { _ = a.srv.Serve(ln) }()
	go func() {
		time.Sleep(attemptTTL)
		m.mu.Lock()
		expired := m.pending == a
		if expired {
			m.stopPendingLocked()
			m.flowState, m.flowErr = "failed", "The sign-in was not completed within 10 minutes. Try again."
		}
		m.mu.Unlock()
		if expired {
			m.changed()
		}
	}()
	if m.OpenBrowser != nil {
		m.OpenBrowser(authURL)
	}
	m.changed()
	return authURL, nil
}

// Cancel stops a running sign-in attempt.
func (m *Manager) Cancel() {
	m.mu.Lock()
	if m.pending != nil {
		m.stopPendingLocked()
		m.flowState, m.flowErr = "", ""
	}
	m.mu.Unlock()
	m.changed()
}

func (m *Manager) stopPendingLocked() {
	if m.pending != nil && m.pending.srv != nil {
		srv := m.pending.srv
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = srv.Shutdown(ctx)
		}()
	}
	m.pending = nil
}

const pageTmpl = `<!doctype html><html><head><meta charset="utf-8"><title>NEXUS</title>
<style>body{font:15px system-ui,sans-serif;background:#0d1117;color:#e6edf3;display:grid;place-items:center;height:100vh;margin:0}div{max-width:460px;text-align:center}h1{font-size:20px}</style>
</head><body><div><h1>%s</h1><p>%s</p></div></body></html>`

func page(w http.ResponseWriter, code int, title, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	fmt.Fprintf(w, pageTmpl, html.EscapeString(title), html.EscapeString(msg))
}

func (m *Manager) callback(w http.ResponseWriter, r *http.Request, a *attempt) {
	q := r.URL.Query()
	m.mu.Lock()
	current := m.pending == a
	m.mu.Unlock()
	if !current || subtle.ConstantTimeCompare([]byte(q.Get("state")), []byte(a.state)) != 1 {
		page(w, http.StatusBadRequest, "Sign-in not recognised", "This sign-in link is not the one NEXUS is waiting for. Start again from NEXUS.")
		return
	}
	finish := func(state, msg string) {
		m.mu.Lock()
		if m.pending == a {
			m.stopPendingLocked()
		}
		m.flowState, m.flowErr = state, msg
		m.mu.Unlock()
		m.changed()
	}
	if e := q.Get("error"); e != "" {
		if e == "access_denied" {
			page(w, http.StatusOK, "ChatGPT was not linked", "You declined access. NEXUS keeps using its current AI engine. You can try again from Settings → AI engine.")
			finish("declined", "You declined access in the browser.")
			return
		}
		msg := e
		if d := q.Get("error_description"); d != "" {
			msg += ": " + d
		}
		page(w, http.StatusOK, "Sign-in failed", msg)
		finish("failed", "ChatGPT sign-in failed: "+msg)
		return
	}
	clientID := a.clientID
	if cb := q.Get("client_id"); a.newReg {
		if cb == "" || cb == dynamicClient {
			page(w, http.StatusOK, "Sign-in incomplete", "ChatGPT did not return a registration for NEXUS. Start again from NEXUS.")
			finish("failed", "The registration did not complete (no client ID was returned).")
			return
		}
		clientID = cb
	} else if cb != "" && cb != a.clientID {
		page(w, http.StatusOK, "Sign-in rejected", "ChatGPT returned a different app registration than the one NEXUS asked for.")
		finish("failed", "ChatGPT returned a different app registration; the sign-in was rejected.")
		return
	}
	code := q.Get("code")
	if code == "" {
		page(w, http.StatusBadRequest, "Sign-in failed", "No authorization code was returned.")
		finish("failed", "No authorization code was returned.")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := m.complete(ctx, a, clientID, code, q.Get("scope")); err != nil {
		page(w, http.StatusOK, "Sign-in failed", err.Error())
		finish("failed", err.Error())
		return
	}
	page(w, http.StatusOK, "ChatGPT is linked to NEXUS", "You can close this window and return to NEXUS.")
	finish("done", "")
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
	TokenType    string `json:"token_type"`
}

type OAuthError struct {
	Status      int
	Code        string
	Description string
}

func (e *OAuthError) Error() string {
	s := fmt.Sprintf("ChatGPT sign-in service returned HTTP %d", e.Status)
	if e.Code != "" {
		s += " (" + e.Code + ")"
	}
	if e.Description != "" {
		s += ": " + e.Description
	}
	return s
}

func (m *Manager) tokenRequest(ctx context.Context, form url.Values) (*tokenResponse, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", m.ep.Token, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	resp, err := m.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot reach the ChatGPT sign-in service: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		oe := &OAuthError{Status: resp.StatusCode}
		var e struct {
			Error       json.RawMessage `json:"error"`
			Description string          `json:"error_description"`
			Detail      string          `json:"detail"`
		}
		_ = json.Unmarshal(body, &e)
		var code string
		if json.Unmarshal(e.Error, &code) != nil {
			var obj struct{ Code, Message string }
			_ = json.Unmarshal(e.Error, &obj)
			code, e.Description = obj.Code, obj.Message
		}
		oe.Code, oe.Description = code, strings.TrimSpace(e.Description+" "+e.Detail)
		return nil, oe
	}
	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil || tr.AccessToken == "" {
		return nil, errors.New("the ChatGPT sign-in service returned an unreadable token response")
	}
	return &tr, nil
}

func (m *Manager) complete(ctx context.Context, a *attempt, clientID, code, cbScope string) error {
	tr, err := m.tokenRequest(ctx, url.Values{
		"grant_type": {"authorization_code"}, "client_id": {clientID}, "code": {code},
		"code_verifier": {a.verifier}, "redirect_uri": {a.redirect}, "resource": {Resource},
	})
	if err != nil {
		return err
	}
	claims, err := m.verifyIDToken(ctx, tr.IDToken, clientID, a.nonce)
	if err != nil {
		return fmt.Errorf("the ChatGPT ID token failed validation: %w", err)
	}
	scope := tr.Scope
	if scope == "" {
		scope = cbScope
	}
	scopes := strings.Fields(scope)
	slices.Sort(scopes)
	if tr.RefreshToken == "" {
		return errors.New("ChatGPT did not grant offline access (no refresh token), so NEXUS could not stay signed in")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !a.newReg && m.rec.Subject != "" && claims.Subject != m.rec.Subject {
		return errors.New("you signed in with a different ChatGPT account than the linked one; use “Use a different account” instead")
	}
	if a.newReg && m.rec.ClientID != "" && m.rec.Subject != claims.Subject {
		m.rec.WelcomeSeen = false // a new account gets the first-use notice
	}
	m.rec.ClientID, m.rec.Subject, m.rec.Email, m.rec.Name = clientID, claims.Subject, claims.Email, claims.Name
	m.rec.Scopes, m.rec.LinkedAt, m.rec.LastError = scopes, m.now().UTC(), ""
	m.tok = &tokens{Access: tr.AccessToken, Refresh: tr.RefreshToken, ID: tr.IDToken, Expiry: m.now().Add(time.Duration(max(tr.ExpiresIn, 60)) * time.Second)}
	m.limitedAt = time.Time{}
	if err := m.saveLocked(ctx); err != nil {
		return err
	}
	_ = m.st.Audit(ctx, "operator", "ai.chatgpt.link", "ai", map[string]any{"email": claims.Email, "plan_usage": slices.Contains(scopes, PlanScope)})
	if m.log != nil {
		m.log.Printf("chatgpt: linked account %s (plan usage %v)", claims.Email, slices.Contains(scopes, PlanScope))
	}
	return nil
}

// ---------------------------------------------------------------- tokens

// unusableRefresh lists refresh errors after which the sign-in must be repeated.
var unusableRefresh = []string{"invalid_grant", "invalid_refresh_token", "token_expired", "refresh_token_expired", "refresh_token_invalidated", "refresh_token_reused"}

// AccessToken returns a valid access token, refreshing it when it is close
// to expiry. Refreshes are serialized because the refresh token rotates.
func (m *Manager) AccessToken(ctx context.Context) (string, error) {
	m.refreshMu.Lock()
	defer m.refreshMu.Unlock()
	m.mu.Lock()
	t, rec := m.tok, m.rec
	limited := !m.limitedAt.IsZero() && m.now().Sub(m.limitedAt) < limitPause
	m.mu.Unlock()
	if t == nil || t.Refresh == "" {
		return "", ErrNotLinked
	}
	if !slices.Contains(rec.Scopes, PlanScope) {
		return "", errors.New("ChatGPT plan use is not enabled for this sign-in: open Settings → AI engine and choose Enable ChatGPT plan use")
	}
	if limited {
		return "", fmt.Errorf("ChatGPT usage limit reached; NEXUS paused requests that use your ChatGPT plan. Review your limits at %s, then choose Resume in Settings → AI engine", UsageURL)
	}
	if t.Access != "" && m.now().Before(t.Expiry.Add(-2*time.Minute)) {
		return t.Access, nil
	}
	tr, err := m.tokenRequest(ctx, url.Values{"grant_type": {"refresh_token"}, "client_id": {rec.ClientID}, "refresh_token": {t.Refresh}, "resource": {Resource}})
	if err != nil {
		var oe *OAuthError
		if errors.As(err, &oe) && slices.Contains(unusableRefresh, oe.Code) {
			m.mu.Lock()
			m.tok = nil
			m.rec.LastError = "Your ChatGPT sign-in expired or was disconnected in ChatGPT settings. Sign in again in Settings → AI engine."
			_ = m.saveLocked(ctx)
			m.mu.Unlock()
			m.changed()
			return "", errors.New(m.rec.LastError)
		}
		if errors.As(err, &oe) && oe.Code == "invalid_client" {
			return "", fmt.Errorf("the NEXUS registration in ChatGPT is no longer valid (%s); choose Use a different account to register again", oe.Code)
		}
		return "", err // temporary: keep the credentials
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	nt := &tokens{Access: tr.AccessToken, Refresh: t.Refresh, ID: t.ID, Expiry: m.now().Add(time.Duration(max(tr.ExpiresIn, 60)) * time.Second)}
	if tr.RefreshToken != "" {
		nt.Refresh = tr.RefreshToken
	}
	if tr.IDToken != "" {
		nt.ID = tr.IDToken
	}
	if tr.Scope != "" {
		sc := strings.Fields(tr.Scope)
		slices.Sort(sc)
		m.rec.Scopes = sc
	}
	m.tok, m.rec.LastError = nt, ""
	if err := m.saveLocked(ctx); err != nil {
		return "", err
	}
	return nt.Access, nil
}

// SignOutResult reports whether OpenAI confirmed the revocation.
type SignOutResult struct {
	Revoked bool   `json:"revoked"`
	Note    string `json:"note,omitempty"`
}

// SignOut revokes the renewable session and clears the tokens. The issued
// client ID, host ID and email are kept for a later sign-in.
func (m *Manager) SignOut(ctx context.Context) (SignOutResult, error) {
	m.refreshMu.Lock()
	defer m.refreshMu.Unlock()
	m.mu.Lock()
	t, clientID := m.tok, m.rec.ClientID
	m.mu.Unlock()
	res := SignOutResult{Revoked: t == nil}
	if t != nil && t.Refresh != "" {
		endpoint := m.revocationEndpoint(ctx)
		var lastErr error
		for i := 0; i < 3 && endpoint != ""; i++ {
			if i > 0 {
				select {
				case <-ctx.Done():
				case <-time.After(time.Duration(i) * time.Second):
				}
			}
			lastErr = m.revoke(ctx, endpoint, clientID, t.Refresh)
			var oe *OAuthError
			if lastErr == nil || (errors.As(lastErr, &oe) && oe.Status < 500) {
				break
			}
		}
		res.Revoked = lastErr == nil && endpoint != ""
		if !res.Revoked {
			res.Note = "NEXUS signed out locally, but OpenAI did not confirm that the session was revoked. You can disconnect NEXUS in ChatGPT Settings → Connected apps."
		}
	}
	m.mu.Lock()
	m.tok, m.limitedAt = nil, time.Time{}
	m.rec.Scopes, m.rec.LastError = nil, ""
	err := m.saveLocked(ctx)
	m.mu.Unlock()
	_ = m.st.Audit(ctx, "operator", "ai.chatgpt.signout", "ai", map[string]any{"revoked": res.Revoked})
	m.changed()
	return res, err
}

func (m *Manager) revocationEndpoint(ctx context.Context) string {
	req, err := http.NewRequestWithContext(ctx, "GET", m.ep.Discovery, nil)
	if err == nil {
		if resp, err := m.hc.Do(req); err == nil {
			defer resp.Body.Close()
			var d struct {
				Revocation string `json:"revocation_endpoint"`
			}
			ok := resp.StatusCode == 200 && json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&d) == nil
			if ok && (strings.HasPrefix(d.Revocation, "https://") || strings.HasPrefix(d.Revocation, "http://127.0.0.1")) {
				return d.Revocation
			}
		}
	}
	return m.ep.RevokeFall
}

func (m *Manager) revoke(ctx context.Context, endpoint, clientID, token string) error {
	form := url.Values{"token": {token}, "token_type_hint": {"refresh_token"}, "client_id": {clientID}}
	req, err := http.NewRequestWithContext(ctx, "POST", endpoint, bytes.NewBufferString(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := m.hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode != http.StatusOK {
		return &OAuthError{Status: resp.StatusCode}
	}
	return nil
}
