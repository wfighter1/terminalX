package session

import (
	"bytes"
	"context"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wfighter1/terminalX/internal/proto"
)

// collector gathers OnOutput batches.
type collector struct {
	mu   sync.Mutex
	buf  bytes.Buffer
	seqs []uint64
}

func (c *collector) on(seq uint64, data []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seqs = append(c.seqs, seq)
	c.buf.Write(data)
}

func (c *collector) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

func (c *collector) waitFor(t *testing.T, marker string, d time.Duration) {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if strings.Contains(c.String(), marker) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("marker %q not seen in output:\n%s", marker, c.String())
}

func requireBash(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("bash-based test")
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not installed")
	}
}

func startShell(t *testing.T, c *collector, onExit func(int32, *proto.Resumable), ringSize int) *Session {
	t.Helper()
	s, err := Start(Options{
		Shell:    "bash",
		Cwd:      t.TempDir(),
		Tool:     ToolSpec{Tool: proto.ToolShell},
		Env:      []string{"PS1=$ ", "TERM=dumb"},
		Cols:     80,
		Rows:     24,
		RingSize: ringSize,
		OnOutput: c.on,
		OnExit:   onExit,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(s.Close)
	return s
}

func TestSessionEchoRoundTrip(t *testing.T) {
	requireBash(t)
	c := &collector{}
	s := startShell(t, c, nil, 0)
	if !s.Alive() {
		t.Fatal("session should be alive")
	}
	if err := s.Write([]byte("echo tx-$((1+1))\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	c.waitFor(t, "tx-2", 5*time.Second)
	info := s.Info()
	if info.Seq == 0 || info.Seq != s.Seq() || !info.PTYAlive || info.State != proto.StateRunning {
		t.Fatalf("bad info: %+v", info)
	}
	// Batches must be contiguous: seq[i+1] == seq[i] + len(batch i).
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.seqs[0] != 0 {
		t.Fatalf("first batch seq = %d, want 0", c.seqs[0])
	}
	if last := c.seqs[len(c.seqs)-1]; last >= uint64(c.buf.Len()) {
		t.Fatalf("last batch seq %d >= total %d", last, c.buf.Len())
	}
}

func TestSessionAttachPaths(t *testing.T) {
	requireBash(t)
	c := &collector{}
	s := startShell(t, c, nil, 256) // tiny ring so an old last_seq falls out of the window
	if err := s.Write([]byte("echo tx-$((3+3))\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	c.waitFor(t, "tx-6", 5*time.Second)
	seq := s.Seq()
	// force the ring past its capacity
	if err := s.Write([]byte("printf '%0400d\\n' 0; echo tx-$((4+4))\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	c.waitFor(t, "tx-8", 5*time.Second)
	time.Sleep(50 * time.Millisecond)
	cur := s.Seq()
	tests := []struct {
		name     string
		lastSeq  uint64
		wantOK   bool
		wantSnap bool
	}{
		{"caught up", cur, true, false},
		{"slightly behind", cur - 10, true, false},
		{"before window", seq, false, true},
		{"zero", 0, false, true},
		{"future", cur + 100, false, true},
	}
	for _, tc := range tests {
		delta, ok := s.Delta(tc.lastSeq)
		if ok != tc.wantOK {
			t.Errorf("%s: Delta ok=%v want %v", tc.name, ok, tc.wantOK)
			continue
		}
		if ok && uint64(len(delta)) != cur-tc.lastSeq {
			t.Errorf("%s: delta len %d want %d", tc.name, len(delta), cur-tc.lastSeq)
		}
		if tc.wantSnap {
			snap, end := s.Snapshot()
			if end != cur || len(snap) != 256 || !strings.Contains(string(snap), "tx-8") {
				t.Errorf("%s: snapshot len=%d end=%d cur=%d", tc.name, len(snap), end, cur)
			}
		}
	}
}

func TestSessionKillResumeShell(t *testing.T) {
	requireBash(t)
	c := &collector{}
	exits := make(chan int32, 1)
	s := startShell(t, c, func(code int32, _ *proto.Resumable) { exits <- code }, 0)
	if err := s.Write([]byte("echo tx-$((5+5))\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	c.waitFor(t, "tx-10", 5*time.Second)
	before := s.Seq()
	start := time.Now()
	if err := s.Signal(proto.SigKillResume); err != nil {
		t.Fatalf("kill_resume: %v", err)
	}
	if d := time.Since(start); d > killResumeGrace {
		t.Fatalf("kill_resume took %v: graceful exit did not work", d)
	}
	c.waitFor(t, "[terminalX] kill & resume", 2*time.Second)
	if s.Seq() <= before {
		t.Fatal("seq must continue across kill_resume")
	}
	if !s.Alive() {
		t.Fatal("session must be alive after respawn")
	}
	if err := s.Write([]byte("echo tx-$((6+6))\n")); err != nil {
		t.Fatalf("Write after respawn: %v", err)
	}
	c.waitFor(t, "tx-12", 5*time.Second)
	select {
	case code := <-exits:
		t.Fatalf("OnExit fired during kill_resume with code %d", code)
	default:
	}
	if s.Info().State != proto.StateRunning {
		t.Fatalf("state after respawn: %s", s.Info().State)
	}
}

func TestSessionExitReportsCode(t *testing.T) {
	requireBash(t)
	c := &collector{}
	type exit struct {
		code int32
		r    *proto.Resumable
	}
	exits := make(chan exit, 1)
	s := startShell(t, c, func(code int32, r *proto.Resumable) { exits <- exit{code, r} }, 0)
	if err := s.Write([]byte("exit 3\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	select {
	case e := <-exits:
		if e.code != 3 {
			t.Fatalf("exit code %d want 3", e.code)
		}
		if e.r == nil || e.r.Tool != proto.ToolShell || e.r.Cwd == "" {
			t.Fatalf("resumable %+v", e.r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("OnExit not called")
	}
	info := s.Info()
	if info.State != proto.StateExited || info.PTYAlive || info.ExitCode == nil || *info.ExitCode != 3 {
		t.Fatalf("info after exit: %+v", info)
	}
	if err := s.Write([]byte("x")); err != ErrExited {
		t.Fatalf("Write after exit err=%v want ErrExited", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := s.Wait(ctx); err != nil {
		t.Fatalf("Wait: %v", err)
	}
}

func TestSessionToolCommandTyped(t *testing.T) {
	requireBash(t)
	c := &collector{}
	// A "tool" that is really grok: the command line typed is "grok"; bash
	// prints "command not found", proving the line was typed after the
	// prompt appeared. Use a shell alias-free marker via Extra.
	s, err := Start(Options{
		Shell:    "bash",
		Cwd:      t.TempDir(),
		Tool:     ToolSpec{Tool: proto.ToolGrok, Extra: []string{"tx-marker-7"}},
		Env:      []string{"PS1=$ ", "TERM=dumb"},
		OnOutput: c.on,
	})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(s.Close)
	c.waitFor(t, "grok tx-marker-7", 5*time.Second)
	if s.Tool() != proto.ToolGrok {
		t.Fatalf("Tool() = %s", s.Tool())
	}
	s.ToolExited()
	if s.Tool() != proto.ToolShell {
		t.Fatalf("Tool() after ToolExited = %s", s.Tool())
	}
}

func TestSessionStateAndHeuristicGate(t *testing.T) {
	requireBash(t)
	c := &collector{}
	s := startShell(t, c, nil, 0)
	now := time.Now()
	s.SetState(proto.StateNeedsInput, proto.NeedPermission, proto.SourceHook, proto.ConfidenceHigh, now)
	info := s.Info()
	if info.State != proto.StateNeedsInput || info.Kind != proto.NeedPermission || info.Source != proto.SourceHook {
		t.Fatalf("SetState not applied: %+v", info)
	}
	if v := s.Heuristic(now); v.Kind != VerdictNone {
		t.Fatalf("shell session must never get a heuristic verdict, got %v", v.Kind)
	}
	cost := 1.5
	if got := s.SetMetrics(&cost, nil, now); got.CostUSD == nil || *got.CostUSD != 1.5 {
		t.Fatalf("SetMetrics: %+v", got)
	}
	if got := s.SetApprovalMode(proto.ApprovalRemoteFirst); got.ApprovalMode != proto.ApprovalRemoteFirst {
		t.Fatalf("SetApprovalMode: %+v", got)
	}
	if err := s.Resize(100, 30); err != nil {
		t.Fatalf("Resize: %v", err)
	}
	if cols, rows := s.Size(); cols != 100 || rows != 30 {
		t.Fatalf("Size = %dx%d", cols, rows)
	}
}

func TestNewSID(t *testing.T) {
	seen := map[uint32]bool{}
	for i := 0; i < 1000; i++ {
		sid := NewSID()
		if sid == 0 || sid&0x80000000 != 0 {
			t.Fatalf("bad sid %d", sid)
		}
		seen[sid] = true
	}
	if len(seen) < 990 {
		t.Fatalf("sids not random enough: %d unique", len(seen))
	}
}
