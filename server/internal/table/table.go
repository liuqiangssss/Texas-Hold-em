package table

import (
	"context"
	"log"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/liuqiangssss/texas-holdem/server/internal/deck"
	"github.com/liuqiangssss/texas-holdem/server/internal/eval"
	"github.com/liuqiangssss/texas-holdem/server/internal/proto"
)

const MaxSeats = 6

const (
	autoStartDelay = 800 * time.Millisecond
	streetDelay    = 900 * time.Millisecond
	handEndDelay   = 2500 * time.Millisecond
	defaultBuyIn   = 1000

	// S3.6 Time Bank.
	baseTurnMs        = 15_000
	timeBankInitMs    = 30_000
	timeBankPerHandMs = 5_000
	timeBankCapMs     = 60_000
)

// Player is the server-side view of a seated client.
type Player struct {
	UserID   string
	Nickname string
	Seat     int
	Stack    int
	Send     chan<- any // per-client outbound channel (JSON encoded upstream)

	// TimeBankMs is the player's personal time-bank reserve, persisted across
	// hands at the same table. Replenished by timeBankPerHandMs at the end of
	// every hand they survive (capped at timeBankCapMs).
	TimeBankMs int
}

// ---- actor commands ----

type sitCmd struct {
	player *Player
	done   chan bool
}

type leaveCmd struct {
	userID string
}

type startHandCmd struct{}

type advanceStreetCmd struct {
	handID string
}

type endHandCmd struct {
	handID string
}

type actionCmd struct {
	userID string
	handID string
	action proto.ActionType
	amount int
}

type preActionCmd struct {
	userID string
	handID string
	action proto.ActionType
	amount int
}

// turnTimeoutCmd is fired by an internal timer when a seat has run through
// both base time and their personal time bank. handID + seat guard against
// stale fires from old turns.
type turnTimeoutCmd struct {
	handID string
	seat   int
}

// Table is a single 6-max poker table running in its own goroutine (actor).
type Table struct {
	ID     string
	Blinds [2]int

	seats       [MaxSeats]*Player
	hand        *hand
	button      int
	seqCtr      atomic.Uint64
	seatedCount atomic.Int32

	cmdIn chan any

	// Turn timer state — only mutated from the actor goroutine.
	turnTimer    *time.Timer
	turnHandID   string
	turnSeat     int
	turnStartAt  time.Time
	turnBudgetMs int // base + bank used to size the timer
}

func New(blinds [2]int) *Table {
	return &Table{
		ID:     uuid.NewString(),
		Blinds: blinds,
		cmdIn:  make(chan any, 64),
		button: -1,
	}
}

// Run drives the actor loop. Stops when ctx is canceled.
func (t *Table) Run(ctx context.Context) {
	log.Printf("[table %s] actor started (blinds %d/%d)", t.ID[:8], t.Blinds[0], t.Blinds[1])
	for {
		select {
		case <-ctx.Done():
			log.Printf("[table %s] actor stopped", t.ID[:8])
			return
		case cmd := <-t.cmdIn:
			t.handle(cmd)
		}
	}
}

func (t *Table) handle(cmd any) {
	switch c := cmd.(type) {
	case sitCmd:
		ok := t.seatPlayer(c.player)
		c.done <- ok
		if ok {
			t.broadcastState()
			if t.activePlayers() >= 2 && t.hand == nil {
				t.scheduleAfter(autoStartDelay, startHandCmd{})
			}
		}
	case leaveCmd:
		for i, p := range t.seats {
			if p != nil && p.UserID == c.userID {
				t.seats[i] = nil
				t.seatedCount.Add(-1)
				log.Printf("[table %s] %s left seat %d", t.ID[:8], p.Nickname, i)
				if t.hand != nil && t.hand.seats[i] != nil && !t.hand.seats[i].folded {
					t.hand.seats[i].folded = true
					t.hand.seats[i].hasActed = true
					t.hand.clearPending(i)
					if t.hand.toAct == i {
						t.cancelTurnTimer()
						t.hand.advance()
					}
					t.maybeFinishStreetOrHand()
				}
				t.broadcastState()
				return
			}
		}
	case startHandCmd:
		t.startHand()
	case advanceStreetCmd:
		if t.hand == nil || t.hand.id != c.handID {
			return
		}
		t.advanceStreet()
	case endHandCmd:
		if t.hand == nil || t.hand.id != c.handID {
			return
		}
		t.cleanupHand()
		if t.activePlayers() >= 2 {
			t.scheduleAfter(autoStartDelay, startHandCmd{})
		}
	case actionCmd:
		t.handleAction(c)
	case preActionCmd:
		t.handlePreAction(c)
	case turnTimeoutCmd:
		t.handleTurnTimeout(c)
	default:
		if t.handleExtra(cmd) {
			return
		}
		log.Printf("[table %s] unknown cmd %T", t.ID[:8], cmd)
	}
}

// scheduleAfter posts a command back to our own actor loop after `d`.
func (t *Table) scheduleAfter(d time.Duration, cmd any) {
	time.AfterFunc(d, func() {
		select {
		case t.cmdIn <- cmd:
		default:
			log.Printf("[table %s] dropped scheduled cmd %T", t.ID[:8], cmd)
		}
	})
}

// ---- public API ----

func (t *Table) Sit(p *Player) bool {
	done := make(chan bool, 1)
	t.cmdIn <- sitCmd{player: p, done: done}
	return <-done
}

func (t *Table) Leave(userID string) {
	t.cmdIn <- leaveCmd{userID: userID}
}

// Action enqueues a player intent. Returns immediately; result is broadcast.
func (t *Table) Action(userID, handID string, action proto.ActionType, amount int) {
	t.cmdIn <- actionCmd{userID: userID, handID: handID, action: action, amount: amount}
}

// PreAction enqueues a pre-armed action (or a clear). Returns immediately;
// the slot is updated server-side and resolved when the seat's turn lands.
func (t *Table) PreAction(userID, handID string, action proto.ActionType, amount int) {
	t.cmdIn <- preActionCmd{userID: userID, handID: handID, action: action, amount: amount}
}

// ---- seating ----

func (t *Table) seatPlayer(p *Player) bool {
	for i := 0; i < MaxSeats; i++ {
		if t.seats[i] == nil {
			p.Seat = i
			if p.Stack <= 0 {
				p.Stack = defaultBuyIn
			}
			if p.TimeBankMs <= 0 {
				p.TimeBankMs = timeBankInitMs
			}
			t.seats[i] = p
			t.seatedCount.Add(1)
			log.Printf("[table %s] %s seated at %d (stack %d, bank %dms)",
				t.ID[:8], p.Nickname, i, p.Stack, p.TimeBankMs)
			return true
		}
	}
	return false
}

func (t *Table) activePlayers() int {
	n := 0
	for _, p := range t.seats {
		if p != nil && p.Stack > 0 {
			n++
		}
	}
	return n
}

func (t *Table) nextSeq() uint64 { return t.seqCtr.Add(1) }

// ---- broadcast ----

func (t *Table) snapshot() proto.TableState {
	seats := make([]proto.SeatInfo, 0, MaxSeats)
	for i, p := range t.seats {
		if p == nil {
			continue
		}
		info := proto.SeatInfo{
			Seat:     i,
			UserID:   p.UserID,
			Nickname: p.Nickname,
			Stack:    p.Stack,
		}
		if t.hand != nil && t.hand.seats[i] != nil {
			s := t.hand.seats[i]
			info.Stack = s.stack
			info.Bet = s.bet
			info.Committed = s.committed
			info.Folded = s.folded
			info.AllIn = s.allIn
		}
		seats = append(seats, info)
	}
	state := proto.TableState{
		Envelope: proto.Envelope{Type: proto.MsgTableState, Seq: t.nextSeq()},
		TableID:  t.ID,
		Blinds:   t.Blinds,
		Seats:    seats,
		Stage:    proto.StageWaiting,
		Button:   t.button,
		ToAct:    -1,
	}
	if t.hand != nil {
		state.HandID = t.hand.id
		state.Stage = t.hand.stage
		state.Community = t.hand.community
		state.Pot = t.totalCommitted()
		state.Button = t.hand.button
		state.ToAct = t.hand.toAct
		state.LastBet = t.hand.currentBet
		state.MinRaise = t.hand.minRaise
	}
	return state
}

func (t *Table) totalCommitted() int {
	if t.hand == nil {
		return 0
	}
	tot := 0
	for _, s := range t.hand.seats {
		if s != nil {
			tot += s.committed
		}
	}
	return tot
}

func (t *Table) broadcastState() {
	state := t.snapshot()
	for _, p := range t.seats {
		if p == nil {
			continue
		}
		personal := state
		personal.YourSeat = p.Seat
		t.sendTo(p, personal)
	}
}

func (t *Table) broadcast(msg any) {
	for _, p := range t.seats {
		if p == nil {
			continue
		}
		t.sendTo(p, msg)
	}
}

func (t *Table) sendTo(p *Player, msg any) {
	select {
	case p.Send <- msg:
	default:
		log.Printf("[table %s] drop msg %T to %s (slow)", t.ID[:8], msg, p.Nickname)
	}
}

// ---- hand lifecycle ----

func (t *Table) startHand() {
	if t.hand != nil {
		return
	}
	if t.activePlayers() < 2 {
		return
	}
	// Advance button to next seated player.
	for i := 1; i <= MaxSeats; i++ {
		idx := (t.button + i) % MaxSeats
		if t.seats[idx] != nil && t.seats[idx].Stack > 0 {
			t.button = idx
			break
		}
	}
	d := deck.NewDeck()
	deck.Shuffle(d)
	hid := uuid.NewString()
	h, err := newHand(hid, t.button, t.Blinds, d, t.seats)
	if err != nil {
		log.Printf("[table %s] start_hand: %v", t.ID[:8], err)
		return
	}
	t.hand = h
	log.Printf("[table %s] hand %s start, button=%d", t.ID[:8], hid[:8], t.button)

	t.broadcast(proto.HandStart{
		Envelope: proto.Envelope{Type: proto.MsgHandStart, Seq: t.nextSeq()},
		HandID:   hid,
		TableID:  t.ID,
		Button:   t.button,
		Blinds:   t.Blinds,
	})
	holes := h.startPreflop()
	for seat, cards := range holes {
		p := t.seats[seat]
		if p == nil {
			continue
		}
		t.sendTo(p, proto.DealHole{
			Envelope: proto.Envelope{Type: proto.MsgDealHole, Seq: t.nextSeq()},
			HandID:   hid,
			Cards:    cards,
		})
	}
	t.broadcastState()
	t.broadcastToAct()
}

func (t *Table) cleanupHand() {
	if t.hand == nil {
		return
	}
	t.cancelTurnTimer()
	// Sync each player's persistent stack from per-hand stack and clear
	// hand state.
	for i, s := range t.hand.seats {
		if s != nil && t.seats[i] != nil {
			t.seats[i].Stack = s.stack
		}
	}
	t.hand = nil
	// Replenish time bank for everyone still seated. The cap protects
	// against unboundedly accumulating bank for players who fold instantly
	// every hand.
	for _, p := range t.seats {
		if p == nil {
			continue
		}
		p.TimeBankMs += timeBankPerHandMs
		if p.TimeBankMs > timeBankCapMs {
			p.TimeBankMs = timeBankCapMs
		}
	}
	// Drop players with 0 chips after the hand.
	for i, p := range t.seats {
		if p != nil && p.Stack <= 0 {
			t.sendTo(p, proto.ErrorMsg{
				Envelope: proto.Envelope{Type: proto.MsgError, Seq: t.nextSeq()},
				Code:     "broke",
				Message:  "out of chips, please re-buy",
			})
			t.seats[i] = nil
			t.seatedCount.Add(-1)
		}
	}
	t.broadcastState()
}

// handleAction processes a player action message.
func (t *Table) handleAction(c actionCmd) {
	if t.hand == nil {
		t.sendErrorByUser(c.userID, "no_hand", "no active hand")
		return
	}
	if c.handID != "" && c.handID != t.hand.id {
		t.sendErrorByUser(c.userID, "stale_hand", "action targets a finished hand")
		return
	}
	seat := -1
	for i, p := range t.seats {
		if p != nil && p.UserID == c.userID {
			seat = i
			break
		}
	}
	if seat < 0 {
		t.sendErrorByUser(c.userID, "not_seated", "player not at table")
		return
	}
	// A real action arrived in time — settle the bank and apply.
	if seat == t.hand.toAct {
		t.consumeBankFor(seat, time.Since(t.turnStartAt))
	}
	t.applyActionAndBroadcast(seat, c.action, c.amount, true /* surfaceErrors */)
}

// applyActionAndBroadcast is the shared path for human actions, pre-action
// resolutions, and forced-timeout fallbacks. Returns true if the action was
// applied (either successfully or surfaced as an error to the player).
func (t *Table) applyActionAndBroadcast(seat int, action proto.ActionType, amount int, surfaceErrors bool) bool {
	actual, paid, err := t.hand.applyAction(seat, action, amount)
	if err != nil {
		if surfaceErrors {
			if p := t.seats[seat]; p != nil {
				t.sendErrorByUser(p.UserID, "illegal_action", err.Error())
			}
		}
		return false
	}
	// Action consumed any pre-action this seat had armed.
	t.hand.clearPending(seat)
	t.cancelTurnTimer()
	t.broadcast(proto.ActionApplied{
		Envelope: proto.Envelope{Type: proto.MsgActionApplied, Seq: t.nextSeq()},
		HandID:   t.hand.id,
		Seat:     seat,
		Action:   actual,
		Amount:   paid,
		Stack:    t.hand.seats[seat].stack,
		Bet:      t.hand.seats[seat].bet,
	})
	t.broadcastPotUpdate()
	t.maybeFinishStreetOrHand()
	return true
}

// handlePreAction validates and stores (or clears) a seat's pre-action.
func (t *Table) handlePreAction(c preActionCmd) {
	if t.hand == nil {
		t.sendErrorByUser(c.userID, "no_hand", "no active hand")
		return
	}
	if c.handID != "" && c.handID != t.hand.id {
		t.sendErrorByUser(c.userID, "stale_hand", "pre-action targets a finished hand")
		return
	}
	seat := -1
	for i, p := range t.seats {
		if p != nil && p.UserID == c.userID {
			seat = i
			break
		}
	}
	if seat < 0 {
		t.sendErrorByUser(c.userID, "not_seated", "player not at table")
		return
	}
	if err := t.hand.setPending(seat, c.action, c.amount); err != nil {
		t.sendErrorByUser(c.userID, "illegal_pre_action", err.Error())
		return
	}
	// If the player armed a pre-action exactly while their turn is current,
	// fire it immediately.
	if t.hand.toAct == seat {
		t.tryResolvePreActionLoop()
	}
}

// handleTurnTimeout fires when the actor's whole budget has elapsed without
// an action. Auto-fold (when facing a bet) or auto-check.
func (t *Table) handleTurnTimeout(c turnTimeoutCmd) {
	if t.hand == nil || t.hand.id != c.handID {
		return
	}
	if t.hand.toAct != c.seat {
		return
	}
	s := t.hand.seats[c.seat]
	if s == nil {
		return
	}
	// Bank is fully consumed when we hit the timeout.
	if p := t.seats[c.seat]; p != nil {
		p.TimeBankMs = 0
	}
	auto := proto.ActFold
	if h := t.hand; s.bet >= h.currentBet {
		auto = proto.ActCheck
	}
	log.Printf("[table %s] hand %s seat %d timeout → %s",
		t.ID[:8], t.hand.id[:8], c.seat, auto)
	t.applyActionAndBroadcast(c.seat, auto, 0, false)
}

// maybeFinishStreetOrHand inspects the hand state after an action (or a
// forced fold from disconnect) and either advances the actor, schedules the
// next street, fast-forwards an all-in showdown, or settles the hand.
func (t *Table) maybeFinishStreetOrHand() {
	if t.hand == nil {
		return
	}
	h := t.hand

	// Hand ends immediately if only one non-folded seat remains.
	if h.activeNotFolded() <= 1 {
		t.endByFoldOut()
		return
	}

	if !h.roundClosed() {
		h.advance()
		t.broadcastState()
		t.broadcastToAct()
		return
	}

	// Round closed — let the actor advance the street after a short pause
	// for the client to render the round-end. advanceStreet will fast-
	// forward through subsequent streets when no one can act anymore.
	t.scheduleAfter(streetDelay, advanceStreetCmd{handID: h.id})
}

// advanceStreet deals the next street and reopens betting (or, if no one can
// bet, runs straight to settlement).
func (t *Table) advanceStreet() {
	h := t.hand
	if h.stage == proto.StageRiver {
		t.settleAndBroadcast()
		return
	}
	cards := h.dealStreet()
	t.broadcast(proto.DealCommunity{
		Envelope: proto.Envelope{Type: proto.MsgDealCommunity, Seq: t.nextSeq()},
		HandID:   h.id,
		Stage:    h.stage,
		Cards:    cards,
	})
	if !h.resetForNextStreet() {
		// no one can act on this street either — keep dealing.
		if h.stage == proto.StageRiver {
			t.settleAndBroadcast()
			return
		}
		t.scheduleAfter(streetDelay, advanceStreetCmd{handID: h.id})
		return
	}
	t.broadcastState()
	t.broadcastToAct()
}

// handleExtra is a placeholder for future cmd types; returns true if cmd
// was recognized.
func (t *Table) handleExtra(cmd any) bool {
	_ = cmd
	return false
}

func (t *Table) endByFoldOut() {
	winnerSeat, total := t.hand.awardSinglePot()
	winners := []proto.WinnerInfo{}
	if winnerSeat >= 0 {
		winners = append(winners, proto.WinnerInfo{Seat: winnerSeat, Amount: total})
	}
	t.broadcast(proto.Showdown{
		Envelope:  proto.Envelope{Type: proto.MsgShowdown, Seq: t.nextSeq()},
		HandID:    t.hand.id,
		Community: t.hand.community,
		Winners:   winners,
	})
	t.broadcast(proto.HandEnd{
		Envelope: proto.Envelope{Type: proto.MsgHandEnd, Seq: t.nextSeq()},
		HandID:   t.hand.id,
		Reason:   "fold_out",
		NextIn:   int(handEndDelay / time.Millisecond),
	})
	t.scheduleAfter(handEndDelay, endHandCmd{handID: t.hand.id})
}

func (t *Table) settleAndBroadcast() {
	h := t.hand
	pots, winners, reveals := h.settle()
	t.broadcastPotsExplicit(pots)
	t.broadcast(proto.Showdown{
		Envelope:  proto.Envelope{Type: proto.MsgShowdown, Seq: t.nextSeq()},
		HandID:    h.id,
		Community: h.community,
		Reveals:   reveals,
		Winners:   winners,
	})
	t.broadcast(proto.HandEnd{
		Envelope: proto.Envelope{Type: proto.MsgHandEnd, Seq: t.nextSeq()},
		HandID:   h.id,
		Reason:   "showdown",
		NextIn:   int(handEndDelay / time.Millisecond),
	})
	t.scheduleAfter(handEndDelay, endHandCmd{handID: h.id})
}

func (t *Table) broadcastPotUpdate() {
	if t.hand == nil {
		return
	}
	contrib := map[int]int{}
	folded := map[int]bool{}
	for _, s := range t.hand.seats {
		if s == nil {
			continue
		}
		if s.committed > 0 {
			contrib[s.seat] = s.committed
		}
		folded[s.seat] = s.folded
	}
	pots := eval.BuildPots(contrib, folded)
	t.broadcastPotsExplicit(pots)
}

func (t *Table) broadcastPotsExplicit(pots []eval.Pot) {
	if t.hand == nil {
		return
	}
	out := make([]proto.PotInfo, len(pots))
	tot := 0
	for i, p := range pots {
		out[i] = proto.PotInfo{Amount: p.Amount, Eligible: p.Eligible}
		tot += p.Amount
	}
	t.broadcast(proto.PotUpdate{
		Envelope: proto.Envelope{Type: proto.MsgPotUpdate, Seq: t.nextSeq()},
		HandID:   t.hand.id,
		Pots:     out,
		Total:    tot,
	})
}

func (t *Table) broadcastToAct() {
	if t.hand == nil || t.hand.toAct < 0 {
		return
	}
	// First, attempt to chain through any pre-actions that the seats waiting
	// to-act have armed. If a pre-action fires, it will recurse via
	// maybeFinishStreetOrHand → broadcastToAct, so we just return.
	if t.tryResolvePreActionLoop() {
		return
	}
	s := t.hand.seats[t.hand.toAct]
	if s == nil {
		return
	}
	toCall := t.hand.currentBet - s.bet
	if toCall < 0 {
		toCall = 0
	}
	bank := 0
	if p := t.seats[t.hand.toAct]; p != nil {
		bank = p.TimeBankMs
	}
	t.broadcast(proto.ToActMsg{
		Envelope:   proto.Envelope{Type: proto.MsgToAct, Seq: t.nextSeq()},
		HandID:     t.hand.id,
		Seat:       t.hand.toAct,
		TimeLeftMs: baseTurnMs,
		TimeBankMs: bank,
		MinRaise:   t.hand.minRaise,
		ToCall:     toCall,
	})
	t.armTurnTimer(t.hand.id, t.hand.toAct, baseTurnMs+bank)
}

// tryResolvePreActionLoop fires at most once for the current toAct seat. If
// the seat has an armed pre-action that resolves cleanly, it is applied and
// the resulting state change advances toAct (which the caller chain will
// re-broadcast). Returns true when a pre-action fired.
func (t *Table) tryResolvePreActionLoop() bool {
	if t.hand == nil || t.hand.toAct < 0 {
		return false
	}
	seat := t.hand.toAct
	action, amount, ok := t.hand.resolvePending(seat)
	if !ok {
		// No applicable pre-action — discard any stale slot so it doesn't
		// linger into a future round with mutated context.
		t.hand.clearPending(seat)
		return false
	}
	// Apply through the same path as a real action — pre-action does not
	// consume bank time (the player decided ahead of their turn).
	return t.applyActionAndBroadcast(seat, action, amount, false)
}

// armTurnTimer (re)arms the per-turn timer. Stops any previous timer first.
// Total budget is base + the seat's current time bank (caller computes).
func (t *Table) armTurnTimer(handID string, seat int, totalMs int) {
	t.cancelTurnTimer()
	if totalMs <= 0 {
		// Defensive: a player with 0 base time would never get to act, so
		// fall back to base.
		totalMs = baseTurnMs
	}
	t.turnHandID = handID
	t.turnSeat = seat
	t.turnStartAt = time.Now()
	t.turnBudgetMs = totalMs
	t.turnTimer = time.AfterFunc(time.Duration(totalMs)*time.Millisecond, func() {
		select {
		case t.cmdIn <- turnTimeoutCmd{handID: handID, seat: seat}:
		default:
			log.Printf("[table %s] dropped timeout cmd hand=%s seat=%d", t.ID[:8], handID[:8], seat)
		}
	})
}

func (t *Table) cancelTurnTimer() {
	if t.turnTimer != nil {
		t.turnTimer.Stop()
		t.turnTimer = nil
	}
	t.turnHandID = ""
	t.turnSeat = -1
	t.turnStartAt = time.Time{}
	t.turnBudgetMs = 0
}

// consumeBankFor deducts the portion of `elapsed` past base time from the
// player's time bank. Floors at 0. No-op unless the active timer is the one
// for `seat` in the current hand.
func (t *Table) consumeBankFor(seat int, elapsed time.Duration) {
	if t.hand == nil || t.turnHandID != t.hand.id || t.turnSeat != seat {
		return
	}
	p := t.seats[seat]
	if p == nil {
		return
	}
	used := int(elapsed/time.Millisecond) - baseTurnMs
	if used <= 0 {
		return
	}
	p.TimeBankMs -= used
	if p.TimeBankMs < 0 {
		p.TimeBankMs = 0
	}
}

func (t *Table) sendErrorByUser(userID, code, msg string) {
	for _, p := range t.seats {
		if p != nil && p.UserID == userID {
			t.sendTo(p, proto.ErrorMsg{
				Envelope: proto.Envelope{Type: proto.MsgError, Seq: t.nextSeq()},
				Code:     code,
				Message:  msg,
			})
			return
		}
	}
}
