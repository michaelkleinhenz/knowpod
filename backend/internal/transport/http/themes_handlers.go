package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/service"
)

func (s *Server) handleListThemes(w http.ResponseWriter, r *http.Request) {
	list, err := s.themes.List(r.Context(), accountFrom(r.Context()))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleCreateTheme(w http.ResponseWriter, r *http.Request) {
	var in service.ThemeInput
	if !decode(w, r, &in) {
		return
	}
	t, err := s.themes.Create(r.Context(), accountFrom(r.Context()), in)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, t)
}

func (s *Server) handleUpdateTheme(w http.ResponseWriter, r *http.Request) {
	var in service.ThemeInput
	if !decode(w, r, &in) {
		return
	}
	t, err := s.themes.Update(r.Context(), accountFrom(r.Context()), chi.URLParam(r, "id"), in)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleDeleteTheme(w http.ResponseWriter, r *http.Request) {
	if err := s.themes.Delete(r.Context(), accountFrom(r.Context()), chi.URLParam(r, "id")); err != nil {
		s.writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleSummaryLanguages lists the languages a summary can be written in.
func (s *Server) handleSummaryLanguages(w http.ResponseWriter, r *http.Request) {
	tags := make([]string, 0, len(service.SummaryLanguages))
	for tag := range service.SummaryLanguages {
		tags = append(tags, tag)
	}
	sortStrings(tags)
	writeJSON(w, http.StatusOK, tags)
}
