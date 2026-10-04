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
// group 2 the label. Models sometimes leave out the leading zero of the seconds ("[0:3]").
var speakerLine = regexp.MustCompile(`^(\s*(?:\[(?:\d{1,2}:)?\d{1,3}:\d{1,2}\]\s*)?)([^:\n\[\]]{1,40}):\s`)

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
	return s.NameSpeakers(ctx, acc, id, SpeakerNames{Names: []SpeakerRename{in}})
}

// SpeakerNames names several speakers of a note's transcript at once.
type SpeakerNames struct {
	Names []SpeakerRename `json:"names"`
	// Resummarize makes the summary again from the renamed transcript, so it is written with
	// the names; otherwise the labels are only replaced where the summary mentions them.
	Resummarize bool `json:"resummarize"`
}

// maxSpeakerRenames bounds the speakers named at once.
const maxSpeakerRenames = 50

// clean normalizes the names and checks that they can be applied one after the other.
func (in SpeakerNames) clean() ([]SpeakerRename, error) {
	if len(in.Names) == 0 || len(in.Names) > maxSpeakerRenames {
		return nil, invalid("name 1-%d speakers", maxSpeakerRenames)
	}
	names := make([]SpeakerRename, len(in.Names))
	renamed := map[string]bool{}
	for i, n := range in.Names {
		from, to := strings.TrimSpace(n.From), strings.Join(strings.Fields(n.To), " ")
		if err := validSpeakerName(to); err != nil {
			return nil, err
		}
		if renamed[from] {
			return nil, invalid("speaker %q is named twice", from)
		}
		names[i] = SpeakerRename{From: from, To: to}
		if from != to {
			renamed[from] = true
		}
	}
	for _, n := range names {
		if n.From != n.To && renamed[n.To] {
			return nil, invalid("%q is renamed too; name the speakers one at a time", n.To)
		}
	}
	return names, nil
}

// NameSpeakers gives speakers of the note's transcript names (see RenameSpeaker). With
// Resummarize the summary is made again from the renamed transcript; the one it replaces is
// kept as an earlier version.
func (s *RecordingService) NameSpeakers(ctx context.Context, acc *Account, id string, in SpeakerNames) (*recording.Recording, error) {
	names, err := in.clean()
	if err != nil {
		return nil, err
	}
	if in.Resummarize {
		return s.requeueAs(ctx, acc, id, recording.RoleEditor, func(rec *recording.Recording) (recording.Status, error) {
			if _, err := renameSpeakers(rec, names, false); err != nil {
				return "", err
			}
			rec.Summary = nil
			return recording.StatusTranscribed, nil
		})
	}
	return s.change(ctx, acc, id, recording.RoleEditor, func(rec *recording.Recording, _ recording.Role) error {
		changed, err := renameSpeakers(rec, names, true)
		if changed {
			// The text changed: edits made on the old one must not undo this.
			rec.Revision++
		}
		return err
	})
}

// renameSpeakers applies names to the transcript and, with inSummary, to the summary and its
// action items. It reports whether anything changed.
func renameSpeakers(rec *recording.Recording, names []SpeakerRename, inSummary bool) (bool, error) {
	if rec.Transcript == nil || rec.IsDocument() {
		return false, errors.Join(ErrNotReady, errors.New("the note has no transcript with speakers"))
	}
	switch rec.Status {
	case recording.StatusUploading, recording.StatusReceived, recording.StatusStored, recording.StatusTranscribed:
		// The pipeline would save its result over the names.
		return false, errors.Join(ErrNotReady, errors.New("the note is still being processed"))
	}
	labels := SpeakerLabels(rec.Transcript.Text)
	for _, n := range names {
		if !slices.Contains(labels, n.From) {
			return false, invalid("the transcript has no speaker %q", n.From)
		}
	}
	transcript := *rec.Transcript
	var summary *recording.Summary
	if inSummary && rec.Summary != nil {
		sum := *rec.Summary
		sum.ActionItems = slices.Clone(sum.ActionItems)
		sum.Speakers = slices.Clone(sum.Speakers)
		summary = &sum
	}
	changed := false
	for _, n := range names {
		if n.From == n.To {
			continue
		}
		changed = true
		transcript.Text = renameSpeakerLines(transcript.Text, n.From, n.To)
		if summary == nil {
			continue
		}
		summary.Markdown = replaceName(summary.Markdown, n.From, n.To)
		for i := range summary.ActionItems {
			it := &summary.ActionItems[i]
			it.Text, it.Owner = replaceName(it.Text, n.From, n.To), replaceName(it.Owner, n.From, n.To)
		}
		// The suggestion for this speaker is taken care of.
		summary.Speakers = slices.DeleteFunc(summary.Speakers, func(sn recording.SpeakerName) bool { return sn.Label == n.From })
	}
	if changed {
		rec.Transcript = &transcript
		if summary != nil {
			rec.Summary = summary
		}
	}
	return changed, nil
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
