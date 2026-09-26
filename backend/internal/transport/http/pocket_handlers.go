package http

import (
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/pocket"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/service"
)

// maxWebhookBody bounds a webhook payload (it includes transcript and summaries).
const maxWebhookBody = 10 << 20

// handlePocketWebhook verifies and accepts a Pocket webhook for the user its URL belongs to.
// The audio is fetched in the background, so the response is immediate (Pocket times out
// after 30 seconds).
func (s *Server) handlePocketWebhook(w http.ResponseWriter, r *http.Request) {
	owner, err := s.pocket.WebhookUser(r.Context(), chi.URLParam(r, "webhookId"))
	if err != nil {
		writeCode(w, http.StatusNotFound, "unknown_webhook", "unknown webhook")
		return
	}
	if owner.Pocket.WebhookSecret == "" || owner.Pocket.APIKey == "" {
		writeCode(w, http.StatusServiceUnavailable, "pocket_not_configured", "Pocket integration is not set up for this user")
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWebhookBody))
	if err != nil {
		writeCode(w, http.StatusRequestEntityTooLarge, "too_large", "payload too large")
		return
	}
	err = pocket.VerifySignature(owner.Pocket.WebhookSecret, r.Header.Get(pocket.TimestampHeader),
		r.Header.Get(pocket.SignatureHeader), body, s.now())
	if err != nil {
		s.log.Warn("pocket webhook rejected", "err", err, "user", owner.ID, "remote", r.RemoteAddr)
		writeCode(w, http.StatusUnauthorized, "bad_signature", pocket.ErrBadSignature.Error())
		return
	}
	ev, err := pocket.ParseEvent(body)
	if err != nil {
		writeCode(w, http.StatusBadRequest, "invalid_request", "invalid payload")
		return
	}
	result, err := s.pocket.HandleWebhook(r.Context(), owner, ev)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": string(result)})
}

func (s *Server) handleGetPocketSettings(w http.ResponseWriter, r *http.Request) {
	v, err := s.pocket.Settings(r.Context(), accountFrom(r.Context()))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handleUpdatePocketSettings(w http.ResponseWriter, r *http.Request) {
	var in service.PocketUpdate
	if !decode(w, r, &in) {
		return
	}
	v, err := s.pocket.UpdateSettings(r.Context(), accountFrom(r.Context()), in)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}
