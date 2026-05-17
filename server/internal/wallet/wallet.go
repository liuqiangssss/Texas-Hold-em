package wallet

import "context"

// Service is the wallet boundary. All implementations MUST be safe for
// concurrent use and MUST treat repeated Apply calls with the same
// IdempotencyKey as no-ops that return the first persisted result.
type Service interface {
	// EnsureAccount returns the account row, creating it at zero balance
	// on first call. Idempotent.
	EnsureAccount(ctx context.Context, userID string) (*Account, error)

	// GetBalance returns the current balance, creating a zero account on
	// first lookup. The auto-create behaviour is intentional: callers
	// should not have to special-case "first time we ever see this user".
	GetBalance(ctx context.Context, userID string) (int64, error)

	// Apply runs one debit/credit. The returned Account reflects the
	// post-apply balance. The Ledger is the entry that was written (or
	// the original entry, on idempotent replay).
	Apply(ctx context.Context, op Op) (*Account, *Ledger, error)

	// History returns the most-recent `limit` ledger entries for a user,
	// newest first.
	History(ctx context.Context, userID string, limit int) ([]Ledger, error)

	// Close releases any underlying resources (mongo client, etc).
	Close(ctx context.Context) error
}

// validateOp centralises the cheap pre-checks so MemoryWallet and
// MongoWallet share the same invariants.
func validateOp(op Op) error {
	if op.UserID == "" {
		return ErrInvalidOp
	}
	if op.Delta == 0 {
		return ErrInvalidOp
	}
	if op.IdempotencyKey == "" {
		return ErrInvalidOp
	}
	if !op.Reason.IsValid() {
		return ErrInvalidOp
	}
	return nil
}
