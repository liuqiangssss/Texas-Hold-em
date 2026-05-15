import { useEffect, useMemo, useState } from 'react';
import { socket } from '../net/ws';
import { useSession } from '../store/session';
import type { ActionType } from '../proto/messages';

// ActionBar renders Fold / Check or Call / Raise controls when it's the local
// player's turn. Decisions are sent via the WebSocket and the server is the
// single source of truth — we only render local intent.
export function ActionBar() {
  const yourSeat = useSession((s) => s.yourSeat);
  const seats = useSession((s) => s.seats);
  const toAct = useSession((s) => s.toAct);
  const handId = useSession((s) => s.handId);
  const blinds = useSession((s) => s.blinds);

  const isMyTurn = toAct?.seat === yourSeat;
  const me = seats.find((s) => s.seat === yourSeat);

  const minRaise = toAct?.minRaise ?? blinds[1];
  const toCall = toAct?.toCall ?? 0;
  const myBet = me?.bet ?? 0;
  const stack = me?.stack ?? 0;

  const minRaiseTo = (myBet + toCall) + Math.max(minRaise, blinds[1]);
  const maxRaiseTo = myBet + stack;

  const [raiseAmount, setRaiseAmount] = useState<number>(minRaiseTo);

  useEffect(() => {
    if (isMyTurn) {
      setRaiseAmount(Math.min(Math.max(minRaiseTo, 0), maxRaiseTo));
    }
  }, [isMyTurn, minRaiseTo, maxRaiseTo]);

  const send = (action: ActionType, amount?: number) => {
    if (!handId) return;
    socket.send({
      type: 'action',
      hand_id: handId,
      action,
      amount: amount ?? 0,
    });
  };

  const checkOrCallLabel = useMemo(() => {
    if (toCall <= 0) return '过牌';
    if (toCall >= stack) return `跟注 ${stack} (All-in)`;
    return `跟注 ${toCall}`;
  }, [toCall, stack]);

  const canRaise = stack > toCall && maxRaiseTo > myBet + toCall;

  if (!isMyTurn || !me || me.folded) {
    return (
      <div className="action-bar idle">
        <span className="muted">
          {toAct ? `等待座位 ${toAct.seat} 行动…` : '等待开局…'}
        </span>
      </div>
    );
  }

  return (
    <div className="action-bar">
      <button className="btn btn-fold" onClick={() => send('fold')}>
        弃牌
      </button>
      <button
        className="btn btn-call"
        onClick={() => (toCall > 0 ? send('call') : send('check'))}
      >
        {checkOrCallLabel}
      </button>
      {canRaise ? (
        <div className="raise-group">
          <input
            type="range"
            min={minRaiseTo}
            max={maxRaiseTo}
            step={blinds[0]}
            value={raiseAmount}
            onChange={(e) => setRaiseAmount(Number(e.target.value))}
          />
          <div className="raise-value">加注到 {raiseAmount}</div>
          <div className="quick">
            <button onClick={() => setRaiseAmount(Math.min(maxRaiseTo, minRaiseTo))}>Min</button>
            <button
              onClick={() =>
                setRaiseAmount(
                  Math.min(maxRaiseTo, Math.max(minRaiseTo, Math.floor(potOnTable() * 0.5) + myBet + toCall)),
                )
              }
            >
              ½ Pot
            </button>
            <button
              onClick={() =>
                setRaiseAmount(
                  Math.min(maxRaiseTo, Math.max(minRaiseTo, potOnTable() + myBet + toCall)),
                )
              }
            >
              Pot
            </button>
            <button onClick={() => setRaiseAmount(maxRaiseTo)}>All-in</button>
          </div>
          <button
            className="btn btn-raise"
            onClick={() =>
              raiseAmount >= maxRaiseTo
                ? send('all_in')
                : send('raise', raiseAmount)
            }
          >
            确定加注
          </button>
        </div>
      ) : stack > 0 ? (
        <button className="btn btn-raise" onClick={() => send('all_in')}>
          All-in {stack}
        </button>
      ) : null}
    </div>
  );
}

function potOnTable(): number {
  return useSession.getState().pot;
}
