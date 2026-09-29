package http

import (
	"net/http"
	"path"

	"github.com/go-chi/chi/v5"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/service"
)

// handleListVersions lists the earlier versions of a note's title and text, newest first.
func (s *Server) handleListVersions(w http.ResponseWriter, r *http.Request) {
	list, err := s.actions.ListVersions(r.Context(), accountFrom(r.Context()), chi.URLParam(r, "id"))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// handleGetVersion returns an earlier version of a note's title and text.
func (s *Server) handleGetVersion(w http.ResponseWriter, r *http.Request) {
	v, err := s.actions.Version(r.Context(), accountFrom(r.Context()), chi.URLParam(r, "id"), chi.URLParam(r, "versionId"))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// handleRestoreVersion makes an earlier version the note's title and text again.
func (s *Server) handleRestoreVersion(w http.ResponseWriter, r *http.Request) {
	var in struct {
		BaseRevision *int64 `json:"baseRevision,omitempty"`
	}
	if r.ContentLength != 0 && !decode(w, r, &in) {
		return
	}
	rec, err := s.actions.RestoreVersion(r.Context(), accountFrom(r.Context()), chi.URLParam(r, "id"), chi.URLParam(r, "versionId"), in.BaseRevision)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// handleSetTemplate marks a text note as a template, or makes it a plain note again.
func (s *Server) handleSetTemplate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Template bool `json:"template"`
	}
	if !decode(w, r, &in) {
		return
	}
	rec, err := s.actions.SetTemplate(r.Context(), accountFrom(r.Context()), chi.URLParam(r, "id"), in.Template)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// handlePublishNote gives a note a public web link (or keeps the one it has).
func (s *Server) handlePublishNote(w http.ResponseWriter, r *http.Request) {
	rec, err := s.actions.Publish(r.Context(), accountFrom(r.Context()), chi.URLParam(r, "id"))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// handleUnpublishNote takes a note's public web link down.
func (s *Server) handleUnpublishNote(w http.ResponseWriter, r *http.Request) {
	rec, err := s.actions.Unpublish(r.Context(), accountFrom(r.Context()), chi.URLParam(r, "id"))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// handlePublicNote shows a published note to anyone with its link, without signing in.
func (s *Server) handlePublicNote(w http.ResponseWriter, r *http.Request) {
	note, err := s.actions.Published(r.Context(), chi.URLParam(r, "token"))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Robots-Tag", "noindex")
	writeJSON(w, http.StatusOK, note)
}

// handlePublicImage streams a picture of a published note.
func (s *Server) handlePublicImage(w http.ResponseWriter, r *http.Request) {
	obj, err := s.actions.PublishedImage(r.Context(), chi.URLParam(r, "token"), chi.URLParam(r, "imageId"))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	w.Header().Set("X-Robots-Tag", "noindex")
	s.streamObject(w, r, obj, "image"+path.Ext(obj.Key))
}

// handleAIWrite has the AI write or rewrite a passage of a note for the editor.
func (s *Server) handleAIWrite(w http.ResponseWriter, r *http.Request) {
	var in service.WriteInput
	if !decode(w, r, &in) {
		return
	}
	out, err := s.ai.Write(r.Context(), accountFrom(r.Context()), in)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
