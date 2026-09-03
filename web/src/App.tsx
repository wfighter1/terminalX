import { useEffect, useState } from 'react';
import { api } from './api';
import { StoreProvider, useStore } from './store';
import Login from './components/Login';
import TopBar from './components/TopBar';
import DeviceRail from './components/DeviceRail';
import TerminalPane from './components/TerminalPane';
import SidePanel from './components/SidePanel';
import InboxPage from './components/InboxPage';
import DevicesPage from './components/DevicesPage';
import SettingsPage from './components/SettingsPage';
import NewSessionDialog from './components/NewSessionDialog';
import PairDialog from './components/PairDialog';

export type Page = 'console' | 'inbox' | 'devices' | 'settings';

export default function App() {
  const [auth, setAuth] = useState<'checking' | 'in' | 'out'>('checking');
  useEffect(() => {
    api.me().then((r) => setAuth(r.authenticated ? 'in' : 'out')).catch(() => setAuth('out'));
  }, []);
  if (auth === 'checking') return <div className="login"><p style={{ color: 'var(--muted)' }}>正在连接…</p></div>;
  if (auth === 'out') return <Login onLogin={() => setAuth('in')} />;
  return (
    <StoreProvider>
      <Shell onLogout={() => setAuth('out')} />
    </StoreProvider>
  );
}

function Shell({ onLogout }: { onLogout: () => void }) {
  const { st, dispatch } = useStore();
  const [page, setPage] = useState<Page>('console');
  const [showNew, setShowNew] = useState<string | null>(null); // device id
  const [showPair, setShowPair] = useState(false);

  return (
    <div className="app">
      <TopBar page={page} setPage={setPage} onLogout={onLogout} />
      {page === 'console' && (
        <div className="main">
          <DeviceRail onNewSession={(d) => setShowNew(d)} onPair={() => setShowPair(true)} />
          <TerminalPane onNewSession={() => setShowNew(Object.keys(st.devices)[0] ?? '')} />
          <SidePanel onOpenInbox={() => setPage('inbox')} />
        </div>
      )}
      {page === 'inbox' && <InboxPage onOpenSession={(sid) => { dispatch({ type: 'openTab', sid }); setPage('console'); }} />}
      {page === 'devices' && <DevicesPage onPair={() => setShowPair(true)} onNewSession={(d) => { setShowNew(d); }} />}
      {page === 'settings' && <SettingsPage />}
      {showNew !== null && <NewSessionDialog deviceId={showNew} onClose={() => { setShowNew(null); setPage('console'); }} />}
      {showPair && <PairDialog onClose={() => setShowPair(false)} />}
      {st.toast && <div className={`toast${st.toast.err ? ' err' : ''}`} role="status">{st.toast.text}</div>}
    </div>
  );
}
