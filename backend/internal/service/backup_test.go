package service

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/repository/memory"
	memstore "github.com/michaelkleinhenz/knowpod-service/backend/internal/storage/memory"
)

// bsonDoc is a minimal valid BSON document: {} with a marker in its length-padding-free form
// is too small to tell apart, so this makes {"v": "<s>"}.
func bsonDoc(s string) []byte {
	body := []byte{0x02, 'v', 0, 0, 0, 0, 0}
	binary.LittleEndian.PutUint32(body[3:], uint32(len(s)+1))
	body = append(body, s...)
	body = append(body, 0, 0)
	out := make([]byte, 4, 4+len(body))
	binary.LittleEndian.PutUint32(out, uint32(4+len(body)))
	return append(out, body...)
}

func newBackupService(t *testing.T) (*BackupService, *memory.Backup, *memstore.Store) {
	t.Helper()
	db, objects := memory.NewBackup("users", "recordings"), memstore.New()
	s := NewBackupService(db, objects)
	s.TempDir = t.TempDir()
	return s, db, objects
}

func TestBackupAndRestore(t *testing.T) {
	ctx := context.Background()
	s, db, objects := newBackupService(t)
	db.Docs["users"] = [][]byte{bsonDoc("ann"), bsonDoc("bob")}
	db.Docs["recordings"] = [][]byte{bsonDoc("note")}
	_ = objects.Put(ctx, "recordings/a/1.flac", strings.NewReader("audio"), 5, "audio/flac")
	_ = objects.Put(ctx, "notes/n1/images/i.png", strings.NewReader("png"), 3, "image/png")

	var buf bytes.Buffer
	sum, err := s.Backup(ctx, &buf)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Collections["users"] != 2 || sum.Collections["recordings"] != 1 || sum.Objects != 2 || sum.ObjectBytes != 8 {
		t.Fatalf("summary: %+v", sum)
	}

	// Everything changes after the backup.
	db.Docs["users"] = [][]byte{bsonDoc("mallory")}
	db.Docs["recordings"] = nil
	_ = objects.Delete(ctx, "recordings/a/1.flac")
	_ = objects.Put(ctx, "orphan", strings.NewReader("x"), 1, "text/plain")

	got, err := s.Restore(ctx, bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	if got.Objects != 2 || got.Collections["users"] != 2 {
		t.Fatalf("restore summary: %+v", got)
	}
	if len(db.Docs["users"]) != 2 || !bytes.Equal(db.Docs["users"][0], bsonDoc("ann")) || len(db.Docs["recordings"]) != 1 {
		t.Fatalf("documents after restore: %v", db.Docs)
	}
	o, ok := objects.Object("recordings/a/1.flac")
	if !ok || string(o.Data) != "audio" || o.ContentType != "audio/flac" {
		t.Fatalf("object after restore: %+v %v", o, ok)
	}
	if _, ok := objects.Object("orphan"); !ok {
		t.Error("objects outside the backup are left alone")
	}
}

func TestRestoreRejectsBadBackupsWithoutChangingAnything(t *testing.T) {
	ctx := context.Background()
	s, db, objects := newBackupService(t)
	db.Docs["users"] = [][]byte{bsonDoc("ann")}
	_ = objects.Put(ctx, "k", strings.NewReader("data"), 4, "text/plain")
	var good bytes.Buffer
	if _, err := s.Backup(ctx, &good); err != nil {
		t.Fatal(err)
	}
	db.Docs["users"] = [][]byte{bsonDoc("current")}

	zipWith := func(files map[string][]byte) []byte {
		var b bytes.Buffer
		zw := zip.NewWriter(&b)
		for name, data := range files {
			w, _ := zw.Create(name)
			_, _ = w.Write(data)
		}
		_ = zw.Close()
		return b.Bytes()
	}
	manifest := func(s string) []byte { return []byte(s) }
	cases := map[string][]byte{
		"not a zip":         []byte("hello"),
		"cut off":           good.Bytes()[:good.Len()/2],
		"no manifest":       zipWith(map[string][]byte{"db/users.bson": bsonDoc("x")}),
		"wrong format":      zipWith(map[string][]byte{"manifest.json": manifest(`{"format":"other","version":1}`)}),
		"future version":    zipWith(map[string][]byte{"manifest.json": manifest(`{"format":"knowpod-backup","version":99}`)}),
		"unknown coll":      zipWith(map[string][]byte{"manifest.json": manifest(`{"format":"knowpod-backup","version":1,"collections":{"nope":1}}`)}),
		"missing coll data": zipWith(map[string][]byte{"manifest.json": manifest(`{"format":"knowpod-backup","version":1,"collections":{"users":1}}`)}),
		"wrong doc count":   zipWith(map[string][]byte{"manifest.json": manifest(`{"format":"knowpod-backup","version":1,"collections":{"users":2}}`), "db/users.bson": bsonDoc("x")}),
		"damaged bson":      zipWith(map[string][]byte{"manifest.json": manifest(`{"format":"knowpod-backup","version":1,"collections":{"users":1}}`), "db/users.bson": {1, 0, 0, 0, 9}}),
		"missing object":    zipWith(map[string][]byte{"manifest.json": manifest(`{"format":"knowpod-backup","version":1,"objects":[{"key":"k","size":4}]}`)}),
		"object wrong size": zipWith(map[string][]byte{"manifest.json": manifest(`{"format":"knowpod-backup","version":1,"objects":[{"key":"k","size":4}]}`), "objects/k": []byte("abc")}),
	}
	for name, data := range cases {
		if _, err := s.Restore(ctx, bytes.NewReader(data)); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%s: err = %v, want ErrInvalidInput", name, err)
		}
	}
	if len(db.Docs["users"]) != 1 || !bytes.Equal(db.Docs["users"][0], bsonDoc("current")) {
		t.Fatalf("a rejected restore changed the data: %v", db.Docs)
	}
}

func TestBackupAndRestoreExclude(t *testing.T) {
	ctx := context.Background()
	s, _, _ := newBackupService(t)
	// A restore can't start while one runs, nor a backup.
	s.mu.Lock()
	if _, err := s.Backup(ctx, io.Discard); !errors.Is(err, ErrBackupBusy) {
		t.Errorf("backup during restore: %v", err)
	}
	if _, err := s.Restore(ctx, strings.NewReader("")); !errors.Is(err, ErrBackupBusy) {
		t.Errorf("restore during restore: %v", err)
	}
	s.mu.Unlock()
	s.mu.RLock()
	if _, err := s.Restore(ctx, strings.NewReader("")); !errors.Is(err, ErrBackupBusy) {
		t.Errorf("restore during backup: %v", err)
	}
	s.mu.RUnlock()
}
