import { useStore } from '../store';
import { ApprovalButtons } from './ApprovalCard';
import { levelLabel, toolLabel, toolShort, waited } from '../util';

export default function InboxPage({ onOpenSession }: { onOpenSession: (sid: number) => void }) {
  const { st } = useStore();
  const all = Object.values(st.approvals).sort((a, b) => Date.parse(a.created_at) - Date.parse(b.created_at));
  const pending = all.filter((a) => a.status === 'pending');
  const done = all.filter((a) => a.status !== 'pending').slice(-20).reverse();
  const statusText: Record<string, string> = { allowed: '已允许', denied: '已拒绝', closed_local: '本机已处理 · 自动关闭', fallback: 'hook 超时 · 回落本机对话框' };
  return (
    <div className="page">
      <div className="section-title">
        <h2>待确认收件箱</h2>
        <p>所有设备、所有 AI CLI 停下来等你的地方，最久的排最前。卡片标明它是怎么被发现的，以及你在这里的动作到底是什么。</p>
      </div>
      <div className="two-col">
        <div className="card">
          <div className="inbox-row head"><span>设备 / 会话</span><span>请求</span><span>已等待</span><span>操作</span></div>
          {pending.length === 0 && <div className="empty">收件箱已清空。</div>}
          {pending.map((a) => {
            const lv = levelLabel(a.level);
            const s = st.sessions[a.sid];
            const d = a.device_id ? st.devices[a.device_id] : undefined;
            return (
              <div className="inbox-row" key={a.key}>
                <div className="dev"><b>{d?.name ?? a.device_id}</b><small><span className={`tool ${a.agent}`} style={{ width: 16, height: 16, fontSize: 9, verticalAlign: -3, marginRight: 4 }}>{toolShort(a.agent)}</span>{s?.name ?? `#${a.sid}`}</small></div>
                <div className="req"><span className="kind"><span className={`tag ${lv.cls}`}>{lv.label}</span>{toolLabel(a.agent)} · {a.tool}</span><span><code>{a.summary}</code></span>{a.cwd && <span className="kind">{a.cwd}</span>}</div>
                <div className="wait">{waited(a.created_at)}</div>
                <div className="ops"><ApprovalButtons a={a} sm onOpen={() => onOpenSession(a.sid)} /></div>
              </div>
            );
          })}
          {done.length > 0 && <div className="card-head" style={{ borderTop: '1px solid var(--border)' }}>最近已处理</div>}
          {done.map((a) => {
            const lv = levelLabel(a.level);
            const d = a.device_id ? st.devices[a.device_id] : undefined;
            return (
              <div className="inbox-row done" key={a.key}>
                <div className="dev"><b>{d?.name ?? a.device_id}</b><small>{toolLabel(a.agent)}</small></div>
                <div className="req"><span className="kind"><span className={`tag ${lv.cls}`}>{lv.label}</span>{a.tool}</span><span><code>{a.summary}</code></span></div>
                <div className="wait" style={{ color: 'var(--muted)', fontFamily: 'var(--font-ui)' }}>{a.decided_at ? new Date(a.decided_at).toLocaleTimeString() : ''}</div>
                <div className="ops"><span style={{ fontSize: 12, color: 'var(--muted)' }}>{statusText[a.status] ?? a.status}{a.decided_by ? ` · ${a.decided_by}` : ''}</span></div>
              </div>
            );
          })}
        </div>
        <div className="card">
          <div className="card-head">三种卡片，三种动作</div>
          <ul className="policy">
            <li><span className="tag a">hook 决定</span><span>该会话开启了「远程优先」：Claude Code 的 PermissionRequest hook 挂起等你，你点「允许」就是 hook 的决定。挂起期间终端里的对话框仍会显示，先答的一方生效；超时后回落。</span></li>
            <li><span className="tag b">按键</span><span>默认「通知模式」：hook 只登记并推送，终端里的对话框照常弹出。你在这里点的是「发送 1 / 发送 3」（Claude）或「发送 y / n」，等同于坐在电脑前按键。Codex 第一阶段只走这条路。</span></li>
            <li><span className="tag c">疑似</span><span>没有 hooks 的工具靠输出停顿加末行提示符推断，低置信。只提示，不自动处理，也不替你按键；卡片只有「打开终端确认」一个按钮。</span></li>
            <li><span className="tag n">规则</span><span>远程权限不高于本地：以 <code>bypassPermissions</code> 启动的会话根本不会触发 hook，因此不会出现在这里。本机答过的请求会自动关闭。</span></li>
          </ul>
        </div>
      </div>
    </div>
  );
}
