package http

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/audio/audiotest"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/config"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/repository/memory"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/service"
	memstore "github.com/michaelkleinhenz/knowpod-service/backend/internal/storage/memory"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/worker"
)

const adminToken = "test-admin-token"

type apiFixture struct {
	t      *testing.T
	srv    *httptest.Server
	worker *worker.Worker
}

func newAPIFixture(t *testing.T) *apiFixture {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	recs := memory.NewRecordings()
	objects := memstore.New()
	spool, err := service.NewSpool(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	archiver := service.NewArchiver(spool, objects, false, log)
	w := worker.New(recs, []worker.Stage{{
		Name: "archive", From: recording.StatusReceived, To: recording.StatusStored, Run: archiver.Run, Cleanup: archiver.Cleanup,
	}}, worker.Options{}, log)
	s := NewServer(Deps{
		Cfg: config.Config{AdminToken: adminToken}, Log: log,
		Devices: service.NewDeviceService(memory.NewDevices()), Uploads: service.NewUploadService(recs, spool, 1<<30),
		Recordings: recs, Objects: objects,
	})
	srv := httptest.NewServer(s.Router())
	t.Cleanup(srv.Close)
	return &apiFixture{t: t, srv: srv, worker: w}
}

// do sends a request and decodes a JSON response into out (when non-nil).
func (f *apiFixture) do(method, path, token string, headers map[string]string, body io.Reader, out any) *http.Response {
	f.t.Helper()
	req, _ := http.NewRequest(method, f.srv.URL+path, body)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		f.t.Fatal(err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	if out != nil {
		if b, ok := out.(*[]byte); ok {
			*b = data
		} else if err := json.Unmarshal(data, out); err != nil {
			f.t.Fatalf("%s %s: decoding %q: %v", method, path, data, err)
		}
	}
	return res
}

func jsonBody(v any) io.Reader {
	b, _ := json.Marshal(v)
	return bytes.NewReader(b)
}

func TestUploadProtocolEndToEnd(t *testing.T) {
	f := newAPIFixture(t)

	// Provision a device.
	var reg registerDeviceResponse
	if res := f.do("POST", "/api/v1/admin/devices", adminToken, nil, jsonBody(map[string]string{"name": "rec-1"}), &reg); res.StatusCode != 201 || reg.Token == "" {
		t.Fatalf("register: %d %+v", res.StatusCode, reg)
	}
	token := reg.Token

	wav := audiotest.WAV(16000, 16, audiotest.Samples(1, 16000, 16))
	sum := sha256.Sum256(wav)
	create := createUploadRequest{RecordingID: "2026-09-26T10-00-00", Size: int64(len(wav)), SHA256: hex.EncodeToString(sum[:])}

	var up uploadResponse
	if res := f.do("POST", "/api/v1/uploads", token, nil, jsonBody(create), &up); res.StatusCode != 201 || up.Offset != 0 {
		t.Fatalf("create: %d %+v", res.StatusCode, up)
	}
	// Retrying the create returns the same upload.
	var again uploadResponse
	if res := f.do("POST", "/api/v1/uploads", token, nil, jsonBody(create), &again); res.StatusCode != 200 || again.UploadID != up.UploadID {
		t.Fatalf("create retry: %d %+v", res.StatusCode, again)
	}

	path := "/api/v1/uploads/" + up.UploadID
	half := len(wav) / 2
	patch := func(off int, chunk []byte, out any) *http.Response {
		return f.do("PATCH", path, token, map[string]string{uploadOffsetHeader: strconv.Itoa(off), "Content-Type": "application/offset+octet-stream"}, bytes.NewReader(chunk), out)
	}

	if res := patch(0, wav[:half], &up); res.StatusCode != 200 || up.Offset != int64(half) || res.Header.Get(uploadOffsetHeader) != strconv.Itoa(half) {
		t.Fatalf("first chunk: %d %+v", res.StatusCode, up)
	}
	// A chunk at the wrong offset is rejected with the offset to resume from.
	var mismatch errResponse
	if res := patch(0, wav[:10], &mismatch); res.StatusCode != 409 || mismatch.Offset == nil || *mismatch.Offset != int64(half) {
		t.Fatalf("offset mismatch: %d %+v", res.StatusCode, mismatch)
	}
	// A chunk overrunning the declared size is rejected.
	if res := patch(half, append(append([]byte{}, wav[half:]...), 0), nil); res.StatusCode != 413 {
		t.Fatalf("overrun: %d", res.StatusCode)
	}
	if res := f.do("GET", path, token, nil, nil, &up); res.StatusCode != 200 || up.Offset != int64(half) {
		t.Fatalf("status: %d %+v", res.StatusCode, up)
	}
	if res := patch(half, wav[half:], &up); res.StatusCode != 200 || up.Status != "received" || up.Offset != int64(len(wav)) {
		t.Fatalf("last chunk: %d %+v", res.StatusCode, up)
	}

	// Audio is not downloadable before archiving; afterwards it is FLAC.
	if res := f.do("GET", "/api/v1/admin/recordings/"+up.UploadID+"/audio", adminToken, nil, nil, nil); res.StatusCode != 409 {
		t.Fatalf("audio before archive: %d", res.StatusCode)
	}
	if n := f.worker.RunOnce(context.Background()); n != 1 {
		t.Fatalf("worker processed %d", n)
	}
	var rec recording.Recording
	if res := f.do("GET", "/api/v1/admin/recordings/"+up.UploadID, adminToken, nil, nil, &rec); res.StatusCode != 200 || rec.Status != recording.StatusStored || rec.Format.DurationMs != 1000 {
		t.Fatalf("recording: %d %+v", res.StatusCode, rec)
	}
	var flacData []byte
	res := f.do("GET", "/api/v1/admin/recordings/"+up.UploadID+"/audio", adminToken, nil, nil, &flacData)
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "audio/flac" || string(flacData[:4]) != "fLaC" {
		t.Fatalf("audio: %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
	var list []recording.Recording
	if res := f.do("GET", "/api/v1/admin/recordings?status=stored", adminToken, nil, nil, &list); res.StatusCode != 200 || len(list) != 1 {
		t.Fatalf("list: %d %d", res.StatusCode, len(list))
	}

	// Revoked devices are locked out.
	if res := f.do("DELETE", "/api/v1/admin/devices/"+reg.Device.ID, adminToken, nil, nil, nil); res.StatusCode != 204 {
		t.Fatalf("revoke: %d", res.StatusCode)
	}
	if res := f.do("GET", path, token, nil, nil, nil); res.StatusCode != 401 {
		t.Fatalf("revoked device: %d", res.StatusCode)
	}
}

func TestAuthRequired(t *testing.T) {
	f := newAPIFixture(t)
	for _, tc := range []struct {
		method, path, token string
		want                int
	}{
		{"POST", "/api/v1/uploads", "", 401},
		{"POST", "/api/v1/uploads", "kpd_bogus", 401},
		{"GET", "/api/v1/admin/devices", "", 401},
		{"GET", "/api/v1/admin/devices", "wrong", 401},
		{"GET", "/api/v1/admin/devices", adminToken, 200},
	} {
		if res := f.do(tc.method, tc.path, tc.token, nil, jsonBody(map[string]any{}), nil); res.StatusCode != tc.want {
			t.Errorf("%s %s with %q = %d, want %d", tc.method, tc.path, tc.token, res.StatusCode, tc.want)
		}
	}
}

func TestAdminRequiresSessionOrToken(t *testing.T) {
	s := NewServer(Deps{})
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/admin/devices", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rec.Code)
	}
}
