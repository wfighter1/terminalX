//go:build !windows

package session

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// ResolveShell picks the shell executable for a session. An empty name means
// the platform default ($SHELL, then bash, then sh). Returns the resolved
// path, the shell kind used for quoting, and the startup args.
func ResolveShell(name string) (path string, kind string, args []string, err error) {
	if name == "" {
		if sh := os.Getenv("SHELL"); sh != "" {
			name = sh
		} else {
			name = "bash"
		}
	}
	p, err := exec.LookPath(name)
	if err != nil {
		if name != "bash" {
			return "", "", nil, fmt.Errorf("shell %q not found: %w", name, err)
		}
		if p, err = exec.LookPath("sh"); err != nil {
			return "", "", nil, fmt.Errorf("no shell found (tried bash, sh): %w", err)
		}
	}
	return p, "sh", nil, nil
}

// ShellName returns the short display name of a resolved shell path.
func ShellName(path string) string { return filepath.Base(path) }

// CandidateShells lists shells worth probing in `doctor`.
func CandidateShells() []string { return []string{"bash", "zsh", "fish", "sh"} }

// setSysProcAttr makes the child a session leader with the PTY slave as its
// controlling terminal (xpty's UnixPty.Start does not do this itself).
func setSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true}
}

// terminate asks the process to exit (SIGTERM) and, if it does not within
// grace, kills it. Errors from an already-exited process are ignored.
//
// The signal goes to the whole process group, not just the shell: the shell
// is a session leader (Setsid above), so its pgid equals its pid and the AI
// CLI it launched is in that group. Signalling only the shell would leave a
// wedged `claude` behind holding the tty.
func terminate(cmd *exec.Cmd, done <-chan struct{}, grace <-chan struct{}) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	signalGroup(cmd.Process.Pid, syscall.SIGTERM)
	select {
	case <-done:
		return
	case <-grace:
	}
	signalGroup(cmd.Process.Pid, syscall.SIGKILL)
}

// signalGroup signals the process group led by pid, falling back to the
// single process when the group is already gone.
func signalGroup(pid int, sig syscall.Signal) {
	if pid <= 0 {
		return
	}
	if err := syscall.Kill(-pid, sig); err != nil {
		_ = syscall.Kill(pid, sig)
	}
}
