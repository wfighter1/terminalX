import { api } from '../api';
import { sessionsOf, useStore } from '../store';
import { ago, isZeroTime } from '../util';

export default function DevicesPage({ onPair, onNewSession }: { onPair: () => void; onNewSession: (deviceId: string) => void }) {
  const { st, dispatch } = useStore();
  const devices = Object.values(st.devices).sort((a, b) => a.name.localeCompare(b.name));
  async function rename(id: string, cur: string) {
    const name = prompt('设备名称', cur);
    if (!name || name === cur) return;
    try {
      await api.renameDevice(id, name);
    } catch (e) {
      dispatch({ type: 'toast', text: e instanceof Error ? e.message : '重命名失败', err: true });
    }
  }
  async function revoke(id: string, name: string) {
    if (!confirm(`吊销「${name}」？被控端会在 15 秒内断开，需要重新配对。`)) return;
    try {
      await api.revokeDevice(id);
      dispatch({ type: 'toast', text: '已吊销' });
    } catch (e) {
      dispatch({ type: 'toast', text: e instanceof Error ? e.message : '吊销失败', err: true });
    }
  }
  return (
    <div className="page">
      <div className="section-title">
        <h2>设备</h2>
        <p>机器是一级实体。第一阶段只做列表；聚合告警与用量在第二阶段。</p>
        <button className="btn primary sm" style={{ marginLeft: 'auto' }} onClick={onPair}>＋ 添加设备</button>
      </div>
      <div className="card">
        {devices.length === 0 && <div className="empty">还没有设备。</div>}
        {devices.map((d) => {
          const ss = sessionsOf(st, d.id);
          return (
            <div className="dev-row" key={d.id}>
              <span className={`device-status ${d.online ? '' : 'off'}`}></span>
              <div style={{ minWidth: 0 }}>
                <div className="device-name">{d.name} <span style={{ color: 'var(--muted)', fontWeight: 400, fontSize: 12 }}>{d.os}{d.agent_version ? ` · Agent ${d.agent_version}` : ''}</span></div>
                <div className="meta">{d.online ? `在线 · 心跳 ${isZeroTime(d.last_seen) ? '—' : ago(d.last_seen)}${d.rtt_ms ? ` · ${d.rtt_ms} ms` : ''}` : `离线${isZeroTime(d.last_seen) ? '' : ` · 最后在线 ${ago(d.last_seen)}`}`} · {ss.length} 个会话 · 指纹 <code>{d.fingerprint || '—'}</code></div>
              </div>
              <button className="btn sm" disabled={!d.online} onClick={() => onNewSession(d.id)}>新建会话</button>
              <span style={{ display: 'flex', gap: 6 }}>
                <button className="btn sm ghost" onClick={() => rename(d.id, d.name)}>重命名</button>
                <button className="btn sm danger" onClick={() => revoke(d.id, d.name)}>吊销</button>
              </span>
            </div>
          );
        })}
      </div>
    </div>
  );
}
