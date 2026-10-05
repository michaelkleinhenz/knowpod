package service

import (
	"context"
	"testing"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/repository/memory"
)

func TestDuplicateNoteWithSubNotes(t *testing.T) {
	f := newTaskFixture(t)
	ctx := context.Background()
	head := f.note(t, TextNoteInput{SummaryEdit: SummaryEdit{Title: "Head", Markdown: "text"}})
	f.note(t, TextNoteInput{SummaryEdit: SummaryEdit{Title: "Sub"}, ParentID: head.ID})

	cp, err := f.s.Duplicate(ctx, f.acc, head.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cp.ID == head.ID || cp.Summary.Title != "Head (copy)" || cp.Summary.Markdown != "text" {
		t.Fatalf("copy: %+v", cp.Summary)
	}
	subs, _ := f.s.List(ctx, f.acc, recording.ListFilter{ParentID: cp.ID})
	if len(subs) != 1 || subs[0].Summary.Title != "Sub" {
		t.Fatalf("sub-notes of the copy: %d", len(subs))
	}
	if _, err := f.s.Duplicate(ctx, &Account{ID: "other"}, head.ID); err == nil {
		t.Fatal("duplicated someone else's note")
	}
}

func TestDuplicateFolder(t *testing.T) {
	f := newTaskFixture(t)
	ctx := context.Background()
	fs := NewFolderService(memory.NewFolders(), f.recs)
	fs.Notes = f.s
	f.s.Folders = fs
	top, err := fs.Create(ctx, f.acc, FolderInput{Name: "Work"})
	if err != nil {
		t.Fatal(err)
	}
	inner, err := fs.Create(ctx, f.acc, FolderInput{Name: "Inner", ParentID: top.ID})
	if err != nil {
		t.Fatal(err)
	}
	f.note(t, TextNoteInput{SummaryEdit: SummaryEdit{Title: "A"}, FolderID: top.ID})
	f.note(t, TextNoteInput{SummaryEdit: SummaryEdit{Title: "B"}, FolderID: inner.ID})

	cp, err := fs.Duplicate(ctx, f.acc, top.ID)
	if err != nil || cp.Name != "Work (copy)" {
		t.Fatalf("duplicate: %v %+v", err, cp)
	}
	again, err := fs.Duplicate(ctx, f.acc, top.ID)
	if err != nil || again.Name != "Work (copy) 2" {
		t.Fatalf("second duplicate: %v %+v", err, again)
	}
	list, _ := fs.List(ctx, f.acc)
	var innerCopy string
	for _, x := range list {
		if x.ParentID == cp.ID && x.Name == "Inner" {
			innerCopy = x.ID
		}
	}
	if innerCopy == "" {
		t.Fatalf("inner folder not copied: %d folders", len(list))
	}
	all, _ := f.s.List(ctx, f.acc, recording.ListFilter{})
	in := map[string]string{}
	for _, n := range all {
		in[n.FolderID] = n.Summary.Title
	}
	if in[cp.ID] != "A" || in[innerCopy] != "B" {
		t.Fatalf("notes in the copy: %v", in)
	}
}
