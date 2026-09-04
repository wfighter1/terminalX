//go:build darwin

package agent

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// TaskName is the LaunchAgent label registered by `tx-agent install`.
const TaskName = "io.terminalx.agent"

const plistTemplate = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key>
  <array>
    <string>%s</string><string>run</string><string>--config</string><string>%s</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>EnvironmentVariables</key>
  <dict>
%s  </dict>
  <key>StandardOutPath</key><string>%s</string>
  <key>StandardErrorPath</key><string>%s</string>
</dict>
</plist>
`

// Install writes ~/Library/LaunchAgents/io.terminalx.agent.plist and loads it
// into the current GUI session, so the agent starts at login and is restarted
// if it exits.
func Install(cfgPath string) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("agent install: executable path: %w", err)
	}
	cfgPath, _ = filepath.Abs(cfgPath)
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("agent install: home directory: %w", err)
	}
	dir := filepath.Join(home, "Library", "LaunchAgents")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("agent install: create %s: %w", dir, err)
	}
	logDir := filepath.Join(home, "Library", "Logs", "terminalX")
	_ = os.MkdirAll(logDir, 0o755)
	logPath := filepath.Join(logDir, "tx-agent.log")

	plistPath := filepath.Join(dir, TaskName+".plist")
	body := fmt.Sprintf(plistTemplate, TaskName, exe, cfgPath, environmentEntries(), logPath, logPath)
	if err := os.WriteFile(plistPath, []byte(body), 0o644); err != nil {
		return "", fmt.Errorf("agent install: write %s: %w", plistPath, err)
	}

	var out strings.Builder
	fmt.Fprintf(&out, "LaunchAgent written: %s\n", plistPath)
	target := "gui/" + strconv.Itoa(os.Getuid())
	_, _ = runOut("launchctl", "bootout", target+"/"+TaskName) // ignore "not loaded"
	if b, err := runOut("launchctl", "bootstrap", target, plistPath); err != nil {
		out.Write(b)
		// Older macOS releases only have load -w.
		if b2, err2 := runOut("launchctl", "load", "-w", plistPath); err2 != nil {
			out.Write(b2)
			return out.String(), fmt.Errorf("agent install: launchctl bootstrap/load: %w", err)
		}
	}
	fmt.Fprintf(&out, "loaded: %s (starts at login, restarted if it exits)\n", TaskName)
	fmt.Fprintf(&out, "logs: %s\n", logPath)
	fmt.Fprintln(&out, "Note: a Mac that sleeps takes the agent offline; check Energy Saver if it must stay reachable.")
	return out.String(), nil
}

// Uninstall unloads and removes the LaunchAgent.
func Uninstall() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("agent uninstall: home directory: %w", err)
	}
	plistPath := filepath.Join(home, "Library", "LaunchAgents", TaskName+".plist")
	var out strings.Builder
	target := "gui/" + strconv.Itoa(os.Getuid())
	if b, err := runOut("launchctl", "bootout", target+"/"+TaskName); err != nil {
		out.Write(b)
		_, _ = runOut("launchctl", "unload", "-w", plistPath)
	}
	if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
		return out.String(), fmt.Errorf("agent uninstall: remove %s: %w", plistPath, err)
	}
	fmt.Fprintf(&out, "removed: %s\n", plistPath)
	return out.String(), nil
}

// autostartStatus is the `tx-agent doctor` line for the autostart entry.
func autostartStatus() string {
	b, err := runOut("launchctl", "list", TaskName)
	if err != nil {
		return TaskName + " not loaded (run `tx-agent install`)"
	}
	pid := "-"
	for _, line := range strings.Split(string(b), "\n") {
		if strings.Contains(line, `"PID"`) {
			pid = strings.TrimSpace(strings.Trim(strings.SplitN(line, "=", 2)[1], " ;"))
		}
	}
	return fmt.Sprintf("%s loaded, pid %s", TaskName, pid)
}

// environmentEntries captures the parts of the installing shell's environment
// launchd does not inherit but the agent needs to find the AI CLIs.
func environmentEntries() string {
	var b strings.Builder
	for _, key := range []string{"PATH", "SHELL", "LANG"} {
		v := os.Getenv(key)
		if v == "" || strings.ContainsAny(v, "<>&\n\r") {
			continue
		}
		fmt.Fprintf(&b, "    <key>%s</key><string>%s</string>\n", key, v)
	}
	return b.String()
}

func runOut(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}
