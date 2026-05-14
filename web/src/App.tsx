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
      switch (msg.type) {
        case 'login_ok':
          useSession.getState().setLogin(msg.user_id, msg.nickname);
          break;
        case 'table_state':
          useSession
            .getState()
            .applyTableState(msg.table_id, msg.blinds, msg.seats, msg.your_seat);
          break;
        case 'hand_start':
          useSession.getState().applyHandStart(msg.hand_id, msg.dealer_msg);
          break;
        case 'deal_hole':
          useSession.getState().applyHole(msg.cards);
          break;
        case 'error':
          useSession.getState().setError(`${msg.code}: ${msg.message}`);
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
