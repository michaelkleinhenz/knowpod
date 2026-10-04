package service

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/elevenlabs"
)

// fakeSpeech records what it transcribes and answers with a fixed transcript.
type fakeSpeech struct {
	answer   *elevenlabs.Transcript
	err      error
	audio    []byte
	filename string
	checked  []string
}

func (f *fakeSpeech) Transcribe(_ context.Context, apiKey string, audio io.Reader, filename string) (*elevenlabs.Transcript, error) {
	if apiKey != "el-test" {
		return nil, elevenlabs.ErrUnauthorized
	}
	f.audio, _ = io.ReadAll(audio)
	f.filename = filename
	return f.answer, f.err
}

func (f *fakeSpeech) Check(_ context.Context, apiKey string) error {
	f.checked = append(f.checked, apiKey)
	if apiKey != "el-test" {
		return elevenlabs.ErrUnauthorized
	}
	return nil
}

func words(spec ...any) []elevenlabs.Word {
	// spec: text, start, speaker, … ; words are separated by spacing.
	var out []elevenlabs.Word
	for i := 0; i < len(spec); i += 3 {
		if i > 0 {
			out = append(out, elevenlabs.Word{Text: " ", Type: "spacing"})
		}
		typ := "word"
		if strings.HasPrefix(spec[i].(string), "(") {
			typ = "audio_event"
		}
		out = append(out, elevenlabs.Word{Text: spec[i].(string), Start: spec[i+1].(float64), Type: typ, SpeakerID: spec[i+2].(string)})
	}
	return out
}

func TestElevenLabsTranscript(t *testing.T) {
	got := elevenLabsTranscript(&elevenlabs.Transcript{Words: words(
		"Hello", 0.5, "speaker_3",
		"Anna.", 1.0, "speaker_3",
		"Hi!", 2.2, "speaker_1",
		"(laughter)", 3.0, "",
		"Okay.", 4.0, "speaker_3",
		"Long", 40.0, "speaker_3", // a new line: the turn is past 30 s after a sentence
		"turn", 70.0, "speaker_3",
		"goes", 101.0, "speaker_3", // a new line: past a minute without a sentence end
		"on", 3700.0, "speaker_1",
	)})
	want := "[0:00] Speaker 1: Hello Anna.\n" +
		"[0:02] Speaker 2: Hi! (laughter)\n" +
		"[0:04] Speaker 1: Okay.\n" +
		"[0:40] Speaker 1: Long turn\n" +
		"[1:41] Speaker 1: goes\n" +
		"[1:01:40] Speaker 2: on"
	if got != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	if labels := SpeakerLabels(got); strings.Join(labels, ",") != "Speaker 1,Speaker 2" {
		t.Fatalf("labels = %v", labels)
	}

	if got := elevenLabsTranscript(&elevenlabs.Transcript{Text: " just text "}); got != "[0:00] Speaker 1: just text" {
		t.Fatalf("no words: %q", got)
	}
	if got := elevenLabsTranscript(&elevenlabs.Transcript{}); got != "" {
		t.Fatalf("silence: %q", got)
	}
}

// useElevenLabs switches the service's transcription to ElevenLabs.
func useElevenLabs(t *testing.T, s *AIService, speech *fakeSpeech) {
	t.Helper()
	s.Speech = speech
	provider, key := "elevenlabs", "el-test"
	v, err := s.UpdateSettings(context.Background(), OpenRouterUpdate{TranscriptionProvider: &provider, ElevenLabsAPIKey: &key})
	if err != nil {
		t.Fatal(err)
	}
	if v.TranscriptionProvider != "elevenlabs" || !v.ElevenLabsAPIKeyConfigured || v.ElevenLabsAPIKeyHint != "" || !v.APIKeyConfigured {
		t.Fatalf("view = %+v", v)
	}
}

func TestTranscribeElevenLabs(t *testing.T) {
	ai := &fakeAI{}
	s, objects := newAI(t, ai)
	speech := &fakeSpeech{answer: &elevenlabs.Transcript{Words: words("Hi.", 1.0, "speaker_0", "Hello.", 2.0, "speaker_1")}}
	useElevenLabs(t, s, speech)

	mp3 := []byte("ID3 fake mp3")
	_ = objects.Put(context.Background(), "recordings/pocket/r1.mp3", bytes.NewReader(mp3), int64(len(mp3)), "audio/mpeg")
	// Size doesn't matter: ElevenLabs takes long recordings in one piece.
	rec := &recording.Recording{ID: "r1", Audio: &recording.Object{Key: "recordings/pocket/r1.mp3", ContentType: "audio/mpeg", Size: maxPassthroughAudio + 1}}
	if err := s.Transcribe(context.Background(), rec); err != nil {
		t.Fatal(err)
	}
	if string(speech.audio) != string(mp3) || speech.filename != "r1.mp3" || len(ai.requests) != 0 {
		t.Fatalf("sent %q as %s; OpenRouter requests %d", speech.audio, speech.filename, len(ai.requests))
	}
	if rec.Transcript.Text != "[0:01] Speaker 1: Hi.\n[0:02] Speaker 2: Hello." || rec.Transcript.Model != "elevenlabs/scribe_v2" {
		t.Fatalf("transcript = %+v", rec.Transcript)
	}

	speech.err = errors.New("boom")
	if err := s.Transcribe(context.Background(), rec); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error: %v", err)
	}

	// Without the ElevenLabs client the provider can't be used.
	s.Speech = nil
	if err := s.Transcribe(context.Background(), rec); !errors.Is(err, ErrSpeechUnavailable) {
		t.Fatalf("no client: %v", err)
	}
}

func TestElevenLabsSettings(t *testing.T) {
	s, _ := newAI(t, &fakeAI{})
	ctx := context.Background()
	if !s.CanTranscribe(ctx) {
		t.Fatal("OpenRouter transcription should be configured")
	}
	if v, _ := s.Settings(ctx); v.TranscriptionProvider != "openrouter" {
		t.Fatalf("default provider = %q", v.TranscriptionProvider)
	}

	// ElevenLabs without a key isn't configured.
	provider := "elevenlabs"
	if _, err := s.UpdateSettings(ctx, OpenRouterUpdate{TranscriptionProvider: &provider}); err != nil {
		t.Fatal(err)
	}
	if s.CanTranscribe(ctx) {
		t.Fatal("ElevenLabs without a key can't transcribe")
	}
	key := "el-0123456789"
	if v, _ := s.UpdateSettings(ctx, OpenRouterUpdate{ElevenLabsAPIKey: &key}); !s.CanTranscribe(ctx) || v.ElevenLabsAPIKeyHint != "…6789" {
		t.Fatalf("with key: %+v", v)
	}

	bad := "whisper"
	if _, err := s.UpdateSettings(ctx, OpenRouterUpdate{TranscriptionProvider: &bad}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("bad provider: %v", err)
	}
}

func TestTestElevenLabs(t *testing.T) {
	s, _ := newAI(t, &fakeAI{})
	ctx := context.Background()
	if err := s.TestElevenLabs(ctx, "el-test"); !errors.Is(err, ErrSpeechUnavailable) {
		t.Fatalf("no client: %v", err)
	}
	speech := &fakeSpeech{}
	s.Speech = speech
	if err := s.TestElevenLabs(ctx, ""); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("no key: %v", err)
	}
	if err := s.TestElevenLabs(ctx, " wrong "); !errors.Is(err, elevenlabs.ErrUnauthorized) || speech.checked[0] != "wrong" {
		t.Fatalf("wrong key: %v %v", err, speech.checked)
	}
	useElevenLabs(t, s, speech)
	if err := s.TestElevenLabs(ctx, ""); err != nil || speech.checked[1] != "el-test" {
		t.Fatalf("stored key: %v %v", err, speech.checked)
	}
}
