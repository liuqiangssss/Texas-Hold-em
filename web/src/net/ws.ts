import type { ClientMsg, ServerMsg } from '../proto/messages';

export type ServerHandler = (msg: ServerMsg) => void;

export class GameSocket {
  private ws: WebSocket | null = null;
  private handlers = new Set<ServerHandler>();
  private queue: ClientMsg[] = [];

  connect(url: string): Promise<void> {
    return new Promise((resolve, reject) => {
      const ws = new WebSocket(url);
      this.ws = ws;
      ws.onopen = () => {
        for (const m of this.queue) ws.send(JSON.stringify(m));
        this.queue = [];
        resolve();
      };
      ws.onerror = (e) => reject(e);
      ws.onmessage = (ev) => {
        try {
          const msg = JSON.parse(ev.data) as ServerMsg;
          for (const h of this.handlers) h(msg);
        } catch (err) {
          console.error('bad server frame', err, ev.data);
        }
      };
      ws.onclose = () => {
        console.warn('[ws] closed');
        this.ws = null;
      };
    });
  }

  on(handler: ServerHandler): () => void {
    this.handlers.add(handler);
    return () => this.handlers.delete(handler);
  }

  send(msg: ClientMsg): void {
    if (this.ws && this.ws.readyState === WebSocket.OPEN) {
      this.ws.send(JSON.stringify(msg));
    } else {
      this.queue.push(msg);
    }
  }
}

export const socket = new GameSocket();
