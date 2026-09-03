package session

import (
	"strings"

	"github.com/wfighter1/terminalX/internal/proto"
)

// ToolSpec describes how to launch an AI CLI inside the shell.
type ToolSpec struct {
	Tool           string // claude | codex | grok | shell
	Name           string // session name (claude --name)
	PermissionMode string // claude --permission-mode
	Resume         string // explicit resume target from session.open
	Extra          []string
	SettingsPath   string   // claude --settings file
	CodexNotify    []string // argv for codex -c notify=[…]
	ShellKind      string   // pwsh | powershell | cmd | sh (quoting rules)
}

// Command renders the first line typed into the shell to start the tool.
// resumeAfterKill switches to the continue form used by kill_resume.
// It returns "" for plain shell sessions.
func (t ToolSpec) Command(resumeAfterKill bool) string {
	q := func(s string) string { return QuoteArg(t.ShellKind, s) }
	var parts []string
	switch t.Tool {
	case proto.ToolClaude:
		parts = append(parts, "claude")
		if t.Name != "" {
			parts = append(parts, "--name", q(t.Name))
		}
		if t.PermissionMode != "" {
			parts = append(parts, "--permission-mode", q(t.PermissionMode))
		}
		if t.SettingsPath != "" {
			parts = append(parts, "--settings", q(t.SettingsPath))
		}
		switch {
		case resumeAfterKill && t.Name != "":
			parts = append(parts, "--resume", q(t.Name))
		case resumeAfterKill:
			parts = append(parts, "--continue")
		case t.Resume == "last" || t.Resume == "continue":
			parts = append(parts, "--continue")
		case t.Resume != "":
			parts = append(parts, "--resume", q(t.Resume))
		}
	case proto.ToolCodex:
		parts = append(parts, "codex")
		switch {
		case resumeAfterKill || t.Resume == "last":
			parts = append(parts, "resume", "--last")
		case t.Resume != "":
			parts = append(parts, "resume", q(t.Resume))
		}
		if len(t.CodexNotify) > 0 {
			parts = append(parts, "-c", q("notify="+tomlStringArray(t.CodexNotify)))
		}
	case proto.ToolGrok:
		parts = append(parts, "grok")
	default:
		return ""
	}
	for _, e := range t.Extra {
		parts = append(parts, q(e))
	}
	return strings.Join(parts, " ")
}

// tomlStringArray renders ["a","b"] with TOML basic-string escaping.
func tomlStringArray(items []string) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, s := range items {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteByte('"')
		for _, r := range s {
			switch r {
			case '\\':
				b.WriteString(`\\`)
			case '"':
				b.WriteString(`\"`)
			case '\n':
				b.WriteString(`\n`)
			default:
				b.WriteRune(r)
			}
		}
		b.WriteByte('"')
	}
	b.WriteByte(']')
	return b.String()
}

// QuoteArg quotes s for the given shell kind when necessary.
func QuoteArg(kind, s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\"'`$&|;<>()[]{}*?!\\#~^%,") {
		return s
	}
	switch kind {
	case "pwsh", "powershell":
		return "'" + strings.ReplaceAll(s, "'", "''") + "'"
	case "cmd":
		// cmd.exe passes the raw tail to the program; Windows CRT argv
		// parsing understands \" inside double quotes.
		return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	default:
		return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
	}
}
