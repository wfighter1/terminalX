import { pendingApprovals, useStore } from '../store';
import { relay } from '../ws';
import { ago, isZeroTime, levelLabel, stateClass, stateLabel, toolLabel, toolShort, waited } from '../util';

export default function SidePanel({ onOpenInbox }: { onOpenInbox: () => void }) {
  const { st, dispatch } = useStore();
  const cur = st.currentSid !== null ? st.sessions[st.currentSid] : undefined;
  const dev = cur?.device_id ? st.devices[cur.device_id] : undefined;
  const pending = pendingApprovals(st);
  const events = cur ? st.events[cur.sid] ?? [] : [];

  function toggleMode() {
    if (!cur?.device_id) return;
    const mode = cur.approval_mode === 'remote_first' ? 'notify' : 'remote_first';
    relay.send({ t: 'session.set_mode', device_id: cur.device_id, sid: cur.sid, mode });
    dispatch({ type: 'toast', text: mode === 'remote_first' ? '已开启远程优先：下一次审批会挂起等你，终端内对话框暂不显示' : '已切回通知模式：终端内对话框照常，这里只发按键' });
  }
  function closeSession() {
    if (!cur?.device_id) return;
    if (!confirm('结束会话并从列表移除？滚动缓冲将丢失。')) return;
    relay.send({ t: 'session.close', device_id: cur.device_id, sid: cur.sid });
  }
  function resume() {
    if (!cur?.device_id) return;
    relay.send({ t: 'session.signal', device_id: cur.device_id, sid: cur.sid, sig: 'kill_resume' });
  }

  return (
    <aside className="side">
      <div className="card">
        <div className="card-head">会话信息</div>
        {cur ? (
          <>
            <dl className="kv">
              <dt>设备</dt><dd>{dev?.name ?? cur.device_id}</dd>
              <dt>工具</dt><dd><span className={`tool ${cur.tool}`} style={{ width: 18, height: 18, fontSize: 10, verticalAlign: -4, marginRight: 6 }}>{toolShort(cur.tool)}</span>{toolLabel(cur.tool)}</dd>
              <dt>Shell</dt><dd><code>{cur.shell}</code></dd>
              <dt>供应商</dt><dd>{cur.preset || '默认（不注入）'}</dd>
              <dt>目录</dt><dd><code title={cur.cwd}>{cur.cwd || '—'}</code></dd>
              <dt>状态</dt><dd><span className={`pill ${stateClass(cur)}`}>{stateLabel(cur)}</span></dd>
              <dt>权限模式</dt><dd>{cur.permission_mode || '—'}</dd>
              <dt>审批模式</dt>
              <dd>
                {cur.tool === 'claude' ? (
                  <label className={`switch ${cur.approval_mode === 'remote_first' ? 'on' : ''}`} onClick={toggleMode}><i></i>{cur.approval_mode === 'remote_first' ? '远程优先' : '通知模式'}</label>
                ) : cur.tool === 'codex' ? '通知模式（第一阶段）' : '—'}
              </dd>
              <dt>感知来源</dt><dd>{cur.source}{cur.confidence === 'low' ? '（低置信）' : ''}</dd>
              <dt>已运行</dt><dd className="num mono">{isZeroTime(cur.started_at) ? '—' : ago(cur.started_at).replace('前', '')}</dd>
              <dt>用量</dt><dd className="num mono">{cur.cost_usd !== undefined ? `$${cur.cost_usd.toFixed(2)}` : '—'}<span style={{ color: 'var(--muted)' }}> · 上下文 {cur.context_pct !== undefined ? `${Math.round(cur.context_pct)}%` : '—'}</span></dd>
              {cur.exit_code !== undefined && <><dt>退出码</dt><dd className="num mono">{cur.exit_code}</dd></>}
            </dl>
            <div className="side-actions">
              {cur.state === 'exited' && cur.resumable && <button className="btn sm primary" onClick={resume}>拉回（{cur.resumable.tool}）</button>}
              <button className="btn sm" onClick={resume}>kill &amp; resume</button>
              <button className="btn sm danger" onClick={closeSession}>结束会话</button>
            </div>
          </>
        ) : (
          <div className="empty">选择一个会话查看详情。</div>
        )}
      </div>
      <div className="card">
        <div className="card-head">待确认收件箱 <span className={`count${pending.length ? ' hot' : ''}`}>{pending.length}</span></div>
        <div className="inbox-list">
          {pending.length === 0 && <div className="empty">没有待处理的请求。</div>}
          {pending.slice(0, 6).map((a) => {
            const lv = levelLabel(a.level);
            const s = st.sessions[a.sid];
            const d = a.device_id ? st.devices[a.device_id] : undefined;
            return (
              <button className="inbox-item" key={a.key} onClick={() => dispatch({ type: 'openTab', sid: a.sid })}>
                <span className={`tool ${a.agent}`}>{toolShort(a.agent)}</span>
                <span style={{ minWidth: 0 }}>
                  <div className="where">{d?.name ?? a.device_id} · {s?.name ?? `#${a.sid}`}</div>
                  <div className="ask">{a.tool}：<code>{a.summary}</code></div>
                  <div className="wait"><span className={`tag ${lv.cls}`}>{lv.label}</span>已等待 {waited(a.created_at)}</div>
                </span>
              </button>
            );
          })}
        </div>
        {pending.length > 0 && <div className="note"><button className="btn sm ghost" onClick={onOpenInbox}>打开完整收件箱 →</button></div>}
      </div>
      <div className="card">
        <div className="card-head">事件</div>
        <div className="events">
          {events.length === 0 && <div className="empty" style={{ padding: 6 }}>暂无事件。</div>}
          {events.slice().reverse().map((e, i) => (
            <div className={`ev${e.hot ? ' hot' : ''}${e.ok ? ' ok' : ''}${e.err ? ' err' : ''}`} key={i}><time>{e.t}</time><span>{e.s}</span></div>
          ))}
        </div>
      </div>
    </aside>
  );
}
