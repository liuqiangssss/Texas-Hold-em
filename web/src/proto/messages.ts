// Keep in sync with server/internal/proto/messages.go

export type MsgType =
  | 'login'
  | 'login_ok'
  | 'sit'
  | 'sit_out'
  | 'sit_in'
  | 'leave'
  | 'action'
  | 'pre_action'
  | 'table_state'
  | 'hand_start'
  | 'hand_end'
  | 'deal_hole'
  | 'deal_community'
  | 'action_applied'
  | 'to_act'
  | 'pot_update'
  | 'showdown'
  | 'error';

export type ActionType =
  | 'fold'
  | 'check'
  | 'call'
  | 'bet'
  | 'raise'
  | 'all_in'
  | 'post_blind'
  | 'pre_check_fold'
  | 'pre_call_any'
  | 'pre_raise_to'
  | 'pre_clear';

export type Stage =
  | 'waiting'
  | 'preflop'
  | 'flop'
  | 'turn'
  | 'river'
  | 'showdown';

export interface Envelope {
  type: MsgType;
  seq?: number;
}

export interface LoginReq extends Envelope {
  type: 'login';
  nickname: string;
}

export interface LoginOK extends Envelope {
  type: 'login_ok';
  user_id: string;
  nickname: string;
}

export interface SitReq extends Envelope {
  type: 'sit';
  table_id?: string;
  blinds: [number, number];
}

export interface SitOutReq extends Envelope {
  type: 'sit_out';
}

export interface SitInReq extends Envelope {
  type: 'sit_in';
}

export interface LeaveReq extends Envelope {
  type: 'leave';
}

export interface ActionReq extends Envelope {
  type: 'action';
  hand_id: string;
  action: ActionType;
  amount?: number;
}

export interface PreActionReq extends Envelope {
  type: 'pre_action';
  hand_id: string;
  action: ActionType;
  amount?: number;
}

export interface SeatInfo {
  seat: number;
  user_id: string;
  nickname: string;
  stack: number;
  bet: number;
  committed: number;
  folded: boolean;
  all_in: boolean;
  sitting_out: boolean;
  must_post_bb: boolean;
  missed_hands?: number;
}

export interface TableState extends Envelope {
  type: 'table_state';
  table_id: string;
  blinds: [number, number];
  seats: SeatInfo[];
  your_seat: number;
  hand_id?: string;
  community?: string[];
  pot?: number;
  stage?: Stage;
  button: number;
  to_act: number;
  last_bet: number;
  min_raise: number;
  time_left_ms?: number;
}

export interface HandStart extends Envelope {
  type: 'hand_start';
  hand_id: string;
  table_id: string;
  button: number;
  blinds: [number, number];
  dealer_msg?: string;
}

export interface DealHole extends Envelope {
  type: 'deal_hole';
  hand_id: string;
  cards: string[];
}

export interface DealCommunity extends Envelope {
  type: 'deal_community';
  hand_id: string;
  stage: Stage;
  cards: string[];
}

export interface ActionApplied extends Envelope {
  type: 'action_applied';
  hand_id: string;
  seat: number;
  action: ActionType;
  amount: number;
  stack: number;
  bet: number;
}

export interface ToActMsg extends Envelope {
  type: 'to_act';
  hand_id: string;
  seat: number;
  time_left_ms: number;
  time_bank_ms: number;
  min_raise: number;
  to_call: number;
}

export interface PotInfo {
  amount: number;
  eligible: number[];
}

export interface PotUpdate extends Envelope {
  type: 'pot_update';
  hand_id: string;
  pots: PotInfo[];
  total: number;
}

export interface WinnerInfo {
  seat: number;
  amount: number;
  hand_rank?: string;
  best5?: string[];
}

export interface ShowdownMsg extends Envelope {
  type: 'showdown';
  hand_id: string;
  community: string[];
  reveals?: Record<number, string[]>;
  winners: WinnerInfo[];
}

export interface HandEndMsg extends Envelope {
  type: 'hand_end';
  hand_id: string;
  reason: string;
  next_in_ms: number;
}

export interface ErrorMsg extends Envelope {
  type: 'error';
  code: string;
  message: string;
}

export type ServerMsg =
  | LoginOK
  | TableState
  | HandStart
  | HandEndMsg
  | DealHole
  | DealCommunity
  | ActionApplied
  | ToActMsg
  | PotUpdate
  | ShowdownMsg
  | ErrorMsg;
export type ClientMsg =
  | LoginReq
  | SitReq
  | SitOutReq
  | SitInReq
  | LeaveReq
  | ActionReq
  | PreActionReq;
