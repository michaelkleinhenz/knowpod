package http

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/config"
)

// buildTestRouter wires a Server with just enough for the middleware/routing smoke tests.
func buildTestRouter() http.Handler {
	s := NewServer(Deps{Cfg: config.Config{FrontendURL: "http://localhost:5173"}})
	return s.Router()
}

func TestHealthEndpoint(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	buildTestRouter().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("healthz = %d", rec.Code)
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("security headers missing")
	}
}

type fakePinger struct{ err error }

func (f fakePinger) Ping(context.Context) error { return f.err }

func TestHealthEndpointReportsDatabaseDown(t *testing.T) {
	s := NewServer(Deps{DB: fakePinger{err: errors.New("down")}})
	rec := httptest.NewRecorder()
	s.Router().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("healthz = %d, want 503", rec.Code)
	}
}

func TestInfoEndpoint(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/info", nil)
	rec := httptest.NewRecorder()
	buildTestRouter().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("info = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), serviceName) {
		t.Fatalf("body = %q", rec.Body.String())
	}
}

func TestClientRouteServesSPA(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/some/client/route", nil)
	rec := httptest.NewRecorder()
	buildTestRouter().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("spa fallback = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("content-type = %q", ct)
	}
}

func TestUnknownAPIRouteIsJSON404(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/does-not-exist", nil)
	rec := httptest.NewRecorder()
	buildTestRouter().ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("content-type = %q", ct)
	}
}
