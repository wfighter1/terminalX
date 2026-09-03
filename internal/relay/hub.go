package relay

import (
	"context"
	"time"

	"github.com/coder/websocket"

	"github.com/wfighter1/terminalX/internal/proto"
	"github.com/wfighter1/terminalX/internal/relay/store"
)

// deviceState is the relay's in-memory view of one paired device. Session
// metadata is kept while the agent is offline so the console can show
// "可拉回" sessions after a reboot.
type deviceState struct {
	info     proto.DeviceInfo
	sessions map[uint32]*proto.SessionInfo
	conn     *agentConn // nil when offline
}

func newDeviceState(d store.Device) *deviceState {
	return &deviceState{
		info: proto.DeviceInfo{
			ID: d.ID, Name: d.Name, OS: d.OS, AgentVersion: d.AgentVersion,
			LastSeen: d.LastSeen, Fingerprint: d.Fingerprint,
		},
		sessions: map[uint32]*proto.SessionInfo{},
	}
}

func (d *deviceState) sessionList() []proto.SessionInfo {
	out := make([]proto.SessionInfo, 0, len(d.sessions))
	for _, si := range d.sessions {
		out = append(out, *si)
	}
	return out
}

// outMsg is one queued WebSocket message.
type outMsg struct {
	typ  websocket.MessageType
	data []byte
}

// ---- locked helpers -----------------------------------------------------

// deviceInfoLocked returns a copy of the DeviceInfo. mu must be held.
func (s *Server) deviceInfoLocked(id string) (proto.DeviceInfo, bool) {
	d, ok := s.devices[id]
	if !ok {
		return proto.DeviceInfo{}, false
	}
	return d.info, true
}

func (s *Server) deviceListLocked() []proto.DeviceInfo {
	out := make([]proto.DeviceInfo, 0, len(s.devices))
	for _, d := range s.devices {
		out = append(out, d.info)
	}
	return out
}

// registerSIDLocked binds sid to deviceID. It refuses when the sid already
// belongs to another device (sids are agent-generated 31-bit random values;
// a collision across devices is rejected rather than silently rerouted).
func (s *Server) registerSIDLocked(sid uint32, deviceID string) bool {
	if owner, ok := s.sidOwner[sid]; ok && owner != deviceID {
		s.log.Warn("sid already owned by another device, rejecting",
			"sid", sid, "owner", owner, "device_id", deviceID)
		return false
	}
	s.sidOwner[sid] = deviceID
	return true
}

func (s *Server) forgetSIDLocked(sid uint32) {
	delete(s.sidOwner, sid)
	delete(s.attachments, sid)
	for _, c := range s.clients {
		delete(c.attached, sid)
	}
}

// pendingApprovalsLocked returns the cached pending approvals.
func (s *Server) pendingApprovalsLocked() []proto.Approval {
	out := make([]proto.Approval, 0, len(s.approvals))
	for _, a := range s.approvals {
		if a.Status == proto.ApprovalPending {
			out = append(out, *a)
		}
	}
	return out
}

// ---- broadcast ----------------------------------------------------------

// broadcast sends a control message to every client. Safe with or without mu.
func (s *Server) broadcast(m proto.Msg) {
	data, err := m.Encode()
	if err != nil {
		s.log.Error("encode broadcast", "t", m.T, "err", err)
		return
	}
	s.mu.Lock()
	clients := make([]*clientConn, 0, len(s.clients))
	for _, c := range s.clients {
		clients = append(clients, c)
	}
	s.mu.Unlock()
	for _, c := range clients {
		c.send(websocket.MessageText, data)
	}
}

func (s *Server) broadcastDeviceState(id string) {
	s.mu.Lock()
	info, ok := s.deviceInfoLocked(id)
	s.mu.Unlock()
	if !ok {
		return
	}
	s.broadcast(proto.Msg{T: proto.TDeviceState, DeviceID: id, Device: &info})
}

func (s *Server) broadcastDeviceList() {
	s.mu.Lock()
	list := s.deviceListLocked()
	s.mu.Unlock()
	s.broadcast(proto.Msg{T: proto.TDeviceList, Devices: list})
}

func (s *Server) broadcastSessionList(id string) {
	s.mu.Lock()
	d, ok := s.devices[id]
	var list []proto.SessionInfo
	if ok {
		list = d.sessionList()
	}
	s.mu.Unlock()
	if !ok {
		return
	}
	if list == nil {
		list = []proto.SessionInfo{}
	}
	s.broadcast(proto.Msg{T: proto.TSessionList, DeviceID: id, Sessions: list})
}

// ---- audit --------------------------------------------------------------

func (s *Server) audit(actor, deviceID string, sid uint32, action, detail string) {
	e := store.AuditEntry{At: s.now(), Actor: actor, DeviceID: deviceID, SID: sid, Action: action, Detail: detail}
	if err := s.store.AppendAudit(context.Background(), e); err != nil {
		s.log.Error("audit write failed", "action", action, "err", err)
	}
}

// ---- approvals ----------------------------------------------------------

// decideApproval applies a console decision: persists it, forwards
// approval.decide to the owning agent and tells every client. actor is
// "web:<client_id>" or "web:<login session>".
func (s *Server) decideApproval(key, decision, actor, deviceHint, reqID string) error {
	if decision != "allow" && decision != "deny" {
		return errBadRequest("decision must be allow or deny")
	}
	s.mu.Lock()
	a, ok := s.approvals[key]
	if !ok {
		// fall back to the store (e.g. relay restarted with a non-pending row)
		s.mu.Unlock()
		row, err := s.store.GetApproval(context.Background(), key)
		if err != nil {
			return errNotFound("approval not found")
		}
		s.mu.Lock()
		a = &row
		s.approvals[key] = a
	}
	if a.Status != proto.ApprovalPending {
		s.mu.Unlock()
		return errConflict("approval already " + a.Status)
	}
	deviceID := a.DeviceID
	if deviceID == "" {
		deviceID = deviceHint
	}
	d := s.devices[deviceID]
	var ac *agentConn
	if d != nil {
		ac = d.conn
	}
	if ac == nil {
		s.mu.Unlock()
		return errOffline("device offline")
	}
	now := s.now()
	status := proto.ApprovalAllowed
	if decision == "deny" {
		status = proto.ApprovalDenied
	}
	a.Status = status
	a.DecidedBy = actor
	a.DecidedAt = &now
	snapshot := *a
	s.mu.Unlock()

	if err := s.store.SetApprovalStatus(context.Background(), key, status, actor, now); err != nil {
		s.log.Error("persist approval decision", "key", key, "err", err)
	}
	ac.sendMsg(proto.Msg{
		T: proto.TApprovalDecide, ReqID: reqID, DeviceID: deviceID, ClientID: actor,
		SID: snapshot.SID, Key: key, Decision: decision, By: actor,
	})
	s.audit(actor, deviceID, snapshot.SID, "approval.decide", decision+" "+snapshot.Tool+" "+snapshot.Summary)
	s.broadcast(proto.Msg{
		T: proto.TApprovalClosed, DeviceID: deviceID, SID: snapshot.SID,
		Key: key, Decision: decision, By: actor, Approval: &snapshot,
	})
	return nil
}

// revokeDevice destroys the token, closes the agent connection and forgets
// the device. actor is the console actor for the audit log.
func (s *Server) revokeDevice(id, actor string) error {
	if err := s.store.RevokeDevice(context.Background(), id, s.now()); err != nil {
		return err
	}
	s.mu.Lock()
	d := s.devices[id]
	var ac *agentConn
	if d != nil {
		ac = d.conn
		for sid := range d.sessions {
			s.forgetSIDLocked(sid)
		}
		delete(s.devices, id)
	}
	s.mu.Unlock()
	if ac != nil {
		ac.closeNow(websocket.StatusPolicyViolation, "device revoked")
	}
	s.audit(actor, id, 0, "device.revoke", "")
	s.broadcastDeviceList()
	return nil
}

// touchLastSeen persists last_seen for a device (best effort).
func (s *Server) touchLastSeen(id string, at time.Time, osName, version string) {
	if err := s.store.TouchDevice(context.Background(), id, at, osName, version); err != nil {
		s.log.Error("touch device", "device_id", id, "err", err)
	}
}
