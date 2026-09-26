package http

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/config"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/repository/memory"
	memstore "github.com/michaelkleinhenz/knowpod-service/backend/internal/storage/memory"
)

func TestParseRange(t *testing.T) {
	for header, want := range map[string]struct {
		off, n      int64
		partial, ok bool
	}{
		"":              {0, 100, false, true},
		"bytes=0-":      {0, 100, true, true},
		"bytes=10-19":   {10, 10, true, true},
		"bytes=90-500":  {90, 10, true, true},
		"bytes=-30":     {70, 30, true, true},
		"bytes=100-":    {0, 0, false, false},
		"bytes=0-1,5-9": {0, 100, false, true},
		"items=0-10":    {0, 100, false, true},
	} {
		off, n, partial, ok := parseRange(header, 100)
		if off != want.off || n != want.n || partial != want.partial || ok != want.ok {
			t.Errorf("parseRange(%q) = %d %d %v %v, want %+v", header, off, n, partial, ok, want)
		}
	}
}

func TestAudioRangeAndDownload(t *testing.T) {
	ctx := context.Background()
	recs := memory.NewRecordings()
	objects := memstore.New()
	data := []byte("0123456789abcdefghij")
	_ = objects.Put(ctx, "recordings/d/r1.flac", bytes.NewReader(data), int64(len(data)), "audio/flac")
	_ = recs.Create(ctx, &recording.Recording{ID: "r1", DeviceID: "d", ClientID: "c",
		Audio: &recording.Object{Key: "recordings/d/r1.flac", ContentType: "audio/flac", Size: int64(len(data))}})
	h := NewServer(Deps{Cfg: config.Config{AdminToken: adminToken}, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Recordings: recs, Objects: objects}).Router()

	get := func(path, rng string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+adminToken)
		if rng != "" {
			req.Header.Set("Range", rng)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	res := get("/api/v1/admin/recordings/r1/audio", "bytes=5-9")
	if res.Code != 206 || res.Body.String() != "56789" || res.Header().Get("Content-Range") != "bytes 5-9/20" {
		t.Fatalf("range: %d %q %q", res.Code, res.Body, res.Header().Get("Content-Range"))
	}
	res = get("/api/v1/admin/recordings/r1/audio", "")
	if res.Code != 200 || res.Body.String() != string(data) || res.Header().Get("Accept-Ranges") != "bytes" ||
		res.Header().Get("Content-Disposition") != `inline; filename="r1.flac"` {
		t.Fatalf("full: %d %v", res.Code, res.Header())
	}
	if res = get("/api/v1/admin/recordings/r1/audio?download=1", ""); res.Header().Get("Content-Disposition") != `attachment; filename="r1.flac"` {
		t.Fatalf("download disposition = %q", res.Header().Get("Content-Disposition"))
	}
	if res = get("/api/v1/admin/recordings/r1/audio", "bytes=50-"); res.Code != 416 {
		t.Fatalf("unsatisfiable range: %d", res.Code)
	}
}
