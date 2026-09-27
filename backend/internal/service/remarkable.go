package service

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/folder"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/tablet"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/ports"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/remarkable"
)

// RemarkableFolder is the name of the knowpod folder that imported documents are put into.
const RemarkableFolder = "reMarkable"

var (
	// ErrNotPaired is returned when the user has no paired reMarkable account.
	ErrNotPaired = errors.New("no reMarkable account is paired")
	// ErrInvalidPairingCode is returned when the reMarkable cloud refuses a one-time code.
	ErrInvalidPairingCode = remarkable.ErrInvalidCode
	// ErrPairingRevoked is returned when the reMarkable cloud no longer accepts the pairing.
	ErrPairingRevoked = remarkable.ErrUnauthorized
	// ErrCloudUnavailable is returned when the reMarkable cloud can't be reached or fails.
	ErrCloudUnavailable = errors.New("the reMarkable cloud could not be reached")
)

// itemReaders is how many documents' indexes are read at the same time on a pull.
const itemReaders = 8

// RemarkableService reads documents from users' reMarkable clouds. Users pair their account
// with a one-time code; pulls then import all documents of the account (except those in
// the trash) as notes in the knowpod folder "reMarkable", inside folders mirroring the
// cloud's folders, and import a document again when it changed. Nothing is ever written to the cloud, and notes stay when documents are
// deleted there.
type RemarkableService struct {
	links   ports.TabletLinkRepository
	recs    ports.RecordingRepository
	folders ports.FolderRepository
	objects ports.ObjectStore
	cloud   *remarkable.Client
	spool   *Spool
	maxSize int64
	log     *slog.Logger
	clock   func() time.Time
	// OnQueued is called when documents wait to be fetched, e.g. to wake the worker.
	OnQueued func()

	mu    sync.Mutex
	locks map[string]*sync.Mutex // per user: pairing and pulls don't overlap
}

// NewRemarkableService builds the service.
func NewRemarkableService(links ports.TabletLinkRepository, recs ports.RecordingRepository, folders ports.FolderRepository, objects ports.ObjectStore, cloud *remarkable.Client, spool *Spool, maxSize int64, log *slog.Logger) *RemarkableService {
	return &RemarkableService{links: links, recs: recs, folders: folders, objects: objects, cloud: cloud, spool: spool, maxSize: maxSize,
		log: log, clock: time.Now, locks: map[string]*sync.Mutex{}}
}

// lock serializes the operations on one user's link.
func (s *RemarkableService) lock(userID string) func() {
	s.mu.Lock()
	l, ok := s.locks[userID]
	if !ok {
		l = &sync.Mutex{}
		s.locks[userID] = l
	}
	s.mu.Unlock()
	l.Lock()
	return l.Unlock
}

// --- pairing ---

// RemarkableView is the user's reMarkable link as shown in the UI (no token).
type RemarkableView struct {
	Paired     bool               `json:"paired"`
	PairedAt   *time.Time         `json:"pairedAt,omitempty"`
	Folder     string             `json:"folder"`
	ConnectURL string             `json:"connectUrl"`
	LastPullAt *time.Time         `json:"lastPullAt,omitempty"`
	LastError  string             `json:"lastError,omitempty"`
	LastResult *tablet.PullResult `json:"lastResult,omitempty"`
}

func remarkableView(l *tablet.Link) *RemarkableView {
	v := &RemarkableView{Folder: RemarkableFolder, ConnectURL: remarkable.ConnectURL}
	if l != nil {
		at := l.PairedAt
		v.Paired, v.PairedAt, v.LastPullAt, v.LastError, v.LastResult = true, &at, l.LastPullAt, l.LastError, l.LastResult
	}
	return v
}

func userOnly(acc *Account) error {
	if acc.ID == "" {
		return errors.Join(ErrForbidden, errors.New("the reMarkable link belongs to a user; sign in"))
	}
	return nil
}

// Status returns the account's link.
func (s *RemarkableService) Status(ctx context.Context, acc *Account) (*RemarkableView, error) {
	if err := userOnly(acc); err != nil {
		return nil, err
	}
	l, err := s.links.Get(ctx, acc.ID)
	if errors.Is(err, ErrNotFound) {
		return remarkableView(nil), nil
	}
	if err != nil {
		return nil, err
	}
	return remarkableView(l), nil
}

// Pair links the account to the reMarkable account the one-time code belongs to (from
// remarkable.ConnectURL). Pairing again replaces the link.
func (s *RemarkableService) Pair(ctx context.Context, acc *Account, code string) (*RemarkableView, error) {
	if err := userOnly(acc); err != nil {
		return nil, err
	}
	code = strings.Join(strings.Fields(code), "")
	if len(code) != 8 {
		return nil, invalid("the one-time code has 8 characters")
	}
	defer s.lock(acc.ID)()
	token, err := s.cloud.Pair(ctx, code)
	if err != nil {
		return nil, cloudErr(err)
	}
	l := &tablet.Link{UserID: acc.ID, DeviceToken: token, PairedAt: s.clock().UTC()}
	if err := s.links.Save(ctx, l); err != nil {
		return nil, err
	}
	s.log.Info("remarkable paired", "user", acc.ID)
	return remarkableView(l), nil
}

// Unpair forgets the link. The imported notes stay. (The device can only be removed from the
// reMarkable account on its website.)
func (s *RemarkableService) Unpair(ctx context.Context, acc *Account) error {
	if err := userOnly(acc); err != nil {
		return err
	}
	defer s.lock(acc.ID)()
	if err := s.links.Delete(ctx, acc.ID); err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	return nil
}

// DeleteByOwner removes a user's link (when the user is deleted).
func (s *RemarkableService) DeleteByOwner(ctx context.Context, userID string) error {
	defer s.lock(userID)()
	if err := s.links.Delete(ctx, userID); err != nil && !errors.Is(err, ErrNotFound) {
		return err
	}
	return nil
}

// cloudErr keeps the errors people can act on and reports the rest as the cloud being
// unavailable.
func cloudErr(err error) error {
	switch {
	case errors.Is(err, remarkable.ErrInvalidCode), errors.Is(err, remarkable.ErrUnauthorized),
		errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	}
	return fmt.Errorf("%w: %v", ErrCloudUnavailable, err)
}

// --- pulling ---

// Pull imports the account's new and changed documents now.
func (s *RemarkableService) Pull(ctx context.Context, acc *Account) (*RemarkableView, error) {
	if err := userOnly(acc); err != nil {
		return nil, err
	}
	defer s.lock(acc.ID)()
	l, err := s.links.Get(ctx, acc.ID)
	if errors.Is(err, ErrNotFound) {
		return nil, ErrNotPaired
	}
	if err != nil {
		return nil, err
	}
	if err := s.pull(ctx, l); err != nil {
		return nil, err
	}
	return remarkableView(l), nil
}

// PullAll pulls every paired account; failures are recorded on the links and logged.
func (s *RemarkableService) PullAll(ctx context.Context) {
	ids, err := s.links.UserIDs(ctx)
	if err != nil {
		s.log.Error("listing reMarkable links failed", "err", err)
		return
	}
	for _, id := range ids {
		if ctx.Err() != nil {
			return
		}
		func() {
			defer s.lock(id)()
			l, err := s.links.Get(ctx, id)
			if err != nil {
				return // unpaired meanwhile
			}
			if err := s.pull(ctx, l); err != nil {
				s.log.Warn("reMarkable pull failed", "user", id, "err", err)
			}
		}()
	}
}

// pull reads the account's documents and queues the new and changed ones. The outcome is saved on the link. The caller holds the user's lock.
func (s *RemarkableService) pull(ctx context.Context, l *tablet.Link) error {
	res, err := s.sync(ctx, l)
	now := s.clock().UTC()
	l.LastPullAt = &now
	if err != nil {
		err = cloudErr(err)
		l.LastError = err.Error()
	} else {
		l.LastError, l.LastResult = "", res
	}
	if serr := s.links.Save(ctx, l); serr != nil && err == nil {
		err = serr
	}
	if err == nil && res.Imported+res.Updated > 0 {
		s.log.Info("reMarkable documents queued", "user", l.UserID, "imported", res.Imported, "updated", res.Updated)
		if s.OnQueued != nil {
			s.OnQueued()
		}
	}
	return err
}

func (s *RemarkableService) sync(ctx context.Context, l *tablet.Link) (*tablet.PullResult, error) {
	sess, err := s.cloud.Open(ctx, l.DeviceToken)
	if err != nil {
		return nil, err
	}
	root, err := sess.Root(ctx)
	if err != nil {
		return nil, err
	}
	switch {
	case root.Hash == "":
		l.RootHash, l.Items = "", nil
	case root.Hash != l.RootHash:
		items, err := s.readItems(ctx, sess, root.Hash, l.Items)
		if err != nil {
			return nil, err
		}
		l.RootHash, l.Items = root.Hash, items
	}

	docs, dirs := live(l.Items)
	res := &tablet.PullResult{Documents: len(docs)}
	notes := make([]*recording.Recording, len(docs))
	fresh := false
	for i, d := range docs {
		rec, err := s.recs.GetByClientID(ctx, recording.RemarkableDeviceID(l.UserID), d.ID)
		switch {
		case errors.Is(err, ErrNotFound):
			fresh = true
		case err != nil:
			return nil, err
		default:
			notes[i] = rec
		}
	}
	// The folders are mirrored, and notes moved along, when the cloud changed since the
	// last complete placement or when new notes need a folder.
	var m *mirror
	if len(docs) > 0 && (fresh || l.MirroredHash != l.RootHash) {
		var err error
		if m, err = s.mirror(ctx, l, dirs); err != nil {
			return nil, err
		}
	}
	complete := true
	for i, d := range docs {
		queued, isNew, placed, err := s.queue(ctx, l, d, notes[i], m)
		if err != nil {
			return nil, err
		}
		complete = complete && placed
		switch {
		case queued && isNew:
			res.Imported++
		case queued:
			res.Updated++
		}
	}
	if m != nil && complete {
		l.MirroredHash = l.RootHash
	}
	return res, nil
}

// readItems reads the root index and the metadata of every item that changed since the
// cached list, a few at a time.
func (s *RemarkableService) readItems(ctx context.Context, sess *remarkable.Session, rootHash string, cached []tablet.Item) ([]tablet.Item, error) {
	entries, err := sess.Index(ctx, rootHash, "root.docSchema")
	if err != nil {
		return nil, err
	}
	known := make(map[string]tablet.Item, len(cached))
	for _, it := range cached {
		known[it.ID] = it
	}
	items := make([]tablet.Item, len(entries))
	var todo []int
	for i, e := range entries {
		if it, ok := known[e.ID]; ok && it.Hash == e.Hash {
			items[i] = it
		} else {
			todo = append(todo, i)
		}
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		first   error
		next    = make(chan int)
		workers = min(itemReaders, len(todo))
	)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range next {
				it, err := sess.ReadItem(ctx, entries[i])
				if err != nil {
					mu.Lock()
					if first == nil {
						first = err
						cancel()
					}
					mu.Unlock()
					continue
				}
				m := it.Metadata
				items[i] = tablet.Item{
					ID: it.ID, Hash: it.Hash, ContentHash: it.ContentHash(), Name: m.VisibleName, Parent: m.Parent,
					Folder: m.Type == remarkable.TypeCollection, Deleted: m.Deleted,
					CreatedAt: m.CreatedTime.Time(), ModifiedAt: m.LastModified.Time(),
				}
			}
		}()
	}
	for _, i := range todo {
		if ctx.Err() != nil {
			break
		}
		next <- i
	}
	close(next)
	wg.Wait()
	if first != nil {
		return nil, first
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

// live returns the account's documents and folders, leaving out deleted ones and those in
// the trash (or in a folder in the trash).
func live(items []tablet.Item) (docs, dirs []tablet.Item) {
	byID := make(map[string]*tablet.Item, len(items))
	for i := range items {
		byID[items[i].ID] = &items[i]
	}
	trashed := func(it *tablet.Item) bool {
		for depth := 0; depth < 64 && it != nil; depth++ {
			if it.Deleted || it.Parent == remarkable.TrashParent {
				return true
			}
			it = byID[it.Parent]
		}
		return false
	}
	for i := range items {
		switch it := &items[i]; {
		case trashed(it):
		case it.Folder:
			dirs = append(dirs, *it)
		default:
			docs = append(docs, *it)
		}
	}
	return docs, dirs
}

// folder returns the ID of the user's knowpod folder for imported documents, creating it at
// the top level when it's missing. The folder is remembered on the link, so it can be renamed
// or moved; when it's deleted, a new one is made for the next new document.
func (s *RemarkableService) folder(ctx context.Context, l *tablet.Link) (string, error) {
	if l.FolderID != "" {
		f, err := s.folders.Get(ctx, l.FolderID)
		if err == nil && f.OwnerID == l.UserID {
			return f.ID, nil
		}
		if err != nil && !errors.Is(err, ErrNotFound) {
			return "", err
		}
	}
	all, err := s.folders.List(ctx, l.UserID)
	if err != nil {
		return "", err
	}
	for _, f := range all {
		if f.ParentID == "" && strings.EqualFold(f.Name, RemarkableFolder) {
			l.FolderID = f.ID
			return f.ID, nil
		}
	}
	now := s.clock().UTC()
	f := &folder.Folder{ID: newID(), OwnerID: l.UserID, Name: RemarkableFolder, CreatedAt: now, UpdatedAt: now}
	if err := s.folders.Create(ctx, f); err != nil {
		return "", err
	}
	l.FolderID = f.ID
	return f.ID, nil
}

// mirror is where the cloud's folders are in knowpod after a pull.
type mirror struct {
	root string
	// placed maps a cloud folder to the knowpod folder its documents go into.
	placed map[string]string
	// managed holds the knowpod folders the import put notes into: the reMarkable folder and
	// the folders mirroring the cloud's, now or before. Notes found elsewhere were moved there
	// by the user and stay.
	managed map[string]bool
}

// folderOf returns the knowpod folder for documents in the cloud folder parent.
func (m *mirror) folderOf(parent string) string {
	if id, ok := m.placed[parent]; ok {
		return id
	}
	return m.root
}

// mirror makes the folder tree inside the reMarkable folder match the cloud's folders (not
// those in the trash): each cloud folder has a knowpod folder of its name in the knowpod
// folder of its parent. Folders are created, renamed and moved as the cloud's are; knowpod
// folders of folders deleted in the cloud stay. Folders nested deeper than knowpod allows
// share the deepest possible folder.
func (s *RemarkableService) mirror(ctx context.Context, l *tablet.Link, dirs []tablet.Item) (*mirror, error) {
	rootID, err := s.folder(ctx, l)
	if err != nil {
		return nil, err
	}
	all, err := s.folders.List(ctx, l.UserID)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]*folder.Folder, len(all))
	for _, f := range all {
		byID[f.ID] = f
	}
	// The reMarkable folder and the folders it is in can't mirror a cloud folder.
	above := map[string]bool{}
	rootDepth := 0
	for p := rootID; p != "" && rootDepth < 64; rootDepth++ {
		above[p] = true
		f := byID[p]
		if f == nil {
			break
		}
		p = f.ParentID
	}

	cloud := make(map[string]*tablet.Item, len(dirs))
	for i := range dirs {
		cloud[dirs[i].ID] = &dirs[i]
	}
	m := &mirror{root: rootID, placed: map[string]string{}, managed: map[string]bool{rootID: true}}
	mappedTo := map[string]string{} // knowpod folder → cloud folder, from the last pull
	for dir, id := range l.Folders {
		if !above[id] {
			m.managed[id] = true
			mappedTo[id] = dir
		}
	}
	folders := map[string]string{}
	claimed := map[string]bool{}
	levels := map[string]int{"": rootDepth}
	visiting := map[string]bool{}
	now := s.clock().UTC()

	// ensure returns the knowpod folder mirroring the cloud folder it inside parent: the one
	// used last time, else one of the same name that isn't mirroring another folder, else a
	// new one.
	ensure := func(it *tablet.Item, parent string) (string, error) {
		free := func(f *folder.Folder) bool {
			if f == nil || above[f.ID] || claimed[f.ID] {
				return false
			}
			owner, ok := mappedTo[f.ID]
			return !ok || owner == it.ID || cloud[owner] == nil
		}
		base := folderName(it.Name)
		var f *folder.Folder
		if id, ok := l.Folders[it.ID]; ok && free(byID[id]) {
			f = byID[id]
		}
		for _, c := range all {
			if f == nil && c.ParentID == parent && strings.EqualFold(c.Name, base) && free(c) {
				f = c
			}
		}
		name := base
		for n := 2; ; n++ {
			taken := false
			for _, c := range all {
				if c != f && c.ParentID == parent && strings.EqualFold(c.Name, name) {
					taken = true
					break
				}
			}
			if !taken {
				break
			}
			name = fmt.Sprintf("%s (%d)", base, n)
		}
		switch {
		case f == nil:
			f = &folder.Folder{ID: newID(), OwnerID: l.UserID, Name: name, ParentID: parent, CreatedAt: now, UpdatedAt: now}
			if err := s.folders.Create(ctx, f); err != nil {
				return "", err
			}
			all = append(all, f)
			byID[f.ID] = f
		case f.Name != name || f.ParentID != parent:
			f.Name, f.ParentID, f.UpdatedAt = name, parent, now
			if err := s.folders.Update(ctx, f); err != nil {
				return "", err
			}
		}
		claimed[f.ID] = true
		return f.ID, nil
	}

	// place returns the knowpod folder for documents in the cloud folder id, and its level.
	var place func(id string) (string, int, error)
	place = func(id string) (string, int, error) {
		if kp, ok := m.placed[id]; ok {
			return kp, levels[id], nil
		}
		it := cloud[id]
		if it == nil || visiting[id] { // top level, unknown, or in a cycle
			return rootID, rootDepth, nil
		}
		visiting[id] = true
		parent, level, err := place(it.Parent)
		if err != nil {
			return "", 0, err
		}
		kp := parent
		if level < maxFolderDepth {
			if kp, err = ensure(it, parent); err != nil {
				return "", 0, err
			}
			folders[id] = kp
			m.managed[kp] = true
			level++
		}
		m.placed[id], levels[id] = kp, level
		return kp, level, nil
	}
	for i := range dirs {
		if _, _, err := place(dirs[i].ID); err != nil {
			return nil, err
		}
	}
	l.Folders = folders
	return m, nil
}

// folderName makes a cloud folder's name a valid knowpod folder name, leaving room for a
// number that tells folders of the same name apart.
func folderName(name string) string {
	name = strings.Join(strings.Fields(name), " ")
	if r := []rune(name); len(r) > 72 {
		name = strings.TrimSpace(string(r[:72]))
	}
	if name == "" {
		return "Untitled"
	}
	return name
}

// queue creates the note of a new document (rec is nil) in the knowpod folder of its cloud
// folder, or updates an existing note: it's queued again when the document's content
// changed, renamed with the document, and moved along with it when the folders are being
// placed (m isn't nil) and the note is in one of the import's folders. A note that is being
// processed is left alone; the next pull looks at it again. placed is false when the note
// still has to be moved.
func (s *RemarkableService) queue(ctx context.Context, l *tablet.Link, d tablet.Item, rec *recording.Recording, m *mirror) (queued, isNew, placed bool, err error) {
	now := s.clock().UTC()
	owner := l.UserID
	if rec == nil {
		rec = &recording.Recording{
			ID: newID(), OwnerID: owner, DeviceID: recording.RemarkableDeviceID(owner), ClientID: d.ID,
			Type: recording.TypeDocument, Source: recording.SourceRemarkable, Title: d.Name,
			Status: recording.StatusRemote, SourceRevision: d.ContentHash, RecordedAt: d.CreatedAt,
			FolderID: m.folderOf(d.Parent), NotBefore: now, CreatedAt: now, UpdatedAt: now,
		}
		if rec.RecordedAt == nil {
			rec.RecordedAt = d.ModifiedAt
		}
		if err := s.recs.Create(ctx, rec); err != nil {
			if errors.Is(err, errDuplicate) {
				return false, false, true, nil
			}
			return false, false, false, err
		}
		return true, true, true, nil
	}
	folderID := rec.FolderID
	if m != nil && m.managed[rec.FolderID] {
		folderID = m.folderOf(d.Parent)
	}
	if rec.SourceRevision == d.ContentHash && rec.Title == d.Name && rec.FolderID == folderID {
		return false, false, true, nil
	}
	if rec.Status != recording.StatusSummarized && rec.Status != recording.StatusFailed {
		return false, false, rec.FolderID == folderID, nil
	}
	rec.Title, rec.FolderID = d.Name, folderID
	requeue := rec.SourceRevision != d.ContentHash
	if requeue {
		rec.Status, rec.Attempts, rec.LastError, rec.NotBefore = recording.StatusRemote, 0, "", now
		rec.SourceRevision = d.ContentHash
	}
	rec.UpdatedAt = now
	if err := s.recs.Update(ctx, rec); err != nil {
		return false, false, false, err
	}
	return requeue, false, true, nil
}

// --- processing stages ---

// Fetch is the processing stage remote → received for documents: it downloads the
// document's current files from the owner's cloud into the spool, as a zip.
func (s *RemarkableService) Fetch(ctx context.Context, rec *recording.Recording) error {
	l, err := s.links.Get(ctx, rec.OwnerID)
	if errors.Is(err, ErrNotFound) {
		return errors.New("the owner's reMarkable account is no longer paired")
	}
	if err != nil {
		return err
	}
	sess, err := s.cloud.Open(ctx, l.DeviceToken)
	if err != nil {
		return err
	}
	root, err := sess.Root(ctx)
	if err != nil {
		return err
	}
	var entry *remarkable.Entry
	if root.Hash != "" {
		entries, err := sess.Index(ctx, root.Hash, "root.docSchema")
		if err != nil {
			return err
		}
		for i := range entries {
			if entries[i].ID == rec.ClientID {
				entry = &entries[i]
			}
		}
	}
	if entry == nil {
		return errors.New("the document is no longer in the reMarkable cloud")
	}
	it, err := sess.ReadItem(ctx, *entry)
	if err != nil {
		return err
	}
	path := s.spool.DownloadPath(rec.ID)
	n, err := sess.Download(ctx, it, path, s.maxSize)
	if err != nil {
		return err
	}
	now := s.clock().UTC()
	rec.Size, rec.SourceRevision, rec.SourceContentType, rec.ReceivedAt = n, it.ContentHash(), "application/zip", &now
	s.log.Info("reMarkable document fetched", "id", rec.ID, "document", rec.ClientID, "bytes", n)
	return nil
}

// Store is the processing stage received → stored for documents: it stores the document's
// PDF (rendered from the pages of a notebook) or EPUB, and its files for reading the text.
func (s *RemarkableService) Store(ctx context.Context, rec *recording.Recording) error {
	path := s.spool.DownloadPath(rec.ID)
	a, err := remarkable.OpenArchive(path)
	if err != nil {
		return err
	}
	defer a.Close()

	var file *recording.Object
	switch a.Kind() {
	case remarkable.KindNotebook:
		pages, err := a.Pages()
		if err != nil {
			return err
		}
		var buf bytes.Buffer
		if err := remarkable.WritePDF(&buf, pages, rec.Title); err != nil {
			return err
		}
		if file, err = s.put(ctx, ObjectKey(rec, "pdf"), &buf, int64(buf.Len()), "application/pdf"); err != nil {
			return err
		}
		rec.Pages = max(len(pages), 1)
	default:
		body, size, err := a.Original()
		if err != nil {
			return err
		}
		ctype := "application/pdf"
		if a.Kind() == remarkable.KindEPUB {
			ctype = "application/epub+zip"
		}
		file, err = s.put(ctx, ObjectKey(rec, string(a.Kind())), body, size, ctype)
		body.Close()
		if err != nil {
			return err
		}
		rec.Pages = len(a.Content.PageIDs())
		if rec.Pages == 0 {
			rec.Pages = a.Content.PageCount
		}
	}

	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	original, err := s.put(ctx, ObjectKey(rec, "rmdoc"), f, st.Size(), "application/zip")
	if err != nil {
		return err
	}
	now := s.clock().UTC()
	rec.File, rec.Original, rec.StoredAt = file, original, &now
	s.log.Info("reMarkable document stored", "id", rec.ID, "kind", string(a.Kind()), "pages", rec.Pages, "bytes", file.Size)
	return nil
}

func (s *RemarkableService) put(ctx context.Context, key string, body io.Reader, size int64, contentType string) (*recording.Object, error) {
	if err := s.objects.Put(ctx, key, body, size, contentType); err != nil {
		return nil, fmt.Errorf("store %s: %w", key, err)
	}
	return &recording.Object{Key: key, ContentType: contentType, Size: size}, nil
}
