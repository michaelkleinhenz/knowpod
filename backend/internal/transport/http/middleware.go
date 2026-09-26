package http

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/device"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/service"
)

type ctxKey string

const (
	deviceKey  ctxKey = "device"
	accountKey ctxKey = "account"
)

// securityHeaders sets conservative security headers. API responses use the strictest
// possible CSP (default-src 'none'). The embedded SPA needs to load its own scripts, styles
// and images, and connect back to the API, so it gets a 'self'-based policy instead.
func securityHeaders(next http.Handler) http.Handler {
	const apiCSP = "default-src 'none'; frame-ancestors 'none'"
	const spaCSP = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; font-src 'self'; connect-src 'self'; frame-ancestors 'none'"

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		if strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/healthz" {
			h.Set("Content-Security-Policy", apiCSP)
		} else {
			h.Set("Content-Security-Policy", spaCSP)
		}
		next.ServeHTTP(w, r)
	})
}

// requireDevice authenticates a recorder by its bearer token and puts it in the context.
func (s *Server) requireDevice(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		dev, err := s.devices.Authenticate(r.Context(), bearer(r))
		if errors.Is(err, service.ErrUnauthorized) {
			writeJSON(w, http.StatusUnauthorized, errResponse{Error: err.Error()})
			return
		}
		if err != nil {
			s.log.Error("device authentication failed", "err", err)
			writeJSON(w, http.StatusInternalServerError, errResponse{Error: "internal error"})
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), deviceKey, dev)))
	})
}

// requireUser admits requests with a valid web UI session and puts the account in the
// context.
func (s *Server) requireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		acc, err := s.sessionAccount(r)
		if err != nil {
			s.writeErr(w, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), accountKey, acc)))
	})
}

// requireAdmin admits signed-in web UI users and requests bearing ADMIN_TOKEN (for scripts).
func (s *Server) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if tok := bearer(r); tok != "" {
			if s.cfg.AdminToken == "" || subtle.ConstantTimeCompare([]byte(tok), []byte(s.cfg.AdminToken)) != 1 {
				writeJSON(w, http.StatusUnauthorized, errResponse{Error: "invalid admin token"})
				return
			}
			next.ServeHTTP(w, r)
			return
		}
		s.requireUser(next).ServeHTTP(w, r)
	})
}

// sessionAccount resolves the session cookie.
func (s *Server) sessionAccount(r *http.Request) (*service.Account, error) {
	c, err := r.Cookie(sessionCookie)
	if err != nil || s.auth == nil {
		return nil, service.ErrNotSignedIn
	}
	return s.auth.Authenticate(r.Context(), c.Value)
}

func bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if strings.HasPrefix(h, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
	}
	return ""
}

func deviceFrom(ctx context.Context) *device.Device {
	d, _ := ctx.Value(deviceKey).(*device.Device)
	return d
}

func accountFrom(ctx context.Context) *service.Account {
	a, _ := ctx.Value(accountKey).(*service.Account)
	return a
}
