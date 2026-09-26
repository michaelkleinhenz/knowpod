package http

import (
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/service"
)

// uploadOffsetHeader carries the byte offset of a chunk (request) and the bytes received so
// far (response), as in the tus protocol.
const uploadOffsetHeader = "Upload-Offset"

type createUploadRequest struct {
	RecordingID string     `json:"recordingId"`
	Size        int64      `json:"size"`
	SHA256      string     `json:"sha256"`
	RecordedAt  *time.Time `json:"recordedAt,omitempty"`
	// Highlights marked while recording (offsetMs or at each).
	Highlights []service.HighlightInput `json:"highlights,omitempty"`
}

type highlightsRequest struct {
	Highlights []service.HighlightInput `json:"highlights"`
}

type uploadResponse struct {
	UploadID    string `json:"uploadId"`
	RecordingID string `json:"recordingId"`
	Status      string `json:"status"`
	Size        int64  `json:"size"`
	Offset      int64  `json:"offset"`
	Error       string `json:"error,omitempty"`
}

func writeUpload(w http.ResponseWriter, status int, up *service.Upload) {
	w.Header().Set(uploadOffsetHeader, strconv.FormatInt(up.Offset, 10))
	writeJSON(w, status, uploadResponse{
		UploadID: up.Recording.ID, RecordingID: up.Recording.ClientID, Status: string(up.Recording.Status),
		Size: up.Recording.Size, Offset: up.Offset, Error: up.Recording.LastError,
	})
}

// handleCreateUpload starts an upload (201) or returns the device's existing upload with the
// same recordingId (200), so retrying a create is safe.
func (s *Server) handleCreateUpload(w http.ResponseWriter, r *http.Request) {
	var req createUploadRequest
	if !decode(w, r, &req) {
		return
	}
	up, created, err := s.uploads.Create(r.Context(), deviceFrom(r.Context()), service.CreateUploadInput{
		RecordingID: req.RecordingID, Size: req.Size, SHA256: req.SHA256, RecordedAt: req.RecordedAt,
		Highlights: req.Highlights,
	})
	if err != nil {
		s.writeErr(w, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeUpload(w, status, up)
}

// handleSetHighlights replaces the highlights of one of the device's recordings.
func (s *Server) handleSetHighlights(w http.ResponseWriter, r *http.Request) {
	var req highlightsRequest
	if !decode(w, r, &req) {
		return
	}
	rec, err := s.uploads.SetHighlights(r.Context(), deviceFrom(r.Context()), chi.URLParam(r, "id"), req.Highlights)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"uploadId": rec.ID, "highlights": rec.Highlights})
}

func (s *Server) handleGetUpload(w http.ResponseWriter, r *http.Request) {
	up, err := s.uploads.Get(r.Context(), deviceFrom(r.Context()), chi.URLParam(r, "id"))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeUpload(w, http.StatusOK, up)
}

// handleAppendUpload appends the request body at the Upload-Offset. The body is streamed to
// disk; if the connection drops, the bytes received so far are kept and GET reports the
// offset to resume from.
func (s *Server) handleAppendUpload(w http.ResponseWriter, r *http.Request) {
	offset, err := strconv.ParseInt(r.Header.Get(uploadOffsetHeader), 10, 64)
	if err != nil || offset < 0 {
		writeCode(w, http.StatusBadRequest, "invalid_request", "Upload-Offset header must be a non-negative integer")
		return
	}
	dev := deviceFrom(r.Context())
	id := chi.URLParam(r, "id")

	// Reject chunks that would overrun the declared size before reading them.
	if r.ContentLength > 0 {
		cur, err := s.uploads.Get(r.Context(), dev, id)
		if err != nil {
			s.writeErr(w, err)
			return
		}
		if offset+r.ContentLength > cur.Recording.Size {
			s.writeErr(w, service.ErrTooLarge)
			return
		}
	}

	up, err := s.uploads.Append(r.Context(), dev, id, offset, r.Body)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeUpload(w, http.StatusOK, up)
}
