import { useEffect, useState } from 'react';
import { api } from '../api';
import { useStore } from '../store';

export default function SettingsPage() {
  const { dispatch } = useStore();
  const [url, setUrl] = useState('');
  const [audit, setAudit] = useState<Array<{ id: number; at: string; actor: string; action: string; detail?: string; device_id?: string }>>([]);
  useEffect(() => {
    api.webhookGet().then((r) => setUrl(r.url ?? '')).catch(() => {});
    api.audit(50).then(setAudit).catch(() => {});
  }, []);
  async function save() {
    try {
      await api.webhookPut(url);
      dispatch({ type: 'toast', text: '已保存' });
    } catch (e) {
      dispatch({ type: 'toast', text: e instanceof Error ? e.message : '保存失败', err: true });
    }
  }
  return (
    <div className="page">
      <div className="section-title"><h2>设置</h2><p>通知通道与审计元数据。E2E、Passkey、只读分享在 1.1。</p></div>
      <div className="two-col">
        <div className="card">
          <div className="card-head">通知 webhook</div>
          <div className="form" style={{ padding: 14 }}>
            <label>URL（每条「等你确认」向此地址 POST JSON：title / body / device / session / key / url；可填 ntfy、Bark、飞书 / 企微机器人）
              <input value={url} onChange={(e) => setUrl(e.target.value)} placeholder="https://ntfy.sh/your-topic" />
            </label>
            <div className="form-actions"><button className="btn primary" onClick={save}>保存</button></div>
          </div>
        </div>
        <div className="card">
          <div className="card-head">审计（最近 50 条，仅元数据）</div>
          <div className="events" style={{ maxHeight: 420 }}>
            {audit.length === 0 && <div className="empty">暂无记录。</div>}
            {audit.map((a) => (
              <div className="ev" key={a.id}><time>{new Date(a.at).toLocaleTimeString()}</time><span>{a.actor} · {a.action}{a.device_id ? ` · ${a.device_id.slice(0, 8)}` : ''}{a.detail ? ` · ${a.detail}` : ''}</span></div>
            ))}
          </div>
        </div>
      </div>
    </div>
  );
}
