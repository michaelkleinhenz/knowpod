package http

import (
	"testing"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/service"
)

func TestUserManagementAPI(t *testing.T) {
	f := newAPIFixture(t)
	admin := f.signedIn(adminEmail, adminPassword)

	var bob service.UserView
	if res := admin.do("POST", "/api/v1/admin/users", map[string]string{"email": "bob@example.com", "password": "bob-password", "role": "user"}, nil, &bob); res.StatusCode != 201 {
		t.Fatalf("create: %d", res.StatusCode)
	}
	if res := admin.do("POST", "/api/v1/admin/users", map[string]string{"email": "bob@example.com", "password": "bob-password"}, nil, nil); res.StatusCode != 409 {
		t.Fatalf("duplicate: %d", res.StatusCode)
	}
	var list []service.UserView
	admin.do("GET", "/api/v1/admin/users", nil, nil, &list)
	if len(list) != 2 {
		t.Fatalf("list: %+v", list)
	}
	var builtIn service.UserView
	for _, u := range list {
		if u.BuiltIn {
			builtIn = u
		}
	}

	bobBrowser := f.signedIn("bob@example.com", "bob-password")
	if res := admin.do("PUT", "/api/v1/admin/users/"+bob.ID, map[string]string{"role": "admin"}, nil, nil); res.StatusCode != 200 {
		t.Fatalf("promote: %d", res.StatusCode)
	}
	var me service.Account
	bobBrowser.do("GET", "/api/v1/auth/me", nil, nil, &me)
	if me.Role != "admin" {
		t.Fatalf("role change not effective in existing session: %+v", me)
	}

	if res := admin.do("PUT", "/api/v1/admin/users/"+bob.ID+"/password", map[string]string{"password": "reset-password"}, nil, nil); res.StatusCode != 204 {
		t.Fatalf("set password: %d", res.StatusCode)
	}
	if res := bobBrowser.do("GET", "/api/v1/auth/me", nil, nil, nil); res.StatusCode != 401 {
		t.Fatalf("bob still signed in after reset: %d", res.StatusCode)
	}
	if res := admin.do("GET", "/api/v1/auth/me", nil, nil, nil); res.StatusCode != 200 {
		t.Fatalf("admin signed out by resetting someone else: %d", res.StatusCode)
	}
	f.signedIn("bob@example.com", "reset-password")

	if res := admin.do("DELETE", "/api/v1/admin/users/"+builtIn.ID, nil, nil, nil); res.StatusCode != 403 {
		t.Fatalf("delete built-in/self: %d", res.StatusCode)
	}
	if res := admin.do("DELETE", "/api/v1/admin/users/"+bob.ID, nil, nil, nil); res.StatusCode != 204 {
		t.Fatalf("delete: %d", res.StatusCode)
	}
	if res := admin.do("DELETE", "/api/v1/admin/users/"+bob.ID, nil, nil, nil); res.StatusCode != 404 {
		t.Fatalf("delete again: %d", res.StatusCode)
	}
}
