package http

import (
	"context"
	"net/http"
	"time"
)

// pullTimeout bounds a pull started from the UI; a first pull of a large account reads the
// metadata of every document.
const pullTimeout = 2 * time.Minute

func (s *Server) handleGetRemarkable(w http.ResponseWriter, r *http.Request) {
	v, err := s.remarkable.Status(r.Context(), accountFrom(r.Context()))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handlePairRemarkable(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Code string `json:"code"`
	}
	if !decode(w, r, &in) {
		return
	}
	v, err := s.remarkable.Pair(r.Context(), accountFrom(r.Context()), in.Code)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handleUpdateRemarkable(w http.ResponseWriter, r *http.Request) {
	var in struct {
		IgnoredNames []string `json:"ignoredNames"`
	}
	if !decode(w, r, &in) {
		return
	}
	v, err := s.remarkable.SetIgnoredNames(r.Context(), accountFrom(r.Context()), in.IgnoredNames)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handleUnpairRemarkable(w http.ResponseWriter, r *http.Request) {
	if err := s.remarkable.Unpair(r.Context(), accountFrom(r.Context())); err != nil {
		s.writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handlePullRemarkable(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), pullTimeout)
	defer cancel()
	v, err := s.remarkable.Pull(ctx, accountFrom(r.Context()))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}
