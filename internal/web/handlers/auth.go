package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"

	"github.com/nrhtr/spruce/internal/email"
)

const sessionCookie = "spruce_session"

func (h *Handler) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Dev mode: static token acts as a valid session.
		if h.cfg.DevMode && h.cfg.AdminToken != "" {
			if cookie, err := r.Cookie(sessionCookie); err == nil && cookie.Value == h.cfg.AdminToken {
				next.ServeHTTP(w, r)
				return
			}
		}

		cookie, err := r.Cookie(sessionCookie)
		if err != nil {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		n, err := h.queries.VerifySession(r.Context(), cookie.Value)
		if err != nil || n == 0 {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (h *Handler) LoginGet(w http.ResponseWriter, r *http.Request) {
	// Dev mode: auto-login by setting the static token as a cookie.
	if h.cfg.DevMode && h.cfg.AdminToken != "" {
		setSessionCookie(w, h.cfg.AdminToken)
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	h.render(w, "login", map[string]any{"Sent": false})
}

func (h *Handler) LoginPost(w http.ResponseWriter, r *http.Request) {
	token := newToken()

	if err := h.queries.CreateMagicLink(r.Context(), token); err != nil {
		h.log.Error("create magic link", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	siteURL := h.cfg.SiteURL
	if siteURL == "" {
		siteURL = "http://" + r.Host
	}
	link := fmt.Sprintf("%s/auth/verify?token=%s", siteURL, token)

	body := fmt.Sprintf(
		"<p>Click the link below to log in to spruce. It expires in 15 minutes.</p><p><a href=\"%s\">%s</a></p>",
		link, link,
	)
	if err := email.Send(h.cfg, h.cfg.EmailTo, "spruce login", body); err != nil {
		h.log.Error("send login email", "error", err)
	}

	h.render(w, "login", map[string]any{"Sent": true, "Email": h.cfg.EmailTo})
}

func (h *Handler) AuthVerify(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	res, err := h.queries.VerifyMagicLink(r.Context(), token)
	if err != nil {
		h.log.Error("verify magic link", "error", err)
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		// Token invalid, already used, or expired.
		http.Redirect(w, r, "/login", http.StatusSeeOther)
		return
	}

	sessionToken := newToken()
	if err := h.queries.CreateSession(r.Context(), sessionToken); err != nil {
		h.log.Error("create session", "error", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	setSessionCookie(w, sessionToken)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func setSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   86400,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

func newToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}
