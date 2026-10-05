package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/service"
)

func (s *Server) handleListFolders(w http.ResponseWriter, r *http.Request) {
	list, err := s.folders.List(r.Context(), accountFrom(r.Context()))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleCreateFolder(w http.ResponseWriter, r *http.Request) {
	var in service.FolderInput
	if !decode(w, r, &in) {
		return
	}
	f, err := s.folders.Create(r.Context(), accountFrom(r.Context()), in)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, f)
}

func (s *Server) handleUpdateFolder(w http.ResponseWriter, r *http.Request) {
	var in service.FolderInput
	if !decode(w, r, &in) {
		return
	}
	f, err := s.folders.Update(r.Context(), accountFrom(r.Context()), chi.URLParam(r, "id"), in)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, f)
}

// handleDuplicateFolder copies a folder with what is in it.
func (s *Server) handleDuplicateFolder(w http.ResponseWriter, r *http.Request) {
	f, err := s.folders.Duplicate(r.Context(), accountFrom(r.Context()), chi.URLParam(r, "id"))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, f)
}

func (s *Server) handleDeleteFolder(w http.ResponseWriter, r *http.Request) {
	if err := s.folders.Delete(r.Context(), accountFrom(r.Context()), chi.URLParam(r, "id")); err != nil {
		s.writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleSetNoteFolder moves a note with {"folderId": "..."}; an empty ID is the top level.
func (s *Server) handleSetNoteFolder(w http.ResponseWriter, r *http.Request) {
	var in struct {
		FolderID string `json:"folderId"`
	}
	if !decode(w, r, &in) {
		return
	}
	rec, err := s.actions.SetFolder(r.Context(), accountFrom(r.Context()), chi.URLParam(r, "id"), in.FolderID)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// handleSetNoteParent makes a note a sub-note with {"parentId": "..."}; an empty ID takes it
// out from under its parent to the top level.
func (s *Server) handleSetNoteParent(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ParentID string `json:"parentId"`
	}
	if !decode(w, r, &in) {
		return
	}
	rec, err := s.actions.SetParent(r.Context(), accountFrom(r.Context()), chi.URLParam(r, "id"), in.ParentID)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// orderInput lists the folders or notes of one place in the order they are shown.
type orderInput struct {
	IDs []string `json:"ids"`
}

// handleReorderFolders orders folders that are in the same place with {"ids": [...]}.
func (s *Server) handleReorderFolders(w http.ResponseWriter, r *http.Request) {
	var in orderInput
	if !decode(w, r, &in) {
		return
	}
	if err := s.folders.Reorder(r.Context(), accountFrom(r.Context()), in.IDs); err != nil {
		s.writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleReorderNotes orders notes that are in the same place (folder or parent note) with
// {"ids": [...]}.
func (s *Server) handleReorderNotes(w http.ResponseWriter, r *http.Request) {
	var in orderInput
	if !decode(w, r, &in) {
		return
	}
	if err := s.actions.Reorder(r.Context(), accountFrom(r.Context()), in.IDs); err != nil {
		s.writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
