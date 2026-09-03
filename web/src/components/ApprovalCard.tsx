import { useStore } from '../store';
import { relay } from '../ws';
import { sendKeys } from './TerminalView';
import type { Approval, SessionInfo } from '../protocol/messages';
import { approvalKeys, levelLabel, toolLabel, waited } from '../util';

export function decide(a: Approval, decision: 'allow' | 'deny'): void {
  relay.send({ t: 'approval.decide', device_id: a.device_id, key: a.key, decision });
}

export function modeText(a: Approval): string {
  if (a.level === 'A') return '远程优先：hook 已挂起等你，终端内对话框暂不显示；超时后回落到本机对话框。';
  if (a.level === 'B') return '通知模式：终端里的对话框已弹出。这里发送的是按键，等同于坐在电脑前按键。';
  return '疑似：由 PTY 探测得出，低置信。不会替你按键；先打开终端看一眼。';
}

export function ApprovalButtons({ a, sm, onOpen }: { a: Approval; sm?: boolean; onOpen?: () => void }) {
  const cls = sm ? ' sm' : '';
  const k = approvalKeys(a.agent);
  if (a.level === 'A') {
    return (
      <>
        <button className={`btn ok${cls}`} onClick={() => decide(a, 'allow')}>允许</button>
        <button className={`btn danger${cls}`} onClick={() => decide(a, 'deny')}>拒绝</button>
        {onOpen && <button className={`btn${cls}`} onClick={onOpen}>打开终端</button>}
      </>
    );
  }
  if (a.level === 'B') {
    return (
      <>
        <button className={`btn ok${cls}`} onClick={() => sendKeys(a.sid, k.yes)}>{k.yesLabel}</button>
        <button className={`btn danger${cls}`} onClick={() => sendKeys(a.sid, k.no)}>{k.noLabel}</button>
        {onOpen && <button className={`btn${cls}`} onClick={onOpen}>打开终端</button>}
      </>
    );
  }
  return <button className={`btn${cls}`} onClick={onOpen}>打开终端确认</button>;
}

export default function ApprovalCard({ approval: a, session: s, compact }: { approval: Approval; session?: SessionInfo; compact?: boolean }) {
  const { dispatch } = useStore();
  const lv = levelLabel(a.level);
  const open = () => dispatch({ type: 'openTab', sid: a.sid });
  return (
    <div className={`approval${a.level === 'C' ? ' suspect' : ''}`} role="region" aria-label="待确认">
      <span className="icon" aria-hidden="true">{a.level === 'C' ? '?' : '!'}</span>
      <div style={{ minWidth: 0 }}>
        <div className="what"><span className={`tag ${lv.cls}`}>{lv.label}</span>{toolLabel(a.agent)} · {a.tool}：<code>{a.summary}</code></div>
        <div className="why">{a.cwd ? `${a.cwd} · ` : ''}已等待 {waited(a.created_at)}{s?.name ? ` · ${s.name}` : ''}</div>
        <div className="mode">{modeText(a)}</div>
      </div>
      <div className="actions"><ApprovalButtons a={a} sm={compact} onOpen={compact ? undefined : open} /></div>
    </div>
  );
}
