package http

import (
	"bufio"
	"net/http"
	"strings"
	"testing"
)

func TestNotificationStream(t *testing.T) {
	f := newAPIFixture(t)
	c := f.signedIn(adminEmail, adminPassword)

	// Without Web Push and without a listening app, there is nothing to test.
	if res := c.do("POST", "/api/v1/me/notifications/test", nil, nil, nil); res.StatusCode != http.StatusConflict {
		t.Fatalf("test without receivers = %d", res.StatusCode)
	}
	if res := f.browser().do("GET", "/api/v1/me/notifications/stream", nil, nil, nil); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("signed-out stream = %d", res.StatusCode)
	}

	req, _ := http.NewRequest("GET", f.srv.URL+"/api/v1/me/notifications/stream", nil)
	res, err := c.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("stream = %d %s", res.StatusCode, res.Header.Get("Content-Type"))
	}
	lines := bufio.NewScanner(res.Body)
	if !lines.Scan() || lines.Text() != "retry: 5000" || !lines.Scan() || lines.Text() != "" {
		t.Fatalf("stream opened with %q", lines.Text())
	}

	var status struct {
		Listening int `json:"listening"`
	}
	c.do("GET", "/api/v1/me/notifications", nil, nil, &status)
	if status.Listening != 1 {
		t.Errorf("listening = %d", status.Listening)
	}
	var sent struct {
		Sent int `json:"sent"`
	}
	if res := c.do("POST", "/api/v1/me/notifications/test", nil, nil, &sent); res.StatusCode != 200 || sent.Sent != 1 {
		t.Fatalf("test = %d, sent %d", res.StatusCode, sent.Sent)
	}
	var got []string
	for lines.Scan() && lines.Text() != "" {
		got = append(got, lines.Text())
	}
	if len(got) != 2 || got[0] != "event: notification" || !strings.HasPrefix(got[1], `data: {"title":"knowpod"`) || !strings.Contains(got[1], `"tag":"test"`) {
		t.Errorf("event %q", got)
	}

	// Shutting down ends the stream.
	f.notifications.Shutdown()
	for lines.Scan() {
	}
	if c.do("GET", "/api/v1/me/notifications", nil, nil, &status); status.Listening != 0 {
		t.Errorf("listening after shutdown = %d", status.Listening)
	}
}
