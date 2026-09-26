package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/device"
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
	d, token, err := s.devices.Register(r.Context(), accountFrom(r.Context()), req.Name)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, registerDeviceResponse{Device: d, Token: token})
}

func (s *Server) handleListDevices(w http.ResponseWriter, r *http.Request) {
	list, err := s.devices.List(r.Context(), accountFrom(r.Context()))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// handleRotateDeviceToken issues a new token for a device; the old one stops working.
func (s *Server) handleRotateDeviceToken(w http.ResponseWriter, r *http.Request) {
	d, token, err := s.devices.RotateToken(r.Context(), accountFrom(r.Context()), chi.URLParam(r, "id"))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, registerDeviceResponse{Device: d, Token: token})
}

func (s *Server) handleRevokeDevice(w http.ResponseWriter, r *http.Request) {
	if err := s.devices.Revoke(r.Context(), accountFrom(r.Context()), chi.URLParam(r, "id")); err != nil {
		s.writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
