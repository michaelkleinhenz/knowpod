package mongo

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/settings"
)

// openRouterSettingsID is the settings document holding the OpenRouter configuration.
const openRouterSettingsID = "openrouter"

// emailSettingsID is the settings document holding the email (SES) configuration.
const emailSettingsID = "email"

// webPushSettingsID is the settings document holding the VAPID keys.
const webPushSettingsID = "webpush"

// SettingsRepo is the MongoDB implementation of ports.SettingsRepository. Each settings
// group is one document in the settings collection.
type SettingsRepo struct{ c *mongo.Collection }

// NewSettingsRepo builds the repository.
func NewSettingsRepo(s *Store) *SettingsRepo {
	return &SettingsRepo{c: s.DB().Collection(CollSettings)}
}

func (r *SettingsRepo) OpenRouter(ctx context.Context) (*settings.OpenRouter, error) {
	var s settings.OpenRouter
	err := r.c.FindOne(ctx, bson.M{"_id": openRouterSettingsID}).Decode(&s)
	if IsNoDocs(err) {
		return &settings.OpenRouter{}, nil
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *SettingsRepo) InitWebPush(ctx context.Context, k *settings.WebPush) (*settings.WebPush, error) {
	if _, err := r.c.UpdateOne(ctx, bson.M{"_id": webPushSettingsID}, bson.M{"$setOnInsert": k}, options.Update().SetUpsert(true)); err != nil {
		return nil, err
	}
	var out settings.WebPush
	return &out, r.c.FindOne(ctx, bson.M{"_id": webPushSettingsID}).Decode(&out)
}

func (r *SettingsRepo) SaveOpenRouter(ctx context.Context, s *settings.OpenRouter) error {
	_, err := r.c.UpdateOne(ctx, bson.M{"_id": openRouterSettingsID}, bson.M{"$set": s}, options.Update().SetUpsert(true))
	return err
}

func (r *SettingsRepo) Email(ctx context.Context) (*settings.Email, error) {
	var s settings.Email
	err := r.c.FindOne(ctx, bson.M{"_id": emailSettingsID}).Decode(&s)
	if IsNoDocs(err) {
		return &settings.Email{}, nil
	}
	if err != nil {
		return nil, err
	}
	return &s, nil
}

func (r *SettingsRepo) SaveEmail(ctx context.Context, s *settings.Email) error {
	_, err := r.c.UpdateOne(ctx, bson.M{"_id": emailSettingsID}, bson.M{"$set": s}, options.Update().SetUpsert(true))
	return err
}
