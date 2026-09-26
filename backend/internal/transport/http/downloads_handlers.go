package http

import (
	"net/http"
	"net/url"
	"strings"
	"unicode"

	"github.com/go-chi/chi/v5"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
)

// handleDownloadTranscript returns the transcript as a text file, or as JSON with
// ?format=json.
func (s *Server) handleDownloadTranscript(w http.ResponseWriter, r *http.Request) {
	rec, err := s.actions.Get(r.Context(), accountFrom(r.Context()), chi.URLParam(r, "id"))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	if rec.Transcript == nil {
		writeCode(w, http.StatusConflict, "not_ready", "the recording has no transcript yet")
		return
	}
	if r.URL.Query().Get("format") == "json" {
		writeJSON(w, http.StatusOK, rec.Transcript)
		return
	}
	sendFile(w, "text/plain; charset=utf-8", fileName(rec, "transcript", "txt"), rec.Transcript.Text+"\n")
}

// handleDownloadSummary returns the summary as a Markdown file (title as the first
// heading), or as JSON with ?format=json.
func (s *Server) handleDownloadSummary(w http.ResponseWriter, r *http.Request) {
	rec, err := s.actions.Get(r.Context(), accountFrom(r.Context()), chi.URLParam(r, "id"))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	if rec.Summary == nil {
		writeCode(w, http.StatusConflict, "not_ready", "the recording has no summary yet")
		return
	}
	if r.URL.Query().Get("format") == "json" {
		writeJSON(w, http.StatusOK, rec.Summary)
		return
	}
	kind := "summary"
	if rec.IsText() {
		kind = "note"
	}
	sendFile(w, "text/markdown; charset=utf-8", fileName(rec, kind, "md"), "# "+rec.Summary.Title+"\n\n"+rec.Summary.Markdown+"\n")
}

func sendFile(w http.ResponseWriter, contentType, name, body string) {
	h := w.Header()
	h.Set("Content-Type", contentType)
	// ASCII fallback plus the UTF-8 name for clients that support it (RFC 6266).
	h.Set("Content-Disposition", `attachment; filename="`+asciiName(name)+`"; filename*=UTF-8''`+url.PathEscape(name))
	h.Set("Cache-Control", "private, no-cache")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(body))
}

// fileName builds a download name from the note's title, e.g. "Team sync – summary.md".
func fileName(rec *recording.Recording, kind, ext string) string {
	title := rec.Title
	if rec.Summary != nil && rec.Summary.Title != "" {
		title = rec.Summary.Title
	}
	title = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || strings.ContainsRune(`/\\:*?"<>|`, r) {
			return -1
		}
		return r
	}, strings.TrimSpace(title))
	if r := []rune(title); len(r) > 80 {
		title = strings.TrimSpace(string(r[:80]))
	}
	if title == "" {
		title = rec.ID
	}
	return title + " - " + kind + "." + ext
}

// asciiName replaces non-ASCII characters for the plain filename parameter.
func asciiName(name string) string {
	return strings.Map(func(r rune) rune {
		if r > 126 || r < 32 || r == '"' || r == '\\' {
			return '_'
		}
		return r
	}, name)
}
