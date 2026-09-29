package service

import (
	"archive/zip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/bson"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/filter"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/folder"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/label"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/theme"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/timelog"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/ports"
)

// A personal backup is a zip file in the layout of a full backup (see backup.go) that holds
// only what one user owns: their notes with the files of the notes, folders, labels,
// themes, saved filters and finished time entries. It leaves out what belongs to the
// installation or to other people: the account itself (password, tokens, pairings),
// devices, and who notes and folders are shared with.
const (
	personalFormat  = "knowpod-personal-backup"
	personalVersion = 1

	collNotes       = "recordings"
	collFolders     = "folders"
	collLabels      = "labels"
	collThemes      = "themes"
	collFilters     = "filters"
	collTimeEntries = "timeEntries"

	personalPage = 100
)

var personalCollections = []string{collNotes, collFolders, collLabels, collThemes, collFilters, collTimeEntries}

// PersonalBackupService makes backups of one user's notes and content, and restores them.
type PersonalBackupService struct {
	recs        ports.RecordingRepository
	folders     ports.FolderRepository
	labels      ports.LabelRepository
	themes      ports.ThemeRepository
	filters     ports.FilterRepository
	timeEntries ports.TimeEntryRepository
	objects     ports.ObjectStore
	// recordings deletes the notes a restore replaces, with their files.
	recordings *RecordingService
	// TempDir is where an uploaded backup and the files being restored are held; empty is
	// the system's temporary directory.
	TempDir string
	// Version is the app version recorded in backups.
	Version string
	now     func() time.Time

	// mu guards the users' state: any number of backups of a user may run side by side, but a
	// restore runs alone.
	mu    sync.Mutex
	state map[string]*userBackupState
}

type userBackupState struct {
	backups   int
	restoring bool
}

// NewPersonalBackupService builds the service.
func NewPersonalBackupService(recs ports.RecordingRepository, folders ports.FolderRepository, labels ports.LabelRepository,
	themes ports.ThemeRepository, filters ports.FilterRepository, timeEntries ports.TimeEntryRepository,
	objects ports.ObjectStore, recordings *RecordingService) *PersonalBackupService {
	return &PersonalBackupService{recs: recs, folders: folders, labels: labels, themes: themes, filters: filters,
		timeEntries: timeEntries, objects: objects, recordings: recordings, now: time.Now,
		state: map[string]*userBackupState{}}
}

// begin marks a backup or (exclusive) restore of the user as running; it reports false when
// that isn't possible now. The returned func ends it.
func (s *PersonalBackupService) begin(userID string, exclusive bool) (func(), bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.state[userID]
	if st == nil {
		st = &userBackupState{}
		s.state[userID] = st
	}
	if st.restoring || (exclusive && st.backups > 0) {
		return nil, false
	}
	if exclusive {
		st.restoring = true
	} else {
		st.backups++
	}
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if exclusive {
			st.restoring = false
		} else {
			st.backups--
		}
		if !st.restoring && st.backups == 0 {
			delete(s.state, userID)
		}
	}, true
}

func (s *PersonalBackupService) owner(acc *Account) (string, error) {
	if acc == nil || acc.ID == "" {
		return "", errors.Join(ErrForbidden, errors.New("there is no user account to make a backup of"))
	}
	return acc.ID, nil
}

// noteObjects returns the files a note refers to.
func noteObjects(rec *recording.Recording) []recording.Object {
	var out []recording.Object
	for _, o := range []*recording.Object{rec.Audio, rec.Original, rec.File} {
		if o != nil && o.Key != "" {
			out = append(out, *o)
		}
	}
	for _, o := range rec.Images {
		if o.Key != "" {
			out = append(out, o)
		}
	}
	for _, a := range rec.Attachments {
		if a.Key != "" {
			out = append(out, a.Object())
		}
	}
	return out
}

// ownsObjectKey reports whether the key is one of the note's own: recordings/<owner>/<note>.<ext>
// or under recordings/<owner>/<note>/. A restore writes and later deletes these files, so it
// must never accept a key that could belong to someone else's note.
func ownsObjectKey(noteID, key string) bool {
	if slices.ContainsFunc(strings.Split(key, "/"), func(seg string) bool { return seg == "" || seg == "." || seg == ".." }) || strings.Contains(key, `\`) {
		return false
	}
	parts := strings.SplitN(key, "/", 3)
	if len(parts) != 3 || parts[0] != "recordings" || parts[1] == "" || noteID == "" || !strings.HasPrefix(parts[2], noteID) {
		return false
	}
	rest := parts[2][len(noteID):]
	return len(rest) > 1 && (rest[0] == '.' || rest[0] == '/')
}

// inFlight is a status the note's file isn't stored for: its data waits in the local spool,
// which a backup doesn't hold.
func inFlight(st recording.Status) bool {
	return st == recording.StatusUploading || st == recording.StatusReceived
}

// Backup writes a backup of the account's own content to w. The data isn't frozen while it
// is written: the documents go first and the files after them, so every file a note refers
// to is there (unless it was missing already).
func (s *PersonalBackupService) Backup(ctx context.Context, acc *Account, w io.Writer) (*BackupSummary, error) {
	owner, err := s.owner(acc)
	if err != nil {
		return nil, err
	}
	end, ok := s.begin(owner, false)
	if !ok {
		return nil, ErrBackupBusy
	}
	defer end()

	created := s.now().UTC()
	zw := zip.NewWriter(w)
	m := &BackupManifest{Format: personalFormat, Version: personalVersion, CreatedAt: created, AppVersion: s.Version,
		Owner: owner, Collections: map[string]int{}, Objects: []BackupObject{}}
	docs := func(coll string) (*docWriter, error) {
		fw, err := zw.CreateHeader(&zip.FileHeader{Name: dbPrefix + coll + ".bson", Method: zip.Deflate, Modified: created})
		if err != nil {
			return nil, err
		}
		return &docWriter{w: fw, n: func() { m.Collections[coll]++ }}, nil
	}

	// The notes, in pages so that a large archive isn't held in memory at once.
	nw, err := docs(collNotes)
	if err != nil {
		return nil, err
	}
	files := map[string]recording.Object{}
	var keys []string
	for offset := 0; ; offset += personalPage {
		page, err := s.recs.List(ctx, recording.ListFilter{OwnerID: owner, Trash: recording.TrashAny, Limit: personalPage, Offset: offset})
		if err != nil {
			return nil, fmt.Errorf("backup of the notes: %w", err)
		}
		for _, rec := range page {
			if inFlight(rec.Status) {
				continue
			}
			rec.Shares, rec.Members, rec.CreatedBy = nil, nil, ""
			if rec.AssigneeID != owner {
				rec.AssigneeID = ""
			}
			if err := nw.add(rec); err != nil {
				return nil, err
			}
			for _, o := range noteObjects(rec) {
				if _, seen := files[o.Key]; !seen {
					keys = append(keys, o.Key)
				}
				files[o.Key] = o
			}
		}
		if len(page) < personalPage {
			break
		}
	}

	if err := s.writeContent(ctx, owner, docs); err != nil {
		return nil, err
	}

	for _, key := range keys {
		o := files[key]
		body, err := s.objects.Get(ctx, key, 0, -1)
		if errors.Is(err, ErrNotFound) {
			continue // the note lost its file earlier; there is nothing to keep
		}
		if err != nil {
			return nil, fmt.Errorf("backup of the file %s: %w", key, err)
		}
		ow, err := zw.CreateHeader(&zip.FileHeader{Name: objectPrefix + key, Method: zip.Store, Modified: created})
		if err != nil {
			body.Close()
			return nil, err
		}
		n, err := io.Copy(ow, body)
		body.Close()
		if err != nil {
			return nil, fmt.Errorf("backup of the file %s: %w", key, err)
		}
		m.Objects = append(m.Objects, BackupObject{Key: key, Size: n, ContentType: o.ContentType})
	}

	mw, err := zw.CreateHeader(&zip.FileHeader{Name: manifestName, Method: zip.Deflate, Modified: created})
	if err != nil {
		return nil, err
	}
	if err := json.NewEncoder(mw).Encode(m); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return summarize(m), nil
}

// writeContent writes the collections other than the notes.
func (s *PersonalBackupService) writeContent(ctx context.Context, owner string, docs func(string) (*docWriter, error)) error {
	fw, err := docs(collFolders)
	if err != nil {
		return err
	}
	folders, err := s.folders.List(ctx, owner)
	if err != nil {
		return fmt.Errorf("backup of the folders: %w", err)
	}
	for _, f := range folders {
		if f.OwnerID != owner {
			continue
		}
		f.Shares, f.Placements = nil, nil
		if err := fw.add(f); err != nil {
			return err
		}
	}

	lw, err := docs(collLabels)
	if err != nil {
		return err
	}
	labels, err := s.labels.List(ctx, owner)
	if err != nil {
		return fmt.Errorf("backup of the labels: %w", err)
	}
	for _, l := range labels {
		if l.OwnerID == owner {
			if err := lw.add(l); err != nil {
				return err
			}
		}
	}

	tw, err := docs(collThemes)
	if err != nil {
		return err
	}
	themes, err := s.themes.List(ctx, owner)
	if err != nil {
		return fmt.Errorf("backup of the themes: %w", err)
	}
	for _, t := range themes {
		if t.OwnerID == owner {
			if err := tw.add(t); err != nil {
				return err
			}
		}
	}

	filw, err := docs(collFilters)
	if err != nil {
		return err
	}
	filters, err := s.filters.List(ctx, owner)
	if err != nil {
		return fmt.Errorf("backup of the filters: %w", err)
	}
	for _, f := range filters {
		if f.OwnerID == owner {
			if err := filw.add(f); err != nil {
				return err
			}
		}
	}

	ew, err := docs(collTimeEntries)
	if err != nil {
		return err
	}
	entries, err := s.timeEntries.List(ctx, timelog.Range{OwnerID: owner, From: time.Unix(0, 0), To: s.now().AddDate(100, 0, 0)})
	if err != nil {
		return fmt.Errorf("backup of the time entries: %w", err)
	}
	for _, e := range entries {
		// A running timer belongs to the moment, not to the archive.
		if e.OwnerID == owner && e.End != nil {
			if err := ew.add(e); err != nil {
				return err
			}
		}
	}
	return nil
}

// docWriter writes documents as concatenated raw BSON.
type docWriter struct {
	w io.Writer
	n func()
}

func (d *docWriter) add(v any) error {
	raw, err := bson.Marshal(v)
	if err != nil {
		return err
	}
	d.n()
	_, err = d.w.Write(raw)
	return err
}

// personalData is what a personal backup holds, read and checked.
type personalData struct {
	manifest *BackupManifest
	notes    []*recording.Recording
	folders  []*folder.Folder
	labels   []*label.Label
	themes   []*theme.Theme
	filters  []*filter.Filter
	entries  []*timelog.Entry
	files    map[string]*zip.File
}

// Restore replaces the account's own content with the contents of the backup read from src:
// the account's notes (with their files), folders, labels, themes, saved filters and time
// entries are deleted and the backup's are put in their place. Everything else stays: the
// account, devices, and other users' data. Notes and folders that were shared are no longer
// shared. The backup is checked completely before anything changes. A failure afterwards
// leaves the restore partly done; running it again with the same backup finishes it.
func (s *PersonalBackupService) Restore(ctx context.Context, acc *Account, src io.Reader) (*BackupSummary, error) {
	owner, err := s.owner(acc)
	if err != nil {
		return nil, err
	}
	end, ok := s.begin(owner, true)
	if !ok {
		return nil, ErrBackupBusy
	}
	defer end()

	tmp, err := os.CreateTemp(s.TempDir, "restore-*.zip")
	if err != nil {
		return nil, err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	size, err := io.Copy(tmp, src)
	if err != nil {
		return nil, err
	}
	zr, err := zip.NewReader(tmp, size)
	if err != nil {
		return nil, invalid("not a knowpod backup (%v)", err)
	}
	files := make(map[string]*zip.File, len(zr.File))
	for _, f := range zr.File {
		files[f.Name] = f
	}
	m, err := readManifest(files[manifestName], personalFormat)
	if err != nil {
		return nil, err
	}
	data, err := s.read(m, files)
	if err != nil {
		return nil, err
	}
	s.claim(owner, data)
	if err := s.check(ctx, owner, data); err != nil {
		return nil, err
	}

	if err := s.clear(ctx, owner); err != nil {
		return nil, fmt.Errorf("removing the current content: %w", err)
	}
	for _, obj := range m.Objects {
		if err := s.putObject(ctx, files[objectPrefix+obj.Key], obj); err != nil {
			return nil, fmt.Errorf("restoring file %s: %w", obj.Key, err)
		}
	}
	if err := s.create(ctx, owner, data); err != nil {
		return nil, err
	}
	return summarize(m), nil
}

func readTyped[T any](files map[string]*zip.File, m *BackupManifest, coll string) ([]*T, error) {
	f := files[dbPrefix+coll+".bson"]
	if f == nil {
		if m.Collections[coll] != 0 {
			return nil, invalid("the backup is missing the data of %s", coll)
		}
		return nil, nil
	}
	var out []*T
	err := readDocs(f, func(doc []byte) error {
		v := new(T)
		if err := bson.Unmarshal(doc, v); err != nil {
			return err
		}
		out = append(out, v)
		return nil
	})
	if err != nil {
		return nil, invalid("%s is damaged in the backup (%v)", coll, err)
	}
	if len(out) != m.Collections[coll] {
		return nil, invalid("%s holds %d documents in the backup, not the %d it should", coll, len(out), m.Collections[coll])
	}
	return out, nil
}

// read loads the documents of the backup.
func (s *PersonalBackupService) read(m *BackupManifest, files map[string]*zip.File) (*personalData, error) {
	for coll := range m.Collections {
		if !slices.Contains(personalCollections, coll) {
			return nil, invalid("the backup holds a collection this version doesn't know: %s", coll)
		}
	}
	d := &personalData{manifest: m, files: files}
	var err error
	if d.notes, err = readTyped[recording.Recording](files, m, collNotes); err != nil {
		return nil, err
	}
	if d.folders, err = readTyped[folder.Folder](files, m, collFolders); err != nil {
		return nil, err
	}
	if d.labels, err = readTyped[label.Label](files, m, collLabels); err != nil {
		return nil, err
	}
	if d.themes, err = readTyped[theme.Theme](files, m, collThemes); err != nil {
		return nil, err
	}
	if d.filters, err = readTyped[filter.Filter](files, m, collFilters); err != nil {
		return nil, err
	}
	if d.entries, err = readTyped[timelog.Entry](files, m, collTimeEntries); err != nil {
		return nil, err
	}
	return d, nil
}

// claim makes everything in the backup the account's own, and drops what points at things
// that aren't in it: the backup may come from another account, or from before some of its
// content was deleted.
func (s *PersonalBackupService) claim(owner string, d *personalData) {
	folderIDs, labelIDs, noteIDs := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, f := range d.folders {
		f.OwnerID, f.Shares, f.Placements = owner, nil, nil
		folderIDs[f.ID] = true
	}
	for _, l := range d.labels {
		l.OwnerID = owner
		labelIDs[l.ID] = true
	}
	for _, t := range d.themes {
		t.OwnerID = owner
	}
	for _, f := range d.filters {
		f.OwnerID = owner
	}
	for _, r := range d.notes {
		noteIDs[r.ID] = true
	}
	for _, f := range d.folders {
		if !folderIDs[f.ParentID] {
			f.ParentID = ""
		}
	}
	// A note's device is one of the user's own kinds ("text:<user>"), which follow the user,
	// or a registered recorder, which stays as it was.
	retarget := func(id string) string {
		for _, kind := range []string{"text:", "board:", "pocket:", "remarkable:"} {
			if d.manifest.Owner != "" && id == kind+d.manifest.Owner {
				return kind + owner
			}
		}
		return id
	}
	for _, r := range d.notes {
		r.OwnerID, r.Shares, r.Members, r.CreatedBy = owner, nil, nil, ""
		r.DeviceID = retarget(r.DeviceID)
		if r.AssigneeID != owner {
			r.AssigneeID = ""
		}
		if !folderIDs[r.FolderID] {
			r.FolderID = ""
		}
		if !noteIDs[r.ParentID] || r.ParentID == r.ID {
			r.ParentID = ""
		}
		r.Labels = slices.DeleteFunc(slices.Clone(r.Labels), func(id string) bool { return !labelIDs[id] && !isBuiltInLabel(id) })
	}
	kept := d.entries[:0]
	for _, e := range d.entries {
		if noteIDs[e.NoteID] && e.End != nil {
			e.OwnerID, e.Open = owner, false
			kept = append(kept, e)
		}
	}
	d.entries = kept
}

// check verifies, before anything is changed, that the backup is complete and safe to put
// in place: the documents are valid and don't collide with anyone else's, every file is
// there in full, and every file belongs to a note of the backup.
func (s *PersonalBackupService) check(ctx context.Context, owner string, d *personalData) error {
	seen := map[string]bool{}
	referenced := map[string]bool{}
	for _, r := range d.notes {
		if r.ID == "" || strings.Contains(r.ID, "/") || seen[r.ID] {
			return invalid("the backup holds a note with a missing, invalid or repeated ID")
		}
		seen[r.ID] = true
		if inFlight(r.Status) {
			return invalid("the note %s was not stored completely when the backup was made", r.ID)
		}
		if existing, err := s.recs.Get(ctx, r.ID); err == nil && existing.OwnerID != owner {
			return invalid("the note %s belongs to someone else", r.ID)
		} else if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		if r.DeviceID != "" || r.ClientID != "" {
			if other, err := s.recs.GetByClientID(ctx, r.DeviceID, r.ClientID); err == nil && other.ID != r.ID && other.OwnerID != owner {
				return invalid("the note %s clashes with a note of someone else", r.ID)
			} else if err != nil && !errors.Is(err, ErrNotFound) {
				return err
			}
		}
		for _, o := range noteObjects(r) {
			if !ownsObjectKey(r.ID, o.Key) {
				return invalid("the note %s refers to a file that isn't its own", r.ID)
			}
			referenced[o.Key] = true
		}
	}
	for _, f := range d.folders {
		if f.ID == "" {
			return invalid("the backup holds a folder without an ID")
		}
		if other, err := s.folders.Get(ctx, f.ID); err == nil && other.OwnerID != owner {
			return invalid("the folder %s belongs to someone else", f.ID)
		} else if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
	}
	for _, l := range d.labels {
		if l.ID == "" || isBuiltInLabel(l.ID) {
			return invalid("the backup holds a label with a missing or reserved ID")
		}
		if other, err := s.labels.Get(ctx, l.ID); err == nil && other.OwnerID != owner {
			return invalid("the label %s belongs to someone else", l.ID)
		} else if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
	}
	for _, t := range d.themes {
		if t.ID == "" {
			return invalid("the backup holds a theme without an ID")
		}
		if other, err := s.themes.Get(ctx, t.ID); err == nil && other.OwnerID != owner {
			return invalid("the theme %s belongs to someone else", t.ID)
		} else if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
	}
	for _, f := range d.filters {
		if f.ID == "" {
			return invalid("the backup holds a saved filter without an ID")
		}
		if other, err := s.filters.Get(ctx, f.ID); err == nil && other.OwnerID != owner {
			return invalid("the saved filter %s belongs to someone else", f.ID)
		} else if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
	}
	for _, e := range d.entries {
		if e.ID == "" {
			return invalid("the backup holds a time entry without an ID")
		}
		if other, err := s.timeEntries.Get(ctx, e.ID); err == nil && other.OwnerID != owner {
			return invalid("the time entry %s belongs to someone else", e.ID)
		} else if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
	}
	for _, obj := range d.manifest.Objects {
		f := d.files[objectPrefix+obj.Key]
		if f == nil || obj.Key == "" || int64(f.UncompressedSize64) != obj.Size {
			return invalid("the file %q is missing or incomplete in the backup", obj.Key)
		}
		if !referenced[obj.Key] {
			return invalid("the file %q belongs to no note of the backup", obj.Key)
		}
	}
	return nil
}

// clear deletes the account's notes (with their files) and the rest of its content.
func (s *PersonalBackupService) clear(ctx context.Context, owner string) error {
	for {
		list, err := s.recs.List(ctx, recording.ListFilter{OwnerID: owner, Limit: personalPage, Brief: true, Trash: recording.TrashAny})
		if err != nil {
			return err
		}
		if len(list) == 0 {
			break
		}
		for _, r := range list {
			if err := s.recordings.delete(ctx, r); err != nil && !errors.Is(err, ErrNotFound) {
				return err
			}
		}
	}
	if err := s.folders.DeleteByOwner(ctx, owner); err != nil {
		return err
	}
	if err := s.labels.DeleteByOwner(ctx, owner); err != nil {
		return err
	}
	if err := s.themes.DeleteByOwner(ctx, owner); err != nil {
		return err
	}
	if err := s.filters.DeleteByOwner(ctx, owner); err != nil {
		return err
	}
	return s.timeEntries.DeleteByOwner(ctx, owner)
}

// create puts the backup's documents in place.
func (s *PersonalBackupService) create(ctx context.Context, owner string, d *personalData) error {
	for _, f := range d.folders {
		if err := s.folders.Create(ctx, f); err != nil {
			return fmt.Errorf("restoring folder %s: %w", f.ID, err)
		}
	}
	for _, l := range d.labels {
		if err := s.labels.Create(ctx, l); err != nil {
			return fmt.Errorf("restoring label %s: %w", l.ID, err)
		}
	}
	for _, t := range d.themes {
		if err := s.themes.Create(ctx, t); err != nil {
			return fmt.Errorf("restoring theme %s: %w", t.ID, err)
		}
	}
	for _, f := range d.filters {
		if err := s.filters.Create(ctx, f); err != nil {
			return fmt.Errorf("restoring saved filter %s: %w", f.ID, err)
		}
	}
	var top int64
	for _, r := range d.notes {
		top = max(top, r.Number)
	}
	if top > 0 {
		if err := s.recs.ReserveNumbers(ctx, owner, top); err != nil {
			return err
		}
	}
	for _, r := range d.notes {
		if err := s.recs.Create(ctx, r); err != nil {
			return fmt.Errorf("restoring note %s: %w", r.ID, err)
		}
	}
	for _, e := range d.entries {
		if err := s.timeEntries.Create(ctx, e); err != nil {
			return fmt.Errorf("restoring time entry %s: %w", e.ID, err)
		}
	}
	return nil
}

// putObject writes one file. The store wants a seekable body, so it goes through a
// temporary file.
func (s *PersonalBackupService) putObject(ctx context.Context, f *zip.File, obj BackupObject) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	tmp, err := os.CreateTemp(s.TempDir, "restore-object-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	defer tmp.Close()
	if _, err := io.Copy(tmp, rc); err != nil {
		return err
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return err
	}
	ct := obj.ContentType
	if ct == "" {
		ct = "application/octet-stream"
	}
	return s.objects.Put(ctx, obj.Key, tmp, obj.Size, ct)
}
