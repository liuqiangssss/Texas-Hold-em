package wallet

import (
	"context"
	"sort"
	"sync"
	"time"
)

// MemoryWallet is the in-process reference implementation. It is the
// fallback when MONGO_URI is unset, and the substrate that unit tests
// run against. All operations take a single mutex; that is fine because
// MVP traffic is bounded and the critical section is tiny.
type MemoryWallet struct {
	mu       sync.Mutex
	now      func() time.Time
	accounts map[string]*Account
	ledgers  map[string][]Ledger // user_id -> ledger entries (append-only)
	idem     map[string]Ledger   // idempotency_key -> ledger snapshot
}

// NewMemoryWallet returns an empty in-memory wallet using the system
// clock. Tests can swap the clock via the unexported now field.
func NewMemoryWallet() *MemoryWallet {
	return &MemoryWallet{
		now:      time.Now,
		accounts: map[string]*Account{},
		ledgers:  map[string][]Ledger{},
		idem:     map[string]Ledger{},
	}
}

func (m *MemoryWallet) EnsureAccount(_ context.Context, userID string) (*Account, error) {
	if userID == "" {
		return nil, ErrInvalidOp
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ensureLocked(userID), nil
}

func (m *MemoryWallet) GetBalance(_ context.Context, userID string) (int64, error) {
	if userID == "" {
		return 0, ErrInvalidOp
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ensureLocked(userID).Balance, nil
}

func (m *MemoryWallet) Apply(_ context.Context, op Op) (*Account, *Ledger, error) {
	if err := validateOp(op); err != nil {
		return nil, nil, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if prev, ok := m.idem[op.IdempotencyKey]; ok {
		acct := m.ensureLocked(op.UserID)
		ledgerCopy := prev
		acctCopy := *acct
		return &acctCopy, &ledgerCopy, nil
	}

	acct := m.ensureLocked(op.UserID)
	if op.Delta < 0 && acct.Balance+op.Delta < 0 {
		return nil, nil, ErrInsufficient
	}

	acct.Balance += op.Delta
	acct.Version++
	acct.UpdatedAt = m.now()

	led := Ledger{
		IdempotencyKey: op.IdempotencyKey,
		UserID:         op.UserID,
		Delta:          op.Delta,
		BalanceAfter:   acct.Balance,
		Reason:         op.Reason,
		RefID:          op.RefID,
		Timestamp:      acct.UpdatedAt,
	}
	m.ledgers[op.UserID] = append(m.ledgers[op.UserID], led)
	m.idem[op.IdempotencyKey] = led

	acctCopy := *acct
	return &acctCopy, &led, nil
}

func (m *MemoryWallet) History(_ context.Context, userID string, limit int) ([]Ledger, error) {
	if userID == "" {
		return nil, ErrInvalidOp
	}
	if limit <= 0 {
		limit = 50
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	all := m.ledgers[userID]
	out := make([]Ledger, len(all))
	copy(out, all)
	sort.SliceStable(out, func(i, j int) bool { return out[i].Timestamp.After(out[j].Timestamp) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemoryWallet) Close(_ context.Context) error { return nil }

// ensureLocked must be called with mu held; it returns the live pointer
// so Apply can mutate balance/version in place.
func (m *MemoryWallet) ensureLocked(userID string) *Account {
	if a, ok := m.accounts[userID]; ok {
		return a
	}
	now := m.now()
	a := &Account{
		UserID:    userID,
		Balance:   0,
		Version:   0,
		CreatedAt: now,
		UpdatedAt: now,
	}
	m.accounts[userID] = a
	return a
}
