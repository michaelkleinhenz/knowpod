package http

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/audio/audiotest"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/folder"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/service"
)

func ptr64(v int64) *int64 { return &v }

func TestDeviceUploadEndToEnd(t *testing.T) {
	f := newAPIFixture(t)
	admin := f.signedIn(adminEmail, adminPassword)

	var reg registerDeviceResponse
	if res := admin.do("POST", "/api/v1/devices", map[string]string{"name": "rec-1"}, nil, &reg); res.StatusCode != 201 || reg.Token == "" {
		t.Fatalf("register: %d %+v", res.StatusCode, reg)
	}
	dev := f.script(reg.Token)

	wav := audiotest.WAV(16000, 16, audiotest.Samples(1, 16000, 16))
	sum := sha256.Sum256(wav)
	create := createUploadRequest{RecordingID: "2026-09-26T10-00-00", Size: int64(len(wav)), SHA256: hex.EncodeToString(sum[:]),
		Highlights: []service.HighlightInput{{OffsetMs: ptr64(300)}}}

	var up uploadResponse
	if res := dev.do("POST", "/api/v1/uploads", create, nil, &up); res.StatusCode != 201 || up.Offset != 0 {
		t.Fatalf("create: %d %+v", res.StatusCode, up)
	}
	var again uploadResponse
	if res := dev.do("POST", "/api/v1/uploads", create, nil, &again); res.StatusCode != 200 || again.UploadID != up.UploadID {
		t.Fatalf("create retry: %d %+v", res.StatusCode, again)
	}

	path := "/api/v1/uploads/" + up.UploadID
	half := len(wav) / 2
	patch := func(off int, chunk []byte, out any) int {
		return dev.do("PATCH", path, chunk, map[string]string{uploadOffsetHeader: strconv.Itoa(off)}, out).StatusCode
	}
	if code := patch(0, wav[:half], &up); code != 200 || up.Offset != int64(half) {
		t.Fatalf("first chunk: %d %+v", code, up)
	}
	var mismatch errResponse
	if code := patch(0, wav[:10], &mismatch); code != 409 || mismatch.Offset == nil || *mismatch.Offset != int64(half) {
		t.Fatalf("offset mismatch: %d %+v", code, mismatch)
	}
	if code := patch(half, append(append([]byte{}, wav[half:]...), 0), nil); code != 413 {
		t.Fatalf("overrun: %d", code)
	}
	if code := patch(half, wav[half:], &up); code != 200 || up.Status != "received" {
		t.Fatalf("last chunk: %d %+v", code, up)
	}

	id := up.UploadID
	if res := admin.do("GET", "/api/v1/recordings/"+id+"/audio", nil, nil, nil); res.StatusCode != 409 {
		t.Fatalf("audio before archive: %d", res.StatusCode)
	}
	if n := f.worker.RunOnce(context.Background()); n != 1 {
		t.Fatalf("worker processed %d", n)
	}
	// The device replaces the highlights after the upload.
	var hl struct {
		Highlights []recording.Highlight `json:"highlights"`
	}
	if res := dev.do("PUT", path+"/highlights", map[string]any{"highlights": []map[string]int64{{"offsetMs": 900}, {"offsetMs": 100}}}, nil, &hl); res.StatusCode != 200 ||
		len(hl.Highlights) != 2 || hl.Highlights[0].OffsetMs != 100 {
		t.Fatalf("set highlights: %d %+v", res.StatusCode, hl)
	}
	var e2 errResponse
	if res := dev.do("PUT", path+"/highlights", map[string]any{"highlights": []map[string]int64{{"offsetMs": -5}}}, nil, &e2); res.StatusCode != 400 || e2.Code != "invalid_input" {
		t.Fatalf("invalid highlight: %d %+v", res.StatusCode, e2)
	}
	var rec recording.Recording
	if res := admin.do("GET", "/api/v1/recordings/"+id, nil, nil, &rec); res.StatusCode != 200 || rec.Status != recording.StatusStored || rec.OwnerID == "" {
		t.Fatalf("recording: %d %+v", res.StatusCode, rec)
	}
	var flac []byte
	if res := admin.do("GET", "/api/v1/recordings/"+id+"/audio", nil, nil, &flac); res.StatusCode != 200 || string(flac[:4]) != "fLaC" {
		t.Fatalf("audio: %d", res.StatusCode)
	}

	// Rotating the token and removing the device lock the old credentials out.
	var rotated registerDeviceResponse
	if res := admin.do("POST", "/api/v1/devices/"+reg.Device.ID+"/token", nil, nil, &rotated); res.StatusCode != 200 || rotated.Token == reg.Token {
		t.Fatalf("rotate: %d", res.StatusCode)
	}
	if res := dev.do("GET", path, nil, nil, nil); res.StatusCode != 401 {
		t.Fatalf("old token after rotation: %d", res.StatusCode)
	}
	if res := admin.do("DELETE", "/api/v1/devices/"+reg.Device.ID, nil, nil, nil); res.StatusCode != 204 {
		t.Fatalf("revoke: %d", res.StatusCode)
	}
	if res := f.script(rotated.Token).do("GET", path, nil, nil, nil); res.StatusCode != 401 {
		t.Fatalf("revoked device: %d", res.StatusCode)
	}
}

func TestManualUploadAndIsolation(t *testing.T) {
	f := newAPIFixture(t)
	admin := f.signedIn(adminEmail, adminPassword)
	admin.do("POST", "/api/v1/admin/users", map[string]string{"email": "bob@example.com", "password": "bob-password"}, nil, nil)
	bob := f.signedIn("bob@example.com", "bob-password")

	wav := audiotest.WAV(16000, 16, audiotest.Samples(1, 8000, 16))
	var rec recording.Recording
	res := bob.do("POST", "/api/v1/recordings", wav, map[string]string{
		"Content-Type": "audio/wav", "X-Filename": url.QueryEscape("Standup notes.wav"), "X-Recorded-At": "2026-09-25T09:30:00Z",
	}, &rec)
	if res.StatusCode != 201 || rec.Title != "Standup notes" || rec.Source != recording.SourceUpload || rec.RecordedAt == nil {
		t.Fatalf("upload: %d %+v", res.StatusCode, rec)
	}
	if res := bob.do("POST", "/api/v1/recordings", []byte("OggS\x00\x02 not supported"), nil, nil); res.StatusCode != 415 {
		t.Fatalf("ogg upload: %d", res.StatusCode)
	}

	// Each user sees only their own recordings; ADMIN_TOKEN sees all.
	var list []recording.Recording
	bob.do("GET", "/api/v1/recordings", nil, nil, &list)
	if len(list) != 1 {
		t.Fatalf("bob sees %d recordings", len(list))
	}
	admin.do("GET", "/api/v1/recordings", nil, nil, &list)
	if len(list) != 0 {
		t.Fatalf("admin sees %d of bob's recordings in the UI", len(list))
	}
	if res := admin.do("GET", "/api/v1/recordings/"+rec.ID, nil, nil, nil); res.StatusCode != 404 {
		t.Fatalf("admin opening bob's recording: %d", res.StatusCode)
	}
	if res := admin.do("DELETE", "/api/v1/recordings/"+rec.ID, nil, nil, nil); res.StatusCode != 404 {
		t.Fatalf("admin deleting bob's recording: %d", res.StatusCode)
	}
	f.script(adminToken).do("GET", "/api/v1/recordings", nil, nil, &list)
	if len(list) != 1 {
		t.Fatalf("ADMIN_TOKEN sees %d recordings", len(list))
	}

	// The uploaded WAV is archived as FLAC.
	f.worker.RunOnce(context.Background())
	bob.do("GET", "/api/v1/recordings/"+rec.ID, nil, nil, &rec)
	if rec.Status != recording.StatusStored || rec.Audio == nil || rec.Audio.ContentType != "audio/flac" {
		t.Fatalf("archived: %+v", rec)
	}
	// Summaries can be edited once they exist.
	var e errResponse
	if res := bob.do("PUT", "/api/v1/recordings/"+rec.ID+"/summary", map[string]string{"title": "T", "markdown": "M"}, nil, &e); res.StatusCode != 409 || e.Code != "not_ready" {
		t.Fatalf("edit without summary: %d %+v", res.StatusCode, e)
	}
	stored, _ := f.recs.Get(context.Background(), rec.ID)
	stored.Summary = &recording.Summary{Title: "AI title", Markdown: "AI text", Model: "m"}
	_ = f.recs.Update(context.Background(), stored)
	if res := admin.do("PUT", "/api/v1/recordings/"+rec.ID+"/summary", map[string]string{"title": "T", "markdown": "M"}, nil, nil); res.StatusCode != 404 {
		t.Fatalf("admin editing bob's summary: %d", res.StatusCode)
	}
	if res := bob.do("PUT", "/api/v1/recordings/"+rec.ID+"/summary", map[string]string{"title": "My title", "markdown": "## Mine\n- point"}, nil, &rec); res.StatusCode != 200 ||
		rec.Summary.Title != "My title" || rec.Summary.EditedAt == nil {
		t.Fatalf("edit: %d %+v", res.StatusCode, rec.Summary)
	}

	// Downloads: Markdown/text files, or JSON.
	if res := bob.do("GET", "/api/v1/recordings/"+rec.ID+"/transcript", nil, nil, &e); res.StatusCode != 409 || e.Code != "not_ready" {
		t.Fatalf("transcript before transcription: %d %+v", res.StatusCode, e)
	}
	stored, _ = f.recs.Get(context.Background(), rec.ID)
	stored.Transcript = &recording.Transcript{Text: "[0:01] Speaker 1: Hallo", Model: "m"}
	_ = f.recs.Update(context.Background(), stored)
	var md []byte
	res = bob.do("GET", "/api/v1/recordings/"+rec.ID+"/summary", nil, nil, &md)
	if res.StatusCode != 200 || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/markdown") ||
		string(md) != "# My title\n\n## Mine\n- point\n" || !strings.Contains(res.Header.Get("Content-Disposition"), `filename="My title - summary.md"`) {
		t.Fatalf("summary download: %d %q %v", res.StatusCode, md, res.Header)
	}
	var txt []byte
	res = bob.do("GET", "/api/v1/recordings/"+rec.ID+"/transcript", nil, nil, &txt)
	if res.StatusCode != 200 || string(txt) != "[0:01] Speaker 1: Hallo\n" || !strings.HasPrefix(res.Header.Get("Content-Type"), "text/plain") {
		t.Fatalf("transcript download: %d %q", res.StatusCode, txt)
	}
	var tj recording.Transcript
	if res := bob.do("GET", "/api/v1/recordings/"+rec.ID+"/transcript?format=json", nil, nil, &tj); res.StatusCode != 200 || tj.Model != "m" {
		t.Fatalf("transcript json: %d %+v", res.StatusCode, tj)
	}
	var sj recording.Summary
	if res := bob.do("GET", "/api/v1/recordings/"+rec.ID+"/summary?format=json", nil, nil, &sj); res.StatusCode != 200 || sj.Title != "My title" {
		t.Fatalf("summary json: %d %+v", res.StatusCode, sj)
	}
	if res := admin.do("GET", "/api/v1/recordings/"+rec.ID+"/summary", nil, nil, nil); res.StatusCode != 404 {
		t.Fatalf("admin downloading bob's summary: %d", res.StatusCode)
	}
	if res := f.script(adminToken).do("GET", "/api/v1/recordings/"+rec.ID+"/transcript", nil, nil, nil); res.StatusCode != 200 {
		t.Fatalf("script download: %d", res.StatusCode)
	}

	if res := bob.do("DELETE", "/api/v1/recordings/"+rec.ID, nil, nil, nil); res.StatusCode != 204 {
		t.Fatalf("delete own: %d", res.StatusCode)
	}
}

func TestTextNotes(t *testing.T) {
	f := newAPIFixture(t)
	admin := f.signedIn(adminEmail, adminPassword)

	var e errResponse
	if res := admin.do("POST", "/api/v1/recordings/text", map[string]string{"title": " ", "markdown": "x"}, nil, &e); res.StatusCode != 400 || e.Code != "invalid_input" {
		t.Fatalf("empty title: %d %+v", res.StatusCode, e)
	}

	var note recording.Recording
	if res := admin.do("POST", "/api/v1/recordings/text", map[string]string{"title": " Shopping ", "markdown": "- milk\r\n- eggs"}, nil, &note); res.StatusCode != 201 ||
		note.Type != recording.TypeText || note.Status != recording.StatusSummarized || note.Summary == nil ||
		note.Summary.Title != "Shopping" || note.Summary.Markdown != "- milk\n- eggs" {
		t.Fatalf("create: %d %+v", res.StatusCode, note)
	}
	path := "/api/v1/recordings/" + note.ID

	// Listed with its type; nothing for the worker to do.
	var list []recording.Recording
	if res := admin.do("GET", "/api/v1/recordings", nil, nil, &list); res.StatusCode != 200 || len(list) != 1 || list[0].Type != recording.TypeText {
		t.Fatalf("list: %d %+v", res.StatusCode, list)
	}
	if n := f.worker.RunOnce(context.Background()); n != 0 {
		t.Fatalf("worker processed %d text notes", n)
	}

	// Edited like a summary.
	var edited recording.Recording
	if res := admin.do("PUT", path+"/summary", map[string]string{"title": "Groceries", "markdown": "- bread"}, nil, &edited); res.StatusCode != 200 ||
		edited.Summary.Title != "Groceries" || edited.Summary.EditedAt == nil {
		t.Fatalf("edit: %d %+v", res.StatusCode, edited)
	}
	res := admin.do("GET", path+"/summary", nil, nil, nil)
	if res.StatusCode != 200 || !strings.Contains(res.Header.Get("Content-Disposition"), "Groceries - note.md") {
		t.Fatalf("download: %d %s", res.StatusCode, res.Header.Get("Content-Disposition"))
	}

	// Audio actions don't apply.
	for _, p := range []string{"/retranscribe", "/resummarize"} {
		if res := admin.do("POST", path+p, nil, nil, nil); res.StatusCode != 409 {
			t.Errorf("%s: %d", p, res.StatusCode)
		}
	}
	for _, p := range []string{"/audio", "/transcript"} {
		if res := admin.do("GET", path+p, nil, nil, nil); res.StatusCode != 409 {
			t.Errorf("%s: %d", p, res.StatusCode)
		}
	}

	if res := admin.do("DELETE", path, nil, nil, nil); res.StatusCode != 204 {
		t.Fatalf("delete: %d", res.StatusCode)
	}
	if res := admin.do("GET", path, nil, nil, nil); res.StatusCode != 404 {
		t.Fatalf("get after delete: %d", res.StatusCode)
	}
}

func TestLabels(t *testing.T) {
	f := newAPIFixture(t)
	admin := f.signedIn(adminEmail, adminPassword)

	var list []service.LabelView
	if res := admin.do("GET", "/api/v1/labels", nil, nil, &list); res.StatusCode != 200 || len(list) != 1 || list[0].ID != "task" || !list[0].BuiltIn {
		t.Fatalf("list: %d %+v", res.StatusCode, list)
	}
	var e errResponse
	for _, in := range []service.LabelInput{{Name: "work", Color: "red"}, {Name: "TASK", Color: "#aabbcc"}, {Name: "", Color: "#aabbcc"}} {
		if res := admin.do("POST", "/api/v1/labels", in, nil, &e); res.StatusCode != 400 {
			t.Errorf("create %+v: %d", in, res.StatusCode)
		}
	}
	var work service.LabelView
	if res := admin.do("POST", "/api/v1/labels", service.LabelInput{Name: " Work ", Color: "#AA0000"}, nil, &work); res.StatusCode != 201 || work.Name != "Work" || work.Color != "#aa0000" {
		t.Fatalf("create: %d %+v", res.StatusCode, work)
	}
	if res := admin.do("POST", "/api/v1/labels", service.LabelInput{Name: "work", Color: "#aa0000"}, nil, nil); res.StatusCode != 400 {
		t.Fatalf("duplicate name: %d", res.StatusCode)
	}
	if res := admin.do("PUT", "/api/v1/labels/task", service.LabelInput{Name: "Todo", Color: "#aa0000"}, nil, nil); res.StatusCode != 403 {
		t.Fatalf("change built-in: %d", res.StatusCode)
	}
	if res := admin.do("PUT", "/api/v1/labels/"+work.ID, service.LabelInput{Name: "Job", Color: "#00aa00"}, nil, &work); res.StatusCode != 200 || work.Name != "Job" {
		t.Fatalf("update: %d %+v", res.StatusCode, work)
	}

	var note recording.Recording
	admin.do("POST", "/api/v1/recordings/text", map[string]string{"title": "Call Bob", "markdown": ""}, nil, &note)
	path := "/api/v1/recordings/" + note.ID
	if res := admin.do("PUT", path+"/done", map[string]bool{"done": true}, nil, nil); res.StatusCode != 400 {
		t.Fatalf("done without task label: %d", res.StatusCode)
	}
	for _, bad := range [][]string{{"nope"}, {"task", "task"}} {
		if res := admin.do("PUT", path+"/labels", map[string][]string{"labels": bad}, nil, nil); res.StatusCode != 400 {
			t.Errorf("labels %v: %d", bad, res.StatusCode)
		}
	}
	if res := admin.do("PUT", path+"/labels", map[string][]string{"labels": {"task", work.ID}}, nil, &note); res.StatusCode != 200 || len(note.Labels) != 2 {
		t.Fatalf("set labels: %d %+v", res.StatusCode, note.Labels)
	}
	if res := admin.do("PUT", path+"/done", map[string]bool{"done": true}, nil, &note); res.StatusCode != 200 || !note.Done {
		t.Fatalf("done: %d %+v", res.StatusCode, note)
	}
	var got recording.Recording
	if admin.do("GET", path, nil, nil, &got); !got.Done {
		t.Fatal("check mark not stored")
	}

	// Deleting a label takes it off the note; removing the task label clears the check mark.
	if res := admin.do("DELETE", "/api/v1/labels/"+work.ID, nil, nil, nil); res.StatusCode != 204 {
		t.Fatalf("delete label: %d", res.StatusCode)
	}
	if admin.do("GET", path, nil, nil, &got); len(got.Labels) != 1 || got.Labels[0] != "task" {
		t.Fatalf("labels after delete: %v", got.Labels)
	}
	var cleared recording.Recording
	if res := admin.do("PUT", path+"/labels", map[string][]string{"labels": {}}, nil, &cleared); res.StatusCode != 200 || cleared.Done || len(cleared.Labels) != 0 {
		t.Fatalf("clear labels: %d %+v", res.StatusCode, cleared)
	}
}

func TestFolders(t *testing.T) {
	f := newAPIFixture(t)
	admin := f.signedIn(adminEmail, adminPassword)

	var list []folder.Folder
	if res := admin.do("GET", "/api/v1/folders", nil, nil, &list); res.StatusCode != 200 || len(list) != 0 {
		t.Fatalf("list: %d %+v", res.StatusCode, list)
	}
	var work, sub folder.Folder
	if res := admin.do("POST", "/api/v1/folders", service.FolderInput{Name: " Work "}, nil, &work); res.StatusCode != 201 || work.Name != "Work" {
		t.Fatalf("create: %d %+v", res.StatusCode, work)
	}
	for _, in := range []service.FolderInput{{Name: ""}, {Name: "work"}, {Name: "X", ParentID: "nope"}} {
		if res := admin.do("POST", "/api/v1/folders", in, nil, nil); res.StatusCode != 400 {
			t.Errorf("create %+v: %d", in, res.StatusCode)
		}
	}
	if res := admin.do("POST", "/api/v1/folders", service.FolderInput{Name: "Work", ParentID: work.ID}, nil, &sub); res.StatusCode != 201 || sub.ParentID != work.ID {
		t.Fatalf("create sub: %d %+v", res.StatusCode, sub)
	}
	// A folder can't be moved into itself or into one of its own folders.
	for _, parent := range []string{work.ID, sub.ID} {
		if res := admin.do("PUT", "/api/v1/folders/"+work.ID, service.FolderInput{Name: "Work", ParentID: parent}, nil, nil); res.StatusCode != 400 {
			t.Errorf("move into %s: %d", parent, res.StatusCode)
		}
	}

	var note recording.Recording
	admin.do("POST", "/api/v1/recordings/text", map[string]string{"title": "Plan", "markdown": ""}, nil, &note)
	path := "/api/v1/recordings/" + note.ID + "/folder"
	if res := admin.do("PUT", path, map[string]string{"folderId": "nope"}, nil, nil); res.StatusCode != 400 {
		t.Fatalf("unknown folder: %d", res.StatusCode)
	}
	if res := admin.do("PUT", path, map[string]string{"folderId": sub.ID}, nil, &note); res.StatusCode != 200 || note.FolderID != sub.ID {
		t.Fatalf("move note: %d %+v", res.StatusCode, note.FolderID)
	}

	// Deleting a folder moves its notes and folders up into its parent.
	if res := admin.do("DELETE", "/api/v1/folders/"+sub.ID, nil, nil, nil); res.StatusCode != 204 {
		t.Fatalf("delete sub: %d", res.StatusCode)
	}
	var got recording.Recording
	if admin.do("GET", "/api/v1/recordings/"+note.ID, nil, nil, &got); got.FolderID != work.ID {
		t.Fatalf("note after deleting its folder: %q", got.FolderID)
	}
	var child folder.Folder
	admin.do("POST", "/api/v1/folders", service.FolderInput{Name: "Child", ParentID: work.ID}, nil, &child)
	if res := admin.do("DELETE", "/api/v1/folders/"+work.ID, nil, nil, nil); res.StatusCode != 204 {
		t.Fatalf("delete: %d", res.StatusCode)
	}
	var top recording.Recording
	if admin.do("GET", "/api/v1/recordings/"+note.ID, nil, nil, &top); top.FolderID != "" {
		t.Fatalf("note after deleting top folder: %q", got.FolderID)
	}
	list = nil
	if admin.do("GET", "/api/v1/folders", nil, nil, &list); len(list) != 1 || list[0].ID != child.ID || list[0].ParentID != "" {
		t.Fatalf("folders after delete: %+v", list)
	}
	var moved recording.Recording
	if res := admin.do("PUT", path, map[string]string{"folderId": ""}, nil, &moved); res.StatusCode != 200 || moved.FolderID != "" {
		t.Fatalf("move to top: %d", res.StatusCode)
	}
}

func TestSubNotes(t *testing.T) {
	f := newAPIFixture(t)
	admin := f.signedIn(adminEmail, adminPassword)

	var work folder.Folder
	admin.do("POST", "/api/v1/folders", service.FolderInput{Name: "Work"}, nil, &work)
	var head, sub, subsub recording.Recording
	admin.do("POST", "/api/v1/recordings/text", map[string]string{"title": "Project", "markdown": ""}, nil, &head)
	admin.do("PUT", "/api/v1/recordings/"+head.ID+"/folder", map[string]string{"folderId": work.ID}, nil, &head)

	// Sub-notes are created under a note, or moved under one.
	if res := admin.do("POST", "/api/v1/recordings/text", map[string]string{"title": "Tasks", "markdown": "", "parentId": head.ID}, nil, &sub); res.StatusCode != 201 || sub.ParentID != head.ID {
		t.Fatalf("create sub-note: %d %+v", res.StatusCode, sub.ParentID)
	}
	if res := admin.do("POST", "/api/v1/recordings/text", map[string]string{"title": "X", "markdown": "", "parentId": "nope"}, nil, nil); res.StatusCode != 400 {
		t.Fatalf("create under unknown note: %d", res.StatusCode)
	}
	admin.do("POST", "/api/v1/recordings/text", map[string]string{"title": "Details", "markdown": ""}, nil, &subsub)
	admin.do("PUT", "/api/v1/recordings/"+subsub.ID+"/folder", map[string]string{"folderId": work.ID}, nil, &subsub)
	path := "/api/v1/recordings/" + subsub.ID + "/parent"
	var moved recording.Recording
	if res := admin.do("PUT", path, map[string]string{"parentId": sub.ID}, nil, &moved); res.StatusCode != 200 || moved.ParentID != sub.ID || moved.FolderID != "" {
		t.Fatalf("move under note: %d %+v %+v", res.StatusCode, moved.ParentID, moved.FolderID)
	}

	// A note can't go under itself or one of its own sub-notes.
	for _, parent := range []string{head.ID, sub.ID, subsub.ID, "nope"} {
		if res := admin.do("PUT", "/api/v1/recordings/"+head.ID+"/parent", map[string]string{"parentId": parent}, nil, nil); res.StatusCode != 400 {
			t.Errorf("move head under %s: %d", parent, res.StatusCode)
		}
	}

	// Deleting a note moves its sub-notes up to where it was.
	if res := admin.do("DELETE", "/api/v1/recordings/"+sub.ID, nil, nil, nil); res.StatusCode != 204 {
		t.Fatalf("delete sub-note: %d", res.StatusCode)
	}
	var got recording.Recording
	if admin.do("GET", "/api/v1/recordings/"+subsub.ID, nil, nil, &got); got.ParentID != head.ID || got.FolderID != "" {
		t.Fatalf("after deleting its parent: %+v %+v", got.ParentID, got.FolderID)
	}
	if res := admin.do("DELETE", "/api/v1/recordings/"+head.ID, nil, nil, nil); res.StatusCode != 204 {
		t.Fatalf("delete head: %d", res.StatusCode)
	}
	var up recording.Recording
	if admin.do("GET", "/api/v1/recordings/"+subsub.ID, nil, nil, &up); up.ParentID != "" || up.FolderID != work.ID {
		t.Fatalf("after deleting the head: %+v %+v", up.ParentID, up.FolderID)
	}

	// Moving a sub-note into a folder takes it out from under its parent.
	var other recording.Recording
	admin.do("POST", "/api/v1/recordings/text", map[string]string{"title": "Other", "markdown": ""}, nil, &other)
	admin.do("PUT", path, map[string]string{"parentId": other.ID}, nil, nil)
	var out recording.Recording
	if res := admin.do("PUT", "/api/v1/recordings/"+subsub.ID+"/folder", map[string]string{"folderId": work.ID}, nil, &out); res.StatusCode != 200 || out.ParentID != "" || out.FolderID != work.ID {
		t.Fatalf("move sub-note into folder: %d %+v %+v", res.StatusCode, out.ParentID, out.FolderID)
	}
}

func TestBoards(t *testing.T) {
	f := newAPIFixture(t)
	admin := f.signedIn(adminEmail, adminPassword)

	var board recording.Recording
	if res := admin.do("POST", "/api/v1/recordings/board", map[string]string{"title": " Sprint "}, nil, &board); res.StatusCode != 201 ||
		board.Type != recording.TypeBoard || board.Status != recording.StatusSummarized || board.Summary == nil || board.Summary.Title != "Sprint" ||
		board.Board == nil || len(board.Board.Columns) != 3 || board.Board.Columns[0].Name != "Todo" || board.Board.Columns[0].ID == "" {
		t.Fatalf("create: %d %+v", res.StatusCode, board)
	}
	if n := f.worker.RunOnce(context.Background()); n != 0 {
		t.Fatalf("worker processed %d boards", n)
	}
	path := "/api/v1/recordings/" + board.ID + "/board"

	var work folder.Folder
	admin.do("POST", "/api/v1/folders", service.FolderInput{Name: "Work"}, nil, &work)
	var note recording.Recording
	admin.do("POST", "/api/v1/recordings/text", map[string]string{"title": "Plan", "markdown": ""}, nil, &note)

	cols := board.Board.Columns
	for _, bad := range []recording.Board{
		{Scope: recording.BoardScope{Kind: "folder", ID: "nope"}, Columns: cols},
		{Scope: recording.BoardScope{Kind: "label", ID: ""}, Columns: cols},
		{Scope: recording.BoardScope{Kind: "tag", ID: "x"}, Columns: cols},
		{Scope: recording.BoardScope{Kind: "folder", ID: work.ID}},
		{Columns: []recording.BoardColumn{{Name: " "}}},
		{Columns: []recording.BoardColumn{{ID: "a", Name: "A"}, {ID: "a", Name: "B"}}},
		{Columns: []recording.BoardColumn{{Name: "A", Notes: []string{note.ID}}, {Name: "B", Notes: []string{note.ID}}}},
	} {
		if res := admin.do("PUT", path, bad, nil, nil); res.StatusCode != 400 {
			t.Errorf("set %+v: %d", bad, res.StatusCode)
		}
	}

	// Rename a column, add one, and put the note into it.
	set := recording.Board{
		Scope:   recording.BoardScope{Kind: "folder", ID: work.ID},
		Columns: []recording.BoardColumn{{ID: cols[0].ID, Name: "Backlog"}, cols[1], cols[2], {Name: " Review ", Notes: []string{note.ID}}},
	}
	var got recording.Recording
	if res := admin.do("PUT", path, set, nil, &got); res.StatusCode != 200 || len(got.Board.Columns) != 4 ||
		got.Board.Columns[0].Name != "Backlog" || got.Board.Columns[3].Name != "Review" || got.Board.Columns[3].ID == "" ||
		len(got.Board.Columns[3].Notes) != 1 || got.Board.Scope.ID != work.ID {
		t.Fatalf("set: %d %+v", res.StatusCode, got.Board)
	}

	// Only boards have columns.
	if res := admin.do("PUT", "/api/v1/recordings/"+note.ID+"/board", set, nil, nil); res.StatusCode != 400 {
		t.Fatalf("set on a text note: %d", res.StatusCode)
	}
	// Other users don't see it.
	admin.do("POST", "/api/v1/admin/users", map[string]string{"email": "bob@example.com", "password": "bob-password"}, nil, nil)
	bob := f.signedIn("bob@example.com", "bob-password")
	if res := bob.do("PUT", path, set, nil, nil); res.StatusCode != 404 {
		t.Fatalf("other user: %d", res.StatusCode)
	}

	// Deleting the folder points the board at the folder its notes moved into.
	admin.do("DELETE", "/api/v1/folders/"+work.ID, nil, nil, nil)
	if admin.do("GET", "/api/v1/recordings/"+board.ID, nil, nil, &got); got.Board.Scope.Kind != "folder" || got.Board.Scope.ID != "" {
		t.Fatalf("scope after deleting the folder: %+v", got.Board.Scope)
	}

	// Deleting a label clears the scope of boards showing it.
	var l service.LabelView
	admin.do("POST", "/api/v1/labels", service.LabelInput{Name: "Work", Color: "#aa0000"}, nil, &l)
	set.Scope = recording.BoardScope{Kind: "label", ID: l.ID}
	if res := admin.do("PUT", path, set, nil, &got); res.StatusCode != 200 {
		t.Fatalf("label scope: %d", res.StatusCode)
	}
	admin.do("DELETE", "/api/v1/labels/"+l.ID, nil, nil, nil)
	if admin.do("GET", "/api/v1/recordings/"+board.ID, nil, nil, &got); got.Board.Scope.Kind != "" || len(got.Board.Columns) != 4 {
		t.Fatalf("board after deleting its label: %+v", got.Board)
	}

	// Renamed like a text note.
	if res := admin.do("PUT", "/api/v1/recordings/"+board.ID+"/summary", map[string]string{"title": "Q3", "markdown": ""}, nil, &got); res.StatusCode != 200 || got.Summary.Title != "Q3" {
		t.Fatalf("rename: %d", res.StatusCode)
	}
}

func TestNoteNumbers(t *testing.T) {
	f := newAPIFixture(t)
	admin := f.signedIn(adminEmail, adminPassword)
	admin.do("POST", "/api/v1/admin/users", map[string]string{"email": "bob@example.com", "password": "bob-password"}, nil, nil)
	bob := f.signedIn("bob@example.com", "bob-password")

	// Every user counts their own notes, whatever their type.
	var a1, a2, b1, a3 recording.Recording
	admin.do("POST", "/api/v1/recordings/text", map[string]string{"title": "One", "markdown": ""}, nil, &a1)
	admin.do("POST", "/api/v1/recordings/board", map[string]string{"title": "Two"}, nil, &a2)
	bob.do("POST", "/api/v1/recordings/text", map[string]string{"title": "Bob's", "markdown": ""}, nil, &b1)
	if a1.Number != 1 || a2.Number != 2 || b1.Number != 1 {
		t.Fatalf("numbers: %d %d %d", a1.Number, a2.Number, b1.Number)
	}
	// Numbers are never reused.
	admin.do("DELETE", "/api/v1/recordings/"+a2.ID, nil, nil, nil)
	admin.do("POST", "/api/v1/recordings/text", map[string]string{"title": "Three", "markdown": "see #1"}, nil, &a3)
	if a3.Number != 3 {
		t.Fatalf("after delete: %d", a3.Number)
	}
	// Kept when the note is edited.
	var edited recording.Recording
	admin.do("PUT", "/api/v1/recordings/"+a3.ID+"/summary", map[string]string{"title": "Three", "markdown": "see #1 and #3"}, nil, &edited)
	if edited.Number != 3 {
		t.Fatalf("after edit: %d", edited.Number)
	}

	// Looked up by number, among one's own notes only.
	var list []recording.Recording
	if res := admin.do("GET", "/api/v1/recordings?number=1", nil, nil, &list); res.StatusCode != 200 || len(list) != 1 || list[0].ID != a1.ID {
		t.Fatalf("by number: %d %+v", res.StatusCode, list)
	}
	list = nil
	if bob.do("GET", "/api/v1/recordings?number=3", nil, nil, &list); len(list) != 0 {
		t.Fatalf("bob sees admin's #3: %+v", list)
	}
}

func TestTaskEndpoints(t *testing.T) {
	f := newAPIFixture(t)
	c := f.signedIn(adminEmail, adminPassword)
	var note recording.Recording
	if res := c.do("POST", "/api/v1/recordings/text", map[string]any{
		"title": "Pay rent", "markdown": "", "due": map[string]any{"date": "2099-10-01", "remind": 0}, "priority": 2,
	}, nil, &note); res.StatusCode != 201 {
		t.Fatalf("create: %d", res.StatusCode)
	}
	if note.Due == nil || note.Priority != 2 || note.RemindAt == nil || len(note.Labels) != 1 || note.Labels[0] != "task" {
		t.Fatalf("created %+v", note)
	}

	var got recording.Recording
	body := map[string]any{"due": map[string]any{"date": "2099-10-01", "time": "08:00", "repeat": map[string]any{"every": 1, "unit": "month"}}}
	if res := c.do("PUT", "/api/v1/recordings/"+note.ID+"/due", body, nil, &got); res.StatusCode != 200 {
		t.Fatalf("due: %d", res.StatusCode)
	}
	if got.Due.Repeat == nil || got.Due.Repeat.MonthDay != 1 || got.RemindAt != nil {
		t.Errorf("due %+v, remind %v", got.Due, got.RemindAt)
	}
	if res := c.do("PUT", "/api/v1/recordings/"+note.ID+"/done", map[string]bool{"done": true}, nil, &got); res.StatusCode != 200 || got.Done || got.Due.Date != "2099-11-01" {
		t.Errorf("checking off a monthly task: %d, %+v", res.StatusCode, got.Due)
	}
	if res := c.do("PUT", "/api/v1/recordings/"+note.ID+"/priority", map[string]int{"priority": 9}, nil, nil); res.StatusCode != 400 {
		t.Errorf("priority 9: %d", res.StatusCode)
	}
	var cleared recording.Recording
	if res := c.do("PUT", "/api/v1/recordings/"+note.ID+"/due", map[string]any{"due": nil}, nil, &cleared); res.StatusCode != 200 || cleared.Due != nil {
		t.Errorf("clearing: %d %+v", res.StatusCode, cleared.Due)
	}
	if res := c.do("POST", "/api/v1/recordings/"+note.ID+"/action-items/x/task", nil, nil, nil); res.StatusCode != 404 {
		t.Errorf("unknown action item: %d", res.StatusCode)
	}
	if res := f.browser().do("PUT", "/api/v1/recordings/"+note.ID+"/priority", map[string]int{"priority": 1}, nil, nil); res.StatusCode != 401 {
		t.Errorf("signed out: %d", res.StatusCode)
	}
}
