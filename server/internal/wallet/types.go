// Package wallet implements the gold-coin account: balance, ledger, and
// idempotent debit/credit. It is the single write boundary for any chip
// movement that should outlive a single hand (buy-in, settle, daily gift,
// admin adjust, etc).
//
// Design notes:
//   - Amount unit is a single gold coin, stored as int64 (no decimals).
//   - Apply is the only mutation entry point; every Op MUST carry an
//     idempotency key. Replays return the first persisted result.
//   - Concurrency is handled by optimistic CAS on Account.Version; the
//     interface intentionally hides the retry loop.
package wallet

import (
	"errors"
	"time"
)

// Reason classifies a ledger entry. The set is closed: callers must use
// one of the constants below so the back office can aggregate cleanly.
type Reason string

const (
	ReasonBuyIn       Reason = "buy_in"        // chips moving INTO a table
	ReasonCashOut     Reason = "cash_out"      // chips returning FROM a table
	ReasonSettleWin   Reason = "settle_win"    // hand result, winner side
	ReasonSettleLoss  Reason = "settle_loss"   // hand result, loser side
	ReasonRake        Reason = "rake"          // house take
	ReasonDailyGift   Reason = "daily_gift"    // S4.2 placeholder
	ReasonRelief      Reason = "relief"        // S4.3 placeholder
	ReasonAdjustIn    Reason = "adjust_in"     // ops manual credit
	ReasonAdjustOut   Reason = "adjust_out"    // ops manual debit
)

// IsValid reports whether r is one of the recognised reasons.
func (r Reason) IsValid() bool {
	switch r {
	case ReasonBuyIn, ReasonCashOut, ReasonSettleWin, ReasonSettleLoss,
		ReasonRake, ReasonDailyGift, ReasonRelief, ReasonAdjustIn, ReasonAdjustOut:
		return true
	}
	return false
}

// Account is the per-user balance row. Version is the CAS guard.
type Account struct {
	UserID    string    `bson:"user_id" json:"user_id"`
	Balance   int64     `bson:"balance" json:"balance"`
	Version   int64     `bson:"version" json:"version"`
	CreatedAt time.Time `bson:"created_at" json:"created_at"`
	UpdatedAt time.Time `bson:"updated_at" json:"updated_at"`
}

// Ledger is one immutable money-movement record. BalanceAfter snapshots
// the post-apply balance so we can render history without re-summing.
type Ledger struct {
	IdempotencyKey string    `bson:"idempotency_key" json:"idempotency_key"`
	UserID         string    `bson:"user_id" json:"user_id"`
	Delta          int64     `bson:"delta" json:"delta"`
	BalanceAfter   int64     `bson:"balance_after" json:"balance_after"`
	Reason         Reason    `bson:"reason" json:"reason"`
	RefID          string    `bson:"ref_id,omitempty" json:"ref_id,omitempty"`
	Timestamp      time.Time `bson:"ts" json:"ts"`
}

// Op is a single chip-movement request. Delta>0 credits the account,
// Delta<0 debits. Delta==0 is rejected.
type Op struct {
	UserID         string
	Delta          int64
	Reason         Reason
	RefID          string
	IdempotencyKey string
}

// Errors returned by Service.
var (
	ErrInvalidOp       = errors.New("wallet: invalid op")
	ErrInsufficient    = errors.New("wallet: insufficient balance")
	ErrUserNotFound    = errors.New("wallet: user not found")
	ErrConflict        = errors.New("wallet: cas conflict, retries exhausted")
	ErrIdempotentReplay = errors.New("wallet: idempotent replay") // sentinel; never surfaced — Apply returns the first result instead
)
