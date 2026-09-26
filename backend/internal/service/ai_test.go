package service

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/audio"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/audio/audiotest"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/openrouter"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/repository/memory"
	memstore "github.com/michaelkleinhenz/knowpod-service/backend/internal/storage/memory"
)

// fakeAI records requests and answers with a fixed text.
type fakeAI struct {
	answer   string
	err      error
	requests []openrouter.Request
	models   []openrouter.Model
}

func (f *fakeAI) Complete(_ context.Context, apiKey string, r openrouter.Request) (string, error) {
	if apiKey != "sk-test" {
		return "", errors.New("wrong key")
	}
	f.requests = append(f.requests, r)
	return f.answer, f.err
}

func (f *fakeAI) Models(context.Context) ([]openrouter.Model, error) { return f.models, nil }

func newAI(t *testing.T, ai *fakeAI) (*AIService, *memstore.Store) {
	t.Helper()
	objects := memstore.New()
	s := NewAIService(memory.NewSettings(), objects, ai, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	key, model := "sk-test", "google/gemini-2.5-flash"
	if _, err := s.UpdateSettings(context.Background(), OpenRouterUpdate{APIKey: &key, TranscriptionModel: &model, SummaryModel: &model}); err != nil {
		t.Fatal(err)
	}
	return s, objects
}

// storedFLAC archives a FLAC recording of the given length in the object store.
func storedFLAC(t *testing.T, objects *memstore.Store, seconds int) *recording.Recording {
	t.Helper()
	dir := t.TempDir()
	wavPath, flacPath := filepath.Join(dir, "a.wav"), filepath.Join(dir, "a.flac")
	_ = os.WriteFile(wavPath, audiotest.WAV(48000, 16, audiotest.Samples(2, 48000*seconds, 16)), 0o600)
	if _, err := audio.EncodeFLAC(context.Background(), wavPath, flacPath); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(flacPath)
	key := "recordings/dev/r1.flac"
	_ = objects.Put(context.Background(), key, bytes.NewReader(data), int64(len(data)), "audio/flac")
	return &recording.Recording{ID: "r1", Audio: &recording.Object{Key: key, ContentType: "audio/flac", Size: int64(len(data))}}
}

func TestTranscribeFLAC(t *testing.T) {
	ai := &fakeAI{answer: "  Speaker 1: Hello.  "}
	s, objects := newAI(t, ai)
	rec := storedFLAC(t, objects, 3)

	if err := s.Transcribe(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
	if rec.Transcript == nil || rec.Transcript.Text != "Speaker 1: Hello." || rec.Transcript.Model != "google/gemini-2.5-flash" {
		t.Fatalf("transcript = %+v", rec.Transcript)
	}
	if len(ai.requests) != 1 {
		t.Fatalf("requests = %d", len(ai.requests))
	}
	// The audio is sent as mono 16 kHz WAV.
	part := ai.requests[0].Messages[0].Content.([]any)[1].(openrouter.AudioPart)
	wav, _ := base64.StdEncoding.DecodeString(part.InputAudio.Data)
	info, err := audio.ReadWAVInfo(bytes.NewReader(wav), int64(len(wav)))
	if err != nil || part.InputAudio.Format != "wav" || info.SampleRate != 16000 || info.Channels != 1 {
		t.Fatalf("sent audio: format=%s info=%+v err=%v", part.InputAudio.Format, info, err)
	}
}

func TestTranscribePassthrough(t *testing.T) {
	ai := &fakeAI{answer: "Hi."}
	s, objects := newAI(t, ai)
	mp3 := []byte("ID3\x04\x00 fake mp3")
	_ = objects.Put(context.Background(), "recordings/pocket/r2.mp3", bytes.NewReader(mp3), int64(len(mp3)), "audio/mpeg")
	rec := &recording.Recording{ID: "r2", Audio: &recording.Object{Key: "recordings/pocket/r2.mp3", ContentType: "audio/mpeg", Size: int64(len(mp3))}}

	if err := s.Transcribe(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
	part := ai.requests[0].Messages[0].Content.([]any)[1].(openrouter.AudioPart)
	if part.InputAudio.Format != "mp3" || part.InputAudio.Data != base64.StdEncoding.EncodeToString(mp3) {
		t.Fatalf("sent %+v", part.InputAudio)
	}

	rec.Audio.Size = maxPassthroughAudio + 1
	if err := s.Transcribe(context.Background(), rec); err == nil || !strings.Contains(err.Error(), "too large") {
		t.Fatalf("oversized: %v", err)
	}
}

func TestSummarize(t *testing.T) {
	ai := &fakeAI{answer: "```json\n{\"title\": \"Weekly Standup.\", \"summary\": \"The team met.\\n\\n## Key points\\n- Ship v2\"}\n```"}
	s, _ := newAI(t, ai)
	rec := &recording.Recording{ID: "r1", Transcript: &recording.Transcript{Text: "Speaker 1: let's ship v2"}}
	if err := s.Summarize(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
	if rec.Summary.Title != "Weekly Standup" || !strings.Contains(rec.Summary.Markdown, "## Key points") || !ai.requests[0].JSON {
		t.Fatalf("summary = %+v", rec.Summary)
	}

	// Empty transcripts don't call the model.
	empty := &recording.Recording{ID: "r2", Transcript: &recording.Transcript{Text: " "}}
	if err := s.Summarize(context.Background(), empty); err != nil || empty.Summary.Title != "No speech detected" || len(ai.requests) != 1 {
		t.Fatalf("empty: %+v, %v", empty.Summary, err)
	}
}

func TestParseSummary(t *testing.T) {
	for in, want := range map[string]string{
		`{"title":"A","summary":"B"}`:                             "A",
		"Sure! {\"title\":\"A\",\"summary\":\"B\"} Hope it helps": "A",
		"# Plain title\nBody text":                                "Plain title",
		`{"title":"","summary":"only body"}`:                      "Untitled conversation",
	} {
		if got, _ := parseSummary(in); got != want {
			t.Errorf("parseSummary(%q) title = %q, want %q", in, got, want)
		}
	}
}

func TestSettings(t *testing.T) {
	ctx := context.Background()
	ai := &fakeAI{models: []openrouter.Model{
		{ID: "google/gemini-2.5-flash", Name: "Gemini Flash", InputModalities: []string{"text", "audio"}, OutputModalities: []string{"text"}},
		{ID: "google/gemini-2.5-flash:batch", Name: "Gemini Flash (batch)", InputModalities: []string{"text", "audio"}, OutputModalities: []string{"text"}},
		{ID: "anthropic/claude", Name: "Claude", InputModalities: []string{"text"}, OutputModalities: []string{"text"}},
		{ID: "openrouter/auto", Name: "Auto", InputModalities: []string{"text", "audio"}, OutputModalities: []string{"text"}},
		{ID: "x/image-gen", Name: "Image", InputModalities: []string{"text"}, OutputModalities: []string{"image"}},
	}}
	s, _ := newAI(t, ai)

	v, _ := s.Settings(ctx)
	if !v.APIKeyConfigured || v.APIKeyHint != "" || !s.CanTranscribe(ctx) {
		t.Fatalf("short key must not produce a hint: %+v", v)
	}
	long := "sk-or-v1-abcdefgh1234"
	v, _ = s.UpdateSettings(ctx, OpenRouterUpdate{APIKey: &long})
	if v.APIKeyHint != "…1234" || v.TranscriptionModel != "google/gemini-2.5-flash" {
		t.Fatalf("after key change: %+v", v)
	}
	bad := "not a model"
	if _, err := s.UpdateSettings(ctx, OpenRouterUpdate{SummaryModel: &bad}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("invalid model: %v", err)
	}
	empty := ""
	if v, _ = s.UpdateSettings(ctx, OpenRouterUpdate{APIKey: &empty}); v.APIKeyConfigured || s.CanSummarize(ctx) {
		t.Fatalf("key not removed: %+v", v)
	}

	opts, err := s.Models(ctx)
	if err != nil || len(opts.Transcription) != 1 || opts.Transcription[0].ID != "google/gemini-2.5-flash" || len(opts.Summary) != 2 {
		t.Fatalf("models = %+v, %v", opts, err)
	}
}

func TestRecordingActions(t *testing.T) {
	ctx := context.Background()
	recs := memory.NewRecordings()
	objects := memstore.New()
	spool, _ := NewSpool(t.TempDir())
	s := NewRecordingService(recs, objects, spool)
	requeued := 0
	s.OnRequeued = func() { requeued++ }

	_ = objects.Put(ctx, "k.flac", strings.NewReader("x"), 1, "audio/flac")
	rec := &recording.Recording{ID: "r1", DeviceID: "d", ClientID: "c", Status: recording.StatusSummarized,
		Audio:      &recording.Object{Key: "k.flac"},
		Transcript: &recording.Transcript{Text: "t"}, Summary: &recording.Summary{Title: "s"}, Attempts: 3, LastError: "old"}
	_ = recs.Create(ctx, rec)

	got, err := s.Resummarize(ctx, "r1")
	if err != nil || got.Status != recording.StatusTranscribed || got.Summary != nil || got.Transcript == nil || got.Attempts != 0 || got.LastError != "" {
		t.Fatalf("resummarize: %+v, %v", got, err)
	}
	got, err = s.Retranscribe(ctx, "r1")
	if err != nil || got.Status != recording.StatusStored || got.Transcript != nil || requeued != 2 {
		t.Fatalf("retranscribe: %+v, %v", got, err)
	}
	if _, err := s.Resummarize(ctx, "r1"); !errors.Is(err, ErrNotReady) {
		t.Fatalf("resummarize without transcript: %v", err)
	}

	if err := s.Delete(ctx, "r1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := objects.Object("k.flac"); ok {
		t.Fatal("audio object not deleted")
	}
	if _, err := recs.Get(ctx, "r1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("recording not deleted: %v", err)
	}
}
