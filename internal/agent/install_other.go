//go:build !windows

package agent

import (
	"fmt"
	"os"
	"runtime"
)

// TaskName is the Task Scheduler task name used on Windows.
const TaskName = "terminalX Agent"

// Install prints instructions on non-Windows platforms (phase 1 targets
// Windows; Linux/macOS run the agent by hand or via systemd/launchd).
func Install(cfgPath string) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("agent install: executable path: %w", err)
	}
	switch runtime.GOOS {
	case "darwin":
		return fmt.Sprintf(`Automatic install is Windows-only in this phase. On macOS create a LaunchAgent, e.g.
~/Library/LaunchAgents/io.terminalx.agent.plist with ProgramArguments
  ["%s", "run", "--config", "%s"], RunAtLoad=true, KeepAlive=true
then: launchctl load ~/Library/LaunchAgents/io.terminalx.agent.plist
`, exe, cfgPath), nil
	default:
		return fmt.Sprintf(`Automatic install is Windows-only in this phase. On %s create a user service, e.g.
~/.config/systemd/user/tx-agent.service:
  [Unit]
  Description=terminalX agent
  After=network-online.target
  [Service]
  ExecStart=%s run --config %s
  Restart=always
  RestartSec=5
  [Install]
  WantedBy=default.target
then: systemctl --user enable --now tx-agent && loginctl enable-linger $USER
`, runtime.GOOS, exe, cfgPath), nil
	}
}

// Uninstall prints instructions on non-Windows platforms.
func Uninstall() (string, error) {
	return "Automatic uninstall is Windows-only in this phase; remove the LaunchAgent / systemd user unit you created by hand.\n", nil
}
