import { create } from 'zustand';
import type { SeatInfo } from '../proto/messages';

export type Scene = 'login' | 'lobby' | 'table';

interface SessionState {
  scene: Scene;
  userId: string | null;
  nickname: string | null;

  tableId: string | null;
  blinds: [number, number];
  seats: SeatInfo[];
  yourSeat: number;
  handId: string | null;
  holeCards: string[];
  dealerMsg: string;

  lastError: string | null;

  setScene: (s: Scene) => void;
  setLogin: (userId: string, nickname: string) => void;
  applyTableState: (tableId: string, blinds: [number, number], seats: SeatInfo[], yourSeat: number) => void;
  applyHandStart: (handId: string, dealerMsg?: string) => void;
  applyHole: (cards: string[]) => void;
  setError: (msg: string | null) => void;
  reset: () => void;
}

const initialTable = {
  tableId: null,
  blinds: [5, 10] as [number, number],
  seats: [] as SeatInfo[],
  yourSeat: -1,
  handId: null,
  holeCards: [] as string[],
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
  applyTableState: (tableId, blinds, seats, yourSeat) =>
    set({ tableId, blinds, seats, yourSeat, scene: 'table' }),
  applyHandStart: (handId, dealerMsg) =>
    set({ handId, holeCards: [], dealerMsg: dealerMsg ?? '' }),
  applyHole: (cards) => set({ holeCards: cards }),
  setError: (lastError) => set({ lastError }),
  reset: () => set({ scene: 'login', userId: null, nickname: null, ...initialTable, lastError: null }),
}));
