package mongo

import (
	"context"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/oauth"
)

// OAuthRepo is the MongoDB implementation of ports.OAuthRepository.
type OAuthRepo struct{ clients, grants *mongo.Collection }

// NewOAuthRepo builds the repository.
func NewOAuthRepo(s *Store) *OAuthRepo {
	return &OAuthRepo{clients: s.DB().Collection(CollOAuthClients), grants: s.DB().Collection(CollOAuthGrants)}
}

func (r *OAuthRepo) CreateClient(ctx context.Context, c *oauth.Client) error {
	_, err := r.clients.InsertOne(ctx, c)
	return mapErr(err)
}

func (r *OAuthRepo) GetClient(ctx context.Context, id string) (*oauth.Client, error) {
	var c oauth.Client
	if err := r.clients.FindOne(ctx, bson.M{"_id": id}).Decode(&c); err != nil {
		return nil, mapErr(err)
	}
	return &c, nil
}

func (r *OAuthRepo) CreateGrant(ctx context.Context, g *oauth.Grant) error {
	_, err := r.grants.InsertOne(ctx, g)
	return mapErr(err)
}

func (r *OAuthRepo) ClaimCode(ctx context.Context, codeHash string) (*oauth.Grant, error) {
	if codeHash == "" {
		return nil, ErrNotFound
	}
	var g oauth.Grant
	err := r.grants.FindOneAndUpdate(ctx, bson.M{"codeHash": codeHash}, bson.M{"$unset": bson.M{"codeHash": ""}},
		options.FindOneAndUpdate().SetReturnDocument(options.After)).Decode(&g)
	if err != nil {
		return nil, mapErr(err)
	}
	return &g, nil
}

func (r *OAuthRepo) GetGrantByAccessHash(ctx context.Context, hash string) (*oauth.Grant, error) {
	return r.findGrant(ctx, "accessHash", hash)
}

func (r *OAuthRepo) GetGrantByRefreshHash(ctx context.Context, hash string) (*oauth.Grant, error) {
	return r.findGrant(ctx, "refreshHash", hash)
}

func (r *OAuthRepo) UpdateGrant(ctx context.Context, g *oauth.Grant) error {
	res, err := r.grants.ReplaceOne(ctx, bson.M{"_id": g.ID}, g)
	if err != nil {
		return mapErr(err)
	}
	if res.MatchedCount == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *OAuthRepo) RotateGrant(ctx context.Context, g *oauth.Grant, prevRefreshHash string) error {
	res, err := r.grants.ReplaceOne(ctx, bson.M{"_id": g.ID, "refreshHash": prevRefreshHash}, g)
	if err != nil {
		return mapErr(err)
	}
	if res.MatchedCount == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *OAuthRepo) TouchGrant(ctx context.Context, id string, at time.Time) error {
	_, err := r.grants.UpdateOne(ctx, bson.M{"_id": id}, bson.M{"$set": bson.M{"lastUsedAt": at}})
	return err
}

func (r *OAuthRepo) ListGrants(ctx context.Context, userID string) ([]*oauth.Grant, error) {
	cur, err := r.grants.Find(ctx, bson.M{"userId": userID}, options.Find().SetSort(bson.D{{Key: "createdAt", Value: -1}}))
	if err != nil {
		return nil, err
	}
	out := []*oauth.Grant{}
	return out, cur.All(ctx, &out)
}

func (r *OAuthRepo) DeleteGrant(ctx context.Context, id string) error {
	res, err := r.grants.DeleteOne(ctx, bson.M{"_id": id})
	if err != nil {
		return err
	}
	if res.DeletedCount == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *OAuthRepo) DeleteGrantsByUser(ctx context.Context, userID string) error {
	_, err := r.grants.DeleteMany(ctx, bson.M{"userId": userID})
	return err
}

func (r *OAuthRepo) findGrant(ctx context.Context, field, hash string) (*oauth.Grant, error) {
	if hash == "" {
		return nil, ErrNotFound
	}
	var g oauth.Grant
	if err := r.grants.FindOne(ctx, bson.M{field: hash}).Decode(&g); err != nil {
		return nil, mapErr(err)
	}
	return &g, nil
}
