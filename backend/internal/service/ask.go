package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/openrouter"
)

// Semantic search works without an index: the summary model reads a catalog of the user's
// notes (title, date and the start of the text of each) and picks the ones that fit the
// question, and names words to look for. Those words are looked up in the full texts
// (transcripts included), which finds what the catalog doesn't show. The notes found both
// ways are then given to the model in full (long transcripts as excerpts around the words)
// to answer the question, citing its sources.
const (
	// maxQuestion bounds a question.
	maxQuestion = 1000
	// maxCatalogNotes is how many notes, newest first, the catalog lists.
	maxCatalogNotes = 800
	// maxCatalogChars bounds the catalog sent to the model; the snippets get shorter to fit.
	maxCatalogChars = 120_000
	// catalogSnippet is how much of a note's text the catalog shows at most.
	catalogSnippet = 240
	// maxPicked and maxKeywords bound what the model picks from the catalog.
	maxPicked   = 12
	maxKeywords = 16
	// maxSources is how many notes are read to answer a question; maxFound how many Find
	// returns.
	maxSources = 8
	maxFound   = 20
	// maxContextChars bounds the notes' texts sent to answer a question; maxNoteChars each
	// note's share of it.
	maxContextChars = 80_000
	maxNoteChars    = 16_000
	// excerptRadius is how much text around a word found in a long text is shown.
	excerptRadius = 700
	// maxHistory is how many earlier questions and answers a follow-up question carries.
	maxHistory = 3
)

// citePattern matches a citation of a source in an answer: [1].
var citePattern = regexp.MustCompile(`\[(\d{1,2})\]`)

// AskService answers questions about a user's notes and finds the notes about a topic,
// through the summary model (see above).
type AskService struct {
	ai    *AIService
	notes *RecordingService
	log   *slog.Logger
	clock func() time.Time
}

// NewAskService builds the service.
func NewAskService(ai *AIService, notes *RecordingService, log *slog.Logger) *AskService {
	return &AskService{ai: ai, notes: notes, log: log, clock: time.Now}
}

// AskTurn is an earlier question and its answer, for follow-up questions.
type AskTurn struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
}

// AskInput is a question about the user's notes.
type AskInput struct {
	Question string `json:"question"`
	// History are the questions asked before in the same conversation, oldest first.
	History []AskTurn `json:"history,omitempty"`
}

// AskSource is a note an answer is based on. Ref is the number the answer cites it with
// ("[1]").
type AskSource struct {
	Ref    int    `json:"ref"`
	ID     string `json:"id"`
	Number int64  `json:"number,omitempty"`
	Title  string `json:"title"`
	Type   string `json:"type"`
	// Date is when the note was recorded or made.
	Date time.Time `json:"date"`
	// Quote is a short passage of the note the answer draws on; OffsetMs is where it is said
	// in a recording, when the model named the time stamp.
	Quote    string `json:"quote,omitempty"`
	OffsetMs *int64 `json:"offsetMs,omitempty"`
}

// AskAnswer is the answer to a question: Markdown that cites its sources as [1], [2], ….
type AskAnswer struct {
	Answer  string      `json:"answer"`
	Sources []AskSource `json:"sources"`
	Model   string      `json:"model"`
}

// FoundNote is a note found about a topic, with a short passage showing why.
type FoundNote struct {
	Note    *recording.Recording
	Excerpt string
}

// candidate is a note considered for a question.
type candidate struct {
	rec   *recording.Recording
	text  string // title and texts, for looking up words
	score int
}

// ready checks the question and that the summary model is configured.
func (s *AskService) ready(ctx context.Context, in *AskInput) (apiKey, model string, err error) {
	in.Question = strings.TrimSpace(in.Question)
	if in.Question == "" || utf8.RuneCountInString(in.Question) > maxQuestion {
		return "", "", invalid("the question is 1-%d characters", maxQuestion)
	}
	if len(in.History) > maxHistory {
		in.History = in.History[len(in.History)-maxHistory:]
	}
	for i := range in.History {
		in.History[i].Question = truncateRunes(strings.TrimSpace(in.History[i].Question), maxQuestion)
		in.History[i].Answer = truncateRunes(strings.TrimSpace(in.History[i].Answer), 4000)
	}
	st, err := s.ai.settings.OpenRouter(ctx)
	if err != nil {
		return "", "", err
	}
	if !st.CanSummarize() {
		return "", "", errors.Join(ErrNotReady, errors.New("the AI models are not set up; an administrator sets them up under Admin → General"))
	}
	return st.APIKey, st.SummaryModel, nil
}

// Ask answers a question from the account's notes (its own and those shared with it).
func (s *AskService) Ask(ctx context.Context, acc *Account, in AskInput) (*AskAnswer, error) {
	apiKey, model, err := s.ready(ctx, &in)
	if err != nil {
		return nil, err
	}
	start := s.clock()
	found, keywords, err := s.search(ctx, acc, apiKey, model, in, maxSources)
	if err != nil {
		return nil, err
	}
	out := &AskAnswer{Sources: []AskSource{}, Model: model}
	if len(found) == 0 {
		return out, nil
	}

	var notes strings.Builder
	budget := maxContextChars
	for i, c := range found {
		share := min(maxNoteChars, budget/(len(found)-i))
		block := noteContext(i+1, c.rec, keywords, share)
		budget -= len(block)
		notes.WriteString(block)
	}
	system := `You answer questions about the user's own notes: recorded conversations (with transcripts), handwritten notes, documents, photos and text notes. The notes that may be relevant follow, each introduced by its reference number in brackets, e.g. [1].
Answer from these notes only. Be concise and specific: names, dates, numbers and decisions. Cite the notes you use right after the statement they support, as [1] or [1][3]. If the notes don't answer the question, say so in one sentence and, if they help, mention what they do say.
Write the answer in the language the question is asked in, in Markdown (short paragraphs or a list; no headings).
Reply with a JSON object with exactly these fields:
- "answer": the answer in Markdown, with the citations.
- "sources": for each note cited, an object with "ref" (its number), "quote" (a short verbatim passage from the note that supports the answer, at most 200 characters, in the note's language) and "time" (the [m:ss] time stamp of the transcript line the quote is from, e.g. "12:30", or "" if it isn't from a transcript).
Reply with the JSON object only.`
	messages := []openrouter.Message{{Role: "system", Content: system + "\n\nToday is " + s.today(ctx, acc) + ".\n\nNotes:\n\n" + notes.String()}}
	for _, h := range in.History {
		messages = append(messages, openrouter.Message{Role: "user", Content: h.Question}, openrouter.Message{Role: "assistant", Content: h.Answer})
	}
	messages = append(messages, openrouter.Message{Role: "user", Content: in.Question})
	answer, err := s.ai.ai.Complete(ctx, apiKey, openrouter.Request{Model: model, JSON: true, Messages: messages})
	if err != nil {
		return nil, fmt.Errorf("answer: %w", err)
	}
	out.Answer, out.Sources = parseAnswer(answer, found)
	s.log.Info("question answered", "user", acc.ID, "model", model, "considered", len(found), "cited", len(out.Sources),
		"took", s.clock().Sub(start).Round(time.Millisecond).String())
	return out, nil
}

// Find returns the account's notes about a topic or question, the most relevant first, each
// with a passage showing why.
func (s *AskService) Find(ctx context.Context, acc *Account, query string, limit int) ([]FoundNote, error) {
	in := AskInput{Question: query}
	apiKey, model, err := s.ready(ctx, &in)
	if err != nil {
		return nil, err
	}
	if limit <= 0 || limit > maxFound {
		limit = maxFound
	}
	found, keywords, err := s.search(ctx, acc, apiKey, model, in, limit)
	if err != nil {
		return nil, err
	}
	out := make([]FoundNote, len(found))
	for i, c := range found {
		out[i] = FoundNote{Note: c.rec, Excerpt: excerpt(c.text, keywords, 400)}
	}
	return out, nil
}

// search picks the notes to read for a question: first the ones the model picked from the
// catalog, then the ones with the most of the words it named (and the question's own).
func (s *AskService) search(ctx context.Context, acc *Account, apiKey, model string, in AskInput, limit int) ([]*candidate, []string, error) {
	list, err := s.notes.List(ctx, acc, recording.ListFilter{})
	if err != nil {
		return nil, nil, err
	}
	var all []*candidate
	for _, r := range list {
		if r.IsBoard() {
			continue
		}
		all = append(all, &candidate{rec: r, text: noteText(r)})
	}
	if len(all) == 0 {
		return nil, nil, nil
	}
	slices.SortStableFunc(all, func(a, b *candidate) int { return noteDate(b.rec).Compare(noteDate(a.rec)) })
	catalog := all[:min(len(all), maxCatalogNotes)]

	var question strings.Builder
	for _, h := range in.History {
		fmt.Fprintf(&question, "Earlier question: %s\n", h.Question)
	}
	question.WriteString("Question: " + in.Question)
	system := `You find the notes that can answer a question. Below is a catalog of the user's notes, one per line: a reference such as "c12", the date, the kind of note, the title and the start of its text.
Reply with a JSON object with exactly these fields:
- "notes": the references of up to ` + strconv.Itoa(maxPicked) + ` notes most likely to answer the question (by what they are about, not only by the words), the most relevant first; an empty array if none fits.
- "keywords": up to ` + strconv.Itoa(maxKeywords) + ` words or short phrases likely to appear in the text of notes that answer the question: names, terms, synonyms, and the same words in English and German. Lower case.
Today is ` + s.today(ctx, acc) + `. Reply with the JSON object only.`
	answer, err := s.ai.ai.Complete(ctx, apiKey, openrouter.Request{Model: model, JSON: true, Messages: []openrouter.Message{
		{Role: "system", Content: system + "\n\nCatalog:\n" + catalogText(catalog)},
		{Role: "user", Content: question.String()},
	}})
	if err != nil {
		return nil, nil, fmt.Errorf("search notes: %w", err)
	}
	picked, keywords := parsePicks(answer, len(catalog))
	keywords = append(keywords, questionWords(in.Question)...)
	slices.Sort(keywords)
	keywords = slices.Compact(keywords)

	var picks []*candidate
	for _, i := range picked {
		picks = append(picks, catalog[i])
	}
	// The notes with the most keywords, besides the picked ones.
	for _, c := range all {
		c.score = 0
		lower := strings.ToLower(c.text)
		for _, k := range keywords {
			c.score += min(strings.Count(lower, k), 5) * (1 + utf8.RuneCountInString(k)/6)
		}
	}
	byScore := slices.Clone(all)
	sort.SliceStable(byScore, func(i, j int) bool { return byScore[i].score > byScore[j].score })
	var matched []*candidate
	for _, c := range byScore {
		if c.score == 0 || len(matched) == limit {
			break
		}
		if !slices.Contains(picks, c) {
			matched = append(matched, c)
		}
	}
	// The picked notes come first, but leave room for a few found by their words.
	room := 0
	if limit >= 4 {
		room = min(len(matched), max(2, limit/3))
	}
	out := picks[:min(len(picks), limit-room)]
	for _, c := range matched {
		if len(out) == limit {
			break
		}
		out = append(out, c)
	}
	return out, keywords, nil
}

// today is the account's current date, as the model is told.
func (s *AskService) today(ctx context.Context, acc *Account) string {
	return s.clock().In(s.notes.location(ctx, acc.ID)).Format("Monday, 2 January 2006")
}

// noteDate is when a note was recorded or made.
func noteDate(r *recording.Recording) time.Time {
	if r.RecordedAt != nil {
		return *r.RecordedAt
	}
	return r.CreatedAt
}

// noteKind names the kind of a note for the model.
func noteKind(r *recording.Recording) string {
	switch {
	case r.IsText():
		return "text note"
	case r.IsImage():
		return "photo"
	case r.IsDocument():
		return "document"
	default:
		return "recording"
	}
}

// noteText is the note's title and texts, for looking up words.
func noteText(r *recording.Recording) string {
	var b strings.Builder
	b.WriteString(noteTitle(r))
	if r.Summary != nil {
		b.WriteString("\n" + r.Summary.Markdown)
		for _, it := range r.Summary.ActionItems {
			b.WriteString("\n- " + it.Text)
		}
	}
	if r.Transcript != nil && !r.IsText() {
		b.WriteString("\n" + r.Transcript.Text)
	}
	return b.String()
}

// catalogText lists the notes for the model to pick from, shortening the snippets until
// the catalog fits its budget.
func catalogText(notes []*candidate) string {
	for snippet := catalogSnippet; ; snippet /= 2 {
		var b strings.Builder
		for i, c := range notes {
			r := c.rec
			fmt.Fprintf(&b, "c%d | %s | %s | %s", i, noteDate(r).Format("2006-01-02"), noteKind(r), oneLine(noteTitle(r), 120))
			if snippet >= 30 && r.Summary != nil {
				if s := oneLine(r.Summary.Markdown, snippet); s != "" {
					b.WriteString(" | " + s)
				}
			}
			b.WriteByte('\n')
		}
		if b.Len() <= maxCatalogChars || snippet < 30 {
			return b.String()
		}
	}
}

// oneLine collapses Markdown to one line of plain words, at most n characters.
func oneLine(s string, n int) string {
	s = strings.NewReplacer("#", "", "*", "", "_", "", "`", "", "|", "/", ">", "").Replace(s)
	return truncateRunes(strings.Join(strings.Fields(s), " "), n)
}

// parsePicks reads the catalog references and keywords of the model's answer.
func parsePicks(answer string, catalogSize int) (picked []int, keywords []string) {
	var out struct {
		Notes    []json.RawMessage `json:"notes"`
		Keywords []string          `json:"keywords"`
	}
	s := strings.TrimSpace(answer)
	if i, j := strings.Index(s, "{"), strings.LastIndex(s, "}"); i >= 0 && j > i {
		_ = json.Unmarshal([]byte(s[i:j+1]), &out)
	}
	for _, raw := range out.Notes {
		var ref string
		if json.Unmarshal(raw, &ref) != nil {
			var n int
			if json.Unmarshal(raw, &n) != nil {
				continue
			}
			ref = strconv.Itoa(n)
		}
		n, err := strconv.Atoi(strings.TrimPrefix(strings.TrimSpace(strings.ToLower(ref)), "c"))
		if err != nil || n < 0 || n >= catalogSize || slices.Contains(picked, n) {
			continue
		}
		picked = append(picked, n)
		if len(picked) == maxPicked {
			break
		}
	}
	for _, k := range out.Keywords {
		k = strings.ToLower(strings.Join(strings.Fields(k), " "))
		if utf8.RuneCountInString(k) >= 3 && utf8.RuneCountInString(k) <= 60 {
			keywords = append(keywords, k)
		}
		if len(keywords) == maxKeywords {
			break
		}
	}
	return picked, keywords
}

// questionWords are the longer words of a question, looked up besides the model's keywords.
func questionWords(q string) []string {
	var out []string
	for _, w := range strings.FieldsFunc(strings.ToLower(q), func(r rune) bool {
		return !(r == '-' || r == '\'' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r > 127 && r != '’' && r != '“' && r != '”' && r != '„')
	}) {
		if utf8.RuneCountInString(w) >= 5 {
			out = append(out, w)
		}
	}
	return out
}

// noteContext presents a note to answer a question from, in at most budget bytes: its
// summary or text, then its transcript (as excerpts around the keywords when it is long).
func noteContext(ref int, r *recording.Recording, keywords []string, budget int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "[%d] %s — %s, %s\n", ref, noteTitle(r), noteKind(r), noteDate(r).Format("Monday, 2 January 2006 15:04"))
	if r.Summary != nil && r.Summary.Markdown != "" {
		label := "Summary"
		if r.IsText() {
			label = "Text"
		}
		text := r.Summary.Markdown
		if len(text) > budget/2 && r.Transcript != nil && !r.IsText() {
			text = excerpt(text, keywords, budget/2)
		} else if len(text) > budget {
			text = excerpt(text, keywords, budget)
		}
		fmt.Fprintf(&b, "%s:\n%s\n", label, text)
	}
	if r.Transcript != nil && !r.IsText() && r.Transcript.Text != "" {
		label := "Transcript"
		if r.IsDocument() {
			label = "Text read from the pages"
		}
		if rest := budget - b.Len(); rest > 500 {
			fmt.Fprintf(&b, "%s:\n%s\n", label, excerpt(r.Transcript.Text, keywords, rest))
		}
	}
	b.WriteString("\n")
	return b.String()
}

// excerpt shortens text to about budget bytes: its start, then the passages around the
// keywords, each extended to whole lines.
func excerpt(text string, keywords []string, budget int) string {
	if len(text) <= budget {
		return text
	}
	lower := strings.ToLower(text)
	if len(lower) != len(text) {
		lower = text // offsets must match; rare characters change length when lowered
	}
	type span struct{ from, to int }
	var spans []span
	head := min(len(text), budget/4, 1500)
	spans = append(spans, span{0, head})
	for _, k := range keywords {
		for i, n := 0, 0; n < 8; n++ {
			j := strings.Index(lower[i:], k)
			if j < 0 {
				break
			}
			pos := i + j
			spans = append(spans, span{max(0, pos-excerptRadius), min(len(text), pos+len(k)+excerptRadius)})
			i = pos + len(k)
		}
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].from < spans[j].from })
	var merged []span
	for _, sp := range spans {
		// Whole lines.
		if i := strings.LastIndexByte(text[:sp.from], '\n'); i >= 0 && sp.from-i < 300 {
			sp.from = i + 1
		}
		if i := strings.IndexByte(text[sp.to:], '\n'); i >= 0 && i < 300 {
			sp.to += i
		}
		if n := len(merged); n > 0 && sp.from <= merged[n-1].to {
			merged[n-1].to = max(merged[n-1].to, sp.to)
		} else {
			merged = append(merged, sp)
		}
	}
	var b strings.Builder
	for _, sp := range merged {
		part := text[sp.from:sp.to]
		if b.Len()+len(part) > budget {
			part = part[:max(0, budget-b.Len())]
			for len(part) > 0 && !utf8.ValidString(part) {
				part = part[:len(part)-1]
			}
		}
		if part == "" {
			break
		}
		if b.Len() > 0 || sp.from > 0 {
			b.WriteString("\n…\n")
		}
		b.WriteString(part)
	}
	if merged[len(merged)-1].to < len(text) {
		b.WriteString("\n…")
	}
	return b.String()
}

// parseAnswer reads the model's answer and keeps the sources it cites, numbered as it cited
// them.
func parseAnswer(answer string, found []*candidate) (string, []AskSource) {
	var out struct {
		Answer  string `json:"answer"`
		Sources []struct {
			Ref   json.RawMessage `json:"ref"`
			Quote string          `json:"quote"`
			Time  string          `json:"time"`
		} `json:"sources"`
	}
	s := strings.TrimSpace(answer)
	if i, j := strings.Index(s, "{"), strings.LastIndex(s, "}"); i >= 0 && j > i && json.Unmarshal([]byte(s[i:j+1]), &out) == nil && out.Answer != "" {
		s = strings.TrimSpace(out.Answer)
	} else {
		s, out.Sources = strings.TrimSpace(stripFence(s)), nil
	}
	sources := []AskSource{}
	add := func(ref int) *AskSource {
		if ref < 1 || ref > len(found) {
			return nil
		}
		for i := range sources {
			if sources[i].Ref == ref {
				return &sources[i]
			}
		}
		r := found[ref-1].rec
		typ := string(r.Type)
		if typ == "" {
			typ = "audio"
		}
		sources = append(sources, AskSource{Ref: ref, ID: r.ID, Number: r.Number, Title: noteTitle(r), Type: typ, Date: noteDate(r)})
		return &sources[len(sources)-1]
	}
	for _, src := range out.Sources {
		ref, err := strconv.Atoi(strings.Trim(string(src.Ref), `"[] `))
		if err != nil || !strings.Contains(s, "["+strconv.Itoa(ref)+"]") {
			continue
		}
		if a := add(ref); a != nil && a.Quote == "" {
			a.Quote = truncateRunes(strings.Join(strings.Fields(src.Quote), " "), 300)
			if m := timestampPattern.FindStringSubmatch("[" + strings.Trim(src.Time, "[] ") + "]"); m != nil {
				h, _ := strconv.Atoi(m[1])
				min, _ := strconv.Atoi(m[2])
				sec, _ := strconv.Atoi(m[3])
				ms := (int64(h)*3600 + int64(min)*60 + int64(sec)) * 1000
				a.OffsetMs = &ms
			}
		}
	}
	// Citations without an entry in "sources" still name their note.
	for _, m := range citePattern.FindAllStringSubmatch(s, -1) {
		if ref, err := strconv.Atoi(m[1]); err == nil {
			add(ref)
		}
	}
	sort.Slice(sources, func(i, j int) bool { return sources[i].Ref < sources[j].Ref })
	return s, sources
}
