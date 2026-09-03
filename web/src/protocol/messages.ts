// Mirror of internal/proto/messages.go. Keep the two in sync.

export const T = {
  // agent → relay
  AgentHello: 'agent.hello',
  Heartbeat: 'heartbeat',
  SessionOpened: 'session.opened',
  SessionState: 'session.state',
  SessionExited: 'session.exited',
  SessionClosed: 'session.closed',
  SessionUpdated: 'session.updated',
  ApprovalNew: 'approval.new',
  ApprovalClosed: 'approval.closed',
  // client → relay (→ agent)
  ClientHello: 'client.hello',
  SessionOpen: 'session.open',
  SessionAttach: 'session.attach',
  SessionDetach: 'session.detach',
  SessionResize: 'session.resize',
  SessionSignal: 'session.signal',
  SessionClose: 'session.close',
  SessionSetMode: 'session.set_mode',
  ApprovalDecide: 'approval.decide',
  // relay → client
  DeviceList: 'device.list',
  DeviceState: 'device.state',
  SessionList: 'session.list',
  ApprovalList: 'approval.list',
  Ack: 'ack',
  Error: 'error',
} as const;

export const Sig = {
  Esc: 'esc',
  CtrlC: 'ctrl_c',
  EOF: 'eof',
  KillResume: 'kill_resume',
} as const;

export type SessionState = 'running' | 'needs_input' | 'idle' | 'failed' | 'exited' | 'quota_wait' | 'unknown';
export type NeedKind = 'permission' | 'question' | '';
export type Source = 'hook' | 'hooks_json' | 'notify' | 'statusline' | 'pty_heuristic' | 'none';
export type Confidence = 'high' | 'low';
export type ApprovalMode = 'notify' | 'remote_first';
export type ApprovalLevel = 'A' | 'B' | 'C';
export type Tool = 'claude' | 'codex' | 'grok' | 'shell';

export interface Resumable {
  tool: string;
  name?: string;
  cwd?: string;
}

export interface SessionInfo {
  sid: number;
  device_id?: string;
  name: string;
  tool: Tool | string;
  shell: string;
  cwd: string;
  preset?: string;
  permission_mode?: string;
  approval_mode: ApprovalMode;
  state: SessionState;
  kind?: NeedKind;
  source: Source;
  confidence: Confidence;
  started_at: string;
  last_output_at: string;
  cols: number;
  rows: number;
  seq: number;
  cost_usd?: number;
  context_pct?: number;
  exit_code?: number;
  resumable?: Resumable;
  pty_alive: boolean;
}

export type ApprovalStatus = 'pending' | 'allowed' | 'denied' | 'closed_local' | 'fallback';

export interface Approval {
  key: string;
  sid: number;
  device_id?: string;
  agent: string;
  tool: string;
  summary: string;
  input?: unknown;
  cwd?: string;
  level: ApprovalLevel;
  mode: ApprovalMode;
  created_at: string;
  hook_timeout_at?: string;
  status: ApprovalStatus;
  decided_by?: string;
  decided_at?: string;
}

export interface DeviceInfo {
  id: string;
  name: string;
  os: string;
  agent_version?: string;
  online: boolean;
  last_seen: string;
  rtt_ms?: number;
  fingerprint?: string;
  power?: string;
}

export interface OpenRequest {
  shell: string;
  cwd?: string;
  tool: Tool | string;
  name?: string;
  preset?: string;
  permission_mode?: string;
  approval_mode?: ApprovalMode;
  resume?: string;
  cols?: number;
  rows?: number;
  extra?: string[];
}

export interface SessionHealth {
  sid: number;
  pty_alive: boolean;
  last_output_at: string;
  seq: number;
}

export interface Heartbeat {
  seq: number;
  sent_at: string;
  power?: string;
  sessions?: SessionHealth[];
}

export interface Msg {
  t: string;
  req_id?: string;
  device_id?: string;
  client_id?: string;
  sid?: number;
  agent_id?: string;
  version?: string;
  os?: string;
  caps?: string[];
  sessions?: SessionInfo[];
  session?: SessionInfo;
  devices?: DeviceInfo[];
  device?: DeviceInfo;
  approvals?: Approval[];
  approval?: Approval;
  heartbeat?: Heartbeat;
  open?: OpenRequest;
  last_seq?: number;
  cols?: number;
  rows?: number;
  sig?: string;
  mode?: ApprovalMode;
  state?: SessionState;
  kind?: NeedKind;
  source?: Source;
  confidence?: Confidence;
  code?: number;
  resumable?: Resumable;
  key?: string;
  decision?: 'allow' | 'deny';
  by?: string;
  error?: string;
}
