import type { SessionInfo, SessionState, Approval } from './protocol/messages';

export const TOOL_LABEL: Record<string, string> = { claude: 'Claude Code', codex: 'Codex CLI', grok: 'Grok Build', shell: 'Shell' };
export const TOOL_SHORT: Record<string, string> = { claude: 'CC', codex: 'CX', grok: 'GK', shell: 'SH' };

export function toolLabel(t: string): string {
  return TOOL_LABEL[t] ?? t;
}
export function toolShort(t: string): string {
  return TOOL_SHORT[t] ?? t.slice(0, 2).toUpperCase();
}

export const STATE_LABEL: Record<SessionState, string> = {
  running: '运行中',
  needs_input: '等待确认',
  idle: '空闲',
  failed: '出错',
  exited: '已退出',
  quota_wait: '额度等待',
  unknown: '未知',
};

/** CSS class for a session's state pill: low-confidence needs_input becomes "suspect". */
export function stateClass(s: SessionInfo): string {
  if (s.state === 'needs_input' && s.confidence === 'low') return 'suspect';
  return s.state;
}
export function stateLabel(s: SessionInfo): string {
  if (s.state === 'needs_input' && s.confidence === 'low') return '疑似等待';
  return STATE_LABEL[s.state] ?? s.state;
}

export function levelLabel(level: Approval['level']): { cls: string; label: string } {
  switch (level) {
    case 'A':
      return { cls: 'a', label: 'hook 决定' };
    case 'B':
      return { cls: 'b', label: '按键' };
    default:
      return { cls: 'c', label: '疑似' };
  }
}

export function ago(iso: string | undefined, now = Date.now()): string {
  if (!iso) return '—';
  const t = Date.parse(iso);
  if (Number.isNaN(t) || t <= 0) return '—';
  const s = Math.max(0, Math.round((now - t) / 1000));
  if (s < 5) return '刚刚';
  if (s < 60) return `${s} 秒前`;
  const m = Math.round(s / 60);
  if (m < 60) return `${m} 分钟前`;
  const h = Math.round(m / 60);
  if (h < 48) return `${h} 小时前`;
  return `${Math.round(h / 24)} 天前`;
}

export function waited(iso: string, now = Date.now()): string {
  const t = Date.parse(iso);
  const s = Math.max(0, Math.round((now - t) / 1000));
  if (s < 60) return `${s} 秒`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m} 分钟`;
  return `${Math.floor(m / 60)} 小时 ${m % 60} 分`;
}

export function hhmm(d = new Date()): string {
  return `${String(d.getHours()).padStart(2, '0')}:${String(d.getMinutes()).padStart(2, '0')}`;
}

export function shortName(name: string): string {
  return name.split(' · ')[0] || name;
}

export function isZeroTime(iso: string | undefined): boolean {
  return !iso || iso.startsWith('0001-01-01');
}

/** Keys the "按键" buttons send for each tool's native approval prompt. */
export function approvalKeys(agent: string): { yes: string; no: string; yesLabel: string; noLabel: string } {
  if (agent === 'claude') return { yes: '1', no: '3', yesLabel: '发送 1', noLabel: '发送 3' };
  return { yes: 'y', no: 'n', yesLabel: '发送 y', noLabel: '发送 n' };
}
