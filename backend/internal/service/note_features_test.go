package service

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/noteversion"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/repository/memory"
)

func (f *taskFixture) edit(t *testing.T, id, title, markdown string) *recording.Recording {
	t.Helper()
	rec, err := f.s.EditSummary(context.Background(), f.acc, id, SummaryEdit{Title: title, Markdown: markdown})
	if err != nil {
		t.Fatal(err)
	}
	return rec
}

func TestEditsKeepEarlierVersions(t *testing.T) {
	f := newTaskFixture(t)
	f.s.Versions = memory.NewNoteVersions()
	ctx := context.Background()
	note := f.note(t, TextNoteInput{SummaryEdit: SummaryEdit{Title: "Plan"}})

	// The empty text isn't kept.
	f.edit(t, note.ID, "Plan", "one")
	if list, _ := f.s.ListVersions(ctx, f.acc, note.ID); len(list) != 0 {
		t.Fatalf("versions of an empty text: %d", len(list))
	}
	// The first edit of a text keeps it; edits right after it go into the same version.
	f.now = f.now.Add(time.Minute)
	f.edit(t, note.ID, "Plan", "one two")
	f.now = f.now.Add(time.Minute)
	f.edit(t, note.ID, "Plan", "one two three")
	list, err := f.s.ListVersions(ctx, f.acc, note.ID)
	if err != nil || len(list) != 1 {
		t.Fatalf("versions: %d, %v", len(list), err)
	}
	if list[0].Markdown != "" || list[0].Reason != noteversion.ReasonEdit {
		t.Fatalf("listed version: %+v", list[0])
	}
	v, err := f.s.Version(ctx, f.acc, note.ID, list[0].ID)
	if err != nil || v.Markdown != "one" || v.Title != "Plan" {
		t.Fatalf("version: %+v, %v", v, err)
	}
	// Later edits keep the text again.
	f.now = f.now.Add(versionEvery)
	f.edit(t, note.ID, "Plan B", "four")
	if list, _ = f.s.ListVersions(ctx, f.acc, note.ID); len(list) != 2 {
		t.Fatalf("versions after a while: %d", len(list))
	}
	if newest, _ := f.s.Version(ctx, f.acc, note.ID, list[0].ID); newest.Markdown != "one two three" {
		t.Fatalf("newest version: %q", newest.Markdown)
	}

	// Restoring brings the text back and keeps the text it replaced, so it can be undone.
	rec, err := f.s.RestoreVersion(ctx, f.acc, note.ID, v.ID, nil)
	if err != nil || rec.Summary.Markdown != "one" || rec.Summary.Title != "Plan" {
		t.Fatalf("restore: %+v, %v", rec, err)
	}
	list, _ = f.s.ListVersions(ctx, f.acc, note.ID)
	if len(list) != 3 || list[0].Reason != noteversion.ReasonRestore || list[0].Title != "Plan B" {
		t.Fatalf("versions after restoring: %+v", list)
	}
	// A restore on an older revision is refused.
	old := rec.Revision - 1
	if _, err := f.s.RestoreVersion(ctx, f.acc, note.ID, list[0].ID, &old); !errors.Is(err, ErrChanged) {
		t.Fatalf("restore on an old revision: %v", err)
	}

	// Versions of other notes aren't found through this one.
	other := f.note(t, TextNoteInput{SummaryEdit: SummaryEdit{Title: "Other"}})
	if _, err := f.s.Version(ctx, f.acc, other.ID, v.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("version through another note: %v", err)
	}
	// Nor by other users.
	if _, err := f.s.ListVersions(ctx, &Account{ID: "u9"}, note.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("versions of someone else's note: %v", err)
	}

	// Deleting the note deletes its versions.
	if err := f.s.Delete(ctx, f.acc, note.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := f.s.Versions.List(ctx, note.ID); len(got) != 0 {
		t.Fatalf("versions after deleting the note: %d", len(got))
	}
}

func TestVersionsArePruned(t *testing.T) {
	f := newTaskFixture(t)
	f.s.Versions = memory.NewNoteVersions()
	note := f.note(t, TextNoteInput{SummaryEdit: SummaryEdit{Title: "Log", Markdown: "0"}})
	for i := 1; i <= maxVersions+5; i++ {
		f.now = f.now.Add(versionEvery)
		f.edit(t, note.ID, "Log", strings.Repeat("x", i))
	}
	list, _ := f.s.ListVersions(context.Background(), f.acc, note.ID)
	if len(list) != maxVersions {
		t.Fatalf("kept %d versions, want %d", len(list), maxVersions)
	}
}

func TestTemplates(t *testing.T) {
	f := newTaskFixture(t)
	ctx := context.Background()
	note := f.note(t, TextNoteInput{SummaryEdit: SummaryEdit{Title: "Meeting", Markdown: "## Agenda"}})
	rec, err := f.s.SetTemplate(ctx, f.acc, note.ID, true)
	if err != nil || !rec.Template {
		t.Fatalf("set template: %+v, %v", rec, err)
	}
	if rec, _ = f.s.Get(ctx, f.acc, note.ID); !rec.Template {
		t.Fatal("template flag not saved")
	}
	if rec, _ = f.s.SetTemplate(ctx, f.acc, note.ID, false); rec.Template {
		t.Fatal("template flag not cleared")
	}
	board, err := f.s.CreateBoard(ctx, f.acc, BoardInput{Title: "Board"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.SetTemplate(ctx, f.acc, board.ID, true); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("board as template: %v", err)
	}
}

func TestPublishedNotes(t *testing.T) {
	f := newTaskFixture(t)
	ctx := context.Background()
	note := f.note(t, TextNoteInput{SummaryEdit: SummaryEdit{Title: "Trip", Markdown: "Plans"}})
	png := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0}, 64)...)
	img, err := f.s.AddImage(ctx, f.acc, note.ID, bytes.NewReader(png))
	if err != nil {
		t.Fatal(err)
	}
	own := "/api/v1/recordings/" + note.ID + "/images/" + img
	f.edit(t, note.ID, "Trip", "Plans\n\n![map]("+own+"#w=300)\n\n![x](/api/v1/recordings/other/images/abc)")

	// Only the owner publishes.
	if _, err := f.s.Publish(ctx, &Account{ID: "u9"}, note.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("publish someone else's note: %v", err)
	}
	rec, err := f.s.Publish(ctx, f.acc, note.ID)
	if err != nil || rec.Public == nil || !publicToken.MatchString(rec.Public.Token) {
		t.Fatalf("publish: %+v, %v", rec.Public, err)
	}
	token := rec.Public.Token
	// Publishing again keeps the link.
	if again, _ := f.s.Publish(ctx, f.acc, note.ID); again.Public.Token != token {
		t.Fatal("publishing again made a new link")
	}

	pub, err := f.s.Published(ctx, token)
	if err != nil {
		t.Fatal(err)
	}
	if pub.Title != "Trip" || !strings.Contains(pub.Markdown, "![map](/api/v1/public/"+token+"/images/"+img+"#w=300)") ||
		!strings.Contains(pub.Markdown, "![x](#)") || strings.Contains(pub.Markdown, "/recordings/") {
		t.Fatalf("published note: %+v", pub)
	}
	if obj, err := f.s.PublishedImage(ctx, token, img); err != nil || obj.ContentType != "image/png" {
		t.Fatalf("published image: %+v, %v", obj, err)
	}
	for _, bad := range []string{"", "nope", strings.Repeat("0", 64)} {
		if _, err := f.s.Published(ctx, bad); !errors.Is(err, ErrNotFound) {
			t.Fatalf("token %q: %v", bad, err)
		}
	}

	// In the trash, the link doesn't work; restored, it works again.
	if _, err := f.s.Trash(ctx, f.acc, note.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Published(ctx, token); !errors.Is(err, ErrNotFound) {
		t.Fatalf("published note in the trash: %v", err)
	}
	if _, err := f.s.Restore(ctx, f.acc, note.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Published(ctx, token); err != nil {
		t.Fatalf("restored published note: %v", err)
	}

	// Taken down, the link stops working.
	if rec, err = f.s.Unpublish(ctx, f.acc, note.ID); err != nil || rec.Public != nil {
		t.Fatalf("unpublish: %+v, %v", rec, err)
	}
	if _, err := f.s.Published(ctx, token); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unpublished note: %v", err)
	}
}

func TestWrite(t *testing.T) {
	ai := &fakeAI{answer: "```markdown\n**Better** text\n```"}
	s, _ := newAI(t, ai)
	ctx := context.Background()
	acc := &Account{ID: "u1"}

	out, err := s.Write(ctx, acc, WriteInput{Action: "improve", Text: "bad text", Context: "The note."})
	if err != nil || out.Markdown != "**Better** text" || out.Model == "" {
		t.Fatalf("write: %+v, %v", out, err)
	}
	prompt := ai.requests[0].Messages[1].Content.(string)
	if !strings.Contains(prompt, "bad text") || !strings.Contains(prompt, "The note.") || !strings.Contains(prompt, "Improve") {
		t.Fatalf("prompt: %s", prompt)
	}

	if _, err := s.Write(ctx, acc, WriteInput{Action: "translate", Text: "Hallo", Language: "fr-FR"}); err != nil {
		t.Fatal(err)
	}
	if prompt = ai.requests[1].Messages[1].Content.(string); !strings.Contains(prompt, "into French") {
		t.Fatalf("translate prompt: %s", prompt)
	}
	if _, err := s.Write(ctx, acc, WriteInput{Action: "write", Instruction: "A packing list for a hike"}); err != nil {
		t.Fatal(err)
	}

	for _, bad := range []WriteInput{
		{Action: "dance", Text: "x"},
		{Action: "improve"},
		{Action: "translate", Text: "x", Language: "xx"},
		{Action: "custom", Text: "x"},
		{Action: "write"},
		{Action: "improve", Text: strings.Repeat("x", maxWriteText+1)},
	} {
		if _, err := s.Write(ctx, acc, bad); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%+v: %v", bad.Action, err)
		}
	}
}
