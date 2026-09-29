package http

import (
	"archive/zip"
	"bytes"
	"testing"
)

func TestBackupAndRestoreAPI(t *testing.T) {
	f := newAPIFixture(t) // its backup service holds one seeded document
	admin := f.signedIn(adminEmail, adminPassword)

	// Without credentials, and as a plain user, there is no backup.
	if res := f.browser().do("GET", "/api/v1/admin/backup", nil, nil, nil); res.StatusCode != 401 {
		t.Fatalf("anonymous backup: %d", res.StatusCode)
	}
	if res := f.script("wrong").do("GET", "/api/v1/admin/backup", nil, nil, nil); res.StatusCode != 401 {
		t.Fatalf("wrong token backup: %d", res.StatusCode)
	}
	admin.do("POST", "/api/v1/admin/users", map[string]string{"email": "bob@example.com", "password": "bob-password"}, nil, nil)
	if res := f.signedIn("bob@example.com", "bob-password").do("GET", "/api/v1/admin/backup", nil, nil, nil); res.StatusCode != 403 {
		t.Fatalf("user backup: %d", res.StatusCode)
	}

	// The bearer token downloads a zip.
	var data []byte
	res := f.script(adminToken).do("GET", "/api/v1/admin/backup", nil, nil, &data)
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "application/zip" ||
		res.Header.Get("Content-Disposition") == "" {
		t.Fatalf("backup: %d %v", res.StatusCode, res.Header)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, zf := range zr.File {
		names[zf.Name] = true
	}
	if !names["manifest.json"] || !names["db/users.bson"] || !names["objects/seed.txt"] {
		t.Fatalf("backup files: %v", names)
	}

	// Restoring needs the confirmation, and takes the zip.
	if res := admin.do("POST", "/api/v1/admin/restore", data, nil, nil); res.StatusCode != 400 {
		t.Fatalf("restore without confirmation: %d", res.StatusCode)
	}
	if res := admin.do("POST", "/api/v1/admin/restore?confirm=replace-all-data", []byte("junk"), nil, nil); res.StatusCode != 400 {
		t.Fatalf("restore of junk: %d", res.StatusCode)
	}
	var sum struct {
		Objects int `json:"objects"`
	}
	if res := admin.do("POST", "/api/v1/admin/restore?confirm=replace-all-data", data, nil, &sum); res.StatusCode != 200 || sum.Objects != 1 {
		t.Fatalf("restore: %d %+v", res.StatusCode, sum)
	}
	if res := f.script(adminToken).do("POST", "/api/v1/admin/restore?confirm=replace-all-data", data, nil, nil); res.StatusCode != 200 {
		t.Fatalf("restore with token: %d", res.StatusCode)
	}
}
