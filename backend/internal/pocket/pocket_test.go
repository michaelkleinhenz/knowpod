package pocket

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Sign computes a webhook signature as Pocket does.
func Sign(secret, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(timestamp + "."))
	mac.Write(body)
	return hex.EncodeToString(mac.Sum(nil))
}

func TestVerifySignature(t *testing.T) {
	now := time.Now()
	ts := strconv.FormatInt(now.UnixMilli(), 10)
	body := []byte(`{"event":"recording.created"}`)
	sig := Sign("s3cret", ts, body)

	if err := VerifySignature("s3cret", ts, sig, body, now); err != nil {
		t.Fatalf("valid: %v", err)
	}
	if err := VerifySignature("s3cret", ts, "sha256="+strings.ToUpper(sig), body, now); err != nil {
		t.Fatalf("prefixed/upper-case: %v", err)
	}
	stale := strconv.FormatInt(now.Add(-10*time.Minute).UnixMilli(), 10)
	for name, tc := range map[string]struct{ secret, ts, sig, body string }{
		"wrong secret":  {"other", ts, sig, string(body)},
		"tampered body": {"s3cret", ts, sig, `{"event":"recording.deleted"}`},
		"no signature":  {"s3cret", ts, "", string(body)},
		"no timestamp":  {"s3cret", "", sig, string(body)},
		"stale":         {"s3cret", stale, Sign("s3cret", stale, body), string(body)},
	} {
		if err := VerifySignature(tc.secret, tc.ts, tc.sig, []byte(tc.body), now); !errors.Is(err, ErrBadSignature) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestParseEvent(t *testing.T) {
	ev, err := ParseEvent([]byte(`{"event":"summary.completed","recording":{"id":"rec_abc123","title":"Team Standup","duration":1800,"recordingAt":"2026-02-17T15:02:00.000Z"},"transcript":[{"speaker":"Alice"}]}`))
	if err != nil || ev.Event != "summary.completed" || ev.Recording.ID != "rec_abc123" || ev.Recording.Title != "Team Standup" {
		t.Fatalf("ParseEvent = %+v, %v", ev, err)
	}
}

func TestAudioURL(t *testing.T) {
	for name, body := range map[string]string{
		"string":       `{"success":true,"data":"https://s3.example.com/a.mp3?sig=1"}`,
		"url":          `{"success":true,"data":{"url":"https://s3.example.com/a.mp3?sig=1","expires_in":3600}}`,
		"download_url": `{"success":true,"data":{"download_url":"https://s3.example.com/a.mp3?sig=1"}}`,
		"nested":       `{"success":true,"data":{"audio":{"signedUrl":"https://s3.example.com/a.mp3?sig=1"}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/public/recordings/rec_1/audio-url" || r.Header.Get("Authorization") != "Bearer pk_test" {
					http.Error(w, "bad request "+r.URL.Path, 400)
					return
				}
				_, _ = w.Write([]byte(body))
			}))
			defer srv.Close()
			got, err := NewClient(srv.URL, "pk_test").AudioURL(context.Background(), "rec_1")
			if err != nil || got != "https://s3.example.com/a.mp3?sig=1" {
				t.Fatalf("AudioURL = %q, %v", got, err)
			}
		})
	}
}

func TestAudioURLErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/public/recordings/missing/audio-url":
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"success":false,"error":"Recording not found"}`))
		default:
			_, _ = w.Write([]byte(`{"success":true,"data":{"something":"else"}}`))
		}
	}))
	defer srv.Close()
	c := NewClient(srv.URL, "pk_test")
	if _, err := c.AudioURL(context.Background(), "missing"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	if _, err := c.AudioURL(context.Background(), "odd"); err == nil || !strings.Contains(err.Error(), "something") {
		t.Fatalf("unrecognised response should be reported with its body: %v", err)
	}
	if _, err := NewClient(srv.URL, "").AudioURL(context.Background(), "x"); err == nil {
		t.Fatal("expected error without API key")
	}
}

func TestDownload(t *testing.T) {
	mp3 := append([]byte("ID3\x04\x00\x00\x00\x00\x00\x00"), make([]byte, 5000)...)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			http.Error(w, "pre-signed URLs must not get the API key", 400)
			return
		}
		if r.URL.Path == "/gone" {
			http.Error(w, "expired", 403)
			return
		}
		w.Header().Set("Content-Type", "binary/octet-stream")
		_, _ = w.Write(mp3)
	}))
	defer srv.Close()
	c := NewClient("unused", "pk_test")
	dst := filepath.Join(t.TempDir(), "a.download")

	d, err := c.Download(context.Background(), srv.URL+"/a.mp3", dst, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(mp3)
	if d.ContentType != "audio/mpeg" || d.Size != int64(len(mp3)) || d.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("download = %+v", d)
	}

	if _, err := c.Download(context.Background(), srv.URL+"/a.mp3", dst, 100); err == nil {
		t.Fatal("expected size limit error")
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Fatal("partial file not removed")
	}
	if _, err := c.Download(context.Background(), srv.URL+"/gone", dst, 1<<20); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("expected HTTP error: %v", err)
	}
}
