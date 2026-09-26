package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/audio"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/settings"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/openrouter"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/ports"
)

// AIClient is the part of the OpenRouter client the AI stages need.
type AIClient interface {
	Complete(ctx context.Context, apiKey string, r openrouter.Request) (string, error)
	Models(ctx context.Context) ([]openrouter.Model, error)
}

const (
	// transcriptionChunk is the length of the audio pieces sent for transcription. Five
	// minutes of 16 kHz mono WAV is about 10 MB (13 MB base64), within provider limits.
	transcriptionChunk = 5 * time.Minute
	// maxPassthroughAudio limits compressed audio (e.g. MP3 from Pocket) sent in one piece,
	// since it can't be split without a decoder.
	maxPassthroughAudio = 20 << 20
)

const transcriptionPrompt = `Transcribe this audio recording verbatim in its original language. Do not translate, summarize or comment.
Start each speaker's turn on a new line with its start time in the form [m:ss] (minutes and seconds from the start of this audio), followed by a speaker label such as "Speaker 1:" when more than one person speaks, e.g. "[1:05] Speaker 2: …". In long turns, start a new line with a new time stamp at least every 30 seconds.
Output only the transcript. If there is no speech, output nothing.`

// timestampPattern matches the [m:ss] / [h:mm:ss] time stamps in transcripts.
var timestampPattern = regexp.MustCompile(`\[(?:(\d{1,2}):)?(\d{1,3}):(\d{2})\]`)

// shiftTimestamps adds offset to the time stamps in a transcript piece, so that the
// pieces of a long recording carry times relative to the whole recording.
func shiftTimestamps(text string, offset time.Duration) string {
	return timestampPattern.ReplaceAllStringFunc(text, func(m string) string {
		p := timestampPattern.FindStringSubmatch(m)
		h, _ := strconv.Atoi(p[1])
		min, _ := strconv.Atoi(p[2])
		sec, _ := strconv.Atoi(p[3])
		d := time.Duration(h)*time.Hour + time.Duration(min)*time.Minute + time.Duration(sec)*time.Second + offset
		return "[" + formatOffset(d.Milliseconds()) + "]"
	})
}

// summaryPrompt is completed with the theme's structure and the output language.
const summaryPrompt = `You summarize transcripts of recorded conversations and voice notes.
Reply with a JSON object with exactly two string fields:
- "title": a short, specific title for the conversation (at most 8 words, no quotes, no trailing period).
- "summary": the summary in Markdown, structured as follows:
%s
%s Reply with the JSON object only.`

// summarySystemPrompt builds the instructions for a theme, a language ("auto" or a key of
// SummaryLanguages) and the highlights the user marked.
func summarySystemPrompt(instructions, language string, highlights []recording.Highlight) string {
	lang := "Write the title and the summary in the language of the transcript."
	if name, ok := SummaryLanguages[language]; ok {
		lang = "Write the title and the summary in " + name + ", regardless of the transcript's language."
	}
	if len(highlights) > 0 {
		times := make([]string, len(highlights))
		for i, h := range highlights {
			times[i] = formatOffset(h.OffsetMs)
		}
		instructions += "\n\nWhile recording, the user marked these moments as highlights: " + strings.Join(times, ", ") +
			". End the summary with a section \"## Highlights\" (heading translated into the summary's language): a bullet list with one item per highlight, in order, starting with the time in bold (e.g. **" + times[0] +
			"**), followed by one sentence on what was being said or decided at that moment. Use the [m:ss] time stamps in the transcript to find it."
	}
	return fmt.Sprintf(summaryPrompt, instructions, lang)
}

// AIService runs the transcription and summary stages through OpenRouter, and manages the
// OpenRouter settings.
type AIService struct {
	themes   *ThemeService
	settings ports.SettingsRepository
	objects  ports.ObjectStore
	ai       AIClient
	tmpDir   string
	log      *slog.Logger
	clock    func() time.Time
	// OnSettingsChanged is called after the settings were saved, e.g. to wake the worker so
	// recordings waiting for a configuration are processed. Optional.
	OnSettingsChanged func()
}

// NewAIService builds the service. tmpDir holds audio while it is being transcribed.
func NewAIService(st ports.SettingsRepository, themes *ThemeService, objects ports.ObjectStore, ai AIClient, tmpDir string, log *slog.Logger) *AIService {
	return &AIService{settings: st, themes: themes, objects: objects, ai: ai, tmpDir: tmpDir, log: log, clock: time.Now}
}

// --- settings ---

// OpenRouterView is the OpenRouter configuration as shown to administrators: the API key
// itself is never returned.
type OpenRouterView struct {
	APIKeyConfigured   bool      `json:"apiKeyConfigured"`
	APIKeyHint         string    `json:"apiKeyHint,omitempty"` // last characters, e.g. "…a1b2"
	TranscriptionModel string    `json:"transcriptionModel"`
	SummaryModel       string    `json:"summaryModel"`
	UpdatedAt          time.Time `json:"updatedAt,omitempty"`
}

// OpenRouterUpdate changes settings. Nil fields are left unchanged; an empty APIKey removes
// the key.
type OpenRouterUpdate struct {
	APIKey             *string `json:"apiKey,omitempty"`
	TranscriptionModel *string `json:"transcriptionModel,omitempty"`
	SummaryModel       *string `json:"summaryModel,omitempty"`
}

// Settings returns the current OpenRouter configuration.
func (s *AIService) Settings(ctx context.Context) (*OpenRouterView, error) {
	st, err := s.settings.OpenRouter(ctx)
	if err != nil {
		return nil, err
	}
	return view(st), nil
}

// UpdateSettings applies an update.
func (s *AIService) UpdateSettings(ctx context.Context, u OpenRouterUpdate) (*OpenRouterView, error) {
	st, err := s.settings.OpenRouter(ctx)
	if err != nil {
		return nil, err
	}
	if u.APIKey != nil {
		st.APIKey = strings.TrimSpace(*u.APIKey)
	}
	if u.TranscriptionModel != nil {
		st.TranscriptionModel = strings.TrimSpace(*u.TranscriptionModel)
	}
	if u.SummaryModel != nil {
		st.SummaryModel = strings.TrimSpace(*u.SummaryModel)
	}
	for _, m := range []string{st.TranscriptionModel, st.SummaryModel} {
		if len(m) > 200 || strings.ContainsAny(m, " \t\n") {
			return nil, invalid("model IDs look like \"google/gemini-2.5-flash\"")
		}
	}
	st.UpdatedAt = s.clock().UTC()
	if err := s.settings.SaveOpenRouter(ctx, st); err != nil {
		return nil, err
	}
	if s.OnSettingsChanged != nil {
		s.OnSettingsChanged()
	}
	return view(st), nil
}

func view(st *settings.OpenRouter) *OpenRouterView {
	v := &OpenRouterView{
		APIKeyConfigured: st.APIKey != "", TranscriptionModel: st.TranscriptionModel,
		SummaryModel: st.SummaryModel, UpdatedAt: st.UpdatedAt,
	}
	if k := st.APIKey; len(k) >= 8 {
		v.APIKeyHint = "…" + k[len(k)-4:]
	}
	return v
}

// ModelOption is a model offered for selection.
type ModelOption struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	ContextLength   int    `json:"contextLength"`
	PromptPrice     string `json:"promptPrice"`     // USD per input token
	CompletionPrice string `json:"completionPrice"` // USD per output token
	AudioPrice      string `json:"audioPrice,omitempty"`
}

// ModelOptions lists the models suitable for each task.
type ModelOptions struct {
	Transcription []ModelOption `json:"transcription"` // audio in, text out
	Summary       []ModelOption `json:"summary"`       // text in, text out
}

// Models returns the OpenRouter models usable for transcription and for summaries. Batch
// variants (asynchronous) and the automatic routers are left out.
func (s *AIService) Models(ctx context.Context) (*ModelOptions, error) {
	models, err := s.ai.Models(ctx)
	if err != nil {
		return nil, err
	}
	out := &ModelOptions{Transcription: []ModelOption{}, Summary: []ModelOption{}}
	for _, m := range models {
		if strings.HasSuffix(m.ID, ":batch") || strings.HasPrefix(m.ID, "openrouter/") || !m.Produces("text") {
			continue
		}
		o := ModelOption{ID: m.ID, Name: m.Name, ContextLength: m.ContextLength,
			PromptPrice: m.PromptPrice, CompletionPrice: m.CompletionPrice, AudioPrice: m.AudioPrice}
		if m.Accepts("audio") {
			out.Transcription = append(out.Transcription, o)
		}
		if m.Accepts("text") {
			out.Summary = append(out.Summary, o)
		}
	}
	byName := func(l []ModelOption) {
		sort.Slice(l, func(i, j int) bool { return strings.ToLower(l[i].Name) < strings.ToLower(l[j].Name) })
	}
	byName(out.Transcription)
	byName(out.Summary)
	return out, nil
}

// --- stages ---

// CanTranscribe reports whether the transcription stage is configured.
func (s *AIService) CanTranscribe(ctx context.Context) bool {
	st, err := s.settings.OpenRouter(ctx)
	return err == nil && st.CanTranscribe()
}

// CanSummarize reports whether the summary stage is configured.
func (s *AIService) CanSummarize(ctx context.Context) bool {
	st, err := s.settings.OpenRouter(ctx)
	return err == nil && st.CanSummarize()
}

// Transcribe is the stage stored → transcribed.
func (s *AIService) Transcribe(ctx context.Context, rec *recording.Recording) error {
	st, err := s.settings.OpenRouter(ctx)
	if err != nil {
		return err
	}
	if !st.CanTranscribe() {
		return errors.New("transcription is not configured")
	}
	if rec.Audio == nil {
		return errors.New("recording has no archived audio")
	}

	// Fetch the archived audio to a temporary file.
	tmp, err := os.CreateTemp(s.tmpDir, rec.ID+"-*.transcribe")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	body, err := s.objects.Get(ctx, rec.Audio.Key, 0, -1)
	if err != nil {
		tmp.Close()
		return fmt.Errorf("read archived audio: %w", err)
	}
	_, err = io.Copy(tmp, body)
	body.Close()
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("read archived audio: %w", err)
	}

	start := s.clock()
	var parts []string
	transcribe := func(data []byte, format string, part int) error {
		prompt := transcriptionPrompt
		if part > 1 {
			prompt += fmt.Sprintf("\nThis is part %d of a longer recording; the previous part ended just before it.", part)
		}
		text, err := s.ai.Complete(ctx, st.APIKey, openrouter.Request{
			Model: st.TranscriptionModel,
			Messages: []openrouter.Message{{Role: "user", Content: []any{
				openrouter.Text(prompt),
				openrouter.Audio(base64.StdEncoding.EncodeToString(data), format),
			}}},
		})
		if err != nil {
			return fmt.Errorf("transcribe part %d: %w", part, err)
		}
		if t := strings.TrimSpace(text); t != "" {
			parts = append(parts, shiftTimestamps(t, time.Duration(part-1)*transcriptionChunk))
		}
		return nil
	}

	if rec.Audio.ContentType == "audio/flac" {
		// Our own FLAC: split into short mono 16 kHz WAV pieces.
		n := 0
		err = audio.SpeechChunks(tmp.Name(), transcriptionChunk, func(wav []byte) error {
			n++
			return transcribe(wav, "wav", n)
		})
	} else {
		// Compressed audio from an external source: send as it is.
		var data []byte
		if rec.Audio.Size > maxPassthroughAudio {
			return fmt.Errorf("%s audio of %d MB is too large to transcribe in one piece (limit %d MB)",
				rec.Audio.ContentType, rec.Audio.Size>>20, maxPassthroughAudio>>20)
		}
		if data, err = os.ReadFile(tmp.Name()); err == nil {
			err = transcribe(data, strings.TrimPrefix(path.Ext(rec.Audio.Key), "."), 1)
		}
	}
	if err != nil {
		return err
	}

	rec.Transcript = &recording.Transcript{Text: strings.Join(parts, "\n\n"), Model: st.TranscriptionModel, CreatedAt: s.clock().UTC()}
	s.log.Info("recording transcribed", "id", rec.ID, "model", st.TranscriptionModel, "chars", len(rec.Transcript.Text),
		"took", s.clock().Sub(start).Round(time.Second).String())
	return nil
}

// Summarize is the stage transcribed → summarized.
func (s *AIService) Summarize(ctx context.Context, rec *recording.Recording) error {
	st, err := s.settings.OpenRouter(ctx)
	if err != nil {
		return err
	}
	if !st.CanSummarize() {
		return errors.New("summarization is not configured")
	}
	if rec.Transcript == nil {
		return errors.New("recording has no transcript")
	}
	opts := rec.SummaryOptions
	language := opts.Language
	if language == "" {
		language = "auto"
	}
	model := st.SummaryModel
	if opts.Model != "" {
		model = opts.Model
	}
	th := s.themes.Resolve(ctx, rec.OwnerID, opts.ThemeID)
	if strings.TrimSpace(rec.Transcript.Text) == "" {
		rec.Summary = &recording.Summary{Title: "No speech detected", Markdown: "_No speech was detected in this recording._",
			Language: language, ThemeID: th.ID, ThemeName: th.Name, CreatedAt: s.clock().UTC()}
		return nil
	}

	var meta strings.Builder
	if rec.RecordedAt != nil {
		fmt.Fprintf(&meta, "Recorded: %s\n", rec.RecordedAt.Format(time.RFC1123))
	}
	if rec.Title != "" {
		fmt.Fprintf(&meta, "Source title: %s\n", rec.Title)
	}
	answer, err := s.ai.Complete(ctx, st.APIKey, openrouter.Request{
		Model: model,
		JSON:  true,
		Messages: []openrouter.Message{
			{Role: "system", Content: summarySystemPrompt(th.Instructions, language, rec.Highlights)},
			{Role: "user", Content: meta.String() + "\nTranscript:\n\n" + rec.Transcript.Text},
		},
	})
	if err != nil {
		return fmt.Errorf("summarize: %w", err)
	}
	title, markdown := parseSummary(answer)
	rec.Summary = &recording.Summary{Title: title, Markdown: markdown, Model: model, Language: language,
		ThemeID: th.ID, ThemeName: th.Name, CreatedAt: s.clock().UTC()}
	s.log.Info("recording summarized", "id", rec.ID, "model", model, "theme", th.ID, "language", language, "title", title)
	return nil
}

// parseSummary reads the model's JSON answer. Models sometimes wrap it in a code fence or
// add text around it; if no JSON can be found, the first line becomes the title.
func parseSummary(answer string) (title, markdown string) {
	s := strings.TrimSpace(answer)
	if i, j := strings.Index(s, "{"), strings.LastIndex(s, "}"); i >= 0 && j > i {
		var out struct {
			Title   string `json:"title"`
			Summary string `json:"summary"`
		}
		if json.Unmarshal([]byte(s[i:j+1]), &out) == nil && (out.Title != "" || out.Summary != "") {
			return cleanTitle(out.Title), strings.TrimSpace(out.Summary)
		}
	}
	first, rest, _ := strings.Cut(s, "\n")
	return cleanTitle(strings.TrimLeft(first, "# ")), strings.TrimSpace(rest)
}

func cleanTitle(t string) string {
	t = strings.Trim(strings.TrimSpace(t), `"'*`)
	t = strings.TrimSuffix(t, ".")
	if r := []rune(t); len(r) > 120 {
		t = string(r[:120]) + "…"
	}
	if t == "" {
		t = "Untitled conversation"
	}
	return t
}
