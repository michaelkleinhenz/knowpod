package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/service"
)

// handleGetSharing says who a note is shared with.
func (s *Server) handleGetSharing(w http.ResponseWriter, r *http.Request) {
	out, err := s.actions.Sharing(r.Context(), accountFrom(r.Context()), chi.URLParam(r, "id"))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleShareNote shares a note (and everything under it) with the user signing in with
// the email in the body, or changes their role.
func (s *Server) handleShareNote(w http.ResponseWriter, r *http.Request) {
	var in service.ShareInput
	if !decode(w, r, &in) {
		return
	}
	out, err := s.actions.Share(r.Context(), accountFrom(r.Context()), chi.URLParam(r, "id"), in)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleSetShareRole changes what a user a note is shared with may do with it.
func (s *Server) handleSetShareRole(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Role recording.Role `json:"role"`
	}
	if !decode(w, r, &in) {
		return
	}
	out, err := s.actions.SetShareRole(r.Context(), accountFrom(r.Context()), chi.URLParam(r, "id"), chi.URLParam(r, "userId"), in.Role)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleUnshareNote stops sharing a note with a user; users can remove themselves.
func (s *Server) handleUnshareNote(w http.ResponseWriter, r *http.Request) {
	out, err := s.actions.Unshare(r.Context(), accountFrom(r.Context()), chi.URLParam(r, "id"), chi.URLParam(r, "userId"))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	if out == nil {
		// The user left the note.
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handleNoteEvents tells the web app about changes of the notes the user sees as they
// happen, as server-sent events: "note" (data {id, version}: load the note again; it is
// gone when that fails) and "reload" (load the list again).
func (s *Server) handleNoteEvents(w http.ResponseWriter, r *http.Request) {
	if s.events == nil {
		writeCode(w, http.StatusNotFound, "not_found", "live updates are not available")
		return
	}
	msgs, stop, err := s.events.Listen(accountFrom(r.Context()))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	defer stop()
	streamEvents(w, r, msgs, func(e service.NoteEvent) string { return e.Type })
}
