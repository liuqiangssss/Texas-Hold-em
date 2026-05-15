package table

import (
	"testing"
	"time"

	"github.com/liuqiangssss/texas-holdem/server/internal/proto"
)

// captureSink fans broadcast messages into a slice for assertions. The Send
// channel is generously sized so tests don't have to drain inline.
type captureSink struct {
	ch chan any
}

func newCaptureSink() *captureSink { return &captureSink{ch: make(chan any, 256)} }

func (cs *captureSink) drainTypes() []proto.MsgType {
	out := []proto.MsgType{}
	for {
		select {
		case msg := <-cs.ch:
			if env, ok := envelopeOf(msg); ok {
				out = append(out, env.Type)
			}
		default:
			return out
		}
	}
}

func envelopeOf(msg any) (proto.Envelope, bool) {
	switch m := msg.(type) {
	case proto.ToActMsg:
		return m.Envelope, true
	case proto.ActionApplied:
		return m.Envelope, true
	case proto.HandStart:
		return m.Envelope, true
	case proto.HandEnd:
		return m.Envelope, true
	case proto.DealHole:
		return m.Envelope, true
	case proto.DealCommunity:
		return m.Envelope, true
	case proto.PotUpdate:
		return m.Envelope, true
	case proto.Showdown:
		return m.Envelope, true
	case proto.TableState:
		return m.Envelope, true
	case proto.ErrorMsg:
		return m.Envelope, true
	}
	return proto.Envelope{}, false
}

// newSyntheticTable wires up a Table with `n` seated players but does NOT run
// the actor loop. Tests drive Table state via direct method calls so they
// don't have to coordinate goroutines or wait on real timers.
func newSyntheticTable(t *testing.T, n int) (*Table, []*captureSink) {
	t.Helper()
	tbl := New([2]int{5, 10})
	tbl.button = -1
	sinks := make([]*captureSink, n)
	for i := 0; i < n; i++ {
		s := newCaptureSink()
		sinks[i] = s
		tbl.seatPlayer(&Player{
			UserID:   string(rune('A' + i)),
			Nickname: string(rune('A' + i)),
			Stack:    1000,
			Send:     s.ch,
		})
	}
	return tbl, sinks
}

// startSyntheticHand runs the same prep as Table.startHand without the
// auto-scheduling. Returns the started hand for further direct manipulation.
func startSyntheticHand(t *testing.T, tbl *Table) *hand {
	t.Helper()
	tbl.startHand()
	if tbl.hand == nil {
		t.Fatal("hand did not start")
	}
	return tbl.hand
}

func TestTimeBankInitOnSeat(t *testing.T) {
	tbl, _ := newSyntheticTable(t, 2)
	for _, p := range tbl.seats[:2] {
		if p.TimeBankMs != timeBankInitMs {
			t.Errorf("seat %d bank = %d, want %d", p.Seat, p.TimeBankMs, timeBankInitMs)
		}
	}
}

func TestConsumeBankFor(t *testing.T) {
	tbl, _ := newSyntheticTable(t, 2)
	startSyntheticHand(t, tbl)

	seat := tbl.hand.toAct
	tbl.turnHandID = tbl.hand.id
	tbl.turnSeat = seat
	tbl.turnStartAt = time.Now().Add(-22 * time.Second) // 22s elapsed
	before := tbl.seats[seat].TimeBankMs

	tbl.consumeBankFor(seat, 22*time.Second)
	want := before - (22000 - baseTurnMs) // 22000 - 15000 = 7000
	if tbl.seats[seat].TimeBankMs != want {
		t.Errorf("bank after consume = %d, want %d", tbl.seats[seat].TimeBankMs, want)
	}
}

func TestConsumeBankForUnderBaseIsNoOp(t *testing.T) {
	tbl, _ := newSyntheticTable(t, 2)
	startSyntheticHand(t, tbl)

	seat := tbl.hand.toAct
	tbl.turnHandID = tbl.hand.id
	tbl.turnSeat = seat
	before := tbl.seats[seat].TimeBankMs

	tbl.consumeBankFor(seat, 5*time.Second)
	if tbl.seats[seat].TimeBankMs != before {
		t.Errorf("bank should be unchanged for action under base; got %d, want %d",
			tbl.seats[seat].TimeBankMs, before)
	}
}

func TestConsumeBankForFloorsAtZero(t *testing.T) {
	tbl, _ := newSyntheticTable(t, 2)
	startSyntheticHand(t, tbl)

	seat := tbl.hand.toAct
	tbl.turnHandID = tbl.hand.id
	tbl.turnSeat = seat
	tbl.seats[seat].TimeBankMs = 1000

	tbl.consumeBankFor(seat, 30*time.Second) // 30s - 15s = 15s used, only 1s available
	if tbl.seats[seat].TimeBankMs != 0 {
		t.Errorf("bank should floor at 0, got %d", tbl.seats[seat].TimeBankMs)
	}
}

func TestCleanupHandReplenishesBank(t *testing.T) {
	tbl, _ := newSyntheticTable(t, 2)
	startSyntheticHand(t, tbl)
	tbl.seats[0].TimeBankMs = 10_000
	tbl.seats[1].TimeBankMs = timeBankCapMs - 1000

	tbl.cleanupHand()

	if tbl.seats[0].TimeBankMs != 10_000+timeBankPerHandMs {
		t.Errorf("seat 0 bank after replenish = %d, want %d",
			tbl.seats[0].TimeBankMs, 10_000+timeBankPerHandMs)
	}
	if tbl.seats[1].TimeBankMs != timeBankCapMs {
		t.Errorf("seat 1 bank should cap at %d, got %d",
			timeBankCapMs, tbl.seats[1].TimeBankMs)
	}
}

// drainSinks empties pending broadcast queues so we can assert only what comes
// after a specific action.
func drainSinks(sinks []*captureSink) {
	for _, s := range sinks {
		s.drainTypes()
	}
}

// findActionApplied returns the latest action_applied for the given seat, or
// nil when none was broadcast.
func findActionApplied(sinks []*captureSink, seat int) *proto.ActionApplied {
	for _, s := range sinks {
		drained := []any{}
		for {
			select {
			case msg := <-s.ch:
				drained = append(drained, msg)
			default:
				goto done
			}
		}
	done:
		for _, msg := range drained {
			if a, ok := msg.(proto.ActionApplied); ok && a.Seat == seat {
				// Re-queue everything we drained for the next caller. (Tests
				// don't care about ordering preservation across callers; this
				// is only because we want a "last seen" semantic.)
				return &a
			}
		}
	}
	return nil
}

func TestForceTimeoutAutoChecks(t *testing.T) {
	tbl, sinks := newSyntheticTable(t, 3)
	h := startSyntheticHand(t, tbl)
	drainSinks(sinks)

	// Seat 0 (UTG) folds. SB calls. BB checks → preflop closed.
	tbl.handleAction(actionCmd{userID: tbl.seats[0].UserID, handID: h.id, action: proto.ActFold})
	tbl.handleAction(actionCmd{userID: tbl.seats[1].UserID, handID: h.id, action: proto.ActCall})
	tbl.handleAction(actionCmd{userID: tbl.seats[2].UserID, handID: h.id, action: proto.ActCheck})

	// Drain auto-broadcast scheduling — we can't run the timer-driven
	// advanceStreet, so step it directly.
	tbl.advanceStreet()
	drainSinks(sinks)

	// SB (seat 1) is first to act on flop with no bet. Force timeout → check.
	if h.toAct != 1 {
		t.Fatalf("toAct on flop = %d, want 1", h.toAct)
	}
	tbl.handleTurnTimeout(turnTimeoutCmd{handID: h.id, seat: 1})
	if applied := findActionApplied(sinks, 1); applied == nil || applied.Action != proto.ActCheck {
		var got proto.ActionType
		if applied != nil {
			got = applied.Action
		}
		t.Errorf("auto action on timeout = %v, want check", got)
	}
	if tbl.seats[1].TimeBankMs != 0 {
		t.Errorf("bank after timeout = %d, want 0", tbl.seats[1].TimeBankMs)
	}
}

func TestForceTimeoutAutoFoldsFacingBet(t *testing.T) {
	tbl, sinks := newSyntheticTable(t, 3)
	h := startSyntheticHand(t, tbl)
	drainSinks(sinks)

	// 3-handed: UTG = button = seat 0. UTG raises → SB faces a bet.
	tbl.handleAction(actionCmd{userID: tbl.seats[0].UserID, handID: h.id, action: proto.ActRaise, amount: 30})
	if h.toAct != 1 {
		t.Fatalf("toAct after UTG raise = %d, want 1", h.toAct)
	}
	tbl.handleTurnTimeout(turnTimeoutCmd{handID: h.id, seat: 1})

	if applied := findActionApplied(sinks, 1); applied == nil || applied.Action != proto.ActFold {
		var got proto.ActionType
		if applied != nil {
			got = applied.Action
		}
		t.Errorf("auto action on timeout facing bet = %v, want fold", got)
	}
}

func TestForceTimeoutStaleHandIDIgnored(t *testing.T) {
	tbl, sinks := newSyntheticTable(t, 3)
	startSyntheticHand(t, tbl)
	drainSinks(sinks)

	// Stale hand id — must not mutate state.
	before := *tbl.hand
	tbl.handleTurnTimeout(turnTimeoutCmd{handID: "ghost", seat: tbl.hand.toAct})
	if tbl.hand.toAct != before.toAct {
		t.Error("stale timeout cmd mutated state")
	}
}

func TestPreActionFiresImmediatelyWhenItsYourTurn(t *testing.T) {
	tbl, sinks := newSyntheticTable(t, 3)
	h := startSyntheticHand(t, tbl)
	drainSinks(sinks)

	// UTG (seat 0) sends pre_call_any when it's already their turn → resolves
	// to call immediately.
	tbl.handlePreAction(preActionCmd{
		userID: tbl.seats[0].UserID,
		handID: h.id,
		action: proto.ActPreCallAny,
	})
	if applied := findActionApplied(sinks, 0); applied == nil || applied.Action != proto.ActCall {
		var got proto.ActionType
		if applied != nil {
			got = applied.Action
		}
		t.Errorf("pre_call_any when toAct should auto-call; got %v", got)
	}
}

func TestPreActionChainsThroughMultipleSeats(t *testing.T) {
	tbl, sinks := newSyntheticTable(t, 3)
	h := startSyntheticHand(t, tbl)
	drainSinks(sinks)

	// Seats 1 (SB) and 2 (BB) arm pre_call_any. UTG calls → both pre-actions
	// auto-resolve in order, closing the round.
	tbl.handlePreAction(preActionCmd{userID: tbl.seats[1].UserID, handID: h.id, action: proto.ActPreCallAny})
	tbl.handlePreAction(preActionCmd{userID: tbl.seats[2].UserID, handID: h.id, action: proto.ActPreCallAny})
	tbl.handleAction(actionCmd{userID: tbl.seats[0].UserID, handID: h.id, action: proto.ActCall})

	// All three seats should have committed BB worth of chips. (Seat 0 calls
	// 10, seat 1 already had SB blind 5 + 5 to call, seat 2 already had BB
	// 10.)
	for i := 0; i < 3; i++ {
		if h.seats[i].bet != 10 {
			t.Errorf("seat %d bet = %d, want 10", i, h.seats[i].bet)
		}
	}
	if !h.roundClosed() {
		t.Error("round should be closed after pre-actions chain through")
	}
}
