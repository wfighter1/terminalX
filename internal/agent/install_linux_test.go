//go:build linux

package agent

import (
	"strings"
	"testing"
)

func TestRenderUnit(t *testing.T) {
	t.Setenv("PATH", "/home/u/.local/bin:/usr/bin")
	t.Setenv("SHELL", "/usr/bin/zsh")
	t.Setenv("LANG", "en_US.UTF-8")
	unit := renderUnit("/opt/tx/tx-agent", "/home/u/.config/terminalx/agent.json")

	for _, want := range []string{
		"ExecStart=/opt/tx/tx-agent run --config /home/u/.config/terminalx/agent.json",
		"Restart=always",
		"WantedBy=default.target",
		// A systemd user unit does not inherit the login shell's PATH, so an
		// agent installed without it cannot find claude/codex/grok.
		"Environment=PATH=/home/u/.local/bin:/usr/bin",
		"Environment=SHELL=/usr/bin/zsh",
	} {
		if !strings.Contains(unit, want) {
			t.Errorf("unit is missing %q:\n%s", want, unit)
		}
	}
	// A user unit must never be installed as a system one.
	if strings.Contains(unit, "multi-user.target") || strings.Contains(unit, "User=") {
		t.Errorf("unit must stay a --user unit:\n%s", unit)
	}
}

func TestEnvironmentLinesSkipsUnset(t *testing.T) {
	t.Setenv("PATH", "/usr/bin")
	t.Setenv("SHELL", "")
	t.Setenv("LANG", "")
	got := environmentLines()
	if got != "Environment=PATH=/usr/bin\n" {
		t.Fatalf("environmentLines() = %q", got)
	}
}
