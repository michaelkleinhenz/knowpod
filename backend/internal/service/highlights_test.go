package service

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
)

func i64(v int64) *int64 { return &v }

func TestNormalizeHighlights(t *testing.T) {
	start := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)
	at := start.Add(90 * time.Second)
	got, err := normalizeHighlights([]HighlightInput{{OffsetMs: i64(5000)}, {At: &at}, {OffsetMs: i64(1000)}, {OffsetMs: i64(5000)}}, &start)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].OffsetMs != 1000 || got[1].OffsetMs != 5000 || got[2].OffsetMs != 90000 || got[2].At == nil {
		t.Fatalf("got %+v", got)
	}
	for name, tc := range map[string]struct {
		in    []HighlightInput
		start *time.Time
	}{
		"both":         {[]HighlightInput{{OffsetMs: i64(1), At: &at}}, &start},
		"neither":      {[]HighlightInput{{}}, &start},
		"negative":     {[]HighlightInput{{OffsetMs: i64(-1)}}, &start},
		"too far":      {[]HighlightInput{{OffsetMs: i64(25 * 3600 * 1000)}}, &start},
		"at, no start": {[]HighlightInput{{At: &at}}, nil},
		"before start": {[]HighlightInput{{At: ptr(start.Add(-time.Minute))}}, &start},
	} {
		if _, err := normalizeHighlights(tc.in, tc.start); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestUploadHighlights(t *testing.T) {
	ctx := context.Background()
	s, recs, _ := newUploads(t)
	wav := testWAV()
	in := CreateUploadInput{RecordingID: "rec-1", Size: int64(len(wav)), SHA256: sum(wav), Highlights: []HighlightInput{{OffsetMs: i64(2500)}}}
	up, _, err := s.Create(ctx, dev1, in)
	if err != nil || len(up.Recording.Highlights) != 1 {
		t.Fatalf("create: %+v, %v", up, err)
	}
	// A repeated create with highlights replaces them.
	in.Highlights = []HighlightInput{{OffsetMs: i64(100)}, {OffsetMs: i64(200)}}
	if _, _, err := s.Create(ctx, dev1, in); err != nil {
		t.Fatal(err)
	}
	if rec, _ := recs.Get(ctx, up.Recording.ID); len(rec.Highlights) != 2 {
		t.Fatalf("after repeated create: %+v", rec.Highlights)
	}
	// Later, the device sets them explicitly; other devices can't.
	if _, err := s.SetHighlights(ctx, dev2, up.Recording.ID, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("other device: %v", err)
	}
	if _, err := s.Append(ctx, dev1, up.Recording.ID, 0, bytes.NewReader(wav)); err != nil {
		t.Fatal(err)
	}
	rec, err := s.SetHighlights(ctx, dev1, up.Recording.ID, []HighlightInput{{OffsetMs: i64(750)}})
	if err != nil || len(rec.Highlights) != 1 || rec.Highlights[0].OffsetMs != 750 || rec.Status != recording.StatusReceived {
		t.Fatalf("set highlights: %+v, %v", rec, err)
	}
}

func TestShiftTimestamps(t *testing.T) {
	in := "[0:05] Speaker 1: hi\n[4:59] Speaker 2: bye\n[1:02:03] later"
	got := shiftTimestamps(in, 5*time.Minute)
	want := "[5:05] Speaker 1: hi\n[9:59] Speaker 2: bye\n[1:07:03] later"
	if got != want {
		t.Fatalf("got %q", got)
	}
	if shiftTimestamps("[0:30] a", 55*time.Minute+45*time.Second) != "[56:15] a" {
		t.Fatal("minute overflow")
	}
	if shiftTimestamps("[0:30] a", 59*time.Minute+45*time.Second) != "[1:00:15] a" {
		t.Fatal("hour overflow")
	}
	// Seconds without their leading zero are read, and written with it.
	if got := shiftTimestamps("[0:0] a\n[0:3] b\n[0:12] c", 0); got != "[0:00] a\n[0:03] b\n[0:12] c" {
		t.Fatalf("short seconds: got %q", got)
	}
}

func TestSummaryPromptWithHighlights(t *testing.T) {
	ctx := context.Background()
	ai := &fakeAI{answer: `{"title":"T","summary":"S"}`}
	s, _ := newAI(t, ai)
	rec := &recording.Recording{ID: "r1", OwnerID: "alice", Transcript: &recording.Transcript{Text: "[0:10] hi"},
		Highlights: []recording.Highlight{{OffsetMs: 45_000}, {OffsetMs: 3_725_000}}}
	if err := s.Summarize(ctx, rec); err != nil {
		t.Fatal(err)
	}
	system := ai.requests[0].Messages[0].Content.(string)
	if !strings.Contains(system, "0:45, 1:02:05") || !strings.Contains(system, "## Highlights") {
		t.Fatalf("system prompt: %q", system)
	}
	// Without highlights, no highlights section is requested.
	rec2 := &recording.Recording{ID: "r2", OwnerID: "alice", Transcript: &recording.Transcript{Text: "hi"}}
	_ = s.Summarize(ctx, rec2)
	if strings.Contains(ai.requests[1].Messages[0].Content.(string), "## Highlights") {
		t.Fatal("highlights section requested without highlights")
	}
}

func TestSummarizeNextSummary(t *testing.T) {
	ctx := context.Background()
	ai := &fakeAI{answer: `{"title":"T","summary":"S"}`}
	s, _ := newAI(t, ai)
	rec := &recording.Recording{ID: "r1", OwnerID: "alice", Transcript: &recording.Transcript{Text: "hi"},
		SummaryOptions: recording.SummaryOptions{Model: "openai/gpt-x"},
		NextSummary:    &recording.SummaryOptions{Model: "anthropic/old", ThemeID: "meeting"}}
	if err := s.Summarize(ctx, rec); err != nil {
		t.Fatal(err)
	}
	if ai.requests[0].Model != "anthropic/old" || rec.Summary.Model != "anthropic/old" || rec.Summary.ThemeID != "meeting" || rec.NextSummary != nil {
		t.Fatalf("summary %+v, next %+v", rec.Summary, rec.NextSummary)
	}
	// Used once: the next summary follows the note's options again.
	if err := s.Summarize(ctx, rec); err != nil {
		t.Fatal(err)
	}
	if ai.requests[1].Model != "openai/gpt-x" || rec.Summary.ThemeID != AutoTheme {
		t.Fatalf("second summary %+v", rec.Summary)
	}
}
