import { decodeFrame, type Frame } from './protocol/frame';
import type { Msg } from './protocol/messages';

export type ConnState = 'connecting' | 'open' | 'closed';

type FrameHandler = (f: Frame) => void;

/**
 * RelayClient owns the single WebSocket to the relay. Control messages are
 * dispatched to `onMsg`; binary frames are routed to the handler registered
 * for their sid. It reconnects with exponential backoff and replays attaches
 * for every registered terminal (with the last seq the terminal reported).
 */
export class RelayClient {
  private ws: WebSocket | null = null;
  private handlers = new Map<number, FrameHandler>();
  private lastSeq = new Map<number, bigint>();
  private attachInfo = new Map<number, string>(); // sid -> device_id
  private deviceOnline = new Map<string, boolean>();
  private backoff = 2000;
  private closedByUser = false;
  private reconnectTimer: number | null = null;
  state: ConnState = 'closed';

  onMsg: (m: Msg) => void = () => {};
  onState: (s: ConnState) => void = () => {};

  connect(): void {
    this.closedByUser = false;
    if (this.ws && (this.ws.readyState === WebSocket.OPEN || this.ws.readyState === WebSocket.CONNECTING)) return;
    const url = `${location.protocol === 'https:' ? 'wss' : 'ws'}://${location.host}/ws/client`;
    const ws = new WebSocket(url);
    ws.binaryType = 'arraybuffer';
    this.ws = ws;
    this.setState('connecting');
    ws.onopen = () => {
      this.backoff = 2000;
      this.setState('open');
      this.send({ t: 'client.hello' });
      // re-attach everything we were watching
      for (const [sid, deviceId] of this.attachInfo) {
        this.send({ t: 'session.attach', device_id: deviceId, sid, last_seq: Number(this.lastSeq.get(sid) ?? 0n) });
      }
    };
    ws.onmessage = (ev) => {
      if (typeof ev.data === 'string') {
        let m: Msg;
        try {
          m = JSON.parse(ev.data) as Msg;
        } catch (e) {
          console.error('bad control message', e);
          return;
        }
        this.trackDevices(m);
        this.onMsg(m);
        return;
      }
      let f: Frame;
      try {
        f = decodeFrame(ev.data as ArrayBuffer);
      } catch (e) {
        console.error('bad frame, closing (fail-closed)', e);
        ws.close(1002, 'bad frame');
        return;
      }
      this.handlers.get(f.sid)?.(f);
    };
    ws.onclose = () => {
      if (this.ws !== ws) return;
      this.ws = null;
      this.setState('closed');
      if (!this.closedByUser) this.scheduleReconnect();
    };
    ws.onerror = () => {
      /* onclose follows */
    };
  }

  close(): void {
    this.closedByUser = true;
    if (this.reconnectTimer) window.clearTimeout(this.reconnectTimer);
    this.ws?.close();
    this.ws = null;
    this.setState('closed');
  }

  /**
   * When an agent reconnects to the relay, the relay flags our attachments
   * as "pending snapshot" but nobody re-sends session.attach to the new
   * agent connection; do it here on the offline → online transition so the
   * terminal receives its delta / snapshot again.
   */
  private trackDevices(m: Msg): void {
    const seen = (id: string, online: boolean) => {
      const was = this.deviceOnline.get(id);
      this.deviceOnline.set(id, online);
      if (online && was === false) this.reattachDevice(id);
    };
    if (m.t === 'device.state' && m.device) seen(m.device.id, m.device.online);
    if (m.t === 'device.list') for (const d of m.devices ?? []) seen(d.id, d.online);
  }

  private reattachDevice(deviceId: string): void {
    for (const [sid, dev] of this.attachInfo) {
      if (dev === deviceId) this.send({ t: 'session.attach', device_id: deviceId, sid, last_seq: Number(this.lastSeq.get(sid) ?? 0n) });
    }
  }

  private scheduleReconnect(): void {
    if (this.reconnectTimer) window.clearTimeout(this.reconnectTimer);
    const jitter = Math.random() * 500;
    this.reconnectTimer = window.setTimeout(() => {
      this.reconnectTimer = null;
      this.connect();
    }, this.backoff + jitter);
    this.backoff = Math.min(this.backoff * 2, 16000);
  }

  private setState(s: ConnState): void {
    this.state = s;
    this.onState(s);
  }

  send(m: Msg): boolean {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) return false;
    this.ws.send(JSON.stringify(m));
    return true;
  }

  sendFrame(bytes: Uint8Array): boolean {
    if (!this.ws || this.ws.readyState !== WebSocket.OPEN) return false;
    this.ws.send(bytes);
    return true;
  }

  /** Register a terminal for sid and ask the agent to attach (delta or snapshot). */
  attach(deviceId: string, sid: number, handler: FrameHandler): void {
    this.handlers.set(sid, handler);
    this.attachInfo.set(sid, deviceId);
    this.send({ t: 'session.attach', device_id: deviceId, sid, last_seq: Number(this.lastSeq.get(sid) ?? 0n) });
  }

  detach(sid: number): void {
    const deviceId = this.attachInfo.get(sid);
    this.handlers.delete(sid);
    this.attachInfo.delete(sid);
    if (deviceId) this.send({ t: 'session.detach', device_id: deviceId, sid });
  }

  /** Forget a session entirely (closed on the agent). */
  forget(sid: number): void {
    this.handlers.delete(sid);
    this.attachInfo.delete(sid);
    this.lastSeq.delete(sid);
  }

  setLastSeq(sid: number, seq: bigint): void {
    this.lastSeq.set(sid, seq);
  }
  getLastSeq(sid: number): bigint {
    return this.lastSeq.get(sid) ?? 0n;
  }
}

export const relay = new RelayClient();
