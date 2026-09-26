package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/service"
)

func (s *Server) handleListLabels(w http.ResponseWriter, r *http.Request) {
	list, err := s.labels.List(r.Context(), accountFrom(r.Context()))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleCreateLabel(w http.ResponseWriter, r *http.Request) {
	var in service.LabelInput
	if !decode(w, r, &in) {
		return
	}
	l, err := s.labels.Create(r.Context(), accountFrom(r.Context()), in)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, l)
}

func (s *Server) handleUpdateLabel(w http.ResponseWriter, r *http.Request) {
	var in service.LabelInput
	if !decode(w, r, &in) {
		return
	}
	l, err := s.labels.Update(r.Context(), accountFrom(r.Context()), chi.URLParam(r, "id"), in)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, l)
}

func (s *Server) handleDeleteLabel(w http.ResponseWriter, r *http.Request) {
	if err := s.labels.Delete(r.Context(), accountFrom(r.Context()), chi.URLParam(r, "id")); err != nil {
		s.writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleSetNoteLabels replaces a note's labels with the IDs in {"labels": [...]}.
func (s *Server) handleSetNoteLabels(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Labels []string `json:"labels"`
	}
	if !decode(w, r, &in) {
		return
	}
	rec, err := s.actions.SetLabels(r.Context(), accountFrom(r.Context()), chi.URLParam(r, "id"), in.Labels)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// handleSetNoteDone checks or unchecks a task note with {"done": true|false}.
func (s *Server) handleSetNoteDone(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Done bool `json:"done"`
	}
	if !decode(w, r, &in) {
		return
	}
	rec, err := s.actions.SetDone(r.Context(), accountFrom(r.Context()), chi.URLParam(r, "id"), in.Done)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}
