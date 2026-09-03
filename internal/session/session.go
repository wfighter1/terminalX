package session

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/x/xpty"

	"github.com/wfighter1/terminalX/internal/proto"
)

// Defaults for Options.
const (
	DefaultRingSize      = 4 << 20 // 4 MiB scrollback per session
	DefaultBatchInterval = 12 * time.Millisecond
	DefaultCols          = 120
	DefaultRows          = 36
	// SnapshotTail is how much of the ring an attach snapshot replays.
	SnapshotTail = 512 << 10
	// killResumeGrace is how long kill_resume waits for a graceful exit
	// before terminating the process.
	killResumeGrace = 5 * time.Second
	// toolStartTimeout bounds how long we wait for the shell's first output
	// before typing the tool command anyway.
	toolStartTimeout = 2 * time.Second
	toolStartSettle  = 150 * time.Millisecond
)

// ErrExited is returned by Write / Resize / Signal when the PTY is gone.
var ErrExited = errors.New("session: process exited")

// Options configure Start.
type Options struct {
	SID          uint32
	Shell        string // requested shell name ("" = platform default)
	Cwd          string
	Tool         ToolSpec // Tool == "shell" (or "") for a plain shell
	Name         string
	Preset       string
	ApprovalMode proto.ApprovalMode
	// Env entries ("K=V") appended to the process environment.
	Env        []string
	Cols, Rows uint16
	// RingSize defaults to DefaultRingSize.
	RingSize int
	// BatchInterval defaults to DefaultBatchInterval.
	BatchInterval time.Duration
	Log           *slog.Logger
	// OnOutput receives coalesced PTY output; seq is the stream offset of
	// data[0]. Called from a session goroutine, never concurrently with
	// itself.
	OnOutput func(seq uint64, data []byte)
	// OnExit is called once when the shell process exits (not on kill_resume
	// respawns).
	OnExit func(code int32, r *proto.Resumable)
}

// proc is one generation of PTY + shell process. kill_resume replaces it
// while the Session (sid, ring, seq) lives on.
type proc struct {
	pty        xpty.Pty
	cmd        *exec.Cmd
	readerDone chan struct{} // reader goroutine stopped
	exited     chan struct{} // process reaped and pty closed
	code       int32
}

// Session is one PTY-backed terminal session.
type Session struct {
	opts Options
	log  *slog.Logger

	shellPath string
	shellKind string
	shellArgs []string

	mu             sync.Mutex
	info           proto.SessionInfo
	cur            *proc
	ring           *Ring
	lastOutput     time.Time
	lastStructured time.Time
	attached       int
	heur           HeurState
	toolSessionID  string
	toolExited     bool
	killing        bool
	closed         bool

	// output batching (guarded by mu)
	pending    []byte
	pendingSeq uint64
	flushTimer *time.Timer
	flushMu    sync.Mutex // serialises OnOutput callbacks
}

// NewSID returns a random 31-bit, non-zero session id.
func NewSID() uint32 {
	var b [4]byte
	for {
		if _, err := rand.Read(b[:]); err != nil {
			panic(fmt.Errorf("session: crypto/rand: %w", err))
		}
		sid := binary.BigEndian.Uint32(b[:]) & 0x7fffffff
		if sid != 0 {
			return sid
		}
	}
}

// Start resolves the shell, allocates a PTY, launches the shell and (for
// tool sessions) types the tool command line into it once the shell has
// produced its first output.
func Start(o Options) (*Session, error) {
	if o.SID == 0 {
		o.SID = NewSID()
	}
	if o.RingSize <= 0 {
		o.RingSize = DefaultRingSize
	}
	if o.BatchInterval <= 0 {
		o.BatchInterval = DefaultBatchInterval
	}
	if o.Cols == 0 {
		o.Cols = DefaultCols
	}
	if o.Rows == 0 {
		o.Rows = DefaultRows
	}
	if o.Log == nil {
		o.Log = slog.Default()
	}
	if o.Tool.Tool == "" {
		o.Tool.Tool = proto.ToolShell
	}
	if o.ApprovalMode == "" {
		o.ApprovalMode = proto.ApprovalNotify
	}
	if o.Cwd != "" {
		st, err := os.Stat(o.Cwd)
		if err != nil || !st.IsDir() {
			return nil, fmt.Errorf("session: cwd %q is not a directory", o.Cwd)
		}
	} else {
		o.Cwd, _ = os.Getwd()
	}
	path, kind, args, err := ResolveShell(o.Shell)
	if err != nil {
		return nil, fmt.Errorf("session: %w", err)
	}
	o.Tool.ShellKind = kind
	s := &Session{
		opts:      o,
		log:       o.Log.With("sid", o.SID),
		shellPath: path,
		shellKind: kind,
		shellArgs: args,
		ring:      NewRing(o.RingSize),
	}
	now := time.Now()
	s.info = proto.SessionInfo{
		SID:            o.SID,
		Name:           o.Name,
		Tool:           o.Tool.Tool,
		Shell:          ShellName(path),
		Cwd:            o.Cwd,
		Preset:         o.Preset,
		PermissionMode: o.Tool.PermissionMode,
		ApprovalMode:   o.ApprovalMode,
		State:          proto.StateRunning,
		Source:         proto.SourceNone,
		Confidence:     proto.ConfidenceLow,
		StartedAt:      now,
		LastOutputAt:   now,
		Cols:           o.Cols,
		Rows:           o.Rows,
		PTYAlive:       true,
	}
	s.lastOutput = now
	p, err := s.spawn(false)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.cur = p
	s.mu.Unlock()
	return s, nil
}

// spawn allocates a PTY and starts the shell; it does not touch s.cur.
func (s *Session) spawn(resume bool) (*proc, error) {
	s.mu.Lock()
	cols, rows := s.info.Cols, s.info.Rows
	s.mu.Unlock()
	pty, err := xpty.NewPty(int(cols), int(rows))
	if err != nil {
		return nil, fmt.Errorf("session: allocate pty: %w", err)
	}
	cmd := exec.Command(s.shellPath, s.shellArgs...)
	cmd.Dir = s.opts.Cwd
	cmd.Env = append(baseEnv(), s.opts.Env...)
	setSysProcAttr(cmd)
	if err := pty.Start(cmd); err != nil {
		_ = pty.Close()
		return nil, fmt.Errorf("session: start %s: %w", s.shellPath, err)
	}
	// On Unix the parent must drop its copy of the slave so the master
	// read returns EIO when the child exits. ConPTY has no slave.
	if sp, ok := pty.(interface{ Slave() *os.File }); ok {
		_ = sp.Slave().Close()
	}
	p := &proc{pty: pty, cmd: cmd, readerDone: make(chan struct{}), exited: make(chan struct{})}
	go s.readLoop(p)
	go s.waitLoop(p)
	if line := s.opts.Tool.Command(resume); line != "" {
		go s.typeTool(p, line)
	}
	return p, nil
}

// afterGrace returns a channel closed after d (terminate's grace input).
func afterGrace(d time.Duration) <-chan struct{} {
	ch := make(chan struct{})
	time.AfterFunc(d, func() { close(ch) })
	return ch
}

func baseEnv() []string {
	env := os.Environ()
	hasTerm := false
	for _, e := range env {
		if strings.HasPrefix(e, "TERM=") {
			hasTerm = true
			break
		}
	}
	if !hasTerm {
		env = append(env, "TERM=xterm-256color")
	}
	return env
}

// typeTool writes the tool command line once the shell shows its prompt
// (first output) or toolStartTimeout elapses.
func (s *Session) typeTool(p *proc, line string) {
	deadline := time.Now().Add(toolStartTimeout)
	s.mu.Lock()
	seq0 := s.ring.Seq()
	s.mu.Unlock()
	for time.Now().Before(deadline) {
		if s.ring.Seq() > seq0 {
			break
		}
		select {
		case <-p.exited:
			return
		case <-time.After(20 * time.Millisecond):
		}
	}
	time.Sleep(toolStartSettle)
	select {
	case <-p.exited:
		return
	default:
	}
	if _, err := p.pty.Write([]byte(line + "\r")); err != nil {
		s.log.Warn("type tool command", "err", err)
		return
	}
	s.log.Info("tool command typed", "tool", s.opts.Tool.Tool, "cmd", line)
}

func (s *Session) readLoop(p *proc) {
	defer close(p.readerDone)
	buf := make([]byte, 32<<10)
	for {
		n, err := p.pty.Read(buf)
		if n > 0 {
			s.emit(buf[:n])
		}
		if err != nil {
			return
		}
	}
}

// emit appends output to the ring and the pending batch. It is the only
// writer of the ring.
func (s *Session) emit(data []byte) {
	s.mu.Lock()
	start := s.ring.Write(data)
	s.lastOutput = time.Now()
	s.info.LastOutputAt = s.lastOutput
	s.info.Seq = s.ring.Seq()
	if len(s.pending) == 0 {
		s.pendingSeq = start
	}
	s.pending = append(s.pending, data...)
	big := len(s.pending) >= 256<<10
	if s.flushTimer == nil {
		s.flushTimer = time.AfterFunc(s.opts.BatchInterval, s.flush)
	}
	s.mu.Unlock()
	if big {
		s.flush()
	}
}

// flush hands the pending batch to OnOutput.
func (s *Session) flush() {
	s.flushMu.Lock()
	defer s.flushMu.Unlock()
	s.flushLocked()
}

// Replay computes what an attaching client with lastSeq needs and hands it
// to send while output delivery is paused, so the replay and the live
// stream stay ordered. delta is true when the ring still covers lastSeq
// (data is the bytes after it); otherwise data is the snapshot tail.
// endSeq is the stream offset at the end of data.
func (s *Session) Replay(lastSeq uint64, send func(data []byte, endSeq uint64, delta bool)) {
	s.flushMu.Lock()
	defer s.flushMu.Unlock()
	s.flushLocked()
	if d, ok := s.ring.Delta(lastSeq); ok {
		send(d, lastSeq+uint64(len(d)), true)
		return
	}
	tail, end := s.ring.Tail(SnapshotTail)
	send(tail, end, false)
}

// flushLocked is flush with flushMu held.
func (s *Session) flushLocked() {
	s.mu.Lock()
	if s.flushTimer != nil {
		s.flushTimer.Stop()
		s.flushTimer = nil
	}
	data, seq := s.pending, s.pendingSeq
	s.pending = nil
	s.mu.Unlock()
	if len(data) == 0 || s.opts.OnOutput == nil {
		return
	}
	s.opts.OnOutput(seq, data)
}

func (s *Session) waitLoop(p *proc) {
	err := xpty.WaitProcess(context.Background(), p.cmd)
	code := int32(-1)
	if p.cmd.ProcessState != nil {
		code = int32(p.cmd.ProcessState.ExitCode())
	}
	if err != nil && code == 0 {
		code = -1
	}
	p.code = code
	// Let the reader drain what is already buffered, then close the PTY
	// (ConPTY keeps its output pipe open until closed).
	select {
	case <-p.readerDone:
	case <-time.After(500 * time.Millisecond):
	}
	_ = p.pty.Close()
	select {
	case <-p.readerDone:
	case <-time.After(2 * time.Second):
		s.log.Warn("pty reader did not stop after close")
	}
	s.flush()

	s.mu.Lock()
	superseded := s.cur != p || s.killing || s.closed
	var resumable *proto.Resumable
	if !superseded {
		s.info.State = proto.StateExited
		s.info.PTYAlive = false
		s.info.ExitCode = &code
		s.info.Kind = ""
		resumable = s.resumableLocked()
		s.info.Resumable = resumable
	}
	s.mu.Unlock()
	close(p.exited)
	if superseded {
		return
	}
	s.log.Info("shell exited", "code", code)
	if s.opts.OnExit != nil {
		s.opts.OnExit(code, resumable)
	}
}

// resumableLocked describes how to bring the session back. mu must be held.
func (s *Session) resumableLocked() *proto.Resumable {
	r := &proto.Resumable{Tool: s.info.Tool, Cwd: s.info.Cwd}
	switch s.info.Tool {
	case proto.ToolClaude:
		if s.info.Name != "" {
			r.Name = s.info.Name
		} else {
			r.Name = s.toolSessionID
		}
	case proto.ToolCodex:
		r.Name = s.toolSessionID
		if r.Name == "" {
			r.Name = "last"
		}
	}
	return r
}

// ---- accessors ----------------------------------------------------------

// SID returns the session id.
func (s *Session) SID() uint32 { return s.opts.SID }

// Info returns a copy of the current SessionInfo.
func (s *Session) Info() proto.SessionInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.info.Seq = s.ring.Seq()
	return s.info
}

// Health returns the heartbeat view of the session.
func (s *Session) Health() proto.SessionHealth {
	s.mu.Lock()
	defer s.mu.Unlock()
	return proto.SessionHealth{SID: s.opts.SID, PTYAlive: s.cur != nil && s.info.PTYAlive, LastOutputAt: s.lastOutput, Seq: s.ring.Seq()}
}

// Seq returns the current output stream offset.
func (s *Session) Seq() uint64 { return s.ring.Seq() }

// Alive reports whether the shell process is running.
func (s *Session) Alive() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.info.PTYAlive && !s.closed
}

// Tool returns the tool of the session (shell once the tool has exited).
func (s *Session) Tool() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.toolExited {
		return proto.ToolShell
	}
	return s.info.Tool
}

// ApprovalMode returns the current approval mode.
func (s *Session) ApprovalMode() proto.ApprovalMode {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.info.ApprovalMode
}

// SetApprovalMode changes the approval mode and returns the new info.
func (s *Session) SetApprovalMode(m proto.ApprovalMode) proto.SessionInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	if m != "" {
		s.info.ApprovalMode = m
	}
	return s.info
}

// SetState records a state transition. Structured sources refresh the
// "last structured signal" clock used by the PTY heuristic.
func (s *Session) SetState(st proto.SessionState, kind proto.NeedKind, src proto.Source, conf proto.Confidence, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.info.State == proto.StateExited {
		return
	}
	s.info.State = st
	s.info.Kind = kind
	s.info.Source = src
	s.info.Confidence = conf
	switch src {
	case proto.SourceHook, proto.SourceHooksJSON, proto.SourceNotify, proto.SourceStatusLine:
		s.lastStructured = now
	}
}

// SetMetrics records statusLine cost / context and returns the info.
func (s *Session) SetMetrics(cost, ctxPct *float64, now time.Time) proto.SessionInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	if cost != nil {
		s.info.CostUSD = cost
	}
	if ctxPct != nil {
		s.info.ContextPct = ctxPct
	}
	s.lastStructured = now
	return s.info
}

// SetToolSession records the tool's own session / thread id for resume.
func (s *Session) SetToolSession(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.toolSessionID = id
	s.toolExited = false
}

// ToolExited marks that the AI CLI left the shell (SessionEnd).
func (s *Session) ToolExited() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.toolExited = true
}

// Attach increments the attached-client count.
func (s *Session) Attach() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attached++
}

// Detach decrements the attached-client count.
func (s *Session) Detach() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.attached > 0 {
		s.attached--
	}
}

// Attached returns the attached-client count.
func (s *Session) Attached() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.attached
}

// Delta returns the output since lastSeq when it is still in the ring.
func (s *Session) Delta(lastSeq uint64) ([]byte, bool) { return s.ring.Delta(lastSeq) }

// Snapshot returns the last SnapshotTail bytes and the seq at its end.
func (s *Session) Snapshot() ([]byte, uint64) { return s.ring.Tail(SnapshotTail) }

// ---- input / control ----------------------------------------------------

func (s *Session) current() (*proc, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cur == nil || !s.info.PTYAlive || s.closed {
		return nil, ErrExited
	}
	return s.cur, nil
}

// Write sends bytes to the PTY.
func (s *Session) Write(p []byte) error {
	pr, err := s.current()
	if err != nil {
		return err
	}
	if _, err := pr.pty.Write(p); err != nil {
		return fmt.Errorf("session: write pty: %w", err)
	}
	return nil
}

// Resize changes the PTY size.
func (s *Session) Resize(cols, rows uint16) error {
	if cols == 0 || rows == 0 {
		return errors.New("session: cols/rows must be > 0")
	}
	s.mu.Lock()
	s.info.Cols, s.info.Rows = cols, rows
	pr := s.cur
	alive := s.info.PTYAlive && !s.closed
	s.mu.Unlock()
	if pr == nil || !alive {
		return ErrExited
	}
	if err := pr.pty.Resize(int(cols), int(rows)); err != nil {
		return fmt.Errorf("session: resize pty: %w", err)
	}
	return nil
}

// Size returns the current PTY size.
func (s *Session) Size() (cols, rows uint16) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.info.Cols, s.info.Rows
}

// Signal implements session.signal. Windows has no SIGTERM/SIGINT, so every
// signal is expressed as PTY input; kill_resume additionally respawns.
func (s *Session) Signal(sig string) error {
	switch sig {
	case proto.SigEsc:
		return s.Write([]byte{0x1b})
	case proto.SigCtrlC:
		return s.Write([]byte{0x03})
	case proto.SigEOF:
		return s.Write([]byte{0x04})
	case proto.SigKillResume:
		return s.KillResume()
	}
	return fmt.Errorf("session: unknown signal %q", sig)
}

// exitCommand is the line that makes the tool (or shell) exit gracefully.
func (s *Session) exitCommand() string {
	switch s.Tool() {
	case proto.ToolClaude:
		return "/exit\r"
	case proto.ToolCodex:
		return "/quit\r"
	case proto.ToolGrok:
		return "/exit\r"
	default:
		return "exit\r"
	}
}

// KillResume stops the current process gracefully (Ctrl-C, exit command,
// then terminate) and respawns the shell + tool in resume form under the
// same sid. The ring buffer and seq continue.
func (s *Session) KillResume() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrExited
	}
	if s.killing {
		s.mu.Unlock()
		return errors.New("session: kill_resume already in progress")
	}
	s.killing = true
	old := s.cur
	alive := s.info.PTYAlive
	s.mu.Unlock()

	if old != nil && alive {
		_, _ = old.pty.Write([]byte{0x03})
		select {
		case <-old.exited:
		case <-time.After(500 * time.Millisecond):
		}
		select {
		case <-old.exited:
		default:
			_, _ = old.pty.Write([]byte(s.exitCommand()))
		}
		select {
		case <-old.exited:
		case <-time.After(killResumeGrace):
			s.log.Warn("kill_resume: graceful exit timed out, terminating")
			terminate(old.cmd, old.exited, afterGrace(2*time.Second))
			// Closing the PTY unblocks a reader stuck on ConPTY.
			select {
			case <-old.exited:
			case <-time.After(3 * time.Second):
				_ = old.pty.Close()
				<-old.exited
			}
		}
	}

	s.emit([]byte(fmt.Sprintf("\r\n\x1b[2m[terminalX] kill & resume: %s 已重启，正在续跑上一段会话…\x1b[0m\r\n", s.opts.Tool.Tool)))
	s.flush()

	p, err := s.spawn(true)
	s.mu.Lock()
	s.killing = false
	if err != nil {
		s.cur = nil
		s.info.PTYAlive = false
		s.info.State = proto.StateFailed
		s.mu.Unlock()
		return fmt.Errorf("session: respawn after kill: %w", err)
	}
	s.cur = p
	s.toolExited = false
	s.info.PTYAlive = true
	s.info.State = proto.StateRunning
	s.info.Kind = ""
	s.info.Source = proto.SourceNone
	s.info.Confidence = proto.ConfidenceLow
	s.info.ExitCode = nil
	s.info.Resumable = nil
	s.mu.Unlock()
	s.log.Info("kill_resume: respawned", "tool", s.opts.Tool.Tool)
	return nil
}

// Close terminates the process and releases the PTY. It is idempotent.
func (s *Session) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	p := s.cur
	alive := s.info.PTYAlive
	s.info.PTYAlive = false
	s.mu.Unlock()
	if p == nil {
		return
	}
	if alive {
		terminate(p.cmd, p.exited, afterGrace(2*time.Second))
	}
	select {
	case <-p.exited:
	case <-time.After(2 * time.Second):
		_ = p.pty.Close()
		select {
		case <-p.exited:
		case <-time.After(2 * time.Second):
		}
	}
}

// Wait blocks until the current process generation exits (tests / shutdown).
func (s *Session) Wait(ctx context.Context) error {
	s.mu.Lock()
	p := s.cur
	s.mu.Unlock()
	if p == nil {
		return nil
	}
	select {
	case <-p.exited:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ---- heuristic ----------------------------------------------------------

// Heuristic runs the PTY heuristic for this session at now. Plain shell
// sessions (and sessions whose tool already exited) never produce a verdict.
func (s *Session) Heuristic(now time.Time) Verdict {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.info.PTYAlive || s.closed || s.toolExited {
		return Verdict{}
	}
	tail, _ := s.ring.Tail(4 << 10)
	in := HeurInput{
		Tool:           s.info.Tool,
		LastOutput:     s.lastOutput,
		LastStructured: s.lastStructured,
		LastLine:       LastLine(tail),
	}
	return Evaluate(in, &s.heur, now)
}
