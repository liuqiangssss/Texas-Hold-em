package table

import (
	"context"
	"log"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/liuqiangssss/texas-holdem/server/internal/deck"
	"github.com/liuqiangssss/texas-holdem/server/internal/proto"
)

const MaxSeats = 6

// Player is the server-side view of a seated client.
type Player struct {
	UserID   string
	Nickname string
	Seat     int
	Stack    int
	Send     chan<- any // per-client outbound channel (JSON encoded upstream)
}

// sitCmd is an internal actor command for seating a player.
type sitCmd struct {
	player *Player
	done   chan bool
}

// leaveCmd removes a player by user id.
type leaveCmd struct {
	userID string
}

// Table is a single 6-max poker table running in its own goroutine (actor).
type Table struct {
	ID     string
	Blinds [2]int

	seats       [MaxSeats]*Player
	handID      string
	button      int
	seqCtr      atomic.Uint64
	seatedCount atomic.Int32 // snapshot for matchmaking, written by actor goroutine only

	cmdIn chan any
}

func New(blinds [2]int) *Table {
	t := &Table{
		ID:     uuid.NewString(),
		Blinds: blinds,
		cmdIn:  make(chan any, 32),
		button: -1,
	}
	return t
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
			if t.activePlayers() >= 2 && t.handID == "" {
				// MVP: auto-start a hand as soon as we have two players.
				time.AfterFunc(500*time.Millisecond, func() {
					t.cmdIn <- startHandCmd{}
				})
			}
		}
	case leaveCmd:
		for i, p := range t.seats {
			if p != nil && p.UserID == c.userID {
				t.seats[i] = nil
				t.seatedCount.Add(-1)
				log.Printf("[table %s] %s left seat %d", t.ID[:8], p.Nickname, i)
				t.broadcastState()
				return
			}
		}
	case startHandCmd:
		t.startHand()
	}
}

// Sit enqueues a seating request. Blocks until accepted/rejected.
func (t *Table) Sit(p *Player) bool {
	done := make(chan bool, 1)
	t.cmdIn <- sitCmd{player: p, done: done}
	return <-done
}

// Leave enqueues a leave request (non-blocking).
func (t *Table) Leave(userID string) {
	t.cmdIn <- leaveCmd{userID: userID}
}

func (t *Table) seatPlayer(p *Player) bool {
	for i := 0; i < MaxSeats; i++ {
		if t.seats[i] == nil {
			p.Seat = i
			t.seats[i] = p
			t.seatedCount.Add(1)
			log.Printf("[table %s] %s seated at %d", t.ID[:8], p.Nickname, i)
			return true
		}
	}
	return false
}

func (t *Table) activePlayers() int {
	n := 0
	for _, p := range t.seats {
		if p != nil {
			n++
		}
	}
	return n
}

func (t *Table) nextSeq() uint64 {
	return t.seqCtr.Add(1)
}

func (t *Table) snapshot() proto.TableState {
	seats := make([]proto.SeatInfo, 0, MaxSeats)
	for _, p := range t.seats {
		if p == nil {
			continue
		}
		seats = append(seats, proto.SeatInfo{
			Seat:     p.Seat,
			UserID:   p.UserID,
			Nickname: p.Nickname,
			Stack:    p.Stack,
		})
	}
	return proto.TableState{
		Envelope: proto.Envelope{Type: proto.MsgTableState, Seq: t.nextSeq()},
		TableID:  t.ID,
		Blinds:   t.Blinds,
		Seats:    seats,
		HandID:   t.handID,
		Stage:    "waiting",
	}
}

func (t *Table) broadcastState() {
	state := t.snapshot()
	for _, p := range t.seats {
		if p == nil {
			continue
		}
		personal := state
		personal.YourSeat = p.Seat
		select {
		case p.Send <- personal:
		default:
			log.Printf("[table %s] drop state to %s (slow client)", t.ID[:8], p.Nickname)
		}
	}
}

// ---------- hand lifecycle (MVP: deal hole cards only) ----------

type startHandCmd struct{}

func (t *Table) startHand() {
	if t.activePlayers() < 2 {
		return
	}
	t.handID = uuid.NewString()
	t.button = (t.button + 1) % MaxSeats
	for t.seats[t.button] == nil {
		t.button = (t.button + 1) % MaxSeats
	}
	log.Printf("[table %s] hand %s start, button=%d", t.ID[:8], t.handID[:8], t.button)

	// Broadcast hand_start.
	startMsg := proto.HandStart{
		Envelope:  proto.Envelope{Type: proto.MsgHandStart, Seq: t.nextSeq()},
		HandID:    t.handID,
		TableID:   t.ID,
		Button:    t.button,
		Blinds:    t.Blinds,
		DealerMsg: "shuffling and dealing...",
	}
	t.broadcast(startMsg)

	// Shuffle & deal two hole cards per active player (serverside only,
	// cards pushed privately to each player). Cards are alternated per
	// real poker dealing, though for MVP the visible order is what matters.
	d := deck.NewDeck()
	deck.Shuffle(d)
	cardCursor := 0
	for i := 0; i < MaxSeats; i++ {
		seatIdx := (t.button + 1 + i) % MaxSeats
		p := t.seats[seatIdx]
		if p == nil {
			continue
		}
		hole := []string{d[cardCursor], d[cardCursor+1]}
		cardCursor += 2
		msg := proto.DealHole{
			Envelope: proto.Envelope{Type: proto.MsgDealHole, Seq: t.nextSeq()},
			HandID:   t.handID,
			Cards:    hole,
		}
		select {
		case p.Send <- msg:
		default:
			log.Printf("[table %s] drop deal_hole to %s", t.ID[:8], p.Nickname)
		}
		log.Printf("[table %s] dealt hole to %s (seat %d): %v", t.ID[:8], p.Nickname, p.Seat, hole)
	}
}

func (t *Table) broadcast(msg any) {
	for _, p := range t.seats {
		if p == nil {
			continue
		}
		select {
		case p.Send <- msg:
		default:
		}
	}
}
