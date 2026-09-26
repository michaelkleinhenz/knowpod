package mongo

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/theme"
)

// ThemeRepo is the MongoDB implementation of ports.ThemeRepository.
type ThemeRepo struct{ c *mongo.Collection }

// NewThemeRepo builds the repository.
func NewThemeRepo(s *Store) *ThemeRepo { return &ThemeRepo{c: s.DB().Collection(CollThemes)} }

func (r *ThemeRepo) Create(ctx context.Context, t *theme.Theme) error {
	_, err := r.c.InsertOne(ctx, t)
	return mapErr(err)
}

func (r *ThemeRepo) Get(ctx context.Context, id string) (*theme.Theme, error) {
	var t theme.Theme
	if err := r.c.FindOne(ctx, bson.M{"_id": id}).Decode(&t); err != nil {
		return nil, mapErr(err)
	}
	return &t, nil
}

func (r *ThemeRepo) List(ctx context.Context, ownerID string) ([]*theme.Theme, error) {
	cur, err := r.c.Find(ctx, bson.M{"ownerId": ownerID}, options.Find().SetSort(bson.D{{Key: "name", Value: 1}}))
	if err != nil {
		return nil, err
	}
	out := []*theme.Theme{}
	return out, cur.All(ctx, &out)
}

func (r *ThemeRepo) Update(ctx context.Context, t *theme.Theme) error {
	res, err := r.c.ReplaceOne(ctx, bson.M{"_id": t.ID}, t)
	if err != nil {
		return mapErr(err)
	}
	if res.MatchedCount == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *ThemeRepo) Delete(ctx context.Context, id string) error {
	res, err := r.c.DeleteOne(ctx, bson.M{"_id": id})
	if err != nil {
		return err
	}
	if res.DeletedCount == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *ThemeRepo) DeleteByOwner(ctx context.Context, ownerID string) error {
	_, err := r.c.DeleteMany(ctx, bson.M{"ownerId": ownerID})
	return err
}
