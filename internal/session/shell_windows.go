//go:build windows

package session

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// ResolveShell picks the shell executable for a session. An empty name walks
// pwsh.exe → powershell.exe → cmd.exe. Startup args switch the console to
// UTF-8 so Chinese output survives the ConPTY → xterm.js path.
func ResolveShell(name string) (path string, kind string, args []string, err error) {
	order := []string{"pwsh", "powershell", "cmd"}
	if name != "" {
		order = []string{strings.TrimSuffix(strings.ToLower(name), ".exe")}
	}
	var lastErr error
	for _, n := range order {
		exe := n
		if !strings.Contains(exe, ".") {
			exe += ".exe"
		}
		p, err := exec.LookPath(exe)
		if err != nil {
			lastErr = err
			continue
		}
		switch n {
		case "pwsh", "powershell":
			return p, n, []string{"-NoLogo", "-NoExit", "-Command",
				"[Console]::InputEncoding=[Console]::OutputEncoding=[System.Text.Encoding]::UTF8"}, nil
		case "cmd":
			return p, "cmd", []string{"/K", "chcp 65001>nul"}, nil
		default:
			return p, "sh", nil, nil // git bash / wsl bash: POSIX quoting
		}
	}
	return "", "", nil, fmt.Errorf("no shell found (tried %s): %w", strings.Join(order, ", "), lastErr)
}

// ShellName returns the short display name of a resolved shell path.
func ShellName(path string) string {
	return strings.TrimSuffix(strings.ToLower(filepath.Base(path)), ".exe")
}

// CandidateShells lists shells worth probing in `doctor`.
func CandidateShells() []string { return []string{"pwsh.exe", "powershell.exe", "cmd.exe", "bash.exe"} }

// setSysProcAttr is a no-op on Windows; ConPTY handles console attachment.
func setSysProcAttr(cmd *exec.Cmd) {}

// terminate kills the process: Windows has no SIGTERM, so os.Process.Kill
// (TerminateProcess) is the only option.
func terminate(cmd *exec.Cmd, done <-chan struct{}, grace <-chan struct{}) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
}
