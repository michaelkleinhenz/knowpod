package mongo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"slices"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// replaceBatchDocs and replaceBatchBytes bound one insert when a collection is restored.
const (
	replaceBatchDocs  = 500
	replaceBatchBytes = 8 << 20
)

// BackupRepo exports and replaces the collections a backup holds. Sessions are left out:
// they are short-lived, and keeping them keeps the administrator who restores signed in.
type BackupRepo struct{ db *mongo.Database }

// NewBackupRepo builds the repository.
func NewBackupRepo(s *Store) *BackupRepo { return &BackupRepo{db: s.DB()} }

// Collections lists the collections a backup holds.
func (r *BackupRepo) Collections() []string {
	return slices.DeleteFunc(slices.Clone(collections), func(c string) bool { return c == CollSessions })
}

func (r *BackupRepo) known(collection string) error {
	if !slices.Contains(r.Collections(), collection) {
		return fmt.Errorf("mongo: %q is not a backed up collection", collection)
	}
	return nil
}

// Export calls fn with the raw BSON of every document of the collection.
func (r *BackupRepo) Export(ctx context.Context, collection string, fn func(doc []byte) error) error {
	if err := r.known(collection); err != nil {
		return err
	}
	cur, err := r.db.Collection(collection).Find(ctx, bson.M{}, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}))
	if err != nil {
		return err
	}
	defer cur.Close(ctx)
	for cur.Next(ctx) {
		if err := fn(cur.Current); err != nil {
			return err
		}
	}
	return cur.Err()
}

// Replace deletes every document of the collection and inserts the ones next returns until
// it returns io.EOF. It is not atomic: a failure leaves the collection partly restored.
func (r *BackupRepo) Replace(ctx context.Context, collection string, next func() ([]byte, error)) error {
	if err := r.known(collection); err != nil {
		return err
	}
	coll := r.db.Collection(collection)
	if _, err := coll.DeleteMany(ctx, bson.M{}); err != nil {
		return err
	}
	var batch []any
	size := 0
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		_, err := coll.InsertMany(ctx, batch)
		batch, size = nil, 0
		return err
	}
	for {
		doc, err := next()
		if errors.Is(err, io.EOF) {
			return flush()
		}
		if err != nil {
			return err
		}
		batch = append(batch, bson.Raw(doc))
		size += len(doc)
		if len(batch) >= replaceBatchDocs || size >= replaceBatchBytes {
			if err := flush(); err != nil {
				return err
			}
		}
	}
}
