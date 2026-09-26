package http

import (
	"net/http"
	"strings"
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
