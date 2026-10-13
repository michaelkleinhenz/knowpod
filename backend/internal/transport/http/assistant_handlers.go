package http

import (
	"net/http"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/service"
)

// handleGetAssistant returns the user's own AI model settings.
func (s *Server) handleGetAssistant(w http.ResponseWriter, r *http.Request) {
	v, err := s.assistant.Settings(r.Context(), accountFrom(r.Context()))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// handleUpdateAssistant changes the user's own AI model settings.
func (s *Server) handleUpdateAssistant(w http.ResponseWriter, r *http.Request) {
	var in service.AssistantUpdate
	if !decode(w, r, &in) {
		return
	}
	v, err := s.assistant.UpdateSettings(r.Context(), accountFrom(r.Context()), in)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// handleAssistantEdit has the user's AI model work on a note; the note itself is unchanged.
func (s *Server) handleAssistantEdit(w http.ResponseWriter, r *http.Request) {
	var in service.AssistantEdit
	if !decode(w, r, &in) {
		return
	}
	out, err := s.assistant.Edit(r.Context(), accountFrom(r.Context()), in)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleAssistantCreate has the user's AI model write a new note; it is not saved.
func (s *Server) handleAssistantCreate(w http.ResponseWriter, r *http.Request) {
	var in service.AssistantCreate
	if !decode(w, r, &in) {
		return
	}
	out, err := s.assistant.Create(r.Context(), accountFrom(r.Context()), in)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
