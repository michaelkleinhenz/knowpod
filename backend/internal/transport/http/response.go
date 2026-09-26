// Package http is the transport layer: routing, middleware and handlers. It maps HTTP to
// the service layer and back, and contains no business rules of its own.
package http

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/service"
)

// writeJSON serialises v as JSON with the given status.
func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

// errResponse is the uniform error envelope.
type errResponse struct {
	Error string `json:"error"`
	// Offset is set on offset mismatches: the offset to resume the upload from.
	Offset *int64 `json:"offset,omitempty"`
}

// writeErr maps a service error to an HTTP status + message.
func (s *Server) writeErr(w http.ResponseWriter, err error) {
	var om *service.OffsetMismatchError
	status := http.StatusInternalServerError
	switch {
	case errors.As(err, &om):
		w.Header().Set(uploadOffsetHeader, strconv.FormatInt(om.Current, 10))
		writeJSON(w, http.StatusConflict, errResponse{Error: err.Error(), Offset: &om.Current})
		return
	case errors.Is(err, service.ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, service.ErrInvalidInput), errors.Is(err, service.ErrWeakPassword):
		status = http.StatusBadRequest
	case errors.Is(err, service.ErrInvalidLogin), errors.Is(err, service.ErrNotSignedIn):
		status = http.StatusUnauthorized
	case errors.Is(err, service.ErrWrongPassword):
		status = http.StatusForbidden
	case errors.Is(err, service.ErrConflict), errors.Is(err, service.ErrNotReady):
		status = http.StatusConflict
	case errors.Is(err, service.ErrTooLarge):
		status = http.StatusRequestEntityTooLarge
	case errors.Is(err, service.ErrChecksumMismatch), errors.Is(err, service.ErrInvalidAudio):
		status = http.StatusUnprocessableEntity
	}
	if status == http.StatusInternalServerError {
		s.log.Error("request failed", "err", err)
		writeJSON(w, status, errResponse{Error: "internal error"})
		return
	}
	writeJSON(w, status, errResponse{Error: err.Error()})
}

// decode reads a JSON body into dst, returning false (and writing 400) on failure.
func decode(w http.ResponseWriter, r *http.Request, dst interface{}) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(dst); err != nil {
		writeJSON(w, http.StatusBadRequest, errResponse{Error: "invalid request body"})
		return false
	}
	return true
}
