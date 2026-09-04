//go:build !windows && !linux && !darwin

package agent

import (
	"fmt"
	"os"
	"runtime"
)

// TaskName is the autostart entry name (unused on this platform).
const TaskName = "tx-agent"

// Install prints instructions: only Windows, Linux and macOS have an
// autostart integration in phase 1.
func Install(cfgPath string) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("agent install: executable path: %w", err)
	}
	return fmt.Sprintf(`Automatic install is not implemented for %s. Register this command with
whatever supervisor the system uses so it starts at login and restarts on exit:

  %s run --config %s
`, runtime.GOOS, exe, cfgPath), nil
}

// Uninstall prints instructions on unsupported platforms.
func Uninstall() (string, error) {
	return fmt.Sprintf("Automatic uninstall is not implemented for %s; remove the entry you created by hand.\n", runtime.GOOS), nil
}

// autostartStatus is the `tx-agent doctor` line for the autostart entry.
func autostartStatus() string {
	return "no autostart integration for " + runtime.GOOS
}
