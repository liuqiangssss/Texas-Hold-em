package table

import (
	"errors"
	"fmt"
	"time"

	"github.com/liuqiangssss/texas-holdem/server/internal/eval"
	"github.com/liuqiangssss/texas-holdem/server/internal/proto"
	"github.com/liuqiangssss/texas-holdem/server/internal/store"
)

// seatState tracks per-seat info that lives only for the duration of a hand.
type seatState struct {
	seat       int
	stack      int    // chips remaining
	stackIn    int    // chips at hand start (before any blinds posted) — for record
	userID     string // immutable for the lifetime of the hand
	nickname   string
	holeCards  []string
	bet        int    // chips invested in the current betting round
	committed  int    // total invested across the whole hand (drives side pots)
	folded     bool
	allIn      bool
	hasActed   bool   // acted at least once in this round (post-blinds)
	sittingOut bool

	pending *pendingAction // armed pre-action; consumed when turn lands
}

// pendingAction is a player's pre-armed intent. It is auto-resolved into a
// concrete action when the seat becomes toAct. amount is interpreted by the
// resolver: ActPreRaiseTo carries the target bet level; the others ignore it.
type pendingAction struct {
	action proto.ActionType
	amount int
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

	// record accumulates a structured snapshot of the hand for offline
	// persistence. Populated by hand methods as the hand progresses;
	// the Table actor finalizes and ships it to the store at hand end.
	record *store.HandRecord
}

// ---------- creation ----------

// newHand initializes a fresh hand from the given seated players. `players`
// is a snapshot of currently seated, non-sitting-out clients (already with
// chips); ordering is by seat id ascending. tableID is recorded into the
// HandRecord so the persisted document can be cross-referenced back to its
// originating table; pass "" if no recording context is available.
func newHand(id, tableID string, button int, blinds [2]int, deck []string,
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
	rec := &store.HandRecord{
		HandID:    id,
		TableID:   tableID,
		Blinds:    blinds,
		Button:    button,
		StartedAt: time.Now().UTC(),
		Seats:     make([]store.SeatSnap, 0, MaxSeats),
	}
	for i, p := range seats {
		if p == nil || p.Stack <= 0 {
			continue
		}
		h.seats[i] = &seatState{
			seat:     i,
			stack:    p.Stack,
			stackIn:  p.Stack,
			userID:   p.UserID,
			nickname: p.Nickname,
		}
		rec.Seats = append(rec.Seats, store.SeatSnap{
			Seat:     i,
			UserID:   p.UserID,
			Nickname: p.Nickname,
			StackIn:  p.Stack,
			IsButton: i == button,
		})
		active++
	}
	if active < 2 {
		return nil, errors.New("not enough players")
	}
	h.record = rec
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
//
// deadBlindSeats lists seats that owe a "dead BB to enter" this hand (typical
// when the player just sat in mid-orbit and missed their natural BB). Each
// such seat contributes BB worth of chips into the pot but does NOT raise
// currentBet/minRaise — the player still owes the call when their turn lands.
// A seat that is also the natural BB this hand is skipped from the dead-blind
// list (no double-post). If `deadBlindSeats` is nil/empty, behaves identically
// to the pre-S3.8 path.
func (h *hand) startPreflop(deadBlindSeats ...int) map[int][]string {
	sb, bb := h.blinds[0], h.blinds[1]

	// Heads-up: button is SB, the other seat is BB.
	// 3+ handed: button + 1 = SB, button + 2 = BB.
	// Sitting-out seats are filtered out at table-level (their seatState is
	// nil here), so nextSeatedAfter naturally skips them — this gives us
	// dead-small-blind behavior when the SB seat is empty.
	live := h.liveSeats()
	var sbSeat, bbSeat int
	if len(live) == 2 {
		sbSeat = h.button
		bbSeat = h.nextSeatedAfter(h.button)
	} else {
		sbSeat = h.nextSeatedAfter(h.button)
		bbSeat = h.nextSeatedAfter(sbSeat)
	}

	// Dead blinds first — money goes into the pot before regular blinds so
	// the order of operations doesn't matter for currentBet/minRaise.
	for _, ds := range deadBlindSeats {
		if ds == bbSeat {
			continue // they will post the natural BB instead
		}
		paid := h.postDeadBlind(ds, bb)
		h.recordAction("preflop", ds, "post_blind", paid, nil)
		h.markSeatFlag(ds, func(s *store.SeatSnap) { s.DeadBB = true })
	}

	sbPaid := h.postBlind(sbSeat, sb)
	h.recordAction("preflop", sbSeat, "post_blind", sbPaid, nil)
	h.markSeatFlag(sbSeat, func(s *store.SeatSnap) { s.PostedSB = true })

	bbPaid := h.postBlind(bbSeat, bb)
	h.recordAction("preflop", bbSeat, "post_blind", bbPaid, nil)
	h.markSeatFlag(bbSeat, func(s *store.SeatSnap) { s.PostedBB = true })

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

// postBlind charges `amount` (clamped to stack) and returns the actually paid
// chips so callers can record the transaction.
func (h *hand) postBlind(seat, amount int) int {
	s := h.seats[seat]
	if s == nil {
		return 0
	}
	pay := amount
	if pay > s.stack {
		pay = s.stack
		s.allIn = true
	}
	s.stack -= pay
	s.bet += pay
	s.committed += pay
	return pay
}

// postDeadBlind drops `amount` chips into the pot from `seat` without touching
// the seat's per-round bet. The chips count toward `committed` (drives side-
// pot eligibility) but do NOT shift currentBet/minRaise — the player still
// owes a full call to see the flop. If the seat doesn't have enough chips,
// they go all-in for the dead blind. Returns chips actually paid.
func (h *hand) postDeadBlind(seat, amount int) int {
	s := h.seats[seat]
	if s == nil {
		return 0
	}
	pay := amount
	if pay > s.stack {
		pay = s.stack
		s.allIn = true
	}
	s.stack -= pay
	s.committed += pay
	return pay
}

// recordAction appends a single timeline entry to the in-progress hand
// record. No-op when the hand was constructed without a record (legacy
// tests calling newHand directly with the old signature can't reach this
// path because newHand always installs a record now).
func (h *hand) recordAction(stage string, seat int, action string, amount int, cards []string) {
	if h.record == nil {
		return
	}
	h.record.Actions = append(h.record.Actions, store.ActionRec{
		Stage:  stage,
		Seat:   seat,
		Action: action,
		Amount: amount,
		Cards:  cards,
	})
}

// markSeatFlag mutates the SeatSnap entry for the given seat in the record,
// when present. Used to flip booleans like PostedSB/PostedBB/DeadBB after
// blinds post.
func (h *hand) markSeatFlag(seat int, fn func(*store.SeatSnap)) {
	if h.record == nil {
		return
	}
	for i := range h.record.Seats {
		if h.record.Seats[i].Seat == seat {
			fn(&h.record.Seats[i])
			return
		}
	}
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
		h.recordAction(string(h.stage), seat, string(proto.ActFold), 0, nil)
		return proto.ActFold, 0, nil

	case proto.ActCheck:
		if s.bet < h.currentBet {
			return "", 0, errIllegalCheck
		}
		s.hasActed = true
		h.recordAction(string(h.stage), seat, string(proto.ActCheck), 0, nil)
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
		h.recordAction(string(h.stage), seat, string(actual), pay, nil)
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
		h.recordAction(string(h.stage), seat, string(actual), pay, nil)
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
			h.recordAction(string(h.stage), seat, string(proto.ActAllIn), pay, nil)
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
		h.recordAction(string(h.stage), seat, string(actual), pay, nil)
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
		h.recordAction(string(proto.StageFlop), -1, "deal_flop", 0, append([]string(nil), cards...))
		return cards
	case proto.StageFlop:
		h.stage = proto.StageTurn
		c := h.drawCard()
		h.community = append(h.community, c)
		h.recordAction(string(proto.StageTurn), -1, "deal_turn", 0, []string{c})
		return []string{c}
	case proto.StageTurn:
		h.stage = proto.StageRiver
		c := h.drawCard()
		h.community = append(h.community, c)
		h.recordAction(string(proto.StageRiver), -1, "deal_river", 0, []string{c})
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
	h.finalizeRecord("showdown", pots, winners, reveals)
	return pots, winners, reveals
}

// finalizeRecord stamps the end-of-hand fields onto the in-progress record.
// reason is "showdown" or "fold_out". Pass nil/empty for fields that don't
// apply to the path (fold_out leaves pots/reveals empty).
func (h *hand) finalizeRecord(reason string, pots []eval.Pot, winners []proto.WinnerInfo, reveals map[int][]string) {
	if h.record == nil {
		return
	}
	h.record.Reason = reason
	h.record.EndedAt = time.Now().UTC()
	h.record.Community = append([]string(nil), h.community...)

	for i := range h.record.Seats {
		seat := h.record.Seats[i].Seat
		if s := h.seats[seat]; s != nil {
			h.record.Seats[i].StackOut = s.stack
		}
	}

	if len(pots) > 0 {
		h.record.Pots = make([]store.PotRec, 0, len(pots))
		for _, p := range pots {
			h.record.Pots = append(h.record.Pots, store.PotRec{
				Amount:   p.Amount,
				Eligible: append([]int(nil), p.Eligible...),
			})
		}
	}
	if len(winners) > 0 {
		h.record.Winners = make([]store.WinnerRec, 0, len(winners))
		for _, w := range winners {
			h.record.Winners = append(h.record.Winners, store.WinnerRec{
				Seat:     w.Seat,
				Amount:   w.Amount,
				HandRank: w.HandRank,
				Best5:    append([]string(nil), w.Best5...),
			})
		}
	}
	if len(reveals) > 0 {
		h.record.HoleCards = make(map[int][]string, len(reveals))
		for seat, cards := range reveals {
			h.record.HoleCards[seat] = append([]string(nil), cards...)
		}
	}
}

// ---------- pre-actions ----------

// setPending stores a pre-action for a seat after sanity-checking the
// arguments. Returns an error if the request is malformed; out-of-context
// (e.g. the seat is currently to-act, or the hand has no betting) is *not* an
// error — it is treated as a request that simply fires immediately.
func (h *hand) setPending(seat int, action proto.ActionType, amount int) error {
	s := h.seats[seat]
	if s == nil || s.folded || s.allIn {
		return errors.New("seat not active in this hand")
	}
	switch action {
	case proto.ActPreClear:
		s.pending = nil
		return nil
	case proto.ActPreCheckFold, proto.ActPreCallAny:
		s.pending = &pendingAction{action: action}
		return nil
	case proto.ActPreRaiseTo:
		if amount <= 0 {
			return fmt.Errorf("%w: pre_raise_to needs positive amount", errBadAmount)
		}
		s.pending = &pendingAction{action: action, amount: amount}
		return nil
	default:
		return fmt.Errorf("not a pre-action: %s", action)
	}
}

// resolvePending interprets a seat's pending pre-action against the current
// hand state, returning the concrete (action, amount) to apply via
// applyAction, or ok=false when the pre-action no longer fits and should be
// discarded silently.
func (h *hand) resolvePending(seat int) (proto.ActionType, int, bool) {
	s := h.seats[seat]
	if s == nil || s.pending == nil {
		return "", 0, false
	}
	p := s.pending
	switch p.action {
	case proto.ActPreCheckFold:
		if h.currentBet > s.bet {
			return proto.ActFold, 0, true
		}
		return proto.ActCheck, 0, true
	case proto.ActPreCallAny:
		if h.currentBet > s.bet {
			return proto.ActCall, 0, true
		}
		return proto.ActCheck, 0, true
	case proto.ActPreRaiseTo:
		// Discard if the table has already moved past the planned level, or
		// if the player can no longer make a legal raise (would be a partial
		// all-in below currentBet, etc).
		if p.amount <= h.currentBet {
			return "", 0, false
		}
		need := p.amount - s.bet
		if need <= 0 || need > s.stack {
			return "", 0, false
		}
		raiseInc := p.amount - h.currentBet
		shortAllIn := need == s.stack && raiseInc < h.minRaise
		if !shortAllIn && raiseInc < h.minRaise {
			return "", 0, false
		}
		return proto.ActRaise, p.amount, true
	}
	return "", 0, false
}

// clearPending removes any armed pre-action for the seat. Used after
// resolvePending returned a result we just applied, or when state has
// invalidated the pre-action.
func (h *hand) clearPending(seat int) {
	if s := h.seats[seat]; s != nil {
		s.pending = nil
	}
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
	winners := []proto.WinnerInfo{}
	if winnerSeat >= 0 {
		winners = append(winners, proto.WinnerInfo{Seat: winnerSeat, Amount: total})
	}
	h.finalizeRecord("fold_out", nil, winners, nil)
	return winnerSeat, total
}
