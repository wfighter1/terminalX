//go:build linux

package agent

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strings"
)

// TaskName is the autostart unit registered by `tx-agent install`.
const TaskName = "tx-agent.service"

// unitTemplate is the systemd **user** unit. A user unit (not a system one)
// is deliberate: the agent must run as the person whose AI CLI credentials,
// PATH and home directory it uses, and a user unit needs no root.
//
// KillMode is left at the default (control-group) because sessions currently
// live inside the agent process: the PTY master dies with it either way, and
// control-group is what stops orphaned children from leaking on restart.
const unitTemplate = `[Unit]
Description=terminalX agent (%s)
Documentation=https://github.com/wfighter1/terminalX
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=%s run --config %s
Restart=always
RestartSec=5
# systemd --user starts with a minimal environment; these are captured from
# the shell that ran "tx-agent install" so the agent can find your AI CLIs.
%s
[Install]
WantedBy=default.target
`

// Install writes ~/.config/systemd/user/tx-agent.service, enables and starts
// it, and turns on lingering so the agent keeps running (and starts at boot)
// without an open login session.
func Install(cfgPath string) (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("agent install: executable path: %w", err)
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		return manualInstructions(exe, cfgPath), fmt.Errorf("agent install: systemctl not found; " +
			"this system does not use systemd, so register the agent with your own init")
	}
	cfgPath, _ = filepath.Abs(cfgPath)

	dir, err := userUnitDir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("agent install: create %s: %w", dir, err)
	}
	unitPath := filepath.Join(dir, TaskName)
	if err := os.WriteFile(unitPath, []byte(renderUnit(exe, cfgPath)), 0o644); err != nil {
		return "", fmt.Errorf("agent install: write %s: %w", unitPath, err)
	}

	var out strings.Builder
	fmt.Fprintf(&out, "unit written: %s\n", unitPath)
	if b, err := runOut("systemctl", "--user", "daemon-reload"); err != nil {
		out.Write(b)
		return out.String(), fmt.Errorf("agent install: systemctl --user daemon-reload: %w "+
			"(no user systemd instance? check that XDG_RUNTIME_DIR is set and `systemctl --user status` works)", err)
	}
	if b, err := runOut("systemctl", "--user", "enable", "--now", TaskName); err != nil {
		out.Write(b)
		return out.String(), fmt.Errorf("agent install: systemctl --user enable --now %s: %w", TaskName, err)
	}
	fmt.Fprintf(&out, "enabled and started: %s\n", TaskName)

	// Without lingering the user manager is torn down at logout and the agent
	// with it, which is exactly the failure this product exists to avoid.
	if name := currentUser(); name != "" {
		if b, err := runOut("loginctl", "enable-linger", name); err != nil {
			out.Write(b)
			fmt.Fprintf(&out, "WARNING lingering could not be enabled (%v).\n"+
				"Without it the agent stops at logout and does not start at boot. Run as root:\n"+
				"  loginctl enable-linger %s\n", err, name)
		} else {
			fmt.Fprintf(&out, "lingering enabled for %s: the agent survives logout and starts at boot\n", name)
		}
	}
	fmt.Fprintf(&out, "logs: journalctl --user -u %s -f\n", TaskName)
	return out.String(), nil
}

// Uninstall stops and removes the user unit. Lingering is left alone: it is a
// user-wide setting that other services may rely on.
func Uninstall() (string, error) {
	var out strings.Builder
	if _, err := exec.LookPath("systemctl"); err != nil {
		return "", fmt.Errorf("agent uninstall: systemctl not found; remove your init entry by hand")
	}
	if b, err := runOut("systemctl", "--user", "disable", "--now", TaskName); err != nil {
		out.Write(b)
		fmt.Fprintf(&out, "(disable reported %v; removing the unit file anyway)\n", err)
	} else {
		fmt.Fprintf(&out, "stopped and disabled: %s\n", TaskName)
	}
	dir, err := userUnitDir()
	if err != nil {
		return out.String(), err
	}
	unitPath := filepath.Join(dir, TaskName)
	if err := os.Remove(unitPath); err != nil && !os.IsNotExist(err) {
		return out.String(), fmt.Errorf("agent uninstall: remove %s: %w", unitPath, err)
	}
	fmt.Fprintf(&out, "removed: %s\n", unitPath)
	_, _ = runOut("systemctl", "--user", "daemon-reload")
	if name := currentUser(); name != "" {
		fmt.Fprintf(&out, "lingering left as is; disable it with: loginctl disable-linger %s\n", name)
	}
	return out.String(), nil
}

// autostartStatus is the `tx-agent doctor` line for the autostart entry.
func autostartStatus() string {
	if _, err := exec.LookPath("systemctl"); err != nil {
		return "systemctl not found (no systemd; the agent must be started by hand)"
	}
	enabled, _ := runOut("systemctl", "--user", "is-enabled", TaskName)
	active, _ := runOut("systemctl", "--user", "is-active", TaskName)
	state := fmt.Sprintf("%s (%s/%s)", TaskName, strings.TrimSpace(string(enabled)), strings.TrimSpace(string(active)))
	linger := "unknown"
	if name := currentUser(); name != "" {
		if b, err := runOut("loginctl", "show-user", name, "--property=Linger", "--value"); err == nil {
			linger = strings.TrimSpace(string(b))
		}
	}
	switch linger {
	case "yes":
		return state + ", lingering: yes"
	case "no":
		return state + ", lingering: NO — the agent stops at logout, run: loginctl enable-linger " + currentUser()
	default:
		return state + ", lingering: " + linger
	}
}

// renderUnit fills the unit template for this machine.
func renderUnit(exe, cfgPath string) string {
	host, _ := os.Hostname()
	return fmt.Sprintf(unitTemplate, host, exe, cfgPath, environmentLines())
}

// environmentLines captures the parts of the installing shell's environment a
// systemd user unit does not inherit but the agent needs.
func environmentLines() string {
	var b strings.Builder
	// TERM is deliberately not captured: sessions always run with the TERM
	// the web console speaks (see session.baseEnv).
	for _, key := range []string{"PATH", "SHELL", "LANG"} {
		v := os.Getenv(key)
		if v == "" || strings.ContainsAny(v, "\n\r") {
			continue
		}
		fmt.Fprintf(&b, "Environment=%s=%s\n", key, v)
	}
	return b.String()
}

func userUnitDir() (string, error) {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "systemd", "user"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("agent install: home directory: %w", err)
	}
	return filepath.Join(home, ".config", "systemd", "user"), nil
}

func currentUser() string {
	if u, err := user.Current(); err == nil {
		return u.Username
	}
	return os.Getenv("USER")
}

func runOut(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}

func manualInstructions(exe, cfgPath string) string {
	return fmt.Sprintf(`No systemd found. Start the agent yourself, e.g. from your desktop session:
  %s run --config %s
or add it to whatever supervisor this system uses (runit, OpenRC, cron @reboot).
`, exe, cfgPath)
}
