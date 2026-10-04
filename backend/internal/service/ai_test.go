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
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/user"
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
	s := NewAIService(memory.NewSettings(), NewThemeService(memory.NewThemes()), objects, ai, t.TempDir(), slog.New(slog.NewTextHandler(io.Discard, nil)))
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
	// ... and say so in the summary's language.
	german := &recording.Recording{ID: "r3", Transcript: &recording.Transcript{Text: ""},
		SummaryOptions: recording.SummaryOptions{Language: "de-DE"}}
	if err := s.Summarize(context.Background(), german); err != nil || german.Summary.Title != "Keine Sprache erkannt" {
		t.Fatalf("german empty: %+v, %v", german.Summary, err)
	}
}

func TestSummarySystemPromptLanguage(t *testing.T) {
	auto := summarySystemPrompt("Use \"## Key points\".", "auto", nil)
	if !strings.Contains(auto, "language of the transcript") || !strings.Contains(auto, "translate the headings") {
		t.Fatalf("auto prompt = %q", auto)
	}
	german := summarySystemPrompt("Use \"## Key points\".", "de-DE", nil)
	if !strings.Contains(german, "in German, regardless") || !strings.Contains(german, "headings given above into German") {
		t.Fatalf("German prompt = %q", german)
	}
}

func TestParseSummary(t *testing.T) {
	for in, want := range map[string]string{
		`{"title":"A","summary":"B"}`:                             "A",
		"Sure! {\"title\":\"A\",\"summary\":\"B\"} Hope it helps": "A",
		"# Plain title\nBody text":                                "Plain title",
		`{"title":"","summary":"only body"}`:                      "Untitled conversation",
	} {
		if got, _, _ := parseSummary(in); got != want {
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
	s := NewRecordingService(recs, objects, spool, NewThemeService(memory.NewThemes()))
	owner, stranger := &Account{ID: "u1"}, &Account{ID: "u2"}
	requeued := 0
	s.OnRequeued = func() { requeued++ }

	_ = objects.Put(ctx, "k.flac", strings.NewReader("x"), 1, "audio/flac")
	rec := &recording.Recording{ID: "r1", OwnerID: "u1", DeviceID: "d", ClientID: "c", Status: recording.StatusSummarized,
		Audio:      &recording.Object{Key: "k.flac"},
		Transcript: &recording.Transcript{Text: "t"}, Summary: &recording.Summary{Title: "s"}, Attempts: 3, LastError: "old"}
	_ = recs.Create(ctx, rec)

	if _, err := s.Resummarize(ctx, stranger, "r1", nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other user's recording: %v", err)
	}
	if err := s.Delete(ctx, stranger, "r1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other user's delete: %v", err)
	}
	got, err := s.Resummarize(ctx, owner, "r1", nil)
	if err != nil || got.Status != recording.StatusTranscribed || got.Summary != nil || got.Transcript == nil || got.Attempts != 0 || got.LastError != "" {
		t.Fatalf("resummarize: %+v, %v", got, err)
	}
	got, err = s.Retranscribe(ctx, owner, "r1")
	if err != nil || got.Status != recording.StatusStored || got.Transcript != nil || requeued != 2 {
		t.Fatalf("retranscribe: %+v, %v", got, err)
	}
	if _, err := s.Resummarize(ctx, owner, "r1", nil); !errors.Is(err, ErrNotReady) {
		t.Fatalf("resummarize without transcript: %v", err)
	}

	if err := s.Delete(ctx, owner, "r1"); err != nil {
		t.Fatal(err)
	}
	if _, ok := objects.Object("k.flac"); ok {
		t.Fatal("audio object not deleted")
	}
	if _, err := recs.Get(ctx, "r1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("recording not deleted: %v", err)
	}
}

func TestInLanguage(t *testing.T) {
	for _, p := range []string{transcriptionPrompt, notebookPrompt, pdfPrompt} {
		if inLanguage(p, "") != p {
			t.Fatal("no language changed the prompt")
		}
		got := inLanguage(p, "German")
		if strings.Contains(got, "original language") || strings.Contains(got, "Do not translate") ||
			!strings.Contains(got, "in German, translating anything in another language into German. Do not summarize") {
			t.Fatalf("prompt = %q", got)
		}
	}
}

func TestOwnerLanguage(t *testing.T) {
	ctx := context.Background()
	ai := &fakeAI{answer: `{"title": "Standup", "summary": "Done."}`}
	s, objects := newAI(t, ai)
	users := memory.NewUsers()
	_ = users.Create(ctx, &user.User{ID: "u1", Email: "a@example.com", Role: user.RoleUser, Language: "de"})
	_ = users.Create(ctx, &user.User{ID: "u2", Email: "b@example.com", Role: user.RoleUser})
	s.Users = users
	mp3 := []byte("ID3\x04\x00 fake mp3")
	_ = objects.Put(ctx, "recordings/pocket/r1.mp3", bytes.NewReader(mp3), int64(len(mp3)), "audio/mpeg")
	newRec := func(owner string) *recording.Recording {
		return &recording.Recording{ID: "r1", OwnerID: owner, Transcript: &recording.Transcript{Text: "Speaker 1: hello"},
			Audio: &recording.Object{Key: "recordings/pocket/r1.mp3", ContentType: "audio/mpeg", Size: int64(len(mp3))}}
	}
	lastPrompt := func() string {
		r := ai.requests[len(ai.requests)-1]
		if c, ok := r.Messages[0].Content.(string); ok {
			return c
		}
		return r.Messages[0].Content.([]any)[0].(openrouter.TextPart).Text
	}

	// The owner chose German: the transcript and the summary are written in German.
	rec := newRec("u1")
	if err := s.Transcribe(ctx, rec); err != nil || !strings.Contains(lastPrompt(), "in German, translating") {
		t.Fatalf("transcription prompt = %q, %v", lastPrompt(), err)
	}
	rec.Transcript.Text = "Speaker 1: hello"
	if err := s.Summarize(ctx, rec); err != nil || rec.Summary.Language != "de-DE" || !strings.Contains(lastPrompt(), "in German, regardless") {
		t.Fatalf("summary language %q, prompt %q, %v", rec.Summary.Language, lastPrompt(), err)
	}

	// A language chosen for the recording still wins.
	rec.SummaryOptions.Language = "fr-FR"
	if err := s.Summarize(ctx, rec); err != nil || rec.Summary.Language != "fr-FR" || !strings.Contains(lastPrompt(), "in French") {
		t.Fatalf("summary language %q, %v", rec.Summary.Language, err)
	}

	// Without a language setting, the recording's own language is kept.
	rec = newRec("u2")
	if err := s.Transcribe(ctx, rec); err != nil || !strings.Contains(lastPrompt(), "in its original language") {
		t.Fatalf("transcription prompt = %q, %v", lastPrompt(), err)
	}
	rec.Transcript.Text = "Speaker 1: hello"
	if err := s.Summarize(ctx, rec); err != nil || rec.Summary.Language != "auto" || !strings.Contains(lastPrompt(), "language of the transcript") {
		t.Fatalf("summary language %q, %v", rec.Summary.Language, err)
	}
}

func TestArchiveAndReadAnUploadedPhoto(t *testing.T) {
	ctx := context.Background()
	ai := &fakeAI{answer: "```markdown\n# Plan\n- [ ] Ship it\n```"}
	s, objects := newAI(t, ai)
	spool, _ := NewSpool(t.TempDir())
	arch := NewArchiver(spool, objects, false, slog.New(slog.NewTextHandler(io.Discard, nil)))

	jpeg := append([]byte{0xFF, 0xD8, 0xFF}, []byte("photo bytes")...)
	rec := &recording.Recording{ID: "p1", OwnerID: "alice", Type: recording.TypeDocument, Source: recording.SourceUpload, SourceContentType: "image/jpeg"}
	if err := os.WriteFile(spool.DownloadPath("p1"), jpeg, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := arch.Run(ctx, rec); err != nil {
		t.Fatal(err)
	}
	if rec.File == nil || rec.File.Key != "recordings/alice/p1.jpg" || rec.File.ContentType != "image/jpeg" || rec.Pages != 1 || rec.Audio != nil || !rec.IsImage() {
		t.Fatalf("archived = %+v, file %+v", rec, rec.File)
	}

	if err := s.Transcribe(ctx, rec); err != nil {
		t.Fatal(err)
	}
	if rec.Transcript == nil || rec.Transcript.Text != "# Plan\n- [ ] Ship it" {
		t.Fatalf("transcript = %+v", rec.Transcript)
	}
	parts := ai.requests[0].Messages[0].Content.([]any)
	if prompt := parts[0].(openrouter.TextPart).Text; !strings.Contains(prompt, "This is a photo") {
		t.Errorf("prompt = %q", prompt)
	}
	if img := parts[1].(openrouter.ImagePart); !strings.HasPrefix(img.ImageURL.URL, "data:image/jpeg;base64,") {
		t.Errorf("image part = %+v", img)
	}
}

func TestSummaryKeepsTheSpeakerNames(t *testing.T) {
	ai := &fakeAI{answer: `{"title":"Intro","summary":"Hi.","actionItems":[],"speakers":[{"label":"Speaker 2","name":"Ben"}]}`}
	s, _ := newAI(t, ai)
	rec := &recording.Recording{ID: "r1", Transcript: &recording.Transcript{Text: "[0:01] Speaker 1: Hi Ben.\n[0:02] Speaker 2: Hi."}}
	if err := s.Summarize(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
	if len(rec.Summary.Speakers) != 1 || rec.Summary.Speakers[0].Name != "Ben" {
		t.Fatalf("speakers = %+v", rec.Summary.Speakers)
	}
	if prompt := ai.requests[0].Messages[0].Content.(string); !strings.Contains(prompt, `"speakers"`) {
		t.Error("the prompt doesn't ask for the speakers")
	}
}

func TestContinuationPrompt(t *testing.T) {
	// Without speakers so far, only the part is named.
	if got := continuationPrompt(2, []string{"[0:01] just words"}); strings.Contains(got, "<previous>") || !strings.Contains(got, "part 2") {
		t.Fatalf("no speakers: %q", got)
	}

	prev := []string{"[0:01] Speaker 1: Hi, I'm Anna.\n[0:04] Speaker 2: Ben here.", "[5:02] Speaker 3: Late, sorry.\n[5:10] Speaker 1: No problem."}
	got := continuationPrompt(3, prev)
	for _, want := range []string{"part 3", "labeled Speaker 1, Speaker 2, Speaker 3", "from Speaker 4 on", "<previous>\n[0:01] Speaker 1: Hi, I'm Anna.", "[5:10] Speaker 1: No problem.\n</previous>"} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt lacks %q: %q", want, got)
		}
	}

	// A long transcript is cut to its end, at a whole line.
	long := strings.Repeat("[0:01] Speaker 1: "+strings.Repeat("blah ", 20)+"\n", 100) + "[4:59] Speaker 2: The end."
	got = continuationPrompt(2, []string{long})
	tail := got[strings.Index(got, "<previous>\n")+len("<previous>\n"):]
	if len(tail) > continuationContext+len("\n</previous>") || !strings.HasPrefix(tail, "[0:01] Speaker 1:") || !strings.Contains(tail, "Speaker 2: The end.") {
		t.Fatalf("tail = %q", tail)
	}
}

func TestTranscriptionPromptLabelsSpeakers(t *testing.T) {
	for _, want := range []string{"Tell the speakers apart", "every time the speaker changes", "Label every line"} {
		if !strings.Contains(transcriptionPrompt, want) {
			t.Fatalf("prompt lacks %q", want)
		}
	}
}
