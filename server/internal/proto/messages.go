package proto

type MsgType string

const (
	MsgLogin         MsgType = "login"
	MsgLoginOK       MsgType = "login_ok"
	MsgSit           MsgType = "sit"
	MsgAction        MsgType = "action"
	MsgPreAction     MsgType = "pre_action"
	MsgTableState    MsgType = "table_state"
	MsgHandStart     MsgType = "hand_start"
	MsgHandEnd       MsgType = "hand_end"
	MsgDealHole      MsgType = "deal_hole"
	MsgDealCommunity MsgType = "deal_community"
	MsgActionApplied MsgType = "action_applied"
	MsgToAct         MsgType = "to_act"
	MsgPotUpdate     MsgType = "pot_update"
	MsgShowdown      MsgType = "showdown"
	MsgError         MsgType = "error"
)

// ActionType — values exchanged on the wire for a player's intent.
type ActionType string

const (
	ActFold      ActionType = "fold"
	ActCheck     ActionType = "check"
	ActCall      ActionType = "call"
	ActBet       ActionType = "bet"
	ActRaise     ActionType = "raise"
	ActAllIn     ActionType = "all_in"
	ActPostBlind ActionType = "post_blind"

	// PreActX — client pre-action intents. Only valid via MsgPreAction; stored
	// per seat and consumed when it becomes that seat's turn.
	ActPreCheckFold ActionType = "pre_check_fold"
	ActPreCallAny   ActionType = "pre_call_any"
	ActPreRaiseTo   ActionType = "pre_raise_to"
	ActPreClear     ActionType = "pre_clear"
)

// Stage — phases of a single hand.
type Stage string

const (
	StageWaiting  Stage = "waiting"
	StagePreflop  Stage = "preflop"
	StageFlop     Stage = "flop"
	StageTurn     Stage = "turn"
	StageRiver    Stage = "river"
	StageShowdown Stage = "showdown"
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

// ActionReq — client intent for the current acting seat.
type ActionReq struct {
	Envelope
	HandID string     `json:"hand_id"`
	Action ActionType `json:"action"`
	Amount int        `json:"amount,omitempty"`
}

// PreActionReq — client intent to arm or clear a pre-action. Action must be
// one of the ActPre* constants. Server validates and stores; the eventual
// concrete action is broadcast as ActionApplied when this seat's turn lands.
type PreActionReq struct {
	Envelope
	HandID string     `json:"hand_id"`
	Action ActionType `json:"action"`
	Amount int        `json:"amount,omitempty"`
}

type SeatInfo struct {
	Seat      int    `json:"seat"`
	UserID    string `json:"user_id"`
	Nickname  string `json:"nickname"`
	Stack     int    `json:"stack"`
	Bet       int    `json:"bet"`
	Committed int    `json:"committed"`
	Folded    bool   `json:"folded"`
	AllIn     bool   `json:"all_in"`
	SittingOut bool  `json:"sitting_out"`
}

type TableState struct {
	Envelope
	TableID    string     `json:"table_id"`
	Blinds     [2]int     `json:"blinds"`
	Seats      []SeatInfo `json:"seats"`
	YourSeat   int        `json:"your_seat"`
	HandID     string     `json:"hand_id,omitempty"`
	Community  []string   `json:"community,omitempty"`
	Pot        int        `json:"pot,omitempty"`
	Stage      Stage      `json:"stage,omitempty"`
	Button     int        `json:"button"`
	ToAct      int        `json:"to_act"`
	LastBet    int        `json:"last_bet"`
	MinRaise   int        `json:"min_raise"`
	TimeLeftMs int        `json:"time_left_ms,omitempty"`
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
	Stage  Stage    `json:"stage"`
	Cards  []string `json:"cards"`
}

// ActionApplied broadcasts the result of a validated player action.
type ActionApplied struct {
	Envelope
	HandID string     `json:"hand_id"`
	Seat   int        `json:"seat"`
	Action ActionType `json:"action"`
	Amount int        `json:"amount"`
	Stack  int        `json:"stack"`
	Bet    int        `json:"bet"`
}

// ToAct tells everyone whose turn it is and how much time is left.
// TimeLeftMs is the base turn budget; TimeBankMs is the personal time bank
// the player can additionally consume. Total deadline = TimeLeftMs + TimeBankMs.
type ToActMsg struct {
	Envelope
	HandID     string `json:"hand_id"`
	Seat       int    `json:"seat"`
	TimeLeftMs int    `json:"time_left_ms"`
	TimeBankMs int    `json:"time_bank_ms"`
	MinRaise   int    `json:"min_raise"`
	ToCall     int    `json:"to_call"`
}

// PotInfo describes a single (main or side) pot.
type PotInfo struct {
	Amount   int   `json:"amount"`
	Eligible []int `json:"eligible"`
}

type PotUpdate struct {
	Envelope
	HandID string    `json:"hand_id"`
	Pots   []PotInfo `json:"pots"`
	Total  int       `json:"total"`
}

// WinnerInfo per pot.
type WinnerInfo struct {
	Seat     int      `json:"seat"`
	Amount   int      `json:"amount"`
	HandRank string   `json:"hand_rank,omitempty"`
	Best5    []string `json:"best5,omitempty"`
}

type Showdown struct {
	Envelope
	HandID    string                 `json:"hand_id"`
	Community []string               `json:"community"`
	Reveals   map[int][]string       `json:"reveals,omitempty"` // seat -> hole cards revealed at showdown
	Winners   []WinnerInfo           `json:"winners"`
}

// HandEnd marks the end of the current hand (after showdown or fold-out).
type HandEnd struct {
	Envelope
	HandID  string `json:"hand_id"`
	Reason  string `json:"reason"` // "showdown" or "fold_out"
	NextIn  int    `json:"next_in_ms"`
}

type ErrorMsg struct {
	Envelope
	Code    string `json:"code"`
	Message string `json:"message"`
}
