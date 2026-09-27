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

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/tablet"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/ports"
	"github.com/michaelkleinhenz/knowpod-service/backend/internal/remarkable"
)

// RemarkableFolder is the top-level folder in the reMarkable cloud whose documents are read.
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
// with a one-time code; pulls then import the documents in the top-level "reMarkable"
// folder as notes, and import a document again when it changed. Nothing is ever written to
// the cloud, and notes stay when documents are deleted or moved there.
type RemarkableService struct {
	links   ports.TabletLinkRepository
	recs    ports.RecordingRepository
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
func NewRemarkableService(links ports.TabletLinkRepository, recs ports.RecordingRepository, objects ports.ObjectStore, cloud *remarkable.Client, spool *Spool, maxSize int64, log *slog.Logger) *RemarkableService {
	return &RemarkableService{links: links, recs: recs, objects: objects, cloud: cloud, spool: spool, maxSize: maxSize,
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

// pull reads the account's documents and queues the new and changed ones in the folder. The
// outcome is saved on the link. The caller holds the user's lock.
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

	docs, found := folderDocuments(l.Items)
	res := &tablet.PullResult{FolderFound: found, Documents: len(docs)}
	for _, d := range docs {
		queued, isNew, err := s.queue(ctx, l.UserID, d)
		if err != nil {
			return nil, err
		}
		switch {
		case queued && isNew:
			res.Imported++
		case queued:
			res.Updated++
		}
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

// folderDocuments returns the documents in the top-level reMarkable folder and its
// subfolders. found is false when there is no such folder.
func folderDocuments(items []tablet.Item) (docs []tablet.Item, found bool) {
	byID := make(map[string]*tablet.Item, len(items))
	roots := map[string]bool{}
	for i := range items {
		it := &items[i]
		byID[it.ID] = it
		if it.Folder && !it.Deleted && it.Parent == "" && it.Name == RemarkableFolder {
			roots[it.ID] = true
		}
	}
	if len(roots) == 0 {
		// Accept another spelling if that is the only one.
		for i := range items {
			it := &items[i]
			if it.Folder && !it.Deleted && it.Parent == "" && strings.EqualFold(strings.TrimSpace(it.Name), RemarkableFolder) {
				roots[it.ID] = true
			}
		}
	}
	inFolder := func(it *tablet.Item) bool {
		for depth := 0; depth < 64 && it != nil && !it.Deleted; depth++ {
			if roots[it.Parent] {
				return true
			}
			it = byID[it.Parent]
		}
		return false
	}
	for i := range items {
		if it := &items[i]; !it.Folder && !it.Deleted && inFolder(it) {
			docs = append(docs, *it)
		}
	}
	return docs, len(roots) > 0
}

// queue creates the note of a new document, or queues an existing note again when the
// document's content changed. A note that is being processed is left alone; the next pull
// looks at it again.
func (s *RemarkableService) queue(ctx context.Context, owner string, d tablet.Item) (queued, isNew bool, err error) {
	now := s.clock().UTC()
	rec, err := s.recs.GetByClientID(ctx, recording.RemarkableDeviceID(owner), d.ID)
	if errors.Is(err, ErrNotFound) {
		rec = &recording.Recording{
			ID: newID(), OwnerID: owner, DeviceID: recording.RemarkableDeviceID(owner), ClientID: d.ID,
			Type: recording.TypeDocument, Source: recording.SourceRemarkable, Title: d.Name,
			Status: recording.StatusRemote, SourceRevision: d.ContentHash, RecordedAt: d.CreatedAt,
			NotBefore: now, CreatedAt: now, UpdatedAt: now,
		}
		if rec.RecordedAt == nil {
			rec.RecordedAt = d.ModifiedAt
		}
		if err := s.recs.Create(ctx, rec); err != nil {
			if errors.Is(err, errDuplicate) {
				return false, false, nil
			}
			return false, false, err
		}
		return true, true, nil
	}
	if err != nil {
		return false, false, err
	}
	if rec.SourceRevision == d.ContentHash && rec.Title == d.Name {
		return false, false, nil
	}
	if rec.Status != recording.StatusSummarized && rec.Status != recording.StatusFailed {
		return false, false, nil
	}
	rec.Title = d.Name
	requeue := rec.SourceRevision != d.ContentHash
	if requeue {
		rec.Status, rec.Attempts, rec.LastError, rec.NotBefore = recording.StatusRemote, 0, "", now
		rec.SourceRevision = d.ContentHash
	}
	rec.UpdatedAt = now
	if err := s.recs.Update(ctx, rec); err != nil {
		return false, false, err
	}
	return requeue, false, nil
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
