package http

import (
	"strings"
	"testing"
)

type featureNote struct {
	ID       string `json:"id"`
	Revision int64  `json:"revision"`
	Template bool   `json:"template"`
	Public   *struct {
		Token string `json:"token"`
	} `json:"public"`
	Summary *struct {
		Title    string `json:"title"`
		Markdown string `json:"markdown"`
	} `json:"summary"`
}

func TestNoteVersionsAPI(t *testing.T) {
	f := newAPIFixture(t)
	admin := f.signedIn(adminEmail, adminPassword)
	admin.do("POST", "/api/v1/admin/users", map[string]string{"email": "bob@example.com", "password": "bob-password"}, nil, nil)
	bob := f.signedIn("bob@example.com", "bob-password")

	var note featureNote
	admin.do("POST", "/api/v1/recordings/text", map[string]string{"title": "Plan", "markdown": "first"}, nil, &note)
	admin.do("PUT", "/api/v1/recordings/"+note.ID+"/summary", map[string]string{"title": "Plan", "markdown": "second"}, nil, &note)

	var versions []struct {
		ID       string `json:"id"`
		Markdown string `json:"markdown"`
		Reason   string `json:"reason"`
	}
	if res := admin.do("GET", "/api/v1/recordings/"+note.ID+"/versions", nil, nil, &versions); res.StatusCode != 200 || len(versions) != 1 {
		t.Fatalf("versions: %d %+v", res.StatusCode, versions)
	}
	if versions[0].Markdown != "" || versions[0].Reason != "edit" {
		t.Fatalf("listed version: %+v", versions[0])
	}
	var v struct {
		Markdown string `json:"markdown"`
	}
	if res := admin.do("GET", "/api/v1/recordings/"+note.ID+"/versions/"+versions[0].ID, nil, nil, &v); res.StatusCode != 200 || v.Markdown != "first" {
		t.Fatalf("version: %d %+v", res.StatusCode, v)
	}
	// Others don't see the note's history.
	if res := bob.do("GET", "/api/v1/recordings/"+note.ID+"/versions", nil, nil, nil); res.StatusCode != 404 {
		t.Fatalf("bob's versions: %d", res.StatusCode)
	}
	// Restoring on an older revision is refused; on the current one it works.
	old := note.Revision - 1
	if res := admin.do("POST", "/api/v1/recordings/"+note.ID+"/versions/"+versions[0].ID+"/restore", map[string]any{"baseRevision": old}, nil, nil); res.StatusCode != 409 {
		t.Fatalf("restore on an old revision: %d", res.StatusCode)
	}
	if res := admin.do("POST", "/api/v1/recordings/"+note.ID+"/versions/"+versions[0].ID+"/restore", map[string]any{"baseRevision": note.Revision}, nil, &note); res.StatusCode != 200 || note.Summary.Markdown != "first" {
		t.Fatalf("restore: %d %+v", res.StatusCode, note.Summary)
	}
	if res := admin.do("GET", "/api/v1/recordings/"+note.ID+"/versions", nil, nil, &versions); res.StatusCode != 200 || len(versions) != 2 || versions[0].Reason != "restore" {
		t.Fatalf("versions after restoring: %+v", versions)
	}
}

func TestTemplateAndPublishAPI(t *testing.T) {
	f := newAPIFixture(t)
	admin := f.signedIn(adminEmail, adminPassword)
	var note featureNote
	admin.do("POST", "/api/v1/recordings/text", map[string]string{"title": "Recipe", "markdown": "| a | b |\n| --- | --- |\n| 1 | 2 |"}, nil, &note)

	if res := admin.do("PUT", "/api/v1/recordings/"+note.ID+"/template", map[string]bool{"template": true}, nil, &note); res.StatusCode != 200 || !note.Template {
		t.Fatalf("template: %d %+v", res.StatusCode, note)
	}

	if res := admin.do("PUT", "/api/v1/recordings/"+note.ID+"/public", nil, nil, &note); res.StatusCode != 200 || note.Public == nil {
		t.Fatalf("publish: %d %+v", res.StatusCode, note)
	}
	// Anyone with the link reads the note, without signing in.
	anon := f.browser()
	var pub struct {
		Title    string `json:"title"`
		Markdown string `json:"markdown"`
	}
	res := anon.do("GET", "/api/v1/public/"+note.Public.Token, nil, nil, &pub)
	if res.StatusCode != 200 || pub.Title != "Recipe" || !strings.Contains(pub.Markdown, "| 1 | 2 |") {
		t.Fatalf("public note: %d %+v", res.StatusCode, pub)
	}
	if res.Header.Get("X-Robots-Tag") != "noindex" {
		t.Fatalf("public note headers: %v", res.Header)
	}
	if res := anon.do("GET", "/api/v1/public/"+strings.Repeat("a", 64), nil, nil, nil); res.StatusCode != 404 {
		t.Fatalf("unknown link: %d", res.StatusCode)
	}

	token := note.Public.Token
	var unpublished featureNote
	if res := admin.do("DELETE", "/api/v1/recordings/"+note.ID+"/public", nil, nil, &unpublished); res.StatusCode != 200 || unpublished.Public != nil {
		t.Fatalf("unpublish: %d %+v", res.StatusCode, unpublished)
	}
	if res := anon.do("GET", "/api/v1/public/"+token, nil, nil, nil); res.StatusCode != 404 {
		t.Fatalf("unpublished link: %d", res.StatusCode)
	}
}

func TestAIWriteAPI(t *testing.T) {
	f := newAPIFixture(t)
	admin := f.signedIn(adminEmail, adminPassword)
	body := map[string]string{"action": "improve", "text": "some text"}
	if res := admin.do("POST", "/api/v1/ai/write", body, nil, nil); res.StatusCode != 409 {
		t.Fatalf("write without AI models: %d", res.StatusCode)
	}
	f.withAI()
	var out struct {
		Markdown string `json:"markdown"`
		Model    string `json:"model"`
	}
	if res := admin.do("POST", "/api/v1/ai/write", body, nil, &out); res.StatusCode != 200 || out.Markdown != "Written" || out.Model != "test/model" {
		t.Fatalf("write: %d %+v", res.StatusCode, out)
	}
	if res := admin.do("POST", "/api/v1/ai/write", map[string]string{"action": "nope", "text": "x"}, nil, nil); res.StatusCode != 400 {
		t.Fatalf("unknown action: %d", res.StatusCode)
	}
	if res := f.browser().do("POST", "/api/v1/ai/write", body, nil, nil); res.StatusCode != 401 {
		t.Fatalf("signed out: %d", res.StatusCode)
	}
}
