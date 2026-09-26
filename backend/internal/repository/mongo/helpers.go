package mongo

import (
	"errors"

	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
)

// ErrNotFound is returned by repositories when a document does not exist.
var ErrNotFound = errors.New("not found")

// NewID returns a fresh id as an ObjectID hex string. All documents use string _id values
// (the hex form) so references between collections stay plain strings end-to-end.
func NewID() string { return primitive.NewObjectID().Hex() }

// IsNoDocs reports whether err is the driver's "no documents" sentinel, so repositories can
// map it to ErrNotFound.
func IsNoDocs(err error) bool { return errors.Is(err, mongo.ErrNoDocuments) }
