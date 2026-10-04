package http

import (
	"net/http"
	"testing"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/service"
)

func TestWebLoginFlow(t *testing.T) {
	f := newAPIFixture(t)
	b := f.browser()

	if res := b.do("GET", "/api/v1/auth/me", nil, nil, nil); res.StatusCode != 401 {
		t.Fatalf("me before login: %d", res.StatusCode)
	}
	if res := b.do("POST", "/api/v1/auth/login", loginRequest{adminEmail, "nope"}, nil, nil); res.StatusCode != 401 {
		t.Fatalf("bad login: %d", res.StatusCode)
	}
	res := b.do("POST", "/api/v1/auth/login", loginRequest{adminEmail, adminPassword}, nil, nil)
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

	var me service.Account
	if res := b.do("GET", "/api/v1/auth/me", nil, nil, &me); res.StatusCode != 200 || me.Email != adminEmail || me.Role != "admin" || me.ID == "" {
		t.Fatalf("me: %d %+v", res.StatusCode, me)
	}
	if res := b.do("PUT", "/api/v1/auth/password", changePasswordRequest{"wrong", "new-password"}, nil, nil); res.StatusCode != 403 {
		t.Fatalf("change with wrong current: %d", res.StatusCode)
	}
	if res := b.do("PUT", "/api/v1/auth/password", changePasswordRequest{adminPassword, "short"}, nil, nil); res.StatusCode != 400 {
		t.Fatalf("change to weak: %d", res.StatusCode)
	}
	if res := b.do("PUT", "/api/v1/auth/password", changePasswordRequest{adminPassword, "new-password"}, nil, nil); res.StatusCode != 204 {
		t.Fatalf("change: %d", res.StatusCode)
	}
	if res := b.do("POST", "/api/v1/auth/logout", nil, nil, nil); res.StatusCode != 204 {
		t.Fatalf("logout: %d", res.StatusCode)
	}
	if res := b.do("GET", "/api/v1/auth/me", nil, nil, nil); res.StatusCode != 401 {
		t.Fatalf("me after logout: %d", res.StatusCode)
	}
	if res := b.do("POST", "/api/v1/auth/login", loginRequest{adminEmail, adminPassword}, nil, nil); res.StatusCode != 401 {
		t.Fatalf("env password after change: %d", res.StatusCode)
	}
	f.signedIn(adminEmail, "new-password")
}

func TestLoginIsRateLimited(t *testing.T) {
	b := newAPIFixture(t).browser()
	var last int
	for i := 0; i < 11; i++ {
		last = b.do("POST", "/api/v1/auth/login", loginRequest{adminEmail, "nope"}, nil, nil).StatusCode
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("11th attempt = %d, want 429", last)
	}
}

func TestAccessControl(t *testing.T) {
	f := newAPIFixture(t)
	admin := f.signedIn(adminEmail, adminPassword)
	if res := admin.do("POST", "/api/v1/admin/users", map[string]string{"email": "bob@example.com", "password": "bob-password"}, nil, nil); res.StatusCode != 201 {
		t.Fatalf("create bob: %d", res.StatusCode)
	}
	bob := f.signedIn("bob@example.com", "bob-password")

	for _, tc := range []struct {
		name   string
		c      *client
		method string
		path   string
		want   int
	}{
		{"anonymous devices", f.browser(), "GET", "/api/v1/devices", 401},
		{"bad token", f.script("wrong"), "GET", "/api/v1/devices", 401},
		{"device token isn't an admin token", f.script("kpd_x"), "GET", "/api/v1/admin/users", 401},
		{"user devices", bob, "GET", "/api/v1/devices", 200},
		{"user may not list users", bob, "GET", "/api/v1/admin/users", 403},
		{"user may not read AI settings", bob, "GET", "/api/v1/admin/settings/openrouter", 403},
		{"user may not test ElevenLabs", bob, "POST", "/api/v1/admin/settings/elevenlabs/test", 403},
		{"user may read AI status", bob, "GET", "/api/v1/ai/status", 200},
		{"admin lists users", admin, "GET", "/api/v1/admin/users", 200},
		{"script lists users", f.script(adminToken), "GET", "/api/v1/admin/users", 200},
		{"anonymous upload", f.browser(), "POST", "/api/v1/uploads", 401},
	} {
		if res := tc.c.do(tc.method, tc.path, nil, nil, nil); res.StatusCode != tc.want {
			t.Errorf("%s: %s %s = %d, want %d", tc.name, tc.method, tc.path, res.StatusCode, tc.want)
		}
	}
}
