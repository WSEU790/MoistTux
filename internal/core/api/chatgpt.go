package api

import (
	"errors"
	"net/http"

	"github.com/WSEU790/MoistTux/internal/core/chatgpt"
)

// ChatGPT sign-in ("Sign in with ChatGPT" plan usage). Tokens never leave
// the core: these routes only start, cancel and end the sign-in and report
// the linked account.
func (s *Server) chatgptRoutes(m *http.ServeMux) {
	need := func(h func(w http.ResponseWriter, r *http.Request, g *chatgpt.Manager)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if s.ChatGPT == nil {
				writeErr(w, http.StatusNotImplemented, errors.New("ChatGPT sign-in is not available in this build"))
				return
			}
			h(w, r, s.ChatGPT)
		}
	}
	m.HandleFunc("GET /api/ai/chatgpt", need(func(w http.ResponseWriter, r *http.Request, g *chatgpt.Manager) {
		writeJSON(w, 200, g.Status())
	}))
	m.HandleFunc("POST /api/ai/chatgpt/signin", need(func(w http.ResponseWriter, r *http.Request, g *chatgpt.Manager) {
		var opt struct {
			NewAccount bool `json:"new_account"`
			Reconsent  bool `json:"reconsent"`
		}
		if r.ContentLength > 0 {
			if err := decode(r, &opt); err != nil {
				writeErr(w, 400, err)
				return
			}
		}
		u, err := g.Begin(r.Context(), chatgpt.BeginOptions{NewAccount: opt.NewAccount, Reconsent: opt.Reconsent})
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		writeJSON(w, 200, map[string]any{"auth_url": u, "opened": g.OpenBrowser != nil})
	}))
	m.HandleFunc("POST /api/ai/chatgpt/cancel", need(func(w http.ResponseWriter, r *http.Request, g *chatgpt.Manager) {
		g.Cancel()
		writeJSON(w, 200, g.Status())
	}))
	m.HandleFunc("POST /api/ai/chatgpt/signout", need(func(w http.ResponseWriter, r *http.Request, g *chatgpt.Manager) {
		res, err := g.SignOut(r.Context())
		if err != nil {
			writeErr(w, 500, err)
			return
		}
		writeJSON(w, 200, res)
	}))
	m.HandleFunc("POST /api/ai/chatgpt/welcome-seen", need(func(w http.ResponseWriter, r *http.Request, g *chatgpt.Manager) {
		if err := g.MarkWelcomeSeen(r.Context()); err != nil {
			writeErr(w, 500, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	m.HandleFunc("POST /api/ai/chatgpt/resume", need(func(w http.ResponseWriter, r *http.Request, g *chatgpt.Manager) {
		g.ClearLimit()
		writeJSON(w, 200, g.Status())
	}))
	// Opens ChatGPT's usage page in the system browser (links inside the
	// desktop window would otherwise open in an embedded window).
	m.HandleFunc("POST /api/ai/chatgpt/open-usage", need(func(w http.ResponseWriter, r *http.Request, g *chatgpt.Manager) {
		if g.OpenBrowser == nil {
			writeJSON(w, 200, map[string]any{"opened": false, "url": chatgpt.UsageURL})
			return
		}
		g.OpenBrowser(chatgpt.UsageURL)
		writeJSON(w, 200, map[string]any{"opened": true, "url": chatgpt.UsageURL})
	}))
}
