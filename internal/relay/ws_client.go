package relay

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/wfighter1/terminalX/internal/proto"
)

const clientQueueLen = 2048

// clientConn is one live /ws/client connection.
type clientConn struct {
	s       *Server
	id      string
	loginID string
	conn    *websocket.Conn
	out     chan outMsg
	ctx     context.Context
	cancel  context.CancelFunc

	attached map[uint32]bool // guarded by s.mu

	closeOnce sync.Once
}

func (c *clientConn) send(typ websocket.MessageType, data []byte) {
	select {
	case c.out <- outMsg{typ, data}:
	default:
		c.s.log.Warn("client send queue full, closing", "client_id", c.id)
		c.closeNow(websocket.StatusPolicyViolation, "slow consumer")
	}
}

func (c *clientConn) sendMsg(m proto.Msg) {
	data, err := m.Encode()
	if err != nil {
		c.s.log.Error("encode client msg", "t", m.T, "err", err)
		return
	}
	c.send(websocket.MessageText, data)
}

func (c *clientConn) sendError(reqID, deviceID string, sid uint32, msg string) {
	c.sendMsg(proto.Msg{T: proto.TError, ReqID: reqID, DeviceID: deviceID, SID: sid, Error: msg})
}

func (c *clientConn) closeNow(code websocket.StatusCode, reason string) {
	c.closeOnce.Do(func() {
		_ = c.conn.Close(code, reason)
		c.cancel()
	})
}

func (c *clientConn) writeLoop() {
	for {
		select {
		case <-c.ctx.Done():
			return
		case m := <-c.out:
			wctx, cancel := context.WithTimeout(c.ctx, 10*time.Second)
			err := c.conn.Write(wctx, m.typ, m.data)
			cancel()
			if err != nil {
				c.closeNow(websocket.StatusAbnormalClosure, "write failed")
				return
			}
		}
	}
}

// handleClientWS authenticates by cookie and runs the console connection.
func (s *Server) handleClientWS(w http.ResponseWriter, r *http.Request) {
	ls := s.sessionFromRequest(r)
	if ls == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns:  s.cfg.AllowedOrigins,
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		s.log.Warn("client ws accept", "err", err)
		return
	}
	conn.SetReadLimit(wsReadLimit)
	ctx, cancel := context.WithCancel(context.Background())
	c := &clientConn{s: s, id: randomID("c-", 4), loginID: ls.ID, conn: conn,
		out: make(chan outMsg, clientQueueLen), ctx: ctx, cancel: cancel, attached: map[uint32]bool{}}

	s.mu.Lock()
	s.clients[c.id] = c
	devices := s.deviceListLocked()
	lists := make([]proto.Msg, 0, len(s.devices))
	for id, d := range s.devices {
		sl := d.sessionList()
		if sl == nil {
			sl = []proto.SessionInfo{}
		}
		lists = append(lists, proto.Msg{T: proto.TSessionList, DeviceID: id, Sessions: sl})
	}
	pending := s.pendingApprovalsLocked()
	s.mu.Unlock()
	s.log.Info("client connected", "client_id", c.id, "login", ls.ID, "ip", clientIP(r))

	go c.writeLoop()
	if devices == nil {
		devices = []proto.DeviceInfo{}
	}
	c.sendMsg(proto.Msg{T: proto.TDeviceList, ClientID: c.id, Devices: devices})
	for _, m := range lists {
		c.sendMsg(m)
	}
	if pending == nil {
		pending = []proto.Approval{}
	}
	c.sendMsg(proto.Msg{T: proto.TApprovalList, Approvals: pending})

	c.readLoop()

	s.mu.Lock()
	delete(s.clients, c.id)
	for sid := range c.attached {
		if atts := s.attachments[sid]; atts != nil {
			delete(atts, c.id)
			if len(atts) == 0 {
				delete(s.attachments, sid)
			}
		}
	}
	s.mu.Unlock()
	s.log.Info("client disconnected", "client_id", c.id)
}

func (c *clientConn) readLoop() {
	defer c.closeNow(websocket.StatusNormalClosure, "")
	for {
		typ, data, err := c.conn.Read(c.ctx)
		if err != nil {
			if c.ctx.Err() == nil && websocket.CloseStatus(err) == -1 && !errors.Is(err, context.Canceled) {
				c.s.log.Debug("client read", "client_id", c.id, "err", err)
			}
			return
		}
		switch typ {
		case websocket.MessageText:
			m, err := proto.Decode(data)
			if err != nil {
				c.sendError("", "", 0, "bad json: "+err.Error())
				continue
			}
			c.s.handleClientMsg(c, m)
		case websocket.MessageBinary:
			if !c.s.routeClientFrame(c, data) {
				c.closeNow(websocket.StatusPolicyViolation, "bad frame")
				return
			}
		}
	}
}

// agentFor resolves the agent connection for m (device_id, or the sid owner
// when device_id is missing). ok=false means the device is offline / unknown.
func (s *Server) agentFor(m *proto.Msg) (*agentConn, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m.DeviceID == "" && m.SID != 0 {
		m.DeviceID = s.sidOwner[m.SID]
	}
	d := s.devices[m.DeviceID]
	if d == nil || d.conn == nil {
		return nil, false
	}
	return d.conn, true
}

// handleClientMsg processes one control message from a console client.
func (s *Server) handleClientMsg(c *clientConn, m proto.Msg) {
	m.ClientID = c.id
	actor := "web:" + c.id
	switch m.T {
	case proto.TClientHello:
		c.sendMsg(proto.Msg{T: proto.TAck, ReqID: m.ReqID, ClientID: c.id})

	case proto.TSessionAttach:
		ac, ok := s.agentFor(&m)
		if !ok {
			c.sendError(m.ReqID, m.DeviceID, m.SID, "device offline")
			return
		}
		s.mu.Lock()
		if s.sidOwner[m.SID] != m.DeviceID {
			s.mu.Unlock()
			c.sendError(m.ReqID, m.DeviceID, m.SID, "unknown session")
			return
		}
		if s.attachments[m.SID] == nil {
			s.attachments[m.SID] = map[string]bool{}
		}
		s.attachments[m.SID][c.id] = true // pending snapshot
		c.attached[m.SID] = true
		s.mu.Unlock()
		ac.sendMsg(m)

	case proto.TSessionDetach:
		s.mu.Lock()
		if m.DeviceID == "" {
			m.DeviceID = s.sidOwner[m.SID]
		}
		delete(c.attached, m.SID)
		if atts := s.attachments[m.SID]; atts != nil {
			delete(atts, c.id)
			if len(atts) == 0 {
				delete(s.attachments, m.SID)
			}
		}
		d := s.devices[m.DeviceID]
		var ac *agentConn
		if d != nil {
			ac = d.conn
		}
		s.mu.Unlock()
		if ac != nil {
			ac.sendMsg(m)
		}
		c.sendMsg(proto.Msg{T: proto.TAck, ReqID: m.ReqID, DeviceID: m.DeviceID, SID: m.SID})

	case proto.TSessionOpen, proto.TSessionResize, proto.TSessionSignal, proto.TSessionClose, proto.TSessionSetMode:
		ac, ok := s.agentFor(&m)
		if !ok {
			c.sendError(m.ReqID, m.DeviceID, m.SID, "device offline")
			return
		}
		ac.sendMsg(m)
		switch m.T {
		case proto.TSessionOpen:
			detail := ""
			if m.Open != nil {
				detail = m.Open.Tool + " " + m.Open.Shell + " " + m.Open.Cwd
			}
			s.audit(actor, m.DeviceID, 0, "session.open", detail)
		case proto.TSessionClose:
			s.audit(actor, m.DeviceID, m.SID, "session.close", "")
		case proto.TSessionSignal:
			s.audit(actor, m.DeviceID, m.SID, "session.signal", m.Sig)
		}

	case proto.TApprovalDecide:
		if err := s.decideApproval(m.Key, m.Decision, actor, m.DeviceID, m.ReqID, c.id); err != nil {
			c.sendError(m.ReqID, m.DeviceID, m.SID, err.Error())
			return
		}
		c.sendMsg(proto.Msg{T: proto.TAck, ReqID: m.ReqID, Key: m.Key, Decision: m.Decision})

	default:
		c.sendError(m.ReqID, m.DeviceID, m.SID, "unknown message type "+m.T)
	}
}

// routeClientFrame forwards Input / Resize / Ack frames to the agent owning
// the sid. The client must be attached to the sid. Returns false on a
// malformed frame (caller closes the connection).
func (s *Server) routeClientFrame(c *clientConn, data []byte) bool {
	t, sid, err := proto.PeekHeader(data)
	if err != nil {
		s.log.Warn("bad frame from client", "client_id", c.id, "err", err)
		return false
	}
	switch t {
	case proto.FrameInput, proto.FrameResize, proto.FrameAck:
	default:
		s.log.Warn("client sent non-input frame, dropped", "client_id", c.id, "type", t)
		return true
	}
	s.mu.Lock()
	var ac *agentConn
	if c.attached[sid] {
		if d := s.devices[s.sidOwner[sid]]; d != nil {
			ac = d.conn
		}
	}
	s.mu.Unlock()
	if ac == nil {
		return true // not attached or device offline: drop
	}
	ac.send(websocket.MessageBinary, data)
	return true
}
