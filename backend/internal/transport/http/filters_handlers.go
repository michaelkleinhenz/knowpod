package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/service"
)

func (s *Server) handleListFilters(w http.ResponseWriter, r *http.Request) {
	list, err := s.filters.List(r.Context(), accountFrom(r.Context()))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleCreateFilter(w http.ResponseWriter, r *http.Request) {
	var in service.FilterInput
	if !decode(w, r, &in) {
		return
	}
	f, err := s.filters.Create(r.Context(), accountFrom(r.Context()), in)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, f)
}

func (s *Server) handleUpdateFilter(w http.ResponseWriter, r *http.Request) {
	var in service.FilterInput
	if !decode(w, r, &in) {
		return
	}
	f, err := s.filters.Update(r.Context(), accountFrom(r.Context()), chi.URLParam(r, "id"), in)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, f)
}

func (s *Server) handleDeleteFilter(w http.ResponseWriter, r *http.Request) {
	if err := s.filters.Delete(r.Context(), accountFrom(r.Context()), chi.URLParam(r, "id")); err != nil {
		s.writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
