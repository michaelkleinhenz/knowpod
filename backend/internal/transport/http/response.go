// Package http is the transport layer: routing, middleware and handlers. It maps HTTP to
// the service layer and back, and contains no business rules of its own.
package http

import (
	"encoding/json"
	"errors"
	"net/http"
	"sort"
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

// errResponse is the uniform error envelope. Code is a stable, machine-readable identifier
// that clients translate; Error is an English message for people and logs.
type errResponse struct {
	Error string `json:"error"`
	Code  string `json:"code,omitempty"`
	// Offset is set on offset mismatches: the offset to resume the upload from.
	Offset *int64 `json:"offset,omitempty"`
}

// errorCodes maps service errors to HTTP statuses and codes, most specific first.
var errorCodes = []struct {
	err    error
	status int
	code   string
}{
	{service.ErrNotFound, http.StatusNotFound, "not_found"},
	{service.ErrChanged, http.StatusConflict, "changed"},
	{service.ErrWeakPassword, http.StatusBadRequest, "weak_password"},
	{service.ErrInvalidInput, http.StatusBadRequest, "invalid_input"},
	{service.ErrInvalidLogin, http.StatusUnauthorized, "invalid_login"},
	{service.ErrNotSignedIn, http.StatusUnauthorized, "not_signed_in"},
	{errInvalidAdminToken, http.StatusUnauthorized, "invalid_token"},
	{service.ErrWrongPassword, http.StatusForbidden, "wrong_password"},
	{service.ErrSelfDelete, http.StatusForbidden, "cannot_delete_self"},
	{service.ErrBuiltInAdmin, http.StatusForbidden, "builtin_admin"},
	{service.ErrLastAdmin, http.StatusForbidden, "last_admin"},
	{service.ErrForbidden, http.StatusForbidden, "forbidden"},
	{service.ErrInvalidPairingCode, http.StatusBadRequest, "invalid_pairing_code"},
	{service.ErrPairingRevoked, http.StatusConflict, "pairing_revoked"},
	{service.ErrNotPaired, http.StatusConflict, "not_paired"},
	{service.ErrCloudUnavailable, http.StatusBadGateway, "remarkable_unavailable"},
	{service.ErrEmailTaken, http.StatusConflict, "email_taken"},
	{service.ErrNotReady, http.StatusConflict, "not_ready"},
	{service.ErrConflict, http.StatusConflict, "conflict"},
	{service.ErrUnsupportedMedia, http.StatusUnsupportedMediaType, "unsupported_media"},
	{service.ErrTooLarge, http.StatusRequestEntityTooLarge, "too_large"},
	{service.ErrChecksumMismatch, http.StatusUnprocessableEntity, "checksum_mismatch"},
	{service.ErrInvalidAudio, http.StatusUnprocessableEntity, "invalid_audio"},
}

// writeErr maps a service error to an HTTP status, code and message.
func (s *Server) writeErr(w http.ResponseWriter, err error) {
	var om *service.OffsetMismatchError
	if errors.As(err, &om) {
		w.Header().Set(uploadOffsetHeader, strconv.FormatInt(om.Current, 10))
		writeJSON(w, http.StatusConflict, errResponse{Error: err.Error(), Code: "offset_mismatch", Offset: &om.Current})
		return
	}
	for _, e := range errorCodes {
		if errors.Is(err, e.err) {
			writeJSON(w, e.status, errResponse{Error: err.Error(), Code: e.code})
			return
		}
	}
	s.log.Error("request failed", "err", err)
	writeJSON(w, http.StatusInternalServerError, errResponse{Error: "internal error", Code: "internal"})
}

func sortStrings(s []string) { sort.Strings(s) }

// writeCode writes an error that isn't a service error.
func writeCode(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, errResponse{Error: message, Code: code})
}

// decode reads a JSON body into dst, returning false (and writing 400) on failure.
func decode(w http.ResponseWriter, r *http.Request, dst interface{}) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(dst); err != nil {
		writeCode(w, http.StatusBadRequest, "invalid_request", "invalid request body")
		return false
	}
	return true
}
