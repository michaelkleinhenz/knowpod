// Package worker runs the background processing pipeline. Each stage moves recordings from
// one status to the next (received → stored today; transcription and AI steps can be added
// as further stages). Work is claimed through the database with a lease, failures are
// retried with exponential backoff, and a recording fails permanently after MaxAttempts.
package worker

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/ports"
)

// Stage is one processing step.
type Stage struct {
	Name     string
	From, To recording.Status
	// Run processes the recording and may update its fields; the worker persists them
	// together with the new status.
	Run func(ctx context.Context, rec *recording.Recording) error
	// Cleanup runs after the new status has been persisted. Optional.
	Cleanup func(rec *recording.Recording)
}

// Options tunes the worker. Zero values get defaults.
type Options struct {
	PollInterval time.Duration // how often to look for due work without a wake-up (default 10s)
	Lease        time.Duration // how long a claimed recording is reserved (default 15m)
	MaxAttempts  int           // attempts per stage before failing the recording (default 5)
	BaseBackoff  time.Duration // delay after the first failure, doubled each time (default 30s)
	MaxBackoff   time.Duration // cap for the delay (default 1h)
}

// Worker processes recordings through the stages.
type Worker struct {
	recs   ports.RecordingRepository
	stages []Stage
	opts   Options
	log    *slog.Logger
	clock  func() time.Time
	wake   chan struct{}
}

// New builds a worker.
func New(recs ports.RecordingRepository, stages []Stage, opts Options, log *slog.Logger) *Worker {
	if opts.PollInterval <= 0 {
		opts.PollInterval = 10 * time.Second
	}
	if opts.Lease <= 0 {
		opts.Lease = 15 * time.Minute
	}
	if opts.MaxAttempts <= 0 {
		opts.MaxAttempts = 5
	}
	if opts.BaseBackoff <= 0 {
		opts.BaseBackoff = 30 * time.Second
	}
	if opts.MaxBackoff <= 0 {
		opts.MaxBackoff = time.Hour
	}
	return &Worker{recs: recs, stages: stages, opts: opts, log: log, clock: time.Now, wake: make(chan struct{}, 1)}
}

// Wake makes the worker look for work immediately. It never blocks.
func (w *Worker) Wake() {
	select {
	case w.wake <- struct{}{}:
	default:
	}
}

// Start processes work until ctx is cancelled.
func (w *Worker) Start(ctx context.Context) {
	ticker := time.NewTicker(w.opts.PollInterval)
	defer ticker.Stop()
	for {
		w.RunOnce(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-w.wake:
		}
	}
}

// RunOnce processes all currently due work and returns the number of recordings handled.
func (w *Worker) RunOnce(ctx context.Context) int {
	n := 0
	for progress := true; progress && ctx.Err() == nil; {
		progress = false
		for _, st := range w.stages {
			for ctx.Err() == nil {
				now := w.clock().UTC()
				rec, err := w.recs.Claim(ctx, st.From, now, now.Add(w.opts.Lease))
				if errors.Is(err, domain.ErrNotFound) {
					break
				}
				if err != nil {
					w.log.Error("claiming work failed", "stage", st.Name, "err", err)
					return n
				}
				w.process(ctx, st, rec)
				n++
				progress = true
			}
		}
	}
	return n
}

func (w *Worker) process(ctx context.Context, st Stage, rec *recording.Recording) {
	err := st.Run(ctx, rec)
	now := w.clock().UTC()
	rec.UpdatedAt = now
	if err == nil {
		rec.Status = st.To
		rec.Attempts = 0
		rec.LastError = ""
		rec.NotBefore = now
		if uerr := w.recs.Update(ctx, rec); uerr != nil {
			// The lease expires and the stage is retried.
			w.log.Error("saving stage result failed", "stage", st.Name, "id", rec.ID, "err", uerr)
			return
		}
		if st.Cleanup != nil {
			st.Cleanup(rec)
		}
		return
	}

	if ctx.Err() != nil {
		// Shutting down: record no error or backoff; the lease expires and the stage is
		// retried. The attempt was already counted when the recording was claimed.
		return
	}
	rec.LastError = err.Error()
	if rec.Attempts >= w.opts.MaxAttempts {
		rec.Status = recording.StatusFailed
		w.log.Error("stage failed permanently", "stage", st.Name, "id", rec.ID, "attempts", rec.Attempts, "err", err)
	} else {
		rec.NotBefore = now.Add(w.backoff(rec.Attempts))
		w.log.Warn("stage failed, will retry", "stage", st.Name, "id", rec.ID, "attempts", rec.Attempts,
			"retryAt", rec.NotBefore, "err", err)
	}
	if uerr := w.recs.Update(ctx, rec); uerr != nil {
		w.log.Error("saving stage failure failed", "stage", st.Name, "id", rec.ID, "err", uerr)
	}
}

func (w *Worker) backoff(attempts int) time.Duration {
	d := w.opts.BaseBackoff
	for i := 1; i < attempts && d < w.opts.MaxBackoff; i++ {
		d *= 2
	}
	return min(d, w.opts.MaxBackoff)
}
