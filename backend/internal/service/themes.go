package service

import (
	"context"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/theme"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/ports"
)

// AutoTheme is the default theme: a structure that adapts to the content.
const AutoTheme = "auto"

// ThemeView is a theme as listed in the UI. Built-in themes have fixed IDs; their names and
// descriptions are translated by the UI (by ID), the English text here is the fallback.
type ThemeView struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Description  string `json:"description"`
	Instructions string `json:"instructions"`
	BuiltIn      bool   `json:"builtIn"`
}

// builtInThemes are available to every user and can't be changed.
var builtInThemes = []ThemeView{
	{ID: AutoTheme, Name: "Auto", Description: "Adaptive structure", Instructions: `Choose the structure that fits the content best. Start with a 2-3 sentence overview, then "## Key points" as a bullet list; add "## Action items" and "## Decisions" as bullet lists only if there are any.`},
	{ID: "meeting", Name: "Meeting Notes", Description: "Topic · Agreement · Conclusion", Instructions: `Write meeting notes: a one-sentence purpose of the meeting, then "## Topics" with a "###" heading per topic discussed and bullets with the key points, "## Agreements" listing what was decided, "## Action items" with the owner in bold where mentioned, and "## Conclusion" in 1-2 sentences.`},
	{ID: "call", Name: "Call Notes", Description: "Background · Action items", Instructions: `Write call notes: "## Background" (who talked, why, context in 2-3 sentences), "## Discussion" as bullets, "## Action items" with owners and dates where mentioned, "## Follow-up" if any.`},
	{ID: "interview", Name: "Interview Notes", Description: "Action items · Conclusion", Instructions: `Write interview notes: "## Participants and purpose", "## Questions and answers" as a bullet per question with the essence of the answer, "## Observations" (strengths, concerns), "## Action items", and "## Conclusion".`},
	{ID: "dictation", Name: "Dictation Notes", Description: "Action items · Summary", Instructions: `This is a dictated note. Write "## Note" with the dictated content cleaned up into well-formed prose (remove filler words and false starts, keep the meaning and all details), then "## Action items" if there are any, and "## Summary" in one or two sentences.`},
	{ID: "keypoints", Name: "Key Points", Description: "Action items · Mind map", Instructions: `Write "## Key points" as a concise bullet list of the most important statements (at most 10), "## Action items" if any, and "## Mind map" as a nested bullet list organizing the main topics and their subtopics.`},
	{ID: "lecture", Name: "Lecture Notes", Description: "Concepts · Examples · Questions", Instructions: `Write study notes: "## Overview", "## Key concepts" with a "###" heading per concept and a short explanation, "## Examples" mentioned in the recording, and "## Open questions" worth reviewing.`},
}

func init() {
	for i := range builtInThemes {
		builtInThemes[i].BuiltIn = true
	}
}

// ThemeInput creates or changes a user's theme.
type ThemeInput struct {
	Name         string `json:"name"`
	Description  string `json:"description"`
	Instructions string `json:"instructions"`
}

// ThemeService manages users' summary themes.
type ThemeService struct {
	repo  ports.ThemeRepository
	clock func() time.Time
}

// NewThemeService builds the service.
func NewThemeService(repo ports.ThemeRepository) *ThemeService {
	return &ThemeService{repo: repo, clock: time.Now}
}

// List returns the built-in themes followed by the account's own.
func (s *ThemeService) List(ctx context.Context, acc *Account) ([]ThemeView, error) {
	out := append([]ThemeView{}, builtInThemes...)
	if acc.ID == "" {
		return out, nil
	}
	own, err := s.repo.List(ctx, acc.ID)
	if err != nil {
		return nil, err
	}
	for _, t := range own {
		out = append(out, viewOf(t))
	}
	return out, nil
}

// Create adds a theme for the account's user.
func (s *ThemeService) Create(ctx context.Context, acc *Account, in ThemeInput) (*ThemeView, error) {
	if acc.ID == "" {
		return nil, errors.Join(ErrForbidden, errors.New("themes belong to a user; sign in"))
	}
	if err := validateTheme(&in); err != nil {
		return nil, err
	}
	now := s.clock().UTC()
	t := &theme.Theme{ID: newID(), OwnerID: acc.ID, Name: in.Name, Description: in.Description,
		Instructions: in.Instructions, CreatedAt: now, UpdatedAt: now}
	if err := s.repo.Create(ctx, t); err != nil {
		return nil, err
	}
	v := viewOf(t)
	return &v, nil
}

// Update changes one of the account's themes.
func (s *ThemeService) Update(ctx context.Context, acc *Account, id string, in ThemeInput) (*ThemeView, error) {
	t, err := s.own(ctx, acc, id)
	if err != nil {
		return nil, err
	}
	if err := validateTheme(&in); err != nil {
		return nil, err
	}
	t.Name, t.Description, t.Instructions, t.UpdatedAt = in.Name, in.Description, in.Instructions, s.clock().UTC()
	if err := s.repo.Update(ctx, t); err != nil {
		return nil, err
	}
	v := viewOf(t)
	return &v, nil
}

// Delete removes one of the account's themes. Summaries made with it keep their text;
// regenerating them falls back to the auto theme.
func (s *ThemeService) Delete(ctx context.Context, acc *Account, id string) error {
	if _, err := s.own(ctx, acc, id); err != nil {
		return err
	}
	return s.repo.Delete(ctx, id)
}

// Resolve returns the theme to summarize a recording of ownerID with. Unknown themes (e.g.
// deleted ones, or another user's) resolve to the auto theme.
func (s *ThemeService) Resolve(ctx context.Context, ownerID, id string) ThemeView {
	for _, b := range builtInThemes {
		if b.ID == id {
			return b
		}
	}
	if id != "" {
		if t, err := s.repo.Get(ctx, id); err == nil && t.OwnerID == ownerID {
			return viewOf(t)
		}
	}
	return builtInThemes[0]
}

// Accessible reports whether the account may use the theme.
func (s *ThemeService) Accessible(ctx context.Context, acc *Account, id string) bool {
	for _, b := range builtInThemes {
		if b.ID == id {
			return true
		}
	}
	t, err := s.repo.Get(ctx, id)
	return err == nil && acc.Owns(t.OwnerID)
}

func (s *ThemeService) own(ctx context.Context, acc *Account, id string) (*theme.Theme, error) {
	t, err := s.repo.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if !acc.Owns(t.OwnerID) {
		return nil, ErrNotFound
	}
	return t, nil
}

func viewOf(t *theme.Theme) ThemeView {
	return ThemeView{ID: t.ID, Name: t.Name, Description: t.Description, Instructions: t.Instructions}
}

func validateTheme(in *ThemeInput) error {
	in.Name = strings.TrimSpace(in.Name)
	in.Description = strings.TrimSpace(in.Description)
	in.Instructions = strings.TrimSpace(in.Instructions)
	switch {
	case in.Name == "" || utf8.RuneCountInString(in.Name) > 60:
		return invalid("name must be 1-60 characters")
	case utf8.RuneCountInString(in.Description) > 120:
		return invalid("description must be at most 120 characters")
	case in.Instructions == "" || utf8.RuneCountInString(in.Instructions) > 4000:
		return invalid("instructions must be 1-4000 characters")
	}
	return nil
}

// SummaryLanguages are the languages a summary can be written in, besides "auto" (the
// language of the transcript). The value is the language named in the prompt.
var SummaryLanguages = map[string]string{
	"en-US": "English (US)", "en-GB": "English (UK)", "de-DE": "German", "de-AT": "German (Austria)",
	"de-CH": "Swiss German (written as standard German)", "fr-FR": "French", "es-ES": "Spanish",
	"it-IT": "Italian", "nl-NL": "Dutch", "pt-PT": "Portuguese", "pt-BR": "Portuguese (Brazil)",
	"pl-PL": "Polish", "sv-SE": "Swedish", "da-DK": "Danish", "nb-NO": "Norwegian", "fi-FI": "Finnish",
	"cs-CZ": "Czech", "tr-TR": "Turkish", "ru-RU": "Russian", "uk-UA": "Ukrainian", "ja-JP": "Japanese",
	"zh-CN": "Chinese (Simplified)", "ko-KR": "Korean",
}
