package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/wfighter1/terminalX/internal/hooks"
	"github.com/wfighter1/terminalX/internal/presets"
	"github.com/wfighter1/terminalX/internal/proto"
	"github.com/wfighter1/terminalX/internal/session"
)

// Version is stamped by -ldflags "-X …/internal/agent.Version=…".
var Version = "0.1.0-dev"

// Capabilities advertised in agent.hello.
var Capabilities = []string{"pty", "hooks.http"}

const (
	heartbeatInterval  = 15 * time.Second
	heuristicInterval  = 15 * time.Second
	remoteFirstTimeout = 3600
	notifyTimeout      = 10
)

// Agent is the controlled-side daemon.
type Agent struct {
	cfg     *Config
	cfgPath string
	root    string // directory holding agent.json; sessions/ lives under it
	exe     string
	log     *slog.Logger

	hooks    *hooks.Server
	store    *hooks.Store
	resolver presets.Resolver

	mu       sync.Mutex
	sessions map[uint32]*session.Session
	ptyKeys  map[uint32]string // sid → active level-C approval key
	acks     map[uint32]uint64 // sid → last acked seq (backpressure bookkeeping)
	conn     *wsConn
	hbSeq    uint64
	rtt      time.Duration
	lastEcho time.Time
	helloOK  bool
	helloID  string
}

// New builds an Agent from a config. cfgPath is where HooksPort is
// persisted after the first Listen.
func New(cfg *Config, cfgPath string, log *slog.Logger) (*Agent, error) {
	if log == nil {
		log = slog.Default()
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("agent: %w", err)
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("agent: executable path: %w", err)
	}
	if abs, err := filepath.Abs(cfgPath); err == nil {
		cfgPath = abs
	}
	a := &Agent{
		cfg:      cfg,
		cfgPath:  cfgPath,
		root:     filepath.Dir(cfgPath),
		exe:      exe,
		log:      log,
		store:    hooks.NewStore(),
		resolver: cfg.Resolver(),
		sessions: map[uint32]*session.Session{},
		ptyKeys:  map[uint32]string{},
		acks:     map[uint32]uint64{},
	}
	a.hooks = &hooks.Server{
		Token:              cfg.HookToken,
		Events:             a,
		Lookup:             a.lookup,
		Store:              a.store,
		Log:                log.With("component", "hooks"),
		RemoteFirstTimeout: remoteFirstTimeout * time.Second,
	}
	return a, nil
}

// Run starts the hooks endpoint and the relay connection loop and blocks
// until ctx is cancelled. Sessions are terminated on return.
func (a *Agent) Run(ctx context.Context) error {
	if changed, err := a.cfg.EnsureIdentity(); err != nil {
		return err
	} else if changed {
		a.hooks.Token = a.cfg.HookToken
		if err := a.cfg.Save(a.cfgPath); err != nil {
			return err
		}
	}
	port, err := a.hooks.Listen(a.cfg.HooksPort)
	if err != nil && a.cfg.HooksPort != 0 {
		a.log.Warn("configured hooks port unavailable, picking a random one", "port", a.cfg.HooksPort, "err", err)
		port, err = a.hooks.Listen(0)
	}
	if err != nil {
		return err
	}
	if port != a.cfg.HooksPort {
		a.cfg.HooksPort = port
		if err := a.cfg.Save(a.cfgPath); err != nil {
			return err
		}
	}
	a.log.Info("hooks endpoint listening", "addr", "127.0.0.1:"+strconv.Itoa(port))
	serveErr := make(chan error, 1)
	go func() { serveErr <- a.hooks.Serve() }()

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		a.tickers(ctx)
	}()

	loopErr := make(chan error, 1)
	go func() { loopErr <- a.connectLoop(ctx) }()

	var runErr error
	select {
	case <-ctx.Done():
	case err := <-serveErr:
		runErr = fmt.Errorf("hooks server stopped: %w", err)
	case err := <-loopErr:
		runErr = err
	}
	a.log.Info("agent shutting down")
	sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = a.hooks.Shutdown(sctx)
	a.closeConn()
	wg.Wait()
	a.closeAllSessions()
	if runErr != nil && !errors.Is(runErr, context.Canceled) {
		return runErr
	}
	return nil
}

// RTT returns the last measured relay round-trip time.
func (a *Agent) RTT() time.Duration {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.rtt
}

// HooksPort returns the bound hooks port (0 before Run).
func (a *Agent) HooksPort() int { return a.hooks.Port() }

// ---- sessions -----------------------------------------------------------

func (a *Agent) session(sid uint32) *session.Session {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sessions[sid]
}

// sessionList returns the SessionInfo of every session, sorted by sid.
func (a *Agent) sessionList() []proto.SessionInfo {
	a.mu.Lock()
	ss := make([]*session.Session, 0, len(a.sessions))
	for _, s := range a.sessions {
		ss = append(ss, s)
	}
	a.mu.Unlock()
	out := make([]proto.SessionInfo, 0, len(ss))
	for _, s := range ss {
		out = append(out, s.Info())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SID < out[j].SID })
	return out
}

func (a *Agent) sessionHealth() []proto.SessionHealth {
	a.mu.Lock()
	ss := make([]*session.Session, 0, len(a.sessions))
	for _, s := range a.sessions {
		ss = append(ss, s)
	}
	a.mu.Unlock()
	out := make([]proto.SessionHealth, 0, len(ss))
	for _, s := range ss {
		out = append(out, s.Health())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SID < out[j].SID })
	return out
}

// lookup implements hooks.SessionLookup.
func (a *Agent) lookup(sid uint32) (string, proto.ApprovalMode, bool) {
	s := a.session(sid)
	if s == nil {
		return "", "", false
	}
	return s.Tool(), s.ApprovalMode(), true
}

// openSession builds the tool spec / environment and starts a session.
func (a *Agent) openSession(o proto.OpenRequest) (*session.Session, error) {
	tool := o.Tool
	if tool == "" {
		tool = proto.ToolShell
	}
	switch tool {
	case proto.ToolShell, proto.ToolClaude, proto.ToolCodex, proto.ToolGrok:
	default:
		return nil, fmt.Errorf("unknown tool %q", tool)
	}
	mode := o.ApprovalMode
	if mode == "" {
		mode = proto.ApprovalNotify
	}
	presetEnv, err := a.resolver.Resolve(o.Preset)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	sid := session.NewSID()
	for a.sessions[sid] != nil {
		sid = session.NewSID()
	}
	a.mu.Unlock()

	env := append([]string{"TX_HOOK_TOKEN=" + a.cfg.HookToken}, presetEnv...)
	spec := session.ToolSpec{
		Tool:           tool,
		Name:           o.Name,
		PermissionMode: o.PermissionMode,
		Resume:         o.Resume,
		Extra:          o.Extra,
	}
	port := a.hooks.Port()
	switch tool {
	case proto.ToolClaude:
		env = append(env, "CLAUDE_CODE_NO_FLICKER=1", "CLAUDE_CODE_ALT_SCREEN_FULL_REPAINT=1")
		path, err := hooks.WriteClaudeSettings(a.root, hooks.ClaudeSettingsOptions{
			SID: sid, Port: port, Token: a.cfg.HookToken, Mode: mode,
			RemoteFirstTimeout: remoteFirstTimeout, NotifyTimeout: notifyTimeout,
		})
		if err != nil {
			return nil, fmt.Errorf("claude settings: %w", err)
		}
		spec.SettingsPath = path
	case proto.ToolCodex:
		spec.CodexNotify = []string{a.exe, "notify", "--sid", strconv.FormatUint(uint64(sid), 10),
			"--port", strconv.Itoa(port), "--token", a.cfg.HookToken}
	}
	shell := o.Shell
	if shell == "" {
		shell = a.cfg.DefaultShell
	}
	s, err := session.Start(session.Options{
		SID:          sid,
		Shell:        shell,
		Cwd:          o.Cwd,
		Tool:         spec,
		Name:         o.Name,
		Preset:       o.Preset,
		ApprovalMode: mode,
		Env:          env,
		Cols:         o.Cols,
		Rows:         o.Rows,
		Log:          a.log,
		OnOutput:     func(seq uint64, data []byte) { a.onOutput(sid, seq, data) },
		OnExit:       func(code int32, r *proto.Resumable) { a.onExit(sid, code, r) },
	})
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	a.sessions[sid] = s
	a.mu.Unlock()
	a.log.Info("session opened", "sid", sid, "tool", tool, "shell", s.Info().Shell, "cwd", s.Info().Cwd, "preset", o.Preset)
	return s, nil
}

func (a *Agent) onOutput(sid uint32, seq uint64, data []byte) {
	for _, f := range proto.Chunk(proto.FrameOutput, sid, seq, data, 0) {
		a.sendFrame(f)
	}
}

func (a *Agent) onExit(sid uint32, code int32, r *proto.Resumable) {
	a.sendFrame(proto.Frame{Type: proto.FrameEOF, SID: sid, Payload: proto.EOFPayload(code)})
	c := code
	a.sendMsg(proto.Msg{T: proto.TSessionExited, SID: sid, Code: &c, Resumable: r})
	for _, ap := range a.store.CloseSession(sid, proto.ApprovalClosedLocal, "exit") {
		a.ApprovalClosed(ap, "exit")
	}
}

// closeSession terminates a session and forgets it.
func (a *Agent) closeSession(sid uint32) bool {
	a.mu.Lock()
	s := a.sessions[sid]
	delete(a.sessions, sid)
	delete(a.ptyKeys, sid)
	delete(a.acks, sid)
	a.mu.Unlock()
	if s == nil {
		return false
	}
	for _, ap := range a.store.CloseSession(sid, proto.ApprovalClosedLocal, "close") {
		a.ApprovalClosed(ap, "close")
	}
	s.Close()
	_ = os.RemoveAll(filepath.Join(a.root, "sessions", strconv.FormatUint(uint64(sid), 10)))
	a.log.Info("session closed", "sid", sid)
	return true
}

func (a *Agent) closeAllSessions() {
	a.mu.Lock()
	sids := make([]uint32, 0, len(a.sessions))
	for sid := range a.sessions {
		sids = append(sids, sid)
	}
	a.mu.Unlock()
	for _, sid := range sids {
		a.closeSession(sid)
	}
}

// ---- hooks.Events -------------------------------------------------------

// SessionState implements hooks.Events.
func (a *Agent) SessionState(sid uint32, st proto.SessionState, kind proto.NeedKind, src proto.Source, conf proto.Confidence) {
	s := a.session(sid)
	if s == nil {
		return
	}
	if !s.SetState(st, kind, src, conf, time.Now()) {
		return // exited sessions keep their state; do not resurrect them in the console
	}
	a.sendMsg(proto.Msg{T: proto.TSessionState, SID: sid, State: st, Kind: kind, Source: src, Confidence: conf})
}

// ApprovalNew implements hooks.Events.
func (a *Agent) ApprovalNew(ap proto.Approval) {
	ap.DeviceID = a.cfg.DeviceID
	a.sendMsg(proto.Msg{T: proto.TApprovalNew, SID: ap.SID, Approval: &ap})
}

// ApprovalClosed implements hooks.Events.
func (a *Agent) ApprovalClosed(ap proto.Approval, by string) {
	ap.DeviceID = a.cfg.DeviceID
	m := proto.Msg{T: proto.TApprovalClosed, SID: ap.SID, Key: ap.Key, By: by, Approval: &ap}
	if ap.Status == proto.ApprovalFallback {
		m.Decision = "fallback"
	}
	a.sendMsg(m)
}

// SessionUpdated implements hooks.Events (statusLine metrics).
func (a *Agent) SessionUpdated(sid uint32, costUSD, contextPct *float64) {
	s := a.session(sid)
	if s == nil {
		return
	}
	info := s.SetMetrics(costUSD, contextPct, time.Now())
	a.sendMsg(proto.Msg{T: proto.TSessionUpdated, SID: sid, Session: &info})
}

// ToolSession implements hooks.Events.
func (a *Agent) ToolSession(sid uint32, agent, toolSessionID string) {
	if s := a.session(sid); s != nil {
		s.SetToolSession(toolSessionID)
		a.log.Debug("tool session recorded", "sid", sid, "agent", agent, "id", toolSessionID)
	}
}

// ToolExited implements hooks.Events.
func (a *Agent) ToolExited(sid uint32) {
	if s := a.session(sid); s != nil {
		s.ToolExited()
	}
}

// ---- periodic work ------------------------------------------------------

func (a *Agent) tickers(ctx context.Context) {
	hb := time.NewTicker(heartbeatInterval)
	heur := time.NewTicker(heuristicInterval)
	sweep := time.NewTicker(10 * time.Minute)
	defer hb.Stop()
	defer heur.Stop()
	defer sweep.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-hb.C:
			a.sendHeartbeat()
		case <-heur.C:
			a.runHeuristics(time.Now())
		case <-sweep.C:
			a.store.Sweep(24 * time.Hour)
		}
	}
}

func (a *Agent) sendHeartbeat() {
	a.mu.Lock()
	a.hbSeq++
	seq := a.hbSeq
	a.mu.Unlock()
	a.sendMsg(proto.Msg{T: proto.THeartbeat, Heartbeat: &proto.Heartbeat{
		Seq: seq, SentAt: time.Now(), Power: "unknown", Sessions: a.sessionHealth(),
	}})
}

func (a *Agent) onHeartbeatEcho(hb *proto.Heartbeat) {
	if hb == nil || hb.SentAt.IsZero() {
		return
	}
	now := time.Now()
	a.mu.Lock()
	a.rtt = now.Sub(hb.SentAt)
	a.lastEcho = now
	a.mu.Unlock()
}

// runHeuristics evaluates the PTY heuristic for every tool session.
func (a *Agent) runHeuristics(now time.Time) {
	a.mu.Lock()
	ss := make(map[uint32]*session.Session, len(a.sessions))
	for sid, s := range a.sessions {
		ss[sid] = s
	}
	a.mu.Unlock()
	for sid, s := range ss {
		v := s.Heuristic(now)
		switch v.Kind {
		case session.VerdictSuspect:
			a.SessionState(sid, proto.StateNeedsInput, proto.NeedQuestion, proto.SourcePTY, proto.ConfidenceLow)
			info := s.Info()
			ap := proto.Approval{
				Key:       ptyKey(sid, v.Line),
				SID:       sid,
				Agent:     s.Tool(),
				Tool:      "pty",
				Summary:   v.Line,
				Cwd:       info.Cwd,
				Level:     proto.LevelSuspect,
				Mode:      info.ApprovalMode,
				CreatedAt: now,
			}
			stored, dup := a.store.Add(ap)
			a.mu.Lock()
			a.ptyKeys[sid] = stored.Key
			a.mu.Unlock()
			if !dup {
				a.ApprovalNew(stored)
			}
		case session.VerdictCleared:
			a.mu.Lock()
			key := a.ptyKeys[sid]
			delete(a.ptyKeys, sid)
			a.mu.Unlock()
			if ap, ok := a.store.Close(key, proto.ApprovalClosedLocal, "output"); ok {
				a.ApprovalClosed(ap, "output")
			}
			a.SessionState(sid, proto.StateRunning, "", proto.SourcePTY, proto.ConfidenceLow)
		case session.VerdictUnknown:
			a.SessionState(sid, proto.StateUnknown, "", proto.SourcePTY, proto.ConfidenceLow)
		case session.VerdictResumed:
			a.SessionState(sid, proto.StateRunning, "", proto.SourcePTY, proto.ConfidenceLow)
		}
	}
}

func ptyKey(sid uint32, line string) string {
	h := sha256.Sum256([]byte("pty" + strconv.FormatUint(uint64(sid), 10) + line))
	return "pty-" + hex.EncodeToString(h[:])[:12]
}

// helloMsg builds agent.hello.
func (a *Agent) helloMsg(reqID string) proto.Msg {
	return proto.Msg{
		T:        proto.TAgentHello,
		ReqID:    reqID,
		AgentID:  a.cfg.AgentID,
		Version:  Version,
		OS:       runtime.GOOS,
		Caps:     Capabilities,
		Sessions: a.sessionList(),
	}
}
