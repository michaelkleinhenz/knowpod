package http

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"strconv"
	"testing"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/audio/audiotest"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
)

func TestDeviceUploadEndToEnd(t *testing.T) {
	f := newAPIFixture(t)
	admin := f.signedIn(adminEmail, adminPassword)

	var reg registerDeviceResponse
	if res := admin.do("POST", "/api/v1/devices", map[string]string{"name": "rec-1"}, nil, &reg); res.StatusCode != 201 || reg.Token == "" {
		t.Fatalf("register: %d %+v", res.StatusCode, reg)
	}
	dev := f.script(reg.Token)

	wav := audiotest.WAV(16000, 16, audiotest.Samples(1, 16000, 16))
	sum := sha256.Sum256(wav)
	create := createUploadRequest{RecordingID: "2026-09-26T10-00-00", Size: int64(len(wav)), SHA256: hex.EncodeToString(sum[:])}

	var up uploadResponse
	if res := dev.do("POST", "/api/v1/uploads", create, nil, &up); res.StatusCode != 201 || up.Offset != 0 {
		t.Fatalf("create: %d %+v", res.StatusCode, up)
	}
	var again uploadResponse
	if res := dev.do("POST", "/api/v1/uploads", create, nil, &again); res.StatusCode != 200 || again.UploadID != up.UploadID {
		t.Fatalf("create retry: %d %+v", res.StatusCode, again)
	}

	path := "/api/v1/uploads/" + up.UploadID
	half := len(wav) / 2
	patch := func(off int, chunk []byte, out any) int {
		return dev.do("PATCH", path, chunk, map[string]string{uploadOffsetHeader: strconv.Itoa(off)}, out).StatusCode
	}
	if code := patch(0, wav[:half], &up); code != 200 || up.Offset != int64(half) {
		t.Fatalf("first chunk: %d %+v", code, up)
	}
	var mismatch errResponse
	if code := patch(0, wav[:10], &mismatch); code != 409 || mismatch.Offset == nil || *mismatch.Offset != int64(half) {
		t.Fatalf("offset mismatch: %d %+v", code, mismatch)
	}
	if code := patch(half, append(append([]byte{}, wav[half:]...), 0), nil); code != 413 {
		t.Fatalf("overrun: %d", code)
	}
	if code := patch(half, wav[half:], &up); code != 200 || up.Status != "received" {
		t.Fatalf("last chunk: %d %+v", code, up)
	}

	id := up.UploadID
	if res := admin.do("GET", "/api/v1/recordings/"+id+"/audio", nil, nil, nil); res.StatusCode != 409 {
		t.Fatalf("audio before archive: %d", res.StatusCode)
	}
	if n := f.worker.RunOnce(context.Background()); n != 1 {
		t.Fatalf("worker processed %d", n)
	}
	var rec recording.Recording
	if res := admin.do("GET", "/api/v1/recordings/"+id, nil, nil, &rec); res.StatusCode != 200 || rec.Status != recording.StatusStored || rec.OwnerID == "" {
		t.Fatalf("recording: %d %+v", res.StatusCode, rec)
	}
	var flac []byte
	if res := admin.do("GET", "/api/v1/recordings/"+id+"/audio", nil, nil, &flac); res.StatusCode != 200 || string(flac[:4]) != "fLaC" {
		t.Fatalf("audio: %d", res.StatusCode)
	}

	// Rotating the token and removing the device lock the old credentials out.
	var rotated registerDeviceResponse
	if res := admin.do("POST", "/api/v1/devices/"+reg.Device.ID+"/token", nil, nil, &rotated); res.StatusCode != 200 || rotated.Token == reg.Token {
		t.Fatalf("rotate: %d", res.StatusCode)
	}
	if res := dev.do("GET", path, nil, nil, nil); res.StatusCode != 401 {
		t.Fatalf("old token after rotation: %d", res.StatusCode)
	}
	if res := admin.do("DELETE", "/api/v1/devices/"+reg.Device.ID, nil, nil, nil); res.StatusCode != 204 {
		t.Fatalf("revoke: %d", res.StatusCode)
	}
	if res := f.script(rotated.Token).do("GET", path, nil, nil, nil); res.StatusCode != 401 {
		t.Fatalf("revoked device: %d", res.StatusCode)
	}
}

func TestManualUploadAndIsolation(t *testing.T) {
	f := newAPIFixture(t)
	admin := f.signedIn(adminEmail, adminPassword)
	admin.do("POST", "/api/v1/admin/users", map[string]string{"email": "bob@example.com", "password": "bob-password"}, nil, nil)
	bob := f.signedIn("bob@example.com", "bob-password")

	wav := audiotest.WAV(16000, 16, audiotest.Samples(1, 8000, 16))
	var rec recording.Recording
	res := bob.do("POST", "/api/v1/recordings", wav, map[string]string{
		"Content-Type": "audio/wav", "X-Filename": url.QueryEscape("Standup notes.wav"), "X-Recorded-At": "2026-09-25T09:30:00Z",
	}, &rec)
	if res.StatusCode != 201 || rec.Title != "Standup notes" || rec.Source != recording.SourceUpload || rec.RecordedAt == nil {
		t.Fatalf("upload: %d %+v", res.StatusCode, rec)
	}
	if res := bob.do("POST", "/api/v1/recordings", []byte("OggS\x00\x02 not supported"), nil, nil); res.StatusCode != 415 {
		t.Fatalf("ogg upload: %d", res.StatusCode)
	}

	// Each user sees only their own recordings; ADMIN_TOKEN sees all.
	var list []recording.Recording
	bob.do("GET", "/api/v1/recordings", nil, nil, &list)
	if len(list) != 1 {
		t.Fatalf("bob sees %d recordings", len(list))
	}
	admin.do("GET", "/api/v1/recordings", nil, nil, &list)
	if len(list) != 0 {
		t.Fatalf("admin sees %d of bob's recordings in the UI", len(list))
	}
	if res := admin.do("GET", "/api/v1/recordings/"+rec.ID, nil, nil, nil); res.StatusCode != 404 {
		t.Fatalf("admin opening bob's recording: %d", res.StatusCode)
	}
	if res := admin.do("DELETE", "/api/v1/recordings/"+rec.ID, nil, nil, nil); res.StatusCode != 404 {
		t.Fatalf("admin deleting bob's recording: %d", res.StatusCode)
	}
	f.script(adminToken).do("GET", "/api/v1/recordings", nil, nil, &list)
	if len(list) != 1 {
		t.Fatalf("ADMIN_TOKEN sees %d recordings", len(list))
	}

	// The uploaded WAV is archived as FLAC.
	f.worker.RunOnce(context.Background())
	bob.do("GET", "/api/v1/recordings/"+rec.ID, nil, nil, &rec)
	if rec.Status != recording.StatusStored || rec.Audio == nil || rec.Audio.ContentType != "audio/flac" {
		t.Fatalf("archived: %+v", rec)
	}
	if res := bob.do("DELETE", "/api/v1/recordings/"+rec.ID, nil, nil, nil); res.StatusCode != 204 {
		t.Fatalf("delete own: %d", res.StatusCode)
	}
}
