package http

import (
	"net/http"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/service"
)

func (s *Server) handleGetBriefing(w http.ResponseWriter, r *http.Request) {
	out, err := s.briefings.Settings(r.Context(), accountFrom(r.Context()))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleUpdateBriefing(w http.ResponseWriter, r *http.Request) {
	var in service.BriefingSettings
	if !decode(w, r, &in) {
		return
	}
	out, err := s.briefings.UpdateSettings(r.Context(), accountFrom(r.Context()), in)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleMakeBriefing makes a daily briefing or weekly review right away.
func (s *Server) handleMakeBriefing(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Kind service.BriefingKind `json:"kind"`
	}
	if !decode(w, r, &in) {
		return
	}
	rec, err := s.briefings.MakeNow(r.Context(), accountFrom(r.Context()), in.Kind)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, rec)
}
