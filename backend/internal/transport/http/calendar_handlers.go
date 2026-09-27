package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func (s *Server) handleGetCalendar(w http.ResponseWriter, r *http.Request) {
	v, err := s.calendar.Status(r.Context(), accountFrom(r.Context()))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// handleEnableCalendar makes a new feed link, replacing the previous one.
func (s *Server) handleEnableCalendar(w http.ResponseWriter, r *http.Request) {
	v, err := s.calendar.Enable(r.Context(), accountFrom(r.Context()))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handleDisableCalendar(w http.ResponseWriter, r *http.Request) {
	if err := s.calendar.Disable(r.Context(), accountFrom(r.Context())); err != nil {
		s.writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleCalendarFeed serves a user's tasks as an iCalendar feed. The secret in the link
// authenticates it, since calendar apps can't sign in.
func (s *Server) handleCalendarFeed(w http.ResponseWriter, r *http.Request) {
	data, err := s.calendar.Feed(r.Context(), chi.URLParam(r, "token"), baseURL(r))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/calendar; charset=utf-8")
	h.Set("Content-Disposition", `inline; filename="knowpod.ics"`)
	h.Set("Cache-Control", "private, max-age=300")
	_, _ = w.Write(data)
}

// baseURL is the scheme and host the request was made to, as seen by the client.
func baseURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https" {
		scheme = "https"
	}
	return scheme + "://" + r.Host
}
