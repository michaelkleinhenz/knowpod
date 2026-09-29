package service

import (
	"archive/zip"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/ports"
)

// A backup is a zip file:
//
//	db/<collection>.bson   the documents of a collection, concatenated raw BSON (as mongodump)
//	objects/<key>          the objects of the S3 bucket, under their keys
//	manifest.json          what the backup holds; written last, so a cut-off download is
//	                       not a valid backup
const (
	backupFormat   = "knowpod-backup"
	backupVersion  = 1
	manifestName   = "manifest.json"
	dbPrefix       = "db/"
	objectPrefix   = "objects/"
	maxBSONDocSize = 16<<20 + 16<<10 // MongoDB's document limit, with room for its overhead
)

// ErrBackupBusy is returned when a backup or restore can't start because another restore
// (or, for a restore, any backup) is running.
var ErrBackupBusy = errors.New("a backup or restore is already running")

// BackupManifest describes the contents of a backup.
type BackupManifest struct {
	Format     string    `json:"format"`
	Version    int       `json:"version"`
	CreatedAt  time.Time `json:"createdAt"`
	AppVersion string    `json:"appVersion,omitempty"`
	// Owner is the user a personal backup was made for; empty for a full backup.
	Owner string `json:"owner,omitempty"`
	// Collections holds the number of documents per collection.
	Collections map[string]int `json:"collections"`
	Objects     []BackupObject `json:"objects"`
}

// BackupObject is an object of the bucket in a backup.
type BackupObject struct {
	Key         string `json:"key"`
	Size        int64  `json:"size"`
	ContentType string `json:"contentType,omitempty"`
}

// BackupSummary tells what a backup or restore covered.
type BackupSummary struct {
	CreatedAt   time.Time      `json:"createdAt"`
	Collections map[string]int `json:"collections"`
	Objects     int            `json:"objects"`
	ObjectBytes int64          `json:"objectBytes"`
}

func summarize(m *BackupManifest) *BackupSummary {
	s := &BackupSummary{CreatedAt: m.CreatedAt, Collections: m.Collections, Objects: len(m.Objects)}
	for _, o := range m.Objects {
		s.ObjectBytes += o.Size
	}
	return s
}

// BackupService makes full backups of the database and the object store and restores them.
type BackupService struct {
	db      ports.BackupRepository
	objects ports.ObjectStore
	// TempDir is where an uploaded backup and the objects being restored are held; empty is
	// the system's temporary directory.
	TempDir string
	// Version is the app version recorded in backups.
	Version string
	now     func() time.Time

	// mu lets backups run side by side but a restore run alone.
	mu sync.RWMutex
}

// NewBackupService builds the service. The object store must be able to list its objects.
func NewBackupService(db ports.BackupRepository, objects ports.ObjectStore) *BackupService {
	return &BackupService{db: db, objects: objects, now: time.Now}
}

// Backup writes a full backup to w. The data isn't frozen while it is written: the database
// goes first and the objects after it, so every object the documents refer to is there.
func (s *BackupService) Backup(ctx context.Context, w io.Writer) (*BackupSummary, error) {
	lister, ok := s.objects.(ports.ObjectLister)
	if !ok {
		return nil, errors.New("backup: the object store can't list its objects")
	}
	if !s.mu.TryRLock() {
		return nil, ErrBackupBusy
	}
	defer s.mu.RUnlock()

	zw := zip.NewWriter(w)
	m := &BackupManifest{Format: backupFormat, Version: backupVersion, CreatedAt: s.now().UTC(),
		AppVersion: s.Version, Collections: map[string]int{}, Objects: []BackupObject{}}

	for _, coll := range s.db.Collections() {
		ew, err := zw.CreateHeader(&zip.FileHeader{Name: dbPrefix + coll + ".bson", Method: zip.Deflate, Modified: m.CreatedAt})
		if err != nil {
			return nil, err
		}
		err = s.db.Export(ctx, coll, func(doc []byte) error {
			m.Collections[coll]++
			_, err := ew.Write(doc)
			return err
		})
		if err != nil {
			return nil, fmt.Errorf("backup of %s: %w", coll, err)
		}
	}

	err := lister.List(ctx, func(info ports.ObjectInfo) error {
		// Objects are audio and documents that are compressed already.
		ow, err := zw.CreateHeader(&zip.FileHeader{Name: objectPrefix + info.Key, Method: zip.Store, Modified: m.CreatedAt})
		if err != nil {
			return err
		}
		body, err := s.objects.Get(ctx, info.Key, 0, -1)
		if errors.Is(err, ErrNotFound) {
			return nil // deleted since it was listed
		}
		if err != nil {
			return err
		}
		defer body.Close()
		n, err := io.Copy(ow, body)
		if err != nil {
			return err
		}
		m.Objects = append(m.Objects, BackupObject{Key: info.Key, Size: n, ContentType: info.ContentType})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("backup of the objects: %w", err)
	}

	mw, err := zw.CreateHeader(&zip.FileHeader{Name: manifestName, Method: zip.Deflate, Modified: m.CreatedAt})
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

// Restore replaces everything with the contents of the backup read from src: every
// collection is emptied and filled from the backup, and the backup's objects are written to
// the object store. Objects that aren't in the backup stay; nothing refers to them anymore.
// The backup is checked completely before anything changes. A failure afterwards leaves the
// restore partly done; running it again with the same backup finishes it.
func (s *BackupService) Restore(ctx context.Context, src io.Reader) (*BackupSummary, error) {
	if !s.mu.TryLock() {
		return nil, ErrBackupBusy
	}
	defer s.mu.Unlock()

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
	m, err := readManifest(files[manifestName], backupFormat)
	if err != nil {
		return nil, err
	}
	if err := s.check(m, files); err != nil {
		return nil, err
	}

	for _, obj := range m.Objects {
		if err := s.restoreObject(ctx, files[objectPrefix+obj.Key], obj); err != nil {
			return nil, fmt.Errorf("restoring object %s: %w", obj.Key, err)
		}
	}
	for _, coll := range s.db.Collections() {
		if err := s.restoreCollection(ctx, coll, files[dbPrefix+coll+".bson"]); err != nil {
			return nil, fmt.Errorf("restoring %s: %w", coll, err)
		}
	}
	return summarize(m), nil
}

func readManifest(f *zip.File, format string) (*BackupManifest, error) {
	if f == nil {
		return nil, invalid("not a knowpod backup: %s is missing (is the file complete?)", manifestName)
	}
	rc, err := f.Open()
	if err != nil {
		return nil, invalid("not a knowpod backup (%v)", err)
	}
	defer rc.Close()
	var m BackupManifest
	if err := json.NewDecoder(rc).Decode(&m); err != nil {
		return nil, invalid("the backup's manifest is unreadable (%v)", err)
	}
	if m.Format != format {
		return nil, invalid("not a knowpod backup")
	}
	if m.Version != backupVersion {
		return nil, invalid("unsupported backup version %d", m.Version)
	}
	return &m, nil
}

// check verifies, before anything is changed, that the backup is complete and fits this
// service: known collections only, each document readable, every object present in full.
func (s *BackupService) check(m *BackupManifest, files map[string]*zip.File) error {
	known := s.db.Collections()
	for coll := range m.Collections {
		if !slices.Contains(known, coll) {
			return invalid("the backup holds a collection this version doesn't know: %s", coll)
		}
	}
	for _, coll := range known {
		f := files[dbPrefix+coll+".bson"]
		if f == nil {
			if m.Collections[coll] != 0 {
				return invalid("the backup is missing the data of %s", coll)
			}
			continue
		}
		n := 0
		if err := readDocs(f, func([]byte) error { n++; return nil }); err != nil {
			return invalid("%s is damaged in the backup (%v)", coll, err)
		}
		if n != m.Collections[coll] {
			return invalid("%s holds %d documents in the backup, not the %d it should", coll, n, m.Collections[coll])
		}
	}
	for _, obj := range m.Objects {
		f := files[objectPrefix+obj.Key]
		if f == nil || obj.Key == "" || int64(f.UncompressedSize64) != obj.Size {
			return invalid("the object %q is missing or incomplete in the backup", obj.Key)
		}
	}
	return nil
}

// readDocs calls fn with each BSON document in f. Documents start with their length.
func readDocs(f *zip.File, fn func(doc []byte) error) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close() // reading to the end also checks the file's CRC
	for {
		var head [4]byte
		if _, err := io.ReadFull(rc, head[:]); err == io.EOF {
			return nil
		} else if err != nil {
			return err
		}
		n := int(binary.LittleEndian.Uint32(head[:]))
		if n < 5 || n > maxBSONDocSize {
			return fmt.Errorf("invalid document length %d", n)
		}
		doc := make([]byte, n)
		copy(doc, head[:])
		if _, err := io.ReadFull(rc, doc[4:]); err != nil {
			return err
		}
		if doc[n-1] != 0 {
			return errors.New("document isn't terminated")
		}
		if err := fn(doc); err != nil {
			return err
		}
	}
}

// restoreObject writes one object. The store wants a seekable body, so it goes through a
// temporary file.
func (s *BackupService) restoreObject(ctx context.Context, f *zip.File, obj BackupObject) error {
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

func (s *BackupService) restoreCollection(ctx context.Context, coll string, f *zip.File) error {
	if f == nil {
		return s.db.Replace(ctx, coll, func() ([]byte, error) { return nil, io.EOF })
	}
	// Documents come from a goroutine reading the file, so Replace can pull them one by one.
	docs := make(chan []byte)
	done := make(chan error, 1)
	stop := make(chan struct{})
	go func() {
		err := readDocs(f, func(doc []byte) error {
			select {
			case docs <- doc:
				return nil
			case <-stop:
				return context.Canceled
			}
		})
		close(docs)
		done <- err
	}()
	err := s.db.Replace(ctx, coll, func() ([]byte, error) {
		if doc, ok := <-docs; ok {
			return doc, nil
		}
		return nil, io.EOF
	})
	close(stop)
	if readErr := <-done; err == nil && !errors.Is(readErr, context.Canceled) {
		err = readErr
	}
	return err
}
