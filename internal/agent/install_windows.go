//go:build windows

package agent

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// TaskName is the Task Scheduler task registered by `tx-agent install`.
const TaskName = "terminalX Agent"

// Install registers an ONLOGON Task Scheduler task that runs
// `tx-agent run --config <cfgPath>` as the current user, then tries to set
// RestartOnFailure (1 min × 999) and no execution time limit through
// PowerShell, which schtasks.exe cannot express.
func Install(cfgPath string) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("agent install: executable path: %w", err)
	}
	var out strings.Builder
	tr := fmt.Sprintf(`"%s" run --config "%s"`, exe, cfgPath)
	cmd := exec.Command("schtasks.exe", "/Create", "/F", "/SC", "ONLOGON", "/RL", "LIMITED",
		"/TN", TaskName, "/TR", tr)
	b, err := cmd.CombinedOutput()
	out.Write(b)
	if err != nil {
		return out.String(), fmt.Errorf("agent install: schtasks /Create: %w", err)
	}
	fmt.Fprintf(&out, "task %q registered: %s\n", TaskName, tr)

	ps := fmt.Sprintf(`$s = New-ScheduledTaskSettingsSet -RestartCount 999 -RestartInterval (New-TimeSpan -Minutes 1) -ExecutionTimeLimit ([TimeSpan]::Zero) -AllowStartIfOnBatteries -DontStopIfGoingOnBatteries; Set-ScheduledTask -TaskName '%s' -Settings $s | Out-Null`, strings.ReplaceAll(TaskName, "'", "''"))
	pcmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", ps)
	if b, err := pcmd.CombinedOutput(); err != nil {
		out.Write(b)
		fmt.Fprintf(&out, "RestartOnFailure could not be set automatically (%v).\n"+
			"Set it by hand: Task Scheduler → %s → Settings → \"If the task fails, restart every 1 minute, up to 999 times\" and clear \"Stop the task if it runs longer than\".\n", err, TaskName)
	} else {
		fmt.Fprintln(&out, "RestartOnFailure: every 1 minute, up to 999 times; ExecutionTimeLimit: none; runs on battery.")
	}
	fmt.Fprintln(&out, "The task starts at the next logon. Start it now with: schtasks /Run /TN \""+TaskName+"\"")
	fmt.Fprintln(&out, "Note: sleep, logout or a locked auto-logon keeps the agent offline; consider `powercfg` and auto-logon on an always-on machine.")
	return out.String(), nil
}

// autostartStatus is the `tx-agent doctor` line for the autostart entry.
func autostartStatus() string {
	b, err := exec.Command("schtasks.exe", "/Query", "/TN", TaskName, "/FO", "LIST").CombinedOutput()
	if err != nil {
		return fmt.Sprintf("task %q not registered (run `tx-agent install`)", TaskName)
	}
	status := ""
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "Status:") {
			status = strings.TrimSpace(strings.TrimPrefix(line, "Status:"))
		}
	}
	if status == "" {
		return fmt.Sprintf("task %q registered", TaskName)
	}
	return fmt.Sprintf("task %q registered, status %s", TaskName, status)
}

// Uninstall removes the Task Scheduler task.
func Uninstall() (string, error) {
	cmd := exec.Command("schtasks.exe", "/Delete", "/F", "/TN", TaskName)
	b, err := cmd.CombinedOutput()
	if err != nil {
		return string(b), fmt.Errorf("agent uninstall: schtasks /Delete: %w", err)
	}
	return string(b) + fmt.Sprintf("task %q removed\n", TaskName), nil
}
