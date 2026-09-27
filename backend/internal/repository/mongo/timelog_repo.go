package mongo

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/timelog"
)

// TimeEntryRepo is the MongoDB implementation of ports.TimeEntryRepository.
type TimeEntryRepo struct{ c *mongo.Collection }

// NewTimeEntryRepo builds the repository.
func NewTimeEntryRepo(s *Store) *TimeEntryRepo {
	return &TimeEntryRepo{c: s.DB().Collection(CollTimeEntries)}
}

func (r *TimeEntryRepo) Create(ctx context.Context, e *timelog.Entry) error {
	_, err := r.c.InsertOne(ctx, e)
	return mapErr(err)
}

func (r *TimeEntryRepo) Get(ctx context.Context, id string) (*timelog.Entry, error) {
	return r.findOne(ctx, bson.M{"_id": id})
}

func (r *TimeEntryRepo) Update(ctx context.Context, e *timelog.Entry) error {
	res, err := r.c.ReplaceOne(ctx, bson.M{"_id": e.ID}, e)
	if err != nil {
		return mapErr(err)
	}
	if res.MatchedCount == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *TimeEntryRepo) Delete(ctx context.Context, id string) error {
	res, err := r.c.DeleteOne(ctx, bson.M{"_id": id})
	if err != nil {
		return err
	}
	if res.DeletedCount == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *TimeEntryRepo) List(ctx context.Context, rg timelog.Range) ([]*timelog.Entry, error) {
	cur, err := r.c.Find(ctx,
		bson.M{"ownerId": rg.OwnerID, "start": bson.M{"$gte": rg.From, "$lt": rg.To}},
		options.Find().SetSort(bson.D{{Key: "start", Value: 1}}))
	if err != nil {
		return nil, err
	}
	out := []*timelog.Entry{}
	return out, cur.All(ctx, &out)
}

func (r *TimeEntryRepo) Running(ctx context.Context, ownerID string) (*timelog.Entry, error) {
	return r.findOne(ctx, bson.M{"ownerId": ownerID, "running": true})
}

func (r *TimeEntryRepo) DeleteByNote(ctx context.Context, noteID string) error {
	_, err := r.c.DeleteMany(ctx, bson.M{"noteId": noteID})
	return err
}

func (r *TimeEntryRepo) DeleteByOwner(ctx context.Context, ownerID string) error {
	_, err := r.c.DeleteMany(ctx, bson.M{"ownerId": ownerID})
	return err
}

func (r *TimeEntryRepo) findOne(ctx context.Context, filter bson.M) (*timelog.Entry, error) {
	var e timelog.Entry
	if err := r.c.FindOne(ctx, filter).Decode(&e); err != nil {
		return nil, mapErr(err)
	}
	return &e, nil
}
