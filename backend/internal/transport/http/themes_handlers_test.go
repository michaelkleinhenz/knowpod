package http

import (
	"testing"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/service"
)

func TestThemesAndPreferencesAPI(t *testing.T) {
	f := newAPIFixture(t)
	admin := f.signedIn(adminEmail, adminPassword)
	admin.do("POST", "/api/v1/admin/users", map[string]string{"email": "bob@example.com", "password": "bob-password"}, nil, nil)
	bob := f.signedIn("bob@example.com", "bob-password")

	var created service.ThemeView
	if res := bob.do("POST", "/api/v1/themes", service.ThemeInput{Name: "Standup", Description: "Yesterday · Today", Instructions: "## Yesterday\n## Today"}, nil, &created); res.StatusCode != 201 {
		t.Fatalf("create theme: %d", res.StatusCode)
	}
	var list []service.ThemeView
	bob.do("GET", "/api/v1/themes", nil, nil, &list)
	if len(list) < 2 || list[0].ID != "auto" || !list[0].BuiltIn || list[len(list)-1].ID != created.ID {
		t.Fatalf("bob's themes: %+v", list)
	}
	admin.do("GET", "/api/v1/themes", nil, nil, &list)
	for _, th := range list {
		if th.ID == created.ID {
			t.Fatal("admin sees bob's theme")
		}
	}
	if res := admin.do("DELETE", "/api/v1/themes/"+created.ID, nil, nil, nil); res.StatusCode != 404 {
		t.Fatalf("admin deleting bob's theme: %d", res.StatusCode)
	}
	if res := bob.do("PUT", "/api/v1/themes/"+created.ID, service.ThemeInput{Name: "Standup 2", Instructions: "x"}, nil, nil); res.StatusCode != 200 {
		t.Fatalf("update: %d", res.StatusCode)
	}
	if res := bob.do("DELETE", "/api/v1/themes/"+created.ID, nil, nil, nil); res.StatusCode != 204 {
		t.Fatalf("delete: %d", res.StatusCode)
	}

	var langs []string
	if res := bob.do("GET", "/api/v1/ai/languages", nil, nil, &langs); res.StatusCode != 200 || len(langs) < 10 {
		t.Fatalf("languages: %d %v", res.StatusCode, langs)
	}

	var acc service.Account
	if res := bob.do("PUT", "/api/v1/me/preferences", map[string]string{"language": "de"}, nil, &acc); res.StatusCode != 200 || acc.Language != "de" {
		t.Fatalf("preferences: %d %+v", res.StatusCode, acc)
	}
	bob.do("GET", "/api/v1/auth/me", nil, nil, &acc)
	if acc.Language != "de" {
		t.Fatalf("me: %+v", acc)
	}
	var e errResponse
	if res := bob.do("PUT", "/api/v1/me/preferences", map[string]string{"language": "xx"}, nil, &e); res.StatusCode != 400 || e.Code != "invalid_input" {
		t.Fatalf("bad language: %d %+v", res.StatusCode, e)
	}
}

func TestErrorCodes(t *testing.T) {
	f := newAPIFixture(t)
	var e errResponse
	if res := f.browser().do("POST", "/api/v1/auth/login", loginRequest{adminEmail, "nope"}, nil, &e); res.StatusCode != 401 || e.Code != "invalid_login" {
		t.Fatalf("login: %d %+v", res.StatusCode, e)
	}
	admin := f.signedIn(adminEmail, adminPassword)
	var me service.Account
	admin.do("GET", "/api/v1/auth/me", nil, nil, &me)
	if res := admin.do("DELETE", "/api/v1/admin/users/"+me.ID, nil, nil, &e); res.StatusCode != 403 || (e.Code != "cannot_delete_self" && e.Code != "builtin_admin") {
		t.Fatalf("delete self: %d %+v", res.StatusCode, e)
	}
	if res := admin.do("GET", "/api/v1/recordings/unknown", nil, nil, &e); res.StatusCode != 404 || e.Code != "not_found" {
		t.Fatalf("not found: %d %+v", res.StatusCode, e)
	}
	if res := admin.do("POST", "/api/v1/admin/users", map[string]string{"email": "x@example.com", "password": "short"}, nil, &e); e.Code != "weak_password" {
		t.Fatalf("weak password: %d %+v", res.StatusCode, e)
	}
}
