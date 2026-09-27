package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/service"
)

func (s *Server) handleNotificationStatus(w http.ResponseWriter, r *http.Request) {
	st, err := s.notifications.Status(r.Context(), accountFrom(r.Context()))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// handleSubscribePush makes the browser receive the user's notifications. The body is the
// browser's PushSubscription.toJSON().
func (s *Server) handleSubscribePush(w http.ResponseWriter, r *http.Request) {
	var in service.PushSubscriptionInput
	if !decode(w, r, &in) {
		return
	}
	d, err := s.notifications.Subscribe(r.Context(), accountFrom(r.Context()), in, r.UserAgent())
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, d)
}

func (s *Server) handleUnsubscribePush(w http.ResponseWriter, r *http.Request) {
	if err := s.notifications.Unsubscribe(r.Context(), accountFrom(r.Context()), chi.URLParam(r, "id")); err != nil {
		s.writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleTestNotification(w http.ResponseWriter, r *http.Request) {
	n, err := s.notifications.SendTest(r.Context(), accountFrom(r.Context()))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"sent": n})
}
