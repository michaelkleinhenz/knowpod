package mongo

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/user"
)

// UserRepo is the MongoDB implementation of ports.UserRepository.
type UserRepo struct{ c *mongo.Collection }

// NewUserRepo builds the repository.
func NewUserRepo(s *Store) *UserRepo { return &UserRepo{c: s.DB().Collection(CollUsers)} }

func (r *UserRepo) Create(ctx context.Context, u *user.User) error {
	_, err := r.c.InsertOne(ctx, u)
	return mapErr(err)
}

func (r *UserRepo) Get(ctx context.Context, id string) (*user.User, error) {
	return r.findOne(ctx, bson.M{"_id": id})
}

func (r *UserRepo) GetByEmail(ctx context.Context, email string) (*user.User, error) {
	return r.findOne(ctx, bson.M{"email": email})
}

func (r *UserRepo) GetByPocketWebhookID(ctx context.Context, webhookID string) (*user.User, error) {
	if webhookID == "" {
		return nil, ErrNotFound
	}
	return r.findOne(ctx, bson.M{"pocket.webhookId": webhookID})
}

func (r *UserRepo) GetByCalendarTokenHash(ctx context.Context, hash string) (*user.User, error) {
	if hash == "" {
		return nil, ErrNotFound
	}
	return r.findOne(ctx, bson.M{"calendar.tokenHash": hash})
}

func (r *UserRepo) List(ctx context.Context) ([]*user.User, error) {
	cur, err := r.c.Find(ctx, bson.M{}, options.Find().SetSort(bson.D{{Key: "email", Value: 1}}))
	if err != nil {
		return nil, err
	}
	out := []*user.User{}
	return out, cur.All(ctx, &out)
}

func (r *UserRepo) Update(ctx context.Context, u *user.User) error {
	res, err := r.c.ReplaceOne(ctx, bson.M{"_id": u.ID}, u)
	if err != nil {
		return mapErr(err)
	}
	if res.MatchedCount == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *UserRepo) Delete(ctx context.Context, id string) error {
	res, err := r.c.DeleteOne(ctx, bson.M{"_id": id})
	if err != nil {
		return err
	}
	if res.DeletedCount == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *UserRepo) findOne(ctx context.Context, filter bson.M) (*user.User, error) {
	var u user.User
	if err := r.c.FindOne(ctx, filter).Decode(&u); err != nil {
		return nil, mapErr(err)
	}
	return &u, nil
}

// SessionRepo is the MongoDB implementation of ports.SessionRepository.
type SessionRepo struct{ c *mongo.Collection }

// NewSessionRepo builds the repository.
func NewSessionRepo(s *Store) *SessionRepo { return &SessionRepo{c: s.DB().Collection(CollSessions)} }

func (r *SessionRepo) Create(ctx context.Context, s *user.Session) error {
	_, err := r.c.InsertOne(ctx, s)
	return mapErr(err)
}

func (r *SessionRepo) Get(ctx context.Context, tokenHash string) (*user.Session, error) {
	var s user.Session
	if err := r.c.FindOne(ctx, bson.M{"_id": tokenHash}).Decode(&s); err != nil {
		return nil, mapErr(err)
	}
	return &s, nil
}

func (r *SessionRepo) Delete(ctx context.Context, tokenHash string) error {
	res, err := r.c.DeleteOne(ctx, bson.M{"_id": tokenHash})
	if err != nil {
		return err
	}
	if res.DeletedCount == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *SessionRepo) DeleteByUser(ctx context.Context, userID, keepTokenHash string) error {
	_, err := r.c.DeleteMany(ctx, bson.M{"userId": userID, "_id": bson.M{"$ne": keepTokenHash}})
	return err
}
