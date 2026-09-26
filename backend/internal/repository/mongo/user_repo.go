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

func (r *UserRepo) GetByEmail(ctx context.Context, email string) (*user.User, error) {
	var u user.User
	if err := r.c.FindOne(ctx, bson.M{"email": email}).Decode(&u); err != nil {
		return nil, mapErr(err)
	}
	return &u, nil
}

func (r *UserRepo) Upsert(ctx context.Context, u *user.User) error {
	_, err := r.c.ReplaceOne(ctx, bson.M{"email": u.Email}, u, options.Replace().SetUpsert(true))
	return mapErr(err)
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

func (r *SessionRepo) DeleteByEmail(ctx context.Context, email, keepTokenHash string) error {
	_, err := r.c.DeleteMany(ctx, bson.M{"email": email, "_id": bson.M{"$ne": keepTokenHash}})
	return err
}
