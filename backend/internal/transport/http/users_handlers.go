package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/service"
)

type setPasswordRequest struct {
	Password string `json:"password"`
}

func (s *Server) handleListUsers(w http.ResponseWriter, r *http.Request) {
	list, err := s.users.List(r.Context())
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var in service.UserInput
	if !decode(w, r, &in) {
		return
	}
	u, err := s.users.Create(r.Context(), in)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, u)
}

func (s *Server) handleUpdateUser(w http.ResponseWriter, r *http.Request) {
	var in service.UserInput
	if !decode(w, r, &in) {
		return
	}
	in.Password = nil // passwords are set with PUT …/password
	u, err := s.users.Update(r.Context(), chi.URLParam(r, "id"), in)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, u)
}

// handleSetUserPassword sets another user's password (an administrator's reset). The user is
// signed out everywhere.
func (s *Server) handleSetUserPassword(w http.ResponseWriter, r *http.Request) {
	var req setPasswordRequest
	if !decode(w, r, &req) {
		return
	}
	keep := ""
	if c, err := r.Cookie(sessionCookie); err == nil {
		keep = service.SessionHash(c.Value) // keep the administrator's own session
	}
	if err := s.users.SetPassword(r.Context(), chi.URLParam(r, "id"), req.Password, keep); err != nil {
		s.writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	if err := s.users.Delete(r.Context(), accountFrom(r.Context()), chi.URLParam(r, "id")); err != nil {
		s.writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
