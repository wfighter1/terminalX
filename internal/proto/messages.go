package proto

import (
	"encoding/json"
	"time"
)

// Control-plane message types. The relay forwards most agent messages to the
// interested clients (adding device_id) and most client messages to the
// agent that owns device_id.
const (
	// agent → relay
	TAgentHello     = "agent.hello"
	THeartbeat      = "heartbeat"
	TSessionOpened  = "session.opened"
	TSessionState   = "session.state"
	TSessionExited  = "session.exited"
	TSessionClosed  = "session.closed"
	TSessionUpdated = "session.updated" // metadata change (cost, ctx, mode, …)
	TApprovalNew    = "approval.new"
	TApprovalClosed = "approval.closed"

	// client → relay (→ agent)
	TClientHello    = "client.hello"
	TSessionOpen    = "session.open"
	TSessionAttach  = "session.attach"
	TSessionDetach  = "session.detach"
	TSessionResize  = "session.resize"
	TSessionSignal  = "session.signal"
	TSessionClose   = "session.close"
	TSessionSetMode = "session.set_mode"
	TApprovalDecide = "approval.decide"

	// relay → client
	TDeviceList   = "device.list"
	TDeviceState  = "device.state"
	TSessionList  = "session.list"
	TApprovalList = "approval.list"
	TAck          = "ack"
	TError        = "error"
)

// Session signals (session.signal). Windows has no SIGTERM/SIGINT; the agent
// implements these by writing to the PTY or terminating the process.
const (
	SigEsc        = "esc"         // write 0x1b
	SigCtrlC      = "ctrl_c"      // write 0x03
	SigEOF        = "eof"         // write 0x04 (Ctrl-D)
	SigKillResume = "kill_resume" // ctrl_c → /exit → terminate → respawn with resume
)

// SessionState is the coarse state of a session as shown in the console.
type SessionState string

const (
	StateRunning    SessionState = "running"
	StateNeedsInput SessionState = "needs_input"
	StateIdle       SessionState = "idle"
	StateFailed     SessionState = "failed"
	StateExited     SessionState = "exited"
	StateQuotaWait  SessionState = "quota_wait"
	StateUnknown    SessionState = "unknown"
)

// NeedKind refines StateNeedsInput.
type NeedKind string

const (
	NeedPermission NeedKind = "permission"
	NeedQuestion   NeedKind = "question"
)

// Source says which signal produced the current state.
type Source string

const (
	SourceHook       Source = "hook"          // Claude http hook
	SourceHooksJSON  Source = "hooks_json"    // Codex hooks.json
	SourceNotify     Source = "notify"        // Codex notify
	SourceStatusLine Source = "statusline"    // Claude statusLine POST
	SourcePTY        Source = "pty_heuristic" // output silence + prompt pattern
	SourceNone       Source = "none"
)

// Confidence of the current state.
type Confidence string

const (
	ConfidenceHigh Confidence = "high"
	ConfidenceLow  Confidence = "low"
)

// ApprovalMode is the per-session approval behaviour for Claude hooks.
type ApprovalMode string

const (
	// ApprovalNotify: the hook registers + pushes and returns without a
	// decision within ~10 s; the local dialog appears as usual.
	ApprovalNotify ApprovalMode = "notify"
	// ApprovalRemoteFirst: the hook blocks (up to the configured timeout)
	// waiting for a remote decision; the local dialog does not appear.
	ApprovalRemoteFirst ApprovalMode = "remote_first"
)

// ApprovalLevel tells the console what the action buttons actually do.
type ApprovalLevel string

const (
	LevelHook    ApprovalLevel = "A" // decision is returned to the hook
	LevelKeys    ApprovalLevel = "B" // buttons send keystrokes to the PTY
	LevelSuspect ApprovalLevel = "C" // low-confidence PTY heuristic; open terminal only
)

// Tools known to the agent.
const (
	ToolClaude = "claude"
	ToolCodex  = "codex"
	ToolGrok   = "grok"
	ToolShell  = "shell"
)

// Resumable describes how a session that exited can be brought back.
type Resumable struct {
	Tool string `json:"tool"`
	Name string `json:"name,omitempty"`
	Cwd  string `json:"cwd,omitempty"`
}

// SessionInfo is the agent's view of a session, forwarded to clients.
type SessionInfo struct {
	SID            uint32       `json:"sid"`
	DeviceID       string       `json:"device_id,omitempty"`
	Name           string       `json:"name"`
	Tool           string       `json:"tool"`
	Shell          string       `json:"shell"`
	Cwd            string       `json:"cwd"`
	Preset         string       `json:"preset,omitempty"`
	PermissionMode string       `json:"permission_mode,omitempty"`
	ApprovalMode   ApprovalMode `json:"approval_mode"`
	State          SessionState `json:"state"`
	Kind           NeedKind     `json:"kind,omitempty"`
	Source         Source       `json:"source"`
	Confidence     Confidence   `json:"confidence"`
	StartedAt      time.Time    `json:"started_at"`
	LastOutputAt   time.Time    `json:"last_output_at"`
	Cols           uint16       `json:"cols"`
	Rows           uint16       `json:"rows"`
	Seq            uint64       `json:"seq"` // current output stream offset
	CostUSD        *float64     `json:"cost_usd,omitempty"`
	ContextPct     *float64     `json:"context_pct,omitempty"`
	ExitCode       *int32       `json:"exit_code,omitempty"`
	Resumable      *Resumable   `json:"resumable,omitempty"`
	PTYAlive       bool         `json:"pty_alive"`
}

// Approval is a "needs you" item as shown in the inbox.
type Approval struct {
	Key           string          `json:"key"`
	SID           uint32          `json:"sid"`
	DeviceID      string          `json:"device_id,omitempty"`
	Agent         string          `json:"agent"`   // claude | codex | grok
	Tool          string          `json:"tool"`    // Bash | Write | Edit | … (hook tool_name)
	Summary       string          `json:"summary"` // command / path shown on the card
	Input         json.RawMessage `json:"input,omitempty"`
	Cwd           string          `json:"cwd,omitempty"`
	Level         ApprovalLevel   `json:"level"`
	Mode          ApprovalMode    `json:"mode"`
	CreatedAt     time.Time       `json:"created_at"`
	HookTimeoutAt *time.Time      `json:"hook_timeout_at,omitempty"` // remote_first only
	Status        string          `json:"status"`                    // pending | allowed | denied | closed_local | fallback
	DecidedBy     string          `json:"decided_by,omitempty"`
	DecidedAt     *time.Time      `json:"decided_at,omitempty"`
}

// Approval statuses.
const (
	ApprovalPending     = "pending"
	ApprovalAllowed     = "allowed"
	ApprovalDenied      = "denied"
	ApprovalClosedLocal = "closed_local" // answered in the local terminal
	ApprovalFallback    = "fallback"     // hook timed out, fell back to local dialog
)

// DeviceInfo is the relay's view of a paired agent.
type DeviceInfo struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	OS           string    `json:"os"`
	AgentVersion string    `json:"agent_version,omitempty"`
	Online       bool      `json:"online"`
	LastSeen     time.Time `json:"last_seen"`
	RTTms        int       `json:"rtt_ms,omitempty"`
	Fingerprint  string    `json:"fingerprint,omitempty"`
	Power        string    `json:"power,omitempty"`
}

// OpenRequest describes a new session (client → agent).
type OpenRequest struct {
	Shell          string       `json:"shell"`            // pwsh | powershell | cmd | bash | …
	Cwd            string       `json:"cwd,omitempty"`
	Tool           string       `json:"tool"`             // claude | codex | grok | shell
	Name           string       `json:"name,omitempty"`   // session name; mapped to --name for claude
	Preset         string       `json:"preset,omitempty"` // vendor preset id
	PermissionMode string       `json:"permission_mode,omitempty"`
	ApprovalMode   ApprovalMode `json:"approval_mode,omitempty"`
	Resume         string       `json:"resume,omitempty"` // resume target (name/id) for claude/codex
	Cols           uint16       `json:"cols,omitempty"`
	Rows           uint16       `json:"rows,omitempty"`
	Extra          []string     `json:"extra,omitempty"` // extra CLI args
}

// SessionHealth is reported in heartbeats.
type SessionHealth struct {
	SID          uint32    `json:"sid"`
	PTYAlive     bool      `json:"pty_alive"`
	LastOutputAt time.Time `json:"last_output_at"`
	Seq          uint64    `json:"seq"`
}

// Heartbeat payload (agent → relay).
type Heartbeat struct {
	Seq      uint64          `json:"seq"`
	SentAt   time.Time       `json:"sent_at"`
	Power    string          `json:"power,omitempty"` // ac | battery | unknown
	Sessions []SessionHealth `json:"sessions,omitempty"`
}

// Msg is the single control-plane envelope. Fields are optional and which
// ones are present depends on T. Keeping one flat struct keeps the Go and
// TypeScript sides trivially in sync.
type Msg struct {
	T        string `json:"t"`
	ReqID    string `json:"req_id,omitempty"`
	DeviceID string `json:"device_id,omitempty"`
	ClientID string `json:"client_id,omitempty"`
	SID      uint32 `json:"sid,omitempty"`

	// agent.hello
	AgentID string   `json:"agent_id,omitempty"`
	Version string   `json:"version,omitempty"`
	OS      string   `json:"os,omitempty"`
	Caps    []string `json:"caps,omitempty"`

	// lists / single objects
	Sessions  []SessionInfo `json:"sessions,omitempty"`
	Session   *SessionInfo  `json:"session,omitempty"`
	Devices   []DeviceInfo  `json:"devices,omitempty"`
	Device    *DeviceInfo   `json:"device,omitempty"`
	Approvals []Approval    `json:"approvals,omitempty"`
	Approval  *Approval     `json:"approval,omitempty"`
	Heartbeat *Heartbeat    `json:"heartbeat,omitempty"`

	// session.open
	Open *OpenRequest `json:"open,omitempty"`

	// session.attach / resize / signal / set_mode
	LastSeq uint64       `json:"last_seq,omitempty"`
	Cols    uint16       `json:"cols,omitempty"`
	Rows    uint16       `json:"rows,omitempty"`
	Sig     string       `json:"sig,omitempty"`
	Mode    ApprovalMode `json:"mode,omitempty"`

	// session.state
	State      SessionState `json:"state,omitempty"`
	Kind       NeedKind     `json:"kind,omitempty"`
	Source     Source       `json:"source,omitempty"`
	Confidence Confidence   `json:"confidence,omitempty"`

	// session.exited
	Code      *int32     `json:"code,omitempty"`
	Resumable *Resumable `json:"resumable,omitempty"`

	// approval.decide / approval.closed
	Key      string `json:"key,omitempty"`
	Decision string `json:"decision,omitempty"` // allow | deny
	By       string `json:"by,omitempty"`       // local | web:<client> | relay

	// ack / error
	Error string `json:"error,omitempty"`
}

// Encode marshals a control message to JSON.
func (m Msg) Encode() ([]byte, error) { return json.Marshal(m) }

// Decode unmarshals a control message from JSON.
func Decode(b []byte) (Msg, error) {
	var m Msg
	err := json.Unmarshal(b, &m)
	return m, err
}
