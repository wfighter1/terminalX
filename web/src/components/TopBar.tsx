import { api } from '../api';
import { pendingApprovals, useStore } from '../store';
import type { Page } from '../App';

export default function TopBar({ page, setPage, onLogout }: { page: Page; setPage: (p: Page) => void; onLogout: () => void }) {
  const { st } = useStore();
  const sessions = Object.values(st.sessions);
  const running = sessions.filter((s) => s.state === 'running').length;
  const needs = sessions.filter((s) => s.state === 'needs_input').length;
  const failed = sessions.filter((s) => s.state === 'failed').length;
  const unknown = sessions.filter((s) => s.state === 'unknown').length;
  const pending = pendingApprovals(st).length;
  const connCls = st.conn === 'open' ? '' : st.conn === 'connecting' ? 'conn' : 'off';
  const connText = st.conn === 'open' ? '中转已连接' : st.conn === 'connecting' ? '正在重连…' : '中转断开';
  const nav: Array<[Page, string]> = [['console', '控制台'], ['inbox', '待确认'], ['devices', '设备'], ['settings', '设置']];
  return (
    <header className="topbar">
      <div className="brand"><span className="brand-mark">tX</span>terminalX <small>v0.1</small></div>
      <div className="gstatus" aria-label="全局状态">
        <span className="ok"><b className="num">{running}</b> 运行中</span><span className="sep">·</span>
        <span className="warn"><b className="num">{needs}</b> 等你</span><span className="sep">·</span>
        <span className="err"><b className="num">{failed}</b> 出错</span><span className="sep">·</span>
        <span style={{ color: 'var(--muted)' }}><b className="num">{unknown}</b> 未知</span>
      </div>
      <nav className="nav" aria-label="页面">
        {nav.map(([p, label]) => (
          <button key={p} aria-current={page === p} onClick={() => setPage(p)} className={p === 'inbox' ? 'badge-btn' : ''}>
            {label}
            {p === 'inbox' && pending > 0 && <span className="badge">{pending}</span>}
          </button>
        ))}
      </nav>
      <div className="relay" title="被控端只出站到你的中转；第一阶段 TLS，内容不落盘">
        <span className={`dot ${connCls}`}></span><span>{connText}</span>
      </div>
      <div className="topbar-right">
        <button className="btn ghost sm" onClick={() => api.logout().finally(onLogout)}>退出</button>
      </div>
    </header>
  );
}
