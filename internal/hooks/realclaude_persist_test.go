package hooks_test

// Manual integration test: a real interactive `claude` must survive the agent
// process going away. It is the tmux persistence path end to end —
//
//	claude runs in a persisted session
//	  → the agent suspends (upgrade / crash / systemd restart)
//	  → a new agent adopts the session
//	  → the same claude is still there and still answers
//
// Skipped unless TX_REAL_CLAUDE=1. Prerequisites are the same as
// TestRealClaudePTYApproval: `claude` is logged in and the working directory
// is already trusted.
//
//	TX_REAL_CLAUDE=1 go test ./internal/hooks -run TestRealClaudeSurvives -v -timeout 10m

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wfighter1/terminalX/internal/proto"
	"github.com/wfighter1/terminalX/internal/session"
)

func TestRealClaudeSurvivesAgentRestart(t *testing.T) {
	if os.Getenv("TX_REAL_CLAUDE") != "1" {
		t.Skip("set TX_REAL_CLAUDE=1 to run against a real claude binary")
	}
	for _, bin := range []string{"claude", "tmux"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not in PATH", bin)
		}
	}
	t.Setenv("TX_TMUX_SERVER", "terminalx-test-claude")

	dir := t.TempDir()
	conf, err := session.WriteTmuxConf(dir)
	if err != nil {
		t.Fatal(err)
	}
	const sid = 515151
	t.Cleanup(func() { _ = session.TmuxKill(conf, session.PersistName(sid)) })

	opts := func(out *bytes.Buffer, mu *sync.Mutex) session.Options {
		return session.Options{
			SID:             sid,
			Cwd:             repoRoot(t),
			Tool:            session.ToolSpec{Tool: proto.ToolClaude, PermissionMode: "default"},
			Cols:            120,
			Rows:            45,
			Persist:         true,
			PersistConf:     conf,
			PersistStateDir: dir,
			OnOutput: func(_ uint64, data []byte) {
				mu.Lock()
				out.Write(data)
				mu.Unlock()
			},
		}
	}

	var mu1 sync.Mutex
	var buf1 bytes.Buffer
	s1, err := session.Start(opts(&buf1, &mu1))
	if err != nil {
		t.Fatal(err)
	}
	screen1 := func() string {
		mu1.Lock()
		defer mu1.Unlock()
		return stripANSI(buf1.String())
	}
	waitForText(t, screen1, "? for shortcuts", 90*time.Second,
		"claude never reached its prompt (onboarding or trust dialog?)")

	// The agent goes away. tmux keeps claude running.
	s1.Suspend()
	if !session.TmuxHasSession(conf, session.PersistName(sid)) {
		t.Fatal("suspending the agent killed the claude session")
	}

	var mu2 sync.Mutex
	var buf2 bytes.Buffer
	s2, err := session.Adopt(opts(&buf2, &mu2))
	if err != nil {
		t.Fatalf("Adopt: %v", err)
	}
	t.Cleanup(s2.Close)
	screen2 := func() string {
		mu2.Lock()
		defer mu2.Unlock()
		snap, _ := s2.Snapshot()
		return stripANSI(string(snap)) + stripANSI(buf2.String())
	}
	// The adopted client gets a full repaint from tmux, so claude's own UI
	// must come back without restarting it.
	waitForText(t, screen2, "? for shortcuts", 60*time.Second,
		"the adopted session did not show claude's UI again")

	// And it still takes keystrokes: typing into the composer costs no tokens
	// and proves the input path survived the restart.
	const probe = "adopted-probe-9911"
	if err := s2.Write([]byte(probe)); err != nil {
		t.Fatal(err)
	}
	waitForText(t, screen2, probe, 30*time.Second,
		"the adopted claude did not echo typed input")
	_ = s2.Write([]byte{0x15}) // Ctrl-U: leave the composer empty
	t.Log("claude survived the agent restart and is still interactive")
}

func waitForText(t *testing.T, screen func() string, want string, d time.Duration, msg string) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if strings.Contains(screen(), want) {
			return
		}
		time.Sleep(300 * time.Millisecond)
	}
	t.Fatalf("%s (looking for %q). Screen:\n%s", msg, want, tail(screen(), 2500))
}
