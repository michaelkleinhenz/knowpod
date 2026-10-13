package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/user"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/repository/memory"
)

func newAssistant(t *testing.T, ai *fakeAI) (*AssistantService, *Account) {
	t.Helper()
	users := memory.NewUsers()
	if err := users.Create(context.Background(), &user.User{ID: "u1", Email: "a@example.com", Role: user.RoleUser}); err != nil {
		t.Fatal(err)
	}
	s := NewAssistantService(users, ai, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s.clock = func() time.Time { return time.Date(2026, 10, 13, 9, 0, 0, 0, time.UTC) }
	return s, &Account{ID: "u1", Role: user.RoleUser}
}


func TestAssistantSettings(t *testing.T) {
	ctx := context.Background()
	s, acc := newAssistant(t, &fakeAI{})
	v, err := s.Settings(ctx, acc)
	if err != nil || v.Configured || v.APIKeyConfigured {
		t.Fatalf("settings = %+v, %v", v, err)
	}
	v, err = s.UpdateSettings(ctx, acc, AssistantUpdate{APIKey: ptr(" sk-test-12345678 "), Model: ptr("anthropic/claude-sonnet-4"), SystemPrompt: ptr("Be brief."), WebSearch: ptr(true)})
	if err != nil || !v.Configured || v.APIKeyHint != "…5678" || v.Model != "anthropic/claude-sonnet-4" || v.SystemPrompt != "Be brief." || !v.WebSearch {
		t.Fatalf("settings = %+v, %v", v, err)
	}
	// Fields left out stay; an empty key removes it.
	v, err = s.UpdateSettings(ctx, acc, AssistantUpdate{APIKey: ptr("")})
	if err != nil || v.Configured || v.APIKeyConfigured || v.Model != "anthropic/claude-sonnet-4" || v.SystemPrompt != "Be brief." {
		t.Fatalf("settings = %+v, %v", v, err)
	}
	if _, err := s.UpdateSettings(ctx, acc, AssistantUpdate{Model: ptr("not a model")}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("bad model: %v", err)
	}
	if _, err := s.UpdateSettings(ctx, acc, AssistantUpdate{SystemPrompt: ptr(strings.Repeat("x", maxAssistantSystemPrompt+1))}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("long system prompt: %v", err)
	}
	if _, err := s.Settings(ctx, &Account{All: true}); !errors.Is(err, ErrForbidden) {
		t.Fatalf("admin token: %v", err)
	}
}

func TestAssistantEdit(t *testing.T) {
	ctx := context.Background()
	ai := &fakeAI{answer: "```markdown\n- [ ] Milk (supermarket)\n- [ ] Nails (hardware store)\n```"}
	s, acc := newAssistant(t, ai)
	in := AssistantEdit{Title: "Shopping", Markdown: "- [ ] Milk\n- [ ] Nails", Prompt: "Add where to buy each item"}
	if _, err := s.Edit(ctx, acc, in); !errors.Is(err, ErrNotReady) {
		t.Fatalf("not set up: %v", err)
	}
	if _, err := s.UpdateSettings(ctx, acc, AssistantUpdate{APIKey: ptr("sk-test"), Model: ptr("openai/gpt-5"), SystemPrompt: ptr("I live in Berlin."), WebSearch: ptr(true)}); err != nil {
		t.Fatal(err)
	}
	out, err := s.Edit(ctx, acc, in)
	if err != nil || out.Markdown != "- [ ] Milk (supermarket)\n- [ ] Nails (hardware store)" || out.Model != "openai/gpt-5" || out.Title != "" {
		t.Fatalf("edit = %+v, %v", out, err)
	}
	r := ai.requests[0]
	system, user := r.Messages[0].Content.(string), r.Messages[1].Content.(string)
	if r.Model != "openai/gpt-5" || !r.WebSearch || !strings.Contains(system, "I live in Berlin.") || !strings.Contains(system, "Tuesday, 13 October 2026") {
		t.Fatalf("request = %+v", r)
	}
	if !strings.Contains(user, "Title: Shopping") || !strings.Contains(user, "- [ ] Nails") || !strings.Contains(user, "Add where to buy each item") {
		t.Fatalf("message = %q", user)
	}
	if _, err := s.Edit(ctx, acc, AssistantEdit{Markdown: "x"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("no prompt: %v", err)
	}
	ai.err = errors.New("model down")
	if _, err := s.Edit(ctx, acc, in); !errors.Is(err, ErrAssistantFailed) {
		t.Fatalf("model error: %v", err)
	}
}

func TestAssistantCreate(t *testing.T) {
	ctx := context.Background()
	ai := &fakeAI{answer: "# Repairing a radio\n\n1. Unplug it.\n2. Open the case."}
	s, acc := newAssistant(t, ai)
	if _, err := s.UpdateSettings(ctx, acc, AssistantUpdate{APIKey: ptr("sk-test"), Model: ptr("openai/gpt-5")}); err != nil {
		t.Fatal(err)
	}
	out, err := s.Create(ctx, acc, AssistantCreate{Prompt: "Steps to repair a radio"})
	if err != nil || out.Title != "Repairing a radio" || out.Markdown != "1. Unplug it.\n2. Open the case." {
		t.Fatalf("create = %+v, %v", out, err)
	}
	if ai.requests[0].WebSearch || strings.Contains(ai.requests[0].Messages[0].Content.(string), "own instructions") {
		t.Fatalf("request = %+v", ai.requests[0])
	}
	// Without a heading, the request names the note.
	ai.answer = "Just text."
	out, err = s.Create(ctx, acc, AssistantCreate{Prompt: "Packing list\nfor a weekend"})
	if err != nil || out.Title != "Packing list" || out.Markdown != "Just text." {
		t.Fatalf("create = %+v, %v", out, err)
	}
}
