package table

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/liuqiangssss/texas-holdem/server/internal/proto"
	"github.com/liuqiangssss/texas-holdem/server/internal/store"
)

// fakeStore captures every SaveHand call. Concurrency-safe: persistHandRecord
// runs the save in its own goroutine, so tests synchronize via the chan.
type fakeStore struct {
	mu    sync.Mutex
	saved []*store.HandRecord
	ch    chan *store.HandRecord
}

func newFakeStore() *fakeStore {
	return &fakeStore{ch: make(chan *store.HandRecord, 8)}
}

func (f *fakeStore) SaveHand(_ context.Context, rec *store.HandRecord) error {
	f.mu.Lock()
	f.saved = append(f.saved, rec)
	f.mu.Unlock()
	f.ch <- rec
	return nil
}

func (f *fakeStore) Close(context.Context) error { return nil }

func (f *fakeStore) waitOne(t *testing.T) *store.HandRecord {
	t.Helper()
	select {
	case rec := <-f.ch:
		return rec
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for SaveHand")
	}
	return nil
}

// withFakeStore wires a fakeStore into a synthetic table so handhistory tests
// can drive hand lifecycles directly without spinning up the actor goroutine.
func withFakeStore(t *testing.T, n int) (*Table, []*captureSink, *fakeStore) {
	t.Helper()
	tbl, sinks := newSyntheticTable(t, n)
	fs := newFakeStore()
	tbl.store = fs
	return tbl, sinks, fs
}

func TestHandHistorySavedOnFoldOut(t *testing.T) {
	tbl, sinks, fs := withFakeStore(t, 3)
	h := startSyntheticHand(t, tbl)
	drainSinks(sinks)

	// 3-handed: button=0, SB=1, BB=2. UTG (seat 0) folds, SB folds → BB wins.
	tbl.handleAction(actionCmd{userID: tbl.seats[0].UserID, handID: h.id, action: proto.ActFold})
	tbl.handleAction(actionCmd{userID: tbl.seats[1].UserID, handID: h.id, action: proto.ActFold})

	rec := fs.waitOne(t)
	if rec.HandID != h.id {
		t.Errorf("hand_id = %q, want %q", rec.HandID, h.id)
	}
	if rec.Reason != "fold_out" {
		t.Errorf("reason = %q, want fold_out", rec.Reason)
	}
	if rec.TableID != tbl.ID {
		t.Errorf("table_id = %q, want %q", rec.TableID, tbl.ID)
	}
	if rec.Button != 0 {
		t.Errorf("button = %d, want 0", rec.Button)
	}
	if rec.Blinds != tbl.Blinds {
		t.Errorf("blinds = %v, want %v", rec.Blinds, tbl.Blinds)
	}
	if len(rec.Seats) != 3 {
		t.Fatalf("seats len = %d, want 3", len(rec.Seats))
	}
	// Hands record: 2 blinds posted + 2 folds = 4 timeline entries.
	if got := len(rec.Actions); got != 4 {
		t.Fatalf("actions len = %d, want 4 (sb,bb posts + 2 folds); got %+v", got, rec.Actions)
	}
	if rec.Actions[0].Action != "post_blind" || rec.Actions[0].Seat != 1 {
		t.Errorf("first action = %+v, want post_blind seat 1", rec.Actions[0])
	}
	if rec.Actions[1].Action != "post_blind" || rec.Actions[1].Seat != 2 {
		t.Errorf("second action = %+v, want post_blind seat 2", rec.Actions[1])
	}
	if rec.Actions[2].Action != "fold" || rec.Actions[2].Seat != 0 {
		t.Errorf("third action = %+v, want fold seat 0", rec.Actions[2])
	}
	if rec.Actions[3].Action != "fold" || rec.Actions[3].Seat != 1 {
		t.Errorf("fourth action = %+v, want fold seat 1", rec.Actions[3])
	}

	if len(rec.Pots) != 0 {
		t.Errorf("fold_out should have no Pots, got %+v", rec.Pots)
	}
	if len(rec.HoleCards) != 0 {
		t.Errorf("fold_out should have no revealed HoleCards, got %+v", rec.HoleCards)
	}
	if len(rec.Winners) != 1 {
		t.Fatalf("winners len = %d, want 1", len(rec.Winners))
	}
	if rec.Winners[0].Seat != 2 {
		t.Errorf("winner seat = %d, want 2", rec.Winners[0].Seat)
	}
	if rec.Winners[0].Amount != tbl.Blinds[0]+tbl.Blinds[1] {
		t.Errorf("winner amount = %d, want %d", rec.Winners[0].Amount, tbl.Blinds[0]+tbl.Blinds[1])
	}

	// Seat snapshots reflect SB/BB posts and starting stacks.
	for _, ss := range rec.Seats {
		if ss.StackIn != 1000 {
			t.Errorf("seat %d stack_in = %d, want 1000", ss.Seat, ss.StackIn)
		}
		switch ss.Seat {
		case 0:
			if !ss.IsButton {
				t.Errorf("seat 0 should be button")
			}
			if ss.PostedSB || ss.PostedBB {
				t.Errorf("seat 0 should post no blind, got %+v", ss)
			}
		case 1:
			if !ss.PostedSB {
				t.Errorf("seat 1 should be SB, got %+v", ss)
			}
		case 2:
			if !ss.PostedBB {
				t.Errorf("seat 2 should be BB, got %+v", ss)
			}
		}
	}

	if rec.StartedAt.IsZero() || rec.EndedAt.IsZero() {
		t.Errorf("timestamps not set: started=%v ended=%v", rec.StartedAt, rec.EndedAt)
	}
	if rec.EndedAt.Before(rec.StartedAt) {
		t.Errorf("ended_at before started_at")
	}
}

func TestHandHistorySavedOnShowdown(t *testing.T) {
	tbl, sinks, fs := withFakeStore(t, 2)
	h := startSyntheticHand(t, tbl)
	drainSinks(sinks)

	// Heads-up: button=SB=0, BB=1. SB(0) calls 5 to match BB. BB checks → flop.
	tbl.handleAction(actionCmd{userID: tbl.seats[0].UserID, handID: h.id, action: proto.ActCall})
	tbl.handleAction(actionCmd{userID: tbl.seats[1].UserID, handID: h.id, action: proto.ActCheck})

	// Streets are scheduled via streetDelay; tests step them directly.
	tbl.advanceStreet() // → flop
	tbl.handleAction(actionCmd{userID: tbl.seats[1].UserID, handID: h.id, action: proto.ActCheck})
	tbl.handleAction(actionCmd{userID: tbl.seats[0].UserID, handID: h.id, action: proto.ActCheck})

	tbl.advanceStreet() // → turn
	tbl.handleAction(actionCmd{userID: tbl.seats[1].UserID, handID: h.id, action: proto.ActCheck})
	tbl.handleAction(actionCmd{userID: tbl.seats[0].UserID, handID: h.id, action: proto.ActCheck})

	tbl.advanceStreet() // → river
	tbl.handleAction(actionCmd{userID: tbl.seats[1].UserID, handID: h.id, action: proto.ActCheck})
	tbl.handleAction(actionCmd{userID: tbl.seats[0].UserID, handID: h.id, action: proto.ActCheck})

	tbl.advanceStreet() // settles + persists

	rec := fs.waitOne(t)
	if rec.Reason != "showdown" {
		t.Errorf("reason = %q, want showdown", rec.Reason)
	}
	if len(rec.Community) != 5 {
		t.Errorf("community = %v, want 5 cards", rec.Community)
	}
	if len(rec.Pots) == 0 {
		t.Errorf("showdown record should have Pots")
	}
	if len(rec.Winners) == 0 {
		t.Errorf("showdown record should have Winners")
	}
	if len(rec.HoleCards) != 2 {
		t.Errorf("showdown record should have 2 revealed HoleCards, got %d", len(rec.HoleCards))
	}
	for seat, cards := range rec.HoleCards {
		if len(cards) != 2 {
			t.Errorf("seat %d revealed %d cards, want 2", seat, len(cards))
		}
	}

	// Timeline includes the 3 dealer events.
	dealEvents := 0
	for _, a := range rec.Actions {
		switch a.Action {
		case "deal_flop", "deal_turn", "deal_river":
			dealEvents++
			if a.Seat != -1 {
				t.Errorf("dealer event seat = %d, want -1", a.Seat)
			}
		}
	}
	if dealEvents != 3 {
		t.Errorf("dealer events = %d, want 3", dealEvents)
	}

	// Stack out non-zero for both seats (one seat won the pot).
	wonBy := -1
	for i, ss := range rec.Seats {
		if ss.StackOut > ss.StackIn {
			wonBy = i
		}
	}
	if wonBy < 0 {
		t.Errorf("expected at least one seat to grow stack at showdown, got %+v", rec.Seats)
	}
}

func TestNoopStoreDoesNotPanicOnHandEnd(t *testing.T) {
	// Default Manager builds a NoopStore — exercise the full fold-out path
	// to make sure the recording code is no-op-safe.
	tbl, sinks := newSyntheticTable(t, 3)
	tbl.store = store.NewNoopStore()
	h := startSyntheticHand(t, tbl)
	drainSinks(sinks)

	tbl.handleAction(actionCmd{userID: tbl.seats[0].UserID, handID: h.id, action: proto.ActFold})
	tbl.handleAction(actionCmd{userID: tbl.seats[1].UserID, handID: h.id, action: proto.ActFold})
}
