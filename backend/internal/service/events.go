package service

import (
	"context"
	"errors"
	"sync"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/ports"
)

// hub hands messages to the live connections (server-sent event streams) of users.
type hub[T any] struct {
	mu sync.Mutex
	// listeners are the live connections per user; closed says Shutdown ran.
	listeners map[string]map[chan T]struct{}
	closed    bool
}

func newHub[T any]() *hub[T] { return &hub[T]{listeners: map[string]map[chan T]struct{}{}} }

// listen makes a live connection of the user receive messages until stop is called or the
// hub shuts down, which closes the channel. A user with too many connections loses one of
// the others (usually a stale one of an app that went away).
func (h *hub[T]) listen(userID string) (msgs <-chan T, stop func(), err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, nil, errors.Join(ErrNotReady, errors.New("the server is shutting down"))
	}
	ls := h.listeners[userID]
	if ls == nil {
		ls = map[chan T]struct{}{}
		h.listeners[userID] = ls
	}
	for len(ls) >= maxListeners {
		for c := range ls {
			delete(ls, c)
			close(c)
			break
		}
	}
	c := make(chan T, 16)
	ls[c] = struct{}{}
	return c, func() { h.unlisten(userID, c) }, nil
}

func (h *hub[T]) unlisten(userID string, c chan T) {
	h.mu.Lock()
	defer h.mu.Unlock()
	ls := h.listeners[userID]
	if _, ok := ls[c]; !ok {
		return
	}
	delete(ls, c)
	close(c)
	if len(ls) == 0 {
		delete(h.listeners, userID)
	}
}

// shutdown ends all live connections.
func (h *hub[T]) shutdown() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
	for id, ls := range h.listeners {
		for c := range ls {
			close(c)
		}
		delete(h.listeners, id)
	}
}

// count says how many live connections the user has.
func (h *hub[T]) count(userID string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.listeners[userID])
}

// send hands a message to the user's live connections and returns how many took it. A
// connection that is too far behind misses it rather than holding up the others.
func (h *hub[T]) send(userID string, m T) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for c := range h.listeners[userID] {
		select {
		case c <- m:
			n++
		default:
		}
	}
	return n
}

// Kinds of NoteEvent.
const (
	// NoteChanged: the note was created, changed or deleted, or its sharing with the user
	// changed. The app loads it again (it is gone when that fails).
	NoteChanged = "note"
	// NotesReload: many notes changed at once; the app loads its list again.
	NotesReload = "reload"
	// FoldersChanged: folders the user sees were made, renamed, moved, ordered or deleted;
	// the app loads its folders again.
	FoldersChanged = "folders"
)

// NoteEvent tells a user's open apps that notes they see changed, so that what someone
// else (or the processing) did shows up right away.
type NoteEvent struct {
	Type string `json:"type"`
	ID   string `json:"id,omitempty"`
	// Version is the note's version after the change (0 when unknown); an app that has
	// this version already can skip loading it.
	Version int64 `json:"version,omitempty"`
}

// NoteEvents delivers NoteEvents to the users' live connections.
type NoteEvents struct {
	hub *hub[NoteEvent]
	// OwnerChanged, when set, is called with the owner of every changed note (or of many
	// notes at once), e.g. to send changed notes elsewhere. Set it before notes change.
	OwnerChanged func(ownerID string)
}

// NewNoteEvents builds the event hub.
func NewNoteEvents() *NoteEvents { return &NoteEvents{hub: newHub[NoteEvent]()} }

// Listen makes a live connection receive the account's note events until stop is called or
// the hub shuts down, which closes the channel.
func (e *NoteEvents) Listen(acc *Account) (<-chan NoteEvent, func(), error) {
	if acc.ID == "" {
		return nil, nil, errors.Join(ErrForbidden, errors.New("notes belong to a user; sign in"))
	}
	return e.hub.listen(acc.ID)
}

// Publish sends an event to the users.
func (e *NoteEvents) Publish(userIDs []string, ev NoteEvent) {
	for _, id := range userIDs {
		if id != "" {
			e.hub.send(id, ev)
		}
	}
}

// Shutdown ends all live connections.
func (e *NoteEvents) Shutdown() { e.hub.shutdown() }

// Watch wraps a recording repository so that every change of a note is published to
// everyone who sees it: its owner and its members. Changes made by any part of the server
// (people, the processing workers, imports) go through the repository, so none is missed.
func (e *NoteEvents) Watch(recs ports.RecordingRepository) ports.RecordingRepository {
	return &watchedRecordings{RecordingRepository: recs, events: e}
}

type watchedRecordings struct {
	ports.RecordingRepository
	events *NoteEvents
}

func (w *watchedRecordings) changed(rec *recording.Recording) {
	w.events.Publish(rec.Audience(), NoteEvent{Type: NoteChanged, ID: rec.ID, Version: rec.Version})
	w.ownerChanged(rec.OwnerID)
}

func (w *watchedRecordings) ownerChanged(ownerID string) {
	if f := w.events.OwnerChanged; f != nil {
		f(ownerID)
	}
}

// changedID publishes a change of a note that was made without reading it.
func (w *watchedRecordings) changedID(ctx context.Context, id string) {
	if rec, err := w.RecordingRepository.Get(ctx, id); err == nil {
		w.changed(rec)
	}
}

func (w *watchedRecordings) reload(userID string) {
	w.events.Publish([]string{userID}, NoteEvent{Type: NotesReload})
	w.ownerChanged(userID)
}

func (w *watchedRecordings) Create(ctx context.Context, r *recording.Recording) error {
	err := w.RecordingRepository.Create(ctx, r)
	if err == nil {
		w.changed(r)
	}
	return err
}

func (w *watchedRecordings) Update(ctx context.Context, r *recording.Recording) error {
	err := w.RecordingRepository.Update(ctx, r)
	if err == nil {
		w.changed(r)
	}
	return err
}

func (w *watchedRecordings) Delete(ctx context.Context, id string) error {
	rec, gerr := w.RecordingRepository.Get(ctx, id)
	err := w.RecordingRepository.Delete(ctx, id)
	if err == nil && gerr == nil {
		w.events.Publish(rec.Audience(), NoteEvent{Type: NoteChanged, ID: id})
		w.ownerChanged(rec.OwnerID)
	}
	return err
}

func (w *watchedRecordings) MoveSubNotes(ctx context.Context, ownerID, from, toParent, toFolder string) error {
	moved, lerr := w.RecordingRepository.List(ctx, recording.ListFilter{OwnerID: ownerID, ParentID: from, Trash: recording.TrashAny, Brief: true})
	err := w.RecordingRepository.MoveSubNotes(ctx, ownerID, from, toParent, toFolder)
	if err == nil && lerr == nil {
		for _, r := range moved {
			w.changedID(ctx, r.ID)
		}
	}
	return err
}

func (w *watchedRecordings) MoveFolder(ctx context.Context, ownerID, from, to string) error {
	err := w.RecordingRepository.MoveFolder(ctx, ownerID, from, to)
	if err == nil {
		w.reload(ownerID)
	}
	return err
}

func (w *watchedRecordings) RemoveLabel(ctx context.Context, ownerID, labelID string) error {
	err := w.RecordingRepository.RemoveLabel(ctx, ownerID, labelID)
	if err == nil {
		w.reload(ownerID)
	}
	return err
}

func (w *watchedRecordings) ClearBoardScope(ctx context.Context, ownerID string, scope recording.BoardScope) error {
	err := w.RecordingRepository.ClearBoardScope(ctx, ownerID, scope)
	if err == nil {
		w.reload(ownerID)
	}
	return err
}

func (w *watchedRecordings) AddTrackedSeconds(ctx context.Context, id string, seconds int64) error {
	err := w.RecordingRepository.AddTrackedSeconds(ctx, id, seconds)
	if err == nil {
		w.changedID(ctx, id)
	}
	return err
}
