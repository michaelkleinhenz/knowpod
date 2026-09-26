package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/michaelkleinhenz/knowpod-service/backend/api"
)

var pathParam = regexp.MustCompile(`\{[^}]+\}`)

// TestOpenAPICoversAllRoutes fails when an /api/v1 route is missing from openapi.yaml or the
// spec documents a route that doesn't exist, so the API docs shown in the web UI stay accurate.
func TestOpenAPICoversAllRoutes(t *testing.T) {
	doc, err := api.OpenAPIJSON()
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Paths map[string]map[string]any `json:"paths"`
	}
	if err := json.Unmarshal(doc, &spec); err != nil {
		t.Fatal(err)
	}
	documented := map[string]bool{}
	for path, ops := range spec.Paths {
		for method := range ops {
			if method != "parameters" {
				documented[strings.ToUpper(method)+" "+pathParam.ReplaceAllString(path, "{}")] = true
			}
		}
	}

	routes := map[string]bool{}
	err = chi.Walk(NewServer(Deps{}).Router().(chi.Routes), func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if strings.HasPrefix(route, "/api/v1/") {
			routes[method+" "+pathParam.ReplaceAllString(strings.TrimPrefix(route, "/api/v1"), "{}")] = true
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(routes) < 10 {
		t.Fatalf("found only %d routes; is the walk broken?", len(routes))
	}
	for r := range routes {
		if !documented[r] {
			t.Errorf("route %s is not documented in openapi.yaml", r)
		}
	}
	for d := range documented {
		if !routes[d] {
			t.Errorf("openapi.yaml documents %s, which is not routed", d)
		}
	}
}

func TestOpenAPIServed(t *testing.T) {
	for path, ctype := range map[string]string{"/api/v1/openapi.yaml": "application/yaml", "/api/v1/openapi.json": "application/json"} {
		rec := httptest.NewRecorder()
		buildTestRouter().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), ctype) || !strings.Contains(rec.Body.String(), "openapi") {
			t.Errorf("%s: %d %s", path, rec.Code, rec.Header().Get("Content-Type"))
		}
	}
}
