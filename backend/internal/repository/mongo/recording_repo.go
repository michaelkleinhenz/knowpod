package mongo

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain"
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

// ReserveNumbers makes sure the owner's next note number is above upTo.
func (r *RecordingRepo) ReserveNumbers(ctx context.Context, ownerID string, upTo int64) error {
	_, err := r.counters.UpdateOne(ctx, bson.M{"_id": noteCounter(ownerID)}, bson.M{"$max": bson.M{"seq": upTo}},
		options.Update().SetUpsert(true))
	return err
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

// Update replaces the recording while it is still at rec.Version (recordings from before
// versions have none, which counts as 0) and counts the version up.
func (r *RecordingRepo) Update(ctx context.Context, rec *recording.Recording) error {
	filter := bson.M{"_id": rec.ID, "version": rec.Version}
	if rec.Version == 0 {
		filter["version"] = bson.M{"$in": bson.A{nil, int64(0)}}
	}
	rec.Version++
	res, err := r.c.ReplaceOne(ctx, filter, rec)
	if err != nil {
		rec.Version--
		return mapErr(err)
	}
	if res.MatchedCount == 0 {
		rec.Version--
		n, err := r.c.CountDocuments(ctx, bson.M{"_id": rec.ID})
		if err != nil {
			return err
		}
		if n > 0 {
			return domain.ErrChanged
		}
		return ErrNotFound
	}
	return nil
}

// bump makes an update also count up the version, so that copies read before it can't be
// saved over it.
func bump(update bson.M) bson.M {
	update["$inc"] = bson.M{"version": int64(1)}
	return update
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
	if f.UserID != "" {
		filter["$or"] = bson.A{bson.M{"ownerId": f.UserID}, bson.M{"members.userId": f.UserID}}
	}
	if f.ParentID != "" {
		filter["parentId"] = f.ParentID
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
	switch f.Trash {
	case recording.TrashExclude:
		filter["deletedAt"] = bson.M{"$exists": false}
	case recording.TrashOnly:
		filter["deletedAt"] = bson.M{"$exists": true}
	}
	opts := options.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}}).SetSkip(int64(f.Offset))
	if f.Brief {
		opts.SetProjection(bson.M{"transcript": 0, "summary.markdown": 0, "summary.actionItems": 0})
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
		bson.M{"$set": bson.M{"notBefore": leaseUntil, "updatedAt": now}, "$inc": bson.M{"attempts": 1, "version": int64(1)}},
		options.FindOneAndUpdate().SetSort(bson.D{{Key: "notBefore", Value: 1}}).SetReturnDocument(options.After),
	).Decode(&rec)
	if err != nil {
		return nil, mapErr(err)
	}
	return &rec, nil
}

func (r *RecordingRepo) SetRemindAt(ctx context.Context, id string, at *time.Time) error {
	update := bson.M{"$unset": bson.M{"remindAt": ""}}
	if at != nil {
		update = bson.M{"$set": bson.M{"remindAt": *at}}
	}
	res, err := r.c.UpdateOne(ctx, bson.M{"_id": id}, bump(update))
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *RecordingRepo) ClaimReminder(ctx context.Context, now time.Time) (*recording.Recording, error) {
	var rec recording.Recording
	err := r.c.FindOneAndUpdate(ctx,
		bson.M{"remindAt": bson.M{"$lte": now}},
		bump(bson.M{"$unset": bson.M{"remindAt": ""}}),
		options.FindOneAndUpdate().SetSort(bson.D{{Key: "remindAt", Value: 1}}).
			SetProjection(bson.M{"transcript": 0, "summary.markdown": 0, "summary.actionItems": 0}),
	).Decode(&rec)
	if err != nil {
		return nil, mapErr(err)
	}
	return &rec, nil
}

func (r *RecordingRepo) SetMemberRemindAt(ctx context.Context, id, userID string, at *time.Time) error {
	update := bson.M{"$unset": bson.M{"members.$.remindAt": ""}}
	if at != nil {
		update = bson.M{"$set": bson.M{"members.$.remindAt": *at}}
	}
	res, err := r.c.UpdateOne(ctx, bson.M{"_id": id, "members.userId": userID}, bump(update))
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *RecordingRepo) ClaimMemberReminder(ctx context.Context, now time.Time) (*recording.Recording, string, error) {
	due := bson.M{"remindAt": bson.M{"$lte": now}}
	var rec recording.Recording
	// The positional operator clears the first member matching the query, which is the
	// first one due in the document as it was before.
	err := r.c.FindOneAndUpdate(ctx,
		bson.M{"members": bson.M{"$elemMatch": due}},
		bump(bson.M{"$unset": bson.M{"members.$.remindAt": ""}}),
		options.FindOneAndUpdate().SetReturnDocument(options.Before).
			SetProjection(bson.M{"transcript": 0, "summary.markdown": 0, "summary.actionItems": 0}),
	).Decode(&rec)
	if err != nil {
		return nil, "", mapErr(err)
	}
	for _, m := range rec.Members {
		if m.RemindAt != nil && !m.RemindAt.After(now) {
			return &rec, m.UserID, nil
		}
	}
	return nil, "", ErrNotFound
}

func (r *RecordingRepo) RemoveMember(ctx context.Context, userID string) error {
	_, err := r.c.UpdateMany(ctx,
		bson.M{"$or": bson.A{bson.M{"members.userId": userID}, bson.M{"shares.userId": userID}}},
		bump(bson.M{"$pull": bson.M{"members": bson.M{"userId": userID}, "shares": bson.M{"userId": userID}}}))
	if err != nil {
		return err
	}
	// Tasks assigned to the user are unassigned.
	_, err = r.c.UpdateMany(ctx, bson.M{"assigneeId": userID}, bump(bson.M{"$unset": bson.M{"assigneeId": ""}}))
	return err
}

// memberOf selects the member entry of userID in an update with array filters.
func memberOf(userID string, match bson.M) *options.UpdateOptions {
	f := bson.M{"m.userId": userID}
	for k, v := range match {
		f["m."+k] = v
	}
	return options.Update().SetArrayFilters(options.ArrayFilters{Filters: []interface{}{f}})
}

func (r *RecordingRepo) ListStale(ctx context.Context, status recording.Status, before time.Time, limit int) ([]*recording.Recording, error) {
	return r.find(ctx, bson.M{"status": status, "updatedAt": bson.M{"$lt": before}}, options.Find().SetLimit(int64(limit)))
}

func (r *RecordingRepo) ListTrashed(ctx context.Context, before time.Time, limit int) ([]*recording.Recording, error) {
	return r.find(ctx, bson.M{"deletedAt": bson.M{"$lt": before}}, options.Find().SetLimit(int64(limit)))
}

func (r *RecordingRepo) AssignOwnerless(ctx context.Context, ownerID string) (int, error) {
	return assignOwnerless(ctx, r.c, ownerID)
}

func (r *RecordingRepo) MoveFolder(ctx context.Context, ownerID, from, to string) error {
	// The notes go after the ordered notes of the folder they move into.
	filter := bson.M{"ownerId": ownerID, "folderId": from}
	update := bson.M{"$set": bson.M{"folderId": to}, "$unset": bson.M{"position": ""}}
	if to == "" {
		update = bson.M{"$unset": bson.M{"folderId": "", "position": ""}}
	}
	if _, err := r.c.UpdateMany(ctx, filter, bump(update)); err != nil {
		return err
	}
	// Shared notes the user put into the folder as a member.
	update = bson.M{"$set": bson.M{"members.$[m].folderId": to}, "$unset": bson.M{"members.$[m].position": ""}}
	if to == "" {
		update = bson.M{"$unset": bson.M{"members.$[m].folderId": "", "members.$[m].position": ""}}
	}
	if _, err := r.c.UpdateMany(ctx, bson.M{"members": bson.M{"$elemMatch": bson.M{"userId": ownerID, "folderId": from}}},
		bump(update), memberOf(ownerID, bson.M{"folderId": from})); err != nil {
		return err
	}
	// Boards showing the folder show the one its notes moved into.
	_, err := r.c.UpdateMany(ctx,
		bson.M{"ownerId": ownerID, "board.scope.kind": recording.ScopeFolder, "board.scope.id": from},
		bump(bson.M{"$set": bson.M{"board.scope.id": to}}))
	return err
}

func (r *RecordingRepo) MoveSubNotes(ctx context.Context, ownerID, from, toParent, toFolder string) error {
	// The notes go after the ordered notes of the place they move into.
	set, unset := bson.M{}, bson.M{"position": ""}
	for field, v := range map[string]string{"parentId": toParent, "folderId": toFolder} {
		if v == "" {
			unset[field] = ""
		} else {
			set[field] = v
		}
	}
	update := bson.M{"$unset": unset}
	if len(set) > 0 {
		update["$set"] = set
	}
	_, err := r.c.UpdateMany(ctx, bson.M{"ownerId": ownerID, "parentId": from}, bump(update))
	return err
}

func (r *RecordingRepo) RemoveLabel(ctx context.Context, ownerID, labelID string) error {
	if _, err := r.c.UpdateMany(ctx, bson.M{"ownerId": ownerID, "labels": labelID}, bump(bson.M{"$pull": bson.M{"labels": labelID}})); err != nil {
		return err
	}
	// Shared notes the user labeled as a member.
	if _, err := r.c.UpdateMany(ctx, bson.M{"members": bson.M{"$elemMatch": bson.M{"userId": ownerID, "labels": labelID}}},
		bump(bson.M{"$pull": bson.M{"members.$[m].labels": labelID}}), memberOf(ownerID, nil)); err != nil {
		return err
	}
	// Boards showing the label show nothing until another scope is chosen.
	_, err := r.c.UpdateMany(ctx,
		bson.M{"ownerId": ownerID, "board.scope.kind": recording.ScopeLabel, "board.scope.id": labelID},
		bump(bson.M{"$set": bson.M{"board.scope": recording.BoardScope{}}}))
	return err
}

func (r *RecordingRepo) ClearBoardScope(ctx context.Context, ownerID string, scope recording.BoardScope) error {
	_, err := r.c.UpdateMany(ctx,
		bson.M{"ownerId": ownerID, "board.scope.kind": scope.Kind, "board.scope.id": scope.ID},
		bump(bson.M{"$set": bson.M{"board.scope": recording.BoardScope{}}}))
	return err
}

func (r *RecordingRepo) AddTrackedSeconds(ctx context.Context, id string, seconds int64) error {
	res, err := r.c.UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$inc": bson.M{"trackedSeconds": seconds, "version": int64(1)}})
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return ErrNotFound
	}
	return nil
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
