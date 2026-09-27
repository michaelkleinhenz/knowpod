package http

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

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

// streamHeartbeat is how often an idle stream sends a comment, so proxies and load
// balancers don't close it as idle and the app notices a dead connection.
const streamHeartbeat = 25 * time.Second

// handleNotificationStream sends the user's notifications as they happen, as server-sent
// events ("notification", data {title, body, url, tag}). The desktop app listens here: it
// has no push service, so Web Push can't reach it.
func (s *Server) handleNotificationStream(w http.ResponseWriter, r *http.Request) {
	msgs, stop, err := s.notifications.Listen(accountFrom(r.Context()))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	defer stop()

	rc := http.NewResponseController(w)
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	// The retry field tells EventSource how long to wait before reconnecting.
	if _, err := io.WriteString(w, "retry: 5000\n\n"); err != nil || rc.Flush() != nil {
		return
	}

	heartbeat := time.NewTicker(streamHeartbeat)
	defer heartbeat.Stop()
	for {
		var chunk string
		select {
		case <-r.Context().Done():
			return
		case m, ok := <-msgs:
			if !ok {
				return // replaced by a newer connection, or the server is shutting down
			}
			data, err := json.Marshal(m)
			if err != nil {
				continue
			}
			chunk = "event: notification\ndata: " + string(data) + "\n\n"
		case <-heartbeat.C:
			chunk = ": ping\n\n"
		}
		if _, err := io.WriteString(w, chunk); err != nil || rc.Flush() != nil {
			return
		}
	}
}
