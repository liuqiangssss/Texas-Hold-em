import { socket } from '../net/ws';
import { useSession } from '../store/session';

const BLINDS: Array<{ label: string; blinds: [number, number] }> = [
  { label: '新手桌 5/10', blinds: [5, 10] },
  { label: '标准桌 25/50', blinds: [25, 50] },
  { label: '高额桌 100/200', blinds: [100, 200] },
];

export function Lobby() {
  const nickname = useSession((s) => s.nickname);

  const onPick = (blinds: [number, number]) => {
    socket.send({ type: 'sit', blinds });
  };

  return (
    <div className="center">
      <div className="card stack" style={{ minWidth: 420 }}>
        <h1>大厅</h1>
        <div className="muted">欢迎，{nickname}。点击任一桌快速入座。</div>
        <div className="row" style={{ flexWrap: 'wrap', gap: 12 }}>
          {BLINDS.map((b) => (
            <button key={b.label} onClick={() => onPick(b.blinds)}>
              {b.label}
            </button>
          ))}
        </div>
        <div className="muted">
          提示：当前骨架只实现"入桌 → 发底牌"。开两个浏览器标签分别登录即可凑齐 2 人看到发牌效果。
        </div>
      </div>
    </div>
  );
}
