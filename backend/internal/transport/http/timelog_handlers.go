package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/service"
)

// handleSetNoteEstimate sets how many minutes a task is expected to take (0 clears it).
func (s *Server) handleSetNoteEstimate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Minutes int `json:"minutes"`
	}
	if !decode(w, r, &in) {
		return
	}
	rec, err := s.actions.SetEstimate(r.Context(), accountFrom(r.Context()), chi.URLParam(r, "id"), in.Minutes)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rec)
}

// timerResult is the running timer, or null.
type timerResult struct {
	Timer *service.TimeEntryView `json:"timer"`
}

// handleGetTimer returns the running timer.
func (s *Server) handleGetTimer(w http.ResponseWriter, r *http.Request) {
	e, err := s.times.Running(r.Context(), accountFrom(r.Context()))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, timerResult{Timer: e})
}

// handleStartTimer starts the timer on a note (a focus session with minutes), stopping the
// one that was running.
func (s *Server) handleStartTimer(w http.ResponseWriter, r *http.Request) {
	var in service.TimerStart
	if !decode(w, r, &in) {
		return
	}
	e, err := s.times.Start(r.Context(), accountFrom(r.Context()), in)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, timerResult{Timer: e})
}

// stoppedResult is the timer that was stopped (null when none ran).
type stoppedResult struct {
	Stopped *service.TimeEntryView `json:"stopped"`
}

// handleStopTimer stops the running timer.
func (s *Server) handleStopTimer(w http.ResponseWriter, r *http.Request) {
	e, err := s.times.Stop(r.Context(), accountFrom(r.Context()))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, stoppedResult{Stopped: e})
}

func logRange(r *http.Request) service.LogRange {
	q := r.URL.Query()
	return service.LogRange{From: q.Get("from"), To: q.Get("to")}
}

// handleListTimeEntries returns the time log of the days from ?from= to ?to= (the current
// week without them).
func (s *Server) handleListTimeEntries(w http.ResponseWriter, r *http.Request) {
	list, err := s.times.List(r.Context(), accountFrom(r.Context()), logRange(r))
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, list)
}

// handleExportTimeEntries returns the time log of a period as a CSV file.
func (s *Server) handleExportTimeEntries(w http.ResponseWriter, r *http.Request) {
	rg := logRange(r)
	data, err := s.times.Export(r.Context(), accountFrom(r.Context()), rg)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	name := "knowpod-time.csv"
	if rg.From != "" && rg.To != "" {
		name = "knowpod-time-" + rg.From + "-to-" + rg.To + ".csv"
	}
	sendFile(w, "text/csv; charset=utf-8", name, string(data))
}

// handleCreateTimeEntry logs time spent on a note by hand.
func (s *Server) handleCreateTimeEntry(w http.ResponseWriter, r *http.Request) {
	var in service.TimeEntryInput
	if !decode(w, r, &in) {
		return
	}
	e, err := s.times.Add(r.Context(), accountFrom(r.Context()), in)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, e)
}

func (s *Server) handleDeleteTimeEntry(w http.ResponseWriter, r *http.Request) {
	if err := s.times.Delete(r.Context(), accountFrom(r.Context()), chi.URLParam(r, "id")); err != nil {
		s.writeErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
