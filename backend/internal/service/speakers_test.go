package service

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
)

const speakerTranscript = "[0:01] Speaker 1: Hi, I'm Anna.\n[0:04] Speaker 2: Hello Anna, Ben here.\n\n[0:09] Speaker 1: Let's talk about Speaker 10.\n[0:15] Speaker 10: That's me."

func TestSpeakerLabels(t *testing.T) {
	got := SpeakerLabels(speakerTranscript + "\nno label here\nhttps://example.com: a link")
	if want := []string{"Speaker 1", "Speaker 2", "Speaker 10"}; !slices.Equal(got, want) {
		t.Fatalf("labels = %q", got)
	}
}

func TestReplaceName(t *testing.T) {
	for _, tc := range []struct{ text, from, to, want string }{
		{"Speaker 1 and Speaker 10 agreed.", "Speaker 1", "Anna", "Anna and Speaker 10 agreed."},
		{"**Speaker 1:** yes", "Speaker 1", "Anna", "**Anna:** yes"},
		{"Anna and Annabel", "Anna", "Anna Weber", "Anna Weber and Annabel"},
		{"Anna Weber said", "Anna", "Anna Weber", "Anna Weber said"},
		{"nothing", "Speaker 1", "Anna", "nothing"},
	} {
		if got := replaceName(tc.text, tc.from, tc.to); got != tc.want {
			t.Errorf("replaceName(%q, %q, %q) = %q, want %q", tc.text, tc.from, tc.to, got, tc.want)
		}
	}
}

func TestRenameSpeaker(t *testing.T) {
	f := newTaskFixture(t)
	ctx := context.Background()
	rec := &recording.Recording{
		ID: "r1", OwnerID: "u1", DeviceID: "d", ClientID: "r1", Status: recording.StatusSummarized,
		Transcript: &recording.Transcript{Text: speakerTranscript},
		Summary: &recording.Summary{Title: "Intro", Markdown: "Speaker 1 introduces herself to Speaker 2.",
			ActionItems: []recording.ActionItem{{ID: "a", Text: "Speaker 1 sends the notes", Owner: "Speaker 1"}},
			Speakers:    []recording.SpeakerName{{Label: "Speaker 1", Name: "Anna"}, {Label: "Speaker 2", Name: "Ben"}}},
	}
	if err := f.recs.Create(ctx, rec); err != nil {
		t.Fatal(err)
	}

	got, err := f.s.RenameSpeaker(ctx, f.acc, "r1", SpeakerRename{From: "Speaker 1", To: "  Anna  "})
	if err != nil {
		t.Fatal(err)
	}
	want := "[0:01] Anna: Hi, I'm Anna.\n[0:04] Speaker 2: Hello Anna, Ben here.\n\n[0:09] Anna: Let's talk about Speaker 10.\n[0:15] Speaker 10: That's me."
	if got.Transcript.Text != want {
		t.Errorf("transcript = %q", got.Transcript.Text)
	}
	if got.Summary.Markdown != "Anna introduces herself to Speaker 2." {
		t.Errorf("summary = %q", got.Summary.Markdown)
	}
	if it := got.Summary.ActionItems[0]; it.Text != "Anna sends the notes" || it.Owner != "Anna" {
		t.Errorf("action item = %+v", it)
	}
	if len(got.Summary.Speakers) != 1 || got.Summary.Speakers[0].Label != "Speaker 2" {
		t.Errorf("suggestions = %+v", got.Summary.Speakers)
	}
	if got.Revision != 1 {
		t.Errorf("revision = %d", got.Revision)
	}

	// Merging a speaker into another.
	if got, err = f.s.RenameSpeaker(ctx, f.acc, "r1", SpeakerRename{From: "Speaker 10", To: "Anna"}); err != nil {
		t.Fatal(err)
	}
	if labels := SpeakerLabels(got.Transcript.Text); !slices.Equal(labels, []string{"Anna", "Speaker 2"}) {
		t.Errorf("labels after merging = %q", labels)
	}

	for name, in := range map[string]SpeakerRename{
		"unknown speaker": {From: "Speaker 7", To: "Carl"},
		"empty name":      {From: "Speaker 2", To: " "},
		"colon":           {From: "Speaker 2", To: "Ben: the boss"},
	} {
		if _, err := f.s.RenameSpeaker(ctx, f.acc, "r1", in); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	text := f.note(t, TextNoteInput{})
	if _, err := f.s.RenameSpeaker(ctx, f.acc, text.ID, SpeakerRename{From: "A", To: "B"}); !errors.Is(err, ErrNotReady) {
		t.Errorf("text note: err = %v", err)
	}
	if _, err := f.s.RenameSpeaker(ctx, &Account{ID: "someone"}, "r1", SpeakerRename{From: "Anna", To: "B"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("other user: err = %v", err)
	}
}

func TestParseSpeakers(t *testing.T) {
	answer := `{"title":"T","summary":"S","actionItems":[],"speakers":[
		{"label":"Speaker 1","name":"Anna"},
		{"label":"Speaker 1","name":"Anne"},
		{"label":"Speaker 3","name":"Nobody"},
		{"label":"Speaker 2","name":"Speaker 2"},
		{"label":"Speaker 10","name":"Carl: boss"}]}`
	got := parseSpeakers(answer, speakerTranscript)
	if len(got) != 1 || got[0] != (recording.SpeakerName{Label: "Speaker 1", Name: "Anna"}) {
		t.Fatalf("speakers = %+v", got)
	}
	if got := parseSpeakers("not json", speakerTranscript); got != nil {
		t.Fatalf("speakers of a non-JSON answer = %+v", got)
	}
}
