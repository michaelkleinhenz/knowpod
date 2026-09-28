package http

import (
	"net/http"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/service"
)

// handleAsk answers a question about the user's notes.
func (s *Server) handleAsk(w http.ResponseWriter, r *http.Request) {
	var in service.AskInput
	if !decode(w, r, &in) {
		return
	}
	out, err := s.ask.Ask(r.Context(), accountFrom(r.Context()), in)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
