package service

import (
	"fmt"
	"sort"
	"time"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
)

const (
	maxHighlights      = 1000
	maxHighlightOffset = 24 * time.Hour
)

// HighlightInput is a highlight as sent by a device: either the offset from the start of
// the recording, or the wall-clock time (converted using the recording's recordedAt).
type HighlightInput struct {
	OffsetMs *int64     `json:"offsetMs,omitempty"`
	At       *time.Time `json:"at,omitempty"`
}

// normalizeHighlights validates highlights and returns them sorted by position, without
// duplicates.
func normalizeHighlights(in []HighlightInput, recordedAt *time.Time) ([]recording.Highlight, error) {
	if len(in) > maxHighlights {
		return nil, invalid("at most %d highlights", maxHighlights)
	}
	out := make([]recording.Highlight, 0, len(in))
	for i, h := range in {
		var hl recording.Highlight
		switch {
		case h.OffsetMs != nil && h.At != nil:
			return nil, invalid("highlight %d: give either offsetMs or at, not both", i)
		case h.OffsetMs != nil:
			hl.OffsetMs = *h.OffsetMs
		case h.At != nil:
			if recordedAt == nil {
				return nil, invalid("highlight %d: \"at\" needs the recording's recordedAt; send offsetMs instead", i)
			}
			at := h.At.UTC()
			hl.At = &at
			hl.OffsetMs = at.Sub(*recordedAt).Milliseconds()
		default:
			return nil, invalid("highlight %d: offsetMs or at is required", i)
		}
		if hl.OffsetMs < 0 || hl.OffsetMs > maxHighlightOffset.Milliseconds() {
			return nil, invalid("highlight %d is outside the recording (offset %d ms)", i, hl.OffsetMs)
		}
		out = append(out, hl)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].OffsetMs < out[j].OffsetMs })
	dedup := out[:0]
	for _, h := range out {
		if len(dedup) == 0 || dedup[len(dedup)-1].OffsetMs != h.OffsetMs {
			dedup = append(dedup, h)
		}
	}
	return dedup, nil
}

// formatOffset formats a position as m:ss, or h:mm:ss from one hour on.
func formatOffset(ms int64) string {
	d := time.Duration(ms) * time.Millisecond
	h, m, s := int(d.Hours()), int(d.Minutes())%60, int(d.Seconds())%60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}
