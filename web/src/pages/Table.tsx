import { useEffect, useRef } from 'react';
import { useSession } from '../store/session';
import { TableScene } from '../engine/TableScene';

export function Table() {
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const sceneRef = useRef<TableScene | null>(null);

  const seats = useSession((s) => s.seats);
  const yourSeat = useSession((s) => s.yourSeat);
  const holeCards = useSession((s) => s.holeCards);
  const blinds = useSession((s) => s.blinds);
  const handId = useSession((s) => s.handId);
  const dealerMsg = useSession((s) => s.dealerMsg);

  useEffect(() => {
    if (!canvasRef.current) return;
    const scene = new TableScene(canvasRef.current);
    sceneRef.current = scene;
    return () => {
      scene.destroy();
      sceneRef.current = null;
    };
  }, []);

  useEffect(() => {
    sceneRef.current?.setYourSeat(yourSeat);
    sceneRef.current?.renderSeats(seats);
  }, [seats, yourSeat]);

  useEffect(() => {
    sceneRef.current?.renderHoleCards(holeCards);
  }, [holeCards]);

  return (
    <div className="pixi-wrap">
      <canvas ref={canvasRef} style={{ width: '100%', height: '100%', display: 'block' }} />
      <div className="hud">
        <div>
          <span className="label">盲注</span>
          {blinds[0]} / {blinds[1]}
        </div>
        <div>
          <span className="label">玩家</span>
          {seats.length} / 6
        </div>
        <div>
          <span className="label">手牌</span>
          {handId ? handId.slice(0, 8) : '等待开始...'}
        </div>
        {dealerMsg ? <div className="muted" style={{ marginTop: 6 }}>{dealerMsg}</div> : null}
      </div>
    </div>
  );
}
