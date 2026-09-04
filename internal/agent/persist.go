package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/wfighter1/terminalX/internal/proto"
	"github.com/wfighter1/terminalX/internal/session"
)

// sessionMeta is what the agent has to remember about a session so a later
// agent process can take it back. It lives next to the session's generated
// hook settings, under <root>/sessions/<sid>/, and is removed when the
// session is closed for good.
//
// It holds no secrets: the preset name is stored, never the resolved
// environment, so the file is as safe as the session directory itself.
type sessionMeta struct {
	SID            uint32    `json:"sid"`
	Name           string    `json:"name,omitempty"`
	Tool           string    `json:"tool"`
	Shell          string    `json:"shell,omitempty"`
	Cwd            string    `json:"cwd,omitempty"`
	Preset         string    `json:"preset,omitempty"`
	ApprovalMode   string    `json:"approval_mode,omitempty"`
	PermissionMode string    `json:"permission_mode,omitempty"`
	Extra          []string  `json:"extra,omitempty"`
	SettingsPath   string    `json:"settings_path,omitempty"`
	CodexNotify    []string  `json:"codex_notify,omitempty"`
	ToolSessionID  string    `json:"tool_session_id,omitempty"`
	StartedAt      time.Time `json:"started_at"`
	Cols           uint16    `json:"cols,omitempty"`
	Rows           uint16    `json:"rows,omitempty"`
}

func (a *Agent) sessionDir(sid uint32) string {
	return filepath.Join(a.root, "sessions", strconv.FormatUint(uint64(sid), 10))
}

func (a *Agent) metaPath(sid uint32) string {
	return filepath.Join(a.sessionDir(sid), "meta.json")
}

// writeMeta persists a session descriptor. Failures are not fatal: they only
// cost the ability to re-adopt that session after a restart.
func (a *Agent) writeMeta(m sessionMeta) {
	dir := a.sessionDir(m.SID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		a.log.Warn("session meta: create dir", "sid", m.SID, "err", err)
		return
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		a.log.Warn("session meta: encode", "sid", m.SID, "err", err)
		return
	}
	tmp := a.metaPath(m.SID) + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		a.log.Warn("session meta: write", "sid", m.SID, "err", err)
		return
	}
	if err := os.Rename(tmp, a.metaPath(m.SID)); err != nil {
		a.log.Warn("session meta: rename", "sid", m.SID, "err", err)
	}
}

// readMeta loads one session descriptor.
func (a *Agent) readMeta(sid uint32) (sessionMeta, error) {
	var m sessionMeta
	b, err := os.ReadFile(a.metaPath(sid))
	if err != nil {
		return m, err
	}
	if err := json.Unmarshal(b, &m); err != nil {
		return m, fmt.Errorf("agent: parse %s: %w", a.metaPath(sid), err)
	}
	m.SID = sid
	return m, nil
}

// noteToolSession records the tool's own session id so a session adopted
// after a restart still knows what to pass to `--resume`.
func (a *Agent) noteToolSession(sid uint32, id string) {
	if id == "" || !a.persist {
		return
	}
	m, err := a.readMeta(sid)
	if err != nil || m.ToolSessionID == id {
		return
	}
	m.ToolSessionID = id
	a.writeMeta(m)
}

// adoptSessions takes back the sessions that outlived the previous agent
// process. tmux is the source of truth for what is alive; the meta files only
// say what each one was. A meta file with no tmux session is stale and its
// directory is removed.
func (a *Agent) adoptSessions() {
	if !a.persist {
		return
	}
	alive := map[uint32]bool{}
	for _, sid := range session.TmuxSessions(a.tmuxConf) {
		alive[sid] = true
	}
	entries, err := os.ReadDir(filepath.Join(a.root, "sessions"))
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		n, err := strconv.ParseUint(e.Name(), 10, 32)
		if err != nil {
			continue
		}
		sid := uint32(n)
		if !alive[sid] {
			_ = os.RemoveAll(a.sessionDir(sid))
			continue
		}
		m, err := a.readMeta(sid)
		if err != nil {
			a.log.Warn("adopt: unreadable session meta, leaving the tmux session alone",
				"sid", sid, "err", err)
			continue
		}
		if err := a.adopt(m); err != nil {
			a.log.Warn("adopt failed", "sid", sid, "err", err)
		}
	}
}

// adopt re-attaches one session described by its meta file.
func (a *Agent) adopt(m sessionMeta) error {
	presetEnv, err := a.resolver.Resolve(m.Preset)
	if err != nil {
		// The preset may have been removed from the config since; the tool is
		// already running with the old environment, so this only affects a
		// later kill_resume.
		a.log.Warn("adopt: preset unavailable", "sid", m.SID, "preset", m.Preset, "err", err)
		presetEnv = nil
	}
	mode := proto.ApprovalMode(m.ApprovalMode)
	if mode == "" {
		mode = proto.ApprovalNotify
	}
	sid := m.SID
	s, err := session.Adopt(session.Options{
		SID:   sid,
		Shell: m.Shell,
		Cwd:   m.Cwd,
		Tool: session.ToolSpec{
			Tool:           m.Tool,
			Name:           m.Name,
			PermissionMode: m.PermissionMode,
			Extra:          m.Extra,
			SettingsPath:   m.SettingsPath,
			CodexNotify:    m.CodexNotify,
		},
		Name:            m.Name,
		Preset:          m.Preset,
		ApprovalMode:    mode,
		Env:             append([]string{"TX_HOOK_TOKEN=" + a.cfg.HookToken}, presetEnv...),
		Cols:            m.Cols,
		Rows:            m.Rows,
		Persist:         true,
		PersistConf:     a.tmuxConf,
		PersistStateDir: a.sessionDir(sid),
		Log:             a.log,
		OnOutput:        func(seq uint64, data []byte) { a.onOutput(sid, seq, data) },
		OnExit:          func(code int32, r *proto.Resumable) { a.onExit(sid, code, r) },
	})
	if err != nil {
		return err
	}
	if m.ToolSessionID != "" {
		s.SetToolSession(m.ToolSessionID)
	}
	a.mu.Lock()
	a.sessions[sid] = s
	a.mu.Unlock()
	a.log.Info("session adopted after restart", "sid", sid, "tool", m.Tool, "name", m.Name)
	return nil
}

// suspendAllSessions releases the PTYs but leaves persisted sessions running
// in tmux, so the next agent process adopts them. Sessions without a
// persistence backend are closed, because nothing would hold them.
func (a *Agent) suspendAllSessions() {
	a.mu.Lock()
	ss := make([]*session.Session, 0, len(a.sessions))
	for _, s := range a.sessions {
		ss = append(ss, s)
	}
	a.sessions = map[uint32]*session.Session{}
	a.mu.Unlock()
	kept := 0
	for _, s := range ss {
		if s.Persisted() {
			kept++
		}
		s.Suspend()
	}
	if kept > 0 {
		a.log.Info("sessions left running in tmux for the next agent", "count", kept)
	}
}
