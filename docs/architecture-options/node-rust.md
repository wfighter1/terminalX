# terminalX 第一阶段技术架构：Node.js 主线（Rust 备选）

> 2026-09-03 ｜ 范围：Web 控制台 → 自建中转 → Windows 被控端 → 终端 / AI CLI ｜ 依据 `docs/research/04`、`05`、三份设计方案 MVP 章节与红队结论。本日以 curl / WebFetch 复核了 Claude Code hooks / statusline / remote-control / cli-reference / agent-view / channels-reference 文档、Codex app-server 与 daemon README、Codex hooks JSON Schema、node-pty package.json、Node SEA 与 `node:sqlite` 文档及文末所列 issue。WebSearch 配额已耗尽，developers.openai.com 被代理拦截，未核实项标「待核实」。

## 0. 选型结论

| 组件 | 选型 | 理由 |
|---|---|---|
| Windows Agent | **Node.js 24 + node-pty 1.1.0**（TS），SEA exe + `native/` 侧车，Inno Setup 打成一个安装包 | 与 Claude hooks / Codex app-server / ACP 适配器同一 TS 生态；`@xterm/headless` 可进程内做屏幕快照；协议类型三端共享 |
| 中转 | **Node.js 24 + TS**（`ws` + Fastify）+ `node:sqlite`（v24.15 起 Release Candidate，`better-sqlite3` 兜底），Docker 一条命令 | 逻辑薄，复用 `packages/protocol` |
| Web | **React 19 + Vite + `@xterm/xterm` 6.0.0**（fit / webgl / search / serialize / unicode11）+ PWA | 唯一成熟的浏览器终端 |
| Rust（`portable-pty` 0.9.0） | 备选，只替换 ptyhost 进程，触发条件见 §7.2 | 单文件与内存更好，但一人团队三语言栈代价高 |

**为什么 Node 而非 Rust**：① 感知层接口全是 TS 生态——Claude hooks 是 HTTP JSON，Codex app-server 是 JSON-RPC，ACP 注册表 9 个适配器都是 npm 包，Node Agent 可进程内加载而无需桥进程；② node-pty 是 VS Code 同源的 ConPTY 绑定，1.1.0 已移除 winpty，包内自带 `prebuilds/` 与 `third_party/`（可选 `conpty.dll` + `OpenConsole.exe`），`install` 脚本先取 prebuild 再回退 `node-gyp`，用户机器无需编译器（本日核实 package.json）；③ `@xterm/headless` + `addon-serialize` 让 Agent 持有「权威屏幕」，重连给精确快照，Rust 侧需自造 VT 解析器；④ 一份 zod schema 与帧编解码，三端不漂移。**代价**：SEA 加载原生插件须落盘再 `process.dlopen`（官方文档，Stability 1.1）；产物更易被 Defender 误报，签名是硬需求；node-pty 仍有 teardown 泄漏类 open issue，需 supervisor / ptyhost 双进程（§2.3）。

## 1. 组件图与职责

```
┌────────────────────────┐   WSS:443 出站   ┌────────────────────────────┐
│ Web 控制台 / PWA        │ ───────────────▶ │ 中转（自建 VPS，1 容器）      │
│ apps/web               │ ◀─────────────── │ apps/relay                 │
│ · xterm.js 6 多标签     │ 控制面 JSON      │ · 认证/配对/路由/吊销        │
│ · 收件箱·会话·设备·设置  │ + 数据面二进制帧  │ · 离线元数据 ≥4h、审计元数据  │
│ · 手机=只读 xterm+键条   │                  │ · 通知出口 ntfy/Bark/飞书     │
│   +独立 composer        │                  │ · 不解析、不落盘 payload     │
│ · Passkey / TOTP        │                  └──────────────┬─────────────┘
└────────────────────────┘                                 │ WSS:443（Agent 出站）
                                                           ▼
┌──────────────────────────────────────────────────────────────────────────┐
│ Windows Agent（用户会话内、登录自启）apps/agent                            │
│  supervisor.exe ──看门狗/升级──▶ ptyhost（同 exe --ptyhost，持有 PTY）      │
│  ┌ relay-client ┐ ┌ session-manager ────────┐ ┌ ai-signals ┐ ┌ local-http ┐│
│  │重连/心跳/背压 │ │node-pty(ConPTY)+环形缓冲 │ │hooks→状态机│ │127.0.0.1:P ││
│  │              │ │+序号+@xterm/headless 快照 │ │启发式=兜底 │ │/hooks/*    ││
│  └──────────────┘ └───────────┬─────────────┘ └────────────┘ └────────────┘│
│        pwsh 7/5.1 ─ claude TUI ─ codex TUI ─ cmd/GitBash/WSL(可选)         │
│        （hooks / notify / statusLine 经回环回打 local-http）                 │
└──────────────────────────────────────────────────────────────────────────┘
（可选）局域网直连：调试用，MVP 默认关闭，绝不监听 0.0.0.0
```

职责边界：**Web** 只渲染与交互；**中转** 是「邮局」——认证、路由、离线元数据、通知，只读帧头，终端内容不进日志；**Agent** 是会话宿主——PTY 生命周期与连接解耦、滚动缓冲、收 hooks、远程解卡（Esc / Ctrl-C / kill & resume），只出站、只监听回环；**AI CLI** 不被包装、不被替换，TUI 原样跑在 ConPTY 里，只从官方 hooks / notify / statusLine 旁路拿信号。

## 2. 协议设计

一条 WSS 同时承载控制面（文本帧 JSON）与数据面（二进制帧），子协议 `terminalx.v1`。

### 2.1 控制面

```jsonc
// Agent → 中转
{"t":"agent.hello","device_id":"dev_8f3a","token":"<256bit>","agent_ver":"0.1.0","os":"win11-26200",
 "shells":["pwsh","powershell","cmd","gitbash","wsl:Ubuntu"],"caps":["snapshot","e2e:none","ai:claude-hooks","ai:codex-notify"]}
{"t":"agent.sessions","sessions":[{"sid":3,"name":"worktree-b","shell":"pwsh","cwd":"C:\\dev\\app-b","cols":120,"rows":40,
 "pid":18324,"alive":true,"seq":18234,"buf_bytes":1310720,"tool":"claude","state":"needs_input"}]}
{"t":"heartbeat","ts":1756900000,"sessions":{"3":{"alive":true,"last_output_ts":1756899990}}}   // 三信号分离
{"t":"ai.event","sid":3,"src":"claude.hook","conf":"high","kind":"permission.requested","key":"sha256:…",
 "tool":"Bash","summary":"git push origin feat/auth","cwd":"C:\\dev\\app-b","session_ref":"claude:00893aaf-…"}
// 中转 → Agent
{"t":"session.open","id":"r1","shell":"pwsh","cwd":"C:\\dev\\app-b","name":"scratch","cols":120,"rows":40,
 "env":{"CLAUDE_CODE_NO_FLICKER":"1"},"provider_preset":"relay-station-a"}
{"t":"session.signal","sid":3,"sig":"esc"}            // esc|ctrl_c|ctrl_d|kill|kill_and_resume
{"t":"approval.decide","sid":3,"key":"sha256:…","decision":"allow","by":"web:zhou"}
// Web ↔ 中转
{"t":"auth","session_cookie":"…"}
{"t":"devices.list"} → {"t":"devices","items":[{"device_id":"dev_8f3a","name":"家里台式机","online":true,"sessions":3,"needs_input":1}]}
{"t":"session.attach","id":"a1","device_id":"dev_8f3a","sid":3,"from_seq":18000,"cols":100,"rows":38}
   → {"t":"attached","id":"a1","chan":7,"mode":"delta"}   // 或 "snapshot"；chan 由中转分配
{"t":"inbox.list"} → 待审批 / 已退出 / Unknown 会话
```

### 2.2 数据面（18 字节头）

```
0  u8  ver=0x01
1  u8  type  0x01 OUTPUT(Agent→Web) 0x02 INPUT 0x03 RESIZE 0x04 ACK 0x05 SNAPSHOT 0x06 PING 0x07 PONG 0x08 EOF(exit code)
2  u32 chan  中转分配的通道号（Agent 侧映射 sid，Web 侧映射标签）
6  u64 seq   OUTPUT=会话累计输出字节偏移；INPUT=客户端计数；ACK=已收 seq
14 u32 len
18 ... payload  原始 VT 字节 / RESIZE={cols u16,rows u16} / SNAPSHOT=ESC c + serialize 串
```

帧头永远明文供路由统计；**payload 第一天起就是不透明字节**，E2E 上线后变 AEAD 密文（`len` 含 16 B tag），帧格式不变。Agent→Web 方向 10–16 ms 合并批次削 Ink / ratatui 全量重绘峰值。背压：Web 每 64 KiB 回 ACK；某客户端未确认超 1 MiB 时只对它停发、恢复时改发 SNAPSHOT，**不暂停 PTY**；`handleFlowControl` 只在全部客户端拥塞且缓冲将满时启用。

### 2.3 会话保持与重连回放

```
pty(node-pty) ─▶ RingBuffer(默认 4 MiB，可配 1–32) ─▶ 各客户端队列        seq = 累计输出字节数(u64)
              └▶ @xterm/headless(cols×rows, scrollback 5000) ─▶ serialize() = SNAPSHOT
```

1. **detach ≠ kill**：断开只是 detach，显式关闭才 kill。
2. **重连**：带 `from_seq` 附着；在缓冲窗口内回 `delta` 补差，否则先 SNAPSHOT（`ESC c` + 序列化屏幕与滚动区）再增量。
3. **多端附着**：输出广播、输入按到达串行；尺寸以最后一次 RESIZE 为准并提示其他端。
4. **Agent 重启不丢会话**：ConPTY 句柄随进程死亡，故拆 `supervisor`（任务计划程序拉起、看门狗、升级）与 `ptyhost`（持有 PTY）。supervisor 崩溃 / 升级不影响会话；ptyhost 崩溃则标「已退出」并提供「拉回」——Claude `claude --resume <session_id>`（id 来自 hooks），Codex `codex resume <thread_id>`（id 来自 notify，字段名待核实）。
5. **PC 重启**：登录自启后读取落盘的会话元数据（名、cwd、tool、最后 session_id），以「已退出，可拉回」列出。中转对离线设备保留清单 ≥4 h，UI 显示「Agent 离线，缓冲在被控端」。

## 3. 安全设计

**网络面**：Agent 与 Web 只出站 443；Agent 本机仅 `127.0.0.1:<随机端口>` 收 hooks，每次启动生成随机 `X-TerminalX-Token` 写入 hook `headers`（Claude http hook 支持 `headers` + `allowedEnvVars`，本日核实）。中转只暴露 443（Caddy 自动证书），反代真实 IP 只信任显式上游；支持 `HTTPS_PROXY`。

**配对**：
```
Web 生成：code=8 位 Crockford Base32，secret=128 bit，5 分钟、一次性、错 5 次锁 15 分钟
二维码/命令：terminalx://pair?relay=wss://r.example.com&code=ABCD-EFGH&secret=<b64url>&relay_fp=<中转 SPKI 指纹>
Agent：terminalx-agent pair <uri>
 1) 校验 relay_fp  2) POST /pair {code, device_pubkey(X25519 静态), hostname}
 3) 中转校验 code + HMAC(secret)，签发 device_id + 256 bit token（库中存 SHA-256）
 4) 两端显示 6 位 SAS 短指纹（HKDF(shared,"sas") 前 20 bit）目视比对一次
配对码即焚；token 可吊销，吊销 ≤15 s 断连
```
`secret` 只经二维码到 Agent、不经中转明文，它就是 E2E 根密钥引导材料——MVP 不开 E2E 也把这一步做完，日后不必重配。

**端到端加密**（协议第一天预留，v1.1 实装，托管 / 多用户发布前硬门槛）：静态 X25519（配对背书）+ 每次附着临时 X25519 → `HKDF-SHA256(shared, salt=chan_nonce, info="terminalx/v1/"+dir)` 派生双向密钥；AES-256-GCM（浏览器 WebCrypto 与 Node `crypto.webcrypto` 同一 API、同一份代码），nonce=`dir(1B)||seq(8B)||pad`，AAD=帧头；备选 XChaCha20-Poly1305（`@noble/ciphers` 2.4.0）。**fail-closed**：解密失败立即断开并显示「密钥不匹配」，绝不降级明文。明文字段只有设备名 / shell 列表 / 会话名 / cwd / 状态 / 事件摘要（可设为「摘要也加密」，代价是通知只剩「有一条审批」）。诚实声明：前端由中转分发，托管形态下 E2E 是承诺而非用户可验证的事实。

**Web 登录**：Passkey 为主（`@simplewebauthn/server` 14.0.0，Node ≥20；首次部署打印一次性引导码注册第一个 Passkey），TOTP 为备（`otpauth`，8 个恢复码）；cookie `HttpOnly; Secure; SameSite=Strict` 7 天滑动；吊销设备、改通知、开「远程优先」审批需 10 分钟内重新验证；单用户模型，表结构预留 `user_id`。

**审计**：SQLite `audit(ts, actor, action, device_id, sid, bytes_in, bytes_out, ip, ua)`，只有元数据；验收：任意终端内容片段在中转磁盘零命中。

## 4. Windows 细节

- **ConPTY**：最低 Win10 1809 / Server 2019（node-pty 与 Claude Code 要求一致）。双路径：默认系统 ConPTY，`pty.useConptyDll=true` 切包内 DLL（node-pty #894：自带 DLL 在 pwsh 7 首屏约 3.5 s，系统路径 340 ms）。前端开 `windowsPty:{backend:'conpty',buildNumber}`。Claude 会话固定列宽 ≥100，注入 `CLAUDE_CODE_NO_FLICKER=1`、`CLAUDE_CODE_ALT_SCREEN_FULL_REPAINT=1`（变量名待核实）；Codex 有 `--no-alt-screen`。不在 WSL 里经 ConPTY 调 Win32 程序（terminal #17822）；resize 整屏重绘，手机端去抖 300 ms。
- **默认 shell**：`pwsh.exe` → `powershell.exe`（5.1）→ `cmd.exe`；可选 Git Bash（`C:\Program Files\Git\bin\bash.exe`，亦是 Claude Bash 工具依赖）与 `wsl.exe -d`。MVP 只承诺 pwsh / 5.1。
- **服务 vs 用户会话（结论：用户会话）**：AI CLI 凭据在 `%USERPROFILE%\.claude\`、`.claude.json`、`.codex\` 下，shell 必须以登录用户 token 运行；Session 0 服务替用户起 shell 需 `CreateProcessAsUser/WithToken`，与 ConPTY 组合的 terminal #11865（2021-12）本日仍 **Open、Backlog**，无官方路径。因此 MVP 与 1.x 都在用户会话内运行；服务化只作「服务看门狗 + 用户会话内 ptyhost」的长期约束并要求自动登录。锁屏不影响，睡眠 / 休眠会断——安装向导提示关闭睡眠，Agent 上报电源状态。UAC 不提权。
- **自启与看门狗**：任务计划程序 `ONLOGON` + `RL LIMITED` + XML `RestartOnFailure`（1 分钟 × 999）+ `ExecutionTimeLimit=PT0S`；备选 `HKCU\...\Run`。ptyhost 5 s 心跳，30 s 无心跳 respawn 并标旧会话「已退出」。隐藏控制台：ptyhost 以 `windowsHide:true` 拉起，supervisor 用 `editbin /SUBSYSTEM:WINDOWS`（Paseo #3389 弹控制台窗是前车之鉴）。
- **防火墙 / Defender / 分发**：无入站监听不触发防火墙弹窗。未签名 exe 必被 SmartScreen 拦：内测阶段接受「仍要运行」，对外发布前买 Authenticode / Azure Trusted Signing（价格待核实），提交误报样本，不用 UPX。打包：Node 24 SEA 生成 `terminalx-agent.exe`，`prebuilds/win32-x64/*.node`、`conpty.dll`、`OpenConsole.exe` 放 `native/` 侧车（比运行时解包更稳、更不像恶意行为），Inno Setup 装到 `%LOCALAPPDATA%\terminalX\`，无需管理员；备选 Bun `--compile`（可嵌 `.node`、`--windows-hide-console`，但自述「still way too big」）、`@yao-pkg/pkg` 6.22.0。升级由 supervisor 下载到 `versions/<ver>/` 切换，ptyhost 不重启；后续 winget。

## 5. AI 感知层

**原则（回应红队）**：① 只用官方旁路通道——hooks / notify / statusLine / app-server；MVP 不解析 TUI 文本作决策、不注入按键作自动决策，PTY 启发式只产「低置信」并标「疑似」。② **hooks 只登记 + 推送，不挂起等手机**：官方文档本日核实「By default, hooks block Claude's execution until they complete」，`PermissionRequest` 超时「renders no decision」流程原样继续——hook 挂多久，终端前的人就被锁多久；Codex #39447（Open）同样证实 PermissionRequest「runs in the approval path, before … approval UI is shown」。默认 `timeout` ≤5 s、不返回 `decision`，原生对话框照常出现。③ **附着到用户自己起的会话、不需要包装器**：terminalX 拉起的会话用 `claude --settings <terminalx-hooks.json>`（本日核实该 flag 接受文件或内联 JSON，只覆盖同名键）注入；用户自起的会话由设置页「安装 hooks（展示 diff）」显式 opt-in。④ **Windows 上 hooks 可靠性存疑，第 1 周实测**：#88896（PreToolUse 在 Windows 不触发）、#90077（`shell:powershell` 找不到 pwsh 就静默不跑）、#88698（`--bg` 会话丢弃 PermissionRequest 决定），均 Open。用 http 型 hook 绕开 shell 解析问题，但仍需一手验证。

**信号与置信度**

| 状态 | 高置信（结构化） | 低置信（PTY 兜底，标「疑似」） |
|---|---|---|
| running | Claude `UserPromptSubmit`/`PreToolUse`/`PostToolUse`；Codex hooks `PreToolUse` / app-server `turn/started` | 3 s 内有输出且非提示符 |
| needs_input·permission | Claude `PermissionRequest`（即时）、`Notification(permission_prompt)`（约 6 s 后，本日核实）；Codex hooks `PermissionRequest` / app-server `item/commandExecution/requestApproval` | 匹配 `Do you want to proceed`/`❯ 1. Yes`/`(y/n)` 且 5 s 无输出 |
| needs_input·question | Claude `Notification(agent_needs_input\|elicitation_dialog)` | 同上 |
| idle | Claude `Stop`（即时）/`Notification(idle_prompt)`（约 60 s）；Codex notify `agent-turn-complete` 后无新 turn | 光标停在提示符 >30 s |
| failed / exited | Claude `StopFailure`；ptyhost EOF 与退出码 | 退出码 |
| quota_wait | Claude `Notification(quota_auto_resume_*)`，仅 claude.ai 登录才有；API Key「no reset to wait for」 | 不做 |
| **unknown** | 心跳正常但 >10 分钟无结构化信号且无输出 | — |

事件带 `src`（`claude.hook`/`codex.notify`/`codex.hook`/`pty.heuristic`）与 `conf`；高置信即时推，低置信 15 s 去抖并标「疑似」。

**审批对象与去重**：官方文档本日核实 `PermissionRequest` 输入**没有 `tool_use_id`**，红队建议的 `tool_use_id` 去重不可用，改 `key = sha256(session_id‖tool_name‖canonical_json(tool_input))`，60 s 窗口内同 key 合并。自动关闭：同会话随后的 `PreToolUse`/`PostToolUse`（已放行）、`PermissionDenied`、`Stop`、下一条 `UserPromptSubmit`、进程退出任一到达即标「已在本机处理」，目标 ≤2 s。卡片显示 `tool_name` + `command/file_path` 摘要 + `cwd` + 输入自带的 `permission_suggestions`（可原样回显为 `updatedPermissions`）。**远程 ≤ 本地**：`bypassPermissions` 会话根本不触发 `PermissionRequest`（文档「run only when Claude Code is about to ask you for permission」），terminalX 不叠审批层，「本地 bypass 会话手机零弹窗」自然成立。

**「一键允许」三条路径（如实标注）**

| 路径 | 机制 | 状态 |
|---|---|---|
| A 通知 + 打开终端 | 推送深链；手机进终端按 `1`+Enter 或键条 | **MVP 默认** |
| B 「远程优先」模式（每会话开关） | hook `timeout` 3600 s 挂起等手机，回 `{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":{"behavior":"allow"}}}`（本日核实的真实 schema：`behavior` 取 `allow\|deny`，另有 `updatedInput`/`updatedPermissions`/`message`/`interrupt`） | MVP 可选，UI 明示「开启期间终端内对话框不出现」（**2026-09-04 真机推翻：对话框照常出现，先答者胜**）；`--bg` 会话不可用（#88698） |
| C Channels permission relay | 自建 MCP channel 声明 `claude/channel/permission`，收 `notifications/claude/channel/permission_request`，回 `…/permission {request_id, behavior}`；文档「Both stay live … whichever answer arrives first」 | 研究预览，需 `--dangerously-load-development-channels`；自定义 `ANTHROPIC_BASE_URL` 下是否可用待核实；1.1 spike |

**Codex**：MVP 只做 `notify`（config.toml `notify=[...]`，源码注释本日核实「spawn this program after each completed turn」，payload 形如 `{"type":"agent-turn-complete","turn-id":…}`）+ `hooks.json` 的 `PermissionRequest`（输出 schema 本日核实：`decision.behavior` 仅 `allow|deny`，`updatedInput`/`updatedPermissions`/`interrupt=true` 目前 fail closed）做登记推送 + 打开终端。一键审批走 app-server **unix socket**（README「also supported on Windows」，目录带 current-user-only DACL）作第二客户端收 `item/commandExecution/requestApproval`、回 `accept|acceptForSession|decline|cancel`——1.1 spike；WebSocket 传输「experimental and unsupported」不用。codex #42507（2026-09-03 Open）称 Windows `remote-control start` 报「Unix only」而 daemon README 又称支持 Windows，以实测为准。

**注入配置（Agent 生成，`claude --settings` 传入）**

```jsonc
{"allowedHttpHookUrls":["http://127.0.0.1:47391/hooks/claude"],
 "hooks":{
  "SessionStart":[{"hooks":[{"type":"http","url":"http://127.0.0.1:47391/hooks/claude","timeout":3,
                    "headers":{"X-TerminalX-Token":"$TX_TOKEN"},"allowedEnvVars":["TX_TOKEN"]}]}],
  "UserPromptSubmit":[…同上…],"PreToolUse":[…],"Stop":[…],"StopFailure":[…],"SessionEnd":[…],
  "PermissionRequest":[{"hooks":[{"type":"http","url":"…/hooks/claude","timeout":5}]}],
  "Notification":[{"matcher":"permission_prompt|idle_prompt|agent_needs_input|elicitation_dialog","hooks":[…]}]},
 "statusLine":{"type":"command","command":"curl.exe -s -X POST -H \"Content-Type: application/json\" -d @- http://127.0.0.1:47391/statusline"}}
```

statusLine 只采 `cost.total_cost_usd`（文档：牌价客户端估算）与 `context_window.used_percentage`；`rate_limits` 仅 Pro/Max 或 apps gateway 才出现，不做额度环。hook 失败模式友好（非 2xx / 连接失败均 non-blocking），Agent 挂了 Claude 照常弹本地对话框。

**统一事件与推送**：`AiEvent={device_id,sid,tool,kind,conf,key,summary,cwd,session_ref,ts}`；中转按规则渲染模板投递到通用 Webhook（预置 ntfy / Bark / 飞书 / 企微），事件：需审批、需回答（即时）、空闲（60 s 合并）、退出 / 失败、Agent 离线（心跳缺失 60 s）。Web Push 放 1.1（iOS 无动作按钮且须加主屏）。

## 6. Monorepo 目录与骨架

```
terminalX/
├─ package.json  pnpm-workspace.yaml  turbo.json        # Node 24, TS 5.x
├─ packages/
│  ├─ protocol/    src/{control.ts, frame.ts, ai-event.ts}   # zod schema + 帧编解码，三端共享
│  ├─ crypto/      配对 HKDF / SAS / (1.1) AEAD 帧加密，WebCrypto 通用
│  └─ ai-signals/  状态机、去重键、Claude/Codex hook 载荷解析（纯函数，可单测）
├─ apps/
│  ├─ agent/  src/main.ts(--supervisor|--ptyhost|pair|doctor)
│  │          src/supervisor/{watchdog,updater,task-scheduler}.ts
│  │          src/ptyhost/{session,ring-buffer,screen(@xterm/headless),shells}.ts
│  │          src/relay-client/{ws,backoff,flow}.ts   src/local-http/{server,claude,codex,statusline}.ts
│  │          src/config/{store,presets}.ts            # 供应商预设=环境变量模板，被控端注入
│  │          sea.config.json  build/{sea.mjs,inno.iss}  native/（prebuilds + conpty.dll + OpenConsole.exe）
│  ├─ relay/  src/{server,router,pairing,devices,sessions,inbox,audit}.ts  auth/{passkey,totp,session}.ts
│  │          notify/{webhook.ts,templates/}  db/migrations/  Dockerfile  compose.yaml（含 Caddy）
│  └─ web/    src/pages/{Inbox,Devices,Sessions,Terminal,Settings}.tsx
│             src/terminal/{XTerm.tsx,attach.ts,snapshot.ts,mobile/{KeyBar,Composer}.tsx}
└─ scripts/acceptance/   seq-20000 比对、断网 60 s、hook 计时等可脚本验收
```

```ts
// packages/protocol/src/frame.ts
export const enum FrameType { Output=1, Input=2, Resize=3, Ack=4, Snapshot=5, Ping=6, Pong=7, Eof=8 }
export function encode(f: {type: FrameType; chan: number; seq: bigint; payload: Uint8Array}) {
  const b = new Uint8Array(18 + f.payload.length), v = new DataView(b.buffer);
  b[0] = 1; b[1] = f.type; v.setUint32(2, f.chan); v.setBigUint64(6, f.seq); v.setUint32(14, f.payload.length);
  b.set(f.payload, 18); return b;               // decode 对称；ver≠1 抛错并断连（fail-closed）
}
// apps/agent/src/ptyhost/session.ts
import * as pty from 'node-pty'; import { Terminal } from '@xterm/headless'; import { SerializeAddon } from '@xterm/addon-serialize';
export class Session {
  seq = 0n; ring = new RingBuffer(4 << 20); screen = new Terminal({cols:120, rows:40, scrollback:5000, allowProposedApi:true}); ser = new SerializeAddon();
  constructor(o: {shell:string; args:string[]; cwd:string; env:Record<string,string>; useConptyDll:boolean}) {
    this.screen.loadAddon(this.ser);
    this.p = pty.spawn(o.shell, o.args, {name:'xterm-256color', cols:120, rows:40, cwd:o.cwd, env:{...process.env, ...o.env}, useConpty:true, useConptyDll:o.useConptyDll});
    this.p.onData(d => { const u8 = Buffer.from(d); this.ring.push(u8); this.seq += BigInt(u8.length); this.screen.write(d); this.lastOutputTs = Date.now(); this.broadcast(u8); });
    this.p.onExit(({exitCode}) => this.emit('exit', exitCode));
  }
  snapshot() { return Buffer.from('\x1bc' + this.ser.serialize({scrollback:5000})); }
  replayFrom(seq: bigint) { return this.ring.sliceFrom(this.seq - seq); }   // 超窗口返回 null → 走 snapshot
  signal(s: 'esc'|'ctrl_c'|'ctrl_d') { this.p.write({esc:'\x1b', ctrl_c:'\x03', ctrl_d:'\x04'}[s]); }
}
// apps/agent/src/local-http/claude.ts —— PermissionRequest 只登记 + 推送
app.post('/hooks/claude', async (req, res) => {
  if (req.headers['x-terminalx-token'] !== TOKEN) return res.status(401).end();
  const ev = ClaudeHookInput.parse(req.body); const out = signals.ingest('claude.hook', ev); relay.send({t:'ai.event', ...out});
  if (ev.hook_event_name === 'PermissionRequest' && remoteFirst.has(ev.session_id)) {        // 仅「远程优先」模式挂起
    const behavior = await inbox.waitDecision(out.key, 3600_000);
    return res.json({hookSpecificOutput:{hookEventName:'PermissionRequest', decision:{behavior}}});
  }
  res.status(200).end();                                                                      // 空 2xx = 不干预
});
```

## 7. 里程碑（10 周）、验收、风险

### 7.1 按周（失败即降级，不顺延）

| 周 | 交付 | 验收 / 降级 |
|---|---|---|
| 1 | 三个 spike：① 原生 Windows 上 http 型 `PermissionRequest`/`Stop`/`Notification` 实测（对照 #88896/#90077）；② node-pty 起 `claude`/`codex` 在 xterm.js 6 渲染，100 列 + resize；③ Android 真机 composer 50 个中文到 PTY。附：OpenSSH 会话里起 psmux/Zellij 断开，验证 #2291 job object 回收 | ①失败→感知层退到 Codex notify + PTY 启发式并向上游提 issue；②碎片严重→固定列宽 + FULL_REPAINT 变量；③失败→手机端只做键条 + 审批 |
| 2 | Agent 骨架：supervisor/ptyhost 双进程、pwsh/5.1、环形缓冲 + 序号、headless 快照、本地调试页 | `seq 1 20000` 逐字节一致；kill supervisor 会话不丢 |
| 3 | 中转：配对 / token / 路由 / 心跳 / 离线元数据 / 审计；Caddy + compose 一条命令 | NAT 后互通；吊销 ≤15 s 断；中转磁盘无终端内容 |
| 4 | Web：Passkey/TOTP、设备 / 会话 / 终端页、多标签、重连补差 + 快照、PWA | 断网 60 s 恢复逐字节一致；可输入 ≤5 s；8 标签无 >100 ms 掉帧 |
| 5 | 感知：hooks 注入（`--settings` 与 opt-in）、状态机、去重 / 自动关闭、statusLine 两个数、Unknown | 用户自起的 claude ≤3 s 出现在列表；本机答过 ≤2 s 自动关闭 |
| 6 | 收件箱 + 全局状态条 + Webhook 模板 + 远程 Esc/Ctrl-C/kill&resume + 拉回 | hook→推送 ≤5 s；bypass 会话手机零弹窗；远程 Esc 解 #51267 类卡死 |
| 7 | 手机端：只读 xterm + 键条 + composer + `visualViewport`；Android Chrome/GBoard 与 iOS Safari 真机 | composer 中文逐字节；键盘弹出后提示符可见 |
| 8 | 自启 + 看门狗 + 重启后「可拉回」+ 供应商预设 + Codex notify/hooks 登记 | `taskkill` 后 ≤60 s 上线；重启登录 ≤60 s 上线；Codex 审批 ≤5 s 推送 |
| 9 | 打包 SEA + native 侧车 + Inno Setup；`doctor`；数据流向图；README「为什么不用 X」 | 安装到手机看到终端 ≤10 分钟；无 0.0.0.0 监听 |
| 10（缓冲） | 「远程优先」模式、Channels relay 与 app-server unix socket spike、失败信号清单 | 未完成项写回归路径，不阻塞发布 |

总验收：A1 关浏览器 24 h 会话仍在且可见 ≥2000 行；A2 断网 60 s 逐字节一致；A5 被杀 / 重启 ≤60 s 上线；A6 离线绝不显示「已连接」，退出 ≤15 s 显示；B1 hook→收件箱 ≤3 s、→推送 ≤5 s；B2 本机答过 ≤2 s 关闭；B5 bypass 远程零弹窗；C composer 中文逐字节；D1 无 0.0.0.0；D4 中转日志无内容；E1 pwsh 首屏 ≤1 s；E2 TUI 100/120 列正常、手机横滚可读（不承诺 40 列）。

### 7.2 风险与规避

| 风险 | 证据 | 规避 / 切换条件 |
|---|---|---|
| http hook 在原生 Windows 不触发或静默失败 | #88896、#90077 Open | 第 1 周 spike；失败则感知降级为 Codex notify + 低置信启发式 |
| node-pty 常驻泄漏 / ConPTY 怪癖 | node-pty #947/#951/#952、#894 | supervisor/ptyhost 分离 + 低峰自重启；双路径 ConPTY；**切 Rust 条件**：ptyhost 24 h RSS 增长 >200 MB 或每周崩溃 >1 次 → 用 `portable-pty` 重写 ptyhost，协议不变 |
| SEA 产物被 Defender / SmartScreen 拦 | 研究 05 §1.4 | 签名前只内测；侧车目录而非运行时解包；发布前 Authenticode |
| Claude TUI 在 ConPTY→xterm.js 渲染碎片 | #51828、#1913（红队引用） | 固定列宽 ≥100 + 环境变量 + 手机横滚；不承诺 40 列 |
| xterm.js 中文 IME | #3600 本日仍 Open、help wanted | 手机端 composer；桌面端标已知风险并提供 composer 开关 |
| Codex Windows 远程宿主矛盾 | #42507 Open vs daemon README | MVP 只登记推送；unix socket 1.1 spike |
| 服务化不可行→睡眠 / 未登录即离线 | terminal #11865 Open、Backlog | 文档明写；电源建议；1.x 服务看门狗 + 自动登录 |
| 官方放开非官方 base URL / Codex 修 Windows daemon / Happier 修稳 Windows | 红队失败信号 | 每月复查 #59062/#71731/#42507/Happier #256；触发即退守「国内可达 + 真终端 + 供应商预设 + 跨工具收件箱」 |
| 目标人群无一手数据 | 红队三方一致 | 动工前访谈 20 个「Windows + 中转 Key + 手机」用户 |

## 8. 本日核实的一手来源

- Claude Code 文档：hooks https://code.claude.com/docs/en/hooks ｜ statusline https://code.claude.com/docs/en/statusline ｜ remote-control https://code.claude.com/docs/en/remote-control ｜ cli-reference https://code.claude.com/docs/en/cli-reference ｜ agent-view https://code.claude.com/docs/en/agent-view ｜ channels-reference https://code.claude.com/docs/en/channels-reference ｜ setup https://code.claude.com/docs/en/setup
- Codex：app-server README https://github.com/openai/codex/blob/main/codex-rs/app-server/README.md ｜ daemon README https://github.com/openai/codex/blob/main/codex-rs/app-server-daemon/README.md ｜ hooks schema https://github.com/openai/codex/tree/main/codex-rs/hooks/schema/generated ｜ `notify` 注释 https://github.com/openai/codex/blob/main/codex-rs/core/src/config/mod.rs ｜ #42507、#39447（Open）
- claude-code #88896 / #90077 / #88698 / #51267（均 Open）；microsoft/terminal #11865（Open，Backlog）；Win32-OpenSSH #2291（Open）；xterm.js #3600（Open）
- node-pty https://github.com/microsoft/node-pty （1.1.0，`files` 含 `prebuilds/`）｜ Node SEA https://nodejs.org/api/single-executable-applications.html ｜ `node:sqlite` https://github.com/nodejs/node/blob/main/doc/api/sqlite.md ｜ Bun `--compile` https://github.com/oven-sh/bun/blob/main/docs/bundler/executables.mdx
- npm：`@xterm/xterm` 6.0.0、`@simplewebauthn/server` 14.0.0、`@noble/ciphers` 2.4.0、`@yao-pkg/pkg` 6.22.0；crates.io `portable-pty` 0.9.0
- 未能核实：developers.openai.com 的 Codex config / hooks 文档（`notify` 完整 payload 与 `approval-requested`）、`CLAUDE_CODE_NO_FLICKER` / `ALT_SCREEN_FULL_REPAINT` 变量名、Node 24 LTS 时间线、Trusted Signing 价格、Node Agent 常驻内存。
