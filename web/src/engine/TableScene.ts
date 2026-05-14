import { Application, Container, Graphics, Text, TextStyle } from 'pixi.js';
import type { SeatInfo } from '../proto/messages';

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
  private holeCards: Graphics[] = [];

  private yourSeat = -1;

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

    // Table felt oval.
    const felt = new Graphics();
    this.root.addChild(felt);

    this.seatLayer = new Container();
    this.communityLayer = new Container();
    this.holeLayer = new Container();
    this.root.addChild(this.seatLayer, this.communityLayer, this.holeLayer);

    this.drawFelt(felt);
    this.app.renderer.on('resize', () => this.drawFelt(felt));
  }

  destroy(): void {
    this.app.destroy(true, { children: true });
  }

  setYourSeat(seat: number): void {
    this.yourSeat = seat;
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

      const chip = new Graphics();
      chip.beginFill(0x1d2545);
      chip.lineStyle(2, 0x4f7cff, 1);
      chip.drawRoundedRect(-70, -26, 140, 52, 10);
      chip.endFill();
      chip.x = cx;
      chip.y = cy;

      const nick = new Text(
        s.nickname + (s.seat === yourSeat ? '（你）' : ''),
        new TextStyle({ fill: '#ffffff', fontSize: 13, fontWeight: '600' }),
      );
      nick.anchor.set(0.5, 0);
      nick.x = cx;
      nick.y = cy - 20;

      const stack = new Text(
        `筹码 ${s.stack}`,
        new TextStyle({ fill: '#a8b5e0', fontSize: 12 }),
      );
      stack.anchor.set(0.5, 0);
      stack.x = cx;
      stack.y = cy + 2;

      this.seatLayer.addChild(chip, nick, stack);
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

  clearHand(): void {
    this.renderCommunity([]);
    this.renderHoleCards([]);
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
    // Position 0 is your bottom-center seat; rotate so your seat maps to 0.
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
