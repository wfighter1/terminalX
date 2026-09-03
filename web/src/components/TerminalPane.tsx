import { useState, type KeyboardEvent } from 'react';
import { pendingApprovals, useStore } from '../store';
import { relay } from '../ws';
import TerminalView, { sendKeys } from './TerminalView';
import ApprovalCard from './ApprovalCard';
import { ago, isZeroTime, shortName, toolLabel, toolShort } from '../util';

const KEYS: Array<{ label: string; bytes: string }> = [
  { label: 'Esc', bytes: '\x1b' },
  { label: 'Tab', bytes: '\t' },
  { label: '↑', bytes: '\x1b[A' },
  { label: '↓', bytes: '\x1b[B' },
  { label: '←', bytes: '\x1b[D' },
  { label: '→', bytes: '\x1b[C' },
  { label: '1', bytes: '1' },
  { label: '2', bytes: '2' },
  { label: '3', bytes: '3' },
  { label: 'y', bytes: 'y' },
  { label: 'n', bytes: 'n' },
  { label: '⏎', bytes: '\r' },
];

export default function TerminalPane({ onNewSession }: { onNewSession: () => void }) {
  const { st, dispatch } = useStore();
  const [line, setLine] = useState('');
  const [ctrl, setCtrl] = useState(false);
  const cur = st.currentSid !== null ? st.sessions[st.currentSid] : undefined;
  const dev = cur?.device_id ? st.devices[cur.device_id] : undefined;
  const tabs = st.openTabs.map((sid) => st.sessions[sid]).filter(Boolean);
  const pending = cur ? pendingApprovals(st).filter((a) => a.sid === cur.sid) : [];

  function signal(sig: string) {
    if (!cur?.device_id) return;
    relay.send({ t: 'session.signal', device_id: cur.device_id, sid: cur.sid, sig });
    dispatch({ type: 'event', sid: cur.sid, ev: { t: new Date().toTimeString().slice(0, 5), s: `远程解卡：${sig}` } });
  }
  function sendLine() {
    if (!cur || !line) return;
    sendKeys(cur.sid, line + '\r');
    setLine('');
  }
  function key(bytes: string) {
    if (!cur) return;
    if (ctrl && bytes.length === 1) {
      const c = bytes.toLowerCase().charCodeAt(0);
      if (c >= 97 && c <= 122) {
        sendKeys(cur.sid, String.fromCharCode(c - 96));
        setCtrl(false);
        return;
      }
    }
    sendKeys(cur.sid, bytes);
  }
  function onInputKey(e: KeyboardEvent<HTMLInputElement>) {
    if (e.key === 'Enter') {
      e.preventDefault();
      sendLine();
    }
  }

  const lastOut = cur && !isZeroTime(cur.last_output_at) ? ago(cur.last_output_at) : '—';
  const stale = cur ? Date.now() - Date.parse(cur.last_output_at) > 5 * 60 * 1000 : false;

  return (
    <section className="card term-pane" aria-label="终端">
      <div className="tabs" role="tablist">
        {tabs.map((s) => (
          <button key={s.sid} className="tab" role="tab" aria-selected={st.currentSid === s.sid} onClick={() => dispatch({ type: 'select', sid: s.sid })}>
            <span className={`tool ${s.tool}`}>{toolShort(s.tool)}</span>
            {shortName(s.name || `${toolLabel(s.tool)} #${s.sid}`)}
            <span className={`st ${s.state}`} aria-label={s.state}></span>
            <span className="x" role="button" aria-label="关闭标签（仅 detach，不结束会话）" onClick={(e) => { e.stopPropagation(); dispatch({ type: 'closeTab', sid: s.sid }); }}>×</span>
          </button>
        ))}
        <button className="tab add" aria-label="新建会话" onClick={onNewSession}>＋</button>
      </div>
      <div className="term-status">
        {cur && dev ? (
          <>
            <span className={`sig ${dev.online ? '' : 'err'}`}>{dev.online ? `心跳 ${isZeroTime(dev.last_seen) ? '—' : ago(dev.last_seen)}` : '被控端离线'}</span>
            <span className={`sig ${cur.pty_alive ? '' : 'off'}`}>{cur.pty_alive ? 'PTY 存活' : 'PTY 已退出'}</span>
            <span className={`sig ${stale ? 'warn' : ''}`}>最近输出 {lastOut}</span>
            <span style={{ color: 'var(--muted)' }}>seq {String(relay.getLastSeq(cur.sid))}</span>
            <span className="unstick" aria-label="远程解卡">
              <button className="btn sm" onClick={() => key('\x1b')}>Esc</button>
              <button className="btn sm" onClick={() => signal('ctrl_c')}>Ctrl-C</button>
              <button className="btn sm" onClick={() => { if (confirm('结束当前进程并按会话续跑（kill & resume）？滚动缓冲保留。')) signal('kill_resume'); }}>kill &amp; resume</button>
            </span>
          </>
        ) : (
          <span style={{ color: 'var(--muted)' }}>从左侧选择一个会话，或新建。</span>
        )}
      </div>
      <div className="term-host">
        {tabs.length === 0 && <div className="term-empty">没有打开的终端。左侧点会话即可附着；关掉标签只是 detach，会话仍在被控端。</div>}
        {tabs.map((s) => (
          <TerminalView key={s.sid} sid={s.sid} deviceId={s.device_id ?? ''} active={st.currentSid === s.sid} />
        ))}
      </div>
      {cur && pending.length > 0 && <ApprovalCard approval={pending[0]} session={cur} compact />}
      <div className="inputbar">
        <div className="input-row">
          <input value={line} onChange={(e) => setLine(e.target.value)} onKeyDown={onInputKey} disabled={!cur} placeholder={cur ? `整行发送到 ${toolLabel(cur.tool)}（Enter 发送，自动附加回车）` : '先选择一个会话'} aria-label="整行输入" autoComplete="off" />
          <button className="btn primary" disabled={!cur || !line} onClick={sendLine}>发送</button>
        </div>
        <div className="keys" aria-label="快捷键栏">
          <button className={`key${ctrl ? ' on' : ''}`} onClick={() => setCtrl((v) => !v)} disabled={!cur}>Ctrl</button>
          <button className="key" onClick={() => key('c')} disabled={!cur}>C</button>
          {KEYS.map((k) => (
            <button key={k.label} className="key" onClick={() => key(k.bytes)} disabled={!cur}>{k.label}</button>
          ))}
          <span className="hint">按键直接进 PTY；整行文字走上面的输入框（手机端不往终端里直接打中文）</span>
        </div>
      </div>
    </section>
  );
}
