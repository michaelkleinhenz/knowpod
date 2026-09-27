package service

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
)

// Limits of a board's setup.
const (
	maxBoardColumns    = 20
	maxColumnName      = 60
	maxBoardPlacements = 5000
)

// DefaultBoardColumns are the columns of a new board when none are given.
var DefaultBoardColumns = []string{"Todo", "In Progress", "Done"}

// BoardInput creates a board note: its title and optionally its setup. Without columns the
// board gets DefaultBoardColumns.
type BoardInput struct {
	Title string           `json:"title"`
	Board *recording.Board `json:"board,omitempty"`
}

// CreateBoard creates a board note for the account's user. Like a text note it needs no
// processing and keeps its title in the summary, so it is listed, renamed, labeled, moved
// and deleted like any other note.
func (s *RecordingService) CreateBoard(ctx context.Context, acc *Account, in BoardInput) (*recording.Recording, error) {
	if acc.ID == "" {
		return nil, errors.Join(ErrForbidden, errors.New("notes belong to a user; sign in"))
	}
	title, _, err := SummaryEdit{Title: in.Title}.clean()
	if err != nil {
		return nil, err
	}
	board := in.Board
	if board == nil {
		board = &recording.Board{}
	}
	if len(board.Columns) == 0 {
		for _, name := range DefaultBoardColumns {
			board.Columns = append(board.Columns, recording.BoardColumn{Name: name})
		}
	}
	if err := s.validBoard(ctx, acc.ID, board); err != nil {
		return nil, err
	}
	id := newID()
	now := s.clock().UTC()
	rec := &recording.Recording{
		ID: id, OwnerID: acc.ID, DeviceID: recording.BoardDeviceID(acc.ID), ClientID: id,
		Type: recording.TypeBoard, Status: recording.StatusSummarized,
		Summary:   &recording.Summary{Title: title, CreatedAt: now},
		Board:     board,
		NotBefore: now, CreatedAt: now, UpdatedAt: now,
	}
	if err := s.recs.Create(ctx, rec); err != nil {
		return nil, err
	}
	return rec, nil
}

// SetBoard replaces a board's setup: its scope, its columns (renamed, added, removed or
// reordered) and which notes are in which column.
func (s *RecordingService) SetBoard(ctx context.Context, acc *Account, id string, board recording.Board) (*recording.Recording, error) {
	rec, err := s.Get(ctx, acc, id)
	if err != nil {
		return nil, err
	}
	if !rec.IsBoard() {
		return nil, invalid("only boards have columns")
	}
	if err := s.validBoard(ctx, rec.OwnerID, &board); err != nil {
		return nil, err
	}
	rec.Board = &board
	return rec, s.save(ctx, rec)
}

// validBoard normalizes a board's setup and checks it: the scope must be one of the owner's
// folders or labels, and there must be 1-20 named columns. Columns without an ID get one;
// a note can be in one column only.
func (s *RecordingService) validBoard(ctx context.Context, ownerID string, b *recording.Board) error {
	b.Scope.ID = strings.TrimSpace(b.Scope.ID)
	switch b.Scope.Kind {
	case recording.ScopeNone:
		b.Scope.ID = ""
	case recording.ScopeFolder:
		if !s.Folders.Usable(ctx, ownerID, b.Scope.ID) {
			return invalid("unknown folder %q", b.Scope.ID)
		}
	case recording.ScopeLabel:
		if b.Scope.ID == "" || !s.Labels.Usable(ctx, ownerID, b.Scope.ID) {
			return invalid("unknown label %q", b.Scope.ID)
		}
	default:
		return invalid("a board's scope is a folder or a label")
	}

	if len(b.Columns) == 0 || len(b.Columns) > maxBoardColumns {
		return invalid("a board has 1-%d columns", maxBoardColumns)
	}
	columnIDs := map[string]bool{}
	notes := map[string]bool{}
	for i := range b.Columns {
		c := &b.Columns[i]
		c.ID, c.Name = strings.TrimSpace(c.ID), strings.TrimSpace(c.Name)
		if c.ID == "" {
			c.ID = newID()
		}
		switch {
		case len(c.ID) > 40:
			return invalid("column IDs are at most 40 characters")
		case columnIDs[c.ID]:
			return invalid("column %q is given twice", c.ID)
		case c.Name == "" || utf8.RuneCountInString(c.Name) > maxColumnName:
			return invalid("column names must be 1-%d characters", maxColumnName)
		}
		columnIDs[c.ID] = true
		for _, n := range c.Notes {
			if n == "" || len(n) > 64 || notes[n] {
				return invalid("note %q is placed twice or not at all", n)
			}
			notes[n] = true
		}
		if len(notes) > maxBoardPlacements {
			return invalid("a board holds at most %d notes", maxBoardPlacements)
		}
	}
	return nil
}
