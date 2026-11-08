package http

import (
	"net/http"
	"time"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/service"
)

// sessionCookie holds the web UI session token. It is HttpOnly (not readable by scripts) and
// SameSite=Strict (not sent on cross-site requests, which protects against CSRF).
const sessionCookie = "knowpod_session"

// persistentCookieAge is how long the cookie of a session that never expires is kept: the
// longest a Chromium-based browser (Electron, Android WebView) accepts. It is renewed each
// time the app asks who is signed in (GET /auth/me, at every start), so it doesn't run out.
const persistentCookieAge = 400 * 24 * time.Hour

type loginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	// Persistent asks for a session that never expires (the desktop and mobile apps).
	Persistent bool `json:"persistent"`
}

type changePasswordRequest struct {
	CurrentPassword string `json:"currentPassword"`
	NewPassword     string `json:"newPassword"`
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if !decode(w, r, &req) {
		return
	}
	acc, token, expires, err := s.auth.Login(r.Context(), req.Email, req.Password, req.Persistent)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	if req.Persistent {
		expires = time.Now().Add(persistentCookieAge)
	}
	setSessionCookie(w, r, token, expires)
	writeJSON(w, http.StatusOK, acc)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		if err := s.auth.Logout(r.Context(), c.Value); err != nil {
			s.writeErr(w, err)
			return
		}
	}
	setSessionCookie(w, r, "", time.Unix(0, 0))
	w.WriteHeader(http.StatusNoContent)
}

// handleLogoutAll ends all of the user's sessions, on every device, this one included.
func (s *Server) handleLogoutAll(w http.ResponseWriter, r *http.Request) {
	if err := s.auth.LogoutEverywhere(r.Context(), accountFrom(r.Context())); err != nil {
		s.writeErr(w, err)
		return
	}
	setSessionCookie(w, r, "", time.Unix(0, 0))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	acc := accountFrom(r.Context())
	// The cookie of a session that never expires is renewed, so the browser keeps it.
	if c, err := r.Cookie(sessionCookie); err == nil && acc.PersistentSession() && bearer(r) == "" {
		setSessionCookie(w, r, c.Value, time.Now().Add(persistentCookieAge))
	}
	writeJSON(w, http.StatusOK, acc)
}

// handleUpdatePreferences changes the signed-in user's own settings (UI language, appearance, font size, time zone).
func (s *Server) handleUpdatePreferences(w http.ResponseWriter, r *http.Request) {
	var p service.Preferences
	if !decode(w, r, &p) {
		return
	}
	acc, err := s.auth.UpdatePreferences(r.Context(), accountFrom(r.Context()), p)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, acc)
}

func (s *Server) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	var req changePasswordRequest
	if !decode(w, r, &req) {
		return
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		writeCode(w, http.StatusBadRequest, "session_required", "sign in to the web UI to change your password")
		return
	}
	if err := s.auth.ChangePassword(r.Context(), accountFrom(r.Context()), c.Value, req.CurrentPassword, req.NewPassword); err != nil {
		s.writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func setSessionCookie(w http.ResponseWriter, r *http.Request, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		// Behind a TLS-terminating proxy the request itself is plain HTTP.
		Secure: r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
	})
}
