package store

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const handHistoryCollection = "hand_histories"

// MongoStore writes one document per hand into the configured database.
// Concurrent calls are safe: the underlying mongo.Client multiplexes I/O
// internally, and each SaveHand uses an Upsert keyed on hand_id so retries
// are idempotent.
type MongoStore struct {
	client *mongo.Client
	coll   *mongo.Collection
}

// NewMongoStore dials Mongo, ensures indexes, and returns a ready store.
// `ctx` is used only for the dial + index calls; the returned store keeps
// its own connection pool for the lifetime of the process. Close() must be
// called at shutdown.
func NewMongoStore(ctx context.Context, uri, dbName string) (*MongoStore, error) {
	if uri == "" {
		return nil, fmt.Errorf("mongo uri is empty")
	}
	if dbName == "" {
		return nil, fmt.Errorf("mongo db name is empty")
	}

	client, err := mongo.Connect(options.Client().ApplyURI(uri))
	if err != nil {
		return nil, fmt.Errorf("mongo connect: %w", err)
	}
	if err := client.Ping(ctx, nil); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, fmt.Errorf("mongo ping: %w", err)
	}

	coll := client.Database(dbName).Collection(handHistoryCollection)
	if err := ensureIndexes(ctx, coll); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, fmt.Errorf("mongo ensure indexes: %w", err)
	}

	return &MongoStore{client: client, coll: coll}, nil
}

func ensureIndexes(ctx context.Context, coll *mongo.Collection) error {
	models := []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "hand_id", Value: 1}},
			Options: options.Index().SetUnique(true).SetName("uniq_hand_id"),
		},
		{
			Keys:    bson.D{{Key: "table_id", Value: 1}, {Key: "ended_at", Value: -1}},
			Options: options.Index().SetName("table_ended"),
		},
	}
	_, err := coll.Indexes().CreateMany(ctx, models)
	return err
}

// SaveHand upserts the record by hand_id. Idempotent: replaying the same
// HandID overwrites the existing document with the latest payload.
func (s *MongoStore) SaveHand(ctx context.Context, rec *HandRecord) error {
	if rec == nil || rec.HandID == "" {
		return fmt.Errorf("invalid hand record")
	}
	filter := bson.D{{Key: "hand_id", Value: rec.HandID}}
	opts := options.Replace().SetUpsert(true)
	_, err := s.coll.ReplaceOne(ctx, filter, rec, opts)
	return err
}

func (s *MongoStore) Close(ctx context.Context) error {
	if s == nil || s.client == nil {
		return nil
	}
	return s.client.Disconnect(ctx)
}
