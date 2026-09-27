// Package timelog models the time people spend on their notes: each entry is one stretch of
// work on one note, from starting its timer to stopping it (or entered by hand).
package timelog

import "time"

// MaxEntry is the longest a single entry can be; a timer left running longer is stopped
// there.
const MaxEntry = 24 * time.Hour

// Entry is time spent on a note. A running timer is an entry without End.
type Entry struct {
	ID      string     `bson:"_id" json:"id"`
	OwnerID string     `bson:"ownerId" json:"-"`
	NoteID  string     `bson:"noteId" json:"noteId"`
	Start   time.Time  `bson:"start" json:"start"`
	End     *time.Time `bson:"end,omitempty" json:"end,omitempty"`
	// Until is when a focus session (Pomodoro) ends: a running entry stops there by itself.
	Until     *time.Time `bson:"until,omitempty" json:"until,omitempty"`
	CreatedAt time.Time  `bson:"createdAt" json:"createdAt"`
	// Open marks the running timer in the database, which keeps one per user; it is
	// cleared together with setting End.
	Open bool `bson:"running,omitempty" json:"-"`
}

// Running reports whether the entry is a timer still counting.
func (e *Entry) Running() bool { return e.End == nil }

// Seconds is how long the entry lasted (a running one up to now).
func (e *Entry) Seconds(now time.Time) int64 {
	end := now
	if e.End != nil {
		end = *e.End
	}
	if end.Before(e.Start) {
		return 0
	}
	return int64(end.Sub(e.Start) / time.Second)
}

// Lapsed returns when a running entry has stopped by itself by now: at the end of its focus
// session, or after MaxEntry. It returns nil while it still runs.
func (e *Entry) Lapsed(now time.Time) *time.Time {
	if e.End != nil {
		return nil
	}
	limit := e.Start.Add(MaxEntry)
	if e.Until != nil && e.Until.Before(limit) {
		limit = *e.Until
	}
	if now.Before(limit) {
		return nil
	}
	return &limit
}

// Range selects the entries that started in [From, To).
type Range struct {
	OwnerID  string
	From, To time.Time
}
