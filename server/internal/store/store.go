// Package store persists per-hand history records for later replay,
// audit, and back-office tooling. The package is deliberately decoupled
// from the table actor: callers feed it complete HandRecord values; how
// those are stored (Mongo, noop, future Postgres etc.) is opaque.
package store

import (
	"context"
	"time"
)

// SeatSnap is a frozen view of one seat at the start of a hand: who was
// sitting there, and what their stack was after blinds had been posted.
type SeatSnap struct {
	Seat      int    `bson:"seat" json:"seat"`
	UserID    string `bson:"user_id" json:"user_id"`
	Nickname  string `bson:"nickname" json:"nickname"`
	StackIn   int    `bson:"stack_in" json:"stack_in"`   // chips at hand start (pre-blind)
	StackOut  int    `bson:"stack_out" json:"stack_out"` // chips at hand end (post-settle)
	IsButton  bool   `bson:"is_button,omitempty" json:"is_button,omitempty"`
	PostedSB  bool   `bson:"posted_sb,omitempty" json:"posted_sb,omitempty"`
	PostedBB  bool   `bson:"posted_bb,omitempty" json:"posted_bb,omitempty"`
	DeadBB    bool   `bson:"dead_bb,omitempty" json:"dead_bb,omitempty"`
}

// ActionRec is a single timeline entry in a hand: a betting action OR a
// dealer event ("deal_flop", "deal_turn", "deal_river"). For dealer events
// Seat is -1 and Cards carries the dealt board cards.
type ActionRec struct {
	Stage  string   `bson:"stage" json:"stage"`            // preflop|flop|turn|river|post
	Seat   int      `bson:"seat" json:"seat"`              // -1 for dealer events
	Action string   `bson:"action" json:"action"`          // post_blind|fold|check|call|bet|raise|all_in|deal_flop|deal_turn|deal_river
	Amount int      `bson:"amount,omitempty" json:"amount,omitempty"`
	Cards  []string `bson:"cards,omitempty" json:"cards,omitempty"`
}

// PotRec is a pot layer at showdown.
type PotRec struct {
	Amount   int   `bson:"amount" json:"amount"`
	Eligible []int `bson:"eligible" json:"eligible"`
}

// WinnerRec is one award out of one pot. A pot may produce multiple winners
// when there is a tie; in that case each tied seat gets their own WinnerRec
// with the split share already computed.
type WinnerRec struct {
	Seat     int      `bson:"seat" json:"seat"`
	Amount   int      `bson:"amount" json:"amount"`
	HandRank string   `bson:"hand_rank,omitempty" json:"hand_rank,omitempty"`
	Best5    []string `bson:"best5,omitempty" json:"best5,omitempty"`
}

// HandRecord is the complete, self-contained snapshot of one hand.
// One document per hand in the underlying store. HandID is the unique key.
type HandRecord struct {
	HandID    string    `bson:"hand_id" json:"hand_id"`
	TableID   string    `bson:"table_id" json:"table_id"`
	Blinds    [2]int    `bson:"blinds" json:"blinds"`
	Button    int       `bson:"button" json:"button"`
	StartedAt time.Time `bson:"started_at" json:"started_at"`
	EndedAt   time.Time `bson:"ended_at" json:"ended_at"`

	Seats     []SeatSnap       `bson:"seats" json:"seats"`
	Community []string         `bson:"community,omitempty" json:"community,omitempty"`
	HoleCards map[int][]string `bson:"hole_cards,omitempty" json:"hole_cards,omitempty"`
	Actions   []ActionRec      `bson:"actions" json:"actions"`
	Pots      []PotRec         `bson:"pots,omitempty" json:"pots,omitempty"`
	Winners   []WinnerRec      `bson:"winners,omitempty" json:"winners,omitempty"`
	Reason    string           `bson:"reason" json:"reason"` // "showdown" | "fold_out"
}

// HandHistoryStore is the persistence boundary. SaveHand must be safe to
// call from multiple goroutines and must respect the supplied context for
// timeouts. Implementations should treat duplicate writes (same HandID) as
// idempotent.
type HandHistoryStore interface {
	SaveHand(ctx context.Context, rec *HandRecord) error
	Close(ctx context.Context) error
}
