package ws

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/liuqiangssss/texas-holdem/server/internal/proto"
	"github.com/liuqiangssss/texas-holdem/server/internal/table"
	"nhooyr.io/websocket"
)

// Handler serves /ws and bridges a browser client to a table actor.
type Handler struct {
	Manager *table.Manager
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true, // MVP: dev only, allow any origin
	})
	if err != nil {
		log.Printf("[ws] accept: %v", err)
		return
	}
	defer c.Close(websocket.StatusNormalClosure, "bye")

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	session := &clientSession{
		conn:    c,
		sendCh:  make(chan any, 32),
		manager: h.Manager,
	}

	go session.writer(ctx)

	if err := session.reader(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Printf("[ws] reader closed: %v", err)
	}

	// On disconnect, remove from any table we were seated on.
	if session.table != nil {
		session.table.Leave(session.userID)
	}
}

type clientSession struct {
	conn    *websocket.Conn
	sendCh  chan any
	manager *table.Manager

	userID   string
	nickname string
	table    *table.Table
}

func (s *clientSession) writer(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case msg, ok := <-s.sendCh:
			if !ok {
				return
			}
			b, err := json.Marshal(msg)
			if err != nil {
				log.Printf("[ws] marshal: %v", err)
				continue
			}
			wctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			if err := s.conn.Write(wctx, websocket.MessageText, b); err != nil {
				cancel()
				return
			}
			cancel()
		}
	}
}

func (s *clientSession) reader(ctx context.Context) error {
	for {
		_, data, err := s.conn.Read(ctx)
		if err != nil {
			return err
		}

		var env proto.Envelope
		if err := json.Unmarshal(data, &env); err != nil {
			s.sendError("bad_frame", "cannot parse envelope")
			continue
		}

		switch env.Type {
		case proto.MsgLogin:
			var req proto.LoginReq
			if err := json.Unmarshal(data, &req); err != nil {
				s.sendError("bad_frame", "login payload invalid")
				continue
			}
			nick := req.Nickname
			if nick == "" {
				nick = "Guest"
			}
			s.userID = uuid.NewString()
			s.nickname = nick
			s.sendCh <- proto.LoginOK{
				Envelope: proto.Envelope{Type: proto.MsgLoginOK},
				UserID:   s.userID,
				Nickname: s.nickname,
			}

		case proto.MsgSit:
			if s.userID == "" {
				s.sendError("not_logged_in", "send login first")
				continue
			}
			var req proto.SitReq
			if err := json.Unmarshal(data, &req); err != nil {
				s.sendError("bad_frame", "sit payload invalid")
				continue
			}
			if req.Blinds == [2]int{0, 0} {
				req.Blinds = [2]int{5, 10}
			}
			t := s.manager.FindOrCreate(req.Blinds)
			p := &table.Player{
				UserID:   s.userID,
				Nickname: s.nickname,
				Stack:    100 * req.Blinds[1], // MVP: 100 BB buy-in
				Send:     s.sendCh,
			}
			if ok := t.Sit(p); !ok {
				s.sendError("table_full", "no seat available")
				continue
			}
			s.table = t

		case proto.MsgAction:
			if s.table == nil {
				s.sendError("not_at_table", "sit at a table first")
				continue
			}
			var req proto.ActionReq
			if err := json.Unmarshal(data, &req); err != nil {
				s.sendError("bad_frame", "action payload invalid")
				continue
			}
			s.table.Action(s.userID, req.HandID, req.Action, req.Amount)

		default:
			s.sendError("unknown_type", string(env.Type))
		}
	}
}

func (s *clientSession) sendError(code, msg string) {
	s.sendCh <- proto.ErrorMsg{
		Envelope: proto.Envelope{Type: proto.MsgError},
		Code:     code,
		Message:  msg,
	}
}
