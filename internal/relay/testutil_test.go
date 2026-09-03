package relay

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/wfighter1/terminalX/internal/proto"
	"github.com/wfighter1/terminalX/internal/relay/store"
)

const waitTimeout = 5 * time.Second

// fakeClock is an injectable clock for Config.Now.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 9, 3, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// testEnv is one relay under httptest.
type testEnv struct {
	t     *testing.T
	srv   *Server
	st    *store.Store
	ts    *httptest.Server
	clock *fakeClock
	pw    string
}

func newTestEnv(t *testing.T, mutate ...func(*Config)) *testEnv {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "relay.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	clock := newFakeClock()
	cfg := Config{
		AdminPassword: "secret-pw",
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
		Now:           clock.Now,
	}
	for _, m := range mutate {
		m(&cfg)
	}
	srv, err := New(cfg, st)
	if err != nil {
		t.Fatalf("relay.New: %v", err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(func() {
		srv.Close()
		ts.Close()
		_ = st.Close()
	})
	return &testEnv{t: t, srv: srv, st: st, ts: ts, clock: clock, pw: cfg.AdminPassword}
}

// apiResp is a decoded JSON API response.
type apiResp struct {
	status  int
	body    map[string]any
	cookies []*http.Cookie
}

func (r apiResp) str(k string) string {
	v, _ := r.body[k].(string)
	return v
}

// do performs an API call; cookie may be "" for anonymous.
func (e *testEnv) do(method, path string, body any, cookie string) apiResp {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			e.t.Fatalf("marshal body: %v", err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, e.ts.URL+path, rd)
	if err != nil {
		e.t.Fatalf("new request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if cookie != "" {
		req.Header.Set("Cookie", cookie)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	out := apiResp{status: resp.StatusCode, cookies: resp.Cookies()}
	data, _ := io.ReadAll(resp.Body)
	if len(data) > 0 {
		if err := json.Unmarshal(data, &out.body); err != nil {
			e.t.Fatalf("%s %s: non-JSON body %q", method, path, data)
		}
	}
	return out
}

// login returns a Cookie header value for an authenticated console session.
func (e *testEnv) login() string {
	e.t.Helper()
	r := e.do(http.MethodPost, "/api/login", map[string]string{"password": e.pw}, "")
	if r.status != http.StatusOK {
		e.t.Fatalf("login: status %d body %v", r.status, r.body)
	}
	for _, c := range r.cookies {
		if c.Name == sessionCookie && c.Value != "" {
			return sessionCookie + "=" + c.Value
		}
	}
	e.t.Fatalf("login: no %s cookie in %v", sessionCookie, r.cookies)
	return ""
}

// pairNew creates a pairing code.
func (e *testEnv) pairNew(cookie string) string {
	e.t.Helper()
	r := e.do(http.MethodPost, "/api/pair/new", nil, cookie)
	if r.status != http.StatusOK || r.str("code") == "" {
		e.t.Fatalf("pair/new: status %d body %v", r.status, r.body)
	}
	return r.str("code")
}

// pairedDevice is the result of a successful redeem.
type pairedDevice struct {
	id, token, name, fingerprint string
}

// pairDevice runs the full pairing flow and returns the device credentials.
func (e *testEnv) pairDevice(cookie, name string) pairedDevice {
	e.t.Helper()
	code := e.pairNew(cookie)
	r := e.do(http.MethodPost, "/api/pair/redeem", map[string]string{"code": code, "name": name, "os": "windows", "agent_version": "0.1-test"}, "")
	if r.status != http.StatusOK {
		e.t.Fatalf("pair/redeem: status %d body %v", r.status, r.body)
	}
	return pairedDevice{id: r.str("device_id"), token: r.str("device_token"), name: r.str("name"), fingerprint: r.str("fingerprint")}
}

// wsPeer is a fake agent or client on a WebSocket.
type wsPeer struct {
	t      *testing.T
	conn   *websocket.Conn
	msgs   chan proto.Msg
	raws   chan []byte
	frames chan proto.Frame
	done   chan error
}

// dialWS opens a WebSocket to path with the given headers. It fails the test
// unless the handshake succeeds.
func (e *testEnv) dialWS(path string, hdr http.Header) *wsPeer {
	e.t.Helper()
	conn, resp, err := e.tryDialWS(path, hdr)
	if err != nil {
		code := 0
		if resp != nil {
			code = resp.StatusCode
		}
		e.t.Fatalf("dial %s: %v (status %d)", path, err, code)
	}
	return e.newPeer(conn)
}

// tryDialWS is dialWS without the failure assertion.
func (e *testEnv) tryDialWS(path string, hdr http.Header) (*websocket.Conn, *http.Response, error) {
	ctx, cancel := context.WithTimeout(context.Background(), waitTimeout)
	defer cancel()
	return websocket.Dial(ctx, e.ts.URL+path, &websocket.DialOptions{HTTPHeader: hdr})
}

func (e *testEnv) newPeer(conn *websocket.Conn) *wsPeer {
	conn.SetReadLimit(wsReadLimit)
	p := &wsPeer{t: e.t, conn: conn,
		msgs: make(chan proto.Msg, 256), raws: make(chan []byte, 256),
		frames: make(chan proto.Frame, 256), done: make(chan error, 1)}
	go p.readLoop()
	e.t.Cleanup(func() { _ = conn.CloseNow() })
	return p
}

func (p *wsPeer) readLoop() {
	for {
		typ, data, err := p.conn.Read(context.Background())
		if err != nil {
			p.done <- err
			return
		}
		switch typ {
		case websocket.MessageText:
			m, err := proto.Decode(data)
			if err != nil {
				p.done <- err
				return
			}
			p.raws <- data
			p.msgs <- m
		case websocket.MessageBinary:
			f, err := proto.Unmarshal(data)
			if err != nil {
				p.done <- err
				return
			}
			p.frames <- f
		}
	}
}

func (p *wsPeer) sendMsg(m proto.Msg) {
	p.t.Helper()
	data, err := m.Encode()
	if err != nil {
		p.t.Fatalf("encode %s: %v", m.T, err)
	}
	p.sendRaw(websocket.MessageText, data)
}

func (p *wsPeer) sendFrame(f proto.Frame) {
	p.t.Helper()
	data, err := f.Marshal()
	if err != nil {
		p.t.Fatalf("marshal frame: %v", err)
	}
	p.sendRaw(websocket.MessageBinary, data)
}

func (p *wsPeer) sendRaw(typ websocket.MessageType, data []byte) {
	p.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), waitTimeout)
	defer cancel()
	if err := p.conn.Write(ctx, typ, data); err != nil {
		p.t.Fatalf("write: %v", err)
	}
}

// waitMsg returns the first control message satisfying pred, discarding the
// ones before it. It fails the test on timeout or connection close.
func (p *wsPeer) waitMsg(what string, pred func(proto.Msg) bool) proto.Msg {
	p.t.Helper()
	timer := time.NewTimer(waitTimeout)
	defer timer.Stop()
	for {
		select {
		case m := <-p.msgs:
			<-p.raws
			if pred(m) {
				return m
			}
		case err := <-p.done:
			p.done <- err
			p.t.Fatalf("waiting for %s: connection closed: %v", what, err)
		case <-timer.C:
			p.t.Fatalf("waiting for %s: timeout", what)
		}
	}
}

// waitType waits for the next message of type t.
func (p *wsPeer) waitType(t string) proto.Msg {
	p.t.Helper()
	return p.waitMsg(t, func(m proto.Msg) bool { return m.T == t })
}

// nextRaw returns the next control message as raw bytes (plus decoded).
func (p *wsPeer) nextRaw() ([]byte, proto.Msg) {
	p.t.Helper()
	select {
	case m := <-p.msgs:
		raw := <-p.raws
		return raw, m
	case err := <-p.done:
		p.done <- err
		p.t.Fatalf("waiting for raw message: connection closed: %v", err)
	case <-time.After(waitTimeout):
		p.t.Fatalf("waiting for raw message: timeout")
	}
	return nil, proto.Msg{}
}

// nextMsg returns the next control message, whatever it is.
func (p *wsPeer) nextMsg() proto.Msg {
	p.t.Helper()
	_, m := p.nextRaw()
	return m
}

// nextFrame returns the next binary frame.
func (p *wsPeer) nextFrame() proto.Frame {
	p.t.Helper()
	select {
	case f := <-p.frames:
		return f
	case err := <-p.done:
		p.done <- err
		p.t.Fatalf("waiting for frame: connection closed: %v", err)
	case <-time.After(waitTimeout):
		p.t.Fatalf("waiting for frame: timeout")
	}
	return proto.Frame{}
}

// waitClosed blocks until the server closes the connection and returns the
// close status (-1 when the error is not a CloseError).
func (p *wsPeer) waitClosed() websocket.StatusCode {
	p.t.Helper()
	select {
	case err := <-p.done:
		return websocket.CloseStatus(err)
	case <-time.After(waitTimeout):
		p.t.Fatalf("waiting for close: timeout")
	}
	return -1
}

// ---- higher-level actors ------------------------------------------------

// agentPeer is a fake tx-agent.
type agentPeer struct {
	*wsPeer
	dev pairedDevice
}

// connectAgent dials /ws/agent with the device token.
func (e *testEnv) connectAgent(dev pairedDevice) *agentPeer {
	e.t.Helper()
	hdr := http.Header{"Authorization": {"Bearer " + dev.token}}
	return &agentPeer{wsPeer: e.dialWS("/ws/agent", hdr), dev: dev}
}

// hello sends agent.hello with sessions and waits for the ack.
func (a *agentPeer) hello(sessions ...proto.SessionInfo) {
	a.t.Helper()
	a.sendMsg(proto.Msg{T: proto.TAgentHello, ReqID: "hello-1", AgentID: a.dev.id, Version: "0.1-test", OS: "windows",
		Caps: []string{"conpty"}, Sessions: sessions})
	ack := a.waitType(proto.TAck)
	if ack.ReqID != "hello-1" || ack.DeviceID != a.dev.id {
		a.t.Fatalf("hello ack: %+v", ack)
	}
}

// clientPeer is a fake console.
type clientPeer struct {
	*wsPeer
	id string // client_id assigned by the relay
}

// connectClient dials /ws/client with the login cookie and consumes the
// initial device.list / session.list / approval.list burst, returning them.
func (e *testEnv) connectClient(cookie string) (*clientPeer, proto.Msg, []proto.Msg, proto.Msg) {
	e.t.Helper()
	p := e.dialWS("/ws/client", http.Header{"Cookie": {cookie}})
	c := &clientPeer{wsPeer: p}
	devList := c.waitType(proto.TDeviceList)
	c.id = devList.ClientID
	if c.id == "" {
		e.t.Fatalf("device.list without client_id: %+v", devList)
	}
	var lists []proto.Msg
	var approvals proto.Msg
	for {
		m := c.waitMsg("initial burst", func(m proto.Msg) bool { return m.T == proto.TSessionList || m.T == proto.TApprovalList })
		if m.T == proto.TApprovalList {
			approvals = m
			break
		}
		lists = append(lists, m)
	}
	return c, devList, lists, approvals
}

// attach sends session.attach and waits until the agent has seen it.
func (c *clientPeer) attach(a *agentPeer, sid uint32, reqID string) proto.Msg {
	c.t.Helper()
	c.sendMsg(proto.Msg{T: proto.TSessionAttach, ReqID: reqID, DeviceID: a.dev.id, SID: sid, LastSeq: 0})
	got := a.waitMsg("session.attach", func(m proto.Msg) bool { return m.T == proto.TSessionAttach && m.ReqID == reqID })
	if got.ClientID != c.id || got.SID != sid {
		c.t.Fatalf("attach seen by agent: %+v want client %s sid %d", got, c.id, sid)
	}
	return got
}

func sessionInfo(sid uint32, name string) proto.SessionInfo {
	return proto.SessionInfo{SID: sid, Name: name, Tool: proto.ToolShell, Shell: "pwsh", Cwd: `D:\work`,
		ApprovalMode: proto.ApprovalNotify, State: proto.StateRunning, Source: proto.SourceNone,
		Confidence: proto.ConfidenceLow, Cols: 120, Rows: 40, PTYAlive: true}
}
