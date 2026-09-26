package openrouter

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestComplete(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" || r.Header.Get("Authorization") != "Bearer sk-test" {
			http.Error(w, "bad request", 400)
			return
		}
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"Hello there."},"finish_reason":"stop"}]}`))
	}))
	defer srv.Close()

	text, err := NewClient(srv.URL, "").Complete(context.Background(), "sk-test", Request{
		Model: "google/gemini-2.5-flash", JSON: true,
		Messages: []Message{{Role: "user", Content: []any{Text("Transcribe"), Audio("AAAA", "wav")}}},
	})
	if err != nil || text != "Hello there." {
		t.Fatalf("Complete = %q, %v", text, err)
	}
	// The audio part must have the documented shape.
	content := got["messages"].([]any)[0].(map[string]any)["content"].([]any)
	audio := content[1].(map[string]any)
	if audio["type"] != "input_audio" || audio["input_audio"].(map[string]any)["format"] != "wav" {
		t.Fatalf("audio part = %v", audio)
	}
	if got["response_format"].(map[string]any)["type"] != "json_object" {
		t.Fatalf("response_format = %v", got["response_format"])
	}
}

func TestCompleteErrors(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   string
		want   string
	}{
		"unauthorized": {401, `{"error":{"code":401,"message":"No auth credentials found"}}`, "rejected the API key"},
		"error body":   {400, `{"error":{"code":400,"message":"audio format not supported"}}`, "audio format not supported"},
		"cut off":      {200, `{"choices":[{"message":{"content":"partial"},"finish_reason":"length"}]}`, "cut off"},
		"no choices":   {200, `{"choices":[]}`, "no answer"},
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			_, err := NewClient(srv.URL, "").Complete(context.Background(), "k", Request{Model: "m"})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want %q", err, tc.want)
			}
			if name == "unauthorized" && !errors.Is(err, ErrUnauthorized) {
				t.Fatal("want ErrUnauthorized")
			}
		})
	}
}

func TestModels(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"id":"google/gemini-2.5-flash","name":"Google: Gemini 2.5 Flash","context_length":1048576,
			"architecture":{"input_modalities":["text","image","audio"],"output_modalities":["text"]},
			"pricing":{"prompt":"0.0000003","completion":"0.0000025","audio":"0.000001"}}]}`))
	}))
	defer srv.Close()
	models, err := NewClient(srv.URL, "").Models(context.Background())
	if err != nil || len(models) != 1 {
		t.Fatalf("Models = %v, %v", models, err)
	}
	m := models[0]
	if !m.Accepts("audio") || !m.Produces("text") || m.Accepts("video") || m.AudioPrice != "0.000001" || m.ContextLength != 1048576 {
		t.Fatalf("model = %+v", m)
	}
}
