package wallet

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
)

func mustEnsure(t *testing.T, w Service, uid string, deposit int64) {
	t.Helper()
	if deposit == 0 {
		if _, err := w.EnsureAccount(context.Background(), uid); err != nil {
			t.Fatalf("ensure: %v", err)
		}
		return
	}
	_, _, err := w.Apply(context.Background(), Op{
		UserID:         uid,
		Delta:          deposit,
		Reason:         ReasonAdjustIn,
		IdempotencyKey: fmt.Sprintf("seed-%s-%d", uid, deposit),
	})
	if err != nil {
		t.Fatalf("seed deposit: %v", err)
	}
}

// TestApply_TableDriven covers the common request-level scenarios that
// MemoryWallet must reject or accept. Each case is independent: a fresh
// wallet is created per case so seeded balances do not bleed across.
func TestApply_TableDriven(t *testing.T) {
	cases := []struct {
		name    string
		seed    int64
		op      Op
		wantErr error
		wantBal int64
	}{
		{
			name:    "zero delta rejected",
			op:      Op{UserID: "u1", Delta: 0, Reason: ReasonAdjustIn, IdempotencyKey: "k"},
			wantErr: ErrInvalidOp,
		},
		{
			name:    "missing idempotency key rejected",
			op:      Op{UserID: "u1", Delta: 100, Reason: ReasonAdjustIn},
			wantErr: ErrInvalidOp,
		},
		{
			name:    "missing user id rejected",
			op:      Op{UserID: "", Delta: 100, Reason: ReasonAdjustIn, IdempotencyKey: "k"},
			wantErr: ErrInvalidOp,
		},
		{
			name:    "unknown reason rejected",
			op:      Op{UserID: "u1", Delta: 100, Reason: Reason("garbage"), IdempotencyKey: "k"},
			wantErr: ErrInvalidOp,
		},
		{
			name:    "credit on empty account",
			op:      Op{UserID: "u1", Delta: 100, Reason: ReasonAdjustIn, IdempotencyKey: "k1"},
			wantBal: 100,
		},
		{
			name:    "debit fails when balance insufficient",
			op:      Op{UserID: "u1", Delta: -10, Reason: ReasonAdjustOut, IdempotencyKey: "k1"},
			wantErr: ErrInsufficient,
		},
		{
			name:    "debit succeeds at exactly zero",
			seed:    50,
			op:      Op{UserID: "u1", Delta: -50, Reason: ReasonAdjustOut, IdempotencyKey: "k2"},
			wantBal: 0,
		},
		{
			name:    "settle_win is a valid reason",
			op:      Op{UserID: "u1", Delta: 200, Reason: ReasonSettleWin, IdempotencyKey: "k3", RefID: "hand-1"},
			wantBal: 200,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := NewMemoryWallet()
			if c.seed > 0 {
				mustEnsure(t, w, c.op.UserID, c.seed)
			}
			_, _, err := w.Apply(context.Background(), c.op)
			if !errors.Is(err, c.wantErr) {
				t.Fatalf("err = %v, want %v", err, c.wantErr)
			}
			if c.wantErr != nil {
				return
			}
			bal, _ := w.GetBalance(context.Background(), c.op.UserID)
			if bal != c.wantBal {
				t.Fatalf("balance = %d, want %d", bal, c.wantBal)
			}
		})
	}
}

// TestApply_IdempotentReplay verifies that a repeated Apply with the
// same key is a no-op that returns the original ledger entry, not an
// error. This is the core contract that lets clients retry safely.
func TestApply_IdempotentReplay(t *testing.T) {
	w := NewMemoryWallet()
	op := Op{UserID: "u1", Delta: 100, Reason: ReasonBuyIn, IdempotencyKey: "buy-1"}

	a1, l1, err := w.Apply(context.Background(), op)
	if err != nil {
		t.Fatalf("first apply: %v", err)
	}
	if a1.Balance != 100 {
		t.Fatalf("balance = %d, want 100", a1.Balance)
	}

	a2, l2, err := w.Apply(context.Background(), op)
	if err != nil {
		t.Fatalf("replay apply: %v", err)
	}
	if a2.Balance != 100 {
		t.Fatalf("after replay balance = %d, want 100 (no double credit)", a2.Balance)
	}
	if l1.IdempotencyKey != l2.IdempotencyKey || l1.BalanceAfter != l2.BalanceAfter {
		t.Fatalf("replay ledger differs: %+v vs %+v", l1, l2)
	}

	hist, _ := w.History(context.Background(), "u1", 10)
	if len(hist) != 1 {
		t.Fatalf("history len = %d, want 1 (replay must not add a row)", len(hist))
	}
}

// TestApply_Concurrent runs N goroutines each subtracting one coin from
// a seeded balance. A correctly mutex-guarded implementation must end
// with balance = seed - N and ledger length = N. We also assert that
// BalanceAfter values are unique (no two ledgers see the same post-bal).
func TestApply_Concurrent(t *testing.T) {
	const N = 1000
	w := NewMemoryWallet()
	mustEnsure(t, w, "u1", N)

	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func(i int) {
			defer wg.Done()
			_, _, err := w.Apply(context.Background(), Op{
				UserID:         "u1",
				Delta:          -1,
				Reason:         ReasonSettleLoss,
				IdempotencyKey: fmt.Sprintf("loss-%d", i),
			})
			if err != nil {
				t.Errorf("apply %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()

	bal, _ := w.GetBalance(context.Background(), "u1")
	if bal != 0 {
		t.Fatalf("final balance = %d, want 0 (concurrent races leaked)", bal)
	}
	hist, _ := w.History(context.Background(), "u1", N+10)
	if len(hist) != N+1 { // +1 for the seed deposit
		t.Fatalf("history len = %d, want %d", len(hist), N+1)
	}
	seen := map[int64]struct{}{}
	for _, h := range hist {
		if h.Delta != -1 {
			continue
		}
		if _, dup := seen[h.BalanceAfter]; dup {
			t.Fatalf("duplicate balance_after %d in ledger — lock missing", h.BalanceAfter)
		}
		seen[h.BalanceAfter] = struct{}{}
	}
}

// TestApply_ConcurrentSameKey simulates two clients firing the same
// idempotent debit at the same instant. Exactly one ledger row must be
// written; both callers must see the same BalanceAfter.
func TestApply_ConcurrentSameKey(t *testing.T) {
	w := NewMemoryWallet()
	mustEnsure(t, w, "u1", 100)

	var wg sync.WaitGroup
	results := make([]int64, 2)
	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func(i int) {
			defer wg.Done()
			_, l, err := w.Apply(context.Background(), Op{
				UserID:         "u1",
				Delta:          -50,
				Reason:         ReasonBuyIn,
				IdempotencyKey: "same-key",
			})
			if err != nil {
				t.Errorf("apply: %v", err)
				return
			}
			results[i] = l.BalanceAfter
		}(i)
	}
	wg.Wait()

	if results[0] != results[1] {
		t.Fatalf("idempotent racers saw different ledger snapshots: %d vs %d", results[0], results[1])
	}
	bal, _ := w.GetBalance(context.Background(), "u1")
	if bal != 50 {
		t.Fatalf("balance = %d, want 50 (must be debited exactly once)", bal)
	}
}

// TestEnsureAccount_Idempotent verifies repeated EnsureAccount calls
// don't reset the version or balance.
func TestEnsureAccount_Idempotent(t *testing.T) {
	w := NewMemoryWallet()
	a1, _ := w.EnsureAccount(context.Background(), "u1")
	if a1.Balance != 0 || a1.Version != 0 {
		t.Fatalf("fresh account = %+v", a1)
	}
	_, _, _ = w.Apply(context.Background(), Op{
		UserID: "u1", Delta: 100, Reason: ReasonAdjustIn, IdempotencyKey: "k",
	})
	a2, _ := w.EnsureAccount(context.Background(), "u1")
	if a2.Balance != 100 || a2.Version != 1 {
		t.Fatalf("after credit account = %+v, want bal=100 ver=1", a2)
	}
}

// TestHistory_NewestFirst confirms ordering & limit truncation.
func TestHistory_NewestFirst(t *testing.T) {
	w := NewMemoryWallet()
	for i := 0; i < 5; i++ {
		_, _, err := w.Apply(context.Background(), Op{
			UserID:         "u1",
			Delta:          int64(10 * (i + 1)),
			Reason:         ReasonAdjustIn,
			IdempotencyKey: fmt.Sprintf("k-%d", i),
		})
		if err != nil {
			t.Fatalf("apply %d: %v", i, err)
		}
	}
	hist, _ := w.History(context.Background(), "u1", 3)
	if len(hist) != 3 {
		t.Fatalf("history len = %d, want 3", len(hist))
	}
	for i := 1; i < len(hist); i++ {
		if hist[i-1].Timestamp.Before(hist[i].Timestamp) {
			t.Fatalf("history not newest-first at index %d", i)
		}
	}
}

// TestReason_IsValid is the static surface check for the Reason enum.
func TestReason_IsValid(t *testing.T) {
	good := []Reason{
		ReasonBuyIn, ReasonCashOut, ReasonSettleWin, ReasonSettleLoss,
		ReasonRake, ReasonDailyGift, ReasonRelief, ReasonAdjustIn, ReasonAdjustOut,
	}
	for _, r := range good {
		if !r.IsValid() {
			t.Fatalf("reason %q should be valid", r)
		}
	}
	if Reason("nope").IsValid() {
		t.Fatalf("garbage reason wrongly valid")
	}
}
