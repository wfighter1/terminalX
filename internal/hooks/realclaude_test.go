package hooks_test

// Manual integration test: drives a REAL `claude -p` (headless) through the
// settings file ClaudeSettings generates and records every hook payload.
// It covers the parts a headless run can prove: that http hooks fire at all,
// that Claude interpolates $TX_HOOK_TOKEN from allowedEnvVars, and that the
// SessionStart command-hook fallback (curl, because AgentExe is empty here)
// reaches the endpoint.
//
// It deliberately does NOT cover approvals: measured on Claude Code 2.1.260,
// `claude -p` never emits PermissionRequest — a tool that needs approval is
// reported in permission_denials and the turn ends. The approval round trip
// is proven interactively by TestRealClaudePTYApproval.
//
// Skipped unless TX_REAL_CLAUDE=1, because it needs a working `claude` login
// and spends tokens. Run it as:
//
//	TX_REAL_CLAUDE=1 go test ./internal/hooks -run TestRealClaude -v
//
// Optional knobs:
//
//	TX_REAL_CLAUDE_DECISION=allow|deny   reply to PermissionRequest (default: {} )
//	TX_REAL_CLAUDE_PERMISSION_MODE=...   passed to claude --permission-mode

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wfighter1/terminalX/internal/hooks"
)

type hookCall struct {
	Event string
	Body  json.RawMessage
	Auth  string
}

func TestRealClaudeHooks(t *testing.T) {
	if os.Getenv("TX_REAL_CLAUDE") != "1" {
		t.Skip("set TX_REAL_CLAUDE=1 to run against a real claude binary")
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude not in PATH")
	}

	const token = "probe-token"
	decision := os.Getenv("TX_REAL_CLAUDE_DECISION")

	var mu sync.Mutex
	var calls []hookCall

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port

	mux := http.NewServeMux()
	mux.HandleFunc("/hook/claude/1/", func(w http.ResponseWriter, r *http.Request) {
		ev := strings.TrimPrefix(r.URL.Path, "/hook/claude/1/")
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		calls = append(calls, hookCall{Event: ev, Body: b, Auth: r.Header.Get("Authorization")})
		mu.Unlock()
		t.Logf("HOOK %s auth=%q\n%s", ev, r.Header.Get("Authorization"), pretty(b))

		w.Header().Set("Content-Type", "application/json")
		if ev == "PermissionRequest" && decision != "" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"hookSpecificOutput": map[string]any{
					"hookEventName": "PermissionRequest",
					"decision":      map[string]any{"behavior": decision},
				},
			})
			t.Logf("replied decision.behavior=%s", decision)
			return
		}
		_, _ = w.Write([]byte("{}"))
	})
	mux.HandleFunc("/statusline/1", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		calls = append(calls, hookCall{Event: "statusLine", Body: b})
		mu.Unlock()
		t.Logf("statusLine\n%s", pretty(b))
		_, _ = w.Write([]byte("tx"))
	})
	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	root := t.TempDir()
	settings, err := hooks.WriteClaudeSettings(root, hooks.ClaudeSettingsOptions{
		SID: 1, Port: port, Token: token,
		RemoteFirstTimeout: 60, NotifyTimeout: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if sb, err := os.ReadFile(settings); err == nil {
		t.Logf("settings:\n%s", sb)
	}

	work := filepath.Join(root, "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}

	prompt := os.Getenv("TX_REAL_CLAUDE_PROMPT")
	if prompt == "" {
		prompt = "Run the bash command: echo probe-marker-7788. Use the Bash tool."
	}
	args := []string{"--settings", settings, "-p", prompt, "--output-format", "json"}
	if pm := os.Getenv("TX_REAL_CLAUDE_PERMISSION_MODE"); pm != "" {
		args = append(args, "--permission-mode", pm)
	}
	if extra := os.Getenv("TX_REAL_CLAUDE_ARGS"); extra != "" {
		args = append(args, strings.Fields(extra)...)
	}
	cmd := exec.Command("claude", args...)
	cmd.Dir = work
	cmd.Env = append(os.Environ(), "TX_HOOK_TOKEN="+token, "CLAUDE_CODE_ENTRYPOINT=", "CLAUDECODE=")
	out, runErr := cmd.CombinedOutput()
	t.Logf("claude exit=%v output:\n%s", runErr, truncate(string(out), 2000))

	time.Sleep(500 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(calls) == 0 {
		t.Fatal("no hook fired: http hooks did not reach the endpoint at all")
	}
	seen := map[string]int{}
	for _, c := range calls {
		seen[c.Event]++
		// The SessionStart command hook carries the token literally, the
		// http hooks carry the interpolated $TX_HOOK_TOKEN; both must end up
		// as the same bearer.
		if c.Event != "statusLine" && c.Auth != "Bearer "+token {
			t.Errorf("%s: Authorization header = %q, want bearer with the interpolated token "+
				"(allowedEnvVars did not substitute $TX_HOOK_TOKEN)", c.Event, c.Auth)
		}
	}
	t.Logf("hook calls by event: %v", seen)
	if seen["SessionStart"] == 0 {
		t.Error("SessionStart never fired: Claude skips http hooks for it, so the " +
			"command-hook forwarder in ClaudeSettings is what must deliver it")
	}
	if seen["UserPromptSubmit"] == 0 || seen["PostToolUse"] == 0 {
		t.Errorf("http hooks did not deliver the turn events: %v", seen)
	}
}

func pretty(b []byte) string {
	var v any
	if json.Unmarshal(b, &v) == nil {
		o, _ := json.MarshalIndent(v, "", "  ")
		return string(o)
	}
	return string(b)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
