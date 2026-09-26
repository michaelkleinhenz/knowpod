package http

import (
	"io"
	"net/http"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/pocket"
)

// pocketWebhookPath is where Pocket delivers webhooks; the Status page shows the full URL.
const pocketWebhookPath = "/api/v1/webhooks/pocket"

// maxWebhookBody bounds a webhook payload (it includes transcript and summaries).
const maxWebhookBody = 10 << 20

// handlePocketWebhook verifies and accepts a Pocket webhook. The audio is fetched in the
// background, so the response is immediate (Pocket times out after 30 seconds).
func (s *Server) handlePocketWebhook(w http.ResponseWriter, r *http.Request) {
	if s.pocket == nil || s.cfg.PocketWebhookSecret == "" || s.cfg.PocketAPIKey == "" {
		writeJSON(w, http.StatusServiceUnavailable, errResponse{Error: "Pocket integration is not configured"})
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxWebhookBody))
	if err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge, errResponse{Error: "payload too large"})
		return
	}
	err = pocket.VerifySignature(s.cfg.PocketWebhookSecret, r.Header.Get(pocket.TimestampHeader),
		r.Header.Get(pocket.SignatureHeader), body, s.now())
	if err != nil {
		s.log.Warn("pocket webhook rejected", "err", err, "remote", r.RemoteAddr)
		writeJSON(w, http.StatusUnauthorized, errResponse{Error: pocket.ErrBadSignature.Error()})
		return
	}
	ev, err := pocket.ParseEvent(body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errResponse{Error: "invalid payload"})
		return
	}
	result, err := s.pocket.HandleWebhook(r.Context(), ev)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": string(result)})
}

type pocketIntegration struct {
	WebhookPath             string `json:"webhookPath"`
	WebhookSecretConfigured bool   `json:"webhookSecretConfigured"`
	APIKeyConfigured        bool   `json:"apiKeyConfigured"`
}

type integrationsResponse struct {
	Pocket pocketIntegration `json:"pocket"`
}

// handleIntegrations reports how external integrations are set up, for the Status page.
func (s *Server) handleIntegrations(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, integrationsResponse{Pocket: pocketIntegration{
		WebhookPath:             pocketWebhookPath,
		WebhookSecretConfigured: s.cfg.PocketWebhookSecret != "",
		APIKeyConfigured:        s.cfg.PocketAPIKey != "",
	}})
}
