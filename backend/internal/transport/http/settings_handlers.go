package http

import (
	"errors"
	"net/http"
	"strings"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/elevenlabs"
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

// handleTestElevenLabs checks that an ElevenLabs API key may transcribe: the one in the
// body, or the stored one when the body has none.
func (s *Server) handleTestElevenLabs(w http.ResponseWriter, r *http.Request) {
	var in struct {
		APIKey string `json:"apiKey"`
	}
	if !decode(w, r, &in) {
		return
	}
	err := s.ai.TestElevenLabs(r.Context(), in.APIKey)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	case errors.Is(err, elevenlabs.ErrUnauthorized):
		writeCode(w, http.StatusBadRequest, "elevenlabs_unauthorized", err.Error())
	case errors.Is(err, service.ErrSpeechUnavailable):
		writeCode(w, http.StatusServiceUnavailable, "elevenlabs_unavailable", err.Error())
	case errors.Is(err, service.ErrInvalidInput):
		s.writeErr(w, err)
	default:
		s.log.Warn("ElevenLabs test failed", "err", err)
		writeCode(w, http.StatusBadGateway, "elevenlabs_failed", "ElevenLabs test failed: "+strings.TrimPrefix(err.Error(), "elevenlabs: "))
	}
}

func (s *Server) handleOpenRouterModels(w http.ResponseWriter, r *http.Request) {
	models, err := s.ai.Models(r.Context())
	if err != nil {
		s.log.Warn("listing OpenRouter models failed", "err", err)
		writeCode(w, http.StatusBadGateway, "openrouter_unreachable", "could not load the model list from OpenRouter")
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
