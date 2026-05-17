package store

import "context"

// NoopStore is the fallback used when no MONGO_URI is configured. It
// satisfies HandHistoryStore by silently dropping every record.
type NoopStore struct{}

func NewNoopStore() *NoopStore { return &NoopStore{} }

func (NoopStore) SaveHand(context.Context, *HandRecord) error { return nil }
func (NoopStore) Close(context.Context) error                 { return nil }
