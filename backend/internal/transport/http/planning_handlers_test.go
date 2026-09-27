package http

import (
	"strings"
	"testing"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/filter"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/service"
)

func TestFilterTimerAndCalendarEndpoints(t *testing.T) {
	f := newAPIFixture(t)
	c := f.signedIn(adminEmail, adminPassword)

	// Saved filters, shown by a board.
	var saved filter.Filter
	if res := c.do("POST", "/api/v1/filters", map[string]any{"name": "Soon", "query": "due:week & !done", "pinned": true}, nil, &saved); res.StatusCode != 201 || saved.ID == "" {
		t.Fatalf("create filter: %d", res.StatusCode)
	}
	var list []filter.Filter
	if res := c.do("GET", "/api/v1/filters", nil, nil, &list); res.StatusCode != 200 || len(list) != 1 || !list[0].Pinned {
		t.Fatalf("list filters: %d %+v", res.StatusCode, list)
	}
	var board recording.Recording
	if res := c.do("POST", "/api/v1/recordings/board", map[string]any{"title": "Soon", "board": map[string]any{"scope": map[string]string{"kind": "filter", "id": saved.ID}}}, nil, &board); res.StatusCode != 201 {
		t.Fatalf("board: %d", res.StatusCode)
	}

	// An estimate and the timer.
	var note recording.Recording
	if res := c.do("POST", "/api/v1/recordings/text", map[string]any{"title": "Call Anna", "markdown": "", "due": map[string]any{"date": "2099-10-01", "time": "15:00"}}, nil, &note); res.StatusCode != 201 {
		t.Fatalf("note: %d", res.StatusCode)
	}
	var got recording.Recording
	if res := c.do("PUT", "/api/v1/recordings/"+note.ID+"/estimate", map[string]int{"minutes": 45}, nil, &got); res.StatusCode != 200 || got.Estimate != 45 {
		t.Fatalf("estimate: %d %+v", res.StatusCode, got.Estimate)
	}
	var timer struct {
		Timer *service.TimeEntryView `json:"timer"`
	}
	if res := c.do("POST", "/api/v1/timer", map[string]any{"noteId": note.ID, "minutes": 25}, nil, &timer); res.StatusCode != 200 || timer.Timer == nil || timer.Timer.Until == nil {
		t.Fatalf("start: %d %+v", res.StatusCode, timer.Timer)
	}
	if res := c.do("GET", "/api/v1/timer", nil, nil, &timer); res.StatusCode != 200 || timer.Timer == nil || timer.Timer.NoteTitle != "Call Anna" {
		t.Fatalf("running: %d %+v", res.StatusCode, timer.Timer)
	}
	var stopped struct {
		Stopped *service.TimeEntryView `json:"stopped"`
	}
	if res := c.do("DELETE", "/api/v1/timer", nil, nil, &stopped); res.StatusCode != 200 || stopped.Stopped == nil || stopped.Stopped.End == nil {
		t.Fatalf("stop: %d %+v", res.StatusCode, stopped.Stopped)
	}
	var entries []service.TimeEntryView
	if res := c.do("GET", "/api/v1/time-entries", nil, nil, &entries); res.StatusCode != 200 || len(entries) != 1 {
		t.Fatalf("log: %d %+v", res.StatusCode, entries)
	}
	var csv []byte
	res := c.do("GET", "/api/v1/time-entries/export?from=2026-01-01&to=2026-12-31", nil, nil, &csv)
	if res.StatusCode != 200 || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/csv") || !strings.HasPrefix(string(csv), "date,start,end,minutes") {
		t.Errorf("export: %d %s %q", res.StatusCode, res.Header.Get("Content-Type"), csv)
	}
	if res := c.do("GET", "/api/v1/time-entries?from=soon", nil, nil, nil); res.StatusCode != 400 {
		t.Errorf("bad range: %d", res.StatusCode)
	}
	if res := c.do("DELETE", "/api/v1/time-entries/"+entries[0].ID, nil, nil, nil); res.StatusCode != 204 {
		t.Errorf("delete entry: %d", res.StatusCode)
	}

	// The calendar feed is read with the link alone.
	var cal service.CalendarView
	if res := c.do("POST", "/api/v1/me/calendar", nil, nil, &cal); res.StatusCode != 200 || cal.FeedPath == "" {
		t.Fatalf("enable calendar: %d %+v", res.StatusCode, cal)
	}
	var ics []byte
	res = f.browser().do("GET", cal.FeedPath, nil, nil, &ics)
	if res.StatusCode != 200 || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/calendar") ||
		!strings.Contains(string(ics), "SUMMARY:Call Anna") || !strings.Contains(string(ics), "/conversations/"+note.ID) {
		t.Fatalf("feed: %d %s\n%s", res.StatusCode, res.Header.Get("Content-Type"), ics)
	}
	if res := f.browser().do("GET", "/api/v1/calendar/kpc_wrong.ics", nil, nil, nil); res.StatusCode != 404 {
		t.Errorf("wrong token: %d", res.StatusCode)
	}
	var status service.CalendarView
	if res := c.do("GET", "/api/v1/me/calendar", nil, nil, &status); res.StatusCode != 200 || !status.Enabled || status.FeedPath != "" {
		t.Errorf("calendar status: %d %+v", res.StatusCode, status)
	}
	if res := c.do("DELETE", "/api/v1/me/calendar", nil, nil, nil); res.StatusCode != 204 {
		t.Errorf("disable: %d", res.StatusCode)
	}
	if res := f.browser().do("GET", cal.FeedPath, nil, nil, nil); res.StatusCode == 200 {
		t.Errorf("feed still served after turning it off")
	}

	if res := c.do("DELETE", "/api/v1/filters/"+saved.ID, nil, nil, nil); res.StatusCode != 204 {
		t.Errorf("delete filter: %d", res.StatusCode)
	}
}
