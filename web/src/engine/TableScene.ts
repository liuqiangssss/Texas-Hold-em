import { Application, Container, Graphics, Text, TextStyle } from 'pixi.js';
import type { SeatInfo, WinnerInfo } from '../proto/messages';

const SEAT_POSITIONS: Array<{ xRatio: number; yRatio: number }> = [
  { xRatio: 0.5, yRatio: 0.88 },  // 0 - self (bottom center)
  { xRatio: 0.18, yRatio: 0.75 }, // 1 - bottom-left
  { xRatio: 0.12, yRatio: 0.4 },  // 2 - left
  { xRatio: 0.5, yRatio: 0.15 },  // 3 - top
  { xRatio: 0.88, yRatio: 0.4 },  // 4 - right
  { xRatio: 0.82, yRatio: 0.75 }, // 5 - bottom-right
];

export class TableScene {
  readonly app: Application;
  private root: Container;
  private seatLayer: Container;
  private communityLayer: Container;
  private holeLayer: Container;
  private revealLayer: Container;
  private holeCards: Graphics[] = [];

  private yourSeat = -1;
  private button = -1;
  private toActSeat = -1;

  constructor(canvas: HTMLCanvasElement) {
    this.app = new Application({
      view: canvas,
      resizeTo: canvas.parentElement ?? window,
      backgroundColor: 0x0f3b2e,
      antialias: true,
      autoDensity: true,
      resolution: window.devicePixelRatio || 1,
    });

    this.root = new Container();
    this.app.stage.addChild(this.root);

    const felt = new Graphics();
    this.root.addChild(felt);

    this.seatLayer = new Container();
    this.communityLayer = new Container();
    this.holeLayer = new Container();
    this.revealLayer = new Container();
    this.root.addChild(this.seatLayer, this.communityLayer, this.holeLayer, this.revealLayer);

    this.drawFelt(felt);
    this.app.renderer.on('resize', () => this.drawFelt(felt));
  }

  destroy(): void {
    this.app.destroy(true, { children: true });
  }

  setYourSeat(seat: number): void {
    this.yourSeat = seat;
  }

  setButton(seat: number): void {
    this.button = seat;
  }

  setToAct(seat: number): void {
    this.toActSeat = seat;
  }

  renderSeats(seats: SeatInfo[]): void {
    this.seatLayer.removeChildren();

    const yourSeat = this.yourSeat;
    for (const s of seats) {
      const slot = TableScene.rotateSeat(s.seat, yourSeat);
      const pos = SEAT_POSITIONS[slot];
      const w = this.app.renderer.width;
      const h = this.app.renderer.height;
      const cx = w * pos.xRatio;
      const cy = h * pos.yRatio;

      const isToAct = s.seat === this.toActSeat;
      const isButton = s.seat === this.button;

      const chip = new Graphics();
      const borderColor = isToAct ? 0xffd54f : s.folded ? 0x404040 : 0x4f7cff;
      const fillColor = s.folded ? 0x141414 : 0x1d2545;
      chip.beginFill(fillColor, s.folded ? 0.6 : 1);
      chip.lineStyle(isToAct ? 3 : 2, borderColor, 1);
      chip.drawRoundedRect(-72, -28, 144, 56, 10);
      chip.endFill();
      chip.x = cx;
      chip.y = cy;

      const nick = new Text(
        s.nickname + (s.seat === yourSeat ? '（你）' : '') + (isButton ? ' Ⓓ' : ''),
        new TextStyle({ fill: '#ffffff', fontSize: 13, fontWeight: '600' }),
      );
      nick.anchor.set(0.5, 0);
      nick.x = cx;
      nick.y = cy - 22;

      const stack = new Text(
        s.folded ? '已弃牌' : `筹码 ${s.stack}${s.all_in ? ' · All-in' : ''}`,
        new TextStyle({ fill: s.folded ? '#666' : '#a8b5e0', fontSize: 12 }),
      );
      stack.anchor.set(0.5, 0);
      stack.x = cx;
      stack.y = cy + 0;

      this.seatLayer.addChild(chip, nick, stack);

      // Bet display in front of seat (toward pot).
      if (s.bet > 0) {
        const bet = new Text(
          `▶ ${s.bet}`,
          new TextStyle({ fill: '#ffe082', fontSize: 12, fontWeight: '700' }),
        );
        bet.anchor.set(0.5, 0);
        bet.x = cx;
        bet.y = cy + 28;
        this.seatLayer.addChild(bet);
      }
    }
  }

  renderHoleCards(cards: string[]): void {
    for (const c of this.holeCards) c.destroy();
    this.holeCards = [];
    this.holeLayer.removeChildren();
    if (cards.length === 0) return;

    const w = this.app.renderer.width;
    const h = this.app.renderer.height;
    const cx = w * 0.5;
    const cy = h * 0.95;

    cards.forEach((c, i) => {
      const card = TableScene.buildCard(c);
      card.x = cx + (i - (cards.length - 1) / 2) * 52;
      card.y = cy - 40;
      this.holeLayer.addChild(card);
      this.holeCards.push(card);
    });
  }

  renderCommunity(cards: string[]): void {
    this.communityLayer.removeChildren();
    if (cards.length === 0) return;
    const w = this.app.renderer.width;
    const h = this.app.renderer.height;
    const cx = w * 0.5;
    const cy = h * 0.5;

    cards.forEach((c, i) => {
      const card = TableScene.buildCard(c);
      card.x = cx + (i - 2) * 52;
      card.y = cy;
      this.communityLayer.addChild(card);
    });
  }

  renderShowdown(showdown: { winners: WinnerInfo[]; reveals: Record<number, string[]> } | null): void {
    this.revealLayer.removeChildren();
    if (!showdown) return;

    const w = this.app.renderer.width;
    const h = this.app.renderer.height;

    // Draw revealed cards next to each non-folded seat.
    for (const seatStr of Object.keys(showdown.reveals)) {
      const seat = Number(seatStr);
      if (seat === this.yourSeat) continue; // already shown via hole layer
      const cards = showdown.reveals[seat];
      const slot = TableScene.rotateSeat(seat, this.yourSeat);
      const pos = SEAT_POSITIONS[slot];
      const cx = w * pos.xRatio;
      const cy = h * pos.yRatio - 60;
      cards.forEach((c, i) => {
        const card = TableScene.buildCard(c);
        card.scale.set(0.7);
        card.x = cx + (i - (cards.length - 1) / 2) * 38;
        card.y = cy;
        this.revealLayer.addChild(card);
      });
    }

    // Highlight winners.
    for (const w of showdown.winners) {
      const slot = TableScene.rotateSeat(w.seat, this.yourSeat);
      const pos = SEAT_POSITIONS[slot];
      const cw = this.app.renderer.width;
      const ch = this.app.renderer.height;
      const cx = cw * pos.xRatio;
      const cy = ch * pos.yRatio;
      const halo = new Graphics();
      halo.lineStyle(3, 0xffd54f, 1);
      halo.drawRoundedRect(-78, -34, 156, 68, 12);
      halo.x = cx;
      halo.y = cy;
      this.revealLayer.addChild(halo);

      const winLabel = new Text(
        `+${w.amount}${w.hand_rank ? ' ' + w.hand_rank : ''}`,
        new TextStyle({ fill: '#ffd54f', fontSize: 13, fontWeight: '700' }),
      );
      winLabel.anchor.set(0.5, 0);
      winLabel.x = cx;
      winLabel.y = cy + 38;
      this.revealLayer.addChild(winLabel);
    }
  }

  clearHand(): void {
    this.renderCommunity([]);
    this.renderHoleCards([]);
    this.revealLayer.removeChildren();
  }

  private drawFelt(felt: Graphics): void {
    felt.clear();
    const w = this.app.renderer.width;
    const h = this.app.renderer.height;
    felt.lineStyle(4, 0x2a6b4f, 1);
    felt.beginFill(0x155e46);
    felt.drawRoundedRect(w * 0.06, h * 0.22, w * 0.88, h * 0.5, 80);
    felt.endFill();
  }

  private static rotateSeat(seatIndex: number, yourSeat: number): number {
    if (yourSeat < 0) return seatIndex;
    return (seatIndex - yourSeat + 6) % 6;
  }

  private static buildCard(code: string): Graphics {
    const g = new Graphics();
    g.beginFill(0xffffff);
    g.lineStyle(1, 0x1b1b1b);
    g.drawRoundedRect(-24, -36, 48, 72, 6);
    g.endFill();

    const rank = code[0];
    const suit = code[1];
    const suitChar = { s: '♠', h: '♥', d: '♦', c: '♣' }[suit] ?? '?';
    const red = suit === 'h' || suit === 'd';
    const fill = red ? '#d32f2f' : '#111';

    const rankText = new Text(rank, new TextStyle({ fill, fontSize: 18, fontWeight: '700' }));
    rankText.anchor.set(0.5, 0);
    rankText.x = 0;
    rankText.y = -32;

    const suitText = new Text(suitChar, new TextStyle({ fill, fontSize: 26 }));
    suitText.anchor.set(0.5, 1);
    suitText.x = 0;
    suitText.y = 30;

    g.addChild(rankText, suitText);
    return g;
  }
}
