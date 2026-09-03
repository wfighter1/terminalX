import { pendingApprovals, sessionsOf, useStore } from '../store';
import { ago, isZeroTime, stateClass, stateLabel, toolLabel, toolShort } from '../util';

export default function DeviceRail({ onNewSession, onPair }: { onNewSession: (deviceId: string) => void; onPair: () => void }) {
  const { st, dispatch } = useStore();
  const devices = Object.values(st.devices).sort((a, b) => Number(b.online) - Number(a.online) || a.name.localeCompare(b.name));
  const sessions = Object.values(st.sessions);
  const running = sessions.filter((s) => s.state === 'running').length;
  const pending = pendingApprovals(st);
  const suspect = pending.filter((a) => a.level === 'C').length;
  const online = devices.filter((d) => d.online).length;
  return (
    <aside className="rail">
      <div className="card">
        <div className="rail-summary">
          <div className="stat ok"><b className="num">{running}</b><span>运行中</span></div>
          <div className="stat warn"><b className="num">{pending.length}</b><span>等你（含 {suspect} 疑似）</span></div>
          <div className="stat"><b className="num">{online}<span style={{ fontSize: 13, color: 'var(--muted)' }}>/{devices.length}</span></b><span>设备在线</span></div>
        </div>
      </div>
      <div className="card">
        <div className="card-head">设备与会话 <span className="count">{sessions.length}</span></div>
        {devices.length === 0 && <div className="empty">还没有配对的设备。点「添加设备」生成配对码。</div>}
        {devices.map((d) => {
          const ss = sessionsOf(st, d.id);
          return (
            <div className="device" key={d.id}>
              <div className="device-head">
                <span className="os" aria-hidden="true">{d.os.toLowerCase().includes('win') ? 'Win' : d.os.toLowerCase().includes('darwin') || d.os.toLowerCase().includes('mac') ? 'mac' : 'nix'}</span>
                <div style={{ minWidth: 0 }}>
                  <div className="device-name">{d.name}</div>
                  <div className="device-meta">
                    {d.os}{d.online ? ` · 心跳 ${isZeroTime(d.last_seen) ? '—' : ago(d.last_seen)}${d.rtt_ms ? ` · ${d.rtt_ms} ms` : ''}` : ` · 离线${isZeroTime(d.last_seen) ? '' : ` · 最后 ${ago(d.last_seen)}`}`}
                  </div>
                </div>
                <span className={`device-status ${d.online ? '' : 'off'}`} title={d.online ? '在线' : '离线'}></span>
              </div>
              <div className="sessions">
                {ss.length === 0 && <div className="empty" style={{ padding: '4px 8px 8px', textAlign: 'left' }}>{d.online ? '没有会话。' : '被控端离线，会话列表在上线后恢复。'}</div>}
                {ss.map((s) => (
                  <button className="sess" key={s.sid} aria-current={st.currentSid === s.sid} onClick={() => dispatch({ type: 'openTab', sid: s.sid })}>
                    <span className={`tool ${s.tool}`}>{toolShort(s.tool)}</span>
                    <span><span className="name">{s.name || `${toolLabel(s.tool)} #${s.sid}`}</span><span className="sub">{toolLabel(s.tool)} · {s.shell}{s.preset ? ` · ${s.preset}` : ''}</span></span>
                    <span className={`pill ${stateClass(s)}`}>{stateLabel(s)}</span>
                  </button>
                ))}
                {d.online && <button className="btn sm ghost" style={{ justifySelf: 'start' }} onClick={() => onNewSession(d.id)}>＋ 在这台机器上新建会话</button>}
              </div>
            </div>
          );
        })}
        <div className="note">第一阶段：多台设备只做列表与跨机会话；聚合告警与用量在第二阶段。</div>
        <div className="rail-foot">
          <button className="btn sm" onClick={onPair}>＋ 添加设备</button>
          <button className="btn sm" disabled={online === 0} onClick={() => onNewSession(devices.find((d) => d.online)?.id ?? '')}>＋ 新会话</button>
        </div>
      </div>
    </aside>
  );
}
