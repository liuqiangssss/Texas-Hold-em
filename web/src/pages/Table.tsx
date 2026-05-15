import { useEffect, useRef } from 'react';
import { useSession } from '../store/session';
import { TableScene } from '../engine/TableScene';
import { ActionBar } from './ActionBar';

export function Table() {
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const sceneRef = useRef<TableScene | null>(null);

  const seats = useSession((s) => s.seats);
  const yourSeat = useSession((s) => s.yourSeat);
  const holeCards = useSession((s) => s.holeCards);
  const blinds = useSession((s) => s.blinds);
  const handId = useSession((s) => s.handId);
  const dealerMsg = useSession((s) => s.dealerMsg);
  const community = useSession((s) => s.community);
  const button = useSession((s) => s.button);
  const toAct = useSession((s) => s.toAct);
  const pot = useSession((s) => s.pot);
  const stage = useSession((s) => s.stage);
  const showdown = useSession((s) => s.showdown);
  const lastError = useSession((s) => s.lastError);

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
    sceneRef.current?.setButton(button);
    sceneRef.current?.setToAct(toAct?.seat ?? -1);
    sceneRef.current?.renderSeats(seats);
  }, [seats, yourSeat, button, toAct]);

  useEffect(() => {
    sceneRef.current?.renderHoleCards(holeCards);
  }, [holeCards]);

  useEffect(() => {
    sceneRef.current?.renderCommunity(community);
  }, [community]);

  useEffect(() => {
    sceneRef.current?.renderShowdown(showdown);
  }, [showdown]);

  return (
    <div className="pixi-wrap">
      <canvas ref={canvasRef} style={{ width: '100%', height: '100%', display: 'block' }} />
      <div className="hud">
        <div>
          <span className="label">盲注</span>
          {blinds[0]} / {blinds[1]}
        </div>
        <div>
          <span className="label">底池</span>
          {pot}
        </div>
        <div>
          <span className="label">阶段</span>
          {stage}
        </div>
        <div>
          <span className="label">手牌</span>
          {handId ? handId.slice(0, 8) : '等待开始...'}
        </div>
        {dealerMsg ? <div className="muted" style={{ marginTop: 6 }}>{dealerMsg}</div> : null}
        {lastError ? <div className="error" style={{ marginTop: 6 }}>{lastError}</div> : null}
        {showdown ? (
          <div style={{ marginTop: 8 }}>
            <div className="label">摊牌</div>
            {showdown.winners.map((w, i) => (
              <div key={i}>
                座位 {w.seat} 赢得 {w.amount}
                {w.hand_rank ? ` (${w.hand_rank})` : ''}
              </div>
            ))}
          </div>
        ) : null}
      </div>
      <ActionBar />
    </div>
  );
}
