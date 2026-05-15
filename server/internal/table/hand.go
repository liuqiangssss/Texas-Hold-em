package table

import (
	"errors"
	"fmt"

	"github.com/liuqiangssss/texas-holdem/server/internal/eval"
	"github.com/liuqiangssss/texas-holdem/server/internal/proto"
)

// seatState tracks per-seat info that lives only for the duration of a hand.
type seatState struct {
	seat       int
	stack      int    // chips remaining
	holeCards  []string
	bet        int    // chips invested in the current betting round
	committed  int    // total invested across the whole hand (drives side pots)
	folded     bool
	allIn      bool
	hasActed   bool   // acted at least once in this round (post-blinds)
	sittingOut bool
}

// hand is the per-hand mutable state owned by the table actor. All mutations
// happen on the actor goroutine; no internal locking is necessary.
type hand struct {
	id       string
	stage    proto.Stage
	deck     []string
	dealCur  int       // next index in deck to deal
	community []string

	seats     []*seatState // ordered by seat id; nil for empty seats
	lastRaiser int         // seat that last opened/raised; -1 if none
	toAct      int         // seat currently to act (-1 when betting closed)

	// betting round bookkeeping
	currentBet int // highest individual bet in the current round
	minRaise   int // minimum legal raise *increment* (BB at preflop, last raise size after)

	button int
	blinds [2]int
}

// ---------- creation ----------

// newHand initializes a fresh hand from the given seated players. `players`
// is a snapshot of currently seated, non-sitting-out clients (already with
// chips); ordering is by seat id ascending.
func newHand(id string, button int, blinds [2]int, deck []string,
	seats [MaxSeats]*Player) (*hand, error) {

	h := &hand{
		id:        id,
		stage:     proto.StageWaiting,
		deck:      deck,
		button:    button,
		blinds:    blinds,
		seats:     make([]*seatState, MaxSeats),
		lastRaiser: -1,
		toAct:     -1,
	}
	active := 0
	for i, p := range seats {
		if p == nil || p.Stack <= 0 {
			continue
		}
		h.seats[i] = &seatState{seat: i, stack: p.Stack}
		active++
	}
	if active < 2 {
		return nil, errors.New("not enough players")
	}
	return h, nil
}

// ---------- helpers ----------

func (h *hand) liveSeats() []*seatState {
	out := make([]*seatState, 0, MaxSeats)
	for _, s := range h.seats {
		if s != nil {
			out = append(out, s)
		}
	}
	return out
}

func (h *hand) activeNotFolded() int {
	n := 0
	for _, s := range h.seats {
		if s != nil && !s.folded {
			n++
		}
	}
	return n
}

// canAct returns seats that can still make a voluntary action (not folded,
// not already all-in, and have chips).
func (h *hand) canAct() []*seatState {
	out := make([]*seatState, 0, MaxSeats)
	for _, s := range h.seats {
		if s != nil && !s.folded && !s.allIn && s.stack > 0 {
			out = append(out, s)
		}
	}
	return out
}

// nextSeatedAfter returns the next non-nil seat strictly after `from`
// (cyclic). Returns -1 if there are no other seats.
func (h *hand) nextSeatedAfter(from int) int {
	for i := 1; i <= MaxSeats; i++ {
		idx := (from + i) % MaxSeats
		if h.seats[idx] != nil {
			return idx
		}
	}
	return -1
}

// nextActorAfter returns the next seat after `from` that can act (not
// folded, not all-in, has chips). Returns -1 if no such seat.
func (h *hand) nextActorAfter(from int) int {
	for i := 1; i <= MaxSeats; i++ {
		idx := (from + i) % MaxSeats
		s := h.seats[idx]
		if s != nil && !s.folded && !s.allIn && s.stack > 0 {
			return idx
		}
	}
	return -1
}

func (h *hand) drawCard() string {
	c := h.deck[h.dealCur]
	h.dealCur++
	return c
}

// ---------- preflop setup ----------

// startPreflop posts blinds, deals hole cards, and computes the first to-act
// seat (UTG = first seat after the BB). Returns the per-seat hole cards keyed
// by seat id so the actor can deliver them privately.
func (h *hand) startPreflop() map[int][]string {
	sb, bb := h.blinds[0], h.blinds[1]

	// Heads-up: button is SB, the other seat is BB.
	// 3+ handed: button + 1 = SB, button + 2 = BB.
	live := h.liveSeats()
	var sbSeat, bbSeat int
	if len(live) == 2 {
		sbSeat = h.button
		bbSeat = h.nextSeatedAfter(h.button)
	} else {
		sbSeat = h.nextSeatedAfter(h.button)
		bbSeat = h.nextSeatedAfter(sbSeat)
	}
	h.postBlind(sbSeat, sb)
	h.postBlind(bbSeat, bb)

	h.currentBet = bb
	h.minRaise = bb
	h.lastRaiser = bbSeat // the BB is treated as the opener until someone raises

	// Deal 2 hole cards each, in seat order starting after the button.
	holes := map[int][]string{}
	first := h.nextSeatedAfter(h.button)
	for round := 0; round < 2; round++ {
		seat := first
		for k := 0; k < len(live); k++ {
			c := h.drawCard()
			h.seats[seat].holeCards = append(h.seats[seat].holeCards, c)
			seat = h.nextSeatedAfter(seat)
		}
		_ = round
	}
	for _, s := range live {
		holes[s.seat] = append([]string(nil), s.holeCards...)
	}

	h.stage = proto.StagePreflop
	// First to act preflop is the seat after BB (UTG). Heads-up: SB.
	if len(live) == 2 {
		h.toAct = sbSeat
	} else {
		h.toAct = h.nextActorAfter(bbSeat)
	}
	// In heads-up the SB blind already ate into their bet; they still need
	// to act first preflop and may complete or raise.
	return holes
}

func (h *hand) postBlind(seat, amount int) {
	s := h.seats[seat]
	if s == nil {
		return
	}
	pay := amount
	if pay > s.stack {
		pay = s.stack
		s.allIn = true
	}
	s.stack -= pay
	s.bet += pay
	s.committed += pay
}

// ---------- action processing ----------

// errIllegal* are returned to the caller for protocol-level validation. The
// transport layer turns them into proto.ErrorMsg frames.
var (
	errNotYourTurn = errors.New("not your turn")
	errIllegalCheck = errors.New("cannot check, must call/raise/fold")
	errIllegalCall  = errors.New("nothing to call")
	errIllegalBet   = errors.New("there is already a bet, use raise")
	errIllegalRaise = errors.New("invalid raise amount")
	errBadAmount    = errors.New("invalid amount")
)

// applyAction validates a player intent and mutates state. Returns
// (actualAction, actualAmount, error). actualAction may differ from the
// request when a player call/raise resolves to all-in due to short stack.
func (h *hand) applyAction(seat int, action proto.ActionType, amount int) (proto.ActionType, int, error) {
	if seat != h.toAct {
		return "", 0, errNotYourTurn
	}
	s := h.seats[seat]
	if s == nil || s.folded || s.allIn {
		return "", 0, errNotYourTurn
	}

	switch action {
	case proto.ActFold:
		s.folded = true
		s.hasActed = true
		return proto.ActFold, 0, nil

	case proto.ActCheck:
		if s.bet < h.currentBet {
			return "", 0, errIllegalCheck
		}
		s.hasActed = true
		return proto.ActCheck, 0, nil

	case proto.ActCall:
		toCall := h.currentBet - s.bet
		if toCall <= 0 {
			return "", 0, errIllegalCall
		}
		pay := toCall
		if pay >= s.stack {
			pay = s.stack
			s.allIn = true
		}
		s.stack -= pay
		s.bet += pay
		s.committed += pay
		s.hasActed = true
		actual := proto.ActCall
		if s.allIn {
			actual = proto.ActAllIn
		}
		return actual, pay, nil

	case proto.ActBet:
		if h.currentBet > 0 {
			return "", 0, errIllegalBet
		}
		if amount < h.blinds[1] {
			return "", 0, fmt.Errorf("%w: bet must be >= BB %d", errBadAmount, h.blinds[1])
		}
		if amount > s.stack {
			return "", 0, errBadAmount
		}
		pay := amount
		if pay == s.stack {
			s.allIn = true
		}
		s.stack -= pay
		s.bet += pay
		s.committed += pay
		s.hasActed = true
		h.currentBet = s.bet
		h.minRaise = pay
		h.lastRaiser = seat
		// Reset hasActed for everyone else so they get a chance to respond.
		for _, o := range h.seats {
			if o != nil && o.seat != seat && !o.folded && !o.allIn {
				o.hasActed = false
			}
		}
		actual := proto.ActBet
		if s.allIn {
			actual = proto.ActAllIn
		}
		return actual, pay, nil

	case proto.ActRaise, proto.ActAllIn:
		// Raise is interpreted as "raise TO `amount`" (final bet level).
		// All-in shortcut: amount == s.bet + s.stack.
		if action == proto.ActAllIn {
			amount = s.bet + s.stack
		}
		// Special-case: an all-in for less than the current bet is treated
		// as a partial call (the player can't afford a raise but can still
		// commit their stack to call-as-much-as-they-can).
		if action == proto.ActAllIn && amount <= h.currentBet {
			pay := s.stack
			s.stack = 0
			s.bet += pay
			s.committed += pay
			s.allIn = true
			s.hasActed = true
			return proto.ActAllIn, pay, nil
		}
		if amount <= h.currentBet {
			return "", 0, errIllegalRaise
		}
		pay := amount - s.bet
		if pay > s.stack {
			return "", 0, errIllegalRaise
		}
		raiseInc := amount - h.currentBet
		shortAllIn := pay == s.stack && raiseInc < h.minRaise
		if !shortAllIn && raiseInc < h.minRaise {
			return "", 0, fmt.Errorf("%w: must raise by at least %d", errIllegalRaise, h.minRaise)
		}
		s.stack -= pay
		s.bet += pay
		s.committed += pay
		if pay == 0 {
			// Should not happen, but guard.
			return "", 0, errIllegalRaise
		}
		if s.stack == 0 {
			s.allIn = true
		}
		s.hasActed = true
		h.currentBet = s.bet
		// A short all-in raise does NOT reopen the action for previously-
		// acted players — they only owe the call delta. Otherwise this is
		// a legal raise that resets hasActed for everyone else.
		if !shortAllIn {
			h.minRaise = raiseInc
			h.lastRaiser = seat
			for _, o := range h.seats {
				if o != nil && o.seat != seat && !o.folded && !o.allIn {
					o.hasActed = false
				}
			}
		}
		actual := proto.ActRaise
		if s.allIn {
			actual = proto.ActAllIn
		}
		return actual, pay, nil

	default:
		return "", 0, fmt.Errorf("unknown action %s", action)
	}
}

// roundClosed reports whether the current betting round is over. A round
// ends when every non-folded seat that can still act has acted at least once
// AND has matched the currentBet (or is all-in).
func (h *hand) roundClosed() bool {
	for _, s := range h.seats {
		if s == nil || s.folded || s.allIn {
			continue
		}
		if !s.hasActed {
			return false
		}
		if s.bet < h.currentBet {
			return false
		}
	}
	return true
}

// advance moves toAct to the next eligible seat. If no other seat can act
// (everyone else folded or all-in), returns -1.
func (h *hand) advance() {
	h.toAct = h.nextActorAfter(h.toAct)
}

// resetForNextStreet zeroes the per-round counters for the next betting
// street and computes the first-to-act seat (first live seat after the
// button). Returns true if there is at least one player that can act on the
// next street; false means we should fast-forward straight through to
// showdown (e.g. heads-up all-in).
func (h *hand) resetForNextStreet() bool {
	for _, s := range h.seats {
		if s == nil {
			continue
		}
		s.bet = 0
		s.hasActed = false
	}
	h.currentBet = 0
	h.minRaise = h.blinds[1]
	h.lastRaiser = -1
	first := h.nextActorAfter(h.button)
	h.toAct = first
	return first >= 0 && len(h.canAct()) >= 2
}

// dealStreet draws the cards for the next street and advances the stage.
// Returns the cards just dealt (for broadcast). Burn cards are NOT used in
// MVP — they don't affect outcome and add no value.
func (h *hand) dealStreet() []string {
	switch h.stage {
	case proto.StagePreflop:
		h.stage = proto.StageFlop
		cards := []string{h.drawCard(), h.drawCard(), h.drawCard()}
		h.community = append(h.community, cards...)
		return cards
	case proto.StageFlop:
		h.stage = proto.StageTurn
		c := h.drawCard()
		h.community = append(h.community, c)
		return []string{c}
	case proto.StageTurn:
		h.stage = proto.StageRiver
		c := h.drawCard()
		h.community = append(h.community, c)
		return []string{c}
	}
	return nil
}

// ---------- showdown ----------

// settle computes pots and winners, and credits each winning seat's stack.
// Returns the pots and winners list ready for broadcast.
func (h *hand) settle() ([]eval.Pot, []proto.WinnerInfo, map[int][]string) {
	contrib := map[int]int{}
	folded := map[int]bool{}
	for _, s := range h.seats {
		if s == nil {
			continue
		}
		if s.committed > 0 {
			contrib[s.seat] = s.committed
		}
		folded[s.seat] = s.folded
	}
	pots := eval.BuildPots(contrib, folded)

	// Evaluate hands for non-folded seats.
	type hand5 struct {
		seat int
		res  eval.Result
	}
	results := map[int]hand5{}
	reveals := map[int][]string{}
	for _, s := range h.seats {
		if s == nil || s.folded {
			continue
		}
		all := append([]string{}, s.holeCards...)
		all = append(all, h.community...)
		// If the board is short (< 3) we can't evaluate; only happens when
		// hand ends by fold-out before flop, in which case this code path
		// isn't taken (caller short-circuits to fold_out winner).
		if len(all) >= 5 {
			results[s.seat] = hand5{seat: s.seat, res: eval.Evaluate7(all)}
			reveals[s.seat] = append([]string(nil), s.holeCards...)
		}
	}

	winners := make([]proto.WinnerInfo, 0, len(pots))
	for _, p := range pots {
		// Of the eligible seats for this pot, find the best score.
		best := eval.Score(0)
		var bestSeats []int
		for _, sid := range p.Eligible {
			r, ok := results[sid]
			if !ok {
				continue
			}
			cmp := eval.Compare(r.res.Score, best)
			if len(bestSeats) == 0 || cmp > 0 {
				best = r.res.Score
				bestSeats = []int{sid}
			} else if cmp == 0 {
				bestSeats = append(bestSeats, sid)
			}
		}
		if len(bestSeats) == 0 {
			// All eligible seats folded (rare edge case e.g. dead money) —
			// award to the last surviving non-folded seat at this layer.
			continue
		}
		share := p.Amount / len(bestSeats)
		remainder := p.Amount - share*len(bestSeats)
		for i, sid := range bestSeats {
			amt := share
			if i == 0 {
				amt += remainder // odd chip goes to first seat after button
			}
			h.seats[sid].stack += amt
			r := results[sid]
			winners = append(winners, proto.WinnerInfo{
				Seat:     sid,
				Amount:   amt,
				HandRank: r.res.Category.String(),
				Best5:    r.res.Best5,
			})
		}
	}
	h.stage = proto.StageShowdown
	return pots, winners, reveals
}

// awardSinglePot is used when only one player is left (everyone else folded).
// The whole pot (sum of all committed) goes to the winner.
func (h *hand) awardSinglePot() (int, int) {
	winnerSeat := -1
	total := 0
	for _, s := range h.seats {
		if s == nil {
			continue
		}
		total += s.committed
		if !s.folded {
			winnerSeat = s.seat
		}
	}
	if winnerSeat >= 0 {
		h.seats[winnerSeat].stack += total
	}
	return winnerSeat, total
}
