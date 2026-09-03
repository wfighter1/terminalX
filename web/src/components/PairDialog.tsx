import { useEffect, useState } from 'react';
import { api } from '../api';

export default function PairDialog({ onClose }: { onClose: () => void }) {
  const [code, setCode] = useState('');
  const [exp, setExp] = useState('');
  const [err, setErr] = useState('');
  const [left, setLeft] = useState(0);
  const origin = location.origin;

  async function gen() {
    setErr('');
    try {
      const r = await api.pairNew();
      setCode(r.code);
      setExp(r.expires_at);
    } catch (e) {
      setErr(e instanceof Error ? e.message : '生成失败');
    }
  }
  useEffect(() => {
    gen();
  }, []);
  useEffect(() => {
    if (!exp) return;
    const id = window.setInterval(() => setLeft(Math.max(0, Math.round((Date.parse(exp) - Date.now()) / 1000))), 500);
    return () => window.clearInterval(id);
  }, [exp]);
  const pretty = code ? `${code.slice(0, 4)}-${code.slice(4)}` : '';
  const cmd = `tx-agent pair --relay ${origin} --code ${pretty || 'XXXX-XXXX'}`;

  return (
    <div className="modal-bg" onClick={onClose}>
      <div className="card modal" onClick={(e) => e.stopPropagation()}>
        <h3>添加一台电脑</h3>
        <p style={{ color: 'var(--text-2)' }}>在目标电脑上安装被控端（普通用户即可，不需要管理员），然后输入下面的配对码。被控端只发起出站 443 连接，不用开端口。</p>
        {err && <p className="err-text">{err}</p>}
        <div className="code-big">{pretty || '……'}</div>
        <p style={{ fontSize: 12, color: 'var(--muted)', textAlign: 'center' }}>{left > 0 ? `剩余 ${Math.floor(left / 60)}:${String(left % 60).padStart(2, '0')} · 单次有效 · 输错 5 次锁定 15 分钟` : code ? '已过期，重新生成' : ''}</p>
        <div className="cmd"><span>{cmd}</span><button className="btn sm" onClick={() => navigator.clipboard?.writeText(cmd)}>复制</button></div>
        <ul className="trust">
          <li>配对成功后被控端会打印 8 位指纹短码，设备页会显示同一个短码，请目视核对。</li>
          <li>Windows：随后运行 <code>tx-agent install</code> 注册登录自启（任务计划程序）；电脑睡眠或注销后会离线。</li>
          <li>中转只记元数据（谁、何时、哪台设备、哪个会话、审批结果），不记终端内容。</li>
        </ul>
        <div className="form-actions">
          <button className="btn" onClick={gen}>重新生成</button>
          <button className="btn primary" onClick={onClose}>完成</button>
        </div>
      </div>
    </div>
  );
}
