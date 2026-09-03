// Package hooks runs the 127.0.0.1 HTTP endpoint that receives structured
// signals from AI CLIs (Claude http hooks, statusLine POSTs, Codex notify)
// and turns them into session state changes and approval items.
package hooks

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/wfighter1/terminalX/internal/proto"
)

// Events receives the normalised signals. All methods are called from HTTP
// handler goroutines and must be safe for concurrent use.
type Events interface {
	// SessionState reports a state transition with its provenance.
	SessionState(sid uint32, st proto.SessionState, kind proto.NeedKind, src proto.Source, conf proto.Confidence)
	// ApprovalNew announces a new pending approval.
	ApprovalNew(a proto.Approval)
	// ApprovalClosed announces that an approval reached a terminal status.
	ApprovalClosed(a proto.Approval, by string)
	// SessionUpdated carries statusLine metrics.
	SessionUpdated(sid uint32, costUSD, contextPct *float64)
	// ToolSession records the tool's own session id (Claude session_id,
	// Codex thread-id) for later resume.
	ToolSession(sid uint32, agent, toolSessionID string)
	// ToolExited marks that the tool process left the shell (SessionEnd).
	ToolExited(sid uint32)
}

// SessionLookup answers whether a sid exists and which approval mode and
// tool it currently has.
type SessionLookup func(sid uint32) (tool string, mode proto.ApprovalMode, ok bool)

// Server is the local hooks endpoint.
type Server struct {
	Token  string
	Events Events
	Lookup SessionLookup
	Store  *Store
	Log    *slog.Logger
	// RemoteFirstTimeout bounds how long a remote_first PermissionRequest
	// blocks waiting for approval.decide. Zero means 3600 s.
	RemoteFirstTimeout time.Duration

	mu   sync.Mutex
	ln   net.Listener
	srv  *http.Server
	port int
}

// Handler returns the routed http.Handler (exposed for httptest).
func (s *Server) Handler() http.Handler {
	if s.Store == nil {
		s.Store = NewStore()
	}
	if s.Log == nil {
		s.Log = slog.Default()
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /hook/claude/{sid}/{event}", s.auth(s.handleClaude))
	mux.HandleFunc("POST /statusline/{sid}", s.auth(s.handleStatusLine))
	mux.HandleFunc("POST /hook/codex/{sid}/notify", s.auth(s.handleCodexNotify))
	mux.HandleFunc("POST /hook/codex/{sid}/{event}", s.auth(s.handleCodexHook))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, "ok\n") })
	return mux
}

// Listen binds 127.0.0.1:port (0 = ephemeral) and returns the actual port.
func (s *Server) Listen(port int) (int, error) {
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return 0, fmt.Errorf("hooks: listen 127.0.0.1:%d: %w", port, err)
	}
	s.mu.Lock()
	s.ln = ln
	s.port = ln.Addr().(*net.TCPAddr).Port
	s.srv = &http.Server{Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	s.mu.Unlock()
	return s.port, nil
}

// Port returns the bound port (0 before Listen).
func (s *Server) Port() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.port
}

// Serve blocks serving HTTP until Shutdown.
func (s *Server) Serve() error {
	s.mu.Lock()
	srv, ln := s.srv, s.ln
	s.mu.Unlock()
	if srv == nil {
		return errors.New("hooks: Serve called before Listen")
	}
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("hooks: serve: %w", err)
	}
	return nil
}

// Shutdown stops the server.
func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	srv := s.srv
	s.mu.Unlock()
	if srv == nil {
		return nil
	}
	return srv.Shutdown(ctx)
}

func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if s.Token == "" || subtle.ConstantTimeCompare([]byte(got), []byte(s.Token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func (s *Server) sessionFromPath(w http.ResponseWriter, r *http.Request) (uint32, string, proto.ApprovalMode, bool) {
	sid64, err := strconv.ParseUint(r.PathValue("sid"), 10, 32)
	if err != nil {
		http.Error(w, "bad sid", http.StatusBadRequest)
		return 0, "", "", false
	}
	sid := uint32(sid64)
	if s.Lookup == nil {
		return sid, "", proto.ApprovalNotify, true
	}
	tool, mode, ok := s.Lookup(sid)
	if !ok {
		http.Error(w, "unknown session", http.StatusNotFound)
		return 0, "", "", false
	}
	if mode == "" {
		mode = proto.ApprovalNotify
	}
	return sid, tool, mode, true
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

const maxBody = 4 << 20

func (s *Server) handleClaude(w http.ResponseWriter, r *http.Request) {
	sid, _, mode, ok := s.sessionFromPath(w, r)
	if !ok {
		return
	}
	event := r.PathValue("event")
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	var in ClaudeInput
	if len(body) > 0 {
		if err := json.Unmarshal(body, &in); err != nil {
			s.Log.Warn("claude hook: bad json", "sid", sid, "event", event, "err", err)
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
	}
	if in.HookEventName == "" {
		in.HookEventName = event
	}
	log := s.Log.With("sid", sid, "event", in.HookEventName)
	ev := s.Events
	switch in.HookEventName {
	case "SessionStart":
		if in.SessionID != "" {
			ev.ToolSession(sid, proto.ToolClaude, in.SessionID)
		}
		ev.SessionState(sid, proto.StateRunning, "", proto.SourceHook, proto.ConfidenceHigh)
		writeJSON(w, map[string]any{})
	case "UserPromptSubmit":
		ev.SessionState(sid, proto.StateRunning, "", proto.SourceHook, proto.ConfidenceHigh)
		writeJSON(w, map[string]any{})
	case "PermissionRequest":
		s.permissionRequest(w, r, sid, mode, in)
	case "PostToolUse":
		key := ApprovalKey(in.SessionID, in.ToolName, in.ToolInput)
		if a, ok := s.Store.Close(key, proto.ApprovalClosedLocal, "local"); ok {
			ev.ApprovalClosed(a, "local")
		}
		ev.SessionState(sid, proto.StateRunning, "", proto.SourceHook, proto.ConfidenceHigh)
		writeJSON(w, map[string]any{})
	case "Notification":
		s.notification(sid, mode, in)
		writeJSON(w, map[string]any{})
	case "Stop":
		ev.SessionState(sid, proto.StateIdle, "", proto.SourceHook, proto.ConfidenceHigh)
		writeJSON(w, map[string]any{})
	case "StopFailure":
		log.Warn("claude stop failure", "error_type", in.ErrorType, "message", in.ErrorMessage)
		ev.SessionState(sid, proto.StateFailed, "", proto.SourceHook, proto.ConfidenceHigh)
		writeJSON(w, map[string]any{})
	case "SessionEnd":
		for _, a := range s.Store.CloseSession(sid, proto.ApprovalClosedLocal, "local") {
			ev.ApprovalClosed(a, "local")
		}
		ev.ToolExited(sid)
		ev.SessionState(sid, proto.StateIdle, "", proto.SourceHook, proto.ConfidenceHigh)
		writeJSON(w, map[string]any{})
	default:
		log.Debug("claude hook: unhandled event")
		writeJSON(w, map[string]any{})
	}
}

func (s *Server) permissionRequest(w http.ResponseWriter, r *http.Request, sid uint32, mode proto.ApprovalMode, in ClaudeInput) {
	ev := s.Events
	level := proto.LevelKeys
	if mode == proto.ApprovalRemoteFirst {
		level = proto.LevelHook
	}
	now := time.Now()
	a := proto.Approval{
		Key:       ApprovalKey(in.SessionID, in.ToolName, in.ToolInput),
		SID:       sid,
		Agent:     proto.ToolClaude,
		Tool:      in.ToolName,
		Summary:   Summary(in.ToolName, in.ToolInput),
		Input:     in.ToolInput,
		Cwd:       in.Cwd,
		Level:     level,
		Mode:      mode,
		CreatedAt: now,
	}
	timeout := s.RemoteFirstTimeout
	if timeout <= 0 {
		timeout = 3600 * time.Second
	}
	if level == proto.LevelHook {
		t := now.Add(timeout)
		a.HookTimeoutAt = &t
	}
	stored, dup := s.Store.Add(a)
	ev.SessionState(sid, proto.StateNeedsInput, proto.NeedPermission, proto.SourceHook, proto.ConfidenceHigh)
	if !dup {
		ev.ApprovalNew(stored)
	}
	if level != proto.LevelHook {
		// notify mode: register + push, return no decision so the local
		// dialog appears as usual.
		writeJSON(w, map[string]any{})
		return
	}
	ch, ok := s.Store.wait(stored.Key)
	if !ok {
		writeJSON(w, map[string]any{})
		return
	}
	select {
	case d := <-ch:
		if d.Decision == "allow" || d.Decision == "deny" {
			s.Log.Info("permission decided remotely", "sid", sid, "key", stored.Key, "decision", d.Decision, "by", d.By)
			writeJSON(w, NewPermissionDecision(d.Decision, "decided via terminalX by "+d.By))
			return
		}
		// Closed without a decision (closed_local / session end): no verdict.
		writeJSON(w, map[string]any{})
	case <-time.After(timeout):
		if a, ok := s.Store.Close(stored.Key, proto.ApprovalFallback, "timeout"); ok {
			ev.ApprovalClosed(a, "timeout")
		}
		s.Log.Warn("permission request timed out, falling back to local dialog", "sid", sid, "key", stored.Key)
		writeJSON(w, map[string]any{})
	case <-r.Context().Done():
		// Claude gave up on the hook (its own timeout) → local dialog.
		if a, ok := s.Store.Close(stored.Key, proto.ApprovalFallback, "timeout"); ok {
			ev.ApprovalClosed(a, "timeout")
		}
	}
}

func (s *Server) notification(sid uint32, mode proto.ApprovalMode, in ClaudeInput) {
	ev := s.Events
	switch in.NotificationType {
	case "permission_prompt":
		ev.SessionState(sid, proto.StateNeedsInput, proto.NeedPermission, proto.SourceHook, proto.ConfidenceHigh)
	case "idle_prompt":
		ev.SessionState(sid, proto.StateIdle, "", proto.SourceHook, proto.ConfidenceHigh)
	case "agent_needs_input", "elicitation_dialog", "elicitation_url_dialog":
		ev.SessionState(sid, proto.StateNeedsInput, proto.NeedQuestion, proto.SourceHook, proto.ConfidenceHigh)
		summary := in.Message
		if summary == "" {
			summary = in.Title
		}
		if summary == "" {
			summary = in.NotificationType
		}
		a := proto.Approval{
			Key:       ApprovalKey(in.SessionID, "notification:"+in.NotificationType, json.RawMessage(strconv.Quote(summary))),
			SID:       sid,
			Agent:     proto.ToolClaude,
			Tool:      in.NotificationType,
			Summary:   summary,
			Cwd:       in.Cwd,
			Level:     proto.LevelKeys,
			Mode:      mode,
			CreatedAt: time.Now(),
		}
		if stored, dup := s.Store.Add(a); !dup {
			ev.ApprovalNew(stored)
		}
	case "quota_auto_resume_fired", "quota_auto_resume_stale", "quota_auto_resume_disabled":
		ev.SessionState(sid, proto.StateQuotaWait, "", proto.SourceHook, proto.ConfidenceHigh)
	default:
		s.Log.Debug("claude notification ignored", "sid", sid, "type", in.NotificationType)
	}
}

func (s *Server) handleStatusLine(w http.ResponseWriter, r *http.Request) {
	sid, _, _, ok := s.sessionFromPath(w, r)
	if !ok {
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	var in StatusLineInput
	if err := json.Unmarshal(body, &in); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	s.Events.SessionUpdated(sid, in.Cost.TotalCostUSD, in.ContextWindow.UsedPercentage)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleCodexNotify(w http.ResponseWriter, r *http.Request) {
	sid, _, _, ok := s.sessionFromPath(w, r)
	if !ok {
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	var n CodexNotify
	if err := json.Unmarshal(body, &n); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	if n.ThreadID != "" {
		s.Events.ToolSession(sid, proto.ToolCodex, n.ThreadID)
	}
	switch n.Type {
	case "agent-turn-complete", "":
		s.Events.SessionState(sid, proto.StateIdle, "", proto.SourceNotify, proto.ConfidenceHigh)
	default:
		s.Log.Debug("codex notify ignored", "sid", sid, "type", n.Type)
	}
	writeJSON(w, map[string]any{})
}

// handleCodexHook accepts Codex hooks.json command-hook payloads forwarded by
// `tx-agent hook` (Claude-compatible input shape: session_id, cwd,
// hook_event_name, tool_name, tool_input). Only registration (level B) is
// done; no decision is returned.
func (s *Server) handleCodexHook(w http.ResponseWriter, r *http.Request) {
	sid, _, mode, ok := s.sessionFromPath(w, r)
	if !ok {
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBody))
	if err != nil {
		http.Error(w, "read body", http.StatusBadRequest)
		return
	}
	var in ClaudeInput
	if len(body) > 0 {
		if err := json.Unmarshal(body, &in); err != nil {
			http.Error(w, "bad json", http.StatusBadRequest)
			return
		}
	}
	if in.HookEventName == "" {
		in.HookEventName = r.PathValue("event")
	}
	ev := s.Events
	switch in.HookEventName {
	case "SessionStart":
		if in.SessionID != "" {
			ev.ToolSession(sid, proto.ToolCodex, in.SessionID)
		}
		ev.SessionState(sid, proto.StateRunning, "", proto.SourceHooksJSON, proto.ConfidenceHigh)
	case "UserPromptSubmit", "PreToolUse":
		ev.SessionState(sid, proto.StateRunning, "", proto.SourceHooksJSON, proto.ConfidenceHigh)
	case "PermissionRequest":
		a := proto.Approval{
			Key:       ApprovalKey(in.SessionID, in.ToolName, in.ToolInput),
			SID:       sid,
			Agent:     proto.ToolCodex,
			Tool:      in.ToolName,
			Summary:   Summary(in.ToolName, in.ToolInput),
			Input:     in.ToolInput,
			Cwd:       in.Cwd,
			Level:     proto.LevelKeys,
			Mode:      mode,
			CreatedAt: time.Now(),
		}
		ev.SessionState(sid, proto.StateNeedsInput, proto.NeedPermission, proto.SourceHooksJSON, proto.ConfidenceHigh)
		if stored, dup := s.Store.Add(a); !dup {
			ev.ApprovalNew(stored)
		}
	case "PostToolUse":
		key := ApprovalKey(in.SessionID, in.ToolName, in.ToolInput)
		if a, ok := s.Store.Close(key, proto.ApprovalClosedLocal, "local"); ok {
			ev.ApprovalClosed(a, "local")
		}
		ev.SessionState(sid, proto.StateRunning, "", proto.SourceHooksJSON, proto.ConfidenceHigh)
	case "Stop":
		ev.SessionState(sid, proto.StateIdle, "", proto.SourceHooksJSON, proto.ConfidenceHigh)
	case "SessionEnd":
		for _, a := range s.Store.CloseSession(sid, proto.ApprovalClosedLocal, "local") {
			ev.ApprovalClosed(a, "local")
		}
		ev.ToolExited(sid)
		ev.SessionState(sid, proto.StateIdle, "", proto.SourceHooksJSON, proto.ConfidenceHigh)
	}
	writeJSON(w, map[string]any{})
}
