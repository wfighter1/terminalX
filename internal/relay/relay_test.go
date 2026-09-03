package relay

import (
	"bytes"
	"context"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/wfighter1/terminalX/internal/proto"
)

func isOnline(m proto.Msg) bool {
	return m.T == proto.TDeviceState && m.Device != nil && m.Device.Online
}
func isOffline(m proto.Msg) bool {
	return m.T == proto.TDeviceState && m.Device != nil && !m.Device.Online
}

// ---- auth ---------------------------------------------------------------

func TestLoginAndCookie(t *testing.T) {
	e := newTestEnv(t)

	cases := []struct {
		name     string
		password string
		want     int
	}{
		{"wrong password", "nope", http.StatusUnauthorized},
		{"empty password", "", http.StatusUnauthorized},
		{"right password", e.pw, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := e.do(http.MethodPost, "/api/login", map[string]string{"password": tc.password}, "")
			if r.status != tc.want {
				t.Fatalf("status %d want %d (%v)", r.status, tc.want, r.body)
			}
			if tc.want == http.StatusOK && r.str("client_id") == "" {
				t.Fatalf("no client_id in %v", r.body)
			}
		})
	}

	// anonymous access to a protected route
	if r := e.do(http.MethodGet, "/api/devices", nil, ""); r.status != http.StatusUnauthorized {
		t.Fatalf("anonymous /api/devices: %d", r.status)
	}
	if r := e.do(http.MethodGet, "/api/me", nil, ""); r.body["authenticated"] != false {
		t.Fatalf("anonymous /api/me: %v", r.body)
	}

	cookie := e.login()
	if r := e.do(http.MethodGet, "/api/me", nil, cookie); r.body["authenticated"] != true {
		t.Fatalf("/api/me with cookie: %v", r.body)
	}
	if r := e.do(http.MethodGet, "/api/devices", nil, cookie); r.status != http.StatusOK {
		t.Fatalf("/api/devices with cookie: %d", r.status)
	}
	// /ws/client requires the cookie too
	if _, resp, err := e.tryDialWS("/ws/client", nil); err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous /ws/client accepted: err=%v", err)
	}

	// logout revokes the session
	if r := e.do(http.MethodPost, "/api/logout", nil, cookie); r.status != http.StatusOK {
		t.Fatalf("logout: %d", r.status)
	}
	if r := e.do(http.MethodGet, "/api/me", nil, cookie); r.body["authenticated"] != false {
		t.Fatalf("/api/me after logout: %v", r.body)
	}

	// cookie expiry honours the injected clock
	cookie = e.login()
	e.clock.Advance(31 * 24 * time.Hour)
	if r := e.do(http.MethodGet, "/api/me", nil, cookie); r.body["authenticated"] != false {
		t.Fatalf("/api/me after TTL: %v", r.body)
	}
}

func TestLoginLockout(t *testing.T) {
	e := newTestEnv(t)
	for i := 0; i < 5; i++ {
		if r := e.do(http.MethodPost, "/api/login", map[string]string{"password": "bad"}, ""); r.status != http.StatusUnauthorized {
			t.Fatalf("attempt %d: %d", i+1, r.status)
		}
	}
	if r := e.do(http.MethodPost, "/api/login", map[string]string{"password": e.pw}, ""); r.status != http.StatusTooManyRequests {
		t.Fatalf("after 5 failures right password: %d want 429", r.status)
	}
	e.clock.Advance(15*time.Minute + time.Second)
	if r := e.do(http.MethodPost, "/api/login", map[string]string{"password": e.pw}, ""); r.status != http.StatusOK {
		t.Fatalf("after lock expiry: %d", r.status)
	}
}

// ---- pairing ------------------------------------------------------------

func TestPairing(t *testing.T) {
	// Every subtest gets its own relay: the lockout counter is per source IP
	// and httptest always connects from 127.0.0.1.
	t.Run("redeem ok and reuse rejected", func(t *testing.T) {
		e := newTestEnv(t)
		cookie := e.login()
		if r := e.do(http.MethodPost, "/api/pair/new", nil, ""); r.status != http.StatusUnauthorized {
			t.Fatalf("anonymous pair/new: %d", r.status)
		}
		code := e.pairNew(cookie)
		if len(code) != 8 {
			t.Fatalf("code %q", code)
		}
		// separators and lowercase are tolerated
		typed := string(code[:4]) + "-" + string(code[4:])
		r := e.do(http.MethodPost, "/api/pair/redeem", map[string]string{"code": typed, "name": "  my pc "}, "")
		if r.status != http.StatusOK {
			t.Fatalf("redeem: %d %v", r.status, r.body)
		}
		if r.str("device_id") == "" || r.str("device_token") == "" || r.str("fingerprint") == "" || r.str("name") != "my pc" {
			t.Fatalf("redeem body: %v", r.body)
		}
		if r2 := e.do(http.MethodPost, "/api/pair/redeem", map[string]string{"code": code}, ""); r2.status != http.StatusBadRequest {
			t.Fatalf("second redeem: %d", r2.status)
		}
		devs := e.do(http.MethodGet, "/api/devices", nil, cookie)
		list, _ := devs.body["devices"].([]any)
		if len(list) != 1 {
			t.Fatalf("devices after pairing: %v", devs.body)
		}
	})

	t.Run("expired", func(t *testing.T) {
		e := newTestEnv(t)
		cookie := e.login()
		code := e.pairNew(cookie)
		e.clock.Advance(5*time.Minute + time.Second)
		if r := e.do(http.MethodPost, "/api/pair/redeem", map[string]string{"code": code}, ""); r.status != http.StatusBadRequest {
			t.Fatalf("expired redeem: %d %v", r.status, r.body)
		}
	})

	t.Run("lockout after 5 failures", func(t *testing.T) {
		e := newTestEnv(t)
		cookie := e.login()
		code := e.pairNew(cookie)
		wrong := []string{"ZZZZZZZZ", "ZZZZZZZ2", "short", "ZZZZZZZ3", "ZZZZZZZ4"}
		for i, w := range wrong {
			if r := e.do(http.MethodPost, "/api/pair/redeem", map[string]string{"code": w}, ""); r.status != http.StatusBadRequest {
				t.Fatalf("wrong attempt %d: %d", i+1, r.status)
			}
		}
		if r := e.do(http.MethodPost, "/api/pair/redeem", map[string]string{"code": code}, ""); r.status != http.StatusTooManyRequests {
			t.Fatalf("right code while locked: %d %v", r.status, r.body)
		}
		e.clock.Advance(14 * time.Minute)
		if r := e.do(http.MethodPost, "/api/pair/redeem", map[string]string{"code": code}, ""); r.status != http.StatusTooManyRequests {
			t.Fatalf("still within lockout: %d", r.status)
		}
		e.clock.Advance(time.Minute + time.Second) // lock expired, but the code (5 min TTL) expired with it
		fresh := e.pairNew(cookie)
		if r := e.do(http.MethodPost, "/api/pair/redeem", map[string]string{"code": fresh}, ""); r.status != http.StatusOK {
			t.Fatalf("after lock expiry: %d %v", r.status, r.body)
		}
	})

	t.Run("per-code lockout", func(t *testing.T) {
		// Hammering one code locks that code as well as the IP, so a
		// distributed guesser (many IPs) still gets at most 5 tries per code.
		e := newTestEnv(t)
		for i := 0; i < 5; i++ {
			e.do(http.MethodPost, "/api/pair/redeem", map[string]string{"code": "AAAAAAAA"}, "")
		}
		if !e.srv.limiter.locked("pair:code:AAAAAAAA") || !e.srv.limiter.locked("pair:ip:127.0.0.1") {
			t.Fatalf("code / ip key not locked")
		}
	})
}

// ---- hub: hello / lists -------------------------------------------------

func TestAgentHelloReachesClients(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login()
	dev := e.pairDevice(cookie, "pc")

	c, devList, lists, approvals := e.connectClient(cookie)
	if len(devList.Devices) != 1 || devList.Devices[0].ID != dev.id || devList.Devices[0].Online {
		t.Fatalf("initial device.list: %+v", devList.Devices)
	}
	if len(lists) != 1 || lists[0].DeviceID != dev.id || len(lists[0].Sessions) != 0 {
		t.Fatalf("initial session.list: %+v", lists)
	}
	if len(approvals.Approvals) != 0 {
		t.Fatalf("initial approval.list: %+v", approvals)
	}

	a := e.connectAgent(dev)
	st := c.waitMsg("device.state online", isOnline)
	if st.DeviceID != dev.id {
		t.Fatalf("device.state: %+v", st)
	}

	a.hello(sessionInfo(7, "build"), sessionInfo(9, "claude"))
	st = c.waitMsg("device.state after hello", func(m proto.Msg) bool {
		return m.T == proto.TDeviceState && m.Device != nil && m.Device.AgentVersion == "0.1-test"
	})
	if st.Device.OS != "windows" || !st.Device.Online {
		t.Fatalf("device.state after hello: %+v", st.Device)
	}
	sl := c.waitType(proto.TSessionList)
	if sl.DeviceID != dev.id || len(sl.Sessions) != 2 {
		t.Fatalf("session.list: %+v", sl)
	}
	for _, si := range sl.Sessions {
		if si.DeviceID != dev.id || (si.SID != 7 && si.SID != 9) {
			t.Fatalf("session entry: %+v", si)
		}
	}

	// a client connecting afterwards gets the same picture in its initial burst
	c2, devList2, lists2, _ := e.connectClient(cookie)
	if !devList2.Devices[0].Online || len(lists2) != 1 || len(lists2[0].Sessions) != 2 {
		t.Fatalf("second client burst: %+v %+v", devList2.Devices, lists2)
	}
	if c2.id == c.id {
		t.Fatalf("client ids collide: %s", c.id)
	}

	// client.hello is acked with the client id
	c.sendMsg(proto.Msg{T: proto.TClientHello, ReqID: "ch"})
	if ack := c.waitType(proto.TAck); ack.ReqID != "ch" || ack.ClientID != c.id {
		t.Fatalf("client.hello ack: %+v", ack)
	}

	// second hello replaces the session set
	a.hello(sessionInfo(9, "claude"))
	sl = c.waitType(proto.TSessionList)
	if len(sl.Sessions) != 1 || sl.Sessions[0].SID != 9 {
		t.Fatalf("session.list after second hello: %+v", sl.Sessions)
	}

	// agent disconnect → offline
	_ = a.conn.Close(websocket.StatusNormalClosure, "bye")
	st = c.waitMsg("device.state offline", isOffline)
	if st.DeviceID != dev.id {
		t.Fatalf("offline state: %+v", st)
	}
	// sessions are remembered while offline
	_, _, lists3, _ := e.connectClient(cookie)
	if len(lists3) != 1 || len(lists3[0].Sessions) != 1 {
		t.Fatalf("session list kept while offline: %+v", lists3)
	}
}

func TestAgentAuth(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login()
	dev := e.pairDevice(cookie, "pc")

	cases := []struct {
		name string
		hdr  http.Header
		path string
		ok   bool
	}{
		{"bearer", http.Header{"Authorization": {"Bearer " + dev.token}}, "/ws/agent", true},
		{"query token", nil, "/ws/agent?token=" + dev.token, true},
		{"wrong token", http.Header{"Authorization": {"Bearer nope"}}, "/ws/agent", false},
		{"no token", nil, "/ws/agent", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			conn, resp, err := e.tryDialWS(tc.path, tc.hdr)
			if tc.ok {
				if err != nil {
					t.Fatalf("dial: %v", err)
				}
				_ = conn.Close(websocket.StatusNormalClosure, "")
				return
			}
			if err == nil {
				_ = conn.CloseNow()
				t.Fatalf("dial succeeded")
			}
			if resp == nil || resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("status: %v", resp)
			}
		})
	}
}

// ---- session.open round trip -------------------------------------------

func TestSessionOpenRoundTrip(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login()
	dev := e.pairDevice(cookie, "pc")
	a := e.connectAgent(dev)
	a.hello()
	c, _, _, _ := e.connectClient(cookie)

	// device offline → error with req_id
	c.sendMsg(proto.Msg{T: proto.TSessionOpen, ReqID: "r0", DeviceID: "dev_unknown", Open: &proto.OpenRequest{Tool: proto.ToolShell, Shell: "pwsh"}})
	if er := c.waitType(proto.TError); er.ReqID != "r0" || er.Error != "device offline" {
		t.Fatalf("open on unknown device: %+v", er)
	}

	c.sendMsg(proto.Msg{T: proto.TSessionOpen, ReqID: "r1", DeviceID: dev.id,
		Open: &proto.OpenRequest{Tool: proto.ToolClaude, Shell: "pwsh", Cwd: `D:\work`, Name: "terminalx", ApprovalMode: proto.ApprovalNotify}})
	got := a.waitType(proto.TSessionOpen)
	if got.ReqID != "r1" || got.ClientID != c.id || got.DeviceID != dev.id || got.Open == nil || got.Open.Name != "terminalx" {
		t.Fatalf("agent saw: %+v", got)
	}

	si := sessionInfo(42, "terminalx")
	a.sendMsg(proto.Msg{T: proto.TSessionOpened, ReqID: got.ReqID, ClientID: got.ClientID, Session: &si})
	opened := c.waitType(proto.TSessionOpened)
	if opened.ReqID != "r1" || opened.SID != 42 || opened.DeviceID != dev.id || opened.Session == nil || opened.Session.DeviceID != dev.id {
		t.Fatalf("client saw: %+v", opened)
	}

	// the session is now in the device's list for newcomers
	_, _, lists, _ := e.connectClient(cookie)
	if len(lists) != 1 || len(lists[0].Sessions) != 1 || lists[0].Sessions[0].SID != 42 {
		t.Fatalf("session list: %+v", lists)
	}

	// agent-side error carrying client_id goes only to that client
	a.sendMsg(proto.Msg{T: proto.TError, ReqID: "r2", ClientID: c.id, Error: "spawn failed"})
	if er := c.waitType(proto.TError); er.ReqID != "r2" || er.Error != "spawn failed" || er.DeviceID != dev.id {
		t.Fatalf("routed error: %+v", er)
	}

	// audit recorded the open (a later round trip on the same client
	// connection guarantees the earlier handler finished writing it)
	c.sendMsg(proto.Msg{T: proto.TClientHello, ReqID: "sync"})
	c.waitType(proto.TAck)
	audit := e.do(http.MethodGet, "/api/audit", nil, cookie)
	entries, _ := audit.body["audit"].([]any)
	found := false
	for _, x := range entries {
		if m, _ := x.(map[string]any); m["action"] == "session.open" && m["actor"] == "web:"+c.id {
			found = true
		}
	}
	if !found {
		t.Fatalf("no session.open audit entry: %v", audit.body)
	}
}

// ---- attach / frames ----------------------------------------------------

func TestAttachSnapshotAndOutputRouting(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login()
	dev := e.pairDevice(cookie, "pc")
	a := e.connectAgent(dev)
	a.hello(sessionInfo(1, "s1"), sessionInfo(2, "s2"))
	c1, _, _, _ := e.connectClient(cookie)
	c2, _, _, _ := e.connectClient(cookie)

	// attach to an unknown sid is refused
	c1.sendMsg(proto.Msg{T: proto.TSessionAttach, ReqID: "bad", DeviceID: dev.id, SID: 999})
	if er := c1.waitType(proto.TError); er.ReqID != "bad" || er.Error != "unknown session" {
		t.Fatalf("attach unknown sid: %+v", er)
	}

	// c1 attaches; agent sees client_id
	c1.attach(a, 1, "a1")

	// snapshot → only c1 (pending); c2 is not attached at all
	snap1 := proto.Frame{Type: proto.FrameSnapshot, Flags: proto.FlagReset, SID: 1, Seq: 100, Payload: []byte("screen-1")}
	a.sendFrame(snap1)
	if f := c1.nextFrame(); f.Type != proto.FrameSnapshot || string(f.Payload) != "screen-1" || f.Seq != 100 || f.Flags&proto.FlagReset == 0 {
		t.Fatalf("c1 snapshot: %+v", f)
	}

	// c2 attaches (pending); a new snapshot reaches c2 only, output reaches both
	c2.attach(a, 1, "a2")
	a.sendFrame(proto.Frame{Type: proto.FrameSnapshot, SID: 1, Seq: 100, Payload: []byte("screen-2")})
	a.sendFrame(proto.Frame{Type: proto.FrameOutput, SID: 1, Seq: 100, Payload: []byte("out")})
	if f := c2.nextFrame(); f.Type != proto.FrameSnapshot || string(f.Payload) != "screen-2" {
		t.Fatalf("c2 snapshot: %+v", f)
	}
	if f := c2.nextFrame(); f.Type != proto.FrameOutput || string(f.Payload) != "out" {
		t.Fatalf("c2 output: %+v", f)
	}
	// c1 must NOT have received screen-2: its next frame is the output
	if f := c1.nextFrame(); f.Type != proto.FrameOutput || string(f.Payload) != "out" {
		t.Fatalf("c1 next frame (expected output, no second snapshot): %+v", f)
	}

	// a multi-chunk snapshot keeps the pending flag until the last chunk
	c1.sendMsg(proto.Msg{T: proto.TSessionDetach, ReqID: "d1", DeviceID: dev.id, SID: 1})
	if ack := c1.waitType(proto.TAck); ack.ReqID != "d1" || ack.SID != 1 {
		t.Fatalf("detach ack: %+v", ack)
	}
	if m := a.waitType(proto.TSessionDetach); m.ClientID != c1.id || m.SID != 1 {
		t.Fatalf("agent saw detach: %+v", m)
	}
	c1.attach(a, 1, "a3")
	a.sendFrame(proto.Frame{Type: proto.FrameSnapshot, Flags: proto.FlagReset | proto.FlagMore, SID: 1, Seq: 200, Payload: []byte("part-1")})
	a.sendFrame(proto.Frame{Type: proto.FrameSnapshot, SID: 1, Seq: 200, Payload: []byte("part-2")})
	a.sendFrame(proto.Frame{Type: proto.FrameEOF, SID: 1, Payload: proto.EOFPayload(0)})
	for _, want := range []string{"part-1", "part-2"} {
		if f := c1.nextFrame(); f.Type != proto.FrameSnapshot || string(f.Payload) != want {
			t.Fatalf("c1 chunk: %+v want %s", f, want)
		}
	}
	if f := c1.nextFrame(); f.Type != proto.FrameEOF {
		t.Fatalf("c1 eof: %+v", f)
	}
	// c2 (already snapshotted) only gets the EOF
	if f := c2.nextFrame(); f.Type != proto.FrameEOF {
		t.Fatalf("c2 eof: %+v", f)
	}

	// frames for sid 2 do not leak to sid-1 attachments; frames for an unowned sid are dropped
	a.sendFrame(proto.Frame{Type: proto.FrameOutput, SID: 2, Payload: []byte("other")})
	a.sendFrame(proto.Frame{Type: proto.FrameOutput, SID: 555, Payload: []byte("nobody")})
	a.sendFrame(proto.Frame{Type: proto.FrameOutput, SID: 1, Payload: []byte("marker")})
	if f := c1.nextFrame(); string(f.Payload) != "marker" {
		t.Fatalf("c1 leaked frame: %+v", f)
	}

	// agent disconnect flips attachments back to pending so the next snapshot reaches them again
	_ = a.conn.Close(websocket.StatusNormalClosure, "")
	c1.waitMsg("offline", isOffline)
	e.srv.mu.Lock()
	pending := e.srv.attachments[1][c1.id] && e.srv.attachments[1][c2.id]
	e.srv.mu.Unlock()
	if !pending {
		t.Fatalf("attachments not marked pending after agent disconnect")
	}
}

func TestClientFramesRoutedToAgent(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login()
	dev := e.pairDevice(cookie, "pc")
	a := e.connectAgent(dev)
	a.hello(sessionInfo(1, "s1"))
	c, _, _, _ := e.connectClient(cookie)

	// not attached → dropped
	c.sendFrame(proto.Frame{Type: proto.FrameInput, SID: 1, Payload: []byte("dropped")})
	c.attach(a, 1, "a1")
	// output frames from a client are never forwarded
	c.sendFrame(proto.Frame{Type: proto.FrameOutput, SID: 1, Payload: []byte("spoof")})

	in := proto.Frame{Type: proto.FrameInput, SID: 1, Payload: []byte("ls\r")}
	c.sendFrame(in)
	if f := a.nextFrame(); f.Type != proto.FrameInput || !bytes.Equal(f.Payload, in.Payload) || f.SID != 1 {
		t.Fatalf("agent got: %+v", f)
	}
	c.sendFrame(proto.Frame{Type: proto.FrameResize, SID: 1, Payload: proto.ResizePayload(132, 43)})
	f := a.nextFrame()
	cols, rows, err := proto.ParseResize(f.Payload)
	if f.Type != proto.FrameResize || err != nil || cols != 132 || rows != 43 {
		t.Fatalf("agent resize: %+v %v", f, err)
	}
	c.sendFrame(proto.Frame{Type: proto.FrameAck, SID: 1, Seq: 4096})
	if f := a.nextFrame(); f.Type != proto.FrameAck || f.Seq != 4096 {
		t.Fatalf("agent ack: %+v", f)
	}

	// a malformed frame closes the client connection
	c.sendRaw(websocket.MessageBinary, []byte{9, 9, 9})
	if code := c.waitClosed(); code != websocket.StatusPolicyViolation {
		t.Fatalf("bad frame close status: %v", code)
	}
	// ... and from an agent too
	a.sendRaw(websocket.MessageBinary, []byte{proto.Version, 1, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 5})
	if code := a.waitClosed(); code != websocket.StatusPolicyViolation {
		t.Fatalf("agent bad frame close status: %v", code)
	}
}

func TestSIDRoutingAcrossDevices(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login()
	d1 := e.pairDevice(cookie, "one")
	d2 := e.pairDevice(cookie, "two")
	a1 := e.connectAgent(d1)
	a2 := e.connectAgent(d2)
	a1.hello(sessionInfo(11, "on-one"))
	a2.hello(sessionInfo(22, "on-two"))
	c, devList, lists, _ := e.connectClient(cookie)
	if len(devList.Devices) != 2 || len(lists) != 2 {
		t.Fatalf("burst: %d devices %d lists", len(devList.Devices), len(lists))
	}

	// device_id omitted: relay resolves the owner from sid
	c.sendMsg(proto.Msg{T: proto.TSessionResize, ReqID: "rz", SID: 22, Cols: 100, Rows: 30})
	if m := a2.waitType(proto.TSessionResize); m.DeviceID != d2.id || m.Cols != 100 || m.ClientID != c.id {
		t.Fatalf("a2 resize: %+v", m)
	}
	c.sendMsg(proto.Msg{T: proto.TSessionSignal, ReqID: "sg", SID: 11, Sig: proto.SigCtrlC})
	// strictly the next message: the resize for sid 22 must not have reached a1
	if m := a1.nextMsg(); m.T != proto.TSessionSignal || m.DeviceID != d1.id || m.Sig != proto.SigCtrlC {
		t.Fatalf("a1 next message: %+v", m)
	}
	c.sendMsg(proto.Msg{T: proto.TSessionSetMode, SID: 11, Mode: proto.ApprovalRemoteFirst})
	if m := a1.waitType(proto.TSessionSetMode); m.Mode != proto.ApprovalRemoteFirst {
		t.Fatalf("a1 set_mode: %+v", m)
	}

	// a second device claiming a sid owned by the first is rejected
	a2.sendMsg(proto.Msg{T: proto.TSessionOpened, ReqID: "dup", Session: &proto.SessionInfo{SID: 11, Name: "dup"}})
	if er := a2.waitType(proto.TError); er.ReqID != "dup" || er.SID != 11 {
		t.Fatalf("dup sid error: %+v", er)
	}
	e.srv.mu.Lock()
	owner := e.srv.sidOwner[11]
	e.srv.mu.Unlock()
	if owner != d1.id {
		t.Fatalf("sid 11 owner %s want %s", owner, d1.id)
	}

	// unknown sid without device → offline error
	c.sendMsg(proto.Msg{T: proto.TSessionClose, ReqID: "cl", SID: 333})
	if er := c.waitType(proto.TError); er.ReqID != "cl" {
		t.Fatalf("close unknown sid: %+v", er)
	}
}

// ---- session state / exit / close --------------------------------------

func TestSessionStateUpdates(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login()
	dev := e.pairDevice(cookie, "pc")
	a := e.connectAgent(dev)
	a.hello(sessionInfo(5, "s"))
	c, _, _, _ := e.connectClient(cookie)

	a.sendMsg(proto.Msg{T: proto.TSessionState, SID: 5, State: proto.StateNeedsInput, Kind: proto.NeedPermission, Source: proto.SourceHook, Confidence: proto.ConfidenceHigh})
	if m := c.waitType(proto.TSessionState); m.DeviceID != dev.id || m.State != proto.StateNeedsInput || m.Kind != proto.NeedPermission {
		t.Fatalf("state: %+v", m)
	}
	cost := 1.25
	a.sendMsg(proto.Msg{T: proto.TSessionUpdated, SID: 5, Session: &proto.SessionInfo{SID: 5, Name: "s", State: proto.StateRunning, CostUSD: &cost}})
	if m := c.waitType(proto.TSessionUpdated); m.Session == nil || m.Session.CostUSD == nil || *m.Session.CostUSD != cost || m.Session.DeviceID != dev.id {
		t.Fatalf("updated: %+v", m)
	}
	code := int32(143)
	a.sendMsg(proto.Msg{T: proto.TSessionExited, SID: 5, Code: &code, Resumable: &proto.Resumable{Tool: proto.ToolClaude, Name: "s"}})
	if m := c.waitType(proto.TSessionExited); m.Code == nil || *m.Code != 143 || m.Resumable == nil {
		t.Fatalf("exited: %+v", m)
	}
	_, _, lists, _ := e.connectClient(cookie)
	si := lists[0].Sessions[0]
	if si.State != proto.StateExited || si.PTYAlive || si.ExitCode == nil || *si.ExitCode != 143 || si.Resumable == nil {
		t.Fatalf("cached session after exit: %+v", si)
	}

	a.sendMsg(proto.Msg{T: proto.TSessionClosed, SID: 5})
	if m := c.waitType(proto.TSessionClosed); m.SID != 5 || m.DeviceID != dev.id {
		t.Fatalf("closed: %+v", m)
	}
	_, _, lists, _ = e.connectClient(cookie)
	if len(lists[0].Sessions) != 0 {
		t.Fatalf("session still listed after close: %+v", lists[0].Sessions)
	}
}

// ---- approvals ----------------------------------------------------------

func newApproval(key string, sid uint32) proto.Approval {
	return proto.Approval{Key: key, SID: sid, Agent: proto.ToolClaude, Tool: "Bash", Summary: "go test ./...",
		Input: []byte(`{"command":"go test ./..."}`), Cwd: `D:\work`, Level: proto.LevelHook, Mode: proto.ApprovalRemoteFirst}
}

func TestApprovalFlowWS(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login()
	dev := e.pairDevice(cookie, "pc")
	a := e.connectAgent(dev)
	a.hello(sessionInfo(3, "claude"))
	c1, _, _, _ := e.connectClient(cookie)
	c2, _, _, _ := e.connectClient(cookie)

	ap := newApproval("k1", 3)
	a.sendMsg(proto.Msg{T: proto.TApprovalNew, SID: 3, Approval: &ap})
	for _, c := range []*clientPeer{c1, c2} {
		m := c.waitType(proto.TApprovalNew)
		if m.Approval == nil || m.Approval.Key != "k1" || m.Approval.DeviceID != dev.id || m.Approval.Status != proto.ApprovalPending || m.SID != 3 {
			t.Fatalf("approval.new at %s: %+v", c.id, m)
		}
		if m.Approval.CreatedAt.IsZero() {
			t.Fatalf("created_at not filled")
		}
	}
	// persisted
	row, err := e.st.GetApproval(context.Background(), "k1")
	if err != nil || row.Status != proto.ApprovalPending || row.DeviceID != dev.id || row.Summary != ap.Summary {
		t.Fatalf("stored approval: %+v %v", row, err)
	}
	// visible to newcomers and the HTTP list
	_, _, _, al := e.connectClient(cookie)
	if len(al.Approvals) != 1 || al.Approvals[0].Key != "k1" {
		t.Fatalf("approval.list: %+v", al.Approvals)
	}
	if r := e.do(http.MethodGet, "/api/approvals", nil, cookie); len(r.body["approvals"].([]any)) != 1 {
		t.Fatalf("GET /api/approvals: %v", r.body)
	}

	// c1 decides over WS
	c1.sendMsg(proto.Msg{T: proto.TApprovalDecide, ReqID: "dec", Key: "k1", Decision: "allow"})
	got := a.waitType(proto.TApprovalDecide)
	if got.Key != "k1" || got.Decision != "allow" || got.SID != 3 || got.By != "web:"+c1.id || got.ReqID != "dec" || got.DeviceID != dev.id {
		t.Fatalf("agent decide: %+v", got)
	}
	// approval.closed is broadcast before the ack reaches the deciding client
	for _, c := range []*clientPeer{c1, c2} {
		m := c.waitType(proto.TApprovalClosed)
		if m.Key != "k1" || m.Decision != "allow" || m.Approval == nil || m.Approval.Status != proto.ApprovalAllowed || m.Approval.DecidedBy != "web:"+c1.id {
			t.Fatalf("approval.closed at %s: %+v", c.id, m)
		}
	}
	if ack := c1.waitType(proto.TAck); ack.ReqID != "dec" || ack.Key != "k1" {
		t.Fatalf("decide ack: %+v", ack)
	}
	row, _ = e.st.GetApproval(context.Background(), "k1")
	if row.Status != proto.ApprovalAllowed || row.DecidedBy != "web:"+c1.id || row.DecidedAt == nil {
		t.Fatalf("stored after decide: %+v", row)
	}

	// deciding twice → conflict error to the client
	c2.sendMsg(proto.Msg{T: proto.TApprovalDecide, ReqID: "again", Key: "k1", Decision: "deny"})
	if er := c2.waitType(proto.TError); er.ReqID != "again" || er.Error != "approval already allowed" {
		t.Fatalf("second decide: %+v", er)
	}

	// answered locally → closed_local
	ap2 := newApproval("k2", 3)
	a.sendMsg(proto.Msg{T: proto.TApprovalNew, Approval: &ap2})
	c1.waitType(proto.TApprovalNew)
	a.sendMsg(proto.Msg{T: proto.TApprovalClosed, Key: "k2"})
	if m := c1.waitType(proto.TApprovalClosed); m.Approval == nil || m.Approval.Status != proto.ApprovalClosedLocal || m.Approval.DecidedBy != "local" || m.SID != 3 {
		t.Fatalf("closed_local: %+v", m)
	}
	// hook timed out → fallback
	ap3 := newApproval("k3", 3)
	a.sendMsg(proto.Msg{T: proto.TApprovalNew, Approval: &ap3})
	c1.waitType(proto.TApprovalNew)
	a.sendMsg(proto.Msg{T: proto.TApprovalClosed, Key: "k3", Decision: "fallback"})
	if m := c1.waitType(proto.TApprovalClosed); m.Approval == nil || m.Approval.Status != proto.ApprovalFallback {
		t.Fatalf("fallback: %+v", m)
	}
	if r := e.do(http.MethodGet, "/api/approvals?status=all", nil, cookie); len(r.body["approvals"].([]any)) != 3 {
		t.Fatalf("all approvals: %v", r.body)
	}
	if r := e.do(http.MethodGet, "/api/approvals?status=bogus", nil, cookie); r.status != http.StatusBadRequest {
		t.Fatalf("bogus status filter: %d", r.status)
	}
}

func TestApprovalDecideHTTP(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login()
	dev := e.pairDevice(cookie, "pc")
	a := e.connectAgent(dev)
	a.hello(sessionInfo(3, "claude"))
	c, _, _, _ := e.connectClient(cookie)

	ap := newApproval("h1", 3)
	a.sendMsg(proto.Msg{T: proto.TApprovalNew, Approval: &ap})
	c.waitType(proto.TApprovalNew)

	cases := []struct {
		name     string
		key      string
		decision string
		cookie   string
		want     int
	}{
		{"anonymous", "h1", "deny", "", http.StatusUnauthorized},
		{"bad decision", "h1", "maybe", cookie, http.StatusBadRequest},
		{"unknown key", "nope", "deny", cookie, http.StatusNotFound},
		{"deny ok", "h1", "deny", cookie, http.StatusOK},
		{"already decided", "h1", "allow", cookie, http.StatusConflict},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := e.do(http.MethodPost, "/api/approvals/"+tc.key+"/decide", map[string]string{"decision": tc.decision}, tc.cookie)
			if r.status != tc.want {
				t.Fatalf("status %d want %d (%v)", r.status, tc.want, r.body)
			}
		})
	}
	got := a.waitType(proto.TApprovalDecide)
	if got.Key != "h1" || got.Decision != "deny" || got.By == "" || got.By == "web:"+c.id {
		t.Fatalf("agent decide via HTTP: %+v", got)
	}
	if m := c.waitType(proto.TApprovalClosed); m.Key != "h1" || m.Approval.Status != proto.ApprovalDenied {
		t.Fatalf("closed broadcast: %+v", m)
	}

	// device offline → 503
	ap2 := newApproval("h2", 3)
	a.sendMsg(proto.Msg{T: proto.TApprovalNew, Approval: &ap2})
	c.waitType(proto.TApprovalNew)
	_ = a.conn.Close(websocket.StatusNormalClosure, "")
	c.waitMsg("offline", isOffline)
	if r := e.do(http.MethodPost, "/api/approvals/h2/decide", map[string]string{"decision": "allow"}, cookie); r.status != http.StatusServiceUnavailable {
		t.Fatalf("decide while offline: %d %v", r.status, r.body)
	}
}

func TestPendingApprovalsSurviveRestart(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login()
	dev := e.pairDevice(cookie, "pc")
	a := e.connectAgent(dev)
	a.hello(sessionInfo(3, "claude"))
	c, _, _, _ := e.connectClient(cookie)
	ap := newApproval("p1", 3)
	a.sendMsg(proto.Msg{T: proto.TApprovalNew, Approval: &ap})
	c.waitType(proto.TApprovalNew)

	// a second Server over the same store sees the device and the pending item
	s2, err := New(Config{AdminPassword: "x", Now: e.clock.Now, Logger: e.srv.log}, e.st)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	s2.mu.Lock()
	_, hasDev := s2.devices[dev.id]
	pend := s2.pendingApprovalsLocked()
	s2.mu.Unlock()
	if !hasDev || len(pend) != 1 || pend[0].Key != "p1" {
		t.Fatalf("reloaded state: dev=%v pending=%+v", hasDev, pend)
	}
}

// ---- revoke / rename ----------------------------------------------------

func TestRevokeClosesAgent(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login()
	dev := e.pairDevice(cookie, "pc")
	a := e.connectAgent(dev)
	a.hello(sessionInfo(1, "s"))
	c, _, _, _ := e.connectClient(cookie)
	c.attach(a, 1, "a1")

	if r := e.do(http.MethodPatch, "/api/devices/"+dev.id, map[string]string{"name": "laptop"}, cookie); r.status != http.StatusOK {
		t.Fatalf("rename: %d %v", r.status, r.body)
	}
	if m := c.waitType(proto.TDeviceList); len(m.Devices) != 1 || m.Devices[0].Name != "laptop" {
		t.Fatalf("device.list after rename: %+v", m.Devices)
	}

	if r := e.do(http.MethodDelete, "/api/devices/"+dev.id, nil, cookie); r.status != http.StatusOK {
		t.Fatalf("revoke: %d %v", r.status, r.body)
	}
	if code := a.waitClosed(); code != websocket.StatusPolicyViolation {
		t.Fatalf("agent close status: %v", code)
	}
	if m := c.waitType(proto.TDeviceList); len(m.Devices) != 0 {
		t.Fatalf("device.list after revoke: %+v", m.Devices)
	}
	// token is dead
	if conn, resp, err := e.tryDialWS("/ws/agent?token="+dev.token, nil); err == nil {
		_ = conn.CloseNow()
		t.Fatalf("revoked token accepted")
	} else if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked token status: %v", resp)
	}
	// second revoke → 404; sid ownership is gone
	if r := e.do(http.MethodDelete, "/api/devices/"+dev.id, nil, cookie); r.status != http.StatusNotFound {
		t.Fatalf("double revoke: %d", r.status)
	}
	e.srv.mu.Lock()
	_, owned := e.srv.sidOwner[1]
	_, attached := e.srv.attachments[1]
	s := e.srv.clients[c.id].attached[1]
	e.srv.mu.Unlock()
	if owned || attached || s {
		t.Fatalf("sid state after revoke: owned=%v attached=%v client=%v", owned, attached, s)
	}
}

// ---- heartbeat ----------------------------------------------------------

func TestHeartbeatEchoAndTimeout(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login()
	dev := e.pairDevice(cookie, "pc")
	a := e.connectAgent(dev)
	a.hello(sessionInfo(1, "s"))
	c, _, _, _ := e.connectClient(cookie)

	sendHB := func(seq uint64) {
		t.Helper()
		hb := proto.Msg{T: proto.THeartbeat, Heartbeat: &proto.Heartbeat{Seq: seq, SentAt: e.clock.Now(), Power: "battery",
			Sessions: []proto.SessionHealth{{SID: 1, PTYAlive: true, Seq: 500 * seq, LastOutputAt: e.clock.Now()}}}}
		want, _ := hb.Encode()
		a.sendMsg(hb)
		raw, m := a.nextRaw()
		if m.T != proto.THeartbeat || !bytes.Equal(raw, want) {
			t.Fatalf("heartbeat echo: %s", raw)
		}
	}

	e.clock.Advance(30 * time.Second)
	sendHB(1)
	st := c.waitMsg("device.state with power", func(m proto.Msg) bool {
		return m.T == proto.TDeviceState && m.Device != nil && m.Device.Power == "battery"
	})
	if !st.Device.Online || !st.Device.LastSeen.Equal(e.clock.Now()) {
		t.Fatalf("device.state after heartbeat: %+v", st.Device)
	}

	// 40s since the last heartbeat: still online after a sweep
	e.clock.Advance(40 * time.Second)
	e.srv.SweepNow()
	sendHB(2) // proves the connection is still alive
	_, _, lists, _ := e.connectClient(cookie)
	if si := lists[0].Sessions[0]; si.Seq != 1000 || !si.PTYAlive {
		t.Fatalf("session health from heartbeat: %+v", si)
	}

	// 46s without a heartbeat → offline
	e.clock.Advance(46 * time.Second)
	e.srv.SweepNow()
	if code := a.waitClosed(); code != websocket.StatusPolicyViolation {
		t.Fatalf("timeout close status: %v", code)
	}
	st = c.waitMsg("offline", isOffline)
	if st.DeviceID != dev.id {
		t.Fatalf("offline state: %+v", st)
	}
	if r := e.do(http.MethodGet, "/healthz", nil, ""); r.body["devices_online"] != float64(0) {
		t.Fatalf("healthz: %v", r.body)
	}

	// reconnect resets the deadline; a newer connection replaces the older one
	a2 := e.connectAgent(dev)
	c.waitMsg("online again", isOnline)
	a3 := e.connectAgent(dev)
	if code := a2.waitClosed(); code != websocket.StatusPolicyViolation {
		t.Fatalf("replaced connection close status: %v", code)
	}
	a3.hello()
	e.srv.mu.Lock()
	d := e.srv.devices[dev.id]
	online := d.conn != nil && d.info.Online
	e.srv.mu.Unlock()
	if !online {
		t.Fatalf("device not online after replacement")
	}
}

// ---- settings / webhook -------------------------------------------------

func TestWebhookSetting(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login()
	cases := []struct {
		url  string
		want int
	}{
		{"https://ntfy.sh/topic", http.StatusOK},
		{"ftp://x", http.StatusBadRequest},
		{"not a url", http.StatusBadRequest},
		{"", http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.url, func(t *testing.T) {
			r := e.do(http.MethodPut, "/api/settings/webhook", map[string]string{"url": tc.url}, cookie)
			if r.status != tc.want {
				t.Fatalf("status %d want %d", r.status, tc.want)
			}
			if tc.want == http.StatusOK {
				if g := e.do(http.MethodGet, "/api/settings/webhook", nil, cookie); g.str("url") != tc.url {
					t.Fatalf("get: %v", g.body)
				}
			}
		})
	}
}

// TestPendingAttachSkipsOutput: output produced between a client's attach
// and the agent's replay must not reach that client (the replay already
// contains it), while clients whose attach is complete keep receiving it.
func TestPendingAttachSkipsOutput(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login()
	dev := e.pairDevice(cookie, "pc")
	a := e.connectAgent(dev)
	a.hello(sessionInfo(1, "s"))
	c1, _, _, _ := e.connectClient(cookie)
	c2, _, _, _ := e.connectClient(cookie)

	c1.attach(a, 1, "a1")
	a.sendFrame(proto.Frame{Type: proto.FrameSnapshot, Flags: proto.FlagReset, SID: 1, Seq: 10, Payload: []byte("screen")})
	if f := c1.nextFrame(); f.Type != proto.FrameSnapshot || string(f.Payload) != "screen" {
		t.Fatalf("c1 snapshot: %+v", f)
	}

	c2.attach(a, 1, "a2") // pending until its snapshot arrives
	a.sendFrame(proto.Frame{Type: proto.FrameOutput, SID: 1, Seq: 10, Payload: []byte("live")})
	a.sendFrame(proto.Frame{Type: proto.FrameSnapshot, SID: 1, Seq: 14, Payload: []byte("screenlive")}) // delta replay for c2
	a.sendFrame(proto.Frame{Type: proto.FrameOutput, SID: 1, Seq: 14, Payload: []byte("after")})

	for i, want := range []string{"live", "after"} {
		if f := c1.nextFrame(); f.Type != proto.FrameOutput || string(f.Payload) != want {
			t.Fatalf("c1 frame %d: %+v want output %q", i, f, want)
		}
	}
	if f := c2.nextFrame(); f.Type != proto.FrameSnapshot || string(f.Payload) != "screenlive" || f.Seq != 14 {
		t.Fatalf("c2 first frame must be its snapshot, got %+v", f)
	}
	if f := c2.nextFrame(); f.Type != proto.FrameOutput || string(f.Payload) != "after" {
		t.Fatalf("c2 after snapshot: %+v", f)
	}
}

// TestDecideLevelAndErrorRouting: only level-A (hook) approvals accept a
// decision; the agent's reply to approval.decide carries the deciding
// client's id so an error reaches that client only.
func TestDecideLevelAndErrorRouting(t *testing.T) {
	e := newTestEnv(t)
	cookie := e.login()
	dev := e.pairDevice(cookie, "pc")
	a := e.connectAgent(dev)
	a.hello(sessionInfo(3, "claude"))
	c1, _, _, _ := e.connectClient(cookie)
	c2, _, _, _ := e.connectClient(cookie)

	tests := []struct {
		name  string
		level proto.ApprovalLevel
		mode  proto.ApprovalMode
		want  int // HTTP status of POST /api/approvals/{key}/decide
	}{
		{"level B keys", proto.LevelKeys, proto.ApprovalNotify, http.StatusBadRequest},
		{"level C suspect", proto.LevelSuspect, proto.ApprovalNotify, http.StatusBadRequest},
		{"level A hook", proto.LevelHook, proto.ApprovalRemoteFirst, http.StatusOK},
	}
	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			key := "k" + strconv.Itoa(i)
			ap := newApproval(key, 3)
			ap.Level, ap.Mode = tc.level, tc.mode
			a.sendMsg(proto.Msg{T: proto.TApprovalNew, SID: 3, Approval: &ap})
			c1.waitType(proto.TApprovalNew)
			c2.waitType(proto.TApprovalNew)

			c1.sendMsg(proto.Msg{T: proto.TApprovalDecide, ReqID: "ws-" + key, Key: key, Decision: "allow"})
			if tc.want != http.StatusOK {
				if er := c1.waitType(proto.TError); er.ReqID != "ws-"+key || !strings.Contains(er.Error, "level "+string(tc.level)) {
					t.Fatalf("ws decide on level %s: %+v", tc.level, er)
				}
				if r := e.do(http.MethodPost, "/api/approvals/"+key+"/decide", map[string]string{"decision": "allow"}, cookie); r.status != tc.want {
					t.Fatalf("http decide: %d %v", r.status, r.body)
				}
				if row, err := e.st.GetApproval(context.Background(), key); err != nil || row.Status != proto.ApprovalPending {
					t.Fatalf("approval must stay pending: %+v %v", row, err)
				}
				return
			}
			got := a.waitType(proto.TApprovalDecide)
			if got.ClientID != c1.id || got.By != "web:"+c1.id || got.ReqID != "ws-"+key {
				t.Fatalf("agent decide must carry the deciding client id: %+v", got)
			}
			c1.waitType(proto.TApprovalClosed)
			c2.waitType(proto.TApprovalClosed)
			c1.waitType(proto.TAck)
			// the agent answers with an error (e.g. it no longer holds the key): only c1 sees it
			a.sendMsg(proto.Msg{T: proto.TError, ReqID: got.ReqID, ClientID: got.ClientID, SID: got.SID, Error: "approval not pending"})
			if er := c1.waitType(proto.TError); er.ReqID != "ws-"+key || er.Error != "approval not pending" {
				t.Fatalf("agent error at c1: %+v", er)
			}
			c2.sendMsg(proto.Msg{T: proto.TClientHello, ReqID: "sentinel"})
			if m := c2.nextMsg(); m.T != proto.TAck || m.ReqID != "sentinel" {
				t.Fatalf("c2 must not receive the agent error, got %+v", m)
			}
		})
	}
}
