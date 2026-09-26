package http

import (
	"fmt"
	"io"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/device"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/service"
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
		Brief: q.Get("full") == "",
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

// handleRecordingAudio streams the archived audio (FLAC, or the original format of fetched
// recordings). It supports single byte ranges so the browser's audio player can seek.
// ?download=1 asks the browser to save the file instead of playing it.
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
	size := rec.Audio.Size
	offset, length, partial, ok := parseRange(r.Header.Get("Range"), size)
	if !ok {
		w.Header().Set("Content-Range", "bytes */"+strconv.FormatInt(size, 10))
		w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
		return
	}
	body, err := s.objects.Get(r.Context(), rec.Audio.Key, offset, length)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	defer body.Close()

	disposition := "inline"
	if r.URL.Query().Get("download") != "" {
		disposition = "attachment"
	}
	h := w.Header()
	h.Set("Content-Type", rec.Audio.ContentType)
	h.Set("Content-Length", strconv.FormatInt(length, 10))
	h.Set("Accept-Ranges", "bytes")
	h.Set("Content-Disposition", disposition+`; filename="`+rec.ID+path.Ext(rec.Audio.Key)+`"`)
	h.Set("Cache-Control", "private, max-age=3600")
	if partial {
		h.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", offset, offset+length-1, size))
		w.WriteHeader(http.StatusPartialContent)
	}
	if _, err := io.Copy(w, body); err != nil {
		s.log.Debug("streaming audio aborted", "id", rec.ID, "err", err)
	}
}

// parseRange interprets a Range header for a resource of the given size. Only a single
// "bytes=" range is supported (what media players send); anything else serves the whole
// file. ok is false for ranges outside the file.
func parseRange(header string, size int64) (offset, length int64, partial, ok bool) {
	spec, found := strings.CutPrefix(header, "bytes=")
	if !found || strings.Contains(spec, ",") {
		return 0, size, false, true
	}
	startStr, endStr, _ := strings.Cut(strings.TrimSpace(spec), "-")
	end := size - 1
	switch {
	case startStr == "": // suffix range: the last N bytes
		n, err := strconv.ParseInt(endStr, 10, 64)
		if err != nil || n <= 0 {
			return 0, size, false, true
		}
		offset = max(size-n, 0)
	default:
		var err error
		if offset, err = strconv.ParseInt(startStr, 10, 64); err != nil {
			return 0, size, false, true
		}
		if endStr != "" {
			if e, err := strconv.ParseInt(endStr, 10, 64); err == nil && e < end {
				end = e
			}
		}
	}
	if offset >= size || offset > end {
		return 0, 0, false, false
	}
	return offset, end - offset + 1, true, true
}

// handleDeleteRecording removes a recording with its audio.
func (s *Server) handleDeleteRecording(w http.ResponseWriter, r *http.Request) {
	if err := s.actions.Delete(r.Context(), chi.URLParam(r, "id")); err != nil {
		s.writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRetranscribe(w http.ResponseWriter, r *http.Request) {
	rec, err := s.actions.Retranscribe(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

func (s *Server) handleResummarize(w http.ResponseWriter, r *http.Request) {
	rec, err := s.actions.Resummarize(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

func (s *Server) handleGetOpenRouterSettings(w http.ResponseWriter, r *http.Request) {
	v, err := s.ai.Settings(r.Context())
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handleUpdateOpenRouterSettings(w http.ResponseWriter, r *http.Request) {
	var u service.OpenRouterUpdate
	if !decode(w, r, &u) {
		return
	}
	v, err := s.ai.UpdateSettings(r.Context(), u)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *Server) handleOpenRouterModels(w http.ResponseWriter, r *http.Request) {
	models, err := s.ai.Models(r.Context())
	if err != nil {
		s.log.Warn("listing OpenRouter models failed", "err", err)
		writeJSON(w, http.StatusBadGateway, errResponse{Error: "could not load the model list from OpenRouter"})
		return
	}
	writeJSON(w, http.StatusOK, models)
}
