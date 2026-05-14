// Keep in sync with server/internal/proto/messages.go

export type MsgType =
  | 'login'
  | 'login_ok'
  | 'sit'
  | 'table_state'
  | 'hand_start'
  | 'deal_hole'
  | 'deal_community'
  | 'error';

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

export interface SeatInfo {
  seat: number;
  user_id: string;
  nickname: string;
  stack: number;
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
  stage?: string;
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
  stage: string;
  cards: string[];
}

export interface ErrorMsg extends Envelope {
  type: 'error';
  code: string;
  message: string;
}

export type ServerMsg = LoginOK | TableState | HandStart | DealHole | DealCommunity | ErrorMsg;
export type ClientMsg = LoginReq | SitReq;
