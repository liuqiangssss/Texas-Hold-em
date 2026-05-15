import { create } from 'zustand';
import type {
  ActionType,
  PotInfo,
  SeatInfo,
  Stage,
  WinnerInfo,
} from '../proto/messages';

export type Scene = 'login' | 'lobby' | 'table';

export interface ToActInfo {
  seat: number;
  toCall: number;
  minRaise: number;
}

interface SessionState {
  scene: Scene;
  userId: string | null;
  nickname: string | null;

  tableId: string | null;
  blinds: [number, number];
  seats: SeatInfo[];
  yourSeat: number;
  button: number;
  handId: string | null;
  stage: Stage;
  community: string[];
  holeCards: string[];
  pot: number;
  pots: PotInfo[];
  toAct: ToActInfo | null;
  lastAction: { seat: number; action: ActionType; amount: number } | null;
  showdown: { winners: WinnerInfo[]; reveals: Record<number, string[]> } | null;
  dealerMsg: string;

  lastError: string | null;

  setScene: (s: Scene) => void;
  setLogin: (userId: string, nickname: string) => void;
  applyTableState: (msg: {
    table_id: string;
    blinds: [number, number];
    seats: SeatInfo[];
    your_seat: number;
    button: number;
    hand_id?: string;
    community?: string[];
    pot?: number;
    stage?: Stage;
    to_act: number;
  }) => void;
  applyHandStart: (handId: string, dealerMsg?: string, button?: number) => void;
  applyHandEnd: () => void;
  applyHole: (cards: string[]) => void;
  applyCommunity: (cards: string[]) => void;
  applyAction: (seat: number, action: ActionType, amount: number, stack: number, bet: number) => void;
  applyToAct: (info: ToActInfo) => void;
  applyPot: (pots: PotInfo[], total: number) => void;
  applyShowdown: (winners: WinnerInfo[], reveals: Record<number, string[]>, community: string[]) => void;
  setError: (msg: string | null) => void;
  reset: () => void;
}

const initialTable = {
  tableId: null as string | null,
  blinds: [5, 10] as [number, number],
  seats: [] as SeatInfo[],
  yourSeat: -1,
  button: -1,
  handId: null as string | null,
  stage: 'waiting' as Stage,
  community: [] as string[],
  holeCards: [] as string[],
  pot: 0,
  pots: [] as PotInfo[],
  toAct: null as ToActInfo | null,
  lastAction: null as SessionState['lastAction'],
  showdown: null as SessionState['showdown'],
  dealerMsg: '',
};

export const useSession = create<SessionState>((set) => ({
  scene: 'login',
  userId: null,
  nickname: null,
  ...initialTable,
  lastError: null,

  setScene: (scene) => set({ scene }),
  setLogin: (userId, nickname) => set({ userId, nickname, scene: 'lobby' }),

  applyTableState: (msg) =>
    set((prev) => ({
      tableId: msg.table_id,
      blinds: msg.blinds,
      seats: msg.seats,
      yourSeat: msg.your_seat,
      button: msg.button,
      handId: msg.hand_id ?? null,
      community: msg.community ?? [],
      pot: msg.pot ?? 0,
      stage: msg.stage ?? 'waiting',
      // keep existing holeCards/showdown state across snapshot updates
      holeCards: prev.holeCards,
      showdown: prev.showdown,
      scene: 'table',
    })),

  applyHandStart: (handId, dealerMsg, button) =>
    set({
      handId,
      stage: 'preflop',
      community: [],
      holeCards: [],
      pot: 0,
      pots: [],
      lastAction: null,
      showdown: null,
      dealerMsg: dealerMsg ?? '',
      button: button ?? -1,
    }),

  applyHandEnd: () =>
    set({
      toAct: null,
    }),

  applyHole: (cards) => set({ holeCards: cards }),

  applyCommunity: (cards) =>
    set((prev) => ({ community: [...prev.community, ...cards] })),

  applyAction: (seat, action, amount, stack, bet) =>
    set((prev) => ({
      lastAction: { seat, action, amount },
      seats: prev.seats.map((s) =>
        s.seat === seat ? { ...s, stack, bet, folded: action === 'fold' ? true : s.folded, all_in: action === 'all_in' ? true : s.all_in } : s,
      ),
    })),

  applyToAct: (info) => set({ toAct: info }),

  applyPot: (pots, total) => set({ pots, pot: total }),

  applyShowdown: (winners, reveals, community) =>
    set({ showdown: { winners, reveals }, community }),

  setError: (lastError) => set({ lastError }),

  reset: () => set({ scene: 'login', userId: null, nickname: null, ...initialTable, lastError: null }),
}));
