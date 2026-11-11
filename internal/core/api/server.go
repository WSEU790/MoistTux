// Package api serves the desktop UI and its JSON API on the loopback
// interface. Every request must carry the per-launch session token (as an
// HttpOnly cookie set by the launch URL, or a bearer header), and requests
// with foreign Host or Origin headers are refused to defeat DNS rebinding and
// cross-site request forgery.
package api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/WSEU790/MoistTux/internal/core/agentclient"
	"github.com/WSEU790/MoistTux/internal/core/ai"
	"github.com/WSEU790/MoistTux/internal/core/events"
	"github.com/WSEU790/MoistTux/internal/core/fleet"
	"github.com/WSEU790/MoistTux/internal/core/identity"
	"github.com/WSEU790/MoistTux/internal/core/monitor"
	"github.com/WSEU790/MoistTux/internal/core/notify"
	"github.com/WSEU790/MoistTux/internal/core/store"
)

type Server struct {
	Store    *store.Store
	Bus      *events.Bus
	Fleet    *fleet.Manager
	Monitor  *monitor.Engine
	Notifier *notify.Notifier
	AI       *ai.Engine
	Registry *ai.Registry
	Policies *ai.PolicyStore
	Identity func() *identity.Identity
	Log      *log.Logger
	UI       fs.FS
	Token    string
	Version  string
	DataDir  string
	Started  time.Time
	DevOrigin string // e.g. http://localhost:5173 during UI development

	allowedHosts map[string]bool
}

const cookieName = "nexus_session"

// SetListenAddr records the host:port the UI is served on.
func (s *Server) SetListenAddr(addr string) {
	_, port, _ := net.SplitHostPort(addr)
	s.allowedHosts = map[string]bool{"127.0.0.1:" + port: true, "localhost:" + port: true, "[::1]:" + port: true}
}

func (s *Server) tokenOK(r *http.Request) bool {
	tok := ""
	if c, err := r.Cookie(cookieName); err == nil {
		tok = c.Value
	}
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		tok = strings.TrimPrefix(h, "Bearer ")
	}
	return tok != "" && subtle.ConstantTimeCompare([]byte(tok), []byte(s.Token)) == 1
}

func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dev := s.DevOrigin != ""
		if s.allowedHosts != nil && !s.allowedHosts[r.Host] && !dev {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			switch {
			case dev && origin == s.DevOrigin:
				w.Header().Set("Access-Control-Allow-Origin", origin)
				w.Header().Set("Access-Control-Allow-Credentials", "true")
				w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
				w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE")
				if r.Method == http.MethodOptions {
					w.WriteHeader(http.StatusNoContent)
					return
				}
			case s.allowedHosts[strings.TrimPrefix(origin, "http://")]:
				// same origin
			default:
				http.Error(w, "forbidden origin", http.StatusForbidden)
				return
			}
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

// Handler returns the full HTTP handler (UI + API).
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	api := http.NewServeMux()
	s.routes(api)
	mux.Handle("/api/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.tokenOK(r) {
			writeErr(w, http.StatusUnauthorized, errors.New("missing or invalid session; relaunch NEXUS"))
			return
		}
		api.ServeHTTP(w, r)
	}))
	mux.HandleFunc("/", s.serveUI)
	return s.guard(mux)
}

func (s *Server) serveUI(w http.ResponseWriter, r *http.Request) {
	// The launch URL carries the token once; exchange it for a cookie.
	if t := r.URL.Query().Get("token"); t != "" {
		if subtle.ConstantTimeCompare([]byte(t), []byte(s.Token)) != 1 {
			http.Error(w, "invalid launch token", http.StatusUnauthorized)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: cookieName, Value: s.Token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	if !s.tokenOK(r) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = io.WriteString(w, `<!doctype html><meta charset=utf-8><title>NEXUS</title><body style="background:#0b0d10;color:#c9d1d9;font:15px system-ui;display:grid;place-items:center;height:100vh;margin:0"><div><h2 style="font-weight:500">NEXUS session required</h2><p>Open NEXUS from its launcher. The local console only accepts the session created at launch.</p></div>`)
		return
	}
	if s.UI == nil {
		http.Error(w, "UI assets not built", 500)
		return
	}
	p := strings.TrimPrefix(r.URL.Path, "/")
	if p == "" {
		p = "index.html"
	}
	if f, err := s.UI.Open(p); err == nil {
		st, _ := f.Stat()
		f.Close()
		if !st.IsDir() {
			if strings.HasPrefix(p, "assets/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			}
			http.ServeFileFS(w, r, s.UI, p)
			return
		}
	}
	// SPA fallback.
	if _, err := fs.Stat(s.UI, "index.html"); err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, `<!doctype html><meta charset=utf-8><title>NEXUS</title><body style="background:#0b0d10;color:#c9d1d9;font:15px system-ui;padding:40px"><h2>UI not built</h2><p>Run <code>make ui</code> (or <code>npm --prefix web run build</code>) and rebuild NEXUS. The API is running.</p>`)
		return
	}
	w.Header().Set("Cache-Control", "no-cache")
	http.ServeFileFS(w, r, s.UI, "index.html")
}

// ---------------------------------------------------------------- helpers

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func statusFor(err error) int {
	var ae *agentclient.Error
	switch {
	case errors.Is(err, store.ErrNotFound):
		return http.StatusNotFound
	case errors.As(err, &ae):
		if ae.Status == 401 || ae.Status == 403 {
			return http.StatusBadGateway
		}
		if ae.Status >= 400 && ae.Status < 500 {
			return ae.Status
		}
		return http.StatusBadGateway
	case errors.Is(err, agentclient.ErrFingerprintMismatch):
		return http.StatusBadGateway
	}
	return http.StatusBadRequest
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(io.LimitReader(r.Body, 2<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return errors.New("invalid request body: " + err.Error())
	}
	return nil
}

func qInt(r *http.Request, k string, def int) int {
	if v, err := strconv.Atoi(r.URL.Query().Get(k)); err == nil {
		return v
	}
	return def
}
