package service

import (
	"context"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/settings"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/elevenlabs"
)

const (
	// lineAfter is how long a speaker's turn runs before a new line is started at the end of
	// a sentence; lineMax starts one in any case.
	lineAfter = 30 * time.Second
	lineMax   = time.Minute
)

// transcribeElevenLabs transcribes a recording's archived audio through ElevenLabs, in one
// piece, which tells the speakers apart by their voices. The transcript has the same form
// as the one the OpenRouter models write: "[m:ss] Speaker 1: …" lines. It is in the
// language that is spoken; ElevenLabs doesn't translate.
func (s *AIService) transcribeElevenLabs(ctx context.Context, st *settings.OpenRouter, rec *recording.Recording) error {
	if s.Speech == nil {
		return ErrSpeechUnavailable
	}
	body, err := s.objects.Get(ctx, rec.Audio.Key, 0, -1)
	if err != nil {
		return fmt.Errorf("read archived audio: %w", err)
	}
	defer body.Close()
	start := s.clock()
	t, err := s.Speech.Transcribe(ctx, st.ElevenLabsAPIKey, body, path.Base(rec.Audio.Key))
	if err != nil {
		return fmt.Errorf("transcribe: %w", err)
	}
	model := settings.ProviderElevenLabs + "/" + elevenlabs.Model
	rec.Transcript = &recording.Transcript{Text: elevenLabsTranscript(t), Model: model, CreatedAt: s.clock().UTC()}
	s.log.Info("recording transcribed", "id", rec.ID, "model", model, "language", t.LanguageCode,
		"chars", len(rec.Transcript.Text), "took", s.clock().Sub(start).Round(time.Second).String())
	return nil
}

// elevenLabsTranscript writes a transcript as lines of "[m:ss] Speaker N: …": a new line every
// time the speaker changes, and in long turns at the end of a sentence after lineAfter.
// Speakers are numbered in the order they first speak.
func elevenLabsTranscript(t *elevenlabs.Transcript) string {
	if len(t.Words) == 0 {
		if text := strings.TrimSpace(t.Text); text != "" {
			return "[0:00] Speaker 1: " + text
		}
		return ""
	}
	labels := map[string]string{}
	label := func(id string) string {
		if l, ok := labels[id]; ok {
			return l
		}
		l := fmt.Sprintf("Speaker %d", len(labels)+1)
		labels[id] = l
		return l
	}

	var lines []string
	var line strings.Builder
	speaker, lineStart, sentenceEnd := "", 0.0, false
	flush := func() {
		if text := strings.Join(strings.Fields(line.String()), " "); text != "" {
			lines = append(lines, fmt.Sprintf("[%s] %s: %s", formatOffset(int64(lineStart*1000)), label(speaker), text))
		}
		line.Reset()
	}
	for _, w := range t.Words {
		if w.Type == "spacing" {
			line.WriteString(w.Text)
			continue
		}
		id := w.SpeakerID
		if id == "" && w.Type == "audio_event" {
			id = speaker // a sound without a speaker stays in the current line
		}
		long := time.Duration((w.Start - lineStart) * float64(time.Second))
		if line.Len() == 0 || id != speaker || (sentenceEnd && long >= lineAfter) || long >= lineMax {
			flush()
			speaker, lineStart = id, w.Start
		}
		line.WriteString(w.Text)
		if w.Type == "word" {
			sentenceEnd = endsSentence(w.Text)
		}
	}
	flush()
	return strings.Join(lines, "\n")
}

// endsSentence reports whether a word ends a sentence, also before closing quotes.
func endsSentence(word string) bool {
	word = strings.TrimRight(word, `"')»“”’`)
	for _, p := range []string{".", "?", "!", "…"} {
		if strings.HasSuffix(word, p) {
			return true
		}
	}
	return false
}
