package table

import (
	"testing"

	"github.com/liuqiangssss/texas-holdem/server/internal/proto"
)

// fixed deck factory: builds a deterministic deck where the first cards are
// dealt to early seats. Caller controls everything — useful for unit tests.
func fixedDeck(extra ...string) []string {
	base := []string{
		"As", "Ks", // seat after-button hole 0
		"Ad", "Kd", // next seat hole 0
		"Ah", "Kh", // 3rd seat hole 0
		"Ac", "Kc", // 4th seat hole 0
		"2c", "3c", // ...
		"4c", "5c",
		"6c", "7c", "8c", // flop
		"9c",       // turn
		"Tc",       // river
	}
	return append(append([]string{}, base...), extra...)
}

func newSeatedTable(t *testing.T, n int) [MaxSeats]*Player {
	t.Helper()
	if n < 2 || n > MaxSeats {
		t.Fatalf("bad n=%d", n)
	}
	out := [MaxSeats]*Player{}
	for i := 0; i < n; i++ {
		out[i] = &Player{
			UserID:   "u" + string(rune('0'+i)),
			Nickname: "P" + string(rune('0'+i)),
			Stack:    1000,
		}
	}
	return out
}

func TestPreflopBlindsAndFirstAct(t *testing.T) {
	players := newSeatedTable(t, 3)
	h, err := newHand("h1", 0 /* button */, [2]int{5, 10}, fixedDeck(), players)
	if err != nil {
		t.Fatal(err)
	}
	h.startPreflop()

	// 3-handed: button=0, SB=1, BB=2, UTG (first to act preflop) = seat 0.
	if h.toAct != 0 {
		t.Errorf("first to act = %d, want 0 (button in 3-handed)", h.toAct)
	}
	if h.seats[1].bet != 5 || h.seats[2].bet != 10 {
		t.Errorf("blinds wrong: SB=%d BB=%d", h.seats[1].bet, h.seats[2].bet)
	}
	if h.currentBet != 10 || h.minRaise != 10 {
		t.Errorf("currentBet=%d minRaise=%d", h.currentBet, h.minRaise)
	}
}

func TestSimpleAllFoldEndsHand(t *testing.T) {
	players := newSeatedTable(t, 3)
	h, _ := newHand("h1", 0, [2]int{5, 10}, fixedDeck(), players)
	h.startPreflop()

	// seat 0 (UTG) folds, seat 1 (SB) folds — seat 2 (BB) wins.
	if _, _, err := h.applyAction(0, proto.ActFold, 0); err != nil {
		t.Fatal(err)
	}
	h.advance()
	if h.toAct != 1 {
		t.Fatalf("toAct = %d, want 1", h.toAct)
	}
	if _, _, err := h.applyAction(1, proto.ActFold, 0); err != nil {
		t.Fatal(err)
	}
	if h.activeNotFolded() != 1 {
		t.Errorf("only 1 active should remain, got %d", h.activeNotFolded())
	}
}

func TestRaiseAndCallProgressesStreet(t *testing.T) {
	players := newSeatedTable(t, 3)
	h, _ := newHand("h1", 0, [2]int{5, 10}, fixedDeck(), players)
	h.startPreflop()

	// seat 0 raises to 30, seat 1 calls, seat 2 calls.
	if _, _, err := h.applyAction(0, proto.ActRaise, 30); err != nil {
		t.Fatal(err)
	}
	h.advance()
	if _, _, err := h.applyAction(1, proto.ActCall, 0); err != nil {
		t.Fatal(err)
	}
	h.advance()
	if _, _, err := h.applyAction(2, proto.ActCall, 0); err != nil {
		t.Fatal(err)
	}
	if !h.roundClosed() {
		t.Fatalf("round should be closed after BB calls a raise")
	}
	if h.currentBet != 30 {
		t.Errorf("currentBet=%d", h.currentBet)
	}
}

func TestIllegalCheckWhenFacingBet(t *testing.T) {
	players := newSeatedTable(t, 2)
	h, _ := newHand("h1", 0, [2]int{5, 10}, fixedDeck(), players)
	h.startPreflop()
	// heads-up: button (0) = SB, acts first preflop facing 5 short of BB.
	if _, _, err := h.applyAction(0, proto.ActCheck, 0); err == nil {
		t.Fatal("expected check to be illegal facing the BB")
	}
}

func TestRaiseTooSmall(t *testing.T) {
	players := newSeatedTable(t, 3)
	h, _ := newHand("h1", 0, [2]int{5, 10}, fixedDeck(), players)
	h.startPreflop()
	// UTG raise to 15 (only +5 over BB) — minRaise is 10 → illegal.
	if _, _, err := h.applyAction(0, proto.ActRaise, 15); err == nil {
		t.Fatal("expected raise-too-small to be rejected")
	}
}

func TestShortStackAllInCallNoReopen(t *testing.T) {
	players := newSeatedTable(t, 3)
	players[0].Stack = 12 // tiny stack
	h, _ := newHand("h1", 0, [2]int{5, 10}, fixedDeck(), players)
	h.startPreflop()
	// UTG (seat 0) all-in for 12 — this is short of a min raise (need 20),
	// so it should NOT reopen action for already-acted players (none here).
	actual, paid, err := h.applyAction(0, proto.ActAllIn, 0)
	if err != nil {
		t.Fatal(err)
	}
	if actual != proto.ActAllIn {
		t.Errorf("got %v, want all_in", actual)
	}
	if paid != 12 {
		t.Errorf("paid = %d, want 12", paid)
	}
	if !h.seats[0].allIn {
		t.Error("seat 0 should be all-in")
	}
	if h.minRaise != 10 { // unchanged because shortAllIn
		t.Errorf("minRaise = %d, want 10 (unchanged)", h.minRaise)
	}
}
