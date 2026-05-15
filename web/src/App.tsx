import { useEffect } from 'react';
import { socket } from './net/ws';
import { useSession } from './store/session';
import { Login } from './pages/Login';
import { Lobby } from './pages/Lobby';
import { Table } from './pages/Table';
import type { ServerMsg } from './proto/messages';

function App() {
  const scene = useSession((s) => s.scene);

  useEffect(() => {
    const off = socket.on((msg: ServerMsg) => {
      const store = useSession.getState();
      switch (msg.type) {
        case 'login_ok':
          store.setLogin(msg.user_id, msg.nickname);
          break;
        case 'table_state':
          store.applyTableState(msg);
          break;
        case 'hand_start':
          store.applyHandStart(msg.hand_id, msg.dealer_msg, msg.button);
          break;
        case 'hand_end':
          store.applyHandEnd();
          break;
        case 'deal_hole':
          store.applyHole(msg.cards);
          break;
        case 'deal_community':
          store.applyCommunity(msg.cards);
          break;
        case 'action_applied':
          store.applyAction(msg.seat, msg.action, msg.amount, msg.stack, msg.bet);
          break;
        case 'to_act':
          store.applyToAct({ seat: msg.seat, toCall: msg.to_call, minRaise: msg.min_raise });
          break;
        case 'pot_update':
          store.applyPot(msg.pots, msg.total);
          break;
        case 'showdown':
          store.applyShowdown(msg.winners, msg.reveals ?? {}, msg.community);
          break;
        case 'error':
          store.setError(`${msg.code}: ${msg.message}`);
          break;
      }
    });
    return () => {
      off();
    };
  }, []);

  if (scene === 'login') return <Login />;
  if (scene === 'lobby') return <Lobby />;
  return <Table />;
}

export default App;
