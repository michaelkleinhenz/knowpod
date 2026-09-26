package http

import (
	"net/http"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/service"
)

func (s *Server) handleGetOpenRouterSettings(w http.ResponseWriter, r *http.Request) {
	v, err := s.ai.Settings(r.Context())
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handleUpdateOpenRouterSettings(w http.ResponseWriter, r *http.Request) {
	var u service.OpenRouterUpdate
	if !decode(w, r, &u) {
		return
	}
	v, err := s.ai.UpdateSettings(r.Context(), u)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handleOpenRouterModels(w http.ResponseWriter, r *http.Request) {
	models, err := s.ai.Models(r.Context())
	if err != nil {
		s.log.Warn("listing OpenRouter models failed", "err", err)
		writeJSON(w, http.StatusBadGateway, errResponse{Error: "could not load the model list from OpenRouter"})
		return
	}
	writeJSON(w, http.StatusOK, models)
}

// handleAIStatus tells any signed-in user whether recordings are being transcribed and
// summarized (the settings themselves are for administrators).
func (s *Server) handleAIStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]bool{
		"transcription": s.ai.CanTranscribe(r.Context()),
		"summary":       s.ai.CanSummarize(r.Context()),
	})
}
