package wallet

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

const (
	accountsCollection = "wallet_accounts"
	ledgerCollection   = "wallet_ledger"
	casMaxRetries      = 8
)

// MongoWallet stores accounts and ledger entries in two collections of
// the configured database. Concurrency safety relies on:
//   - Optimistic CAS via the {user_id, version} filter on accounts.
//   - The ledger's unique index on idempotency_key, which detects
//     concurrent identical Apply calls and lets us return the first one.
type MongoWallet struct {
	client   *mongo.Client
	accounts *mongo.Collection
	ledger   *mongo.Collection
	now      func() time.Time
}

// NewMongoWallet dials Mongo, ensures indexes, and returns a ready
// service. The supplied ctx covers dial + index creation only; the
// service keeps its own pool for the process lifetime.
func NewMongoWallet(ctx context.Context, uri, dbName string) (*MongoWallet, error) {
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
	db := client.Database(dbName)
	w := &MongoWallet{
		client:   client,
		accounts: db.Collection(accountsCollection),
		ledger:   db.Collection(ledgerCollection),
		now:      time.Now,
	}
	if err := w.ensureIndexes(ctx); err != nil {
		_ = client.Disconnect(context.Background())
		return nil, fmt.Errorf("wallet ensure indexes: %w", err)
	}
	return w, nil
}

func (w *MongoWallet) ensureIndexes(ctx context.Context) error {
	if _, err := w.accounts.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "user_id", Value: 1}},
		Options: options.Index().SetUnique(true).SetName("uniq_user_id"),
	}); err != nil {
		return err
	}
	_, err := w.ledger.Indexes().CreateMany(ctx, []mongo.IndexModel{
		{
			Keys:    bson.D{{Key: "idempotency_key", Value: 1}},
			Options: options.Index().SetUnique(true).SetName("uniq_idem_key"),
		},
		{
			Keys:    bson.D{{Key: "user_id", Value: 1}, {Key: "ts", Value: -1}},
			Options: options.Index().SetName("user_ts"),
		},
	})
	return err
}

func (w *MongoWallet) EnsureAccount(ctx context.Context, userID string) (*Account, error) {
	if userID == "" {
		return nil, ErrInvalidOp
	}
	now := w.now()
	filter := bson.D{{Key: "user_id", Value: userID}}
	update := bson.D{
		{Key: "$setOnInsert", Value: bson.D{
			{Key: "user_id", Value: userID},
			{Key: "balance", Value: int64(0)},
			{Key: "version", Value: int64(0)},
			{Key: "created_at", Value: now},
			{Key: "updated_at", Value: now},
		}},
	}
	opts := options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After)
	var acct Account
	if err := w.accounts.FindOneAndUpdate(ctx, filter, update, opts).Decode(&acct); err != nil {
		return nil, fmt.Errorf("ensure account: %w", err)
	}
	return &acct, nil
}

func (w *MongoWallet) GetBalance(ctx context.Context, userID string) (int64, error) {
	a, err := w.EnsureAccount(ctx, userID)
	if err != nil {
		return 0, err
	}
	return a.Balance, nil
}

// Apply is the only mutation entry point. The flow:
//  1. Idempotency probe: if a ledger entry already exists for this
//     IdempotencyKey, return the matching account snapshot + that entry.
//  2. CAS loop: load account by user_id, validate sufficiency, attempt
//     `findOneAndUpdate` filtered on (user_id, version). On a conflict
//     (someone else moved the version), retry up to casMaxRetries.
//  3. Insert the ledger entry. The unique index on idempotency_key
//     catches the race where two concurrent identical Apply calls both
//     pass the probe; the loser reads the winner's row and returns it.
func (w *MongoWallet) Apply(ctx context.Context, op Op) (*Account, *Ledger, error) {
	if err := validateOp(op); err != nil {
		return nil, nil, err
	}

	if existing, err := w.findLedgerByIdem(ctx, op.IdempotencyKey); err != nil {
		return nil, nil, err
	} else if existing != nil {
		acct, err := w.EnsureAccount(ctx, op.UserID)
		if err != nil {
			return nil, nil, err
		}
		return acct, existing, nil
	}

	if _, err := w.EnsureAccount(ctx, op.UserID); err != nil {
		return nil, nil, err
	}

	for attempt := 0; attempt < casMaxRetries; attempt++ {
		var current Account
		if err := w.accounts.FindOne(ctx, bson.D{{Key: "user_id", Value: op.UserID}}).Decode(&current); err != nil {
			return nil, nil, fmt.Errorf("load account: %w", err)
		}
		if op.Delta < 0 && current.Balance+op.Delta < 0 {
			return nil, nil, ErrInsufficient
		}

		now := w.now()
		newBalance := current.Balance + op.Delta
		newVersion := current.Version + 1

		filter := bson.D{
			{Key: "user_id", Value: op.UserID},
			{Key: "version", Value: current.Version},
		}
		update := bson.D{
			{Key: "$set", Value: bson.D{
				{Key: "balance", Value: newBalance},
				{Key: "version", Value: newVersion},
				{Key: "updated_at", Value: now},
			}},
		}
		opts := options.FindOneAndUpdate().SetReturnDocument(options.After)
		var updated Account
		err := w.accounts.FindOneAndUpdate(ctx, filter, update, opts).Decode(&updated)
		if errors.Is(err, mongo.ErrNoDocuments) {
			continue // version drifted; retry
		}
		if err != nil {
			return nil, nil, fmt.Errorf("cas update: %w", err)
		}

		led := Ledger{
			IdempotencyKey: op.IdempotencyKey,
			UserID:         op.UserID,
			Delta:          op.Delta,
			BalanceAfter:   updated.Balance,
			Reason:         op.Reason,
			RefID:          op.RefID,
			Timestamp:      now,
		}
		if _, err := w.ledger.InsertOne(ctx, led); err != nil {
			if mongo.IsDuplicateKeyError(err) {
				existing, qerr := w.findLedgerByIdem(ctx, op.IdempotencyKey)
				if qerr != nil {
					return nil, nil, qerr
				}
				if existing == nil {
					return nil, nil, fmt.Errorf("ledger duplicate but row missing")
				}
				w.compensate(ctx, op.UserID, -op.Delta, now)
				acct, aerr := w.EnsureAccount(ctx, op.UserID)
				if aerr != nil {
					return nil, nil, aerr
				}
				return acct, existing, nil
			}
			w.compensate(ctx, op.UserID, -op.Delta, now)
			return nil, nil, fmt.Errorf("ledger insert: %w", err)
		}
		return &updated, &led, nil
	}
	return nil, nil, ErrConflict
}

// compensate undoes a balance move when the ledger insert fails after a
// successful CAS update. Best-effort: errors are dropped because the
// caller has already received a hard failure.
func (w *MongoWallet) compensate(ctx context.Context, userID string, delta int64, ts time.Time) {
	_, _ = w.accounts.UpdateOne(ctx,
		bson.D{{Key: "user_id", Value: userID}},
		bson.D{
			{Key: "$inc", Value: bson.D{{Key: "balance", Value: delta}, {Key: "version", Value: int64(1)}}},
			{Key: "$set", Value: bson.D{{Key: "updated_at", Value: ts}}},
		},
	)
}

func (w *MongoWallet) findLedgerByIdem(ctx context.Context, key string) (*Ledger, error) {
	var led Ledger
	err := w.ledger.FindOne(ctx, bson.D{{Key: "idempotency_key", Value: key}}).Decode(&led)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("idem lookup: %w", err)
	}
	return &led, nil
}

func (w *MongoWallet) History(ctx context.Context, userID string, limit int) ([]Ledger, error) {
	if userID == "" {
		return nil, ErrInvalidOp
	}
	if limit <= 0 {
		limit = 50
	}
	opts := options.Find().SetSort(bson.D{{Key: "ts", Value: -1}}).SetLimit(int64(limit))
	cur, err := w.ledger.Find(ctx, bson.D{{Key: "user_id", Value: userID}}, opts)
	if err != nil {
		return nil, fmt.Errorf("history find: %w", err)
	}
	defer cur.Close(ctx)
	var out []Ledger
	if err := cur.All(ctx, &out); err != nil {
		return nil, fmt.Errorf("history decode: %w", err)
	}
	return out, nil
}

func (w *MongoWallet) Close(ctx context.Context) error {
	if w == nil || w.client == nil {
		return nil
	}
	return w.client.Disconnect(ctx)
}
