package http

import (
	"net/http"
	"time"
)

// restoreConfirmation must be given as ?confirm= to restore, since a restore replaces all data.
const restoreConfirmation = "replace-all-data"

// personalRestoreConfirmation must be given as ?confirm= to restore a personal backup, since
// it replaces all of the user's notes and content.
const personalRestoreConfirmation = "replace-my-data"

// countingWriter counts what was written to the response.
type countingWriter struct {
	w http.ResponseWriter
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

// handleBackup streams a full backup (database and objects) as a zip file.
func (s *Server) handleBackup(w http.ResponseWriter, r *http.Request) {
	if s.backup == nil {
		writeCode(w, http.StatusNotImplemented, "not_available", "backups are not available")
		return
	}
	name := "knowpod-backup-" + s.now().UTC().Format("20060102-150405") + ".zip"
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Cache-Control", "no-store")
	cw := &countingWriter{w: w}
	sum, err := s.backup.Backup(r.Context(), cw)
	if err != nil {
		if cw.n == 0 {
			w.Header().Del("Content-Disposition")
			s.writeErr(w, err)
			return
		}
		// The download is under way; cut the connection so the file isn't taken for whole.
		s.log.Error("backup failed", "err", err)
		panic(http.ErrAbortHandler)
	}
	s.log.Info("backup made", "objects", sum.Objects, "objectBytes", sum.ObjectBytes)
}

// handleRestore replaces all data with the backup sent as the request body.
func (s *Server) handleRestore(w http.ResponseWriter, r *http.Request) {
	if s.backup == nil {
		writeCode(w, http.StatusNotImplemented, "not_available", "backups are not available")
		return
	}
	if r.URL.Query().Get("confirm") != restoreConfirmation {
		writeCode(w, http.StatusBadRequest, "confirmation_required",
			"a restore replaces all data; add ?confirm="+restoreConfirmation)
		return
	}
	start := time.Now()
	sum, err := s.backup.Restore(r.Context(), r.Body)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	s.log.Warn("data restored from a backup", "backupFrom", sum.CreatedAt, "objects", sum.Objects, "took", time.Since(start))
	writeJSON(w, http.StatusOK, sum)
}

// handlePersonalBackup streams a backup of the signed-in user's notes and content as a zip
// file.
func (s *Server) handlePersonalBackup(w http.ResponseWriter, r *http.Request) {
	if s.personalBackup == nil {
		writeCode(w, http.StatusNotImplemented, "not_available", "backups are not available")
		return
	}
	name := "knowpod-notes-" + s.now().UTC().Format("20060102-150405") + ".zip"
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Cache-Control", "no-store")
	cw := &countingWriter{w: w}
	sum, err := s.personalBackup.Backup(r.Context(), accountFrom(r.Context()), cw)
	if err != nil {
		if cw.n == 0 {
			w.Header().Del("Content-Disposition")
			s.writeErr(w, err)
			return
		}
		// The download is under way; cut the connection so the file isn't taken for whole.
		s.log.Error("personal backup failed", "err", err)
		panic(http.ErrAbortHandler)
	}
	s.log.Info("personal backup made", "user", accountFrom(r.Context()).ID, "objects", sum.Objects, "objectBytes", sum.ObjectBytes)
}

// handlePersonalRestore replaces the signed-in user's notes and content with the backup sent
// as the request body.
func (s *Server) handlePersonalRestore(w http.ResponseWriter, r *http.Request) {
	if s.personalBackup == nil {
		writeCode(w, http.StatusNotImplemented, "not_available", "backups are not available")
		return
	}
	if r.URL.Query().Get("confirm") != personalRestoreConfirmation {
		writeCode(w, http.StatusBadRequest, "confirmation_required",
			"a restore replaces all your notes and content; add ?confirm="+personalRestoreConfirmation)
		return
	}
	acc := accountFrom(r.Context())
	start := time.Now()
	sum, err := s.personalBackup.Restore(r.Context(), acc, r.Body)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	s.log.Warn("user data restored from a personal backup", "user", acc.ID, "backupFrom", sum.CreatedAt, "objects", sum.Objects, "took", time.Since(start))
	writeJSON(w, http.StatusOK, sum)
}
