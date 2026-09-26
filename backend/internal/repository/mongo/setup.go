package mongo

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

// collections lists every collection the service owns. Setup creates any that are missing.
var collections = []string{}

// indexes lists the indexes per collection. Setup creates them; CreateMany on an existing
// identical index is a no-op.
var indexes = map[string][]mongo.IndexModel{}

// Setup creates collections and indexes idempotently. It is safe to run on every start.
func Setup(ctx context.Context, db *mongo.Database) error {
	if err := ensureCollections(ctx, db); err != nil {
		return err
	}
	return ensureIndexes(ctx, db)
}

func ensureCollections(ctx context.Context, db *mongo.Database) error {
	existing, err := db.ListCollectionNames(ctx, bson.M{})
	if err != nil {
		return err
	}
	have := map[string]bool{}
	for _, n := range existing {
		have[n] = true
	}
	for _, name := range collections {
		if !have[name] {
			if err := db.CreateCollection(ctx, name); err != nil {
				return err
			}
		}
	}
	return nil
}

func ensureIndexes(ctx context.Context, db *mongo.Database) error {
	for coll, models := range indexes {
		if len(models) == 0 {
			continue
		}
		if _, err := db.Collection(coll).Indexes().CreateMany(ctx, models); err != nil {
			return err
		}
	}
	return nil
}
