package mongo

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/push"
)

// PushSubscriptionRepo is the MongoDB implementation of ports.PushSubscriptionRepository.
type PushSubscriptionRepo struct{ c *mongo.Collection }

// NewPushSubscriptionRepo builds the repository.
func NewPushSubscriptionRepo(s *Store) *PushSubscriptionRepo {
	return &PushSubscriptionRepo{c: s.DB().Collection(CollPushSubscriptions)}
}

func (r *PushSubscriptionRepo) Save(ctx context.Context, s *push.Subscription) error {
	_, err := r.c.ReplaceOne(ctx, bson.M{"_id": s.ID}, s, options.Replace().SetUpsert(true))
	return mapErr(err)
}

func (r *PushSubscriptionRepo) List(ctx context.Context, userID string) ([]*push.Subscription, error) {
	cur, err := r.c.Find(ctx, bson.M{"userId": userID}, options.Find().SetSort(bson.D{{Key: "createdAt", Value: 1}}))
	if err != nil {
		return nil, err
	}
	out := []*push.Subscription{}
	return out, cur.All(ctx, &out)
}

func (r *PushSubscriptionRepo) Delete(ctx context.Context, id string) error {
	_, err := r.c.DeleteOne(ctx, bson.M{"_id": id})
	return err
}

func (r *PushSubscriptionRepo) DeleteByUser(ctx context.Context, userID string) error {
	_, err := r.c.DeleteMany(ctx, bson.M{"userId": userID})
	return err
}
