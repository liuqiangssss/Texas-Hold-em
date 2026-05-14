package proto

type MsgType string

const (
	MsgLogin         MsgType = "login"
	MsgLoginOK       MsgType = "login_ok"
	MsgSit           MsgType = "sit"
	MsgTableState    MsgType = "table_state"
	MsgHandStart     MsgType = "hand_start"
	MsgDealHole      MsgType = "deal_hole"
	MsgDealCommunity MsgType = "deal_community"
	MsgError         MsgType = "error"
)

type Envelope struct {
	Type MsgType `json:"type"`
	Seq  uint64  `json:"seq"`
}

type LoginReq struct {
	Envelope
	Nickname string `json:"nickname"`
}

type LoginOK struct {
	Envelope
	UserID   string `json:"user_id"`
	Nickname string `json:"nickname"`
}

type SitReq struct {
	Envelope
	TableID string `json:"table_id,omitempty"`
	Blinds  [2]int `json:"blinds"`
}

type SeatInfo struct {
	Seat     int    `json:"seat"`
	UserID   string `json:"user_id"`
	Nickname string `json:"nickname"`
	Stack    int    `json:"stack"`
}

type TableState struct {
	Envelope
	TableID   string     `json:"table_id"`
	Blinds    [2]int     `json:"blinds"`
	Seats     []SeatInfo `json:"seats"`
	YourSeat  int        `json:"your_seat"`
	HandID    string     `json:"hand_id,omitempty"`
	Community []string   `json:"community,omitempty"`
	Pot       int        `json:"pot,omitempty"`
	Stage     string     `json:"stage,omitempty"`
}

type HandStart struct {
	Envelope
	HandID    string `json:"hand_id"`
	TableID   string `json:"table_id"`
	Button    int    `json:"button"`
	Blinds    [2]int `json:"blinds"`
	DealerMsg string `json:"dealer_msg,omitempty"`
}

type DealHole struct {
	Envelope
	HandID string   `json:"hand_id"`
	Cards  []string `json:"cards"`
}

type DealCommunity struct {
	Envelope
	HandID string   `json:"hand_id"`
	Stage  string   `json:"stage"`
	Cards  []string `json:"cards"`
}

type ErrorMsg struct {
	Envelope
	Code    string `json:"code"`
	Message string `json:"message"`
}
