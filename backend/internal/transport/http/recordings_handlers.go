package http

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/service"
)

func (s *Server) handleListRecordings(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	offset, _ := strconv.Atoi(q.Get("offset"))
	number, _ := strconv.ParseInt(q.Get("number"), 10, 64)
	list, err := s.actions.List(r.Context(), accountFrom(r.Context()), recording.ListFilter{
		Number:   max(number, 0),
		DeviceID: q.Get("deviceId"), Status: recording.Status(q.Get("status")), Limit: limit, Offset: max(offset, 0),
		Brief: q.Get("full") == "", Trash: trashFilter(q.Get("trash")),
	})
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

func (s *Server) handleGetRecording(w http.ResponseWriter, r *http.Request) {
	rec, err := s.actions.Get(r.Context(), accountFrom(r.Context()), chi.URLParam(r, "id"))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// handleUploadRecording stores a file sent as the raw request body: audio (WAV or MP3), a
// photo or a PDF. The file name comes in X-Filename (URL-encoded) and optionally the
// recording time in X-Recorded-At (RFC 3339). A voice memo recorded in the web app sends
// X-Recorder: 1, and the moments marked while recording in X-Highlights (comma-separated
// offsets in milliseconds).
func (s *Server) handleUploadRecording(w http.ResponseWriter, r *http.Request) {
	name, _ := url.QueryUnescape(r.Header.Get("X-Filename"))
	in := service.ManualUpload{Filename: name, Body: r.Body, Recorded: r.Header.Get("X-Recorder") == "1"}
	if h := strings.TrimSpace(r.Header.Get("X-Highlights")); h != "" {
		for _, part := range strings.Split(h, ",") {
			ms, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
			if err != nil {
				writeCode(w, http.StatusBadRequest, "invalid_request", "X-Highlights must be comma-separated offsets in milliseconds")
				return
			}
			in.Highlights = append(in.Highlights, ms)
		}
	}
	if t, err := time.Parse(time.RFC3339, r.Header.Get("X-Recorded-At")); err == nil {
		t = t.UTC()
		in.RecordedAt = &t
	}
	rec, err := s.manual.Upload(r.Context(), accountFrom(r.Context()), in)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, rec)
}

// handleRecordingAudio streams the archived audio (FLAC, or the original format of fetched
// and uploaded MP3s). It supports single byte ranges so the browser's audio player can seek.
// ?download=1 asks the browser to save the file instead of playing it.
func (s *Server) handleRecordingAudio(w http.ResponseWriter, r *http.Request) {
	rec, err := s.actions.Get(r.Context(), accountFrom(r.Context()), chi.URLParam(r, "id"))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	if rec.Audio == nil {
		writeCode(w, http.StatusConflict, "not_archived", "audio not archived yet (status "+string(rec.Status)+")")
		return
	}
	s.streamObject(w, r, rec.Audio, rec.ID+path.Ext(rec.Audio.Key))
}

// handleRecordingFile streams a document's PDF or EPUB, with byte ranges like the audio.
func (s *Server) handleRecordingFile(w http.ResponseWriter, r *http.Request) {
	rec, err := s.actions.Get(r.Context(), accountFrom(r.Context()), chi.URLParam(r, "id"))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	if rec.File == nil {
		writeCode(w, http.StatusConflict, "not_archived", "no document file stored (status "+string(rec.Status)+")")
		return
	}
	// The web UI shows the PDF in a frame; the API's headers otherwise forbid framing.
	h := w.Header()
	h.Set("X-Frame-Options", "SAMEORIGIN")
	h.Set("Content-Security-Policy", "default-src 'none'; object-src 'self'; style-src 'unsafe-inline'; frame-ancestors 'self'")
	s.streamObject(w, r, rec.File, fileName(rec, "document", strings.TrimPrefix(path.Ext(rec.File.Key), ".")))
}

// handleAddImage stores a picture pasted or dropped into a note's text, sent as the raw
// request body, and returns the URL to refer to it by.
func (s *Server) handleAddImage(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	imgID, err := s.actions.AddImage(r.Context(), accountFrom(r.Context()), id, http.MaxBytesReader(w, r.Body, 16<<20))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"id": imgID, "url": "/api/v1/recordings/" + url.PathEscape(id) + "/images/" + imgID})
}

// handleGetImage streams a picture of a note's text.
func (s *Server) handleGetImage(w http.ResponseWriter, r *http.Request) {
	obj, err := s.actions.Image(r.Context(), accountFrom(r.Context()), chi.URLParam(r, "id"), chi.URLParam(r, "imageId"))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	s.streamObject(w, r, obj, "image"+path.Ext(obj.Key))
}

// handleAddAttachment stores a file of any type attached to the note, sent as the raw request
// body with its name in ?name=.
func (s *Server) handleAddAttachment(w http.ResponseWriter, r *http.Request) {
	att, err := s.actions.AddAttachment(r.Context(), accountFrom(r.Context()), chi.URLParam(r, "id"), r.URL.Query().Get("name"), http.MaxBytesReader(w, r.Body, 51<<20))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, att)
}

// handleGetAttachment sends an attached file. It is always a download, with a fixed media
// type, so a file such as HTML can't run in the app's origin.
func (s *Server) handleGetAttachment(w http.ResponseWriter, r *http.Request) {
	att, err := s.actions.Attachment(r.Context(), accountFrom(r.Context()), chi.URLParam(r, "id"), chi.URLParam(r, "attachmentId"))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	q := r.URL.Query()
	q.Set("download", "1")
	r.URL.RawQuery = q.Encode()
	obj := att.Object()
	obj.ContentType = "application/octet-stream"
	w.Header().Set("X-Content-Type-Options", "nosniff")
	s.streamObject(w, r, &obj, att.Name)
}

// handleDeleteAttachment removes an attached file.
func (s *Server) handleDeleteAttachment(w http.ResponseWriter, r *http.Request) {
	rec, err := s.actions.DeleteAttachment(r.Context(), accountFrom(r.Context()), chi.URLParam(r, "id"), chi.URLParam(r, "attachmentId"))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// streamObject sends a stored file, or the byte range asked for. ?download=1 sends it as an
// attachment.
func (s *Server) streamObject(w http.ResponseWriter, r *http.Request, obj *recording.Object, name string) {
	size := obj.Size
	offset, length, partial, ok := parseRange(r.Header.Get("Range"), size)
	if !ok {
		w.Header().Set("Content-Range", "bytes */"+strconv.FormatInt(size, 10))
		w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
		return
	}
	body, err := s.objects.Get(r.Context(), obj.Key, offset, length)
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
	h.Set("Content-Type", obj.ContentType)
	h.Set("Content-Length", strconv.FormatInt(length, 10))
	h.Set("Accept-Ranges", "bytes")
	cd := disposition + `; filename="` + asciiName(name) + `"`
	if asciiName(name) != name {
		cd += `; filename*=UTF-8''` + url.PathEscape(name)
	}
	h.Set("Content-Disposition", cd)
	h.Set("Cache-Control", "private, max-age=3600")
	if partial {
		h.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", offset, offset+length-1, size))
		w.WriteHeader(http.StatusPartialContent)
	}
	if _, err := io.Copy(w, body); err != nil {
		s.log.Debug("streaming aborted", "key", obj.Key, "err", err)
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
// trashFilter reads the trash query parameter: "only" lists the notes in the trash, "any" all
// notes; anything else leaves out the notes in the trash.
func trashFilter(v string) recording.Trash {
	switch t := recording.Trash(v); t {
	case recording.TrashOnly, recording.TrashAny:
		return t
	}
	return recording.TrashExclude
}

// handleDeleteRecording moves a note to the trash, or deletes it for good with ?permanent=1.
func (s *Server) handleDeleteRecording(w http.ResponseWriter, r *http.Request) {
	acc, id := accountFrom(r.Context()), chi.URLParam(r, "id")
	if permanent, _ := strconv.ParseBool(r.URL.Query().Get("permanent")); permanent {
		if err := s.actions.Delete(r.Context(), acc, id); err != nil {
			s.writeErr(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	rec, err := s.actions.Trash(r.Context(), acc, id)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// handleRestoreRecording takes a note out of the trash.
func (s *Server) handleRestoreRecording(w http.ResponseWriter, r *http.Request) {
	rec, err := s.actions.Restore(r.Context(), accountFrom(r.Context()), chi.URLParam(r, "id"))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// handleEmptyTrash deletes all notes in the trash for good.
func (s *Server) handleEmptyTrash(w http.ResponseWriter, r *http.Request) {
	if _, err := s.actions.EmptyTrash(r.Context(), accountFrom(r.Context())); err != nil {
		s.writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleCreateTextNote creates a text note from a JSON body with its title and Markdown text,
// and optionally the note it is a sub-note of.
func (s *Server) handleCreateTextNote(w http.ResponseWriter, r *http.Request) {
	var in service.TextNoteInput
	if !decode(w, r, &in) {
		return
	}
	rec, err := s.actions.CreateText(r.Context(), accountFrom(r.Context()), in)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, rec)
}

// handleCreateBoard creates a board note from a JSON body with its title and optionally its
// scope and columns.
func (s *Server) handleCreateBoard(w http.ResponseWriter, r *http.Request) {
	var in service.BoardInput
	if !decode(w, r, &in) {
		return
	}
	rec, err := s.actions.CreateBoard(r.Context(), accountFrom(r.Context()), in)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, rec)
}

// handleSetBoard replaces a board's scope, columns and card placements.
func (s *Server) handleSetBoard(w http.ResponseWriter, r *http.Request) {
	var in recording.Board
	if !decode(w, r, &in) {
		return
	}
	rec, err := s.actions.SetBoard(r.Context(), accountFrom(r.Context()), chi.URLParam(r, "id"), in)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// handleEditSummary saves a person's edits of a summary (title and Markdown text), which is
// also how text notes are edited.
func (s *Server) handleEditSummary(w http.ResponseWriter, r *http.Request) {
	var in service.SummaryEdit
	if !decode(w, r, &in) {
		return
	}
	rec, err := s.actions.EditSummary(r.Context(), accountFrom(r.Context()), chi.URLParam(r, "id"), in)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

func (s *Server) handleRetranscribe(w http.ResponseWriter, r *http.Request) {
	rec, err := s.actions.Retranscribe(r.Context(), accountFrom(r.Context()), chi.URLParam(r, "id"))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// handleResummarize summarizes again, optionally with new options (language, model, theme)
// given as a JSON body.
func (s *Server) handleResummarize(w http.ResponseWriter, r *http.Request) {
	var opts *recording.SummaryOptions
	if r.ContentLength != 0 && r.Body != nil {
		var o recording.SummaryOptions
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&o); err == nil {
			opts = &o
		} else if !errors.Is(err, io.EOF) {
			writeCode(w, http.StatusBadRequest, "invalid_request", "invalid request body")
			return
		}
	}
	rec, err := s.actions.Resummarize(r.Context(), accountFrom(r.Context()), chi.URLParam(r, "id"), opts)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// handleNameSpeakers gives several speakers of the transcript names, optionally making the
// summary again with them.
func (s *Server) handleNameSpeakers(w http.ResponseWriter, r *http.Request) {
	var in service.SpeakerNames
	if !decode(w, r, &in) {
		return
	}
	rec, err := s.actions.NameSpeakers(r.Context(), accountFrom(r.Context()), chi.URLParam(r, "id"), in)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// handleRenameSpeaker gives a speaker of the transcript a name.
func (s *Server) handleRenameSpeaker(w http.ResponseWriter, r *http.Request) {
	var in service.SpeakerRename
	if !decode(w, r, &in) {
		return
	}
	rec, err := s.actions.RenameSpeaker(r.Context(), accountFrom(r.Context()), chi.URLParam(r, "id"), in)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}
