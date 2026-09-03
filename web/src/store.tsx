import { createContext, useContext, useEffect, useMemo, useReducer, type ReactNode, type Dispatch } from 'react';
import type { Approval, DeviceInfo, Msg, SessionInfo } from './protocol/messages';
import { relay, type ConnState } from './ws';
import { hhmm, toolLabel } from './util';

export interface Ev {
  t: string;
  s: string;
  hot?: boolean;
  ok?: boolean;
  err?: boolean;
}

export interface State {
  conn: ConnState;
  clientId: string;
  devices: Record<string, DeviceInfo>;
  sessions: Record<number, SessionInfo>;
  approvals: Record<string, Approval>;
  currentSid: number | null;
  openTabs: number[];
  events: Record<number, Ev[]>;
  pendingOpens: Record<string, true>; // req_id -> waiting for session.opened
  toast: { text: string; err?: boolean; id: number } | null;
}

export type Action =
  | { type: 'conn'; state: ConnState }
  | { type: 'msg'; msg: Msg }
  | { type: 'select'; sid: number | null }
  | { type: 'openTab'; sid: number }
  | { type: 'closeTab'; sid: number }
  | { type: 'event'; sid: number; ev: Ev }
  | { type: 'pendingOpen'; reqId: string }
  | { type: 'toast'; text: string; err?: boolean }
  | { type: 'clearToast' };

const initial: State = {
  conn: 'closed',
  clientId: '',
  devices: {},
  sessions: {},
  approvals: {},
  currentSid: null,
  openTabs: [],
  events: {},
  pendingOpens: {},
  toast: null,
};

let toastSeq = 0;

function pushEvent(events: Record<number, Ev[]>, sid: number, ev: Ev): Record<number, Ev[]> {
  const list = [...(events[sid] ?? []), ev].slice(-60);
  return { ...events, [sid]: list };
}

function withDevice(s: SessionInfo, deviceId?: string): SessionInfo {
  return deviceId && !s.device_id ? { ...s, device_id: deviceId } : s;
}

export function reducer(st: State, a: Action): State {
  switch (a.type) {
    case 'conn':
      return { ...st, conn: a.state };
    case 'select':
      return { ...st, currentSid: a.sid };
    case 'openTab': {
      const openTabs = st.openTabs.includes(a.sid) ? st.openTabs : [...st.openTabs, a.sid];
      return { ...st, openTabs, currentSid: a.sid };
    }
    case 'closeTab': {
      const openTabs = st.openTabs.filter((x) => x !== a.sid);
      const currentSid = st.currentSid === a.sid ? (openTabs[openTabs.length - 1] ?? null) : st.currentSid;
      return { ...st, openTabs, currentSid };
    }
    case 'event':
      return { ...st, events: pushEvent(st.events, a.sid, a.ev) };
    case 'pendingOpen':
      return { ...st, pendingOpens: { ...st.pendingOpens, [a.reqId]: true } };
    case 'toast':
      return { ...st, toast: { text: a.text, err: a.err, id: ++toastSeq } };
    case 'clearToast':
      return { ...st, toast: null };
    case 'msg':
      return applyMsg(st, a.msg);
    default:
      return st;
  }
}

function applyMsg(st: State, m: Msg): State {
  switch (m.t) {
    case 'device.list': {
      const devices: Record<string, DeviceInfo> = {};
      for (const d of m.devices ?? []) devices[d.id] = d;
      return { ...st, devices };
    }
    case 'device.state': {
      if (!m.device) return st;
      return { ...st, devices: { ...st.devices, [m.device.id]: m.device } };
    }
    case 'session.list': {
      const sessions = { ...st.sessions };
      if (m.device_id) {
        for (const sid of Object.keys(sessions)) {
          if (sessions[Number(sid)].device_id === m.device_id) delete sessions[Number(sid)];
        }
      }
      for (const s of m.sessions ?? []) sessions[s.sid] = withDevice(s, m.device_id);
      return { ...st, sessions };
    }
    case 'session.opened': {
      if (!m.session) return st;
      const s = withDevice(m.session, m.device_id);
      let next: State = { ...st, sessions: { ...st.sessions, [s.sid]: s } };
      next = { ...next, events: pushEvent(next.events, s.sid, { t: hhmm(), s: `会话启动 · ${toolLabel(s.tool)} · ${s.shell}` }) };
      if (m.req_id && st.pendingOpens[m.req_id]) {
        const pendingOpens = { ...st.pendingOpens };
        delete pendingOpens[m.req_id];
        const openTabs = next.openTabs.includes(s.sid) ? next.openTabs : [...next.openTabs, s.sid];
        next = { ...next, pendingOpens, openTabs, currentSid: s.sid };
      }
      return next;
    }
    case 'session.updated': {
      if (!m.session) return st;
      const s = withDevice(m.session, m.device_id);
      return { ...st, sessions: { ...st.sessions, [s.sid]: { ...st.sessions[s.sid], ...s } } };
    }
    case 'session.state': {
      const cur = st.sessions[m.sid ?? -1];
      if (!cur) return st;
      const upd: SessionInfo = {
        ...cur,
        state: m.state ?? cur.state,
        kind: m.kind ?? '',
        source: m.source ?? cur.source,
        confidence: m.confidence ?? cur.confidence,
      };
      const label = upd.state === 'needs_input' ? (upd.confidence === 'low' ? '疑似等待（PTY 探测）' : `等待确认（${upd.kind === 'question' ? '提问' : '权限'}）`) : upd.state;
      return {
        ...st,
        sessions: { ...st.sessions, [upd.sid]: upd },
        events: pushEvent(st.events, upd.sid, { t: hhmm(), s: `状态：${label} · 来源 ${upd.source}`, hot: upd.state === 'needs_input', err: upd.state === 'failed' }),
      };
    }
    case 'session.exited': {
      const cur = st.sessions[m.sid ?? -1];
      if (!cur) return st;
      const upd: SessionInfo = { ...cur, state: 'exited', exit_code: m.code, resumable: m.resumable ?? cur.resumable, pty_alive: false };
      return { ...st, sessions: { ...st.sessions, [upd.sid]: upd }, events: pushEvent(st.events, upd.sid, { t: hhmm(), s: `进程退出，退出码 ${m.code ?? '?'}`, err: (m.code ?? 0) !== 0 }) };
    }
    case 'session.closed': {
      const sid = m.sid ?? -1;
      if (!(sid in st.sessions)) return st;
      const sessions = { ...st.sessions };
      delete sessions[sid];
      const openTabs = st.openTabs.filter((x) => x !== sid);
      const currentSid = st.currentSid === sid ? (openTabs[openTabs.length - 1] ?? null) : st.currentSid;
      relay.forget(sid);
      return { ...st, sessions, openTabs, currentSid };
    }
    case 'approval.list': {
      const approvals: Record<string, Approval> = {};
      for (const a of m.approvals ?? []) approvals[a.key] = a;
      return { ...st, approvals };
    }
    case 'approval.new': {
      if (!m.approval) return st;
      const a = { ...m.approval, device_id: m.approval.device_id ?? m.device_id };
      return { ...st, approvals: { ...st.approvals, [a.key]: a }, events: pushEvent(st.events, a.sid, { t: hhmm(), s: `${a.tool}：${a.summary}`, hot: true }) };
    }
    case 'approval.closed': {
      const key = m.key ?? '';
      const cur = st.approvals[key];
      if (!cur) return st;
      const by = m.by ?? '';
      const status = m.approval?.status ?? (by === 'local' ? 'closed_local' : by === 'timeout' ? 'fallback' : by.startsWith('deny') ? 'denied' : 'allowed');
      const upd: Approval = { ...cur, ...(m.approval ?? {}), status, decided_by: by || cur.decided_by, decided_at: new Date().toISOString() };
      const text = status === 'closed_local' ? '本机已处理 · 卡片自动关闭' : status === 'fallback' ? 'hook 超时，已回落本机对话框' : status === 'denied' ? '已拒绝' : status === 'allowed' ? '已允许' : `已关闭（${by}）`;
      return { ...st, approvals: { ...st.approvals, [key]: upd }, events: pushEvent(st.events, cur.sid, { t: hhmm(), s: `${text}：${cur.summary}`, ok: status === 'allowed' || status === 'closed_local' }) };
    }
    case 'ack':
      return st;
    case 'error':
      return { ...st, toast: { text: m.error ?? '未知错误', err: true, id: ++toastSeq } };
    default:
      return st;
  }
}

const Ctx = createContext<{ st: State; dispatch: Dispatch<Action> } | null>(null);

export function StoreProvider({ children }: { children: ReactNode }) {
  const [st, dispatch] = useReducer(reducer, initial);
  useEffect(() => {
    relay.onMsg = (m) => dispatch({ type: 'msg', msg: m });
    relay.onState = (s) => dispatch({ type: 'conn', state: s });
    relay.connect();
    return () => relay.close();
  }, []);
  useEffect(() => {
    if (!st.toast) return;
    const id = window.setTimeout(() => dispatch({ type: 'clearToast' }), 3000);
    return () => window.clearTimeout(id);
  }, [st.toast]);
  const value = useMemo(() => ({ st, dispatch }), [st]);
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export function useStore() {
  const v = useContext(Ctx);
  if (!v) throw new Error('StoreProvider missing');
  return v;
}

export function pendingApprovals(st: State): Approval[] {
  return Object.values(st.approvals)
    .filter((a) => a.status === 'pending')
    .sort((a, b) => Date.parse(a.created_at) - Date.parse(b.created_at));
}

export function sessionsOf(st: State, deviceId: string): SessionInfo[] {
  return Object.values(st.sessions)
    .filter((s) => s.device_id === deviceId)
    .sort((a, b) => Date.parse(a.started_at) - Date.parse(b.started_at));
}
