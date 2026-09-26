package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/repository/memory"
	memstore "github.com/michaelkleinhenz/knowpod-service/backend/internal/storage/memory"
)

func TestThemes(t *testing.T) {
	ctx := context.Background()
	s := NewThemeService(memory.NewThemes())
	alice, bob := &Account{ID: "alice"}, &Account{ID: "bob"}

	list, _ := s.List(ctx, alice)
	if len(list) != len(builtInThemes) || list[0].ID != AutoTheme || !list[0].BuiltIn || !list[len(list)-1].BuiltIn {
		t.Fatalf("built-ins = %+v", list)
	}
	if _, err := s.Create(ctx, alice, ThemeInput{Name: " ", Instructions: "x"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("empty name: %v", err)
	}
	mine, err := s.Create(ctx, alice, ThemeInput{Name: "Sales call", Description: "Needs · Objections", Instructions: "## Needs\n## Objections"})
	if err != nil {
		t.Fatal(err)
	}
	if list, _ := s.List(ctx, alice); len(list) != len(builtInThemes)+1 {
		t.Fatalf("alice's themes: %d", len(list))
	}
	if list, _ := s.List(ctx, bob); len(list) != len(builtInThemes) {
		t.Fatal("bob sees alice's theme")
	}
	if _, err := s.Update(ctx, bob, mine.ID, ThemeInput{Name: "x", Instructions: "y"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("bob editing alice's theme: %v", err)
	}
	if v, err := s.Update(ctx, alice, mine.ID, ThemeInput{Name: "Sales call v2", Instructions: "## Needs"}); err != nil || v.Name != "Sales call v2" {
		t.Fatalf("update: %+v, %v", v, err)
	}

	if th := s.Resolve(ctx, "alice", mine.ID); th.ID != mine.ID {
		t.Fatalf("resolve own = %+v", th)
	}
	if th := s.Resolve(ctx, "bob", mine.ID); th.ID != AutoTheme {
		t.Fatalf("resolve other's = %+v", th)
	}
	if th := s.Resolve(ctx, "alice", "meeting"); th.Name != "Meeting Notes" {
		t.Fatalf("resolve built-in = %+v", th)
	}
	if !s.Accessible(ctx, alice, mine.ID) || s.Accessible(ctx, bob, mine.ID) || !s.Accessible(ctx, bob, "call") {
		t.Fatal("accessibility")
	}
	if err := s.Delete(ctx, alice, mine.ID); err != nil {
		t.Fatal(err)
	}
	if th := s.Resolve(ctx, "alice", mine.ID); th.ID != AutoTheme {
		t.Fatal("deleted theme doesn't fall back to auto")
	}
}

func TestSummarizeWithOptions(t *testing.T) {
	ctx := context.Background()
	ai := &fakeAI{answer: `{"title":"Verkaufsgespräch","summary":"## Bedarf"}`}
	s, _ := newAI(t, ai)
	mine, _ := s.themes.Create(ctx, &Account{ID: "alice"}, ThemeInput{Name: "Sales call", Instructions: "Use sections ## Needs and ## Objections."})

	rec := &recording.Recording{ID: "r1", OwnerID: "alice", Transcript: &recording.Transcript{Text: "Speaker 1: hello"},
		SummaryOptions: recording.SummaryOptions{Language: "de-DE", Model: "anthropic/claude-sonnet-5", ThemeID: mine.ID}}
	if err := s.Summarize(ctx, rec); err != nil {
		t.Fatal(err)
	}
	req := ai.requests[0]
	system := req.Messages[0].Content.(string)
	if req.Model != "anthropic/claude-sonnet-5" || !strings.Contains(system, "## Needs and ## Objections") || !strings.Contains(system, "in German") {
		t.Fatalf("request model=%s system=%q", req.Model, system)
	}
	if sm := rec.Summary; sm.ThemeID != mine.ID || sm.ThemeName != "Sales call" || sm.Language != "de-DE" || sm.Model != "anthropic/claude-sonnet-5" {
		t.Fatalf("summary = %+v", sm)
	}

	// Defaults: auto theme, transcript language, configured model.
	rec2 := &recording.Recording{ID: "r2", OwnerID: "alice", Transcript: &recording.Transcript{Text: "hi"}}
	_ = s.Summarize(ctx, rec2)
	system = ai.requests[1].Messages[0].Content.(string)
	if ai.requests[1].Model != "google/gemini-2.5-flash" || !strings.Contains(system, "language of the transcript") || rec2.Summary.ThemeID != AutoTheme {
		t.Fatalf("defaults: model=%s summary=%+v", ai.requests[1].Model, rec2.Summary)
	}
}

func TestResummarizeOptions(t *testing.T) {
	ctx := context.Background()
	recs := memory.NewRecordings()
	themes := NewThemeService(memory.NewThemes())
	spool, _ := NewSpool(t.TempDir())
	s := NewRecordingService(recs, memstore.New(), spool, themes)
	alice := &Account{ID: "alice"}
	bobTheme, _ := themes.Create(ctx, &Account{ID: "bob"}, ThemeInput{Name: "Bob's", Instructions: "x"})
	_ = recs.Create(ctx, &recording.Recording{ID: "r1", OwnerID: "alice", DeviceID: "d", ClientID: "c",
		Status: recording.StatusSummarized, Transcript: &recording.Transcript{Text: "t"}})

	for name, o := range map[string]recording.SummaryOptions{
		"language": {Language: "xx-YY"},
		"model":    {Model: "not a model"},
		"theme":    {ThemeID: bobTheme.ID},
	} {
		if _, err := s.Resummarize(ctx, alice, "r1", &o); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%s: %v", name, err)
		}
	}
	got, err := s.Resummarize(ctx, alice, "r1", &recording.SummaryOptions{Language: "auto", ThemeID: "meeting", Model: "x/y"})
	if err != nil || got.Status != recording.StatusTranscribed || got.SummaryOptions != (recording.SummaryOptions{ThemeID: "meeting", Model: "x/y"}) {
		t.Fatalf("resummarize: %+v, %v", got, err)
	}
	// nil keeps the options.
	got, _ = s.Resummarize(ctx, alice, "r1", nil)
	if got.SummaryOptions.ThemeID != "meeting" {
		t.Fatalf("options lost: %+v", got.SummaryOptions)
	}
}
