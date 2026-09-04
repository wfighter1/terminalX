package hooks

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/wfighter1/terminalX/internal/proto"
)

// Claude hook event names this agent subscribes to (verified against
// https://code.claude.com/docs/en/hooks on 2026-09-03).
var ClaudeEvents = []string{
	"SessionStart", "UserPromptSubmit", "PermissionRequest", "PostToolUse",
	"Notification", "Stop", "StopFailure", "SessionEnd",
}

// CommandHookEvents are the events delivered by a command hook instead of an
// http one. SessionStart is here because http hooks are silently skipped for
// it: measured against Claude Code 2.1.260 on Linux, a settings file that
// registers BOTH an http and a command hook for every event receives the
// command hook for SessionStart and no http request at all, while every other
// event arrives over both. See docs/04 §0.5.
var CommandHookEvents = map[string]bool{"SessionStart": true}

// ClaudeInput is the union of Claude hook input fields the agent reads.
type ClaudeInput struct {
	SessionID        string          `json:"session_id"`
	PromptID         string          `json:"prompt_id,omitempty"`
	TranscriptPath   string          `json:"transcript_path,omitempty"`
	Cwd              string          `json:"cwd"`
	PermissionMode   string          `json:"permission_mode,omitempty"`
	HookEventName    string          `json:"hook_event_name"`
	ToolName         string          `json:"tool_name,omitempty"`
	ToolInput        json.RawMessage `json:"tool_input,omitempty"`
	ToolUseID        string          `json:"tool_use_id,omitempty"`
	NotificationType string          `json:"notification_type,omitempty"`
	Title            string          `json:"title,omitempty"`
	Message          string          `json:"message,omitempty"`
	ErrorType        string          `json:"error_type,omitempty"`
	ErrorMessage     string          `json:"error_message,omitempty"`
	Reason           string          `json:"reason,omitempty"`
}

// PermissionDecision is the PermissionRequest hook output
// (hookSpecificOutput.decision.behavior ∈ {allow, deny, ask}).
type PermissionDecision struct {
	HookSpecificOutput struct {
		HookEventName string `json:"hookEventName"`
		Decision      struct {
			Behavior string `json:"behavior"`
			Message  string `json:"message,omitempty"`
		} `json:"decision"`
	} `json:"hookSpecificOutput"`
}

// NewPermissionDecision builds the allow/deny output document.
func NewPermissionDecision(behavior, message string) PermissionDecision {
	var d PermissionDecision
	d.HookSpecificOutput.HookEventName = "PermissionRequest"
	d.HookSpecificOutput.Decision.Behavior = behavior
	d.HookSpecificOutput.Decision.Message = message
	return d
}

// ClaudeSettingsOptions parameterise the generated --settings file.
type ClaudeSettingsOptions struct {
	SID   uint32
	Port  int
	Token string
	// Mode is the approval mode at session start. It does not change the
	// generated file: the PermissionRequest hook always gets the
	// remote_first timeout because Claude reads --settings once, while the
	// mode can be toggled per session at runtime (session.set_mode). In
	// notify mode the endpoint answers immediately anyway.
	Mode proto.ApprovalMode
	// RemoteFirstTimeout (seconds) is the PermissionRequest hook timeout;
	// zero picks 3600. Every other hook uses NotifyTimeout (zero = 10).
	RemoteFirstTimeout int
	NotifyTimeout      int
	// AgentExe is the absolute path of the tx-agent binary. Command hooks
	// (SessionStart, statusLine) run `<AgentExe> hook …` so the machine
	// needs no curl. When empty they fall back to curl / curl.exe.
	AgentExe string
}

// ClaudeSettings renders the JSON document passed to `claude --settings`.
// Hooks are http-type wherever Claude actually delivers them, so no shell is
// involved (this avoids the Windows shell:powershell issues); the bearer
// token is referenced as $TX_HOOK_TOKEN and interpolated by Claude from the
// process environment (allowedEnvVars). The two exceptions are statusLine,
// which is command-only by definition, and SessionStart, which Claude never
// delivers over http (CommandHookEvents); both are forwarded by
// `tx-agent hook` and therefore carry the token literally in the command
// string — the settings file is written 0600.
func ClaudeSettings(o ClaudeSettingsOptions) map[string]any {
	base := fmt.Sprintf("http://127.0.0.1:%d", o.Port)
	hookFor := func(event string, timeout int) []map[string]any {
		h := map[string]any{
			"type":           "http",
			"url":            fmt.Sprintf("%s/hook/claude/%d/%s", base, o.SID, event),
			"headers":        map[string]string{"Authorization": "Bearer $TX_HOOK_TOKEN"},
			"allowedEnvVars": []string{"TX_HOOK_TOKEN"},
		}
		if timeout > 0 {
			h["timeout"] = timeout
		}
		return []map[string]any{{"hooks": []map[string]any{h}}}
	}
	permTimeout := o.RemoteFirstTimeout
	if permTimeout == 0 {
		permTimeout = 3600
	}
	otherTimeout := o.NotifyTimeout
	if otherTimeout == 0 {
		otherTimeout = 10
	}
	hooks := map[string]any{}
	for _, ev := range ClaudeEvents {
		if CommandHookEvents[ev] {
			hooks[ev] = []map[string]any{{"hooks": []map[string]any{{
				"type":    "command",
				"command": o.forwardCommand(ev),
				"timeout": otherTimeout,
			}}}}
			continue
		}
		t := otherTimeout
		if ev == "PermissionRequest" {
			t = permTimeout
		}
		hooks[ev] = hookFor(ev, t)
	}
	return map[string]any{
		"hooks":               hooks,
		"allowedHttpHookUrls": []string{base + "/hook/claude/*"},
		"statusLine": map[string]any{
			"type":    "command",
			"command": o.forwardCommand("statusline"),
		},
	}
}

// forwardCommand renders the shell command that forwards a hook payload from
// stdin to the local endpoint. It prefers `tx-agent hook` (no external
// dependency, same binary that is already running) and falls back to curl.
func (o ClaudeSettingsOptions) forwardCommand(event string) string {
	sid := fmt.Sprint(o.SID)
	if o.AgentExe != "" {
		return shJoin(o.AgentExe, "hook", "--sid", sid,
			"--port", strconv.Itoa(o.Port), "--token", o.Token, "--event", event)
	}
	url := fmt.Sprintf("http://127.0.0.1:%d/hook/claude/%s/%s", o.Port, sid, event)
	if event == "statusline" {
		url = fmt.Sprintf("http://127.0.0.1:%d/statusline/%s", o.Port, sid)
	}
	curl := "curl"
	if runtime.GOOS == "windows" {
		curl = "curl.exe"
	}
	return shJoin(curl, "-s", "-X", "POST", "--data-binary", "@-", url,
		"-H", "Authorization: Bearer "+o.Token)
}

// shJoin quotes and joins argv for the shell that runs command hooks:
// cmd.exe on Windows (no single quotes), /bin/sh elsewhere.
func shJoin(argv ...string) string {
	out := make([]string, 0, len(argv))
	for _, a := range argv {
		out = append(out, shQuote(a))
	}
	return strings.Join(out, " ")
}

func shQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\"'`$&|;<>()[]{}*?!\\#~^%,\n") {
		return s
	}
	if runtime.GOOS == "windows" {
		return `"` + strings.ReplaceAll(s, `"`, `\"`) + `"`
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// WriteClaudeSettings writes the settings file for a session under
// <root>/sessions/<sid>/claude-settings.json and returns its path.
func WriteClaudeSettings(root string, o ClaudeSettingsOptions) (string, error) {
	dir := filepath.Join(root, "sessions", fmt.Sprint(o.SID))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create session dir: %w", err)
	}
	b, err := json.MarshalIndent(ClaudeSettings(o), "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode claude settings: %w", err)
	}
	path := filepath.Join(dir, "claude-settings.json")
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}
	return path, nil
}

// StatusLineInput is the subset of the statusLine stdin JSON the agent uses.
type StatusLineInput struct {
	Cost struct {
		TotalCostUSD *float64 `json:"total_cost_usd"`
	} `json:"cost"`
	ContextWindow struct {
		UsedPercentage *float64 `json:"used_percentage"`
	} `json:"context_window"`
}

// CodexNotify is the payload Codex passes to its `notify` program as the last
// argv element (verified in codex-rs/hooks/src/legacy_notify.rs).
type CodexNotify struct {
	Type                 string   `json:"type"` // agent-turn-complete
	ThreadID             string   `json:"thread-id,omitempty"`
	TurnID               string   `json:"turn-id,omitempty"`
	Cwd                  string   `json:"cwd,omitempty"`
	InputMessages        []string `json:"input-messages,omitempty"`
	LastAssistantMessage string   `json:"last-assistant-message,omitempty"`
}
