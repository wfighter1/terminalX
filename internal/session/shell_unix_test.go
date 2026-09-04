//go:build !windows

package session

import (
	"regexp"
	"strconv"
	"syscall"
	"testing"
	"time"
)

// A wedged AI CLI must not outlive the session. Close signals the whole
// process group, so a child the shell started dies with it.
func TestCloseKillsProcessGroup(t *testing.T) {
	requireBash(t)
	c := &collector{}
	s := startShell(t, c, nil, 0)

	// A foreground job: this is how an AI CLI actually runs under the shell.
	if err := s.Write([]byte("bash -c 'echo tx-child=$$; exec sleep 300'\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	// The PTY echoes the typed line first, so wait for the form that only
	// the shell's own output can produce.
	re := regexp.MustCompile(`tx-child=(\d+)`)
	var m []string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if m = re.FindStringSubmatch(c.String()); m != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if m == nil {
		t.Fatalf("no child pid in output:\n%s", c.String())
	}
	pid, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatal(err)
	}
	if !processAlive(pid) {
		t.Fatalf("child %d should be running", pid)
	}

	s.Close()

	deadline = time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL) // do not leak it if the test fails
	t.Fatalf("child %d survived Close: the signal did not reach the process group", pid)
}

// processAlive reports whether pid exists. Signal 0 only checks permission
// and existence; a zombie still counts as existing, but the shell's children
// are reaped by init once the session leader is gone.
func processAlive(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}
