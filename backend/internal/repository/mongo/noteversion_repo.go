package mongo

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/noteversion"
)

// NoteVersionRepo is the MongoDB implementation of ports.NoteVersionRepository.
type NoteVersionRepo struct{ c *mongo.Collection }

// NewNoteVersionRepo builds the repository.
func NewNoteVersionRepo(s *Store) *NoteVersionRepo {
	return &NoteVersionRepo{c: s.DB().Collection(CollNoteVersions)}
}

// newestFirst sorts a note's versions, newest first.
var newestFirst = bson.D{{Key: "createdAt", Value: -1}, {Key: "_id", Value: -1}}

// brief leaves out the text.
var brief = bson.M{"markdown": 0}

func (r *NoteVersionRepo) Create(ctx context.Context, v *noteversion.Version) error {
	_, err := r.c.InsertOne(ctx, v)
	return mapErr(err)
}

func (r *NoteVersionRepo) Get(ctx context.Context, id string) (*noteversion.Version, error) {
	var v noteversion.Version
	if err := r.c.FindOne(ctx, bson.M{"_id": id}).Decode(&v); err != nil {
		return nil, mapErr(err)
	}
	return &v, nil
}

func (r *NoteVersionRepo) List(ctx context.Context, noteID string) ([]*noteversion.Version, error) {
	cur, err := r.c.Find(ctx, bson.M{"noteId": noteID}, options.Find().SetSort(newestFirst).SetProjection(brief))
	if err != nil {
		return nil, err
	}
	out := []*noteversion.Version{}
	return out, cur.All(ctx, &out)
}

func (r *NoteVersionRepo) Latest(ctx context.Context, noteID string) (*noteversion.Version, error) {
	var v noteversion.Version
	err := r.c.FindOne(ctx, bson.M{"noteId": noteID}, options.FindOne().SetSort(newestFirst).SetProjection(brief)).Decode(&v)
	if err != nil {
		return nil, mapErr(err)
	}
	return &v, nil
}

func (r *NoteVersionRepo) Prune(ctx context.Context, noteID string, keep int) error {
	cur, err := r.c.Find(ctx, bson.M{"noteId": noteID},
		options.Find().SetSort(newestFirst).SetSkip(int64(keep)).SetProjection(bson.M{"_id": 1}))
	if err != nil {
		return err
	}
	var old []struct {
		ID string `bson:"_id"`
	}
	if err := cur.All(ctx, &old); err != nil || len(old) == 0 {
		return err
	}
	ids := make([]string, len(old))
	for i, o := range old {
		ids[i] = o.ID
	}
	_, err = r.c.DeleteMany(ctx, bson.M{"_id": bson.M{"$in": ids}})
	return err
}

func (r *NoteVersionRepo) DeleteByNote(ctx context.Context, noteID string) error {
	_, err := r.c.DeleteMany(ctx, bson.M{"noteId": noteID})
	return err
}
