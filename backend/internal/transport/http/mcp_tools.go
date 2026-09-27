package http

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/folder"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/label"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/user"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/service"
)

// mcpTool is a tool of the MCP server. Its input schema is a JSON Schema object; run gets
// the call's arguments and returns a JSON object for the assistant.
type mcpTool struct {
	Name        string         `json:"name"`
	Title       string         `json:"title"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
	Annotations map[string]any `json:"annotations"`
	run         func(s *Server, ctx context.Context, acc *service.Account, args json.RawMessage) (any, error)
}

// Schema helpers.
func schemaObject(required []string, props map[string]any) map[string]any {
	o := map[string]any{"type": "object", "properties": props, "additionalProperties": false}
	if len(required) > 0 {
		o["required"] = required
	}
	return o
}

func schemaOf(typ, desc string) map[string]any {
	return map[string]any{"type": typ, "description": desc}
}

const noteArgDesc = `The note's ID, or its number (e.g. "12" or "#12").`

var mcpReadOnly = map[string]any{"readOnlyHint": true, "openWorldHint": false}

func mcpWrites(idempotent bool) map[string]any {
	return map[string]any{"readOnlyHint": false, "destructiveHint": false, "idempotentHint": idempotent, "openWorldHint": false}
}

var mcpTools = []*mcpTool{
	{
		Name:  "search_notes",
		Title: "Search notes",
		Description: "Finds the user's notes, newest first. Without a query it lists the most recent notes. " +
			"The query matches words in titles, texts and transcripts (all words must appear), or a note number such as #12.",
		InputSchema: schemaObject(nil, map[string]any{
			"query":  schemaOf("string", "Words to look for, or a note number such as #12."),
			"folder": schemaOf("string", "Only notes in this folder (its ID or name) and its sub-folders."),
			"label":  schemaOf("string", "Only notes with this label (its ID or name)."),
			"type":   map[string]any{"type": "string", "enum": []string{"text", "audio", "document", "board"}, "description": "Only notes of this type."},
			"limit":  map[string]any{"type": "integer", "minimum": 1, "maximum": 100, "description": "At most this many notes (default 20)."},
		}),
		Annotations: mcpReadOnly,
		run:         (*Server).mcpSearchNotes,
	},
	{
		Name:  "get_note",
		Title: "Read a note",
		Description: "Reads one note in full: its text (Markdown; for recordings the summary), the transcript of a " +
			"recording or the text read from a document, action items, labels, folder and task fields.",
		InputSchema: schemaObject([]string{"note"}, map[string]any{"note": schemaOf("string", noteArgDesc)}),
		Annotations: mcpReadOnly,
		run:         (*Server).mcpGetNote,
	},
	{
		Name:  "list_tasks",
		Title: "List tasks",
		Description: "Lists the user's tasks (notes labeled as a task), open ones first, by due date and priority. " +
			"Dates are in the user's time zone.",
		InputSchema: schemaObject(nil, map[string]any{
			"includeDone": schemaOf("boolean", "Also list checked-off tasks."),
			"dueBefore":   schemaOf("string", "Only tasks due on or before this date (YYYY-MM-DD)."),
			"limit":       map[string]any{"type": "integer", "minimum": 1, "maximum": 200, "description": "At most this many tasks (default 50)."},
		}),
		Annotations: mcpReadOnly,
		run:         (*Server).mcpListTasks,
	},
	{
		Name:        "list_folders",
		Title:       "List folders",
		Description: "Lists the user's folders with their full paths.",
		InputSchema: schemaObject(nil, map[string]any{}),
		Annotations: mcpReadOnly,
		run:         (*Server).mcpListFolders,
	},
	{
		Name:        "list_labels",
		Title:       "List labels",
		Description: "Lists the labels the user can put on notes. The built-in label \"task\" makes a note a task.",
		InputSchema: schemaObject(nil, map[string]any{}),
		Annotations: mcpReadOnly,
		run:         (*Server).mcpListLabels,
	},
	{
		Name:  "create_note",
		Title: "Create a note",
		Description: "Creates a text note with a title and Markdown text, optionally in a folder or under another note, " +
			"and optionally as a task with a due date and priority.",
		InputSchema: schemaObject([]string{"title"}, map[string]any{
			"title":    schemaOf("string", "The note's title (1-200 characters)."),
			"markdown": schemaOf("string", "The note's text, in Markdown."),
			"folder":   schemaOf("string", "The folder to put it in (its ID or name); the top level when left out."),
			"parent":   schemaOf("string", "Make it a sub-note of this note (its ID or number)."),
			"task":     schemaOf("boolean", "Make the note a task (implied by dueDate and priority)."),
			"dueDate":  schemaOf("string", "The task's due date, YYYY-MM-DD."),
			"dueTime":  schemaOf("string", "The task's due time, HH:MM (24 hours); needs dueDate."),
			"priority": map[string]any{"type": "integer", "minimum": 0, "maximum": 3, "description": "1 (most urgent) to 3; 0 for none."},
		}),
		Annotations: mcpWrites(false),
		run:         (*Server).mcpCreateNote,
	},
	{
		Name:  "update_note",
		Title: "Change a note",
		Description: "Changes a note's title or text (replacing it), moves it into a folder, or replaces its labels. " +
			"Only the fields given are changed.",
		InputSchema: schemaObject([]string{"note"}, map[string]any{
			"note":     schemaOf("string", noteArgDesc),
			"title":    schemaOf("string", "The new title."),
			"markdown": schemaOf("string", "The new text, in Markdown; replaces the whole text."),
			"folder":   schemaOf("string", "Move the note into this folder (its ID or name); an empty string moves it to the top level."),
			"labels":   map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": "The note's labels (IDs or names), replacing the ones it has."},
		}),
		Annotations: mcpWrites(true),
		run:         (*Server).mcpUpdateNote,
	},
	{
		Name:  "update_task",
		Title: "Change a task",
		Description: "Checks a task off (or on again), or sets its due date and priority. A note becomes a task when a " +
			"date or priority is set. Checking off a repeating task moves it to its next date.",
		InputSchema: schemaObject([]string{"note"}, map[string]any{
			"note":     schemaOf("string", noteArgDesc),
			"done":     schemaOf("boolean", "Check the task off (true) or open it again (false)."),
			"dueDate":  schemaOf("string", "The new due date, YYYY-MM-DD; an empty string removes the date."),
			"dueTime":  schemaOf("string", "The new due time, HH:MM (24 hours), with dueDate; left out for the whole day."),
			"priority": map[string]any{"type": "integer", "minimum": 0, "maximum": 3, "description": "1 (most urgent) to 3; 0 for none."},
		}),
		Annotations: mcpWrites(true),
		run:         (*Server).mcpUpdateTask,
	},
}

func mcpToolList() []*mcpTool { return mcpTools }

func findMCPTool(name string) *mcpTool {
	for _, t := range mcpTools {
		if t.Name == name {
			return t
		}
	}
	return nil
}

// mcpArgs decodes a tool's arguments strictly, so a misspelled field is reported.
func mcpArgs(raw json.RawMessage, dst any) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return &mcpArgError{"invalid arguments: " + err.Error()}
	}
	return nil
}

// mcpNote is a note as the assistant sees it.
type mcpNote struct {
	ID          string                 `json:"id"`
	Number      int64                  `json:"number,omitempty"`
	Title       string                 `json:"title"`
	Type        string                 `json:"type"`
	Status      string                 `json:"status,omitempty"`
	Folder      string                 `json:"folder,omitempty"`
	FolderID    string                 `json:"folderId,omitempty"`
	ParentID    string                 `json:"parentId,omitempty"`
	Labels      []string               `json:"labels,omitempty"`
	Task        *mcpTask               `json:"task,omitempty"`
	RecordedAt  *time.Time             `json:"recordedAt,omitempty"`
	CreatedAt   time.Time              `json:"createdAt"`
	UpdatedAt   time.Time              `json:"updatedAt"`
	Markdown    string                 `json:"markdown,omitempty"`
	Transcript  string                 `json:"transcript,omitempty"`
	ActionItems []recording.ActionItem `json:"actionItems,omitempty"`
}

type mcpTask struct {
	Done     bool              `json:"done"`
	DueDate  string            `json:"dueDate,omitempty"`
	DueTime  string            `json:"dueTime,omitempty"`
	Repeat   *recording.Repeat `json:"repeat,omitempty"`
	Priority int               `json:"priority,omitempty"`
}

// mcpContext holds the names of the account's folders and labels, to show and resolve them.
type mcpContext struct {
	folders []*folder.Folder
	labels  []service.LabelView
}

func (s *Server) mcpLoad(ctx context.Context, acc *service.Account) (*mcpContext, error) {
	folders, err := s.folders.List(ctx, acc)
	if err != nil {
		return nil, err
	}
	labels, err := s.labels.List(ctx, acc)
	if err != nil {
		return nil, err
	}
	return &mcpContext{folders: folders, labels: labels}, nil
}

// folderPath is the folder's name with those of the folders it is in, e.g. "Work/Clients".
func (c *mcpContext) folderPath(id string) string {
	var parts []string
	for depth := 0; id != "" && depth < 32; depth++ {
		i := slices.IndexFunc(c.folders, func(f *folder.Folder) bool { return f.ID == id })
		if i < 0 {
			break
		}
		parts = append([]string{c.folders[i].Name}, parts...)
		id = c.folders[i].ParentID
	}
	return strings.Join(parts, "/")
}

// folderID resolves a folder given by ID, name or path ("" is the top level).
func (c *mcpContext) folderID(ref string) (string, error) {
	ref = strings.Trim(strings.TrimSpace(ref), "/")
	if ref == "" {
		return "", nil
	}
	for _, f := range c.folders {
		if f.ID == ref {
			return f.ID, nil
		}
	}
	var found []string
	for _, f := range c.folders {
		if strings.EqualFold(f.Name, ref) || strings.EqualFold(c.folderPath(f.ID), ref) {
			found = append(found, f.ID)
		}
	}
	switch len(found) {
	case 0:
		return "", &mcpArgError{fmt.Sprintf("no folder %q; list_folders shows the folders", ref)}
	case 1:
		return found[0], nil
	default:
		return "", &mcpArgError{fmt.Sprintf("more than one folder is named %q; give its path or ID", ref)}
	}
}

// inFolder reports whether folder id is root or one of its sub-folders.
func (c *mcpContext) inFolder(id, root string) bool {
	for depth := 0; id != "" && depth < 32; depth++ {
		if id == root {
			return true
		}
		i := slices.IndexFunc(c.folders, func(f *folder.Folder) bool { return f.ID == id })
		if i < 0 {
			return false
		}
		id = c.folders[i].ParentID
	}
	return false
}

func (c *mcpContext) labelName(id string) string {
	for _, l := range c.labels {
		if l.ID == id {
			return l.Name
		}
	}
	return id
}

// labelID resolves a label given by ID or name.
func (c *mcpContext) labelID(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	for _, l := range c.labels {
		if l.ID == ref {
			return l.ID, nil
		}
	}
	for _, l := range c.labels {
		if strings.EqualFold(l.Name, ref) {
			return l.ID, nil
		}
	}
	return "", &mcpArgError{fmt.Sprintf("no label %q; list_labels shows the labels", ref)}
}

func mcpNoteType(r *recording.Recording) string {
	if r.Type == recording.TypeAudio {
		return "audio"
	}
	return string(r.Type)
}

func mcpNoteTitle(r *recording.Recording) string {
	switch {
	case r.Summary != nil && r.Summary.Title != "":
		return r.Summary.Title
	case r.Title != "":
		return r.Title
	default:
		return "Untitled"
	}
}

// note converts a note for the assistant; full includes its texts.
func (c *mcpContext) note(r *recording.Recording, full bool) *mcpNote {
	n := &mcpNote{
		ID: r.ID, Number: r.Number, Title: mcpNoteTitle(r), Type: mcpNoteType(r), FolderID: r.FolderID, ParentID: r.ParentID,
		Folder: c.folderPath(r.FolderID), RecordedAt: r.RecordedAt, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
	if r.Status != recording.StatusSummarized {
		n.Status = string(r.Status)
	}
	for _, l := range r.Labels {
		n.Labels = append(n.Labels, c.labelName(l))
	}
	if slices.Contains(r.Labels, label.Task) {
		t := &mcpTask{Done: r.Done, Priority: int(r.Priority)}
		if r.Due != nil {
			t.DueDate, t.DueTime, t.Repeat = r.Due.Date, r.Due.Time, r.Due.Repeat
		}
		n.Task = t
	}
	if full {
		if r.Summary != nil {
			n.Markdown = r.Summary.Markdown
			n.ActionItems = r.Summary.ActionItems
		}
		if r.Transcript != nil {
			n.Transcript = r.Transcript.Text
		}
	}
	return n
}

// mcpResolveNote loads a note given by ID or number.
func (s *Server) mcpResolveNote(ctx context.Context, acc *service.Account, ref string) (*recording.Recording, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, &mcpArgError{"note is required"}
	}
	n, err := strconv.ParseInt(strings.TrimPrefix(ref, "#"), 10, 64)
	if err != nil {
		return s.actions.Get(ctx, acc, ref)
	}
	if n > 0 {
		list, err := s.actions.List(ctx, acc, recording.ListFilter{Number: n, Limit: 1, Trash: recording.TrashAny})
		if err != nil {
			return nil, err
		}
		if len(list) > 0 {
			return list[0], nil
		}
	}
	if !strings.HasPrefix(ref, "#") {
		// An ID that happens to be all digits.
		if r, err := s.actions.Get(ctx, acc, ref); err == nil {
			return r, nil
		}
	}
	return nil, &mcpArgError{fmt.Sprintf("no note #%d", n)}
}

// matches reports whether every word of the query appears in the note's title or texts.
func mcpMatches(r *recording.Recording, words []string) bool {
	text := strings.ToLower(mcpNoteTitle(r))
	if r.Summary != nil {
		text += "\n" + strings.ToLower(r.Summary.Markdown)
	}
	if r.Transcript != nil {
		text += "\n" + strings.ToLower(r.Transcript.Text)
	}
	for _, w := range words {
		if !strings.Contains(text, w) {
			return false
		}
	}
	return true
}

func (s *Server) mcpSearchNotes(ctx context.Context, acc *service.Account, raw json.RawMessage) (any, error) {
	var in struct {
		Query  string `json:"query"`
		Folder string `json:"folder"`
		Label  string `json:"label"`
		Type   string `json:"type"`
		Limit  int    `json:"limit"`
	}
	if err := mcpArgs(raw, &in); err != nil {
		return nil, err
	}
	limit := in.Limit
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	c, err := s.mcpLoad(ctx, acc)
	if err != nil {
		return nil, err
	}
	query := strings.TrimSpace(in.Query)
	if n, err := strconv.ParseInt(strings.TrimPrefix(query, "#"), 10, 64); err == nil && n > 0 && strings.HasPrefix(query, "#") {
		list, err := s.actions.List(ctx, acc, recording.ListFilter{Number: n, Limit: 1, Brief: true})
		if err != nil {
			return nil, err
		}
		out := []*mcpNote{}
		for _, r := range list {
			out = append(out, c.note(r, false))
		}
		return map[string]any{"notes": out}, nil
	}
	folderID, labelID := "", ""
	if in.Folder != "" {
		if folderID, err = c.folderID(in.Folder); err != nil {
			return nil, err
		}
	}
	if in.Label != "" {
		if labelID, err = c.labelID(in.Label); err != nil {
			return nil, err
		}
	}
	words := strings.Fields(strings.ToLower(query))
	list, err := s.actions.List(ctx, acc, recording.ListFilter{Brief: len(words) == 0})
	if err != nil {
		return nil, err
	}
	slices.SortFunc(list, func(a, b *recording.Recording) int { return b.CreatedAt.Compare(a.CreatedAt) })
	out := []*mcpNote{}
	for _, r := range list {
		switch {
		case in.Type != "" && mcpNoteType(r) != in.Type,
			in.Folder != "" && !c.inFolder(r.FolderID, folderID),
			labelID != "" && !slices.Contains(r.Labels, labelID),
			len(words) > 0 && !mcpMatches(r, words):
			continue
		}
		out = append(out, c.note(r, false))
		if len(out) == limit {
			break
		}
	}
	return map[string]any{"notes": out}, nil
}

func (s *Server) mcpGetNote(ctx context.Context, acc *service.Account, raw json.RawMessage) (any, error) {
	var in struct {
		Note string `json:"note"`
	}
	if err := mcpArgs(raw, &in); err != nil {
		return nil, err
	}
	r, err := s.mcpResolveNote(ctx, acc, in.Note)
	if err != nil {
		return nil, err
	}
	c, err := s.mcpLoad(ctx, acc)
	if err != nil {
		return nil, err
	}
	n := c.note(r, true)
	out := map[string]any{"note": n}
	if r.DeletedAt != nil {
		out["inTrash"] = true
	}
	return out, nil
}

func (s *Server) mcpListTasks(ctx context.Context, acc *service.Account, raw json.RawMessage) (any, error) {
	var in struct {
		IncludeDone bool   `json:"includeDone"`
		DueBefore   string `json:"dueBefore"`
		Limit       int    `json:"limit"`
	}
	if err := mcpArgs(raw, &in); err != nil {
		return nil, err
	}
	if in.DueBefore != "" {
		if _, err := recording.ParseDate(in.DueBefore); err != nil {
			return nil, &mcpArgError{"dueBefore must look like 2026-09-28"}
		}
	}
	limit := in.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	c, err := s.mcpLoad(ctx, acc)
	if err != nil {
		return nil, err
	}
	list, err := s.actions.List(ctx, acc, recording.ListFilter{Brief: true})
	if err != nil {
		return nil, err
	}
	tasks := []*recording.Recording{}
	for _, r := range list {
		switch {
		case !slices.Contains(r.Labels, label.Task),
			r.Done && !in.IncludeDone,
			in.DueBefore != "" && (r.Due == nil || r.Due.Date > in.DueBefore):
			continue
		}
		tasks = append(tasks, r)
	}
	// Open before done; dated by date and time, before undated; then by priority (none last).
	slices.SortFunc(tasks, func(a, b *recording.Recording) int {
		if a.Done != b.Done {
			if a.Done {
				return 1
			}
			return -1
		}
		if d := cmp.Compare(mcpDueKey(a), mcpDueKey(b)); d != 0 {
			return d
		}
		if d := cmp.Compare(mcpPriorityKey(a), mcpPriorityKey(b)); d != 0 {
			return d
		}
		return b.CreatedAt.Compare(a.CreatedAt)
	})
	out := []*mcpNote{}
	for _, r := range tasks[:min(limit, len(tasks))] {
		out = append(out, c.note(r, false))
	}
	return map[string]any{"tasks": out, "today": s.now().In(user.LoadLocation(acc.TimeZone)).Format(recording.DateLayout)}, nil
}

func mcpDueKey(r *recording.Recording) string {
	if r.Due == nil {
		return "~" // after every date
	}
	return r.Due.Date + " " + cmp.Or(r.Due.Time, "~")
}

func mcpPriorityKey(r *recording.Recording) int {
	if r.Priority == 0 {
		return int(recording.MaxPriority) + 1
	}
	return int(r.Priority)
}

func (s *Server) mcpListFolders(ctx context.Context, acc *service.Account, raw json.RawMessage) (any, error) {
	if err := mcpArgs(raw, &struct{}{}); err != nil {
		return nil, err
	}
	c, err := s.mcpLoad(ctx, acc)
	if err != nil {
		return nil, err
	}
	type view struct {
		ID       string `json:"id"`
		Name     string `json:"name"`
		Path     string `json:"path"`
		ParentID string `json:"parentId,omitempty"`
	}
	out := []view{}
	for _, f := range c.folders {
		out = append(out, view{ID: f.ID, Name: f.Name, Path: c.folderPath(f.ID), ParentID: f.ParentID})
	}
	slices.SortFunc(out, func(a, b view) int { return strings.Compare(strings.ToLower(a.Path), strings.ToLower(b.Path)) })
	return map[string]any{"folders": out}, nil
}

func (s *Server) mcpListLabels(ctx context.Context, acc *service.Account, raw json.RawMessage) (any, error) {
	if err := mcpArgs(raw, &struct{}{}); err != nil {
		return nil, err
	}
	labels, err := s.labels.List(ctx, acc)
	if err != nil {
		return nil, err
	}
	return map[string]any{"labels": labels}, nil
}

// mcpDue makes the due date of a tool's arguments; nil for none.
func mcpDue(date, clock string) (*recording.Due, error) {
	date, clock = strings.TrimSpace(date), strings.TrimSpace(clock)
	if date == "" {
		if clock != "" {
			return nil, &mcpArgError{"dueTime needs a dueDate"}
		}
		return nil, nil
	}
	return &recording.Due{Date: date, Time: clock}, nil
}

func (s *Server) mcpCreateNote(ctx context.Context, acc *service.Account, raw json.RawMessage) (any, error) {
	var in struct {
		Title    string `json:"title"`
		Markdown string `json:"markdown"`
		Folder   string `json:"folder"`
		Parent   string `json:"parent"`
		Task     bool   `json:"task"`
		DueDate  string `json:"dueDate"`
		DueTime  string `json:"dueTime"`
		Priority int    `json:"priority"`
	}
	if err := mcpArgs(raw, &in); err != nil {
		return nil, err
	}
	c, err := s.mcpLoad(ctx, acc)
	if err != nil {
		return nil, err
	}
	note := service.TextNoteInput{SummaryEdit: service.SummaryEdit{Title: in.Title, Markdown: in.Markdown}}
	if in.Parent != "" {
		parent, err := s.mcpResolveNote(ctx, acc, in.Parent)
		if err != nil {
			return nil, err
		}
		note.ParentID = parent.ID
	} else if note.FolderID, err = c.folderID(in.Folder); err != nil {
		return nil, err
	}
	if note.Due, err = mcpDue(in.DueDate, in.DueTime); err != nil {
		return nil, err
	}
	note.Task, note.Priority = in.Task, recording.Priority(in.Priority)
	r, err := s.actions.CreateText(ctx, acc, note)
	if err != nil {
		return nil, err
	}
	return map[string]any{"note": c.note(r, true)}, nil
}

func (s *Server) mcpUpdateNote(ctx context.Context, acc *service.Account, raw json.RawMessage) (any, error) {
	var in struct {
		Note     string    `json:"note"`
		Title    *string   `json:"title"`
		Markdown *string   `json:"markdown"`
		Folder   *string   `json:"folder"`
		Labels   *[]string `json:"labels"`
	}
	if err := mcpArgs(raw, &in); err != nil {
		return nil, err
	}
	r, err := s.mcpResolveNote(ctx, acc, in.Note)
	if err != nil {
		return nil, err
	}
	c, err := s.mcpLoad(ctx, acc)
	if err != nil {
		return nil, err
	}
	// Check every argument before changing anything.
	var folderID string
	if in.Folder != nil {
		if folderID, err = c.folderID(*in.Folder); err != nil {
			return nil, err
		}
	}
	var labels []string
	if in.Labels != nil {
		labels = []string{}
		for _, ref := range *in.Labels {
			id, err := c.labelID(ref)
			if err != nil {
				return nil, err
			}
			labels = append(labels, id)
		}
	}
	if in.Title != nil || in.Markdown != nil {
		if r.Summary == nil {
			return nil, &mcpArgError{"this note has no text yet (it is still being processed)"}
		}
		edit := service.SummaryEdit{Title: r.Summary.Title, Markdown: r.Summary.Markdown}
		if in.Title != nil {
			edit.Title = *in.Title
		}
		if in.Markdown != nil {
			edit.Markdown = *in.Markdown
		}
		if r, err = s.actions.EditSummary(ctx, acc, r.ID, edit); err != nil {
			return nil, err
		}
	}
	if in.Folder != nil {
		if r, err = s.actions.SetFolder(ctx, acc, r.ID, folderID); err != nil {
			return nil, err
		}
	}
	if labels != nil {
		if r, err = s.actions.SetLabels(ctx, acc, r.ID, labels); err != nil {
			return nil, err
		}
	}
	return map[string]any{"note": c.note(r, false)}, nil
}

func (s *Server) mcpUpdateTask(ctx context.Context, acc *service.Account, raw json.RawMessage) (any, error) {
	var in struct {
		Note     string  `json:"note"`
		Done     *bool   `json:"done"`
		DueDate  *string `json:"dueDate"`
		DueTime  string  `json:"dueTime"`
		Priority *int    `json:"priority"`
	}
	if err := mcpArgs(raw, &in); err != nil {
		return nil, err
	}
	if in.DueDate == nil && in.DueTime != "" {
		return nil, &mcpArgError{"dueTime needs a dueDate"}
	}
	r, err := s.mcpResolveNote(ctx, acc, in.Note)
	if err != nil {
		return nil, err
	}
	c, err := s.mcpLoad(ctx, acc)
	if err != nil {
		return nil, err
	}
	if in.DueDate != nil {
		due, err := mcpDue(*in.DueDate, in.DueTime)
		if err != nil {
			return nil, err
		}
		// Keep the repeat rule and reminder of the task's current date.
		if due != nil && r.Due != nil {
			due.Repeat, due.Remind = r.Due.Repeat, r.Due.Remind
		}
		if r, err = s.actions.SetDue(ctx, acc, r.ID, due); err != nil {
			return nil, err
		}
	}
	if in.Priority != nil {
		if r, err = s.actions.SetPriority(ctx, acc, r.ID, recording.Priority(*in.Priority)); err != nil {
			return nil, err
		}
	}
	if in.Done != nil {
		if r, err = s.actions.SetDone(ctx, acc, r.ID, *in.Done); err != nil {
			return nil, err
		}
	}
	return map[string]any{"note": c.note(r, false)}, nil
}
