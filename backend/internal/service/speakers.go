package service

import (
	"context"
	"errors"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
)

// speakerLine matches a transcript line that starts with a speaker label, after an optional
// time stamp: "[1:05] Speaker 2: …" or "Anna: …". Group 1 is everything before the label,
// group 2 the label.
var speakerLine = regexp.MustCompile(`^(\s*(?:\[(?:\d{1,2}:)?\d{1,3}:\d{2}\]\s*)?)([^:\n\[\]]{1,40}):\s`)

// maxSpeakerName bounds a speaker's name.
const maxSpeakerName = 40

// SpeakerLabels returns the speaker labels of a transcript ("Speaker 1", "Anna", …) in the
// order they first speak.
func SpeakerLabels(transcript string) []string {
	var out []string
	for _, line := range strings.Split(transcript, "\n") {
		if m := speakerLine.FindStringSubmatch(line); m != nil {
			if label := strings.TrimSpace(m[2]); label != "" && !slices.Contains(out, label) {
				out = append(out, label)
			}
		}
	}
	return out
}

// validSpeakerName checks a name a speaker is given.
func validSpeakerName(name string) error {
	switch {
	case name == "" || utf8.RuneCountInString(name) > maxSpeakerName:
		return invalid("a speaker's name is 1-%d characters", maxSpeakerName)
	case strings.ContainsAny(name, ":[]\n\r\t"):
		return invalid("a speaker's name can't contain colons or brackets")
	}
	return nil
}

// SpeakerRename renames a speaker of a recording's transcript.
type SpeakerRename struct {
	// From is the speaker's label in the transcript, e.g. "Speaker 1".
	From string `json:"from"`
	// To is the name the speaker gets, e.g. "Anna".
	To string `json:"to"`
}

// RenameSpeaker gives a speaker of the note's transcript a name: the label starting the
// speaker's lines in the transcript is replaced, and so is the label where the summary and
// its action items mention it. Giving a speaker the name of another one merges the two.
func (s *RecordingService) RenameSpeaker(ctx context.Context, acc *Account, id string, in SpeakerRename) (*recording.Recording, error) {
	from, to := strings.TrimSpace(in.From), strings.Join(strings.Fields(in.To), " ")
	if err := validSpeakerName(to); err != nil {
		return nil, err
	}
	return s.change(ctx, acc, id, recording.RoleEditor, func(rec *recording.Recording, _ recording.Role) error {
		if rec.Transcript == nil || rec.IsDocument() {
			return errors.Join(ErrNotReady, errors.New("the note has no transcript with speakers"))
		}
		if !slices.Contains(SpeakerLabels(rec.Transcript.Text), from) {
			return invalid("the transcript has no speaker %q", from)
		}
		if from == to {
			return nil
		}
		transcript := *rec.Transcript
		transcript.Text = renameSpeakerLines(transcript.Text, from, to)
		rec.Transcript = &transcript
		if rec.Summary != nil {
			summary := *rec.Summary
			summary.Markdown = replaceName(summary.Markdown, from, to)
			summary.ActionItems = slices.Clone(summary.ActionItems)
			for i := range summary.ActionItems {
				it := &summary.ActionItems[i]
				it.Text, it.Owner = replaceName(it.Text, from, to), replaceName(it.Owner, from, to)
			}
			// The suggestion for this speaker is taken care of.
			summary.Speakers = slices.DeleteFunc(slices.Clone(summary.Speakers), func(n recording.SpeakerName) bool { return n.Label == from })
			rec.Summary = &summary
		}
		// The text changed: edits made on the old one must not undo this.
		rec.Revision++
		return nil
	})
}

// renameSpeakerLines replaces the label from at the start of the transcript's lines.
func renameSpeakerLines(text, from, to string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		m := speakerLine.FindStringSubmatchIndex(line)
		if m == nil || strings.TrimSpace(line[m[4]:m[5]]) != from {
			continue
		}
		lines[i] = line[:m[3]] + to + line[m[5]:]
	}
	return strings.Join(lines, "\n")
}

// replaceName replaces the whole-word mentions of from in text by to. A mention that is
// already followed by the rest of to (renaming "Anna" to "Anna Weber" where the text says
// "Anna Weber") stays as it is.
func replaceName(text, from, to string) string {
	if from == "" || !strings.Contains(text, from) {
		return text
	}
	var b strings.Builder
	rest := text
	for {
		i := strings.Index(rest, from)
		if i < 0 {
			b.WriteString(rest)
			return b.String()
		}
		before, after := rest[:i], rest[i+len(from):]
		prev, _ := utf8.DecodeLastRuneInString(before)
		next, _ := utf8.DecodeRuneInString(after)
		whole := (before == "" || !wordRune(prev)) && (after == "" || !wordRune(next))
		b.WriteString(before)
		if whole && !strings.HasPrefix(rest[i:], to) {
			b.WriteString(to)
		} else {
			b.WriteString(from)
		}
		rest = after
	}
}

func wordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' }
