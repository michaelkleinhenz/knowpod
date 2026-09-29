package http

import (
	"bufio"
	"net/http"
	"strings"
	"testing"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/folder"
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

func TestSharingAFolderOverTheAPI(t *testing.T) {
	f := newAPIFixture(t)
	admin := f.signedIn(adminEmail, adminPassword)
	if res := admin.do("POST", "/api/v1/admin/users", map[string]string{"email": "bob@example.com", "password": "bob-password"}, nil, nil); res.StatusCode != 201 {
		t.Fatalf("create bob: %d", res.StatusCode)
	}
	bob := f.signedIn("bob@example.com", "bob-password")

	var work folder.Folder
	if res := admin.do("POST", "/api/v1/folders", map[string]string{"name": "Work"}, nil, &work); res.StatusCode != 201 {
		t.Fatalf("create folder: %d", res.StatusCode)
	}
	var note recording.Recording
	if res := admin.do("POST", "/api/v1/recordings/text", map[string]string{"title": "Plan", "folderId": work.ID}, nil, &note); res.StatusCode != 201 {
		t.Fatalf("create note: %d", res.StatusCode)
	}
	if res := bob.do("GET", "/api/v1/folders/"+work.ID+"/shares", nil, nil, nil); res.StatusCode != 404 {
		t.Fatalf("bob before sharing: %d", res.StatusCode)
	}
	var sharing service.Sharing
	if res := admin.do("POST", "/api/v1/folders/"+work.ID+"/shares", map[string]string{"email": "bob@example.com", "role": "viewer"}, nil, &sharing); res.StatusCode != 200 ||
		len(sharing.Members) != 1 || sharing.Members[0].Role != recording.RoleViewer {
		t.Fatalf("share: %d %+v", res.StatusCode, sharing)
	}
	bobID := sharing.Members[0].UserID

	var folders []folder.Folder
	bob.do("GET", "/api/v1/folders", nil, nil, &folders)
	if len(folders) != 1 || folders[0].ID != work.ID || folders[0].Access != recording.RoleViewer || !folders[0].Shared {
		t.Fatalf("bob's folders: %+v", folders)
	}
	var list []recording.Recording
	bob.do("GET", "/api/v1/recordings", nil, nil, &list)
	if len(list) != 1 || list[0].ID != note.ID || list[0].FolderID != work.ID {
		t.Fatalf("bob's notes: %+v", list)
	}
	// A viewer adds nothing; made an editor, bob does.
	if res := bob.do("POST", "/api/v1/recordings/text", map[string]string{"title": "Mine", "folderId": work.ID}, nil, nil); res.StatusCode != 403 {
		t.Fatalf("viewer adds: %d", res.StatusCode)
	}
	if res := admin.do("PUT", "/api/v1/folders/"+work.ID+"/shares/"+bobID, map[string]string{"role": "editor"}, nil, &sharing); res.StatusCode != 200 || sharing.Members[0].Role != recording.RoleEditor {
		t.Fatalf("role: %d %+v", res.StatusCode, sharing)
	}
	if res := bob.do("POST", "/api/v1/recordings/text", map[string]string{"title": "Mine", "folderId": work.ID}, nil, nil); res.StatusCode != 201 {
		t.Fatalf("editor adds: %d", res.StatusCode)
	}
	// Bob leaves.
	if res := bob.do("DELETE", "/api/v1/folders/"+work.ID+"/shares/"+bobID, nil, nil, nil); res.StatusCode != 204 {
		t.Fatalf("leave: %d", res.StatusCode)
	}
	bob.do("GET", "/api/v1/recordings", nil, nil, &list)
	if len(list) != 0 {
		t.Fatalf("bob's notes after leaving: %+v", list)
	}
}

func TestTaskAssignee(t *testing.T) {
	f := newAPIFixture(t)
	admin := f.signedIn(adminEmail, adminPassword)
	var bobUser struct{ ID string }
	admin.do("POST", "/api/v1/admin/users", map[string]string{"email": "bob@example.com", "password": "bob-password"}, nil, &bobUser)
	admin.do("POST", "/api/v1/admin/users", map[string]string{"email": "carol@example.com", "password": "carol-password"}, nil, nil)
	bob := f.signedIn("bob@example.com", "bob-password")

	var note recording.Recording
	admin.do("POST", "/api/v1/recordings/text", map[string]any{"title": "Call Anna", "markdown": ""}, nil, &note)
	path := "/api/v1/recordings/" + note.ID
	admin.do("POST", path+"/shares", map[string]string{"email": "bob@example.com", "role": "editor"}, nil, nil)

	// Only tasks can be assigned.
	var e errResponse
	if res := admin.do("PUT", path+"/assignee", map[string]string{"assigneeId": bobUser.ID}, nil, &e); res.StatusCode != 400 {
		t.Fatalf("assign a plain note: %d %+v", res.StatusCode, e)
	}
	admin.do("PUT", path+"/priority", map[string]int{"priority": 1}, nil, nil)

	// The creator is the reporter; a user the note is shared with can be assigned, by any editor.
	var sharing service.Sharing
	bob.do("GET", path+"/shares", nil, nil, &sharing)
	if sharing.Reporter == nil || sharing.Reporter.Email != adminEmail {
		t.Fatalf("reporter: %+v", sharing.Reporter)
	}
	var task recording.Recording
	if res := bob.do("PUT", path+"/assignee", map[string]string{"assigneeId": bobUser.ID}, nil, &task); res.StatusCode != 200 || task.AssigneeID != bobUser.ID {
		t.Fatalf("assign: %d %+v", res.StatusCode, task.AssigneeID)
	}
	// Not to someone without access.
	var carol struct{ ID string }
	var users []struct{ ID, Email string }
	admin.do("GET", "/api/v1/admin/users", nil, nil, &users)
	for _, u := range users {
		if u.Email == "carol@example.com" {
			carol.ID = u.ID
		}
	}
	if res := admin.do("PUT", path+"/assignee", map[string]string{"assigneeId": carol.ID}, nil, &e); res.StatusCode != 400 {
		t.Fatalf("assign to carol: %d %+v", res.StatusCode, e)
	}

	// Unsharing unassigns.
	if res := admin.do("DELETE", path+"/shares/"+bobUser.ID, nil, nil, nil); res.StatusCode != 200 {
		t.Fatalf("unshare: %d", res.StatusCode)
	}
	var after recording.Recording
	admin.do("GET", path, nil, nil, &after)
	if after.AssigneeID != "" {
		t.Fatalf("assignee after unsharing: %q", after.AssigneeID)
	}
}
