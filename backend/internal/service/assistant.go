package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/user"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/openrouter"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/ports"
)

// ErrAssistantFailed is returned when the user's AI model could not answer.
var ErrAssistantFailed = errors.New("the AI model failed")

// Limits of the assistant's settings and requests, in characters.
const (
	maxAssistantSystemPrompt = 8_000
	maxAssistantPrompt       = 4_000
	maxAssistantModel        = 200
)

// AssistantService runs a user's own AI model, through their own OpenRouter account, to
// work on a note or to write a new one as asked. It changes no notes itself: the web app
// shows the answer, and saves it if the user takes it.
type AssistantService struct {
	users ports.UserRepository
	ai    AIClient
	log   *slog.Logger
	clock func() time.Time
}

// NewAssistantService builds the service.
func NewAssistantService(users ports.UserRepository, ai AIClient, log *slog.Logger) *AssistantService {
	return &AssistantService{users: users, ai: ai, log: log, clock: time.Now}
}

// AssistantSettings are a user's assistant settings as shown to them: the API key itself is
// never returned.
type AssistantSettings struct {
	// Configured tells whether the assistant can be used (a key and a model are set).
	Configured       bool   `json:"configured"`
	APIKeyConfigured bool   `json:"apiKeyConfigured"`
	APIKeyHint       string `json:"apiKeyHint,omitempty"`
	Model            string `json:"model"`
	SystemPrompt     string `json:"systemPrompt"`
	WebSearch        bool   `json:"webSearch"`
}

// AssistantUpdate changes the settings. Nil fields are left unchanged; an empty APIKey
// removes the key.
type AssistantUpdate struct {
	APIKey       *string `json:"apiKey,omitempty"`
	Model        *string `json:"model,omitempty"`
	SystemPrompt *string `json:"systemPrompt,omitempty"`
	WebSearch    *bool   `json:"webSearch,omitempty"`
}

func (s *AssistantService) user(ctx context.Context, acc *Account) (*user.User, error) {
	if acc.ID == "" {
		return nil, errors.Join(ErrForbidden, errors.New("the AI assistant belongs to a user; sign in"))
	}
	return s.users.Get(ctx, acc.ID)
}

// Settings returns the account's assistant settings.
func (s *AssistantService) Settings(ctx context.Context, acc *Account) (*AssistantSettings, error) {
	u, err := s.user(ctx, acc)
	if err != nil {
		return nil, err
	}
	return assistantView(u.Assistant), nil
}

// UpdateSettings changes the account's assistant settings.
func (s *AssistantService) UpdateSettings(ctx context.Context, acc *Account, in AssistantUpdate) (*AssistantSettings, error) {
	u, err := s.user(ctx, acc)
	if err != nil {
		return nil, err
	}
	a := &u.Assistant
	if in.APIKey != nil {
		a.APIKey = strings.TrimSpace(*in.APIKey)
		if len(a.APIKey) > 500 || strings.ContainsAny(a.APIKey, " \t\n") {
			return nil, invalid("that doesn't look like an OpenRouter API key")
		}
	}
	if in.Model != nil {
		a.Model = strings.TrimSpace(*in.Model)
		if len(a.Model) > maxAssistantModel || strings.ContainsAny(a.Model, " \t\n") {
			return nil, invalid("model IDs look like \"anthropic/claude-sonnet-4\"")
		}
	}
	if in.SystemPrompt != nil {
		a.SystemPrompt = strings.TrimSpace(strings.ReplaceAll(*in.SystemPrompt, "\r\n", "\n"))
		if utf8.RuneCountInString(a.SystemPrompt) > maxAssistantSystemPrompt {
			return nil, invalid("the system prompt is at most %d characters", maxAssistantSystemPrompt)
		}
	}
	if in.WebSearch != nil {
		a.WebSearch = *in.WebSearch
	}
	if err := s.users.Update(ctx, u); err != nil {
		return nil, err
	}
	return assistantView(u.Assistant), nil
}

func assistantView(a user.Assistant) *AssistantSettings {
	return &AssistantSettings{Configured: a.Configured(), APIKeyConfigured: a.APIKey != "", APIKeyHint: keyHint(a.APIKey),
		Model: a.Model, SystemPrompt: a.SystemPrompt, WebSearch: a.WebSearch}
}

// AssistantEdit asks the assistant to work on a note: its title and Markdown text, and what
// to do with it.
type AssistantEdit struct {
	Title    string `json:"title"`
	Markdown string `json:"markdown"`
	Prompt   string `json:"prompt"`
}

// AssistantCreate asks the assistant to write a new note. Context is the text of a related
// note (e.g. the one the new note goes under), optional.
type AssistantCreate struct {
	Prompt  string `json:"prompt"`
	Context string `json:"context,omitempty"`
}

// AssistantAnswer is the note the assistant wrote: a title (for new notes) and the text as
// Markdown.
type AssistantAnswer struct {
	Title    string `json:"title,omitempty"`
	Markdown string `json:"markdown"`
	Model    string `json:"model"`
}

const assistantBase = `You are an AI assistant inside a note-taking app. Notes are written in Markdown: headings, lists, checklists ("- [ ] …"), tables (GitHub style), links, and "#12" references to other notes.
Answer with Markdown only: no introduction, no explanation of what you did, no code fence around the answer.
Write in the language of the note, or for a new note in the language of the request, unless asked otherwise.`

const assistantEditTask = `
You work on the note the user gives you, as they ask. Answer with the complete new text of the note (without its title), keeping what the request doesn't ask to change as it is: its structure, formatting, checklists (checked or not), images, links and "#12" references.`

const assistantCreateTask = `
You write a new note as the user asks. Start the answer with its title as a level 1 heading ("# Title", at most 10 words), followed by the text of the note. Give it a clear structure (headings, lists, checklists for steps to take) where that helps.`

// Edit has the account's assistant work on a note.
func (s *AssistantService) Edit(ctx context.Context, acc *Account, in AssistantEdit) (*AssistantAnswer, error) {
	in.Prompt = strings.TrimSpace(in.Prompt)
	in.Title = strings.TrimSpace(in.Title)
	in.Markdown = strings.TrimSpace(strings.ReplaceAll(in.Markdown, "\r\n", "\n"))
	if err := checkPrompt(in.Prompt); err != nil {
		return nil, err
	}
	if len(in.Markdown) > maxSummaryMarkdown {
		return nil, invalid("the note's text is at most %d characters", maxSummaryMarkdown)
	}
	var msg strings.Builder
	msg.WriteString("The note:\n<note>\n")
	if in.Title != "" {
		msg.WriteString("Title: " + truncateRunes(in.Title, 200) + "\n\n")
	}
	msg.WriteString(in.Markdown + "\n</note>\n\nWhat to do with it: " + in.Prompt)
	out, err := s.run(ctx, acc, assistantEditTask, msg.String())
	if err != nil {
		return nil, err
	}
	s.log.Info("note worked on by the assistant", "user", acc.ID, "model", out.Model)
	return out, nil
}

// Create has the account's assistant write a new note.
func (s *AssistantService) Create(ctx context.Context, acc *Account, in AssistantCreate) (*AssistantAnswer, error) {
	in.Prompt = strings.TrimSpace(in.Prompt)
	if err := checkPrompt(in.Prompt); err != nil {
		return nil, err
	}
	var msg strings.Builder
	if c := truncateRunes(in.Context, maxWriteContext); c != "" {
		msg.WriteString("A related note, for context (don't repeat it):\n<note>\n" + c + "\n</note>\n\n")
	}
	msg.WriteString("Write a note: " + in.Prompt)
	out, err := s.run(ctx, acc, assistantCreateTask, msg.String())
	if err != nil {
		return nil, err
	}
	out.Title, out.Markdown = splitTitle(out.Markdown)
	if out.Title == "" {
		out.Title = truncateRunes(firstLine(in.Prompt), 80)
	}
	if out.Markdown == "" {
		return nil, fmt.Errorf("%w: the model wrote no text", ErrAssistantFailed)
	}
	s.log.Info("note written by the assistant", "user", acc.ID, "model", out.Model)
	return out, nil
}

func checkPrompt(p string) error {
	switch {
	case p == "":
		return invalid("say what the AI should do")
	case utf8.RuneCountInString(p) > maxAssistantPrompt:
		return invalid("the request is at most %d characters", maxAssistantPrompt)
	}
	return nil
}

// run sends a request to the account's model: the app's instructions, the task's, the
// user's own system prompt, and the user's message.
func (s *AssistantService) run(ctx context.Context, acc *Account, task, message string) (*AssistantAnswer, error) {
	u, err := s.user(ctx, acc)
	if err != nil {
		return nil, err
	}
	a := u.Assistant
	if !a.Configured() {
		return nil, errors.Join(ErrNotReady, errors.New("set up your AI model in the settings first"))
	}
	system := assistantBase + task + "\nToday is " + s.clock().In(u.Location()).Format("Monday, 2 January 2006") + "."
	if a.SystemPrompt != "" {
		system += "\n\nThe user's own instructions, to follow in every answer:\n" + a.SystemPrompt
	}
	answer, err := s.ai.Complete(ctx, a.APIKey, openrouter.Request{Model: a.Model, WebSearch: a.WebSearch, Messages: []openrouter.Message{
		{Role: "system", Content: system},
		{Role: "user", Content: message},
	}})
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: %v", ErrAssistantFailed, err)
	}
	answer = strings.TrimSpace(answer)
	if m := fencedAnswer.FindStringSubmatch(answer); m != nil {
		answer = strings.TrimSpace(m[1])
	}
	answer = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(answer, "<note>"), "</note>"))
	if answer == "" {
		return nil, fmt.Errorf("%w: the model gave no answer", ErrAssistantFailed)
	}
	return &AssistantAnswer{Markdown: answer, Model: a.Model}, nil
}

// splitTitle takes a leading level 1 heading off Markdown as the title.
func splitTitle(markdown string) (title, rest string) {
	first, rest, _ := strings.Cut(markdown, "\n")
	if t, ok := strings.CutPrefix(strings.TrimSpace(first), "# "); ok {
		return truncateRunes(strings.Trim(strings.TrimSpace(t), "*_"), 200), strings.TrimSpace(rest)
	}
	return "", markdown
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")
	return strings.TrimSpace(line)
}
