package mongo

import (
	"context"
	"errors"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain"
)

// ErrNotFound is returned by repositories when a document does not exist.
var ErrNotFound = domain.ErrNotFound

// NewID returns a fresh id as an ObjectID hex string. All documents use string _id values
// (the hex form) so references between collections stay plain strings end-to-end.
func NewID() string { return primitive.NewObjectID().Hex() }

// IsNoDocs reports whether err is the driver's "no documents" sentinel, so repositories can
// map it to ErrNotFound.
func IsNoDocs(err error) bool { return errors.Is(err, mongo.ErrNoDocuments) }

// assignOwnerless sets ownerId on documents that have none (data created before users
// existed).
func assignOwnerless(ctx context.Context, c *mongo.Collection, ownerID string) (int, error) {
	res, err := c.UpdateMany(ctx,
		bson.M{"$or": bson.A{bson.M{"ownerId": bson.M{"$exists": false}}, bson.M{"ownerId": ""}}},
		bson.M{"$set": bson.M{"ownerId": ownerID}})
	if err != nil {
		return 0, err
	}
	return int(res.ModifiedCount), nil
}

// mapErr translates driver errors into the domain sentinels.
func mapErr(err error) error {
	switch {
	case err == nil:
		return nil
	case IsNoDocs(err):
		return domain.ErrNotFound
	case mongo.IsDuplicateKeyError(err):
		return domain.ErrDuplicate
	}
	return err
}
