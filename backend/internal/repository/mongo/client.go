// Package mongo contains the MongoDB persistence layer. It is the only package that imports
// the mongo driver; everything else should depend on interfaces.
package mongo

import (
	"context"
	"log/slog"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// Store wraps the connected database and exposes the transaction manager.
type Store struct {
	client     *mongo.Client
	db         *mongo.Database
	replicaSet bool
}

// Connect dials MongoDB and pings it.
func Connect(ctx context.Context, uri, dbName string) (*Store, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	client, err := mongo.Connect(ctx, options.Client().ApplyURI(uri))
	if err != nil {
		return nil, err
	}
	if err := client.Ping(ctx, nil); err != nil {
		return nil, err
	}
	rs := detectReplicaSet(ctx, client.Database(dbName))
	if !rs {
		slog.Warn("MongoDB is not a replica set member; transactions will run without atomicity guarantees")
	}
	return &Store{client: client, db: client.Database(dbName), replicaSet: rs}, nil
}

func detectReplicaSet(ctx context.Context, db *mongo.Database) bool {
	var result bson.M
	if err := db.RunCommand(ctx, bson.D{{Key: "hello", Value: 1}}).Decode(&result); err != nil {
		return false
	}
	_, ok := result["setName"]
	return ok
}

// DB exposes the raw database (used only by repository constructors in this package).
func (s *Store) DB() *mongo.Database { return s.db }

// Client exposes the raw client (used by the transaction manager).
func (s *Store) Client() *mongo.Client { return s.client }

// Ping checks that the database is reachable (used by the health check).
func (s *Store) Ping(ctx context.Context) error { return s.client.Ping(ctx, nil) }

// Disconnect closes the connection.
func (s *Store) Disconnect(ctx context.Context) error { return s.client.Disconnect(ctx) }

// TxManager runs a function inside a multi-document transaction on the replica set.
// On standalone deployments it falls back to executing fn without transaction guarantees.
type TxManager struct {
	client     *mongo.Client
	replicaSet bool
}

// NewTxManager builds the transaction manager.
func (s *Store) NewTxManager() *TxManager {
	return &TxManager{client: s.client, replicaSet: s.replicaSet}
}

// WithTransaction starts a session, runs fn inside a transaction and commits/aborts it.
// The session context is threaded through fn so repository calls join the transaction.
// On a standalone MongoDB it executes fn directly without transaction wrapping.
func (t *TxManager) WithTransaction(ctx context.Context, fn func(ctx context.Context) error) error {
	if !t.replicaSet {
		return fn(ctx)
	}
	sess, err := t.client.StartSession()
	if err != nil {
		return err
	}
	defer sess.EndSession(ctx)
	_, err = sess.WithTransaction(ctx, func(sc mongo.SessionContext) (interface{}, error) {
		return nil, fn(sc)
	})
	return err
}
