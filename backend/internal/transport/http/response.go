// Package http is the transport layer: routing, middleware and handlers. It maps HTTP to
// the service layer and back, and contains no business rules of its own.
package http

import (
	"encoding/json"
	"net/http"
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
}

// decode reads a JSON body into dst, returning false (and writing 400) on failure.
func decode(w http.ResponseWriter, r *http.Request, dst interface{}) bool {
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		writeJSON(w, http.StatusBadRequest, errResponse{Error: "invalid request body"})
		return false
	}
	return true
}
