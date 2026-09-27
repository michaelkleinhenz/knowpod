package http

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	rt "github.com/michaelkleinhenz/knowpod-service/backend/internal/remarkable/remarkabletest"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/service"
)

func TestRemarkablePairPullAndFile(t *testing.T) {
	f := newAPIFixture(t)
	admin := f.signedIn(adminEmail, adminPassword)
	admin.do("POST", "/api/v1/admin/users", map[string]string{"email": "bob@example.com", "password": "bob-password"}, nil, nil)
	bob := f.signedIn("bob@example.com", "bob-password")

	var v service.RemarkableView
	if res := bob.do("GET", "/api/v1/me/remarkable", nil, nil, &v); res.StatusCode != 200 || v.Paired || v.Folder != "reMarkable" {
		t.Fatalf("status: %d %+v", res.StatusCode, v)
	}
	var e errResponse
	if res := bob.do("POST", "/api/v1/me/remarkable/pull", nil, nil, &e); res.StatusCode != 409 || e.Code != "not_paired" {
		t.Fatalf("pull unpaired: %d %+v", res.StatusCode, e)
	}
	if res := bob.do("POST", "/api/v1/me/remarkable/pair", map[string]string{"code": "wrongcod"}, nil, &e); res.StatusCode != 400 || e.Code != "invalid_pairing_code" {
		t.Fatalf("wrong code: %d %+v", res.StatusCode, e)
	}

	f.cloud.AddCode("abcdefgh", "device-bob")
	if res := bob.do("POST", "/api/v1/me/remarkable/pair", map[string]string{"code": "abcdefgh"}, nil, &v); res.StatusCode != 200 || !v.Paired {
		t.Fatalf("pair: %d %+v", res.StatusCode, v)
	}
	var raw []byte
	bob.do("GET", "/api/v1/me/remarkable", nil, nil, &raw)
	if bytes.Contains(raw, []byte("device-bob")) {
		t.Fatal("device token returned to the browser")
	}

	f.cloud.Set(rt.Item{ID: "rm", Name: "reMarkable", Folder: true})
	f.cloud.Set(rt.Item{ID: "n1", Name: "Ideas", Parent: "rm",
		Content: `{"fileType":"notebook","pages":["p1"]}`,
		Files:   map[string][]byte{"p1.rm": rt.Page(rt.Line{Tool: 15, Points: [][3]float32{{0, 10, 2}, {9, 30, 2}}})}})
	if res := bob.do("POST", "/api/v1/me/remarkable/pull", nil, nil, &v); res.StatusCode != 200 || v.LastResult == nil || v.LastResult.Imported != 1 {
		t.Fatalf("pull: %d %+v", res.StatusCode, v)
	}
	// Admin sees nothing of bob's link.
	var av service.RemarkableView
	if admin.do("GET", "/api/v1/me/remarkable", nil, nil, &av); av.Paired {
		t.Fatalf("admin sees bob's link: %+v", av)
	}

	bobUser, _ := f.users.GetByEmail(context.Background(), "bob@example.com")
	rec, err := f.recs.GetByClientID(context.Background(), recording.RemarkableDeviceID(bobUser.ID), "n1")
	if err != nil {
		t.Fatal(err)
	}
	if res := bob.do("GET", "/api/v1/recordings/"+rec.ID+"/file", nil, nil, &e); res.StatusCode != 409 {
		t.Fatalf("file before storing: %d", res.StatusCode)
	}
	f.worker.RunOnce(context.Background())

	var got recording.Recording
	bob.do("GET", "/api/v1/recordings/"+rec.ID, nil, nil, &got)
	if got.Status != recording.StatusStored || got.Type != recording.TypeDocument || got.File == nil || got.Pages != 1 {
		t.Fatalf("processed: %+v", got)
	}
	var pdf []byte
	res := bob.do("GET", "/api/v1/recordings/"+rec.ID+"/file", nil, nil, &pdf)
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "application/pdf" || !bytes.HasPrefix(pdf, []byte("%PDF-")) {
		t.Fatalf("file: %d %s %q", res.StatusCode, res.Header.Get("Content-Type"), pdf[:min(10, len(pdf))])
	}
	if res.Header.Get("X-Frame-Options") != "SAMEORIGIN" || !strings.Contains(res.Header.Get("Content-Security-Policy"), "frame-ancestors 'self'") {
		t.Errorf("the web UI can't frame the PDF: %v", res.Header)
	}
	res = bob.do("GET", "/api/v1/recordings/"+rec.ID+"/file?download=1", nil, map[string]string{"Range": "bytes=0-4"}, &pdf)
	if res.StatusCode != 206 || string(pdf) != "%PDF-" || !strings.HasPrefix(res.Header.Get("Content-Disposition"), `attachment; filename="Ideas - document.pdf"`) {
		t.Fatalf("range download: %d %q %v", res.StatusCode, pdf, res.Header)
	}
	if res := admin.do("GET", "/api/v1/recordings/"+rec.ID+"/file", nil, nil, nil); res.StatusCode != 404 {
		t.Fatalf("admin reads bob's document: %d", res.StatusCode)
	}

	// Unpairing keeps the notes; deleting the user removes the link.
	if res := bob.do("DELETE", "/api/v1/me/remarkable", nil, nil, nil); res.StatusCode != 204 {
		t.Fatalf("unpair: %d", res.StatusCode)
	}
	if _, err := f.recs.Get(context.Background(), rec.ID); err != nil {
		t.Fatalf("note removed with the link: %v", err)
	}
	f.cloud.AddCode("bbbbbbbb", "device-bob-2")
	bob.do("POST", "/api/v1/me/remarkable/pair", map[string]string{"code": "bbbbbbbb"}, nil, nil)
	if res := admin.do("DELETE", "/api/v1/admin/users/"+bobUser.ID, nil, nil, nil); res.StatusCode != 204 {
		t.Fatalf("delete user: %d", res.StatusCode)
	}
	if v, _ := f.remarkable.Status(context.Background(), &service.Account{ID: bobUser.ID}); v.Paired {
		t.Error("link kept after the user was deleted")
	}
}
