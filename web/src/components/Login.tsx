import { useState, type FormEvent } from 'react';
import { api } from '../api';

export default function Login({ onLogin }: { onLogin: () => void }) {
  const [pw, setPw] = useState('');
  const [err, setErr] = useState('');
  const [busy, setBusy] = useState(false);
  async function submit(e: FormEvent) {
    e.preventDefault();
    setBusy(true);
    setErr('');
    try {
      await api.login(pw);
      onLogin();
    } catch (ex) {
      setErr(ex instanceof Error ? ex.message : '登录失败');
    } finally {
      setBusy(false);
    }
  }
  return (
    <div className="login">
      <form className="card" onSubmit={submit}>
        <div className="brand"><span className="brand-mark">tX</span>terminalX <small>自建中转</small></div>
        <p style={{ color: 'var(--muted)', fontSize: 13 }}>输入中转节点的管理密码。第一阶段为单用户；Passkey 登录在 1.1。</p>
        <input type="password" autoFocus placeholder="管理密码" value={pw} onChange={(e) => setPw(e.target.value)} aria-label="管理密码" />
        {err && <p className="err-text">{err}</p>}
        <button className="btn primary" disabled={busy || !pw}>{busy ? '登录中…' : '登录'}</button>
      </form>
    </div>
  );
}
