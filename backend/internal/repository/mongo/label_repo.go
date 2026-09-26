package mongo

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/label"
)

// LabelRepo is the MongoDB implementation of ports.LabelRepository.
type LabelRepo struct{ c *mongo.Collection }

// NewLabelRepo builds the repository.
func NewLabelRepo(s *Store) *LabelRepo { return &LabelRepo{c: s.DB().Collection(CollLabels)} }

func (r *LabelRepo) Create(ctx context.Context, l *label.Label) error {
	_, err := r.c.InsertOne(ctx, l)
	return mapErr(err)
}

func (r *LabelRepo) Get(ctx context.Context, id string) (*label.Label, error) {
	var l label.Label
	if err := r.c.FindOne(ctx, bson.M{"_id": id}).Decode(&l); err != nil {
		return nil, mapErr(err)
	}
	return &l, nil
}

func (r *LabelRepo) List(ctx context.Context, ownerID string) ([]*label.Label, error) {
	cur, err := r.c.Find(ctx, bson.M{"ownerId": ownerID}, options.Find().SetSort(bson.D{{Key: "name", Value: 1}}))
	if err != nil {
		return nil, err
	}
	out := []*label.Label{}
	return out, cur.All(ctx, &out)
}

func (r *LabelRepo) Update(ctx context.Context, l *label.Label) error {
	res, err := r.c.ReplaceOne(ctx, bson.M{"_id": l.ID}, l)
	if err != nil {
		return mapErr(err)
	}
	if res.MatchedCount == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *LabelRepo) Delete(ctx context.Context, id string) error {
	res, err := r.c.DeleteOne(ctx, bson.M{"_id": id})
	if err != nil {
		return err
	}
	if res.DeletedCount == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *LabelRepo) DeleteByOwner(ctx context.Context, ownerID string) error {
	_, err := r.c.DeleteMany(ctx, bson.M{"ownerId": ownerID})
	return err
}
