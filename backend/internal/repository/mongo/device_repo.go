package mongo

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/device"
)

// DeviceRepo is the MongoDB implementation of ports.DeviceRepository.
type DeviceRepo struct{ c *mongo.Collection }

// NewDeviceRepo builds the repository.
func NewDeviceRepo(s *Store) *DeviceRepo { return &DeviceRepo{c: s.DB().Collection(CollDevices)} }

func (r *DeviceRepo) Create(ctx context.Context, d *device.Device) error {
	_, err := r.c.InsertOne(ctx, d)
	return mapErr(err)
}

func (r *DeviceRepo) Get(ctx context.Context, id string) (*device.Device, error) {
	return r.findOne(ctx, bson.M{"_id": id})
}

func (r *DeviceRepo) GetByTokenHash(ctx context.Context, hash string) (*device.Device, error) {
	return r.findOne(ctx, bson.M{"tokenHash": hash})
}

func (r *DeviceRepo) List(ctx context.Context, ownerID string) ([]*device.Device, error) {
	filter := bson.M{}
	if ownerID != "" {
		filter["ownerId"] = ownerID
	}
	cur, err := r.c.Find(ctx, filter, options.Find().SetSort(bson.D{{Key: "createdAt", Value: 1}}))
	if err != nil {
		return nil, err
	}
	out := []*device.Device{}
	return out, cur.All(ctx, &out)
}

func (r *DeviceRepo) Update(ctx context.Context, d *device.Device) error {
	res, err := r.c.ReplaceOne(ctx, bson.M{"_id": d.ID}, d)
	if err != nil {
		return mapErr(err)
	}
	if res.MatchedCount == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *DeviceRepo) Delete(ctx context.Context, id string) error {
	res, err := r.c.DeleteOne(ctx, bson.M{"_id": id})
	if err != nil {
		return err
	}
	if res.DeletedCount == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *DeviceRepo) AssignOwnerless(ctx context.Context, ownerID string) (int, error) {
	return assignOwnerless(ctx, r.c, ownerID)
}

func (r *DeviceRepo) findOne(ctx context.Context, filter bson.M) (*device.Device, error) {
	var d device.Device
	if err := r.c.FindOne(ctx, filter).Decode(&d); err != nil {
		return nil, mapErr(err)
	}
	return &d, nil
}
