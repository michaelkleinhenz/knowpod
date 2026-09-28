package mongo

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/michaelkleinhenz/knowpod-service/backend/internal/domain/folder"
)

// FolderRepo is the MongoDB implementation of ports.FolderRepository.
type FolderRepo struct{ c *mongo.Collection }

// NewFolderRepo builds the repository.
func NewFolderRepo(s *Store) *FolderRepo { return &FolderRepo{c: s.DB().Collection(CollFolders)} }

func (r *FolderRepo) Create(ctx context.Context, f *folder.Folder) error {
	_, err := r.c.InsertOne(ctx, f)
	return mapErr(err)
}

func (r *FolderRepo) Get(ctx context.Context, id string) (*folder.Folder, error) {
	var f folder.Folder
	if err := r.c.FindOne(ctx, bson.M{"_id": id}).Decode(&f); err != nil {
		return nil, mapErr(err)
	}
	return &f, nil
}

func (r *FolderRepo) List(ctx context.Context, ownerID string) ([]*folder.Folder, error) {
	cur, err := r.c.Find(ctx, bson.M{"ownerId": ownerID}, options.Find().SetSort(bson.D{{Key: "name", Value: 1}}))
	if err != nil {
		return nil, err
	}
	out := []*folder.Folder{}
	return out, cur.All(ctx, &out)
}

func (r *FolderRepo) Update(ctx context.Context, f *folder.Folder) error {
	res, err := r.c.ReplaceOne(ctx, bson.M{"_id": f.ID}, f)
	if err != nil {
		return mapErr(err)
	}
	if res.MatchedCount == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *FolderRepo) Delete(ctx context.Context, id string) error {
	res, err := r.c.DeleteOne(ctx, bson.M{"_id": id})
	if err != nil {
		return err
	}
	if res.DeletedCount == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *FolderRepo) DeleteByOwner(ctx context.Context, ownerID string) error {
	_, err := r.c.DeleteMany(ctx, bson.M{"ownerId": ownerID})
	return err
}

func (r *FolderRepo) ListSharedWith(ctx context.Context, userID string) ([]*folder.Folder, error) {
	cur, err := r.c.Find(ctx, bson.M{"shares.userId": userID}, options.Find().SetSort(bson.D{{Key: "name", Value: 1}}))
	if err != nil {
		return nil, err
	}
	out := []*folder.Folder{}
	return out, cur.All(ctx, &out)
}

func (r *FolderRepo) RemoveShares(ctx context.Context, userID string) error {
	_, err := r.c.UpdateMany(ctx, bson.M{"shares.userId": userID}, bson.M{"$pull": bson.M{"shares": bson.M{"userId": userID}}})
	return err
}
