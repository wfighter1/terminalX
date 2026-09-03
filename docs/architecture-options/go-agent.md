# terminalX 第一阶段技术架构（Go Agent + Go 中转 + React/xterm.js）

> 日期：2026-09-03 ｜ 角色：架构师 ｜ 范围：第一阶段「Web 页面 → 自建中转 → Windows 被控端 → 终端 / AI CLI」，并为多端（手机 PWA、macOS/Linux 被控端、原生壳）预留扩展点。
> 依据：`docs/research/04`、`docs/research/05`、三份设计方案与三份红队结论；本日用 WebFetch / GitHub 代码搜索 / Go module proxy / npm & NuGet 注册表复核了关键事实（本会话 WebSearch 配额已耗尽，未能复核的条目一律标「待核实」）。
> 一句话：**Agent 只做「会话宿主 + 字节搬运 + 事件登记」，中转只做「路由 + 元数据」，AI 感知优先走 hooks / notify 等结构化信号，PTY 文本解析永远是带置信度标注的兜底，不作为任何自动决策的依据。**

---

## 0. 选型结论与核实结果

| 层 | 选型 | 核实（2026-09-03） |
|---|---|---|
| 被控端 Agent | Go 1.25+，单 exe，`GOOS=windows GOARCH=amd64/arm64` 交叉编译 | `golang.org/x/sys` v0.47.0（2026-06-30）已导出 `CreatePseudoConsole / ResizePseudoConsole / ClosePseudoConsole` 与 `ProcThreadAttributeListContainer`，**不需要 cgo** 即可直接调 ConPTY |
| ConPTY 封装 | 自写 `internal/conpty`（约 300 行），参考 `charmbracelet/x/conpty` v0.2.0（2025-11-17，`New/Spawn/Resize/Read/Write`，走 x/sys）与 `UserExistsError/conpty`（43★，MIT，`Start/Resize/Wait/Pid`，含 `IsConPtyAvailable()` 检查） | 两个 Go 库都是「薄封装」，成熟度远低于 node-pty / portable-pty；自写能控制 env、cwd、句柄继承与可选自带 `conpty.dll`（NuGet `Microsoft.Windows.Console.ConPTY` 最新 1.25.260710002-preview） |
| WebSocket | `github.com/coder/websocket` v1.8.15（2026-06-15，ISC，nhooyr 的官方后继，context 原生、permessage-deflate） | 中转与 Agent 共用 |
| 中转存储 | `modernc.org/sqlite` v1.58.0（2026-09-01，纯 Go 无 cgo） | 单文件、单二进制 |
| Web 登录 | `github.com/go-webauthn/webauthn` v0.18.0（2026-08-27，BSD-3，FIDO2/Passkey）；TOTP 备选 `pquerna/otp` v1.5.0（2024-12-31） | Passkey 为主，TOTP 为无平台认证器时的降级 |
| Windows 服务 | `kardianos/service` v1.3.0（2026-07-06，zlib） | **MVP 不用**；仅在 1.1 作看门狗（见 §4） |
| 前端 | React 18 + TypeScript + `@xterm/xterm` 6.0.0（npm latest；6.1.0-beta 在推进）+ addon-fit / webgl / search / serialize | 见 `research/05 §2` |
| 可选直连（第二阶段） | `pion/webrtc/v4` v4.2.19（2026-08-27，纯 Go） | 浏览器天然具备 DataChannel |

**为什么 Go 而不是研究 05 推荐的 Rust**：Rust 的 `portable-pty` 确实更成熟，但 (1) x/sys 已原生导出 ConPTY 五个函数，Go 侧封装薄的代价是「自己写 300 行」而非「不可行」；(2) 中转、Agent、协议编解码同语言可共享 `internal/proto`；(3) 单二进制、交叉编译、无 cgo 的 SQLite/WebSocket/WebAuthn 全部齐全；(4) 团队更熟 Go 时开发速度优先。代价：Windows 上 Go 二进制的 Defender 误报案例更多（golang/go #51514 等），必须签名（§4.6）。

---

## 1. 组件图与职责

```
┌──────────────────────────┐  HTTPS/WSS(443, 出站)  ┌──────────────────────────┐  WSS(443, 出站)  ┌──────────────────────────────────┐
│  Web 控制台 (PWA)         │ ─────────────────────▶ │  中转服务端 relay (Go)     │ ◀─────────────── │  Windows Agent (Go, 用户会话内)     │
│  React + TS + xterm.js 6  │ ◀───────────────────── │  · 设备/用户/配对/审批元数据 │ ───────────────▶ │  · ConPTY 会话宿主 (pwsh 默认)      │
│  · /inbox 收件箱 (首页)    │   JSON 控制帧 + 二进制  │  · 按 device_id 路由帧      │  同左            │  · 环形缓冲 + 单调序号              │
│  · /devices /sessions     │   数据帧 (payload 不透明) │  · 心跳、离线元数据、通知出站 │                  │  · 本地 hook 端点 127.0.0.1:随机口   │
│  · /t/<id> 终端 (桌面/手机)│                        │  · SQLite 单文件            │                  │  · notify 子命令 (Codex)           │
│  · /settings 配对/通知/账户│                        │  · Passkey/TOTP 登录        │                  │  · 供应商预设 env 注入 (不经中转)    │
└──────────────────────────┘                        └──────────────────────────┘                  └──────────────────────────────────┘
        ▲ 通知：ntfy / Bark / 飞书 webhook（中转出站）                                                          │ ConPTY
                                                                                                             ▼
                                                                                              pwsh → claude / codex / 其他 CLI
                                                                                              ▲ http hook / notify → Agent 本地端点
   （可选，1.1）本地直连：浏览器与 Agent 同一局域网时，Agent 监听 127.0.0.1 / 局域网口（默认关闭），Web 端「直连」按钮改连 ws://<lan-ip>，协议完全相同
```

| 组件 | 职责 | 明确不做 |
|---|---|---|
| Web 控制台 | 渲染终端（xterm.js）、收件箱、会话/设备列表、审批按钮、手机键条 + composer 输入框、PWA | 不解析终端文本、不存任何密钥到中转 |
| 中转 relay | 用户登录、设备配对与吊销、帧路由（不解析 payload）、审批/会话元数据、心跳与在线态、通知出站、静态托管前端 | 不执行命令、不存终端内容、不做模型链路 |
| Windows Agent | 起 / 持有 PTY，环形缓冲，序号补差，接收 hooks / notify 事件，注入环境变量（供应商预设、`CLAUDE_CODE_NO_FLICKER=1` 等），远程解卡（Esc/Ctrl-C/kill&resume） | 不开入站端口（hooks 端点仅 127.0.0.1）、不解析屏幕做自动决策、不代替用户配置 `~/.claude/settings.json`（默认用 `--settings` 注入） |
| 本地直连（可选） | 同协议、同帧格式的第二条链路 | MVP 不做，只保证协议层无「中转专属」假设 |

多端扩展点：控制端只是「一个实现了协议的 WebSocket 客户端」，手机 PWA、Tauri 桌面、原生壳共用 `web/packages/protocol`；被控端只是「一个实现了协议的 PTY 宿主」，macOS/Linux 换 `internal/pty` 的实现（`creack/pty`）即可。

---

## 2. 协议设计

### 2.1 总体

- 一台 Agent 只维持 **一条** 出站 WSS 到中转（`/ws/agent`），一个浏览器标签只维持一条（`/ws/client`），所有会话在这条连接上复用。
- **控制面用 JSON 文本帧**（低频、可读、易演进），**数据面用二进制帧**（高频、零解析）。中转只读二进制帧的固定帧头做路由，不看 payload。
- 每个会话有 Agent 侧的 `session_id`（UUID）和帧头里的 `sid`（uint32，连接内局部索引，节省帧头）。
- 帧 payload 从第一天起视为 **不透明字节**：MVP 为明文（TLS 保护），1.1 起改为 AES-256-GCM 密文，帧格式不变。

### 2.2 二进制数据帧（Agent ↔ Client，经中转透传）

```
offset  size  字段
0       1     type   0x01 OUTPUT (Agent→Client)  0x02 INPUT (Client→Agent)
                     0x03 RESIZE 0x04 ACK       0x05 SNAPSHOT_BEGIN 0x06 SNAPSHOT_END
1       1     flags  bit0 = payload 已加密(1.1)  bit1 = 压缩
2       4     sid    uint32 BE，连接内会话索引
6       8     seq    uint64 BE，单调序号（OUTPUT 由 Agent 分配；INPUT 由客户端分配）
14      ...   payload   OUTPUT/INPUT: 原始字节；RESIZE: cols(u16) rows(u16)；ACK: 已收到的最后 seq(u64)
```

Go 编解码骨架（`internal/proto/frame.go`）：

```go
type FrameType byte
const (Output FrameType = 0x01; Input = 0x02; Resize = 0x03; Ack = 0x04; SnapBegin = 0x05; SnapEnd = 0x06)

type Frame struct { Type FrameType; Flags byte; SID uint32; Seq uint64; Payload []byte }

func (f *Frame) Encode() []byte {
    b := make([]byte, 14+len(f.Payload))
    b[0], b[1] = byte(f.Type), f.Flags
    binary.BigEndian.PutUint32(b[2:], f.SID)
    binary.BigEndian.PutUint64(b[6:], f.Seq)
    copy(b[14:], f.Payload)
    return b
}
```

中转路由只需 `sid → (device_id, session_id)` 映射，映射由控制面的 `session.attach` 建立。

### 2.3 控制面消息（JSON 文本帧，`{"t":"<type>","id":"<req-id>",...}`）

| 方向 | 类型 | 关键字段 | 说明 |
|---|---|---|---|
| Agent→Relay | `agent.hello` | `device_id, device_token, agent_version, os, hostname, caps:["pty","hooks","notify"]` | 连接后第一帧；token 错即关闭（fail-closed） |
| Relay→Agent | `agent.welcome` | `server_time, heartbeat_sec:15, pending:[...]` | 回放离线期间未处理的控制指令（如 kill/resize） |
| 双向 | `ping` / `pong` | `ts` | 15 s 心跳，3 次未答判离线 |
| Agent→Relay | `session.list` | `sessions:[{session_id,name,shell,cwd,pid,state,last_output_at,started_at,tool,cost_usd?,ctx_pct?}]` | 连接建立与状态变化时全量/增量上报 |
| Client→Relay→Agent | `session.open` | `device_id, shell:"pwsh", cwd, name, env_preset?, cols, rows` | Agent 回 `session.opened{session_id, sid}` |
| Client→Agent | `session.attach` | `session_id, last_seq` | 建立 sid 映射；Agent 决定「补差」或「快照」 |
| Agent→Client | `session.replay_plan` | `mode:"delta"｜"snapshot", from_seq, to_seq` | 客户端据此清屏或直接追加 |
| Client→Agent | `session.detach` / `session.kill` / `session.signal{sig:"esc"｜"ctrl_c"｜"ctrl_d"}` | | 远程解卡三件套；`kill` 二次确认 |
| Agent→Relay | `session.state` | `session_id, state, source:"hook"｜"notify"｜"pty", confidence:0-1, at` | 见 §5 |
| Agent→Relay | `approval.new` | `approval_id, session_id, tool, tool_use_id, input_preview, cwd, mode:"notify_only"｜"remote_first", expires_at` | hook 登记 |
| Client→Relay→Agent | `approval.decide` | `approval_id, behavior:"allow"｜"deny"｜"dontAsk"` | 仅 `remote_first` 模式会回写 hook |
| Agent→Relay | `approval.closed` | `approval_id, reason:"answered_local"｜"answered_remote"｜"timeout"` | 本机答过 ≤2 s 自动关闭 |
| Client→Relay | `pair.begin` / Agent→Relay `pair.claim{code, agent_pubkey}` / Relay→Client `pair.confirm{fingerprint}` | | §3.2 |

### 2.4 会话保持与重连回放

- **PTY 生命周期与连接解耦**：会话由 Agent 持有；浏览器关闭 = detach；显式 `session.kill` 才结束进程；Agent 崩溃/重启后 PTY 随之死亡，会话进入 `exited` 并落盘最后 4 MB 输出快照（`%LOCALAPPDATA%\terminalX\sessions\<id>.tail`），列表提供「拉回」= 执行记录中的恢复命令（`claude --resume <session_id>` / `codex resume <thread_id>`，id 来自 hook / notify 登记）。
- **环形缓冲 + 序号**：每会话默认 4 MB（可配 1–16 MB），记录 `(seq, offset)` 索引。客户端 `attach` 带 `last_seq`：在缓冲范围内 → `delta` 模式只补差；超出范围或首次打开 → `snapshot` 模式：Agent 发 `SNAPSHOT_BEGIN`，客户端 `term.reset()`，再回放缓冲尾部（默认最后 256 KB），`SNAPSHOT_END` 后进入实时流。1.1 可选在 Agent 侧跑 VT 模拟器生成精确屏幕快照（Go 侧候选 `charmbracelet/x/vt`，成熟度待核实），MVP 不依赖。
- **多端同时附着**：输出广播、输入合并；尺寸取最后一次 `RESIZE` 的发送者（tmux `aggressive-resize` 思路）；Claude 会话固定 PTY ≥100 列，手机端横向滚动而不是缩列（Ink 在窄列下有滚动区重复问题，claude-code #51828 Open）。
- **重连退避**：客户端 2 s→16 s 指数退避，连上 10 s 后归零；Agent 侧同样退避并支持 `HTTPS_PROXY`。
- **三信号分离显示**：中转↔Agent 心跳、Agent↔PTY 进程存活、PTY 最近输出时间，任一陈旧即显示 `Unknown`，绝不合并成一个「已连接」。

---

## 3. 安全设计

### 3.1 网络边界

- Agent **只出站 443**（WSS），本机仅监听 `127.0.0.1:<随机端口>` 供 hooks / notify 回调，端口写入 `agent.toml` 并对回调校验一次性 `hook_token`（写入注入的 hooks 配置的 `headers`）。验收：`netstat -ano` 无 `0.0.0.0` 监听。
- 中转只开 443（TLS：`autocert` 或前置 Caddy），无登录不可访问任何页，无 `device_token` 的 `/ws/agent` 直接关闭。
- 中转日志只含 device/session/字节数/时间，**不含终端内容**。

### 3.2 设备配对（配对码 / 二维码）

1. Web 端登录后点「配对新设备」，中转生成 `code`（8 位，`ABCD-1234`，5 分钟、一次有效、错 5 次锁 15 分钟）及二维码（内容 `terminalx://pair?relay=wss://…&code=…&relay_fp=<中转证书/公钥指纹>`）。
2. Windows 上执行 `terminalx-agent pair wss://relay.example.com ABCD-1234`（或扫码后复制命令）。Agent 本地生成 **X25519 静态密钥对**，`pair.claim` 携带公钥。
3. 中转向 Web 端推 `pair.confirm{fingerprint}`；两端各自显示由 `SHA-256(agent_pub ‖ client_pub ‖ code)` 派生的 **6 位短码**，用户目视比对后点确认（防中转伪造公钥，方案 C 的「指纹目视比对」）。
4. 中转签发长期 `device_token`（随机 32 字节，仅存哈希），Agent 写入 `%LOCALAPPDATA%\terminalX\agent.toml`（DPAPI 加密）。吊销 = 删哈希，Agent 下一次心跳即被关闭（≤15 s）。

### 3.3 端到端加密（协议已预留，实装为 1.1 / 对外托管前硬门槛）

- 密钥协商：每个控制端（浏览器）在首次登录时用 WebCrypto 生成 X25519 密钥对（私钥 `extractable:false` 存 IndexedDB）；配对时与 Agent 交换公钥（§3.2 已完成），双方 `HKDF-SHA256(X25519(sk, pk), salt=code, info="terminalx-v1")` 派生根密钥，再按方向派生 `k_a2c / k_c2a`。
- 逐帧 AES-256-GCM：`nonce = sid(4) ‖ seq(8)`（序号单调保证不重用），AAD = 帧头 14 字节；中转只见帧头。
- **fail-closed**：解密失败立即断开并在 UI 显示「密钥不匹配」，绝不回退明文（吸取 RustDesk 验签失败继续握手的教训）。
- 诚实声明：Web 前端 JS 由中转分发，托管形态下「零知识」对用户不可验证（红队 3）；因此 E2E 的定位是「自建单人时的正确默认值」，README 数据流向图如实标注。

### 3.4 Web 登录

- 首启打印一次性初始密码 → 强制注册 **Passkey**（go-webauthn，discoverable credential，RP ID = 中转域名）；无平台认证器时退化为「密码 + TOTP」。
- 会话 cookie `HttpOnly; Secure; SameSite=Strict`，7 天；敏感操作（配对、吊销、修改通知通道）要求 ≤18 小时内的认证，否则触发 Passkey 步进（对齐官方 Trusted Devices 的 18 小时口径，个人版不做组织策略）。

### 3.5 审计（元数据，一张表）

`audit(ts, actor, device_id, session_id, action, detail_json)`，记录：登录、配对、吊销、会话开关、`approval.decide`、远程 signal/kill。不记录终端内容、不做回放。

### 3.6 产品硬规则

- **远程 ≤ 本地**：Agent 不叠加自己的审批层；`bypassPermissions` 会话根本不触发 `PermissionRequest` hook（官方 hooks 文档），远程端零弹窗（验收 B5）。
- 供应商预设（官方 / 中转站 `ANTHROPIC_BASE_URL`+Key / MiniMax / Bedrock 变量）在 **Agent 本机** 注入子进程环境，明文不经中转；Web 端只选预设名。

---

## 4. Windows 细节

### 4.1 ConPTY 实现要点（`internal/conpty`）

```go
// 伪代码：直接使用 golang.org/x/sys/windows，无 cgo
size := windows.Coord{X: int16(cols), Y: int16(rows)}
windows.CreatePipe(&inR, &inW, nil, 0); windows.CreatePipe(&outR, &outW, nil, 0)
windows.CreatePseudoConsole(size, inR, outW, 0, &hpc)
attrs, _ := windows.NewProcThreadAttributeList(1)
attrs.Update(windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE, unsafe.Pointer(hpc), unsafe.Sizeof(hpc))
si := windows.StartupInfoEx{ProcThreadAttributeList: attrs.List()} // 不设 STARTF_USESTDHANDLES，句柄由 ConPTY 接管
windows.CreateProcess(nil, cmdline, nil, nil, false, windows.EXTENDED_STARTUPINFO_PRESENT|windows.CREATE_UNICODE_ENVIRONMENT, envBlock, cwd, &si.StartupInfo, &pi)
// 关闭子进程侧 inR/outW；读 outR 循环写入环形缓冲；Resize → ResizePseudoConsole
```

- 最低 Windows 10 1809 / build 18309（与 Claude Code 原生 Windows 要求一致）；启动时 `IsConPtyAvailable()` 检查。
- `ClosePseudoConsole` 会杀掉子进程且可能阻塞在未读完的输出上：先 `TerminateProcess`，再排空 `outR`，最后 `ClosePseudoConsole`（node-pty 的 teardown 教训）。
- 可选自带 `conpty.dll + OpenConsole.exe`（NuGet 1.25.260710002-preview）作第二路径：系统 ConPTY 默认、异常时切换；node-pty #894 证明自带 DLL 在 pwsh 下首屏可能慢 3 s，所以默认走系统路径并把首屏 ≤1 s 纳入验收。
- 前端 xterm.js 打开 `windowsPty: {backend:'conpty', buildNumber}` 启发式。
- Claude 会话注入 `CLAUDE_CODE_NO_FLICKER=1`、`CLAUDE_CODE_ALT_SCREEN_FULL_REPAINT=1`（ConPTY 宿主的定位写入合并问题，官方 fullscreen 文档口径）。

### 4.2 默认 shell

`pwsh.exe`（PowerShell 7，`-NoLogo`）→ 找不到则 `powershell.exe`（5.1）；`cmd.exe` 作可选项；Git Bash / WSL 第一阶段「尽力支持、不进验收」（WSL 内再起 Win32 程序有 microsoft/terminal #17822 的 3 s 卡顿，禁止混合方案）。

### 4.3 以服务运行 vs 用户会话运行（长期约束，不是 1.1 待办）

- AI CLI 的登录凭据、`~/.claude`、npm 全局目录、`%USERPROFILE%\.codex` 全在用户 profile 下；shell 必须以 **登录用户 token 在用户会话内** 运行。
- Windows 服务跑在 Session 0，若要替用户起 ConPTY 需 `WTSQueryUserToken + CreateProcessAsUser`，而 ConPTY 与 token 类 API 组合的 microsoft/terminal **#11865 自 2021-12 至今 Open、无 assignee**。Win32-OpenSSH 的 sshd 能做到是因为它自己实现了整套登录 + 桌面切换，代价高。
- **MVP 决策：用户态自启。** `terminalx-agent install` 注册任务计划程序：触发器「登录时」、`/RL LIMITED`（不提权）、失败后 1 分钟重启、不限制运行时长、电池供电也运行。备选注册表 `HKCU\...\Run`。
- **1.1（可选）双进程**：`kardianos/service` 装一个只做看门狗的服务（检测用户会话内 Agent 是否存活、拉起 `agent.exe --pty-host`），PTY 永远在用户会话内；前提是机器开启自动登录 —— 安装向导明示「重启后自动拉回会话需要自动登录 + 关闭睡眠」。

### 4.4 开机自启与「重启后拉回」

登录 → 任务计划拉起 Agent（目标 ≤60 s 上线）→ 读取 `sessions/*.json` 中标记为 `auto_resume` 的会话 → 对 Claude 会话执行 `claude --resume <id>`、Codex `codex resume <thread_id>`（id 来自 hook `session_id` / notify `thread-id`），先在 UI 标 `exited → resuming`，成功后重新登记。

### 4.5 防火墙 / Defender

- 只出站，不需要任何防火墙入站规则；企业网络下支持 `HTTPS_PROXY`。
- Defender / SmartScreen：Go 二进制误报有历史案例 → **Authenticode 签名**（Azure Trusted Signing 或 OV 证书，价格待核实）、不用 UPX、提交误报样本、README 说明。

### 4.6 安装包分发

- 主形态：单 exe（`terminalx-agent.exe`）+ 子命令 `pair / install / uninstall / notify / status / hooks install`，自解压式「安装」只是复制到 `%LOCALAPPDATA%\terminalX\bin` 并注册任务计划。
- 次形态：`winget` manifest（需签名）；MSI/Inno 暂不做。
- 自更新：Agent 启动时查中转 `/api/agent/latest`，下载校验 SHA-256 + 签名后替换并重启（1.1）。

---

## 5. AI 感知层（hooks / notify 优先，PTY 兜底）

### 5.1 状态模型

`running`（有工具/模型活动）→ `waiting`（等审批 / 等输入）→ `idle`（turn 完成、无待办）→ `exited`；外加 `unknown`（信号陈旧 > 5 分钟或来源冲突）。每次状态变更带 `source` 与 `confidence`：hook/notify = 1.0（A/B 级）、PTY 启发式 ≤ 0.6（C 级）；UI 用不同图标区分，C 级状态永远带「?」。

### 5.2 Claude Code（A 级：hooks + statusLine）

**注入方式**：terminalX 拉起的会话用 `claude --settings %LOCALAPPDATA%\terminalX\claude-hooks.json`（官方 CLI 文档：`--settings` 接受文件路径或内联 JSON，覆盖同名键、其余键保留），**不改用户 `~/.claude/settings.json`**；用户自己在 Windows Terminal / psmux 里起的会话，提供 `terminalx-agent hooks install`（展示 diff、可回滚）作为「免包装器附着」。

```json
{
  "hooks": {
    "SessionStart":      [{"hooks":[{"type":"http","url":"http://127.0.0.1:PORT/hook/claude","headers":{"X-TX-Token":"..."},"timeout":5}]}],
    "UserPromptSubmit":  [{"hooks":[{"type":"http","url":"http://127.0.0.1:PORT/hook/claude","timeout":5}]}],
    "PermissionRequest": [{"hooks":[{"type":"http","url":"http://127.0.0.1:PORT/hook/claude","timeout":10}]}],
    "Notification":      [{"matcher":"permission_prompt|idle_prompt|agent_needs_input|agent_completed","hooks":[{"type":"http","url":"http://127.0.0.1:PORT/hook/claude","timeout":5}]}],
    "Stop":              [{"hooks":[{"type":"http","url":"http://127.0.0.1:PORT/hook/claude","timeout":5}]}],
    "SessionEnd":        [{"hooks":[{"type":"http","url":"http://127.0.0.1:PORT/hook/claude","timeout":1}]}]
  },
  "statusLine": {"type":"command","command":"terminalx-agent statusline"}
}
```

- 官方口径（本日核实 hooks 文档）：hook 默认阻塞执行；`command/http/mcp_tool` 默认超时 **600 s**；超时后「discarding the hook's output… the permission flow proceeds unchanged」；`PermissionRequest` 的回写 schema 是 `hookSpecificOutput.decision.behavior ∈ allow|deny|ask|dontAsk`，**Exit code 2 不生效**。
- **审批通道两种模式**（红队 3 的核心修正）：
  - 默认 `notify_only`：`PermissionRequest` hook `timeout:10`，Agent 登记 Approval + 推送后 **返回空对象不作决定**，原生对话框照常出现；手机端卡片按钮是「打开终端」+ 键条 `1/2/Enter`。
  - 每会话可开 `remote_first`：hook `timeout` 设为 3600，Agent 挂起直到 `approval.decide` 到达再回写 `{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"allow"}}}`；UI 明示「开启期间终端内对话框不出现」。
  - 实验路径：官方 Channels permission relay（`notifications/claude/channel/permission_request` → `notifications/claude/channel/permission{request_id, behavior}`，「Both stay live… whichever answer arrives first」），但研究预览期需 `--dangerously-load-development-channels`，且在自定义 `ANTHROPIC_BASE_URL` 下是否可用 **待核实**；作为 1.1 spike。
- **去重与自动关闭**：Approval 以 `tool_use_id` 为键；收到同会话的 `PostToolUse`/`Stop`/`Notification` 或 PTY 出现新提示行时判定 `answered_local` 并关闭（≤2 s）。
- statusLine：`terminalx-agent statusline` 读 stdin JSON，只上报 `cost.total_cost_usd` 与 `context_window.used_percentage`（300 ms 去抖，可能为 `null`）；`rate_limits` 仅 Pro/Max 或 apps gateway 才有，UI 有则显示、无则不占位。
- Windows 可靠性风险：#88896（PreToolUse 在 Windows 不触发）、#90077（`shell: powershell` 无 `powershell.exe` 回退）、#88698（`--bg` 会话丢弃 PermissionRequest 决定）均 Open —— 所以 **第 1 周 spike 必须实测 http 型 PermissionRequest / Notification 在原生 Windows 上是否触发与回写**，不通过则该会话自动降级为 PTY 兜底并在 UI 标注。

### 5.3 Codex CLI（B 级：notify；一键审批为 1.1 spike）

- `~/.codex/config.toml` 写 `notify = ["C:\\...\\terminalx-agent.exe", "notify"]`。本日核实源码 `codex-rs/hooks/src/legacy_notify.rs` 与 `tui/src/chatwidget/notifications.rs`：payload `{"type":"agent-turn-complete","thread-id":..,"turn-id":..,"cwd":..,"input-messages":[..],"last-assistant-message":..}`，另有 `type:"approval-requested"`（Exec/Edit/Elicitation 三种审批）。notify 只通知、不能阻塞。
- `hooks.json`（`~/.codex/hooks.json`，Claude 风格事件，含 `PermissionRequest`、默认 `timeout_sec: 600`）在源码中挂在 feature flag `CodexHooks` 之后，**是否默认开启待核实**；MVP 只用 notify。
- 一键审批：app-server 的 WebSocket 传输 README 明写「experimental and unsupported」；可信路径是本地 unix socket（`$CODEX_HOME/app-server-control/app-server-control.sock`，「also supported on Windows」，目录带 current-user-only DACL）以第二客户端接入 daemon，决策值 `accept | acceptForSession | decline | cancel`（`CommandExecutionApprovalDecision` / `FileChangeApprovalDecision`）。代价是 Windows daemon 尚有 Open issue（#42507 等），且托管会话非原生 TUI —— 1.1 spike，MVP 只做「通知 + 打开终端 + 键条 y/n」。

### 5.4 其他工具（C 级：PTY 启发式，只显示不决策）

- 输入：最近输出时间、最后一屏文本（Agent 侧维护最后 N 行）。
- 规则：静默 > 3 s 且最后非空行匹配工具专属提示正则（如 Claude `Do you want to proceed`、`❯ 1. Yes`；Codex `Allow command?`；通用 shell 提示符 `PS .*> $`）→ `waiting`(0.5) / `idle`(0.4)；有输出 → `running`(0.6)；进程退出 → `exited`(1.0)。
- 绝不据此注入按键；只用于列表图标与「可能在等你」的低优先级通知（合并去抖 60 s）。

### 5.5 统一事件与手机推送

```
Agent 事件 → session.state / approval.new → 中转 events 表 → 通知出站（单一通用 webhook 模板，预置 ntfy / Bark / 飞书）
规则：approval.new 即时推（去重 key = tool_use_id）；idle/agent_completed 去抖 60 s 合并推；exited 与 Agent 离线即时推；C 级状态默认不推
深链：https://relay/inbox?approval=<id>（PWA 加到主屏时直达卡片）；iOS 无通知动作按钮，Android 可加「打开」动作（Web Push 放 1.1）
```

---

## 6. 目录结构与代码骨架（monorepo）

```
terminalX/
├── go.mod                       # 单模块：github.com/<you>/terminalx
├── cmd/
│   ├── agent/main.go            # 子命令：run | pair | install | uninstall | notify | statusline | hooks | status
│   └── relay/main.go            # 子命令：serve | user add | migrate
├── internal/
│   ├── proto/                   # 帧编解码、控制面消息类型（Go）；`go generate` 输出 TS 类型到 web/packages/protocol
│   ├── conpty/                  # Windows ConPTY（x/sys），unix 版用 creack/pty（//go:build 分文件）
│   ├── session/                 # 会话管理：环形缓冲、序号、快照落盘、resume 记录
│   ├── hooks/                   # 127.0.0.1 HTTP 端点：/hook/claude、/notify/codex、状态机与置信度
│   ├── transport/               # coder/websocket 客户端（Agent）与服务端（relay），重连退避、心跳
│   ├── crypto/                  # X25519 + HKDF + AES-GCM 帧加密（1.1 启用），配对短码
│   ├── relay/{auth,pairing,router,store,notify}/   # WebAuthn/TOTP、配对、路由、SQLite、通知出站
│   └── winsvc/                  # 任务计划注册、DPAPI、（1.1）kardianos/service 看门狗
├── web/
│   ├── apps/console/            # React + TS + Vite，PWA；路由 /inbox /devices /sessions /t/:id /settings
│   ├── packages/protocol/       # 由 internal/proto 生成的 TS 类型 + 帧编解码
│   └── packages/terminal/       # xterm.js 6 封装：attach/replay、手机键条、composer、visualViewport
├── deploy/{Dockerfile,docker-compose.yml,Caddyfile}
├── scripts/{build-windows.ps1,sign.ps1,acceptance/*.ps1}
└── docs/
```

关键接口骨架：

```go
// internal/session/session.go
type Session struct {
    ID string; SID uint32; Shell string; Cwd string
    pty  conpty.Pty            // Read/Write/Resize/Close/Pid/Wait
    ring *RingBuffer           // Write(seq, data); ReadFrom(seq) ([]byte, ok)
    seq  atomic.Uint64
    state StateMachine         // Apply(Event{Source, Kind, At}) → State{Name, Confidence}
    resume ResumeRecord        // Tool, SessionID/ThreadID, Cmd []string
}
// internal/transport/agent.go
type AgentConn interface { SendFrame(proto.Frame) error; SendControl(any) error; OnFrame(func(proto.Frame)); OnControl(func(json.RawMessage)) }
// internal/hooks/server.go
func (s *Server) HandleClaude(w http.ResponseWriter, r *http.Request) // 按 hook_event_name 分派；PermissionRequest 按会话模式决定是否挂起
```

中转服务端与 Agent 共用 `internal/proto`，前端类型由 `go generate`（tygo 或手写模板）导出，避免帧格式漂移。

---

## 7. 第一阶段里程碑（8–10 周，一人）与验收

| 周 | 交付 | 验收 / 降级触发 |
|---|---|---|
| 1（三个 spike，任一失败即触发降级而非顺延） | ① `internal/conpty` 起 pwsh 并在本地 xterm.js 渲染 `claude` TUI；② 原生 Windows 上 http 型 `PermissionRequest`/`Notification` 实测触发与回写（20 行脚本）；③ Android 真机 composer 发 50 个中文字符到 PTY | ① 首屏 ≤1 s，`seq 1 20000` 逐字节一致；② 不通过 → 审批只做 PTY 兜底 + 打开终端；③ 不通过 → 手机端只承诺看 + 键条 |
| 2 | Agent：会话管理、环形缓冲、序号、快照落盘、本地调试页 | 关浏览器 24 h 会话仍在；断网 60 s 后补差无丢字 |
| 3 | 中转：Passkey/TOTP 登录、配对码 + 指纹短码、路由、心跳、SQLite、Docker 一条命令 | 配对 ≤10 分钟；吊销 ≤15 s 断连；无 token WS 被拒 |
| 4 | Web：xterm.js 多标签、attach/replay、三信号状态条、设备/会话列表 | 8 标签 1 个持续输出切换无 >100 ms 掉帧；Agent 离线绝不显示「已连接」 |
| 5 | Claude 感知：`--settings` 注入、hooks 端点、状态机、收件箱、`notify_only`/`remote_first` 两模式、statusLine 两个数 | hook → 收件箱 ≤3 s；本机答过 ≤2 s 自动关闭；`bypassPermissions` 会话远程零弹窗 |
| 6 | Codex notify、通知出站（ntfy/Bark/飞书）、供应商预设注入、远程 Esc/Ctrl-C/kill&resume | approval-requested → 推送 ≤5 s；预设环境变量在中转抓包不可见 |
| 7 | 手机端：只读 xterm + 键条 + composer、PWA、`visualViewport` 处理；真机矩阵 | Android Chrome + GBoard、iOS Safari 各完成：中文提示词 + Enter、Esc、Ctrl-C、↑ |
| 8 | 任务计划自启、崩溃拉起、重启后 `--resume` 拉回、安装/卸载子命令、签名、文档 | `taskkill` 后 ≤60 s 上线；重启登录后 ≤60 s 上线且会话自动拉回 |
| 9–10（缓冲） | 修 spike 暴露的 ConPTY / IME 问题；`hooks install` 免包装器附着；E2E 帧加密（若进度允许） | 用户自己在 Windows Terminal 起的 claude 装 hooks 后 ≤3 s 出现在收件箱 |

明确不做（写进 README）：Windows 服务化、E2E 实装（协议已预留）、无人值守/额度续跑、成本面板与额度环、fleet 聚合、只读链接、审计页/回放、Web Push、cmd 以外的 Git Bash/WSL 验收、Codex app-server、ACP 适配、原生 App、WebRTC 直连。

---

## 8. 主要风险与规避

| 风险 | 概率 | 规避 |
|---|---|---|
| Go 侧 ConPTY 封装踩坑（teardown 挂起、resize 重绘、IME 闪烁） | 中 | 第 1 周 spike；参考 node-pty 的 teardown 顺序；双路径（系统 / 自带 DLL）；首屏与逐字节验收 |
| Windows 上 http hook 不可靠（#88896/#90077/#88698 Open） | 中高 | 第 1 周实测；失败自动降级为 PTY 兜底并标注；不把收件箱建立在单一信号上 |
| `remote_first` 模式锁住本地终端引发误解 | 中 | 默认 `notify_only`；开启时 UI 与终端状态栏双重明示；每会话独立开关 |
| Claude TUI 在 ConPTY→xterm.js 链路的渲染问题（#51828/#1913 Open） | 中 | 固定 ≥100 列、注入 NO_FLICKER / FULL_REPAINT；不承诺 40 列 |
| xterm.js 中文 IME（#3600/#6049/#6045 Open） | 高 | 手机端不往 xterm 打字，走 composer；桌面端标已知风险，推荐外接 Termius/Blink 做重度输入 |
| Defender/SmartScreen 拦截未签名 Go 二进制 | 高 | 签名 + 不用 UPX + 误报提交；第 8 周前完成 |
| 服务态 ConPTY 无官方路径（#11865 Open） | 确定 | 用户态自启为长期方案；「自动登录 + 关闭睡眠」写进安装向导 |
| 中转分发前端 → 托管形态零知识不可验证 | 确定 | 自建单人为默认形态；托管前做 E2E + SRI + 可复现构建 |
| 外部变量：Anthropic 放开非官方 base URL 的 RC、Codex 修好 Windows daemon、Happier 修好 #256 | 中 | 每月复查；退守「国内可达 + 真终端 + 供应商预设 + Windows 保活」 |

---

## 附：本日核实来源

- Claude Code Hooks（PermissionRequest schema、http hook、600 s 默认超时、Notification matcher、Windows 说明）：https://code.claude.com/docs/en/hooks
- Claude Code CLI reference（`--settings`、`--resume`、`--bg`、`--permission-prompt-tool`）：https://code.claude.com/docs/en/cli-reference
- Claude Code statusLine（`cost.total_cost_usd`、`context_window.used_percentage`、`rate_limits` 仅 Pro/Max 或 apps gateway、300 ms 去抖）：https://code.claude.com/docs/en/statusline
- Claude Code Channels reference（permission relay、`--dangerously-load-development-channels`）：https://code.claude.com/docs/en/channels-reference
- Claude Code Remote Control（API key 不支持、`ANTHROPIC_BASE_URL` 与 v2.1.196、自动重连排队、Trusted Devices 18 小时）：https://code.claude.com/docs/en/remote-control
- openai/codex 源码：`codex-rs/hooks/src/legacy_notify.rs`、`codex-rs/tui/src/chatwidget/notifications.rs`（notify payload 与 `approval-requested`）、`codex-rs/features/src/lib.rs`（`CodexHooks` feature）、`codex-rs/app-server-protocol/.../CommandExecutionApprovalDecision.ts`；app-server README：https://github.com/openai/codex/tree/main/codex-rs/app-server
- Go 库（Go module proxy `@latest`）：`golang.org/x/sys` v0.47.0；`github.com/charmbracelet/x/conpty` v0.2.0、`/xpty` v0.1.4；`github.com/coder/websocket` v1.8.15；`modernc.org/sqlite` v1.58.0；`github.com/go-webauthn/webauthn` v0.18.0；`github.com/pquerna/otp` v1.5.0；`github.com/kardianos/service` v1.3.0；`github.com/pion/webrtc/v4` v4.2.19；`UserExistsError/conpty` https://github.com/UserExistsError/conpty
- npm `@xterm/xterm` latest 6.0.0；NuGet `Microsoft.Windows.Console.ConPTY` 1.25.260710002-preview
- Issues：microsoft/terminal #11865（Open，2021-12）；PowerShell/Win32-OpenSSH #2291（Open，2024-10）；anthropics/claude-code #88896（Open，2026-08-22）；其余（#90077、#88698、#51828、#1913、#51267、#29214、xterm.js #3600/#6049/#6045、codex #42507）沿用红队与 `research/05` 口径，未在本会话重新打开。
