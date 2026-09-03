package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/wfighter1/terminalX/internal/hooks"
	"github.com/wfighter1/terminalX/internal/proto"
)

// ---- fake relay ---------------------------------------------------------

type relayConn struct {
	c      *websocket.Conn
	msgs   chan proto.Msg
	frames chan proto.Frame
	done   chan struct{}
}

type fakeRelay struct {
	t     *testing.T
	srv   *httptest.Server
	token string
	auth  chan string
	conns chan *relayConn
}

func newFakeRelay(t *testing.T, token string) *fakeRelay {
	t.Helper()
	fr := &fakeRelay{t: t, token: token, auth: make(chan string, 8), conns: make(chan *relayConn, 8)}
	mux := http.NewServeMux()
	mux.HandleFunc("/ws/agent", func(w http.ResponseWriter, r *http.Request) {
		fr.auth <- r.Header.Get("Authorization")
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		c.SetReadLimit(4 << 20)
		rc := &relayConn{c: c, msgs: make(chan proto.Msg, 256), frames: make(chan proto.Frame, 4096), done: make(chan struct{})}
		fr.conns <- rc
		defer close(rc.done)
		for {
			typ, data, err := c.Read(r.Context())
			if err != nil {
				return
			}
			switch typ {
			case websocket.MessageText:
				m, err := proto.Decode(data)
				if err != nil {
					t.Errorf("relay: bad json from agent: %v", err)
					continue
				}
				rc.msgs <- m
			case websocket.MessageBinary:
				f, err := proto.Unmarshal(data)
				if err != nil {
					t.Errorf("relay: bad frame from agent: %v", err)
					continue
				}
				rc.frames <- f
			}
		}
	})
	fr.srv = httptest.NewServer(mux)
	t.Cleanup(fr.srv.Close)
	return fr
}

func (fr *fakeRelay) accept(t *testing.T, d time.Duration) *relayConn {
	t.Helper()
	select {
	case rc := <-fr.conns:
		return rc
	case <-time.After(d):
		t.Fatal("agent did not connect")
		return nil
	}
}

func (rc *relayConn) send(t *testing.T, m proto.Msg) {
	t.Helper()
	data, err := m.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if err := rc.c.Write(context.Background(), websocket.MessageText, data); err != nil {
		t.Fatalf("relay write: %v", err)
	}
}

func (rc *relayConn) sendRaw(t *testing.T, m proto.Msg) {
	rc.send(t, m)
}

func (rc *relayConn) sendFrame(t *testing.T, f proto.Frame) {
	t.Helper()
	data, err := f.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if err := rc.c.Write(context.Background(), websocket.MessageBinary, data); err != nil {
		t.Fatalf("relay write frame: %v", err)
	}
}

// expectMsg waits for the first message of type typ (others are dropped
// unless keep returns true for them, in which case the test fails).
func (rc *relayConn) expectMsg(t *testing.T, typ string, d time.Duration) proto.Msg {
	t.Helper()
	deadline := time.After(d)
	for {
		select {
		case m := <-rc.msgs:
			if m.T == typ {
				return m
			}
			if m.T == proto.TError {
				t.Fatalf("waiting for %s, got error: %s", typ, m.Error)
			}
		case <-deadline:
			t.Fatalf("timeout waiting for %s", typ)
		}
	}
}

// collectOutput gathers Output frames for sid until marker appears.
func (rc *relayConn) collectOutput(t *testing.T, sid uint32, marker string, d time.Duration) (data []byte, frames []proto.Frame) {
	t.Helper()
	deadline := time.After(d)
	for {
		select {
		case f := <-rc.frames:
			if f.SID != sid || f.Type != proto.FrameOutput {
				continue
			}
			frames = append(frames, f)
			data = append(data, f.Payload...)
			if bytes.Contains(data, []byte(marker)) {
				return data, frames
			}
		case <-deadline:
			t.Fatalf("marker %q not seen; got %q", marker, data)
		}
	}
}

// collectSnapshot gathers Snapshot frames up to the one without FlagMore.
func (rc *relayConn) collectSnapshot(t *testing.T, sid uint32, d time.Duration) []proto.Frame {
	t.Helper()
	deadline := time.After(d)
	var out []proto.Frame
	for {
		select {
		case f := <-rc.frames:
			if f.SID != sid || f.Type != proto.FrameSnapshot {
				continue
			}
			out = append(out, f)
			if f.Flags&proto.FlagMore == 0 {
				return out
			}
		case <-deadline:
			t.Fatal("timeout waiting for snapshot")
		}
	}
}

// ---- helpers ------------------------------------------------------------

func requireBash(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("bash-based test")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not installed")
	}
}

func startAgent(t *testing.T, fr *fakeRelay) (*Agent, *Config, string) {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "agent.json")
	cfg := &Config{RelayURL: fr.srv.URL, DeviceID: "dev_test", DeviceToken: fr.token, Name: "unit"}
	if err := cfg.Save(cfgPath); err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if testing.Verbose() {
		log = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug}))
	}
	a, err := New(cfg, cfgPath, log)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Run returned %v", err)
			}
		case <-time.After(10 * time.Second):
			t.Error("agent did not stop")
		}
	})
	return a, cfg, cfgPath
}

func waitFor(t *testing.T, d time.Duration, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for %s", what)
}

// ---- tests --------------------------------------------------------------

func TestAgentAgainstFakeRelay(t *testing.T) {
	requireBash(t)
	fr := newFakeRelay(t, "tok-123")
	a, cfg, cfgPath := startAgent(t, fr)
	rc := fr.accept(t, 10*time.Second)

	// auth header + hello
	if got := <-fr.auth; got != "Bearer tok-123" {
		t.Fatalf("Authorization = %q", got)
	}
	hello := rc.expectMsg(t, proto.TAgentHello, 5*time.Second)
	if hello.AgentID == "" || hello.OS != runtime.GOOS || hello.Version == "" || hello.ReqID == "" {
		t.Fatalf("bad hello: %+v", hello)
	}
	if !strings.Contains(strings.Join(hello.Caps, ","), "pty") || !strings.Contains(strings.Join(hello.Caps, ","), "hooks.http") {
		t.Fatalf("caps = %v", hello.Caps)
	}
	rc.send(t, proto.Msg{T: proto.TAck, ReqID: hello.ReqID})

	// hooks port persisted
	waitFor(t, 5*time.Second, func() bool { return a.HooksPort() != 0 }, "hooks port")
	saved, err := Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if saved.HooksPort != a.HooksPort() || saved.HookToken == "" || saved.AgentID != hello.AgentID {
		t.Fatalf("persisted config %+v (port %d)", saved, a.HooksPort())
	}

	// heartbeat echo → RTT
	hb := rc.expectMsg(t, proto.THeartbeat, 5*time.Second)
	if hb.Heartbeat == nil || hb.Heartbeat.Seq == 0 || hb.Heartbeat.SentAt.IsZero() {
		t.Fatalf("bad heartbeat: %+v", hb)
	}
	time.Sleep(5 * time.Millisecond)
	rc.sendRaw(t, hb)
	waitFor(t, 5*time.Second, func() bool { return a.RTT() > 0 }, "rtt")

	// session.open → session.opened with req_id
	cwd := t.TempDir()
	rc.send(t, proto.Msg{T: proto.TSessionOpen, ReqID: "r1", ClientID: "c1", DeviceID: "dev_test",
		Open: &proto.OpenRequest{Tool: proto.ToolShell, Shell: "bash", Cwd: cwd, Cols: 80, Rows: 24, Name: "unit-shell"}})
	opened := rc.expectMsg(t, proto.TSessionOpened, 10*time.Second)
	if opened.ReqID != "r1" || opened.ClientID != "c1" || opened.Session == nil || opened.SID == 0 || opened.Session.SID != opened.SID {
		t.Fatalf("bad session.opened: %+v", opened)
	}
	si := opened.Session
	if si.Tool != proto.ToolShell || si.Shell != "bash" || si.Cwd != cwd || si.Name != "unit-shell" || !si.PTYAlive || si.Cols != 80 {
		t.Fatalf("bad session info: %+v", si)
	}
	sid := opened.SID

	// input → output frames
	rc.sendFrame(t, proto.Frame{Type: proto.FrameInput, SID: sid, Payload: []byte("echo tx-$((7+7))\n")})
	out, frames := rc.collectOutput(t, sid, "tx-14", 10*time.Second)
	var expect uint64
	for _, f := range frames {
		if f.Seq != expect {
			t.Fatalf("output frame seq %d, want %d", f.Seq, expect)
		}
		expect += uint64(len(f.Payload))
	}
	total := expect
	if total != uint64(len(out)) {
		t.Fatalf("seq %d != bytes %d", total, len(out))
	}

	// attach: delta path (last_seq within the window, no reset)
	behind := total - 5
	rc.send(t, proto.Msg{T: proto.TSessionAttach, ClientID: "c1", SID: sid, LastSeq: behind})
	snap := rc.collectSnapshot(t, sid, 5*time.Second)
	if snap[0].Flags&proto.FlagReset != 0 {
		t.Fatalf("delta replay must not carry FlagReset: %+v", snap[0].Flags)
	}
	var delta []byte
	for _, f := range snap {
		delta = append(delta, f.Payload...)
	}
	if len(delta) < 5 || !bytes.Equal(delta[:5], out[behind:total]) {
		t.Fatalf("delta %q does not start with %q", delta, out[behind:total])
	}
	if last := snap[len(snap)-1]; last.Seq < total {
		t.Fatalf("delta end seq %d < %d", last.Seq, total)
	}

	// attach: snapshot path (last_seq in the future → out of window → reset)
	rc.send(t, proto.Msg{T: proto.TSessionAttach, ClientID: "c2", SID: sid, LastSeq: total + 1_000_000})
	snap = rc.collectSnapshot(t, sid, 5*time.Second)
	if snap[0].Flags&proto.FlagReset == 0 {
		t.Fatal("snapshot must carry FlagReset")
	}
	var replay []byte
	for _, f := range snap {
		replay = append(replay, f.Payload...)
	}
	if !bytes.Contains(replay, []byte("tx-14")) {
		t.Fatalf("snapshot missing marker: %q", replay)
	}
	if snap[len(snap)-1].Seq < total {
		t.Fatalf("snapshot seq %d < %d", snap[len(snap)-1].Seq, total)
	}
	if a.session(sid).Attached() != 2 {
		t.Fatalf("attached = %d", a.session(sid).Attached())
	}
	rc.send(t, proto.Msg{T: proto.TSessionDetach, ClientID: "c2", SID: sid})
	waitFor(t, 2*time.Second, func() bool { return a.session(sid).Attached() == 1 }, "detach")

	// resize (message + frame) and signal → ack
	rc.send(t, proto.Msg{T: proto.TSessionResize, ReqID: "r2", ClientID: "c1", SID: sid, Cols: 100, Rows: 30})
	if ack := rc.expectMsg(t, proto.TAck, 5*time.Second); ack.ReqID != "r2" || ack.SID != sid {
		t.Fatalf("bad resize ack %+v", ack)
	}
	rc.sendFrame(t, proto.Frame{Type: proto.FrameResize, SID: sid, Payload: proto.ResizePayload(90, 28)})
	waitFor(t, 2*time.Second, func() bool { c, r := a.session(sid).Size(); return c == 90 && r == 28 }, "resize frame")
	rc.send(t, proto.Msg{T: proto.TSessionSignal, ReqID: "r3", ClientID: "c1", SID: sid, Sig: proto.SigCtrlC})
	if ack := rc.expectMsg(t, proto.TAck, 5*time.Second); ack.ReqID != "r3" {
		t.Fatalf("bad signal ack %+v", ack)
	}
	rc.sendFrame(t, proto.Frame{Type: proto.FrameAck, SID: sid, Seq: total})
	waitFor(t, 2*time.Second, func() bool { a.mu.Lock(); defer a.mu.Unlock(); return a.acks[sid] == total }, "ack bookkeeping")

	// set_mode → session.updated
	rc.send(t, proto.Msg{T: proto.TSessionSetMode, ReqID: "r4", ClientID: "c1", SID: sid, Mode: proto.ApprovalRemoteFirst})
	upd := rc.expectMsg(t, proto.TSessionUpdated, 5*time.Second)
	if upd.Session == nil || upd.Session.ApprovalMode != proto.ApprovalRemoteFirst {
		t.Fatalf("bad session.updated %+v", upd)
	}

	// hooks: remote_first PermissionRequest blocks until approval.decide
	hookURL := "http://127.0.0.1:" + strconv.Itoa(a.HooksPort()) + "/hook/claude/" + strconv.FormatUint(uint64(sid), 10) + "/PermissionRequest"
	body := `{"session_id":"cs1","hook_event_name":"PermissionRequest","tool_name":"Bash","tool_input":{"command":"rm -rf build"},"cwd":"/w"}`
	type hookResp struct {
		status int
		body   []byte
		err    error
	}
	hr := make(chan hookResp, 1)
	go func() {
		req, _ := http.NewRequest(http.MethodPost, hookURL, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+cfg.HookToken)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			hr <- hookResp{err: err}
			return
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		hr <- hookResp{status: resp.StatusCode, body: b}
	}()
	st := rc.expectMsg(t, proto.TSessionState, 5*time.Second)
	if st.SID != sid || st.State != proto.StateNeedsInput || st.Kind != proto.NeedPermission || st.Source != proto.SourceHook {
		t.Fatalf("bad session.state %+v", st)
	}
	an := rc.expectMsg(t, proto.TApprovalNew, 5*time.Second)
	if an.Approval == nil || an.Approval.Level != proto.LevelHook || an.Approval.Summary != "rm -rf build" || an.Approval.DeviceID != "dev_test" {
		t.Fatalf("bad approval.new %+v", an.Approval)
	}
	rc.send(t, proto.Msg{T: proto.TApprovalDecide, SID: sid, Key: an.Approval.Key, Decision: "allow", By: "web:c1"})
	select {
	case r := <-hr:
		if r.err != nil || r.status != 200 {
			t.Fatalf("hook response %d %v %s", r.status, r.err, r.body)
		}
		var d hooks.PermissionDecision
		if err := json.Unmarshal(r.body, &d); err != nil || d.HookSpecificOutput.Decision.Behavior != "allow" {
			t.Fatalf("hook decision %s (%v)", r.body, err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("hook did not return after approval.decide")
	}
	// deciding again is an error routed to the client
	rc.send(t, proto.Msg{T: proto.TApprovalDecide, ReqID: "r5", ClientID: "c1", SID: sid, Key: an.Approval.Key, Decision: "deny"})
	if e := rc.expectMsg(t, proto.TError, 5*time.Second); e.ReqID != "r5" || e.ClientID != "c1" {
		t.Fatalf("bad error reply %+v", e)
	}

	// statusLine → session.updated with metrics
	slURL := "http://127.0.0.1:" + strconv.Itoa(a.HooksPort()) + "/statusline/" + strconv.FormatUint(uint64(sid), 10)
	req, _ := http.NewRequest(http.MethodPost, slURL, strings.NewReader(`{"cost":{"total_cost_usd":0.42},"context_window":{"used_percentage":17}}`))
	req.Header.Set("Authorization", "Bearer "+cfg.HookToken)
	if resp, err := http.DefaultClient.Do(req); err != nil || resp.StatusCode != http.StatusNoContent {
		t.Fatalf("statusline post: %v %v", err, resp)
	} else {
		resp.Body.Close()
	}
	upd = rc.expectMsg(t, proto.TSessionUpdated, 5*time.Second)
	if upd.Session == nil || upd.Session.CostUSD == nil || *upd.Session.CostUSD != 0.42 || upd.Session.ContextPct == nil || *upd.Session.ContextPct != 17 {
		t.Fatalf("bad metrics %+v", upd.Session)
	}

	// second session, then a natural exit → EOF + session.exited
	rc.send(t, proto.Msg{T: proto.TSessionOpen, ReqID: "r6", ClientID: "c1",
		Open: &proto.OpenRequest{Tool: proto.ToolShell, Shell: "bash", Cwd: cwd}})
	opened2 := rc.expectMsg(t, proto.TSessionOpened, 10*time.Second)
	sid2 := opened2.SID
	rc.sendFrame(t, proto.Frame{Type: proto.FrameInput, SID: sid2, Payload: []byte("exit 5\n")})
	ex := rc.expectMsg(t, proto.TSessionExited, 10*time.Second)
	if ex.SID != sid2 || ex.Code == nil || *ex.Code != 5 || ex.Resumable == nil || ex.Resumable.Tool != proto.ToolShell {
		t.Fatalf("bad session.exited %+v", ex)
	}
	waitFor(t, 5*time.Second, func() bool {
		for {
			select {
			case f := <-rc.frames:
				if f.SID == sid2 && f.Type == proto.FrameEOF {
					if code, _ := proto.ParseEOF(f.Payload); code != 5 {
						t.Fatalf("EOF code %d", code)
					}
					return true
				}
			default:
				return false
			}
		}
	}, "EOF frame")
	if len(a.sessionList()) != 2 {
		t.Fatalf("exited session must stay listed: %d", len(a.sessionList()))
	}

	// reconnect: drop the relay side → agent reconnects and re-hellos with both sessions
	_ = rc.c.Close(websocket.StatusGoingAway, "bye")
	rc2 := fr.accept(t, 15*time.Second)
	hello2 := rc2.expectMsg(t, proto.TAgentHello, 5*time.Second)
	if len(hello2.Sessions) != 2 {
		t.Fatalf("hello after reconnect lists %d sessions, want 2", len(hello2.Sessions))
	}
	rc2.send(t, proto.Msg{T: proto.TAck, ReqID: hello2.ReqID})

	// close → session.closed and forgotten
	rc2.send(t, proto.Msg{T: proto.TSessionClose, ReqID: "r7", ClientID: "c1", SID: sid})
	if cl := rc2.expectMsg(t, proto.TSessionClosed, 10*time.Second); cl.SID != sid || cl.ReqID != "r7" {
		t.Fatalf("bad session.closed %+v", cl)
	}
	if a.session(sid) != nil {
		t.Fatal("closed session still present")
	}
	rc2.send(t, proto.Msg{T: proto.TSessionClose, ReqID: "r8", ClientID: "c1", SID: sid})
	if e := rc2.expectMsg(t, proto.TError, 5*time.Second); e.ReqID != "r8" {
		t.Fatalf("closing twice should error: %+v", e)
	}
}

func TestAgentKillResumeViaRelay(t *testing.T) {
	requireBash(t)
	fr := newFakeRelay(t, "tok-kr")
	a, _, _ := startAgent(t, fr)
	rc := fr.accept(t, 10*time.Second)
	<-fr.auth
	hello := rc.expectMsg(t, proto.TAgentHello, 5*time.Second)
	rc.send(t, proto.Msg{T: proto.TAck, ReqID: hello.ReqID})
	rc.send(t, proto.Msg{T: proto.TSessionOpen, ReqID: "o", ClientID: "c1",
		Open: &proto.OpenRequest{Tool: proto.ToolShell, Shell: "bash", Cwd: t.TempDir()}})
	opened := rc.expectMsg(t, proto.TSessionOpened, 10*time.Second)
	sid := opened.SID
	rc.sendFrame(t, proto.Frame{Type: proto.FrameInput, SID: sid, Payload: []byte("echo tx-$((8+8))\n")})
	rc.collectOutput(t, sid, "tx-16", 10*time.Second)
	rc.send(t, proto.Msg{T: proto.TSessionSignal, ReqID: "k", ClientID: "c1", SID: sid, Sig: proto.SigKillResume})
	upd := rc.expectMsg(t, proto.TSessionUpdated, 15*time.Second)
	if upd.Session == nil || !upd.Session.PTYAlive || upd.Session.State != proto.StateRunning {
		t.Fatalf("after kill_resume: %+v", upd.Session)
	}
	rc.expectMsg(t, proto.TAck, 5*time.Second)
	rc.sendFrame(t, proto.Frame{Type: proto.FrameInput, SID: sid, Payload: []byte("echo tx-$((9+9))\n")})
	out, _ := rc.collectOutput(t, sid, "tx-18", 10*time.Second)
	if !bytes.Contains(out, []byte("kill & resume")) {
		t.Fatalf("marker line missing from output: %q", out)
	}
	if a.session(sid) == nil || !a.session(sid).Alive() {
		t.Fatal("session gone after kill_resume")
	}
}

func TestAgentRejectsBadToken(t *testing.T) {
	fr := newFakeRelay(t, "good")
	// relay that answers 401
	mux := http.NewServeMux()
	mux.HandleFunc("/ws/agent", func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":"invalid device token"}`, http.StatusUnauthorized)
	})
	fr.srv.Config.Handler = mux
	cfg := &Config{RelayURL: fr.srv.URL, DeviceID: "d", DeviceToken: "bad"}
	a, err := New(cfg, filepath.Join(t.TempDir(), "agent.json"), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	ws, _ := cfg.WSURL()
	err = a.runConn(context.Background(), ws)
	if err == nil || !strings.Contains(err.Error(), "rejected the device token") {
		t.Fatalf("err = %v", err)
	}
}

func TestBackoffDelay(t *testing.T) {
	tests := []struct {
		n        int
		min, max time.Duration
	}{
		{0, 1500 * time.Millisecond, 2500 * time.Millisecond},
		{1, 3 * time.Second, 5 * time.Second},
		{2, 6 * time.Second, 10 * time.Second},
		{3, 12 * time.Second, 20 * time.Second},
		{9, 12 * time.Second, 20 * time.Second},
	}
	for _, tc := range tests {
		for i := 0; i < 50; i++ {
			d := backoffDelay(tc.n)
			if d < tc.min || d > tc.max {
				t.Fatalf("attempt %d: %v outside [%v,%v]", tc.n, d, tc.min, tc.max)
			}
		}
	}
}

func TestConfigURLs(t *testing.T) {
	tests := []struct {
		in, ws, httpURL string
		wantErr         bool
	}{
		{"http://127.0.0.1:8080", "ws://127.0.0.1:8080/ws/agent", "http://127.0.0.1:8080", false},
		{"https://tx.example.com/", "wss://tx.example.com/ws/agent", "https://tx.example.com", false},
		{"https://tx.example.com/base/", "wss://tx.example.com/base/ws/agent", "https://tx.example.com/base", false},
		{"wss://tx.example.com", "wss://tx.example.com/ws/agent", "https://tx.example.com", false},
		{"ws://h:1", "ws://h:1/ws/agent", "http://h:1", false},
		{"ftp://x", "", "", true},
		{"", "", "", true},
	}
	for _, tc := range tests {
		c := &Config{RelayURL: tc.in}
		ws, err := c.WSURL()
		if (err != nil) != tc.wantErr {
			t.Errorf("WSURL(%q) err=%v", tc.in, err)
			continue
		}
		if ws != tc.ws {
			t.Errorf("WSURL(%q)=%q want %q", tc.in, ws, tc.ws)
		}
		if h, err := c.HTTPURL(); !tc.wantErr && (err != nil || h != tc.httpURL) {
			t.Errorf("HTTPURL(%q)=%q,%v want %q", tc.in, h, err, tc.httpURL)
		}
	}
}

func TestConfigSaveLoadAndIdentity(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "agent.json")
	c := &Config{RelayURL: "http://r", Presets: map[string]map[string]string{"mine": {"A": "1"}},
		Secrets: Secrets{MiniMaxAPIKey: "k"}}
	changed, err := c.EnsureIdentity()
	if err != nil || !changed || c.AgentID == "" || c.HookToken == "" || c.PrivKey == "" || c.PubKey == "" {
		t.Fatalf("EnsureIdentity: %v %v %+v", changed, err, c)
	}
	if changed, _ := c.EnsureIdentity(); changed {
		t.Fatal("second EnsureIdentity must be a no-op")
	}
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" {
		st, _ := os.Stat(path)
		if st.Mode().Perm() != 0o600 {
			t.Fatalf("config perm %o", st.Mode().Perm())
		}
		dst, _ := os.Stat(filepath.Dir(path))
		if dst.Mode().Perm() != 0o700 {
			t.Fatalf("dir perm %o", dst.Mode().Perm())
		}
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.AgentID != c.AgentID || got.PubKey != c.PubKey || got.Presets["mine"]["A"] != "1" || got.Secrets.MiniMaxAPIKey != "k" {
		t.Fatalf("round trip mismatch: %+v", got)
	}
	if env, err := got.Resolver().Resolve("minimax"); err != nil || len(env) != 2 {
		t.Fatalf("resolver: %v %v", env, err)
	}
	if env, err := got.Resolver().Resolve("mine"); err != nil || env[0] != "A=1" {
		t.Fatalf("custom preset: %v %v", env, err)
	}
	if _, err := Load(filepath.Join(dir, "missing.json")); !os.IsNotExist(errUnwrap(err)) {
		t.Fatalf("missing config err = %v", err)
	}
}

func errUnwrap(err error) error {
	for {
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return err
		}
		err = u.Unwrap()
	}
}

func TestPair(t *testing.T) {
	var got map[string]string
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/pair/redeem", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		if got["code"] == "BAD" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid or expired pairing code"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{
			"device_id": "dev_1", "device_token": "tok_1", "fingerprint": LocalFingerprint(got["pubkey"]), "name": got["name"],
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cfg := &Config{}
	res, err := Pair(context.Background(), cfg, srv.URL+"/", "A7K3-9QZP", "my-pc")
	if err != nil {
		t.Fatal(err)
	}
	if got["code"] != "A7K3-9QZP" || got["name"] != "my-pc" || got["os"] != runtime.GOOS || got["pubkey"] != cfg.PubKey || got["agent_version"] != Version {
		t.Fatalf("request body %+v", got)
	}
	if cfg.DeviceID != "dev_1" || cfg.DeviceToken != "tok_1" || cfg.RelayURL != srv.URL || cfg.Name != "my-pc" || cfg.Fingerprint == "" {
		t.Fatalf("config after pair %+v", cfg)
	}
	if res.Fingerprint != res.LocalFP || len(res.LocalFP) != 9 || res.LocalFP[4] != '-' {
		t.Fatalf("fingerprints %+v", res)
	}
	if _, err := Pair(context.Background(), &Config{}, srv.URL, "BAD", ""); err == nil || !strings.Contains(err.Error(), "invalid or expired") {
		t.Fatalf("bad code err = %v", err)
	}
}

func TestNotify(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var gotPath, gotAuth, gotBody string
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		if gotAuth != "Bearer t" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte("{}"))
	})}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	payload := `{"type":"agent-turn-complete","thread-id":"th1"}`
	if err := Notify(context.Background(), port, 42, "t", payload); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/hook/codex/42/notify" || gotBody != payload {
		t.Fatalf("path %q body %q", gotPath, gotBody)
	}
	if err := Notify(context.Background(), port, 42, "wrong", payload); err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("wrong token err = %v", err)
	}
	if err := Notify(context.Background(), port, 42, "t", "not json"); err == nil {
		t.Fatal("non-JSON payload must be rejected")
	}
	if err := Notify(context.Background(), 0, 42, "t", payload); err == nil {
		t.Fatal("missing port must be rejected")
	}
}
