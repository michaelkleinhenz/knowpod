package http

import (
	"bufio"
	"net/http"
	"strings"
	"testing"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/service"
)

func TestSharingOverTheAPI(t *testing.T) {
	f := newAPIFixture(t)
	admin := f.signedIn(adminEmail, adminPassword)
	if res := admin.do("POST", "/api/v1/admin/users", map[string]string{"email": "bob@example.com", "password": "bob-password"}, nil, nil); res.StatusCode != 201 {
		t.Fatalf("create bob: %d", res.StatusCode)
	}
	bob := f.signedIn("bob@example.com", "bob-password")

	var note recording.Recording
	if res := admin.do("POST", "/api/v1/recordings/text", map[string]string{"title": "Groceries", "markdown": "milk"}, nil, &note); res.StatusCode != 201 {
		t.Fatalf("create: %d", res.StatusCode)
	}
	if res := bob.do("GET", "/api/v1/recordings/"+note.ID, nil, nil, nil); res.StatusCode != 404 {
		t.Fatalf("bob before sharing: %d", res.StatusCode)
	}
	var sharing service.Sharing
	if res := admin.do("POST", "/api/v1/recordings/"+note.ID+"/shares", map[string]string{"email": "bob@example.com", "role": "editor"}, nil, &sharing); res.StatusCode != 200 ||
		len(sharing.Members) != 1 || sharing.Members[0].Email != "bob@example.com" {
		t.Fatalf("share: %d %+v", res.StatusCode, sharing)
	}

	var list []recording.Recording
	bob.do("GET", "/api/v1/recordings", nil, nil, &list)
	if len(list) != 1 || list[0].ID != note.ID || list[0].Access != recording.RoleEditor || !list[0].Shared {
		t.Fatalf("bob's list: %+v", list)
	}

	// Bob's app listens for changes.
	req, _ := http.NewRequest("GET", f.srv.URL+"/api/v1/me/events", nil)
	res, err := bob.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	lines := bufio.NewScanner(res.Body)
	if res.StatusCode != 200 || !lines.Scan() || lines.Text() != "retry: 5000" || !lines.Scan() {
		t.Fatalf("stream: %d %q", res.StatusCode, lines.Text())
	}

	// The owner edits; bob's edit made on the older text is refused …
	var edited recording.Recording
	if res := admin.do("PUT", "/api/v1/recordings/"+note.ID+"/summary", map[string]any{"title": "Groceries", "markdown": "milk, eggs", "baseRevision": note.Revision}, nil, &edited); res.StatusCode != 200 {
		t.Fatalf("owner's edit: %d", res.StatusCode)
	}
	var e errResponse
	if res := bob.do("PUT", "/api/v1/recordings/"+note.ID+"/summary", map[string]any{"title": "Groceries", "markdown": "bread", "baseRevision": note.Revision}, nil, &e); res.StatusCode != 409 || e.Code != "changed" {
		t.Fatalf("stale edit: %d %+v", res.StatusCode, e)
	}
	// … and bob's app heard about the owner's edit.
	var got []string
	for lines.Scan() && lines.Text() != "" {
		got = append(got, lines.Text())
	}
	if len(got) != 2 || got[0] != "event: note" || !strings.Contains(got[1], `"id":"`+note.ID+`"`) {
		t.Fatalf("event %q", got)
	}

	// Bob leaves the note.
	if res := bob.do("DELETE", "/api/v1/recordings/"+note.ID+"/shares/"+sharing.Members[0].UserID, nil, nil, nil); res.StatusCode != 204 {
		t.Fatalf("leave: %d", res.StatusCode)
	}
	if res := bob.do("GET", "/api/v1/recordings/"+note.ID+"/shares", nil, nil, nil); res.StatusCode != 404 {
		t.Fatalf("after leaving: %d", res.StatusCode)
	}
	f.events.Shutdown()
}
