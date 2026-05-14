import { useState } from 'react';
import { socket } from '../net/ws';
import { useSession } from '../store/session';

const WS_URL = `${location.protocol === 'https:' ? 'wss' : 'ws'}://${location.host}/ws`;

export function Login() {
  const [nickname, setNickname] = useState('Guest-' + Math.floor(Math.random() * 9000 + 1000));
  const [connecting, setConnecting] = useState(false);
  const lastError = useSession((s) => s.lastError);

  const onSubmit = async () => {
    if (connecting) return;
    setConnecting(true);
    useSession.getState().setError(null);
    try {
      await socket.connect(WS_URL);
      socket.send({ type: 'login', nickname });
    } catch (e) {
      useSession.getState().setError('无法连接到服务器');
      setConnecting(false);
    }
  };

  return (
    <div className="center">
      <div className="card stack">
        <h1>Texas Hold'em</h1>
        <div className="muted">游客登录即可开始（MVP 骨架版）</div>
        <input
          placeholder="昵称"
          value={nickname}
          onChange={(e) => setNickname(e.target.value)}
        />
        <button onClick={onSubmit} disabled={connecting || !nickname.trim()}>
          {connecting ? '连接中...' : '游客登录'}
        </button>
        {lastError ? <div style={{ color: '#ff6b6b', fontSize: 13 }}>{lastError}</div> : null}
      </div>
    </div>
  );
}
