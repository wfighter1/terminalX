package hooks

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/wfighter1/terminalX/internal/proto"
)

type recEvents struct {
	mu       sync.Mutex
	states   []proto.SessionState
	kinds    []proto.NeedKind
	news     []proto.Approval
	closed   []string // "<key>:<by>"
	cost     *float64
	ctx      *float64
	toolSess string
	exited   int
}

func (r *recEvents) SessionState(sid uint32, st proto.SessionState, kind proto.NeedKind, src proto.Source, conf proto.Confidence) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.states = append(r.states, st)
	r.kinds = append(r.kinds, kind)
}
func (r *recEvents) ApprovalNew(a proto.Approval) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.news = append(r.news, a)
}
func (r *recEvents) ApprovalClosed(a proto.Approval, by string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.closed = append(r.closed, a.Key+":"+by+":"+a.Status)
}
func (r *recEvents) SessionUpdated(sid uint32, cost, ctx *float64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cost, r.ctx = cost, ctx
}
func (r *recEvents) ToolSession(sid uint32, agent, id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.toolSess = agent + ":" + id
}
func (r *recEvents) ToolExited(sid uint32) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.exited++
}

func newTestServer(t *testing.T, mode proto.ApprovalMode) (*httptest.Server, *Server, *recEvents) {
	t.Helper()
	ev := &recEvents{}
	s := &Server{
		Token:  "tok",
		Events: ev,
		Lookup: func(sid uint32) (string, proto.ApprovalMode, bool) {
			if sid != 7 {
				return "", "", false
			}
			return proto.ToolClaude, mode, true
		},
		RemoteFirstTimeout: 300 * time.Millisecond,
	}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts, s, ev
}

func post(t *testing.T, url, token string, body any) (*http.Response, []byte) {
	t.Helper()
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, url, bytes.NewReader(b))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post %s: %v", url, err)
	}
	defer resp.Body.Close()
	var out bytes.Buffer
	_, _ = out.ReadFrom(resp.Body)
	return resp, out.Bytes()
}

var permReq = map[string]any{
	"session_id": "cs-1", "cwd": "/w", "hook_event_name": "PermissionRequest",
	"tool_name": "Bash", "tool_input": map[string]any{"command": "go test ./...", "timeout": 5},
}

func TestAuthAndUnknownSession(t *testing.T) {
	ts, _, _ := newTestServer(t, proto.ApprovalNotify)
	if resp, _ := post(t, ts.URL+"/hook/claude/7/Stop", "wrong", map[string]any{}); resp.StatusCode != 401 {
		t.Fatalf("want 401 got %d", resp.StatusCode)
	}
	if resp, _ := post(t, ts.URL+"/hook/claude/9/Stop", "tok", map[string]any{}); resp.StatusCode != 404 {
		t.Fatalf("want 404 got %d", resp.StatusCode)
	}
}

func TestNotifyModePermissionRequest(t *testing.T) {
	ts, s, ev := newTestServer(t, proto.ApprovalNotify)
	start := time.Now()
	resp, body := post(t, ts.URL+"/hook/claude/7/PermissionRequest", "tok", permReq)
	if resp.StatusCode != 200 || time.Since(start) > 200*time.Millisecond {
		t.Fatalf("notify mode should return immediately with 200: %d after %v", resp.StatusCode, time.Since(start))
	}
	if string(bytes.TrimSpace(body)) != "{}" {
		t.Fatalf("notify mode body should be {} got %s", body)
	}
	ev.mu.Lock()
	defer ev.mu.Unlock()
	if len(ev.news) != 1 || ev.news[0].Level != proto.LevelKeys || ev.news[0].Summary != "go test ./..." || ev.news[0].Mode != proto.ApprovalNotify {
		t.Fatalf("approval.new: %+v", ev.news)
	}
	if ev.states[len(ev.states)-1] != proto.StateNeedsInput || ev.kinds[len(ev.kinds)-1] != proto.NeedPermission {
		t.Fatalf("state: %v %v", ev.states, ev.kinds)
	}
	if len(s.Store.Pending()) != 1 {
		t.Fatalf("pending: %d", len(s.Store.Pending()))
	}
	// Dedupe: same request within 60 s does not emit a second approval.new.
	ev.mu.Unlock()
	post(t, ts.URL+"/hook/claude/7/PermissionRequest", "tok", permReq)
	ev.mu.Lock()
	if len(ev.news) != 1 {
		t.Fatalf("dup should not re-announce: %d", len(ev.news))
	}
}

func TestRemoteFirstBlocksUntilDecide(t *testing.T) {
	ts, s, ev := newTestServer(t, proto.ApprovalRemoteFirst)
	done := make(chan []byte, 1)
	go func() {
		_, body := post(t, ts.URL+"/hook/claude/7/PermissionRequest", "tok", permReq)
		done <- body
	}()
	var key string
	deadline := time.Now().Add(2 * time.Second)
	for key == "" && time.Now().Before(deadline) {
		if p := s.Store.Pending(); len(p) == 1 {
			key = p[0].Key
		}
		time.Sleep(5 * time.Millisecond)
	}
	if key == "" {
		t.Fatal("approval not registered")
	}
	select {
	case <-done:
		t.Fatal("handler returned before decision")
	case <-time.After(50 * time.Millisecond):
	}
	if _, ok := s.Store.Decide(key, "allow", "web:test"); !ok {
		t.Fatal("decide failed")
	}
	select {
	case body := <-done:
		var d PermissionDecision
		if err := json.Unmarshal(body, &d); err != nil {
			t.Fatalf("decode: %v %s", err, body)
		}
		if d.HookSpecificOutput.HookEventName != "PermissionRequest" || d.HookSpecificOutput.Decision.Behavior != "allow" {
			t.Fatalf("unexpected decision: %s", body)
		}
	case <-time.After(time.Second):
		t.Fatal("handler did not return after decide")
	}
	ev.mu.Lock()
	defer ev.mu.Unlock()
	if len(ev.news) != 1 || ev.news[0].Level != proto.LevelHook || ev.news[0].HookTimeoutAt == nil {
		t.Fatalf("approval.new: %+v", ev.news)
	}
	if a, _ := s.Store.Get(key); a.Status != proto.ApprovalAllowed || a.DecidedBy != "web:test" {
		t.Fatalf("status: %+v", a)
	}
}

func TestRemoteFirstTimeoutFallback(t *testing.T) {
	ts, s, ev := newTestServer(t, proto.ApprovalRemoteFirst)
	start := time.Now()
	resp, body := post(t, ts.URL+"/hook/claude/7/PermissionRequest", "tok", permReq)
	if resp.StatusCode != 200 || string(bytes.TrimSpace(body)) != "{}" {
		t.Fatalf("fallback should return {}: %d %s", resp.StatusCode, body)
	}
	if el := time.Since(start); el < 250*time.Millisecond {
		t.Fatalf("returned too early: %v", el)
	}
	ev.mu.Lock()
	defer ev.mu.Unlock()
	if len(ev.closed) != 1 || ev.closed[0] != ev.news[0].Key+":timeout:"+proto.ApprovalFallback {
		t.Fatalf("closed: %v", ev.closed)
	}
	if len(s.Store.Pending()) != 0 {
		t.Fatal("should not be pending")
	}
}

func TestPostToolUseClosesLocal(t *testing.T) {
	ts, s, ev := newTestServer(t, proto.ApprovalNotify)
	post(t, ts.URL+"/hook/claude/7/PermissionRequest", "tok", permReq)
	// Same tool input with different key order / whitespace → same key.
	postReq := map[string]any{
		"session_id": "cs-1", "cwd": "/w", "hook_event_name": "PostToolUse",
		"tool_name": "Bash", "tool_input": map[string]any{"timeout": 5, "command": "go test ./..."},
		"tool_response": map[string]any{"stdout": "ok"},
	}
	post(t, ts.URL+"/hook/claude/7/PostToolUse", "tok", postReq)
	ev.mu.Lock()
	defer ev.mu.Unlock()
	if len(ev.closed) != 1 || ev.closed[0] != ev.news[0].Key+":local:"+proto.ApprovalClosedLocal {
		t.Fatalf("closed: %v", ev.closed)
	}
	if ev.states[len(ev.states)-1] != proto.StateRunning {
		t.Fatalf("state after PostToolUse: %v", ev.states)
	}
	if len(s.Store.Pending()) != 0 {
		t.Fatal("should not be pending")
	}
}

func TestOtherClaudeEvents(t *testing.T) {
	ts, _, ev := newTestServer(t, proto.ApprovalNotify)
	post(t, ts.URL+"/hook/claude/7/SessionStart", "tok", map[string]any{"session_id": "cs-9", "hook_event_name": "SessionStart"})
	post(t, ts.URL+"/hook/claude/7/Notification", "tok", map[string]any{"hook_event_name": "Notification", "notification_type": "agent_needs_input", "message": "Which branch?"})
	post(t, ts.URL+"/hook/claude/7/Notification", "tok", map[string]any{"hook_event_name": "Notification", "notification_type": "idle_prompt"})
	post(t, ts.URL+"/hook/claude/7/StopFailure", "tok", map[string]any{"hook_event_name": "StopFailure", "error_type": "rate_limit"})
	post(t, ts.URL+"/hook/claude/7/SessionEnd", "tok", map[string]any{"hook_event_name": "SessionEnd", "reason": "other"})
	ev.mu.Lock()
	defer ev.mu.Unlock()
	if ev.toolSess != "claude:cs-9" {
		t.Fatalf("tool session: %q", ev.toolSess)
	}
	want := []proto.SessionState{proto.StateRunning, proto.StateNeedsInput, proto.StateIdle, proto.StateFailed, proto.StateIdle}
	if len(ev.states) != len(want) {
		t.Fatalf("states: %v", ev.states)
	}
	for i := range want {
		if ev.states[i] != want[i] {
			t.Fatalf("states: %v want %v", ev.states, want)
		}
	}
	if ev.kinds[1] != proto.NeedQuestion || len(ev.news) != 1 || ev.news[0].Summary != "Which branch?" || ev.news[0].Level != proto.LevelKeys {
		t.Fatalf("question approval: %+v", ev.news)
	}
	if ev.exited != 1 {
		t.Fatalf("exited: %d", ev.exited)
	}
	// SessionEnd closes the pending question approval.
	if len(ev.closed) != 1 {
		t.Fatalf("closed: %v", ev.closed)
	}
}

func TestStatusLineAndCodexNotify(t *testing.T) {
	ts, _, ev := newTestServer(t, proto.ApprovalNotify)
	resp, _ := post(t, ts.URL+"/statusline/7", "tok", map[string]any{
		"cost":           map[string]any{"total_cost_usd": 0.42},
		"context_window": map[string]any{"used_percentage": 37.5},
	})
	if resp.StatusCode != 204 {
		t.Fatalf("statusline: %d", resp.StatusCode)
	}
	post(t, ts.URL+"/hook/codex/7/notify", "tok", map[string]any{"type": "agent-turn-complete", "thread-id": "th-1", "turn-id": "t1"})
	ev.mu.Lock()
	defer ev.mu.Unlock()
	if ev.cost == nil || *ev.cost != 0.42 || ev.ctx == nil || *ev.ctx != 37.5 {
		t.Fatalf("statusline parse: %v %v", ev.cost, ev.ctx)
	}
	if ev.toolSess != "codex:th-1" || ev.states[len(ev.states)-1] != proto.StateIdle {
		t.Fatalf("codex notify: %q %v", ev.toolSess, ev.states)
	}
}

func TestApprovalKeyCanonical(t *testing.T) {
	a := ApprovalKey("s", "Bash", json.RawMessage(`{"b":1,"a":{"y":true,"x":"q"}}`))
	b := ApprovalKey("s", "Bash", json.RawMessage(` { "a" : { "x":"q" , "y":true } , "b":1 } `))
	if a != b || len(a) != 16 {
		t.Fatalf("keys differ: %s %s", a, b)
	}
	if ApprovalKey("s2", "Bash", json.RawMessage(`{"b":1}`)) == ApprovalKey("s", "Bash", json.RawMessage(`{"b":1}`)) {
		t.Fatal("session id must affect key")
	}
	tests := []struct{ tool, input, want string }{
		{"Bash", `{"command":"ls -la"}`, "ls -la"},
		{"Write", `{"file_path":"/a/b.go","content":"x"}`, "/a/b.go"},
		{"Edit", `{"file_path":"/a/b.go"}`, "/a/b.go"},
		{"Read", `{"file_path":"/r"}`, "/r"},
		{"WebFetch", `{"url":"https://x"}`, "WebFetch: https://x"},
		{"Task", `{"description":"d"}`, "Task"},
		{"Bash", `not json`, "Bash"},
	}
	for _, tc := range tests {
		if got := Summary(tc.tool, json.RawMessage(tc.input)); got != tc.want {
			t.Errorf("Summary(%s,%s)=%q want %q", tc.tool, tc.input, got, tc.want)
		}
	}
}

func TestClaudeSettings(t *testing.T) {
	m := ClaudeSettings(ClaudeSettingsOptions{SID: 5, Port: 4321, Token: "tk", Mode: proto.ApprovalRemoteFirst})
	hooks := m["hooks"].(map[string]any)
	for _, ev := range ClaudeEvents {
		if _, ok := hooks[ev]; !ok {
			t.Fatalf("missing hook %s", ev)
		}
	}
	pr := hooks["PermissionRequest"].([]map[string]any)[0]["hooks"].([]map[string]any)[0]
	if pr["timeout"] != 3600 || pr["type"] != "http" || pr["url"] != "http://127.0.0.1:4321/hook/claude/5/PermissionRequest" {
		t.Fatalf("PermissionRequest hook: %+v", pr)
	}
	if pr["headers"].(map[string]string)["Authorization"] != "Bearer $TX_HOOK_TOKEN" {
		t.Fatalf("headers: %+v", pr["headers"])
	}
	m2 := ClaudeSettings(ClaudeSettingsOptions{SID: 5, Port: 4321, Token: "tk", Mode: proto.ApprovalNotify})
	pr2 := m2["hooks"].(map[string]any)["PermissionRequest"].([]map[string]any)[0]["hooks"].([]map[string]any)[0]
	if pr2["timeout"] != 10 {
		t.Fatalf("notify timeout: %v", pr2["timeout"])
	}
	sl := m["statusLine"].(map[string]any)
	if sl["type"] != "command" || !bytes.Contains([]byte(sl["command"].(string)), []byte("/statusline/5")) {
		t.Fatalf("statusLine: %+v", sl)
	}
	dir := t.TempDir()
	p, err := WriteClaudeSettings(dir, ClaudeSettingsOptions{SID: 5, Port: 1, Token: "t"})
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]any
	if b, err := readFile(p); err != nil || json.Unmarshal(b, &back) != nil || back["hooks"] == nil {
		t.Fatalf("settings file: %v", err)
	}
}
