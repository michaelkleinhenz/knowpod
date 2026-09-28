package http

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/audio/audiotest"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/service"
)

func TestUploadPhotoAndVoiceMemo(t *testing.T) {
	f := newAPIFixture(t)
	admin := f.signedIn(adminEmail, adminPassword)

	jpeg := append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, make([]byte, 200)...)
	var photo recording.Recording
	if res := admin.do("POST", "/api/v1/recordings", jpeg, map[string]string{"Content-Type": "image/jpeg", "X-Filename": "board.jpg"}, &photo); res.StatusCode != 201 ||
		photo.Type != recording.TypeDocument || photo.Source != recording.SourceUpload {
		t.Fatalf("photo: %d %+v", res.StatusCode, photo)
	}
	f.worker.RunOnce(context.Background())
	admin.do("GET", "/api/v1/recordings/"+photo.ID, nil, nil, &photo)
	if photo.Status != recording.StatusStored || photo.File == nil || photo.File.ContentType != "image/jpeg" {
		t.Fatalf("archived photo: %+v", photo)
	}
	var body []byte
	if res := admin.do("GET", "/api/v1/recordings/"+photo.ID+"/file", nil, nil, &body); res.StatusCode != 200 || len(body) != len(jpeg) {
		t.Fatalf("photo file: %d, %d bytes", res.StatusCode, len(body))
	}

	wav := audiotest.WAV(16000, 16, audiotest.Samples(1, 8000, 16))
	var memo recording.Recording
	res := admin.do("POST", "/api/v1/recordings", wav, map[string]string{
		"Content-Type": "audio/wav", "X-Filename": url.QueryEscape("Voice memo.wav"), "X-Recorder": "1", "X-Highlights": "250, 100",
	}, &memo)
	if res.StatusCode != 201 || memo.Source != recording.SourceRecorder || len(memo.Highlights) != 2 || memo.Highlights[0].OffsetMs != 100 {
		t.Fatalf("memo: %d %+v", res.StatusCode, memo)
	}
	if res := admin.do("POST", "/api/v1/recordings", wav, map[string]string{"X-Highlights": "soon"}, nil); res.StatusCode != 400 {
		t.Fatalf("bad highlights: %d", res.StatusCode)
	}
}

func TestRenameSpeakerAPI(t *testing.T) {
	f := newAPIFixture(t)
	admin := f.signedIn(adminEmail, adminPassword)
	var me service.Account
	admin.do("GET", "/api/v1/auth/me", nil, nil, &me)
	rec := &recording.Recording{ID: "r1", OwnerID: me.ID, DeviceID: "d", ClientID: "r1", Status: recording.StatusSummarized,
		Transcript: &recording.Transcript{Text: "[0:01] Speaker 1: Hi."}, Summary: &recording.Summary{Title: "T", Markdown: "Speaker 1 says hi."}}
	if err := f.recs.Create(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
	var got recording.Recording
	if res := admin.do("POST", "/api/v1/recordings/r1/speakers/rename", map[string]string{"from": "Speaker 1", "to": "Anna"}, nil, &got); res.StatusCode != 200 ||
		got.Transcript.Text != "[0:01] Anna: Hi." || got.Summary.Markdown != "Anna says hi." {
		t.Fatalf("rename: %d %+v", res.StatusCode, got)
	}
	if res := admin.do("POST", "/api/v1/recordings/r1/speakers/rename", map[string]string{"from": "Speaker 1", "to": "Ben"}, nil, nil); res.StatusCode != 400 {
		t.Fatalf("unknown speaker: %d", res.StatusCode)
	}
}

func TestBriefingAPI(t *testing.T) {
	f := newAPIFixture(t)
	admin := f.signedIn(adminEmail, adminPassword)
	var settings service.BriefingSettings
	if res := admin.do("GET", "/api/v1/me/briefing", nil, nil, &settings); res.StatusCode != 200 || settings.Time != "07:00" || settings.Daily {
		t.Fatalf("settings: %d %+v", res.StatusCode, settings)
	}
	if res := admin.do("PUT", "/api/v1/me/briefing", service.BriefingSettings{Daily: true, Time: "06:30", WeeklyDay: 5}, nil, &settings); res.StatusCode != 200 ||
		!settings.Daily || settings.Time != "06:30" || settings.WeeklyDay != 5 {
		t.Fatalf("update: %d %+v", res.StatusCode, settings)
	}
	if res := admin.do("PUT", "/api/v1/me/briefing", map[string]any{"time": "later"}, nil, nil); res.StatusCode != 400 {
		t.Fatalf("bad time: %d", res.StatusCode)
	}
	var note recording.Recording
	if res := admin.do("POST", "/api/v1/me/briefing/run", map[string]string{"kind": "weekly"}, nil, &note); res.StatusCode != 201 ||
		note.Source != recording.SourceBriefing || !strings.HasPrefix(note.Summary.Title, "Weekly review, ") || note.FolderID == "" {
		t.Fatalf("run: %d %+v", res.StatusCode, note)
	}
}

func TestAskNeedsTheAIModels(t *testing.T) {
	f := newAPIFixture(t)
	admin := f.signedIn(adminEmail, adminPassword)
	if res := admin.do("POST", "/api/v1/ask", map[string]string{"question": "What's new?"}, nil, nil); res.StatusCode != 409 {
		t.Fatalf("ask without AI: %d", res.StatusCode)
	}
	if res := admin.do("POST", "/api/v1/ask", map[string]string{"question": ""}, nil, nil); res.StatusCode != 400 {
		t.Fatalf("empty question: %d", res.StatusCode)
	}
}

func TestSharePostWithoutTheServiceWorker(t *testing.T) {
	f := newAPIFixture(t)
	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := c.Post(f.srv.URL+"/share", "multipart/form-data; boundary=x", strings.NewReader("--x--"))
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusSeeOther || res.Header.Get("Location") != "/share?missed=1" {
		t.Fatalf("share post: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	// The share page is the app's.
	page, err := http.Get(f.srv.URL + "/share?missed=1")
	if err != nil {
		t.Fatal(err)
	}
	page.Body.Close()
	if page.StatusCode != 200 || !strings.HasPrefix(page.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("share page: %d %s", page.StatusCode, page.Header.Get("Content-Type"))
	}
}
