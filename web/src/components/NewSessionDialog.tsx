import { useState, type FormEvent } from 'react';
import { useStore } from '../store';
import { relay } from '../ws';
import type { ApprovalMode } from '../protocol/messages';

export default function NewSessionDialog({ deviceId, onClose }: { deviceId: string; onClose: () => void }) {
  const { st, dispatch } = useStore();
  const online = Object.values(st.devices).filter((d) => d.online);
  const [dev, setDev] = useState(deviceId || online[0]?.id || '');
  const [tool, setTool] = useState('claude');
  const [shell, setShell] = useState('');
  const [cwd, setCwd] = useState('');
  const [name, setName] = useState('');
  const [preset, setPreset] = useState('');
  const [perm, setPerm] = useState('default');
  const [mode, setMode] = useState<ApprovalMode>('notify');
  const [resume, setResume] = useState('');

  function submit(e: FormEvent) {
    e.preventDefault();
    if (!dev) return;
    const reqId = `o${Date.now().toString(36)}${Math.random().toString(36).slice(2, 6)}`;
    dispatch({ type: 'pendingOpen', reqId });
    const ok = relay.send({
      t: 'session.open',
      req_id: reqId,
      device_id: dev,
      open: {
        tool,
        shell: shell || undefined!,
        cwd: cwd || undefined,
        name: name || undefined,
        preset: preset || undefined,
        permission_mode: tool === 'claude' && perm !== 'default' ? perm : undefined,
        approval_mode: tool === 'claude' ? mode : undefined,
        resume: resume || undefined,
        cols: 120,
        rows: 36,
      },
    });
    if (!ok) dispatch({ type: 'toast', text: '中转未连接', err: true });
    onClose();
  }

  return (
    <div className="modal-bg" onClick={onClose}>
      <form className="card modal" onClick={(e) => e.stopPropagation()} onSubmit={submit}>
        <h3>新建会话</h3>
        <div className="form">
          <label>设备
            <select value={dev} onChange={(e) => setDev(e.target.value)}>
              {online.map((d) => <option key={d.id} value={d.id}>{d.name} · {d.os}</option>)}
            </select>
          </label>
          <div className="row">
            <label>工具
              <select value={tool} onChange={(e) => setTool(e.target.value)}>
                <option value="claude">Claude Code</option>
                <option value="codex">Codex CLI</option>
                <option value="grok">Grok Build</option>
                <option value="shell">仅 Shell</option>
              </select>
            </label>
            <label>Shell（留空用被控端默认）
              <input value={shell} onChange={(e) => setShell(e.target.value)} placeholder="pwsh / powershell / cmd / bash" />
            </label>
          </div>
          <label>工作目录
            <input value={cwd} onChange={(e) => setCwd(e.target.value)} placeholder="D:\work\project（留空用用户目录）" />
          </label>
          <div className="row">
            <label>会话名（Claude 映射为 --name）
              <input value={name} onChange={(e) => setName(e.target.value)} placeholder="terminalx-relay" />
            </label>
            <label>供应商预设
              <input value={preset} onChange={(e) => setPreset(e.target.value)} placeholder="留空 / minimax / relay-station / 自定义" list="presets" />
              <datalist id="presets"><option value="minimax" /><option value="relay-station" /><option value="anthropic" /></datalist>
            </label>
          </div>
          {tool === 'claude' && (
            <div className="row">
              <label>权限模式
                <select value={perm} onChange={(e) => setPerm(e.target.value)}>
                  <option value="default">default（逐条确认）</option>
                  <option value="acceptEdits">acceptEdits</option>
                  <option value="plan">plan</option>
                  <option value="bypassPermissions">bypassPermissions（远程零弹窗）</option>
                </select>
              </label>
              <label>审批模式
                <select value={mode} onChange={(e) => setMode(e.target.value as ApprovalMode)}>
                  <option value="notify">通知模式（默认）</option>
                  <option value="remote_first">远程优先（hook 挂起等你）</option>
                </select>
              </label>
            </div>
          )}
          {(tool === 'claude' || tool === 'codex') && (
            <label>续跑（Claude：会话名或 id，填 continue 表示 --continue；Codex：last）
              <input value={resume} onChange={(e) => setResume(e.target.value)} placeholder="留空表示新会话" />
            </label>
          )}
        </div>
        <p style={{ fontSize: 12, color: 'var(--muted)' }}>被控端会先起 shell，再把工具命令行作为第一行输入写进去；工具退出后你仍在 shell 提示符。Claude 会话通过 <code>--settings</code> 注入 hooks，不改你的 settings.json。</p>
        <div className="form-actions">
          <button type="button" className="btn" onClick={onClose}>取消</button>
          <button className="btn primary" disabled={!dev}>启动</button>
        </div>
      </form>
    </div>
  );
}
