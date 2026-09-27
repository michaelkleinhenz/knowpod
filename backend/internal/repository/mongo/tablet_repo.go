package mongo

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/tablet"
)

// TabletLinkRepo is the MongoDB implementation of ports.TabletLinkRepository. The user ID is
// the document ID.
type TabletLinkRepo struct{ c *mongo.Collection }

// NewTabletLinkRepo builds the repository.
func NewTabletLinkRepo(s *Store) *TabletLinkRepo {
	return &TabletLinkRepo{c: s.DB().Collection(CollTablets)}
}

func (r *TabletLinkRepo) Get(ctx context.Context, userID string) (*tablet.Link, error) {
	var l tablet.Link
	if err := r.c.FindOne(ctx, bson.M{"_id": userID}).Decode(&l); err != nil {
		return nil, mapErr(err)
	}
	return &l, nil
}

func (r *TabletLinkRepo) Save(ctx context.Context, l *tablet.Link) error {
	_, err := r.c.ReplaceOne(ctx, bson.M{"_id": l.UserID}, l, options.Replace().SetUpsert(true))
	return mapErr(err)
}

func (r *TabletLinkRepo) Delete(ctx context.Context, userID string) error {
	res, err := r.c.DeleteOne(ctx, bson.M{"_id": userID})
	if err != nil {
		return err
	}
	if res.DeletedCount == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *TabletLinkRepo) UserIDs(ctx context.Context) ([]string, error) {
	cur, err := r.c.Find(ctx, bson.M{}, options.Find().SetProjection(bson.M{"_id": 1}))
	if err != nil {
		return nil, err
	}
	var docs []struct {
		ID string `bson:"_id"`
	}
	if err := cur.All(ctx, &docs); err != nil {
		return nil, err
	}
	ids := make([]string, len(docs))
	for i, d := range docs {
		ids[i] = d.ID
	}
	return ids, nil
}
