package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
)

func TestTrashRestoreAndPurge(t *testing.T) {
	f := newTaskFixture(t)
	ctx := context.Background()
	head := f.note(t, TextNoteInput{SummaryEdit: SummaryEdit{Title: "Head"}})
	sub := f.note(t, TextNoteInput{SummaryEdit: SummaryEdit{Title: "Sub"}, ParentID: head.ID})
	task, err := f.s.SetDue(ctx, f.acc, sub.ID, &recording.Due{Date: "2026-09-28", Time: "09:00", Remind: ptr(0)})
	if err != nil || task.RemindAt == nil {
		t.Fatalf("due: %v %+v", err, task)
	}

	// Trashing hides the note from lists and drops its reminder; its sub-notes move up.
	trashed, err := f.s.Trash(ctx, f.acc, sub.ID)
	if err != nil || trashed.DeletedAt == nil || trashed.RemindAt != nil {
		t.Fatalf("trash: %v %+v", err, trashed)
	}
	list, _ := f.s.List(ctx, f.acc, recording.ListFilter{})
	if len(list) != 1 || list[0].ID != head.ID {
		t.Fatalf("list after trashing: %d", len(list))
	}
	if list, _ := f.s.List(ctx, f.acc, recording.ListFilter{Trash: recording.TrashOnly}); len(list) != 1 || list[0].ID != sub.ID {
		t.Fatalf("trash list: %d", len(list))
	}
	// Notes in the trash can't be parents.
	other := f.note(t, TextNoteInput{SummaryEdit: SummaryEdit{Title: "Other"}})
	if _, err := f.s.SetParent(ctx, f.acc, other.ID, sub.ID); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("parent in the trash: %v", err)
	}

	// Restoring brings the note and its reminder back; under its parent when that is still there.
	restored, err := f.s.Restore(ctx, f.acc, sub.ID)
	if err != nil || restored.DeletedAt != nil || restored.RemindAt == nil || restored.ParentID != head.ID {
		t.Fatalf("restore: %v %+v", err, restored)
	}
	// With its parent in the trash, it comes back at the top level.
	if _, err := f.s.Trash(ctx, f.acc, sub.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.s.Trash(ctx, f.acc, head.ID); err != nil {
		t.Fatal(err)
	}
	if restored, _ := f.s.Restore(ctx, f.acc, sub.ID); restored.ParentID != "" {
		t.Fatalf("restored under a note in the trash: %+v", restored.ParentID)
	}

	// Notes are deleted for good once they have been in the trash long enough.
	f.now = f.now.Add(recording.TrashRetention - time.Hour)
	if n, err := f.s.PurgeTrash(ctx); err != nil || n != 0 {
		t.Fatalf("early purge: %d %v", n, err)
	}
	f.now = f.now.Add(2 * time.Hour)
	if n, err := f.s.PurgeTrash(ctx); err != nil || n != 1 {
		t.Fatalf("purge: %d %v", n, err)
	}
	if _, err := f.s.Get(ctx, f.acc, head.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("purged note still there: %v", err)
	}

	// Emptying the trash deletes everything in it right away.
	if _, err := f.s.Trash(ctx, f.acc, other.ID); err != nil {
		t.Fatal(err)
	}
	if n, err := f.s.EmptyTrash(ctx, f.acc); err != nil || n != 1 {
		t.Fatalf("empty trash: %d %v", n, err)
	}
	if list, _ := f.s.List(ctx, f.acc, recording.ListFilter{Trash: recording.TrashAny}); len(list) != 1 || list[0].ID != sub.ID {
		t.Fatalf("left after emptying the trash: %d", len(list))
	}
}
