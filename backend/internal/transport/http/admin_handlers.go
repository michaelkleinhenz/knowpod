package http

import (
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/device"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
)

type registerDeviceRequest struct {
	Name string `json:"name"`
}

type registerDeviceResponse struct {
	Device *device.Device `json:"device"`
	// Token is shown only once; configure it on the recorder.
	Token string `json:"token"`
}

func (s *Server) handleRegisterDevice(w http.ResponseWriter, r *http.Request) {
	var req registerDeviceRequest
	if !decode(w, r, &req) {
		return
	}
	d, token, err := s.devices.Register(r.Context(), req.Name)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, registerDeviceResponse{Device: d, Token: token})
}

// handleRotateDeviceToken issues a new token for a device; the old one stops working.
func (s *Server) handleRotateDeviceToken(w http.ResponseWriter, r *http.Request) {
	d, token, err := s.devices.RotateToken(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, registerDeviceResponse{Device: d, Token: token})
}

func (s *Server) handleListDevices(w http.ResponseWriter, r *http.Request) {
	list, err := s.devices.List(r.Context())
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleRevokeDevice(w http.ResponseWriter, r *http.Request) {
	if err := s.devices.Revoke(r.Context(), chi.URLParam(r, "id")); err != nil {
		s.writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListRecordings(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	offset, _ := strconv.Atoi(q.Get("offset"))
	list, err := s.recordings.List(r.Context(), recording.ListFilter{
		DeviceID: q.Get("deviceId"), Status: recording.Status(q.Get("status")), Limit: limit, Offset: max(offset, 0),
	})
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleGetRecording(w http.ResponseWriter, r *http.Request) {
	rec, err := s.recordings.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// handleRecordingAudio streams the archived FLAC file.
func (s *Server) handleRecordingAudio(w http.ResponseWriter, r *http.Request) {
	rec, err := s.recordings.Get(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	if rec.Audio == nil {
		writeJSON(w, http.StatusConflict, errResponse{Error: "audio not archived yet (status " + string(rec.Status) + ")"})
		return
	}
	body, err := s.objects.Get(r.Context(), rec.Audio.Key)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	defer body.Close()
	w.Header().Set("Content-Type", rec.Audio.ContentType)
	w.Header().Set("Content-Length", strconv.FormatInt(rec.Audio.Size, 10))
	w.Header().Set("Content-Disposition", `attachment; filename="`+rec.ID+`.flac"`)
	if _, err := io.Copy(w, body); err != nil {
		s.log.Warn("streaming audio aborted", "id", rec.ID, "err", err)
	}
}
