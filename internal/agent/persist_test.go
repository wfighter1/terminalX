package agent

import (
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/wfighter1/terminalX/internal/proto"
	"github.com/wfighter1/terminalX/internal/session"
)

func newPersistAgent(t *testing.T, root string) *Agent {
	t.Helper()
	cfgPath := filepath.Join(root, "agent.json")
	cfg := &Config{
		RelayURL: "http://127.0.0.1:1", DeviceID: "d", DeviceToken: "t",
		AgentID: "a", HookToken: "tok",
	}
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatal(err)
	}
	a, err := New(cfg, cfgPath, slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn})))
	if err != nil {
		t.Fatal(err)
	}
	a.setupPersistence()
	if !a.persist {
		t.Skip("tmux-backed persistence unavailable")
	}
	return a
}

// A restarted agent must find the sessions its predecessor left running and
// take them back — otherwise systemd's Restart=always silently destroys every
// session, which is exactly the failure this product exists to prevent.
func TestAgentAdoptsSessionsAfterRestart(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not installed")
	}
	t.Setenv("TX_TMUX_SERVER", "terminalx-test-agent")

	root := t.TempDir()
	first := newPersistAgent(t, root)
	s, err := first.openSession(proto.OpenRequest{Tool: proto.ToolShell, Shell: "bash", Cwd: root})
	if err != nil {
		t.Fatalf("openSession: %v", err)
	}
	sid := s.SID()
	t.Cleanup(func() { _ = session.TmuxKill(first.tmuxConf, session.PersistName(sid)) })
	if !s.Persisted() {
		t.Fatal("session should be tmux-backed")
	}
	if err := s.Write([]byte("TXAGENT=kept-4242\n")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 10*time.Second, func() bool {
		snap, _ := s.Snapshot()
		return strings.Contains(string(snap), "TXAGENT=")
	}, "shell did not echo the marker assignment")

	// The agent process goes away (upgrade, crash, systemd restart).
	first.suspendAllSessions()
	if first.session(sid) != nil {
		t.Fatal("suspend should forget the session objects")
	}
	if !session.TmuxHasSession(first.tmuxConf, session.PersistName(sid)) {
		t.Fatal("the tmux session must outlive the agent")
	}

	second := newPersistAgent(t, root)
	second.adoptSessions()
	adopted := second.session(sid)
	if adopted == nil {
		t.Fatalf("session %d was not adopted; sessions alive in tmux: %v",
			sid, session.TmuxSessions(second.tmuxConf))
	}
	if got := adopted.Info().Shell; got != "bash" {
		t.Errorf("adopted shell = %q, want bash (meta.json did not round-trip)", got)
	}
	if err := adopted.Write([]byte("echo agentmark=$TXAGENT\n")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 10*time.Second, func() bool {
		snap, _ := adopted.Snapshot()
		return strings.Contains(string(snap), "agentmark=kept-4242")
	}, "the adopted session is not the same shell: $TXAGENT was lost")

	// Closing for real removes both the tmux session and its metadata.
	second.closeSession(sid)
	waitFor(t, 5*time.Second, func() bool {
		return !session.TmuxHasSession(second.tmuxConf, session.PersistName(sid))
	}, "close left the tmux session running")
	if _, err := os.Stat(second.metaPath(sid)); !os.IsNotExist(err) {
		t.Errorf("meta file still there after close: %v", err)
	}
}

// A meta file whose tmux session is gone is stale: it must not resurrect a
// session in the console, and its directory must be cleaned up.
func TestAdoptDropsStaleSessionDirs(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	t.Setenv("TX_TMUX_SERVER", "terminalx-test-stale")
	root := t.TempDir()
	a := newPersistAgent(t, root)

	const sid = 987654
	a.writeMeta(sessionMeta{SID: sid, Tool: proto.ToolShell, Shell: "bash", Cwd: root})
	a.adoptSessions()
	if a.session(sid) != nil {
		t.Fatal("a session with no tmux session must not be adopted")
	}
	if _, err := os.Stat(a.sessionDir(sid)); !os.IsNotExist(err) {
		t.Errorf("stale session dir was not removed: %v", err)
	}
}

// TestMain isolates the package's tmux usage on a private socket and removes
// it afterwards: agent tests start real sessions, and with persistence on
// they would otherwise survive the test run by design.
func TestMain(m *testing.M) {
	_ = os.Setenv("TX_TMUX_SERVER", "terminalx-test-agent")
	code := m.Run()
	_ = exec.Command("tmux", "-L", "terminalx-test-agent", "kill-server").Run()
	os.Exit(code)
}
