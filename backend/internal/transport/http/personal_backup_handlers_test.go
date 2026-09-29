package http

import (
	"archive/zip"
	"bytes"
	"testing"
)

func zipNames(t *testing.T, data []byte) map[string]bool {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, zf := range zr.File {
		names[zf.Name] = true
	}
	return names
}

func TestPersonalBackupAndRestoreAPI(t *testing.T) {
	f := newAPIFixture(t)
	admin := f.signedIn(adminEmail, adminPassword)
	admin.do("POST", "/api/v1/admin/users", map[string]string{"email": "bob@example.com", "password": "bob-password"}, nil, nil)
	admin.do("POST", "/api/v1/admin/users", map[string]string{"email": "eve@example.com", "password": "eve-password"}, nil, nil)
	bob, eve := f.signedIn("bob@example.com", "bob-password"), f.signedIn("eve@example.com", "eve-password")

	// Nobody is anonymous, and a wrong token gets nowhere.
	if res := f.browser().do("GET", "/api/v1/me/backup", nil, nil, nil); res.StatusCode != 401 {
		t.Fatalf("anonymous backup: %d", res.StatusCode)
	}
	if res := f.script("wrong").do("POST", "/api/v1/me/restore?confirm=replace-my-data", nil, nil, nil); res.StatusCode != 401 {
		t.Fatalf("wrong token restore: %d", res.StatusCode)
	}

	type note struct {
		ID       string `json:"id"`
		Number   int64  `json:"number"`
		FolderID string `json:"folderId"`
		Summary  *struct {
			Title    string `json:"title"`
			Markdown string `json:"markdown"`
		} `json:"summary"`
	}
	var folder struct {
		ID string `json:"id"`
	}
	bob.do("POST", "/api/v1/folders", map[string]string{"name": "Work"}, nil, &folder)
	var work, other note
	bob.do("POST", "/api/v1/recordings/text", map[string]string{"title": "Plan", "markdown": "the plan"}, nil, &work)
	bob.do("PUT", "/api/v1/recordings/"+work.ID+"/folder", map[string]string{"folderId": folder.ID}, nil, nil)
	eve.do("POST", "/api/v1/recordings/text", map[string]string{"title": "Eve's secret", "markdown": "x"}, nil, &other)
	eve.do("POST", "/api/v1/folders", map[string]string{"name": "Eve's folder"}, nil, nil)

	var data []byte
	res := bob.do("GET", "/api/v1/me/backup", nil, nil, &data)
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "application/zip" || res.Header.Get("Content-Disposition") == "" {
		t.Fatalf("backup: %d %v", res.StatusCode, res.Header)
	}
	names := zipNames(t, data)
	if !names["manifest.json"] || !names["db/recordings.bson"] || !names["db/folders.bson"] {
		t.Fatalf("backup files: %v", names)
	}
	// It holds bob's notes only.
	if bytes.Contains(data, []byte("Eve")) || bytes.Contains(data, []byte("secret")) {
		t.Fatal("the backup holds someone else's data")
	}

	// Restoring needs the confirmation and a real backup; the full backup's format is no
	// personal one.
	if res := bob.do("POST", "/api/v1/me/restore", data, nil, nil); res.StatusCode != 400 {
		t.Fatalf("restore without confirmation: %d", res.StatusCode)
	}
	if res := bob.do("POST", "/api/v1/me/restore?confirm=replace-my-data", []byte("junk"), nil, nil); res.StatusCode != 400 {
		t.Fatalf("restore of junk: %d", res.StatusCode)
	}
	var full []byte
	f.script(adminToken).do("GET", "/api/v1/admin/backup", nil, nil, &full)
	if res := bob.do("POST", "/api/v1/me/restore?confirm=replace-my-data", full, nil, nil); res.StatusCode != 400 {
		t.Fatalf("restore of a full backup: %d", res.StatusCode)
	}

	// Bob loses everything, and gets it back.
	bob.do("DELETE", "/api/v1/recordings/"+work.ID, nil, nil, nil)
	bob.do("DELETE", "/api/v1/folders/"+folder.ID, nil, nil, nil)
	var listed []note
	bob.do("GET", "/api/v1/recordings", nil, nil, &listed)
	if len(listed) != 0 {
		t.Fatalf("notes after delete: %d", len(listed))
	}
	var sum struct {
		Collections map[string]int `json:"collections"`
	}
	if res := bob.do("POST", "/api/v1/me/restore?confirm=replace-my-data", data, nil, &sum); res.StatusCode != 200 ||
		sum.Collections["recordings"] != 1 || sum.Collections["folders"] != 1 {
		t.Fatalf("restore: %d %+v", res.StatusCode, sum)
	}
	var back note
	if res := bob.do("GET", "/api/v1/recordings/"+work.ID, nil, nil, &back); res.StatusCode != 200 ||
		back.Number != work.Number || back.FolderID != folder.ID || back.Summary == nil || back.Summary.Markdown != "the plan" {
		t.Fatalf("restored note: %d %+v", res.StatusCode, back)
	}
	// A note made now doesn't reuse the restored note's number.
	var fresh note
	bob.do("POST", "/api/v1/recordings/text", map[string]string{"title": "New", "markdown": "x"}, nil, &fresh)
	if fresh.Number <= work.Number {
		t.Fatalf("new note number %d after restored %d", fresh.Number, work.Number)
	}

	// Eve's data is untouched, and she can't restore bob's notes into her account by
	// accident: they are bob's.
	var eveNotes []note
	eve.do("GET", "/api/v1/recordings", nil, nil, &eveNotes)
	if len(eveNotes) != 1 || eveNotes[0].ID != other.ID {
		t.Fatalf("eve's notes: %+v", eveNotes)
	}
	if res := eve.do("POST", "/api/v1/me/restore?confirm=replace-my-data", data, nil, nil); res.StatusCode != 400 {
		t.Fatalf("restoring bob's backup as eve: %d", res.StatusCode)
	}
	eve.do("GET", "/api/v1/recordings", nil, nil, &eveNotes)
	if len(eveNotes) != 1 {
		t.Fatalf("eve lost notes: %+v", eveNotes)
	}
}
