package session

import (
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/wfighter1/terminalX/internal/proto"
)

func requireTmux(t *testing.T) {
	t.Helper()
	requireBash(t)
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	// Never touch the socket a real agent on this machine owns.
	t.Setenv("TX_TMUX_SERVER", "terminalx-test")
}

func startPersisted(t *testing.T, c *collector, sid uint32, dir, conf string) *Session {
	t.Helper()
	s, err := Start(Options{
		SID:             sid,
		Shell:           "bash",
		Cwd:             dir,
		Tool:            ToolSpec{Tool: "shell"},
		Env:             []string{"PS1=$ ", "TERM=xterm-256color"},
		Cols:            80,
		Rows:            24,
		Persist:         true,
		PersistConf:     conf,
		PersistStateDir: dir,
		OnOutput:        c.on,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	return s
}

// The product's promise is that a session outlives the agent process. Suspend
// stands in for the agent exiting (systemd restart, upgrade, crash); Adopt is
// what the next agent does. The shell variable proves it is the very same
// shell, not a fresh one.
func TestSessionSurvivesAgentRestart(t *testing.T) {
	requireTmux(t)
	dir := t.TempDir()
	conf, err := WriteTmuxConf(dir)
	if err != nil {
		t.Fatal(err)
	}
	const sid = 424242
	name := PersistName(sid)
	t.Cleanup(func() { _ = TmuxKill(conf, name) })

	c1 := &collector{}
	s1 := startPersisted(t, c1, sid, dir, conf)
	if !s1.Persisted() {
		t.Fatal("session should be tmux-backed")
	}
	if err := s1.Write([]byte("TXMARK=survivor-7788\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := s1.Write([]byte("echo ready-$((6*7))\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	c1.waitFor(t, "ready-42", 10*time.Second)

	// The agent goes away; the session must not.
	s1.Suspend()
	if !TmuxHasSession(conf, name) {
		t.Fatal("Suspend killed the tmux session; it must survive the agent")
	}

	c2 := &collector{}
	s2, err := Adopt(Options{
		SID:             sid,
		Shell:           "bash",
		Cwd:             dir,
		Tool:            ToolSpec{Tool: "shell"},
		Cols:            80,
		Rows:            24,
		Persist:         true,
		PersistConf:     conf,
		PersistStateDir: dir,
		OnOutput:        c2.on,
	})
	if err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	t.Cleanup(s2.Close)

	// The scrollback seeded into the ring is what a console attaching after
	// the restart replays, so it must already hold the history the previous
	// agent had streamed. (It is seeded into the ring directly, not emitted,
	// so it is visible through Snapshot rather than through OnOutput.)
	if snap, _ := s2.Snapshot(); !strings.Contains(string(snap), "ready-42") {
		t.Errorf("adopt did not seed the ring from the tmux scrollback:\n%s", snap)
	}
	if err := s2.Write([]byte("echo mark=$TXMARK\n")); err != nil {
		t.Fatalf("Write after adopt: %v", err)
	}
	c2.waitFor(t, "mark=survivor-7788", 10*time.Second)

	// Close, unlike Suspend, really ends it.
	s2.Close()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !TmuxHasSession(conf, name) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("Close left the tmux session running")
}

// The exit status has to survive the tmux indirection: the process the agent
// owns is the client, whose status is always 0.
func TestPersistedSessionReportsShellExitCode(t *testing.T) {
	requireTmux(t)
	dir := t.TempDir()
	conf, err := WriteTmuxConf(dir)
	if err != nil {
		t.Fatal(err)
	}
	const sid = 424243
	t.Cleanup(func() { _ = TmuxKill(conf, PersistName(sid)) })

	c := &collector{}
	codes := make(chan int32, 1)
	s, err := Start(Options{
		SID:             sid,
		Shell:           "bash",
		Cwd:             dir,
		Tool:            ToolSpec{Tool: "shell"},
		Cols:            80,
		Rows:            24,
		Persist:         true,
		PersistConf:     conf,
		PersistStateDir: dir,
		OnOutput:        c.on,
		OnExit:          func(code int32, _ *proto.Resumable) { codes <- code },
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(s.Close)
	if err := s.Write([]byte("exit 5\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	select {
	case got := <-codes:
		if got != 5 {
			t.Fatalf("exit code = %d, want 5 (the tmux client's own status is not the shell's)", got)
		}
	case <-time.After(15 * time.Second):
		t.Fatalf("no exit reported. Output:\n%s", c.String())
	}
}

func TestPersistNameRoundTrip(t *testing.T) {
	if got := PersistName(7); got != "tx-7" {
		t.Fatalf("PersistName(7) = %q", got)
	}
	if sid, ok := PersistSID("tx-7"); !ok || sid != 7 {
		t.Fatalf("PersistSID(tx-7) = %d, %v", sid, ok)
	}
	for _, bad := range []string{"tx-", "tx-x", "other-7", "7", "tx-99999999999999999999"} {
		if _, ok := PersistSID(bad); ok {
			t.Errorf("PersistSID(%q) should not parse", bad)
		}
	}
}

func TestTmuxLaunchArgs(t *testing.T) {
	t.Setenv("TX_TMUX_SERVER", "terminalx-test")
	argv := tmuxLaunch("/c/tmux.conf", "tx-9", 100, 40, "/w", "/bin/bash", nil, "/state/exit")
	joined := strings.Join(argv, " ")
	for _, want := range []string{
		"-L terminalx-test", "-f /c/tmux.conf",
		"new-session -A -s tx-9", "-x 100 -y 40", "-c /w",
		"/bin/sh -c", "/bin/bash",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("argv %q missing %q", joined, want)
		}
	}
	if !strings.Contains(joined, "'/state/exit'") {
		t.Errorf("exit file not quoted into the wrapper: %q", joined)
	}
	// Without a state dir there is no wrapper, and the shell runs directly.
	plain := strings.Join(tmuxLaunch("", "tx-9", 100, 40, "", "/bin/bash", []string{"-l"}, ""), " ")
	if strings.Contains(plain, "/bin/sh -c") || !strings.HasSuffix(plain, "-- /bin/bash -l") {
		t.Errorf("unwrapped argv = %q", plain)
	}
}

// TestMain keeps the package's tmux sessions on a private socket and tears
// the server down, so a test run never touches — or leaves behind — sessions
// on the socket a real agent uses.
func TestMain(m *testing.M) {
	_ = os.Setenv("TX_TMUX_SERVER", "terminalx-test")
	code := m.Run()
	_ = exec.Command("tmux", "-L", "terminalx-test", "kill-server").Run()
	os.Exit(code)
}
