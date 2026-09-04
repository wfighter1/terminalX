package hooks_test

// Manual integration test: drives a REAL interactive `claude` inside a real
// PTY through internal/session — the same path the agent uses — and proves
// the approval round trip end to end:
//
//	claude asks for permission
//	  → PermissionRequest http hook reaches the agent's endpoint and BLOCKS
//	  → the remote side decides "allow"
//	  → claude runs the tool and prints "Allowed by PermissionRequest hook"
//
// This is the executable form of phase-1 spike #2. It also proves the
// SessionStart forwarder (`tx-agent hook`), because Claude does not deliver
// SessionStart over http at all.
//
// Skipped unless TX_REAL_CLAUDE=1: it needs a logged-in `claude` and spends
// tokens. Run it as:
//
//	TX_REAL_CLAUDE=1 go test ./internal/hooks -run TestRealClaudePTY -v -timeout 10m
//
// Prerequisites on the machine: `claude` has been run once interactively (so
// onboarding is done) and the working directory is already trusted. The test
// runs claude in the repository root for that reason; if the trust dialog
// still appears the test fails on the SessionStart wait with a hint.

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wfighter1/terminalX/internal/hooks"
	"github.com/wfighter1/terminalX/internal/proto"
	"github.com/wfighter1/terminalX/internal/session"
)

const (
	realSID    = 1
	realToken  = "probe-token"
	realPrompt = "Use the Bash tool to run exactly: rm -rf /tmp/tx-probe-nope && curl -s https://example.com/x"
)

type ptyEvents struct {
	mu       sync.Mutex
	approval chan proto.Approval
	closed   chan proto.Approval
	started  chan struct{}
	toolSess string
	once     sync.Once
}

func (e *ptyEvents) SessionState(uint32, proto.SessionState, proto.NeedKind, proto.Source, proto.Confidence) {
}
func (e *ptyEvents) ApprovalNew(a proto.Approval) {
	select {
	case e.approval <- a:
	default:
	}
}
func (e *ptyEvents) ApprovalClosed(a proto.Approval, _ string) {
	select {
	case e.closed <- a:
	default:
	}
}
func (e *ptyEvents) SessionUpdated(uint32, *float64, *float64) {}
func (e *ptyEvents) ToolSession(_ uint32, _, id string) {
	e.mu.Lock()
	e.toolSess = id
	e.mu.Unlock()
	e.once.Do(func() { close(e.started) })
}
func (e *ptyEvents) ToolExited(uint32) {}

func TestRealClaudePTYApproval(t *testing.T) {
	if os.Getenv("TX_REAL_CLAUDE") != "1" {
		t.Skip("set TX_REAL_CLAUDE=1 to run against a real claude binary")
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude not in PATH")
	}

	ev := &ptyEvents{
		approval: make(chan proto.Approval, 4),
		closed:   make(chan proto.Approval, 4),
		started:  make(chan struct{}),
	}
	srv := &hooks.Server{
		Token:  realToken,
		Events: ev,
		Lookup: func(sid uint32) (string, proto.ApprovalMode, bool) {
			if sid != realSID {
				return "", "", false
			}
			return proto.ToolClaude, proto.ApprovalRemoteFirst, true
		},
		RemoteFirstTimeout: 5 * time.Minute,
	}
	port, err := srv.Listen(0)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve() }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})

	root := t.TempDir()
	agentExe := buildAgent(t, root)
	settings, err := hooks.WriteClaudeSettings(root, hooks.ClaudeSettingsOptions{
		SID: realSID, Port: port, Token: realToken, AgentExe: agentExe,
		RemoteFirstTimeout: 300, NotifyTimeout: 10,
	})
	if err != nil {
		t.Fatal(err)
	}

	var outMu sync.Mutex
	var out bytes.Buffer
	sess, err := session.Start(session.Options{
		SID: realSID,
		Cwd: repoRoot(t),
		Tool: session.ToolSpec{
			Tool:           proto.ToolClaude,
			PermissionMode: "default",
			SettingsPath:   settings,
		},
		Env:  []string{"TX_HOOK_TOKEN=" + realToken},
		Cols: 120, Rows: 45,
		OnOutput: func(_ uint64, data []byte) {
			outMu.Lock()
			out.Write(data)
			outMu.Unlock()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sess.Close)
	screen := func() string {
		outMu.Lock()
		defer outMu.Unlock()
		return stripANSI(out.String())
	}

	// SessionStart arrives only through the command-hook forwarder, so this
	// wait covers both `tx-agent hook` and claude reaching a usable prompt.
	select {
	case <-ev.started:
	case <-time.After(90 * time.Second):
		t.Fatalf("SessionStart never reached the endpoint in 90s — the forwarder failed, "+
			"or claude is stuck on onboarding/trust in %s. Screen:\n%s", repoRoot(t), tail(screen(), 1500))
	}

	// Give the TUI a moment to accept input, then submit the prompt.
	time.Sleep(3 * time.Second)
	if err := sess.Write([]byte(realPrompt)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond)
	if err := sess.Write([]byte("\r")); err != nil {
		t.Fatal(err)
	}

	var a proto.Approval
	select {
	case a = <-ev.approval:
	case <-time.After(150 * time.Second):
		t.Fatalf("no PermissionRequest in 150s. Screen:\n%s", tail(screen(), 2000))
	}
	if a.Tool != "Bash" {
		t.Errorf("approval tool = %q, want Bash", a.Tool)
	}
	if !strings.Contains(string(a.Input), "example.com") {
		t.Errorf("approval input = %s", a.Input)
	}
	// Documented and measured: PermissionRequest carries no tool_use_id, which
	// is why the approval key hashes session_id + tool_name + tool_input.
	if a.Key == "" {
		t.Error("approval key is empty")
	}
	t.Logf("approval: tool=%s summary=%q key=%s", a.Tool, a.Summary, a.Key)

	if _, ok := srv.Store.Decide(a.Key, "allow", "test"); !ok {
		t.Fatal("Decide did not find the pending approval")
	}

	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(screen(), "Allowed by PermissionRequest hook") {
			t.Log("claude confirmed the tool ran because the hook allowed it")
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("claude never acknowledged the hook decision. Screen:\n%s", tail(screen(), 3000))
}

// buildAgent compiles tx-agent so the generated settings can point command
// hooks at a real binary.
func buildAgent(t *testing.T, dir string) string {
	t.Helper()
	exe := filepath.Join(dir, "tx-agent")
	cmd := exec.Command("go", "build", "-o", exe, "./cmd/tx-agent")
	cmd.Dir = repoRoot(t)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build tx-agent: %v\n%s", err, b)
	}
	return exe
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd() // …/internal/hooks
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Dir(filepath.Dir(wd))
}

// stripANSI removes escape sequences so assertions can look for plain text.
// Claude positions words with cursor-column escapes instead of spaces, so the
// result is readable but its spacing is not meaningful.
//
// String sequences (OSC, DCS, APC …) end at BEL *or* at ST (ESC \\); tmux
// emits the ST form, and a scanner that only looks for BEL swallows the whole
// rest of the stream.
func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		c := s[i]
		if c != 0x1b {
			if c != 0x0e && c != 0x0f { // SO / SI charset shifts
				b.WriteByte(c)
			}
			i++
			continue
		}
		i++
		if i >= len(s) {
			break
		}
		switch s[i] {
		case '[': // CSI: 0x20-0x3f parameters, then one final byte
			i++
			for i < len(s) && s[i] >= 0x20 && s[i] <= 0x3f {
				i++
			}
			if i < len(s) {
				i++
			}
		case ']', 'P', 'X', '^', '_': // OSC / DCS / SOS / PM / APC
			i++
			for i < len(s) {
				if s[i] == 0x07 {
					i++
					break
				}
				if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '\\' {
					i += 2
					break
				}
				i++
			}
		case '(', ')', '*', '+': // charset designators take one more byte
			i += 2
		default:
			i++
		}
	}
	return b.String()
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}
