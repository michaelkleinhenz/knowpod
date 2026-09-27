package mongo

import (
	"context"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Collection names (single source of truth).
const (
	CollDevices    = "devices"
	CollRecordings = "recordings"
	CollUsers      = "users"
	CollSessions   = "sessions"
	CollSettings   = "settings"
	CollThemes     = "themes"
	CollLabels     = "labels"
	CollFolders    = "folders"
	CollTablets    = "tablets"
	// CollPushSubscriptions holds the browsers that receive users' notifications.
	CollPushSubscriptions = "pushSubscriptions"
	// CollCounters holds sequences, e.g. each user's last note number.
	CollCounters = "counters"
	// CollFilters holds users' saved filters.
	CollFilters = "filters"
	// CollTimeEntries holds the time logged on notes.
	CollTimeEntries = "timeEntries"
	// CollOAuthClients holds the AI assistants registered for the MCP server; CollOAuthGrants
	// the access users gave them.
	CollOAuthClients = "oauthClients"
	CollOAuthGrants  = "oauthGrants"
)

// collections lists every collection the service owns. Setup creates any that are missing.
var collections = []string{CollDevices, CollRecordings, CollUsers, CollSessions, CollSettings, CollThemes, CollLabels, CollFolders, CollTablets, CollCounters, CollPushSubscriptions, CollFilters, CollTimeEntries, CollOAuthClients, CollOAuthGrants}

// indexes lists the indexes per collection. Setup creates them; CreateMany on an existing
// identical index is a no-op.
var indexes = map[string][]mongo.IndexModel{
	CollDevices: {
		{Keys: bson.D{{Key: "tokenHash", Value: 1}}, Options: options.Index().SetUnique(true)},
		{Keys: bson.D{{Key: "ownerId", Value: 1}}},
	},
	CollRecordings: {
		// Idempotency key: one recording per device-assigned ID.
		{Keys: bson.D{{Key: "deviceId", Value: 1}, {Key: "clientId", Value: 1}}, Options: options.Index().SetUnique(true)},
		// Worker claim queue.
		{Keys: bson.D{{Key: "status", Value: 1}, {Key: "notBefore", Value: 1}}},
		// Stale upload cleanup.
		{Keys: bson.D{{Key: "status", Value: 1}, {Key: "updatedAt", Value: 1}}},
		// Listing, newest first.
		{Keys: bson.D{{Key: "createdAt", Value: -1}}},
		{Keys: bson.D{{Key: "ownerId", Value: 1}, {Key: "createdAt", Value: -1}}},
		// Sub-notes of a note.
		{Keys: bson.D{{Key: "ownerId", Value: 1}, {Key: "parentId", Value: 1}},
			Options: options.Index().SetPartialFilterExpression(bson.M{"parentId": bson.M{"$exists": true}})},
		// Notes in the trash, deleted for good when they have been there long enough.
		{Keys: bson.D{{Key: "deletedAt", Value: 1}},
			Options: options.Index().SetPartialFilterExpression(bson.M{"deletedAt": bson.M{"$exists": true}})},
		// Reminders due to be sent.
		{Keys: bson.D{{Key: "remindAt", Value: 1}},
			Options: options.Index().SetPartialFilterExpression(bson.M{"remindAt": bson.M{"$exists": true}})},
		// Note numbers are unique per user.
		{Keys: bson.D{{Key: "ownerId", Value: 1}, {Key: "number", Value: 1}},
			Options: options.Index().SetUnique(true).SetPartialFilterExpression(bson.M{"number": bson.M{"$exists": true}})},
	},
	CollUsers: {
		{Keys: bson.D{{Key: "email", Value: 1}}, Options: options.Index().SetUnique(true)},
		// Webhook routing; users without Pocket have no webhookId.
		{Keys: bson.D{{Key: "pocket.webhookId", Value: 1}}, Options: options.Index().SetUnique(true).SetSparse(true)},
		// Calendar feed lookup; users without a feed have no token hash.
		{Keys: bson.D{{Key: "calendar.tokenHash", Value: 1}}, Options: options.Index().SetUnique(true).SetSparse(true)},
		// MCP server sign-in; users without MCP access have no token hash.
		{Keys: bson.D{{Key: "mcp.tokenHash", Value: 1}}, Options: options.Index().SetUnique(true).SetSparse(true)},
	},
	CollFilters: {
		{Keys: bson.D{{Key: "ownerId", Value: 1}, {Key: "name", Value: 1}}},
	},
	CollTimeEntries: {
		// The time log of a period.
		{Keys: bson.D{{Key: "ownerId", Value: 1}, {Key: "start", Value: 1}}},
		{Keys: bson.D{{Key: "noteId", Value: 1}}},
		// At most one running timer per user.
		{Keys: bson.D{{Key: "ownerId", Value: 1}}, Options: options.Index().SetName("running_timer").SetUnique(true).
			SetPartialFilterExpression(bson.M{"running": true})},
	},
	CollOAuthGrants: {
		{Keys: bson.D{{Key: "userId", Value: 1}, {Key: "createdAt", Value: -1}}},
		{Keys: bson.D{{Key: "codeHash", Value: 1}}, Options: options.Index().SetUnique(true).SetSparse(true)},
		{Keys: bson.D{{Key: "accessHash", Value: 1}}, Options: options.Index().SetUnique(true).SetSparse(true)},
		{Keys: bson.D{{Key: "refreshHash", Value: 1}}, Options: options.Index().SetUnique(true).SetSparse(true)},
		// MongoDB deletes grants once their code or refresh token expired.
		{Keys: bson.D{{Key: "expiresAt", Value: 1}}, Options: options.Index().SetExpireAfterSeconds(0)},
	},
	CollThemes: {
		{Keys: bson.D{{Key: "ownerId", Value: 1}, {Key: "name", Value: 1}}},
	},
	CollLabels: {
		{Keys: bson.D{{Key: "ownerId", Value: 1}, {Key: "name", Value: 1}}},
	},
	CollFolders: {
		{Keys: bson.D{{Key: "ownerId", Value: 1}, {Key: "name", Value: 1}}},
	},
	CollPushSubscriptions: {
		{Keys: bson.D{{Key: "userId", Value: 1}}},
	},
	CollSessions: {
		// MongoDB deletes sessions once they expire.
		{Keys: bson.D{{Key: "expiresAt", Value: 1}}, Options: options.Index().SetExpireAfterSeconds(0)},
		{Keys: bson.D{{Key: "userId", Value: 1}}},
	},
}

// Setup creates collections and indexes idempotently. It is safe to run on every start.
func Setup(ctx context.Context, db *mongo.Database) error {
	if err := ensureCollections(ctx, db); err != nil {
		return err
	}
	return ensureIndexes(ctx, db)
}

func ensureCollections(ctx context.Context, db *mongo.Database) error {
	existing, err := db.ListCollectionNames(ctx, bson.M{})
	if err != nil {
		return err
	}
	have := map[string]bool{}
	for _, n := range existing {
		have[n] = true
	}
	for _, name := range collections {
		if !have[name] {
			if err := db.CreateCollection(ctx, name); err != nil {
				return err
			}
		}
	}
	return nil
}

func ensureIndexes(ctx context.Context, db *mongo.Database) error {
	for coll, models := range indexes {
		if len(models) == 0 {
			continue
		}
		if _, err := db.Collection(coll).Indexes().CreateMany(ctx, models); err != nil {
			return err
		}
	}
	return nil
}
