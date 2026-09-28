package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/openrouter"
)

// scriptedAI answers the requests in turn with the given answers.
type scriptedAI struct {
	answers  []string
	requests []openrouter.Request
}

func (f *scriptedAI) Complete(_ context.Context, apiKey string, r openrouter.Request) (string, error) {
	if apiKey != "sk-test" {
		return "", errors.New("wrong key")
	}
	f.requests = append(f.requests, r)
	if len(f.answers) == 0 {
		return "", errors.New("no more answers")
	}
	a := f.answers[0]
	f.answers = f.answers[1:]
	return a, nil
}

func (f *scriptedAI) Models(context.Context) ([]openrouter.Model, error) { return nil, nil }

// askFixture holds a user's notes: #1 a recording about the budget (the word only in its
// transcript), #2 a text note about holidays, #3 a board, and a note of someone else.
type askFixture struct {
	*taskFixture
	ai  *scriptedAI
	ask *AskService
}

func newAskFixture(t *testing.T, answers ...string) *askFixture {
	t.Helper()
	f := newTaskFixture(t)
	ctx := context.Background()
	ai := &scriptedAI{answers: answers}
	aiSvc, _ := newAI(t, &fakeAI{})
	aiSvc.ai = ai
	day := time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC)
	for _, r := range []*recording.Recording{
		{ID: "rec", Number: 1, OwnerID: "u1", Status: recording.StatusSummarized, CreatedAt: day,
			Audio:      &recording.Object{Key: "a.flac", ContentType: "audio/flac"},
			Transcript: &recording.Transcript{Text: "[0:05] Speaker 1: Hello.\n[12:30] Anna: The Haushalt for Q3 is 40k, not 50k."},
			Summary:    &recording.Summary{Title: "Team meeting", Markdown: "We talked about hiring."}},
		{ID: "text", Number: 2, OwnerID: "u1", Type: recording.TypeText, Status: recording.StatusSummarized, CreatedAt: day.Add(time.Hour),
			Summary: &recording.Summary{Title: "Holidays", Markdown: "Italy in October."}},
		{ID: "board", Number: 3, OwnerID: "u1", Type: recording.TypeBoard, Status: recording.StatusSummarized, CreatedAt: day,
			Summary: &recording.Summary{Title: "Budget board"}},
		{ID: "other", Number: 1, OwnerID: "u2", Type: recording.TypeText, Status: recording.StatusSummarized, CreatedAt: day,
			Summary: &recording.Summary{Title: "Budget secrets", Markdown: "The budget is secret."}},
	} {
		r.DeviceID, r.ClientID = "d", r.ID
		if err := f.recs.Create(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	s := NewAskService(aiSvc, f.s, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.clock = func() time.Time { return f.now }
	return &askFixture{taskFixture: f, ai: ai, ask: s}
}

func TestAskFindsNotesByTheModelsPicksAndKeywords(t *testing.T) {
	f := newAskFixture(t,
		// The model picks the holiday note from the catalog and names "haushalt", which is
		// only in the recording's transcript.
		`{"notes":["c0","c9","x"],"keywords":["Haushalt","budget"]}`,
		"```json\n"+`{"answer":"After the holidays [1], the Q3 budget is 40k [2]. [7]","sources":[{"ref":1,"quote":"Italy","time":"1:00"},{"ref":"2","quote":"The Haushalt for Q3 is 40k","time":"12:30"},{"ref":5,"quote":"none","time":""}]}`+"\n```",
	)
	out, err := f.ask.Ask(context.Background(), f.acc, AskInput{Question: "What is the budget for Q3?",
		History: []AskTurn{{Question: "Earlier?", Answer: "Yes."}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(f.ai.requests) != 2 {
		t.Fatalf("requests = %d", len(f.ai.requests))
	}
	catalog := f.ai.requests[0].Messages[0].Content.(string)
	if !strings.Contains(catalog, "c0 | 2026-09-20 | text note | Holidays | Italy in October.") || !strings.Contains(catalog, "c1 | 2026-09-20 | recording | Team meeting") {
		t.Errorf("catalog:\n%s", catalog)
	}
	if strings.Contains(catalog, "Budget board") || strings.Contains(catalog, "secret") {
		t.Errorf("the catalog lists boards or other users' notes:\n%s", catalog)
	}
	if q := f.ai.requests[0].Messages[1].Content.(string); !strings.Contains(q, "Earlier question: Earlier?") {
		t.Errorf("search question = %q", q)
	}
	// The picked note comes first, then the one found by its words.
	context := f.ai.requests[1].Messages[0].Content.(string)
	if i, j := strings.Index(context, "[1] Holidays"), strings.Index(context, "[2] Team meeting"); i < 0 || j < i {
		t.Errorf("answer context:\n%s", context)
	}
	if msgs := f.ai.requests[1].Messages; len(msgs) != 4 || msgs[1].Content != "Earlier?" || msgs[2].Role != "assistant" {
		t.Errorf("history not sent: %+v", msgs)
	}

	if out.Answer != "After the holidays [1], the Q3 budget is 40k [2]. [7]" {
		t.Errorf("answer = %q", out.Answer)
	}
	if len(out.Sources) != 2 {
		t.Fatalf("sources = %+v", out.Sources)
	}
	holidays, meeting := out.Sources[0], out.Sources[1]
	// Only recordings get a moment to play from.
	if holidays.Ref != 1 || holidays.ID != "text" || holidays.Type != "text" || holidays.Quote != "Italy" || holidays.OffsetMs != nil {
		t.Errorf("source 1 = %+v", holidays)
	}
	if meeting.Ref != 2 || meeting.ID != "rec" || meeting.Number != 1 || meeting.Type != "audio" || meeting.Quote != "The Haushalt for Q3 is 40k" || meeting.OffsetMs == nil || *meeting.OffsetMs != 750_000 {
		t.Errorf("source 2 = %+v", meeting)
	}
}

func TestAskChecksTheQuestionAndTheSetup(t *testing.T) {
	f := newAskFixture(t)
	if _, err := f.ask.Ask(context.Background(), f.acc, AskInput{Question: "  "}); !errors.Is(err, ErrInvalidInput) {
		t.Errorf("empty question: %v", err)
	}
	empty := ""
	if _, err := f.ask.ai.UpdateSettings(context.Background(), OpenRouterUpdate{APIKey: &empty}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.ask.Ask(context.Background(), f.acc, AskInput{Question: "Why?"}); !errors.Is(err, ErrNotReady) {
		t.Errorf("without AI: %v", err)
	}
}

func TestAskWithoutNotes(t *testing.T) {
	f := newAskFixture(t)
	out, err := f.ask.Ask(context.Background(), &Account{ID: "nobody"}, AskInput{Question: "Anything?"})
	if err != nil || out.Answer != "" || len(out.Sources) != 0 || len(f.ai.requests) != 0 {
		t.Fatalf("out = %+v, err = %v, requests = %d", out, err, len(f.ai.requests))
	}
}

func TestFindNotes(t *testing.T) {
	f := newAskFixture(t, `{"notes":[],"keywords":["haushalt"]}`)
	found, err := f.ask.Find(context.Background(), f.acc, "third quarter money", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].Note.ID != "rec" || !strings.Contains(found[0].Excerpt, "Q3") {
		t.Fatalf("found = %+v", found)
	}
}

func TestExcerpt(t *testing.T) {
	text := strings.Repeat("filler line\n", 500) + "the secret word\n" + strings.Repeat("more filler\n", 500)
	got := excerpt(text, []string{"secret"}, 2500)
	if !strings.HasPrefix(got, "filler line") || !strings.Contains(got, "the secret word") || len(got) > 2600 {
		t.Fatalf("excerpt (%d bytes):\n%s", len(got), got)
	}
	if got := excerpt("short", []string{"x"}, 100); got != "short" {
		t.Fatalf("short text = %q", got)
	}
}
