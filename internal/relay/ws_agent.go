package relay

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/wfighter1/terminalX/internal/proto"
)

const (
	wsReadLimit   = 4 << 20 // control messages with large session lists; frames are ≤ 64 KiB + 18
	agentQueueLen = 1024
	pingInterval  = 15 * time.Second
)

// agentConn is one live /ws/agent connection.
type agentConn struct {
	s        *Server
	deviceID string
	conn     *websocket.Conn
	out      chan outMsg
	ctx      context.Context
	cancel   context.CancelFunc

	// guarded by s.mu
	lastHeartbeat time.Time
	rtt           int

	closeOnce sync.Once
}

func (a *agentConn) send(typ websocket.MessageType, data []byte) {
	select {
	case a.out <- outMsg{typ, data}:
	default:
		a.s.log.Warn("agent send queue full, closing", "device_id", a.deviceID)
		a.closeNow(websocket.StatusPolicyViolation, "slow consumer")
	}
}

func (a *agentConn) sendMsg(m proto.Msg) {
	data, err := m.Encode()
	if err != nil {
		a.s.log.Error("encode agent msg", "t", m.T, "err", err)
		return
	}
	a.send(websocket.MessageText, data)
}

func (a *agentConn) closeNow(code websocket.StatusCode, reason string) {
	a.closeOnce.Do(func() {
		_ = a.conn.Close(code, reason)
		a.cancel()
	})
}

func (a *agentConn) writeLoop() {
	for {
		select {
		case <-a.ctx.Done():
			return
		case m := <-a.out:
			wctx, cancel := context.WithTimeout(a.ctx, 10*time.Second)
			err := a.conn.Write(wctx, m.typ, m.data)
			cancel()
			if err != nil {
				a.closeNow(websocket.StatusAbnormalClosure, "write failed")
				return
			}
		}
	}
}

// pingLoop measures RTT with WebSocket ping/pong.
func (a *agentConn) pingLoop() {
	t := time.NewTicker(pingInterval)
	defer t.Stop()
	for {
		select {
		case <-a.ctx.Done():
			return
		case <-t.C:
			pctx, cancel := context.WithTimeout(a.ctx, 10*time.Second)
			start := time.Now()
			err := a.conn.Ping(pctx)
			cancel()
			if err != nil {
				continue
			}
			ms := int(time.Since(start) / time.Millisecond)
			a.s.mu.Lock()
			a.rtt = ms
			if d := a.s.devices[a.deviceID]; d != nil && d.conn == a {
				d.info.RTTms = ms
			}
			a.s.mu.Unlock()
		}
	}
}

func bearerToken(r *http.Request) string {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(strings.ToLower(h), "bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return r.URL.Query().Get("token")
}

// handleAgentWS authenticates the device token and runs the agent connection.
func (s *Server) handleAgentWS(w http.ResponseWriter, r *http.Request) {
	tok := bearerToken(r)
	dev, err := s.store.DeviceByTokenHash(r.Context(), hashToken(tok))
	if err != nil {
		s.log.Warn("agent auth failed", "ip", clientIP(r))
		writeError(w, http.StatusUnauthorized, "invalid device token")
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true, // agents are not browsers; token is the credential
		CompressionMode:    websocket.CompressionDisabled,
	})
	if err != nil {
		s.log.Warn("agent ws accept", "device_id", dev.ID, "err", err)
		return
	}
	conn.SetReadLimit(wsReadLimit)
	ctx, cancel := context.WithCancel(context.Background())
	ac := &agentConn{s: s, deviceID: dev.ID, conn: conn, out: make(chan outMsg, agentQueueLen), ctx: ctx, cancel: cancel}

	// register (a newer connection replaces an older one)
	var old *agentConn
	s.mu.Lock()
	d, ok := s.devices[dev.ID]
	if !ok {
		d = newDeviceState(dev)
		s.devices[dev.ID] = d
	}
	old = d.conn
	d.conn = ac
	d.info.Online = true
	d.info.LastSeen = s.now()
	ac.lastHeartbeat = d.info.LastSeen
	s.mu.Unlock()
	if old != nil {
		old.closeNow(websocket.StatusPolicyViolation, "replaced by a newer connection")
	}
	s.log.Info("agent connected", "device_id", dev.ID, "name", dev.Name, "ip", clientIP(r))
	s.broadcastDeviceState(dev.ID)

	go ac.writeLoop()
	go ac.pingLoop()
	ac.readLoop()

	// unregister
	s.mu.Lock()
	if d := s.devices[dev.ID]; d != nil && d.conn == ac {
		d.conn = nil
		d.info.Online = false
		d.info.RTTms = 0
		for sid := range s.attachments {
			if s.sidOwner[sid] == dev.ID {
				for cid := range s.attachments[sid] {
					s.attachments[sid][cid] = true // next snapshot must reach them again
				}
			}
		}
	}
	s.mu.Unlock()
	s.log.Info("agent disconnected", "device_id", dev.ID)
	s.broadcastDeviceState(dev.ID)
}

func (a *agentConn) readLoop() {
	defer a.closeNow(websocket.StatusNormalClosure, "")
	for {
		typ, data, err := a.conn.Read(a.ctx)
		if err != nil {
			if a.ctx.Err() == nil && websocket.CloseStatus(err) == -1 && !errors.Is(err, context.Canceled) {
				a.s.log.Debug("agent read", "device_id", a.deviceID, "err", err)
			}
			return
		}
		switch typ {
		case websocket.MessageText:
			m, err := proto.Decode(data)
			if err != nil {
				a.s.log.Warn("agent bad json", "device_id", a.deviceID, "err", err)
				continue
			}
			a.s.handleAgentMsg(a, m, data)
		case websocket.MessageBinary:
			if !a.s.routeAgentFrame(a, data) {
				a.closeNow(websocket.StatusPolicyViolation, "bad frame")
				return
			}
		}
	}
}

// handleAgentMsg processes one control message from an agent.
func (s *Server) handleAgentMsg(a *agentConn, m proto.Msg, raw []byte) {
	m.DeviceID = a.deviceID
	switch m.T {
	case proto.TAgentHello:
		s.onAgentHello(a, m)
	case proto.THeartbeat:
		s.onHeartbeat(a, m, raw)
	case proto.TSessionOpened, proto.TSessionUpdated:
		s.onSessionUpsert(a, m)
	case proto.TSessionState:
		s.onSessionState(a, m)
	case proto.TSessionExited:
		s.onSessionExited(a, m)
	case proto.TSessionClosed:
		s.onSessionClosed(a, m)
	case proto.TApprovalNew:
		s.onApprovalNew(a, m)
	case proto.TApprovalClosed:
		s.onApprovalClosed(a, m)
	case proto.TAck, proto.TError:
		// replies to a specific client's request are routed to that client only
		if m.ClientID != "" {
			s.mu.Lock()
			c := s.clients[m.ClientID]
			s.mu.Unlock()
			if c != nil {
				c.sendMsg(m)
			}
			return
		}
		s.broadcast(m)
	default:
		s.log.Debug("forwarding unknown agent message", "t", m.T, "device_id", a.deviceID)
		s.broadcast(m)
	}
}

func (s *Server) onAgentHello(a *agentConn, m proto.Msg) {
	now := s.now()
	s.mu.Lock()
	d := s.devices[a.deviceID]
	if d == nil || d.conn != a {
		s.mu.Unlock()
		return
	}
	if m.OS != "" {
		d.info.OS = m.OS
	}
	if m.Version != "" {
		d.info.AgentVersion = m.Version
	}
	d.info.LastSeen = now
	a.lastHeartbeat = now
	// replace the session set
	seen := map[uint32]bool{}
	fresh := map[uint32]*proto.SessionInfo{}
	for i := range m.Sessions {
		si := m.Sessions[i]
		if !s.registerSIDLocked(si.SID, a.deviceID) {
			continue
		}
		si.DeviceID = a.deviceID
		fresh[si.SID] = &si
		seen[si.SID] = true
	}
	for sid := range d.sessions {
		if !seen[sid] {
			s.forgetSIDLocked(sid)
		}
	}
	d.sessions = fresh
	s.mu.Unlock()

	s.touchLastSeen(a.deviceID, now, m.OS, m.Version)
	s.log.Info("agent hello", "device_id", a.deviceID, "version", m.Version, "os", m.OS, "sessions", len(fresh), "caps", m.Caps)
	a.sendMsg(proto.Msg{T: proto.TAck, ReqID: m.ReqID, DeviceID: a.deviceID})
	s.broadcastDeviceState(a.deviceID)
	s.broadcastSessionList(a.deviceID)
}

func (s *Server) onHeartbeat(a *agentConn, m proto.Msg, raw []byte) {
	now := s.now()
	s.mu.Lock()
	d := s.devices[a.deviceID]
	if d == nil || d.conn != a {
		s.mu.Unlock()
		return
	}
	a.lastHeartbeat = now
	d.info.LastSeen = now
	if hb := m.Heartbeat; hb != nil {
		if hb.Power != "" {
			d.info.Power = hb.Power
		}
		for _, h := range hb.Sessions {
			if si := d.sessions[h.SID]; si != nil {
				si.PTYAlive = h.PTYAlive
				if !h.LastOutputAt.IsZero() {
					si.LastOutputAt = h.LastOutputAt
				}
				if h.Seq > si.Seq {
					si.Seq = h.Seq
				}
			}
		}
	}
	s.mu.Unlock()
	// echo verbatim so the agent can compute RTT from seq / sent_at
	a.send(websocket.MessageText, raw)
	s.touchLastSeen(a.deviceID, now, "", "")
	s.broadcastDeviceState(a.deviceID)
}

// onSessionUpsert handles session.opened and session.updated.
func (s *Server) onSessionUpsert(a *agentConn, m proto.Msg) {
	if m.Session == nil {
		if m.SID == 0 {
			s.log.Warn("session message without session", "t", m.T, "device_id", a.deviceID)
			return
		}
		m.Session = &proto.SessionInfo{SID: m.SID, State: proto.StateUnknown}
	}
	si := *m.Session
	si.DeviceID = a.deviceID
	m.SID = si.SID
	s.mu.Lock()
	d := s.devices[a.deviceID]
	if d == nil || !s.registerSIDLocked(si.SID, a.deviceID) {
		s.mu.Unlock()
		if d != nil {
			a.sendMsg(proto.Msg{T: proto.TError, ReqID: m.ReqID, ClientID: m.ClientID, SID: si.SID, Error: "sid already in use by another device"})
		}
		return
	}
	d.sessions[si.SID] = &si
	s.mu.Unlock()
	m.Session = &si
	s.broadcast(m)
}

func (s *Server) onSessionState(a *agentConn, m proto.Msg) {
	s.mu.Lock()
	if d := s.devices[a.deviceID]; d != nil {
		if si := d.sessions[m.SID]; si != nil {
			if m.State != "" {
				si.State = m.State
			}
			si.Kind = m.Kind
			if m.Source != "" {
				si.Source = m.Source
			}
			if m.Confidence != "" {
				si.Confidence = m.Confidence
			}
			if m.Session != nil {
				cp := *m.Session
				cp.DeviceID = a.deviceID
				*si = cp
			}
		}
	}
	s.mu.Unlock()
	s.broadcast(m)
}

func (s *Server) onSessionExited(a *agentConn, m proto.Msg) {
	s.mu.Lock()
	if d := s.devices[a.deviceID]; d != nil {
		if si := d.sessions[m.SID]; si != nil {
			si.State = proto.StateExited
			si.PTYAlive = false
			si.ExitCode = m.Code
			si.Resumable = m.Resumable
		}
	}
	s.mu.Unlock()
	s.broadcast(m)
}

func (s *Server) onSessionClosed(a *agentConn, m proto.Msg) {
	s.mu.Lock()
	if d := s.devices[a.deviceID]; d != nil {
		delete(d.sessions, m.SID)
	}
	if s.sidOwner[m.SID] == a.deviceID {
		s.forgetSIDLocked(m.SID)
	}
	s.mu.Unlock()
	s.broadcast(m)
}

func (s *Server) onApprovalNew(a *agentConn, m proto.Msg) {
	if m.Approval == nil || m.Approval.Key == "" {
		s.log.Warn("approval.new without approval/key", "device_id", a.deviceID)
		return
	}
	ap := *m.Approval
	ap.DeviceID = a.deviceID
	if ap.Status == "" {
		ap.Status = proto.ApprovalPending
	}
	if ap.CreatedAt.IsZero() {
		ap.CreatedAt = s.now()
	}
	if ap.SID == 0 {
		ap.SID = m.SID
	}
	var deviceName, sessionName string
	s.mu.Lock()
	cached := ap // the cache owns its own copy; ap is read below without the lock
	s.approvals[ap.Key] = &cached
	if d := s.devices[a.deviceID]; d != nil {
		deviceName = d.info.Name
		if si := d.sessions[ap.SID]; si != nil {
			sessionName = si.Name
		}
	}
	s.mu.Unlock()
	if err := s.store.UpsertApproval(context.Background(), ap); err != nil {
		s.log.Error("persist approval", "key", ap.Key, "err", err)
	}
	m.Approval = &ap
	m.SID = ap.SID
	s.broadcast(m)
	if ap.Status == proto.ApprovalPending {
		s.webhook.notifyApproval(ap, deviceName, sessionName)
	}
}

func (s *Server) onApprovalClosed(a *agentConn, m proto.Msg) {
	if m.Key == "" {
		return
	}
	now := s.now()
	by := m.By
	if by == "" {
		by = "local"
	}
	s.mu.Lock()
	ap := s.approvals[m.Key]
	var snapshot *proto.Approval
	changed := false
	if ap != nil {
		if ap.Status == proto.ApprovalPending {
			ap.Status = proto.ApprovalClosedLocal
			if m.Decision == "fallback" {
				ap.Status = proto.ApprovalFallback
			}
			ap.DecidedBy = by
			ap.DecidedAt = &now
			changed = true
		}
		cp := *ap
		snapshot = &cp
		m.SID = ap.SID
	}
	s.mu.Unlock()
	if changed {
		if err := s.store.SetApprovalStatus(context.Background(), m.Key, snapshot.Status, by, now); err != nil {
			s.log.Error("persist approval.closed", "key", m.Key, "err", err)
		}
	}
	m.Approval = snapshot
	s.broadcast(m)
}

// routeAgentFrame delivers a data frame from an agent to attached clients.
// Snapshot frames only reach clients with a pending attach; Output frames
// only reach clients whose attach is complete (the agent computes the
// replay with output delivery paused, so a pending client would otherwise
// see bytes twice: once live and once inside the snapshot); EOF reaches
// every attached client. Returns false on a malformed frame.
func (s *Server) routeAgentFrame(a *agentConn, data []byte) bool {
	t, sid, err := proto.PeekHeader(data)
	if err != nil {
		s.log.Warn("bad frame from agent", "device_id", a.deviceID, "err", err)
		return false
	}
	flags := data[2]
	s.mu.Lock()
	if owner, ok := s.sidOwner[sid]; !ok || owner != a.deviceID {
		s.mu.Unlock()
		return true // unknown sid: drop silently
	}
	atts := s.attachments[sid]
	var targets []*clientConn
	for cid, pending := range atts {
		c := s.clients[cid]
		if c == nil {
			continue
		}
		switch t {
		case proto.FrameSnapshot:
			if !pending {
				continue
			}
			if flags&proto.FlagMore == 0 {
				atts[cid] = false
			}
		case proto.FrameOutput:
			if pending {
				continue
			}
		}
		targets = append(targets, c)
	}
	s.mu.Unlock()
	for _, c := range targets {
		c.send(websocket.MessageBinary, data)
	}
	return true
}
