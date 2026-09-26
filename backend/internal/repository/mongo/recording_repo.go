package mongo

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/recording"
)

// RecordingRepo is the MongoDB implementation of ports.RecordingRepository.
type RecordingRepo struct{ c *mongo.Collection }

// NewRecordingRepo builds the repository.
func NewRecordingRepo(s *Store) *RecordingRepo {
	return &RecordingRepo{c: s.DB().Collection(CollRecordings)}
}

func (r *RecordingRepo) Create(ctx context.Context, rec *recording.Recording) error {
	_, err := r.c.InsertOne(ctx, rec)
	return mapErr(err)
}

func (r *RecordingRepo) Get(ctx context.Context, id string) (*recording.Recording, error) {
	return r.findOne(ctx, bson.M{"_id": id})
}

func (r *RecordingRepo) GetByClientID(ctx context.Context, deviceID, clientID string) (*recording.Recording, error) {
	return r.findOne(ctx, bson.M{"deviceId": deviceID, "clientId": clientID})
}

func (r *RecordingRepo) Update(ctx context.Context, rec *recording.Recording) error {
	res, err := r.c.ReplaceOne(ctx, bson.M{"_id": rec.ID}, rec)
	if err != nil {
		return mapErr(err)
	}
	if res.MatchedCount == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *RecordingRepo) Delete(ctx context.Context, id string) error {
	res, err := r.c.DeleteOne(ctx, bson.M{"_id": id})
	if err != nil {
		return err
	}
	if res.DeletedCount == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *RecordingRepo) List(ctx context.Context, f recording.ListFilter) ([]*recording.Recording, error) {
	filter := bson.M{}
	if f.DeviceID != "" {
		filter["deviceId"] = f.DeviceID
	}
	if f.Status != "" {
		filter["status"] = f.Status
	}
	opts := options.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}}).SetSkip(int64(f.Offset))
	if f.Limit > 0 {
		opts.SetLimit(int64(f.Limit))
	}
	return r.find(ctx, filter, opts)
}

func (r *RecordingRepo) Claim(ctx context.Context, status recording.Status, now, leaseUntil time.Time) (*recording.Recording, error) {
	var rec recording.Recording
	err := r.c.FindOneAndUpdate(ctx,
		bson.M{"status": status, "notBefore": bson.M{"$lte": now}},
		bson.M{"$set": bson.M{"notBefore": leaseUntil, "updatedAt": now}, "$inc": bson.M{"attempts": 1}},
		options.FindOneAndUpdate().SetSort(bson.D{{Key: "notBefore", Value: 1}}).SetReturnDocument(options.After),
	).Decode(&rec)
	if err != nil {
		return nil, mapErr(err)
	}
	return &rec, nil
}

func (r *RecordingRepo) ListStale(ctx context.Context, status recording.Status, before time.Time, limit int) ([]*recording.Recording, error) {
	return r.find(ctx, bson.M{"status": status, "updatedAt": bson.M{"$lt": before}}, options.Find().SetLimit(int64(limit)))
}

func (r *RecordingRepo) findOne(ctx context.Context, filter bson.M) (*recording.Recording, error) {
	var rec recording.Recording
	if err := r.c.FindOne(ctx, filter).Decode(&rec); err != nil {
		return nil, mapErr(err)
	}
	return &rec, nil
}

func (r *RecordingRepo) find(ctx context.Context, filter bson.M, opts *options.FindOptions) ([]*recording.Recording, error) {
	cur, err := r.c.Find(ctx, filter, opts)
	if err != nil {
		return nil, err
	}
	out := []*recording.Recording{}
	return out, cur.All(ctx, &out)
}
