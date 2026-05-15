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
	autoStartDelay   = 800 * time.Millisecond
	streetDelay      = 900 * time.Millisecond
	handEndDelay     = 2500 * time.Millisecond
	defaultBuyIn     = 1000
)

// Player is the server-side view of a seated client.
type Player struct {
	UserID   string
	Nickname string
	Seat     int
	Stack    int
	Send     chan<- any // per-client outbound channel (JSON encoded upstream)
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
					if t.hand.toAct == i {
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

// ---- seating ----

func (t *Table) seatPlayer(p *Player) bool {
	for i := 0; i < MaxSeats; i++ {
		if t.seats[i] == nil {
			p.Seat = i
			if p.Stack <= 0 {
				p.Stack = defaultBuyIn
			}
			t.seats[i] = p
			t.seatedCount.Add(1)
			log.Printf("[table %s] %s seated at %d (stack %d)", t.ID[:8], p.Nickname, i, p.Stack)
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
	// Sync each player's persistent stack from per-hand stack and clear
	// hand state.
	for i, s := range t.hand.seats {
		if s != nil && t.seats[i] != nil {
			t.seats[i].Stack = s.stack
		}
	}
	t.hand = nil
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
	actual, paid, err := t.hand.applyAction(seat, c.action, c.amount)
	if err != nil {
		t.sendErrorByUser(c.userID, "illegal_action", err.Error())
		return
	}
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
	s := t.hand.seats[t.hand.toAct]
	if s == nil {
		return
	}
	toCall := t.hand.currentBet - s.bet
	if toCall < 0 {
		toCall = 0
	}
	t.broadcast(proto.ToActMsg{
		Envelope:   proto.Envelope{Type: proto.MsgToAct, Seq: t.nextSeq()},
		HandID:     t.hand.id,
		Seat:       t.hand.toAct,
		TimeLeftMs: 0, // MVP: no time bank yet
		MinRaise:   t.hand.minRaise,
		ToCall:     toCall,
	})
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
