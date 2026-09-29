package service

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/openrouter"
)

// Limits of a writing request, in characters.
const (
	maxWriteText        = 20_000
	maxWriteContext     = 8_000
	maxWriteInstruction = 1_000
)

// writeActions are the changes the AI makes to a passage of a note, by name: what the model
// is asked to do with the text.
var writeActions = map[string]string{
	"improve":      "Improve the writing of the text: clearer, more concise and better structured, keeping its meaning, facts and tone.",
	"fix":          "Correct the spelling, grammar and punctuation of the text. Change nothing else.",
	"shorter":      "Make the text shorter: about half as long, keeping the essential information.",
	"longer":       "Make the text longer: elaborate on its points with more detail and explanation, without inventing facts.",
	"simplify":     "Rewrite the text in simple, plain language that is easy to understand.",
	"professional": "Rewrite the text in a professional, businesslike tone.",
	"casual":       "Rewrite the text in a casual, friendly tone.",
	"summarize":    "Summarize the text in a few sentences or a short bullet list.",
	"tasks":        "Extract the action items and to-dos from the text as a Markdown checklist (\"- [ ] …\"), one item per task, each starting with a verb. Output only the checklist; if there are none, output an empty checklist item.",
	"table":        "Turn the information in the text into a Markdown table (GitHub style, with a header row). Output only the table.",
	"translate":    "Translate the text into %s.",
	"continue":     "Continue writing the text: write the next one to three paragraphs that follow on naturally from where it ends, in the same style. Output only the new text, not the text given.",
	"custom":       "%s",
	"write":        "Write new text for the note as asked: %s",
}

// WriteInput asks the AI to write or rewrite part of a note.
type WriteInput struct {
	// Action names what to do (see writeActions): e.g. "improve", "translate" or "custom".
	Action string `json:"action"`
	// Text is the passage to work on, as Markdown (the selection); for "continue", the text
	// before the cursor. Empty for "write".
	Text string `json:"text"`
	// Context is the note's text around the passage (optional), for the model to match.
	Context string `json:"context,omitempty"`
	// Instruction is what to do, for "custom" (with the text) and "write" (new text).
	Instruction string `json:"instruction,omitempty"`
	// Language is the language to translate into (a key of SummaryLanguages), for "translate".
	Language string `json:"language,omitempty"`
}

// WriteOutput is the text the AI wrote, as Markdown.
type WriteOutput struct {
	Markdown string `json:"markdown"`
	Model    string `json:"model"`
}

// fencedAnswer matches an answer the model wrapped in a code fence as a whole.
var fencedAnswer = regexp.MustCompile("(?s)^```(?:markdown|md)?[ \\t]*\\n(.*?)\\n?```$")

// Write has the summary model write or rewrite a passage of a note, for the editor. It
// changes nothing itself: the web app puts the answer into the text if the user takes it.
func (s *AIService) Write(ctx context.Context, acc *Account, in WriteInput) (*WriteOutput, error) {
	in.Action = strings.TrimSpace(in.Action)
	in.Text = strings.TrimSpace(strings.ReplaceAll(in.Text, "\r\n", "\n"))
	in.Instruction = strings.TrimSpace(in.Instruction)
	task, ok := writeActions[in.Action]
	switch {
	case !ok:
		return nil, invalid("unknown action %q", in.Action)
	case in.Text == "" && in.Action != "write":
		return nil, invalid("there is no text to work on")
	case utf8.RuneCountInString(in.Text) > maxWriteText:
		return nil, invalid("the text is at most %d characters", maxWriteText)
	case utf8.RuneCountInString(in.Instruction) > maxWriteInstruction:
		return nil, invalid("the instruction is at most %d characters", maxWriteInstruction)
	}
	switch in.Action {
	case "translate":
		name, ok := SummaryLanguages[in.Language]
		if !ok {
			return nil, invalid("unsupported language %q", in.Language)
		}
		task = fmt.Sprintf(task, name)
	case "custom", "write":
		if in.Instruction == "" {
			return nil, invalid("say what to write")
		}
		task = fmt.Sprintf(task, in.Instruction)
	}

	st, err := s.settings.OpenRouter(ctx)
	if err != nil {
		return nil, err
	}
	if !st.CanSummarize() {
		return nil, errors.Join(ErrNotReady, errors.New("the AI models are not set up; an administrator sets them up under Admin → General"))
	}

	system := `You are a writing assistant inside a note-taking app. You work on a passage of the user's note, written in Markdown.
Answer with the resulting Markdown only: no introduction, no explanation, no quotes around it, no code fence around it. Keep Markdown formatting (headings, lists, checklists, tables, links, "#12" references to other notes, images) where it still fits.
Unless asked to translate, write in the language of the text (for new text: the language of the note, else of the instruction).`
	var user strings.Builder
	user.WriteString("Task: " + task + "\n")
	if c := truncateRunes(in.Context, maxWriteContext); c != "" {
		user.WriteString("\nThe note around the passage, for context (don't repeat it):\n<note>\n" + c + "\n</note>\n")
	}
	if in.Text != "" {
		user.WriteString("\nThe text:\n<text>\n" + in.Text + "\n</text>\n")
	}
	answer, err := s.ai.Complete(ctx, st.APIKey, openrouter.Request{Model: st.SummaryModel, Messages: []openrouter.Message{
		{Role: "system", Content: system},
		{Role: "user", Content: user.String()},
	}})
	if err != nil {
		return nil, fmt.Errorf("write: %w", err)
	}
	answer = strings.TrimSpace(answer)
	if m := fencedAnswer.FindStringSubmatch(answer); m != nil {
		answer = strings.TrimSpace(m[1])
	}
	answer = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(answer, "<text>"), "</text>"))
	if answer == "" {
		return nil, errors.New("the model gave no text")
	}
	s.log.Info("text written", "user", acc.ID, "action", in.Action, "model", st.SummaryModel)
	return &WriteOutput{Markdown: answer, Model: st.SummaryModel}, nil
}
