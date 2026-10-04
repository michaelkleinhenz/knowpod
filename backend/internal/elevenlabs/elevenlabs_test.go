package elevenlabs

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTranscribe(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/speech-to-text" || r.Header.Get("xi-api-key") != "el-key" {
			t.Errorf("request %s %s key=%q", r.Method, r.URL.Path, r.Header.Get("xi-api-key"))
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		for k, want := range map[string]string{"model_id": Model, "diarize": "true", "timestamps_granularity": "word"} {
			if got := r.FormValue(k); got != want {
				t.Errorf("%s = %q, want %q", k, got, want)
			}
		}
		f, h, err := r.FormFile("file")
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(f)
		if h.Filename != "r1.flac" || string(data) != "audio" {
			t.Errorf("file %s = %q", h.Filename, data)
		}
		_, _ = io.WriteString(w, `{"text":"Hi there.","language_code":"eng","words":[
			{"text":"Hi","start":0.1,"end":0.3,"type":"word","speaker_id":"speaker_0"},
			{"text":" ","start":0.3,"end":0.4,"type":"spacing","speaker_id":"speaker_0"},
			{"text":"there.","start":0.4,"end":0.8,"type":"word","speaker_id":"speaker_0"}]}`)
	}))
	defer srv.Close()

	got, err := NewClient(srv.URL+"/v1/").Transcribe(context.Background(), "el-key", strings.NewReader("audio"), "r1.flac")
	if err != nil {
		t.Fatal(err)
	}
	if got.Text != "Hi there." || got.LanguageCode != "eng" || len(got.Words) != 3 || got.Words[2].SpeakerID != "speaker_0" || got.Words[2].Start != 0.4 {
		t.Fatalf("transcript = %+v", got)
	}
}

func TestErrors(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
		unauth bool
		want   string
	}{
		{401, `{"detail":{"status":"invalid_api_key","message":"Invalid API key"}}`, true, ""},
		{401, `{"detail":{"status":"missing_permissions","message":"The API key is missing the permission speech_to_text"}}`, false, "speech_to_text"},
		{401, ``, true, ""},
		{422, `{"detail":[{"loc":["body","file"],"msg":"field required"}]}`, false, "field required"},
		{429, `{"detail":"Too many requests"}`, false, "Too many requests"},
		{500, `oops`, false, "HTTP 500: oops"},
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = io.WriteString(w, tc.body)
		}))
		err := NewClient(srv.URL).Check(context.Background(), "k")
		srv.Close()
		if errors.Is(err, ErrUnauthorized) != tc.unauth || err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%d %s: err = %v", tc.status, tc.body, err)
		}
	}
}

func TestSilence(t *testing.T) {
	wav := silence(1e9)
	if len(wav) != 44+32000 || string(wav[:4]) != "RIFF" || string(wav[8:12]) != "WAVE" {
		t.Fatalf("wav header %q, len %d", wav[:12], len(wav))
	}
}
