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
	"slices"
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
Tell the speakers apart by their voices (pitch, timbre, accent, speaking style) and by the conversation (questions and answers, people addressing or answering each other), and number them in the order they first speak. Do not merge different people into one speaker.
Start a new line every time the speaker changes, even for a short interjection, with its start time in the form [m:ss] (minutes and seconds from the start of this audio) and the speaker's label, e.g. "[1:05] Speaker 2: …". Label every line, also "Speaker 1" when only one person speaks, and keep the same label for the same voice throughout. In long turns, start a new line with a new time stamp at least every 30 seconds.
Output only the transcript. If there is no speech, output nothing.`

// continuationContext is how much of the end of the previous part's transcript is shown when
// transcribing the next part of a long recording, so speakers keep their labels.
const continuationContext = 2000

// continuationPrompt tells the model transcribing a later part of a long recording how the
// previous part ended, so the same people keep the labels they had and new ones are numbered
// on. It can't hear the earlier audio: matching voices relies on the conversation.
func continuationPrompt(part int, previous []string) string {
	p := fmt.Sprintf("\nThis is part %d of a longer recording; the previous part ended just before it.", part)
	text := strings.Join(previous, "\n\n")
	labels := SpeakerLabels(text)
	if len(labels) == 0 {
		return p
	}
	tail := text
	if len(tail) > continuationContext {
		tail = tail[len(tail)-continuationContext:]
		if i := strings.IndexByte(tail, '\n'); i >= 0 {
			tail = tail[i+1:] // start at a whole line
		}
	}
	highest := 0
	for _, l := range labels {
		if n, err := strconv.Atoi(strings.TrimPrefix(l, "Speaker ")); err == nil && strings.HasPrefix(l, "Speaker ") {
			highest = max(highest, n)
		}
	}
	p += "\nSo far the speakers were labeled " + strings.Join(labels, ", ") + "."
	p += " Continue the conversation: give people who already spoke the label they had (most likely the ones speaking at the end of the previous part)"
	if highest > 0 {
		p += fmt.Sprintf(", and number new speakers from Speaker %d on", highest+1)
	}
	p += ". Time stamps still count from the start of this audio. The previous part ended with:\n<previous>\n" + tail + "\n</previous>"
	return p
}

// accountLanguage is how an app language a user can choose (user.Languages) applies to their
// recordings: Name is the language transcripts are written in, Summary the key of
// SummaryLanguages their summaries use.
type accountLanguage struct{ Name, Summary string }

var accountLanguages = map[string]accountLanguage{
	"en": {"English", "en-US"},
	"de": {"German", "de-DE"},
}

// inLanguage turns a prompt that keeps the original language into one that writes in
// language (e.g. "German"), translating what is in another language. An empty language
// keeps the prompt as it is.
func inLanguage(prompt, language string) string {
	if language == "" {
		return prompt
	}
	to := "in " + language + ", translating anything in another language into " + language + ". Do not"
	return strings.NewReplacer(
		"in its original language. Do not translate,", to,
		"in their original language. Do not translate,", to,
	).Replace(prompt)
}

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

// summaryIntro opens the summary instructions; documentSummaryIntro replaces it for documents.
const (
	summaryIntro         = "You summarize transcripts of recorded conversations and voice notes.\n"
	documentSummaryIntro = "You summarize handwritten notes and documents, given as the text read from their pages.\n"
)

// summaryPrompt is completed with the theme's structure and the output language.
const summaryPrompt = summaryIntro + `Reply with a JSON object with exactly these fields:
- "title": a short, specific title for the conversation (at most 8 words, no quotes, no trailing period).
- "summary": the summary in Markdown, structured as follows:
%s
- "actionItems": an array of the concrete tasks and follow-ups that someone committed to or was asked to do, in the order they came up; an empty array if there are none. Leave out vague intentions and things already done. Each item is an object with:
  - "text": the task as a short imperative sentence (at most 15 words), e.g. "Send the revised offer to Anna".
  - "owner": the name of the person who should do it, or "" if unclear.
  - "due": the date it is due as YYYY-MM-DD if a date or deadline was stated (resolve relative dates such as "next Friday" from the recording date), otherwise "".
- "speakers": when the transcript's lines carry speaker labels such as "Speaker 1:", the speakers whose names are clear from the conversation (they introduce themselves, are addressed by name or are named by others), as objects with "label" (the label exactly as in the transcript, e.g. "Speaker 1") and "name" (the person's name as used in the conversation, e.g. "Anna"). Leave out speakers whose names aren't clear; an empty array if there are none.
%s Write the action items' text in the same language as the summary. Reply with the JSON object only.`

// emptySummary is the title and text of the summary of a recording without speech (or a
// document without text), in German for a German summary language and in English otherwise.
func emptySummary(language string, document bool) (title, text string) {
	german := strings.HasPrefix(language, "de-")
	switch {
	case document && german:
		return "Kein Text gefunden", "_In diesem Dokument wurde kein Text gefunden._"
	case document:
		return "No text found", "_No text was found in this document._"
	case german:
		return "Keine Sprache erkannt", "_In dieser Aufnahme wurde keine Sprache erkannt._"
	}
	return "No speech detected", "_No speech was detected in this recording._"
}

// maxActionItems bounds the action items kept from one summary.
const maxActionItems = 30

// summarySystemPrompt builds the instructions for a theme, a language ("auto" or a key of
// SummaryLanguages) and the highlights the user marked.
func summarySystemPrompt(instructions, language string, highlights []recording.Highlight) string {
	// These instructions are in English; without a firm rule the model tends to answer in
	// English too, even for a transcript in another language.
	lang := "Write the title and the summary in the language of the transcript (the language most of it is spoken in), " +
		"even though these instructions are in English: translate the headings given above into that language. " +
		"Only write in English if the transcript is in English."
	if name, ok := SummaryLanguages[language]; ok {
		lang = "Write the title and the summary in " + name + ", regardless of the transcript's language, " +
			"translating the headings given above into " + name + "."
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
	// Users looks up a recording's owner, whose app language transcripts and summaries are
	// written in. Optional: without it (or a language), the recording's language is kept.
	Users   ports.UserRepository
	objects ports.ObjectStore
	ai      AIClient
	tmpDir  string
	log     *slog.Logger
	clock   func() time.Time
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
	DocumentModel      string    `json:"documentModel"` // empty: the transcription model
	UpdatedAt          time.Time `json:"updatedAt,omitempty"`
}

// OpenRouterUpdate changes settings. Nil fields are left unchanged; an empty APIKey removes
// the key.
type OpenRouterUpdate struct {
	APIKey             *string `json:"apiKey,omitempty"`
	TranscriptionModel *string `json:"transcriptionModel,omitempty"`
	SummaryModel       *string `json:"summaryModel,omitempty"`
	DocumentModel      *string `json:"documentModel,omitempty"`
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
	if u.DocumentModel != nil {
		st.DocumentModel = strings.TrimSpace(*u.DocumentModel)
	}
	for _, m := range []string{st.TranscriptionModel, st.SummaryModel, st.DocumentModel} {
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
		SummaryModel: st.SummaryModel, DocumentModel: st.DocumentModel, UpdatedAt: st.UpdatedAt,
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
	Document      []ModelOption `json:"document"`      // images in, text out
}

// Models returns the OpenRouter models usable for transcription and for summaries. Batch
// variants (asynchronous) and the automatic routers are left out.
func (s *AIService) Models(ctx context.Context) (*ModelOptions, error) {
	models, err := s.ai.Models(ctx)
	if err != nil {
		return nil, err
	}
	out := &ModelOptions{Transcription: []ModelOption{}, Summary: []ModelOption{}, Document: []ModelOption{}}
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
		if m.Accepts("image") {
			out.Document = append(out.Document, o)
		}
	}
	byName := func(l []ModelOption) {
		sort.Slice(l, func(i, j int) bool { return strings.ToLower(l[i].Name) < strings.ToLower(l[j].Name) })
	}
	byName(out.Transcription)
	byName(out.Summary)
	byName(out.Document)
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

// ownerLanguage returns the app language the owner of a recording chose in the settings,
// or the zero value when there is none (the web UI then follows the browser).
func (s *AIService) ownerLanguage(ctx context.Context, ownerID string) (lang accountLanguage) {
	if s.Users == nil || ownerID == "" {
		return lang
	}
	u, err := s.Users.Get(ctx, ownerID)
	if err != nil {
		if !errors.Is(err, ErrNotFound) {
			s.log.Warn("read owner's language", "user", ownerID, "err", err)
		}
		return lang
	}
	return accountLanguages[u.Language]
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
	language := s.ownerLanguage(ctx, rec.OwnerID).Name
	if rec.IsDocument() {
		return s.readDocument(ctx, st, rec, language)
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
		prompt := inLanguage(transcriptionPrompt, language)
		if part > 1 {
			prompt += continuationPrompt(part, parts)
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
		// Auto: the language the owner chose for the app, else the transcript's.
		language = s.ownerLanguage(ctx, rec.OwnerID).Summary
	}
	if language == "" {
		language = "auto"
	}
	model := st.SummaryModel
	if opts.Model != "" {
		model = opts.Model
	}
	if rec.IsDocument() && rec.Summary != nil && rec.Summary.EditedAt != nil {
		// A changed document was read again; the summary the user edited is kept.
		return nil
	}
	th := s.themes.Resolve(ctx, rec.OwnerID, opts.ThemeID)
	if strings.TrimSpace(rec.Transcript.Text) == "" {
		title, text := emptySummary(language, rec.IsDocument())
		if rec.IsDocument() && rec.Title != "" {
			title = rec.Title
		}
		rec.Summary = &recording.Summary{Title: title, Markdown: text,
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
	system, label := summarySystemPrompt(th.Instructions, language, rec.Highlights), "Transcript"
	if rec.IsDocument() {
		system, label = documentSummaryIntro+strings.TrimPrefix(system, summaryIntro), "Document text"
		system = strings.Replace(system, "title for the conversation", "title for the notes", 1)
	}
	answer, err := s.ai.Complete(ctx, st.APIKey, openrouter.Request{
		Model: model,
		JSON:  true,
		Messages: []openrouter.Message{
			{Role: "system", Content: system},
			{Role: "user", Content: meta.String() + "\n" + label + ":\n\n" + rec.Transcript.Text},
		},
	})
	if err != nil {
		return fmt.Errorf("summarize: %w", err)
	}
	title, markdown, items := parseSummary(answer)
	if rec.Source == recording.SourceRemarkable && rec.Title != "" {
		// A reMarkable note keeps the name it has on the tablet, so it can be found by it.
		title = rec.Title
	}
	rec.Summary = &recording.Summary{Title: title, Markdown: markdown, Model: model, Language: language,
		ThemeID: th.ID, ThemeName: th.Name, ActionItems: items, Speakers: parseSpeakers(answer, rec.Transcript.Text),
		CreatedAt: s.clock().UTC()}
	s.log.Info("recording summarized", "id", rec.ID, "model", model, "theme", th.ID, "language", language, "title", title)
	return nil
}

// parseSummary reads the model's JSON answer. Models sometimes wrap it in a code fence or
// add text around it; if no JSON can be found, the first line becomes the title.
func parseSummary(answer string) (title, markdown string, items []recording.ActionItem) {
	s := strings.TrimSpace(answer)
	if i, j := strings.Index(s, "{"), strings.LastIndex(s, "}"); i >= 0 && j > i {
		var out struct {
			Title       string            `json:"title"`
			Summary     string            `json:"summary"`
			ActionItems []json.RawMessage `json:"actionItems"`
		}
		if json.Unmarshal([]byte(s[i:j+1]), &out) == nil && (out.Title != "" || out.Summary != "") {
			return cleanTitle(out.Title), strings.TrimSpace(out.Summary), parseActionItems(out.ActionItems)
		}
	}
	first, rest, _ := strings.Cut(s, "\n")
	return cleanTitle(strings.TrimLeft(first, "# ")), strings.TrimSpace(rest), nil
}

// parseActionItems keeps the usable action items of the model's answer: items without text
// are skipped, a bare string is taken as the text, and a due date that isn't YYYY-MM-DD is
// dropped.
func parseActionItems(raw []json.RawMessage) []recording.ActionItem {
	var out []recording.ActionItem
	for _, r := range raw {
		var it struct {
			Text  string `json:"text"`
			Owner string `json:"owner"`
			Due   string `json:"due"`
		}
		if json.Unmarshal(r, &it) != nil {
			if json.Unmarshal(r, &it.Text) != nil {
				continue
			}
		}
		text := truncateRunes(strings.Join(strings.Fields(it.Text), " "), 300)
		if text == "" {
			continue
		}
		due := strings.TrimSpace(it.Due)
		if _, err := recording.ParseDate(due); err != nil {
			due = ""
		}
		out = append(out, recording.ActionItem{ID: newID()[:12], Text: text, Owner: truncateRunes(it.Owner, 80), Due: due})
		if len(out) == maxActionItems {
			break
		}
	}
	return out
}

// maxSpeakerNames bounds the speaker names kept from one summary.
const maxSpeakerNames = 20

// parseSpeakers reads the speaker names of the model's JSON answer. Only labels that name
// speakers in the transcript are kept, each once.
func parseSpeakers(answer, transcript string) []recording.SpeakerName {
	s := strings.TrimSpace(answer)
	i, j := strings.Index(s, "{"), strings.LastIndex(s, "}")
	if i < 0 || j <= i {
		return nil
	}
	var out struct {
		Speakers []struct {
			Label string `json:"label"`
			Name  string `json:"name"`
		} `json:"speakers"`
	}
	if json.Unmarshal([]byte(s[i:j+1]), &out) != nil {
		return nil
	}
	labels := SpeakerLabels(transcript)
	var names []recording.SpeakerName
	for _, sp := range out.Speakers {
		label, name := strings.TrimSpace(sp.Label), strings.Join(strings.Fields(sp.Name), " ")
		if !slices.Contains(labels, label) || validSpeakerName(name) != nil || strings.EqualFold(label, name) ||
			slices.ContainsFunc(names, func(n recording.SpeakerName) bool { return n.Label == label }) {
			continue
		}
		names = append(names, recording.SpeakerName{Label: label, Name: name})
		if len(names) == maxSpeakerNames {
			break
		}
	}
	return names
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
