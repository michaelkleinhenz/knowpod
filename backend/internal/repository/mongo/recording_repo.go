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
type RecordingRepo struct{ c, counters *mongo.Collection }

// NewRecordingRepo builds the repository.
func NewRecordingRepo(s *Store) *RecordingRepo {
	return &RecordingRepo{c: s.DB().Collection(CollRecordings), counters: s.DB().Collection(CollCounters)}
}

// Create stores a new recording. A recording with an owner gets the owner's next note number.
func (r *RecordingRepo) Create(ctx context.Context, rec *recording.Recording) error {
	if rec.Number == 0 && rec.OwnerID != "" {
		n, err := r.nextNumber(ctx, rec.OwnerID)
		if err != nil {
			return err
		}
		rec.Number = n
	}
	_, err := r.c.InsertOne(ctx, rec)
	return mapErr(err)
}

func noteCounter(ownerID string) string { return "notes:" + ownerID }

// nextNumber counts up the owner's note numbers atomically.
func (r *RecordingRepo) nextNumber(ctx context.Context, ownerID string) (int64, error) {
	var doc struct {
		Seq int64 `bson:"seq"`
	}
	err := r.counters.FindOneAndUpdate(ctx, bson.M{"_id": noteCounter(ownerID)}, bson.M{"$inc": bson.M{"seq": int64(1)}},
		options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After)).Decode(&doc)
	return doc.Seq, err
}

// NumberNotes gives every owned recording without a number the owner's next one, oldest
// first (notes from before numbers). The counters never fall behind numbers already used.
// It is safe to run on every start.
func (r *RecordingRepo) NumberNotes(ctx context.Context) (int, error) {
	cur, err := r.c.Aggregate(ctx, mongo.Pipeline{
		{{Key: "$match", Value: bson.M{"number": bson.M{"$exists": true}}}},
		{{Key: "$group", Value: bson.M{"_id": "$ownerId", "max": bson.M{"$max": "$number"}}}},
	})
	if err != nil {
		return 0, err
	}
	var used []struct {
		OwnerID string `bson:"_id"`
		Max     int64  `bson:"max"`
	}
	if err := cur.All(ctx, &used); err != nil {
		return 0, err
	}
	for _, u := range used {
		if _, err := r.counters.UpdateOne(ctx, bson.M{"_id": noteCounter(u.OwnerID)}, bson.M{"$max": bson.M{"seq": u.Max}},
			options.Update().SetUpsert(true)); err != nil {
			return 0, err
		}
	}

	cur, err = r.c.Find(ctx, bson.M{"number": bson.M{"$exists": false}, "ownerId": bson.M{"$nin": bson.A{"", nil}}},
		options.Find().SetSort(bson.D{{Key: "createdAt", Value: 1}, {Key: "_id", Value: 1}}).SetProjection(bson.M{"_id": 1, "ownerId": 1}))
	if err != nil {
		return 0, err
	}
	var todo []struct {
		ID      string `bson:"_id"`
		OwnerID string `bson:"ownerId"`
	}
	if err := cur.All(ctx, &todo); err != nil {
		return 0, err
	}
	for _, t := range todo {
		n, err := r.nextNumber(ctx, t.OwnerID)
		if err != nil {
			return 0, err
		}
		if _, err := r.c.UpdateOne(ctx, bson.M{"_id": t.ID, "number": bson.M{"$exists": false}}, bson.M{"$set": bson.M{"number": n}}); err != nil {
			return 0, err
		}
	}
	return len(todo), nil
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
	if f.OwnerID != "" {
		filter["ownerId"] = f.OwnerID
	}
	if f.DeviceID != "" {
		filter["deviceId"] = f.DeviceID
	}
	if f.Status != "" {
		filter["status"] = f.Status
	}
	if f.Number != 0 {
		filter["number"] = f.Number
	}
	opts := options.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}}).SetSkip(int64(f.Offset))
	if f.Brief {
		opts.SetProjection(bson.M{"transcript": 0, "summary.markdown": 0})
	}
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

func (r *RecordingRepo) AssignOwnerless(ctx context.Context, ownerID string) (int, error) {
	return assignOwnerless(ctx, r.c, ownerID)
}

func (r *RecordingRepo) MoveFolder(ctx context.Context, ownerID, from, to string) error {
	filter := bson.M{"ownerId": ownerID, "folderId": from}
	update := bson.M{"$set": bson.M{"folderId": to}}
	if to == "" {
		update = bson.M{"$unset": bson.M{"folderId": ""}}
	}
	if _, err := r.c.UpdateMany(ctx, filter, update); err != nil {
		return err
	}
	// Boards showing the folder show the one its notes moved into.
	_, err := r.c.UpdateMany(ctx,
		bson.M{"ownerId": ownerID, "board.scope.kind": recording.ScopeFolder, "board.scope.id": from},
		bson.M{"$set": bson.M{"board.scope.id": to}})
	return err
}

func (r *RecordingRepo) MoveSubNotes(ctx context.Context, ownerID, from, toParent, toFolder string) error {
	set, unset := bson.M{}, bson.M{}
	for field, v := range map[string]string{"parentId": toParent, "folderId": toFolder} {
		if v == "" {
			unset[field] = ""
		} else {
			set[field] = v
		}
	}
	update := bson.M{}
	if len(set) > 0 {
		update["$set"] = set
	}
	if len(unset) > 0 {
		update["$unset"] = unset
	}
	_, err := r.c.UpdateMany(ctx, bson.M{"ownerId": ownerID, "parentId": from}, update)
	return err
}

func (r *RecordingRepo) RemoveLabel(ctx context.Context, ownerID, labelID string) error {
	if _, err := r.c.UpdateMany(ctx, bson.M{"ownerId": ownerID, "labels": labelID}, bson.M{"$pull": bson.M{"labels": labelID}}); err != nil {
		return err
	}
	// Boards showing the label show nothing until another scope is chosen.
	_, err := r.c.UpdateMany(ctx,
		bson.M{"ownerId": ownerID, "board.scope.kind": recording.ScopeLabel, "board.scope.id": labelID},
		bson.M{"$set": bson.M{"board.scope": recording.BoardScope{}}})
	return err
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
