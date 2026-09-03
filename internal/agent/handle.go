package agent

import (
	"time"

	"github.com/wfighter1/terminalX/internal/proto"
)

// handleMsg dispatches one control message from the relay.
func (a *Agent) handleMsg(m proto.Msg) {
	switch m.T {
	case proto.TAck:
		a.mu.Lock()
		if m.ReqID != "" && m.ReqID == a.helloID {
			a.helloOK = true
		}
		a.mu.Unlock()
	case proto.THeartbeat:
		a.onHeartbeatEcho(m.Heartbeat)
	case proto.TError:
		a.log.Warn("relay error", "req_id", m.ReqID, "sid", m.SID, "error", m.Error)
	case proto.TSessionOpen:
		a.handleOpen(m)
	case proto.TSessionAttach:
		a.handleAttach(m)
	case proto.TSessionDetach:
		if s := a.session(m.SID); s != nil {
			s.Detach()
		}
	case proto.TSessionResize:
		s := a.session(m.SID)
		if s == nil {
			a.replyError(m, "unknown session")
			return
		}
		if err := s.Resize(m.Cols, m.Rows); err != nil {
			a.replyError(m, err.Error())
			return
		}
		a.replyAck(m)
	case proto.TSessionSignal:
		s := a.session(m.SID)
		if s == nil {
			a.replyError(m, "unknown session")
			return
		}
		sig := m.Sig
		// kill_resume may take several seconds; keep the read loop free.
		go func() {
			if err := s.Signal(sig); err != nil {
				a.log.Warn("signal failed", "sid", m.SID, "sig", sig, "err", err)
				a.replyError(m, err.Error())
				return
			}
			if sig == proto.SigKillResume {
				info := s.Info()
				a.sendMsg(proto.Msg{T: proto.TSessionUpdated, SID: m.SID, Session: &info})
			}
			a.replyAck(m)
		}()
	case proto.TSessionClose:
		if !a.closeSession(m.SID) {
			a.replyError(m, "unknown session")
			return
		}
		a.sendMsg(proto.Msg{T: proto.TSessionClosed, ReqID: m.ReqID, ClientID: m.ClientID, SID: m.SID})
	case proto.TSessionSetMode:
		s := a.session(m.SID)
		if s == nil {
			a.replyError(m, "unknown session")
			return
		}
		if m.Mode != proto.ApprovalNotify && m.Mode != proto.ApprovalRemoteFirst {
			a.replyError(m, "mode must be notify or remote_first")
			return
		}
		info := s.SetApprovalMode(m.Mode)
		a.sendMsg(proto.Msg{T: proto.TSessionUpdated, SID: m.SID, Session: &info})
		a.replyAck(m)
	case proto.TApprovalDecide:
		by := m.By
		if by == "" {
			by = m.ClientID
		}
		if by == "" {
			by = "relay"
		}
		if _, ok := a.store.Decide(m.Key, m.Decision, by); !ok {
			a.log.Warn("approval.decide for unknown or settled key", "key", m.Key)
			a.replyError(m, "approval not pending")
			return
		}
		a.log.Info("approval decided", "key", m.Key, "decision", m.Decision, "by", by)
	default:
		a.log.Debug("ignoring control message", "t", m.T)
	}
}

func (a *Agent) replyAck(m proto.Msg) {
	if m.ReqID == "" && m.ClientID == "" {
		return
	}
	a.sendMsg(proto.Msg{T: proto.TAck, ReqID: m.ReqID, ClientID: m.ClientID, SID: m.SID})
}

func (a *Agent) replyError(m proto.Msg, msg string) {
	a.sendMsg(proto.Msg{T: proto.TError, ReqID: m.ReqID, ClientID: m.ClientID, SID: m.SID, Error: msg})
}

func (a *Agent) handleOpen(m proto.Msg) {
	if m.Open == nil {
		a.replyError(m, "session.open without open")
		return
	}
	s, err := a.openSession(*m.Open)
	if err != nil {
		a.log.Warn("session.open failed", "err", err)
		a.replyError(m, err.Error())
		return
	}
	info := s.Info()
	a.sendMsg(proto.Msg{T: proto.TSessionOpened, ReqID: m.ReqID, ClientID: m.ClientID, SID: info.SID, Session: &info})
}

// handleAttach replays output for a (re)attaching client. The relay marks
// the client "pending" until a Snapshot frame without FlagMore arrives and
// forwards Snapshot frames only to pending clients, so both the delta and
// the full-tail paths are sent as FrameSnapshot: the delta without
// FlagReset (the terminal keeps its state), the tail with FlagReset.
// Sending the delta as FrameOutput would duplicate it onto every other
// attached client.
func (a *Agent) handleAttach(m proto.Msg) {
	s := a.session(m.SID)
	if s == nil {
		a.replyError(m, "unknown session")
		return
	}
	s.Attach()
	s.Replay(m.LastSeq, func(data []byte, endSeq uint64, delta bool) {
		var extra uint8
		if !delta {
			extra = proto.FlagReset
		}
		for _, f := range proto.Chunk(proto.FrameSnapshot, m.SID, endSeq, data, extra) {
			a.sendFrame(f)
		}
	})
	a.log.Debug("client attached", "sid", m.SID, "client", m.ClientID, "last_seq", m.LastSeq)
	if s.Tool() != proto.ToolShell && s.Alive() {
		// Nudge alt-screen TUIs into a full repaint (tmux trick).
		go func() {
			cols, rows := s.Size()
			if err := s.Resize(cols+1, rows); err != nil {
				return
			}
			time.Sleep(60 * time.Millisecond)
			_ = s.Resize(cols, rows)
		}()
	}
}

// handleFrame dispatches one data frame from the relay.
func (a *Agent) handleFrame(f proto.Frame) {
	switch f.Type {
	case proto.FrameInput:
		s := a.session(f.SID)
		if s == nil {
			return
		}
		if err := s.Write(f.Payload); err != nil {
			a.log.Debug("input dropped", "sid", f.SID, "err", err)
		}
	case proto.FrameResize:
		s := a.session(f.SID)
		if s == nil {
			return
		}
		cols, rows, err := proto.ParseResize(f.Payload)
		if err != nil {
			a.log.Warn("bad resize payload", "sid", f.SID, "err", err)
			return
		}
		if err := s.Resize(cols, rows); err != nil {
			a.log.Debug("resize dropped", "sid", f.SID, "err", err)
		}
	case proto.FrameAck:
		a.mu.Lock()
		if f.Seq > a.acks[f.SID] {
			a.acks[f.SID] = f.Seq
		}
		a.mu.Unlock()
	default:
		a.log.Debug("ignoring frame from relay", "type", f.Type, "sid", f.SID)
	}
}
