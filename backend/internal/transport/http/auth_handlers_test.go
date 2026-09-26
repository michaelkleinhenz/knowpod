package http

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/config"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/repository/memory"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/service"
)

// browser is an HTTP client with a cookie jar, talking to a server with auth wired up.
type browser struct {
	t      *testing.T
	base   string
	client *http.Client
}

func newBrowser(t *testing.T, srv *httptest.Server) *browser {
	jar, _ := cookiejar.New(nil)
	return &browser{t: t, base: srv.URL, client: &http.Client{Jar: jar}}
}

func newAuthServer(t *testing.T) *httptest.Server {
	t.Helper()
	s := NewServer(Deps{
		Cfg:        config.Config{AdminToken: adminToken},
		Log:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		Auth:       service.NewAuthService(memory.NewUsers(), memory.NewSessions(), "admin@example.com", "env-secret", time.Hour),
		Devices:    service.NewDeviceService(memory.NewDevices()),
		Recordings: memory.NewRecordings(),
	})
	srv := httptest.NewServer(s.Router())
	t.Cleanup(srv.Close)
	return srv
}

func (b *browser) do(method, path string, body any) *http.Response {
	b.t.Helper()
	var r io.Reader
	if body != nil {
		data, _ := json.Marshal(body)
		r = bytes.NewReader(data)
	}
	req, _ := http.NewRequest(method, b.base+path, r)
	req.Header.Set("Content-Type", "application/json")
	res, err := b.client.Do(req)
	if err != nil {
		b.t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, res.Body)
	res.Body.Close()
	return res
}

func TestWebLoginFlow(t *testing.T) {
	srv := newAuthServer(t)
	b := newBrowser(t, srv)

	if res := b.do("GET", "/api/v1/auth/me", nil); res.StatusCode != 401 {
		t.Fatalf("me before login: %d", res.StatusCode)
	}
	if res := b.do("GET", "/api/v1/admin/devices", nil); res.StatusCode != 401 {
		t.Fatalf("admin API before login: %d", res.StatusCode)
	}
	if res := b.do("POST", "/api/v1/auth/login", loginRequest{"admin@example.com", "nope"}); res.StatusCode != 401 {
		t.Fatalf("bad login: %d", res.StatusCode)
	}

	res := b.do("POST", "/api/v1/auth/login", loginRequest{"admin@example.com", "env-secret"})
	if res.StatusCode != 200 {
		t.Fatalf("login: %d", res.StatusCode)
	}
	var cookie *http.Cookie
	for _, c := range res.Cookies() {
		if c.Name == sessionCookie {
			cookie = c
		}
	}
	if cookie == nil || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("session cookie = %+v", cookie)
	}

	if res := b.do("GET", "/api/v1/auth/me", nil); res.StatusCode != 200 {
		t.Fatalf("me: %d", res.StatusCode)
	}
	// A signed-in user can use the admin API.
	if res := b.do("GET", "/api/v1/admin/devices", nil); res.StatusCode != 200 {
		t.Fatalf("admin API with session: %d", res.StatusCode)
	}

	if res := b.do("PUT", "/api/v1/auth/password", changePasswordRequest{"wrong", "new-password"}); res.StatusCode != 403 {
		t.Fatalf("change with wrong current: %d", res.StatusCode)
	}
	if res := b.do("PUT", "/api/v1/auth/password", changePasswordRequest{"env-secret", "short"}); res.StatusCode != 400 {
		t.Fatalf("change to weak: %d", res.StatusCode)
	}
	if res := b.do("PUT", "/api/v1/auth/password", changePasswordRequest{"env-secret", "new-password"}); res.StatusCode != 204 {
		t.Fatalf("change: %d", res.StatusCode)
	}

	if res := b.do("POST", "/api/v1/auth/logout", nil); res.StatusCode != 204 {
		t.Fatalf("logout: %d", res.StatusCode)
	}
	if res := b.do("GET", "/api/v1/auth/me", nil); res.StatusCode != 401 {
		t.Fatalf("me after logout: %d", res.StatusCode)
	}

	// The environment password no longer works; the new one does.
	if res := b.do("POST", "/api/v1/auth/login", loginRequest{"admin@example.com", "env-secret"}); res.StatusCode != 401 {
		t.Fatalf("login with env password after change: %d", res.StatusCode)
	}
	if res := b.do("POST", "/api/v1/auth/login", loginRequest{"admin@example.com", "new-password"}); res.StatusCode != 200 {
		t.Fatalf("login with new password: %d", res.StatusCode)
	}
}

func TestLoginIsRateLimited(t *testing.T) {
	b := newBrowser(t, newAuthServer(t))
	var last int
	for i := 0; i < 11; i++ {
		last = b.do("POST", "/api/v1/auth/login", loginRequest{"admin@example.com", "nope"}).StatusCode
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("11th attempt = %d, want 429", last)
	}
}
