package chatgpt

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/WSEU790/MoistTux/internal/core/secrets"
	"github.com/WSEU790/MoistTux/internal/core/store"
)

// fakeAuth imitates auth.openai.com: authorize (driven by the test as the
// browser), token exchange with PKCE, refresh rotation, JWKS, discovery and
// revocation.
type fakeAuth struct {
	t   *testing.T
	srv *httptest.Server
	key *rsa.PrivateKey

	mu            sync.Mutex
	codes         map[string]authReq // code -> request
	refresh       map[string]bool    // valid refresh tokens
	revoked       []string
	grantScope    string
	issued        string // issued client ID
	sub, email    string
	refreshCount  int
	refreshFailAs string // error code for the next refresh
	lastAuthorize url.Values
}

type authReq struct{ challenge, redirect, nonce, client string }

func newFakeAuth(t *testing.T) *fakeAuth {
	k, _ := rsa.GenerateKey(rand.Reader, 2048)
	f := &fakeAuth{t: t, key: k, codes: map[string]authReq{}, refresh: map[string]bool{}, issued: "oaiapp_test123", sub: "user-sub-1", email: "ops@example.com", grantScope: Scopes}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/jwks.json", func(w http.ResponseWriter, r *http.Request) {
		e := big.NewInt(int64(k.E)).Bytes()
		json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]string{{"kty": "RSA", "kid": "k1", "alg": "RS256", "n": base64.RawURLEncoding.EncodeToString(k.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(e)}}})
	})
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{"revocation_endpoint": f.srv.URL + "/oauth/revoke"})
	})
	mux.HandleFunc("/oauth/revoke", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Form.Get("token_type_hint") != "refresh_token" || r.Form.Get("client_id") != f.issued {
			w.WriteHeader(400)
			return
		}
		f.revoked = append(f.revoked, r.Form.Get("token"))
		delete(f.refresh, r.Form.Get("token"))
	})
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		f.mu.Lock()
		defer f.mu.Unlock()
		fail := func(code string) {
			w.WriteHeader(400)
			json.NewEncoder(w).Encode(map[string]string{"error": code, "error_description": "test"})
		}
		if r.Form.Get("resource") != Resource {
			fail("invalid_target")
			return
		}
		switch r.Form.Get("grant_type") {
		case "authorization_code":
			ar, ok := f.codes[r.Form.Get("code")]
			delete(f.codes, r.Form.Get("code"))
			sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			if !ok || base64.RawURLEncoding.EncodeToString(sum[:]) != ar.challenge || r.Form.Get("redirect_uri") != ar.redirect || r.Form.Get("client_id") != f.issued {
				fail("invalid_grant")
				return
			}
			f.issue(w, ar.nonce)
		case "refresh_token":
			if f.refreshFailAs != "" {
				fail(f.refreshFailAs)
				return
			}
			rt := r.Form.Get("refresh_token")
			if !f.refresh[rt] || r.Form.Get("client_id") != f.issued {
				fail("refresh_token_reused")
				return
			}
			delete(f.refresh, rt) // rotation
			f.refreshCount++
			f.issue(w, "")
		default:
			fail("unsupported_grant_type")
		}
	})
	f.srv = httptest.NewServer(mux)
	return f
}

func (f *fakeAuth) jwt(claims map[string]any) string {
	h, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "k1", "typ": "JWT"})
	c, _ := json.Marshal(claims)
	in := base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(c)
	d := sha256.Sum256([]byte(in))
	sig, _ := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, d[:])
	return in + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func (f *fakeAuth) issue(w http.ResponseWriter, nonce string) {
	rt := fmt.Sprintf("rt-%d-%d", time.Now().UnixNano(), len(f.refresh))
	f.refresh[rt] = true
	resp := map[string]any{"access_token": fmt.Sprintf("at-%d", f.refreshCount), "refresh_token": rt, "token_type": "Bearer", "expires_in": 3600, "scope": f.grantScope}
	if nonce != "" {
		resp["id_token"] = f.jwt(map[string]any{"iss": f.srv.URL, "aud": f.issued, "sub": f.sub, "email": f.email, "exp": time.Now().Add(time.Hour).Unix(), "nonce": nonce})
	}
	json.NewEncoder(w).Encode(resp)
}

// browse plays the browser: it reads the authorization URL, "approves" and
// follows the redirect to NEXUS's loopback callback.
func (f *fakeAuth) browse(t *testing.T, authURL string, mutate func(q url.Values)) (int, string) {
	t.Helper()
	u, err := url.Parse(authURL)
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	f.mu.Lock()
	f.lastAuthorize = q
	code := fmt.Sprintf("code-%d", time.Now().UnixNano())
	f.codes[code] = authReq{challenge: q.Get("code_challenge"), redirect: q.Get("redirect_uri"), nonce: q.Get("nonce"), client: q.Get("client_id")}
	f.mu.Unlock()
	cb := url.Values{"code": {code}, "state": {q.Get("state")}, "scope": {f.grantScope}}
	if q.Get("client_id") == dynamicClient {
		cb.Set("client_id", f.issued)
	}
	if mutate != nil {
		mutate(cb)
	}
	resp, err := http.Get(q.Get("redirect_uri") + "?" + cb.Encode())
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func newManager(t *testing.T, f *fakeAuth) (*Manager, *store.Store, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "nexus.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	prot, err := secrets.NewFileKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	m := New(st, prot, nil)
	m.SetEndpoints(Endpoints{Authorize: f.srv.URL + "/authorize", Token: f.srv.URL + "/oauth/token", JWKS: f.srv.URL + "/.well-known/jwks.json", Discovery: f.srv.URL + "/.well-known/openid-configuration", Issuer: f.srv.URL})
	return m, st, dir
}

func waitFlow(t *testing.T, m *Manager, want string) Status {
	t.Helper()
	for i := 0; i < 100; i++ {
		if s := m.Status(); s.Flow == want {
			return s
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("flow state: %+v", m.Status())
	return Status{}
}

func TestSignInRefreshSignOut(t *testing.T) {
	f := newFakeAuth(t)
	defer f.srv.Close()
	m, st, dir := newManager(t, f)
	ctx := context.Background()
	opened := ""
	m.OpenBrowser = func(u string) { opened = u }

	authURL, err := m.Begin(ctx, BeginOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if opened != authURL {
		t.Error("the system browser was not opened")
	}
	q, _ := url.Parse(authURL)
	p := q.Query()
	for k, v := range map[string]string{"client_id": dynamicClient, "agent_name_hint": "NEXUS", "response_type": "code", "scope": Scopes, "resource": Resource, "code_challenge_method": "S256"} {
		if p.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, p.Get(k), v)
		}
	}
	if !strings.HasPrefix(p.Get("ext_agent_host_id"), "urn:uuid:") || len(p.Get("ext_agent_host_id")) != 45 {
		t.Errorf("host id %q", p.Get("ext_agent_host_id"))
	}
	if ru := p.Get("redirect_uri"); !strings.HasPrefix(ru, "http://127.0.0.1:") || !strings.HasSuffix(ru, "/auth/callback") {
		t.Errorf("redirect %q", ru)
	}
	if st := m.Status(); st.Flow != "waiting" || st.AuthURL == "" {
		t.Errorf("status while waiting: %+v", st)
	}
	hostID := p.Get("ext_agent_host_id")

	// A forged callback (wrong state) is rejected and the attempt goes on.
	if code, _ := f.browse(t, authURL, func(cb url.Values) { cb.Set("state", "forged") }); code != 400 {
		t.Errorf("forged state accepted: %d", code)
	}
	if code, body := f.browse(t, authURL, nil); code != 200 || !strings.Contains(body, "linked") {
		t.Fatalf("callback: %d %s", code, body)
	}
	s := waitFlow(t, m, "done")
	if !s.Linked || !s.PlanEnabled || s.Email != "ops@example.com" || !s.Registered || s.WelcomeSeen {
		t.Fatalf("status after sign-in: %+v", s)
	}

	// Nothing secret is stored in plaintext.
	var raw map[string]any
	st.GetSetting(ctx, settingKey, &raw)
	b, _ := json.Marshal(raw)
	if strings.Contains(string(b), "at-0") || strings.Contains(string(b), "rt-") || strings.Contains(string(b), "eyJ") {
		t.Fatalf("tokens stored in plaintext: %s", b)
	}

	tok, err := m.AccessToken(ctx)
	if err != nil || tok != "at-0" {
		t.Fatalf("access token %q %v", tok, err)
	}
	// Near expiry the token is refreshed (and the refresh token rotates).
	m.now = func() time.Time { return time.Now().Add(59 * time.Minute) }
	if tok, err = m.AccessToken(ctx); err != nil || tok != "at-1" {
		t.Fatalf("refreshed token %q %v", tok, err)
	}
	m.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	if tok, err = m.AccessToken(ctx); err != nil || tok != "at-2" {
		t.Fatalf("second refresh %q %v (rotated refresh token not saved?)", tok, err)
	}
	m.now = time.Now

	// A restart keeps the sign-in (sealed in the store).
	m2 := New(st, mustKey(t, dir), nil)
	m2.SetEndpoints(m.ep)
	if s := m2.Status(); !s.Linked || !s.PlanEnabled {
		t.Fatalf("after restart: %+v", s)
	}

	// Re-authorization reuses the issued client and host ID and sends hints.
	authURL, _ = m2.Begin(ctx, BeginOptions{})
	p2, _ := url.Parse(authURL)
	if p2.Query().Get("client_id") != "oaiapp_test123" || p2.Query().Get("ext_agent_host_id") != hostID || p2.Query().Get("agent_name_hint") != "" || p2.Query().Get("id_token_hint") == "" || p2.Query().Get("login_hint") != "ops@example.com" {
		t.Errorf("reauthorization params: %v", p2.Query())
	}
	m2.Cancel()

	res, err := m2.SignOut(ctx)
	if err != nil || !res.Revoked || len(f.revoked) != 1 {
		t.Fatalf("sign out: %+v %v revoked=%v", res, err, f.revoked)
	}
	if s := m2.Status(); s.Linked || !s.Registered {
		t.Fatalf("after sign-out: %+v", s)
	}
	if _, err := m2.AccessToken(ctx); err != ErrNotLinked {
		t.Errorf("token after sign-out: %v", err)
	}
}

func mustKey(t *testing.T, dir string) secrets.Protector {
	p, err := secrets.NewFileKey(dir)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSignInFailures(t *testing.T) {
	f := newFakeAuth(t)
	defer f.srv.Close()
	m, _, _ := newManager(t, f)
	ctx := context.Background()

	// Declined consent.
	u, _ := m.Begin(ctx, BeginOptions{})
	f.browse(t, u, func(cb url.Values) { cb.Del("code"); cb.Set("error", "access_denied") })
	if s := waitFlow(t, m, "declined"); s.Linked {
		t.Fatal("linked after decline")
	}

	// Registration without an issued client ID.
	u, _ = m.Begin(ctx, BeginOptions{})
	f.browse(t, u, func(cb url.Values) { cb.Del("client_id") })
	waitFlow(t, m, "failed")

	// An ID token for another nonce (replayed) is rejected.
	u, _ = m.Begin(ctx, BeginOptions{})
	pu, _ := url.Parse(u)
	q := pu.Query()
	q.Set("nonce", "other")
	pu.RawQuery = q.Encode()
	f.browse(t, pu.String(), nil)
	if s := waitFlow(t, m, "failed"); !strings.Contains(s.FlowError, "nonce") {
		t.Fatalf("nonce: %+v", s)
	}

	// Sign-in without plan permission: kept, but no inference.
	f.grantScope = "openid profile email offline_access"
	u, _ = m.Begin(ctx, BeginOptions{})
	f.browse(t, u, nil)
	s := waitFlow(t, m, "done")
	if !s.Linked || s.PlanEnabled || m.Linked() {
		t.Fatalf("without plan scope: %+v", s)
	}
	if _, err := m.AccessToken(ctx); err == nil || !strings.Contains(err.Error(), "not enabled") {
		t.Errorf("token without plan scope: %v", err)
	}
	// Enabling it asks for consent again with the saved client.
	f.grantScope = Scopes
	u, _ = m.Begin(ctx, BeginOptions{Reconsent: true})
	if pu, _ := url.Parse(u); pu.Query().Get("prompt") != "consent" || pu.Query().Get("client_id") != "oaiapp_test123" {
		t.Errorf("reconsent params %v", pu.Query())
	}
	f.browse(t, u, nil)
	if s := waitFlow(t, m, "done"); !s.PlanEnabled {
		t.Fatalf("after reconsent: %+v", s)
	}

	// A disconnected app (unusable refresh token) clears the tokens.
	f.refreshFailAs = "invalid_grant"
	m.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	if _, err := m.AccessToken(ctx); err == nil || !strings.Contains(err.Error(), "Sign in again") {
		t.Fatalf("refresh failure: %v", err)
	}
	if s := m.Status(); s.Linked || s.LastError == "" {
		t.Fatalf("after refresh failure: %+v", s)
	}

	// Usage-limit pause and resume.
	m.now = time.Now
	f.refreshFailAs = ""
	u, _ = m.Begin(ctx, BeginOptions{})
	f.browse(t, u, nil)
	waitFlow(t, m, "done")
	m.NoteUsageLimit()
	if _, err := m.AccessToken(ctx); err == nil || !strings.Contains(err.Error(), "usage limit") {
		t.Fatalf("limit: %v", err)
	}
	m.ClearLimit()
	if _, err := m.AccessToken(ctx); err != nil {
		t.Fatal(err)
	}
}
