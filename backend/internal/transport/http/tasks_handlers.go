package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/service"
)

// handleSetNoteDue sets or clears (null) when a task is due, how it repeats and when to
// remind.
func (s *Server) handleSetNoteDue(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Due *recording.Due `json:"due"`
	}
	if !decode(w, r, &in) {
		return
	}
	rec, err := s.actions.SetDue(r.Context(), accountFrom(r.Context()), chi.URLParam(r, "id"), in.Due)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// handleSetNotePriority ranks a task.
func (s *Server) handleSetNotePriority(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Priority recording.Priority `json:"priority"`
	}
	if !decode(w, r, &in) {
		return
	}
	rec, err := s.actions.SetPriority(r.Context(), accountFrom(r.Context()), chi.URLParam(r, "id"), in.Priority)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// handleSetNoteAssignee assigns a task to the note's owner or a user it is shared with.
func (s *Server) handleSetNoteAssignee(w http.ResponseWriter, r *http.Request) {
	var in struct {
		AssigneeID string `json:"assigneeId"`
	}
	if !decode(w, r, &in) {
		return
	}
	rec, err := s.actions.SetAssignee(r.Context(), accountFrom(r.Context()), chi.URLParam(r, "id"), in.AssigneeID)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// actionItemTaskResult is the task made from an action item and the note it came from.
type actionItemTaskResult struct {
	Task *recording.Recording `json:"task"`
	Note *recording.Recording `json:"note"`
}

// handleCreateActionItemTask turns an action item into a task under its note. The body is
// optional.
func (s *Server) handleCreateActionItemTask(w http.ResponseWriter, r *http.Request) {
	var in service.ActionItemTask
	if r.ContentLength != 0 && !decode(w, r, &in) {
		return
	}
	task, note, err := s.actions.CreateActionItemTask(r.Context(), accountFrom(r.Context()), chi.URLParam(r, "id"), chi.URLParam(r, "itemId"), in)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, actionItemTaskResult{Task: task, Note: note})
}

// handleDismissActionItem hides an action item, or shows it again.
func (s *Server) handleDismissActionItem(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Dismissed bool `json:"dismissed"`
	}
	if !decode(w, r, &in) {
		return
	}
	rec, err := s.actions.DismissActionItem(r.Context(), accountFrom(r.Context()), chi.URLParam(r, "id"), chi.URLParam(r, "itemId"), in.Dismissed)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}
