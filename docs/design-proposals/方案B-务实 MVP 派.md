# 方案 B · 务实 MVP 派：4–6 周内自己先用起来的 terminalX

> 2026-09-03 ｜ 角度：独立开发者、4–6 周、最小可行闭环 ｜ 依据 `docs/01` 与 `docs/research/01/03/04/05/06`；本日以 WebFetch 复核了 Claude Code Remote Control / Hooks / Agent View / CLI reference 官方文档、Win32-OpenSSH #2291、claude-code #34255 / #51267、happy #496、Paseo、node-pty、xterm.js #3600、Codex app-server README（本会话 WebSearch 配额已耗尽，未复核的数字标「待核实」）。
> 立场：**先做一条天天能用的窄闭环，再谈 fleet 与团队；每砍一个功能，都写明它回来的路径。**

## 0. 一页结论

- **定位**：装在 Windows 上的「tmux 替身」+ 一个手机能用的网页。让走中转 API 的 Claude Code / Codex 用户随时接管家里那台 Windows 上的 AI CLI 会话，agent 等审批时会叫你、一键放行。
- **为什么是这条窄闭环**：调研里最硬的两条空白——官方 Remote Control 对非官方 `ANTHROPIC_BASE_URL` / API Key 不可用、Windows 没有 tmux 等价物（Win32-OpenSSH #2291 至今 Open）——恰好都落在「一个人 + 一台 Windows + 手机」这个最小场景；而最强竞品 Happy 在 Windows 上被用户判为「Currently unusable」（#496），Paseo 默认 relay 与国内可达性无保证。
- **6 周交付**：单 exe Windows Agent（用户态自启）、单二进制中转（Docker 一条命令）、一个 PWA 网页（设备 / 会话 / 终端 / 待确认收件箱 / 设置）。
- **明确不做**：Windows 服务化、E2E 加密、原生 App、团队与审计、用量面板、跨机接续、MiniMax/Kimi/Grok 适配。第 6 节给出每项回归路径。

## 1. 定位、目标用户与核心场景

**一句话定位**：Windows 上的 tmux + 手机能用的网页——自建、工具无关、断线不丢会话、agent 等你时会叫你。

**目标用户（MVP 只服务一种人）**：画像 A「盯盘的独立开发者」小周。Windows 11 台式机常年开机，Claude Code 走国内中转站 Key，Codex 用 Plus，Android 手机。官方文档明文：「API keys are not supported」「You point `ANTHROPIC_BASE_URL` at a host other than `api.anthropic.com`… Unset the variable to use Remote Control」（v2.1.196 起）——他被官方直接排除。画像 B（多机监工）与 C（合规团队）是路线图对象。

**核心场景：小周的一天**（每个时刻对应一条验收）

| 时刻 | 发生什么 | 必须做到 | 验收 |
|---|---|---|---|
| 08:30 家里 | Web 里开 3 个标签：两个 `claude`（worktree A/B）、一个 `codex`；关浏览器出门 | 关浏览器不杀进程，会话由 Agent 持有 | A1 |
| 09:40 地铁 | 手机 PWA 看 3 个会话状态，点进 A 看最后 200 行；4G 断 30 秒又回来 | 显式「重连中」；恢复后无丢字 | A2 A3 |
| 11:15 会议间隙 | Claude 在 B 等 `git push` 审批 | 5 秒内手机通知；收件箱显示工具 + 命令 + 目录；点允许 3 秒内继续 | B1 B2 |
| 14:00 咖啡馆 | 给 Codex 补一句「跳过这个测试」，要 Esc 和方向键 | 键盘栏有 Esc / Ctrl / 方向键；中文不乱序 | C1 C2 |
| 23:30 睡前 | 起通宵任务；凌晨 PC 自动更新重启 | 登录即自启 60 秒内上线；会话标「已退出」而非 Connected 假象；一键 `claude --resume` 拉回 | A5 A6 |

## 2. 产品亮点（诚实标注强弱）

| # | 亮点 | 为什么现有方案没做到 / 做不到 | 强弱 |
|---|---|---|---|
| 1 | **任何 API 配置都能远程** | 官方 RC 是「本地进程向 Anthropic 云出站轮询」，架构上必须对上 claude.ai 账号，所以 API Key / 非官方 base URL / Bedrock / ZDR 全部不可用，#76653 放行 localhost 代理仍是请求。terminalX 只碰 PTY 字节与 hook 事件，不碰模型链路。 | **强**，但只相对官方；Happy / Paseo 同样不依赖官方账号 |
| 2 | **Windows 原生会话保持（tmux 替身）** | #2291（2024-10 开启、Open、无人认领）承认 Windows 缺「survive a disconnect」；官方解法是「start it inside tmux or screen」，Windows 没有；Codex 曾报「daemon lifecycle is only supported on Unix」（#34584）。Agent 用 ConPTY 持有会话 + 环形缓冲 + 序号补差，浏览器只是 attach。 | **强**，MVP 核心壁垒；psmux / Zellij 0.44+ 在补多路复用层（待核实）但不带远程与手机层 |
| 3 | **审批是持久对象，不是弹窗** | 官方转发对话框默认 5 分钟 `dialogExpiry`；happy #1208「审批等久了连接就掉、手机再也发不回去」。terminalX 通过 Claude `PermissionRequest` hook（`http` 类型，Windows 不用 `.sh`）把请求写入中转库，任意端随时回答。 | **中**。Claude 有官方通道；Codex hook 能否回写决定待实测，app-server 放 1.1 |
| 4 | **手机上是真终端，且能打字** | 官方 RC 只有聊天 UI；VibeTunnel 有终端但「Windows is not yet supported」（#252）；xterm.js #3600 Android 组合事件问题 2022 年至今 Open。自建隐藏 input + 键盘栏绕开上游。 | **中**。工程量大、靠真机反复调 |
| 5 | **「连接死了」是显式状态 + 远程解卡** | #34255（👍107，Open）「locked out until you can physically get back」，提议正是 heartbeat / status indicator；#77252 二十个会话显示 Connected 却静默丢消息；#51267（Win11，Open）「only local Esc recovers it — no remote unstick mechanism」。terminalX 把心跳、进程存活、PTY 输出分开显示，并提供远程 Esc / Ctrl-C / kill & resume。 | **中强**。不难做，只是没人当一等公民 |

不是亮点、别拿来宣传：手机上继续 Claude 会话（官方 + Happy + Paseo 已做好）、接微信/飞书（cc-connect 已是事实标准）、云端多 agent 编排。

## 3. 信息架构与关键页面

MVP 只有 5 个页面，但数据模型第一天就按多机建：`Device → Session → Approval`。fleet 视图后续只是给设备页加聚合列，不改模型。

```
terminalX Web（PWA）
├── /devices      设备列表（MVP 通常只 1 行，但它是一级实体）
├── /sessions     会话列表（跨设备扁平，Needs input 置顶）
├── /t/<id>       终端视图（桌面多标签；手机单标签 + 底部键盘栏）
├── /inbox        待确认收件箱（审批 / 已退出会话）
└── /settings     配对新设备、通知通道、账户与安全
```

**设备列表 `/devices`**

```
┌──────────────────────────────────────────────────────┐
│ terminalX                 [收件箱 ●2]  [设置]          │
├──────────────────────────────────────────────────────┤
│ ● 家里台式机  Win11 · Agent v0.1.3 · 心跳 3 秒前        │
│   3 个会话 · 1 个等你 · [新建会话 ▾ pwsh|cmd|GitBash|WSL]│
│ ○ 公司笔记本 (离线) · 心跳 2 小时前 · 缓冲已保留         │
│ [+ 配对新设备]                                        │
└──────────────────────────────────────────────────────┘
```
元素：设备名（可改）、OS / Agent 版本、心跳相对时间（>60 秒黄、>5 分钟灰）、会话数与「等你」数、新建会话（选 shell、填 cwd）、配对入口。

**会话列表 `/sessions`**

```
┌──────────────────────────────────────────────────────┐
│ 会话                     [全部|等你|运行中|已退出]       │
├──────────────────────────────────────────────────────┤
│ ▲ 等你 claude·worktree-b  家里台式机  等待 12 分钟       │
│        Bash: git push origin feat/auth [允许][拒绝][打开]│
│ ✻ 运行 claude·worktree-a  家里台式机  最后输出 8 秒前    │
│ ∙ 空闲 codex ·api-refactor 家里台式机 最后输出 40 分钟前 │
│ ✕ 退出 pwsh  ·scratch     退出码 0 · 3 小时前 [拉回][清理]│
└──────────────────────────────────────────────────────┘
```
元素：四态图标（借官方 Agent View 的 Working / Needs input / Idle，加 Exited；不做 Completed/Failed 细分）、工具 + 会话名（可重命名，回应 #2112「guess the session roulette」）、设备、最后输出时间、行内审批、已退出会话的「拉回」（Claude 会话执行 `claude --resume <id>`）。

**终端视图 `/t/<id>`（桌面）**

```
┌ [worktree-b ●] [worktree-a] [codex] [+]        家里台式机 ● 在线 ┐
│ PS C:\dev\app-b> claude                                       │
│ ⏺ Bash(git push origin feat/auth)                             │
│   Do you want to proceed? ❯ 1. Yes  2. No                     │
├───────────────────────────────────────────────────────────────┤
│ [Esc][Ctrl-C][↑][↓][Tab][kill & resume ▾]  已连接·序号 18234·60ms│
└───────────────────────────────────────────────────────────────┘
```
元素：标签栏（未读小圆点；关标签 = detach，二次确认才 kill）、xterm.js 6（WebGL 优先，开 `windowsPty` 启发式）、连接态三色（绿已连接 / 黄重连中缓冲保留 / 灰 Agent 离线）、远程解卡按钮、序号与 RTT（排障用）。

**终端视图（手机）**

```
┌──────────────────────────┐
│ ‹ worktree-b  ● 在线   ⋮  │
│ (xterm 全屏，双指缩放字号)  │
│ ❯ 1. Yes  2. No          │
├──────────────────────────┤
│ Esc Tab Ctrl Alt ↑ ↓ ← → │ ← 常驻键盘栏，Ctrl 粘滞
│ ^C ^D ^Z  |  ~  /  粘贴   │
│ [隐藏 input 承接软键盘]     │
└──────────────────────────┘
```
元素：自管隐藏 `<input>` 监听 `beforeinput` / `compositionend`，最终文本经 `term.input()` 注入以规避 #3600 乱序；`visualViewport` resize 时重新 fit。

**待确认收件箱 `/inbox`**

```
┌──────────────────────────────────────────────────────┐
│ 待确认 (2)                       [全部允许低风险]       │
│ 🟡 claude·worktree-b · 12 分钟前                        │
│    Bash  git push origin feat/auth   cwd C:\dev\app-b │
│    [允许一次][拒绝][本会话不再问][打开终端]              │
│ 🟡 claude·worktree-a · 2 分钟前                         │
│    Edit  src/auth/session.ts (+18 −4)                 │
│ 已处理 (今天 7) ▸                                      │
└──────────────────────────────────────────────────────┘
```
元素：三个决定映射 hook 的 `allow / deny / dontAsk`；「打开终端」是 hook 超时后的兜底；「全部允许低风险」只覆盖 Read/Glob/Grep 类，MVP 不做规则引擎。

**设置 `/settings`**：配对（8 位一次性码 + 二维码，5 分钟过期，附命令 `terminalx-agent pair <中转> <码>`）；通知通道（通用 Webhook，预置 Bark / ntfy / 飞书机器人 / 企微机器人模板；事件勾选：需审批、空闲、退出、Agent 离线）；账户（单用户 Web 密码、已配对设备与吊销、「暂停远程」总开关）。

## 4. 核心交互流程

**4.1 首次配对（目标 10 分钟）**
1. VPS：`docker run -p 443:443 -v data:/data terminalx/relay`，首启打印初始密码，浏览器设 Web 密码。
2. `/settings` 生成配对码（8 位、5 分钟、一次有效、错 5 次锁 15 分钟）。
3. Windows 下载单 exe：`terminalx-agent pair wss://relay.example.com ABCD-1234`，换取长期设备 token 与 id，写入 `%LOCALAPPDATA%\terminalX\agent.toml`。
4. Agent 注册任务计划程序「登录时启动 + 失败 1 分钟后重启」（用户态，非服务），立即上线。
5. 设备列表出现「● 在线」，新建 pwsh 会话见到提示符——完成。
6. 可选「为 Claude Code 安装 hooks」：Agent 向 `~/.claude/settings.json` 写入 `PermissionRequest / Notification / Stop / UserPromptSubmit` 四条 `http` 型 hook，指向 `http://127.0.0.1:<port>/hooks/claude`，写前展示 diff。

安全默认值：Agent 只出站 443，本机仅监听 127.0.0.1；未配对设备在中转上不存在入口；配对码用后即焚；设备 token 可吊销。

**4.2 启动一个 Claude Code 会话**
新建会话选 `pwsh`、填 cwd 与名字 → Agent 用 ConPTY 起 `pwsh -NoLogo` → 用户在终端敲 `claude`（MVP 只给命令片段按钮，不封装启动器）。装了 hooks 时：`UserPromptSubmit → working`，`Stop → idle`，`PermissionRequest → needs_input` 并生成 Approval；没装 hooks 时按 PTY 输出时间推断「有输出 / 静默 / 退出」。关闭浏览器只断 WebSocket，Agent 继续持有 PTY 与 4 MB 环形缓冲。

**4.3 手机上批准一次权限确认**
1. Claude 触发 `PermissionRequest` hook（`http`，`timeout` 设 3600 秒，上限待实测），Agent 上报 Approval，hook 连接挂起。
2. 中转向 Webhook 推送「claude·worktree-b 等你：Bash git push …」，链接直达 `/inbox`。
3. 手机点「允许一次」→ 中转记录 → Agent 对挂起的 hook 返回 `{"hookSpecificOutput":{"hookEventName":"PermissionRequest","decision":"allow"}}` → Claude 继续。
4. 兜底：hook 已超时或版本不支持时，卡片显示「请在终端回答」，进入终端按 `1` + Enter。
5. Codex：MVP 只做通知 + 打开终端，不承诺一键回复。

**4.4 断线重连**

| 断法 | Web 表现 | Agent 行为 | 恢复 |
|---|---|---|---|
| 浏览器网络抖动 / 切后台 | 底栏黄「重连中」，终端保持可见 | 继续写缓冲 | 指数退避 2s→16s 重连，带「已收到最后序号」，Agent 只补差 |
| Agent 被杀 / 崩溃 | 设备灰「Agent 离线，缓冲已保留（中转保留元数据 ≥4 小时）」；会话显示「未知」，绝不显示 Connected | 任务计划程序 1 分钟内拉起；PTY 随进程死亡，会话标 `exited`，最后 4 MB 输出快照落盘 | 会话列表「拉回」：`claude --resume <id>`（id 来自 hook 记录），shell 会话重开 |
| PC 重启 / 睡眠 | 同上 | 登录即自启（安装向导提示自动登录与关闭睡眠） | 同上 |

原则：三个信号分开展示——中转↔Agent 心跳（15 秒）、Agent↔PTY 进程存活、PTY 最近输出时间。

**4.5 切换机器**
`/devices` 点另一台 → `/sessions` 自动过滤 → 打开会话；标签可跨设备混排并带设备名。第二台配对流程与第一台完全相同，所以多机在 MVP 里是零额外开发、仅需验证（验收 E3）。

## 5. 第一阶段 MVP：范围、非目标、验收标准

**5.1 范围（6 周）**

| 周 | 交付 |
|---|---|
| 1 | Agent 骨架：ConPTY 起 pwsh / cmd（Git Bash / WSL 尽力）；环形缓冲 + 序号；本地调试页。语言选最熟的：Rust（`portable-pty`）或 Go（`charmbracelet/x/xpty`）；不选 Node（分发与自启是硬伤） |
| 2 | 中转：设备注册、配对码、路由、心跳、离线元数据；TLS；SQLite；Docker 镜像 |
| 3 | Web：React + xterm.js 6（fit/webgl/search/serialize）；多标签；重连补差；快照；PWA manifest |
| 4 | Claude hooks 安装器；四态会话状态；`/inbox` 审批闭环；Webhook 通知模板 |
| 5 | 手机端：隐藏 input + 键盘栏 + 视口处理；真机矩阵（Android Chrome + GBoard、iOS Safari）——最易超期，整周预留 |
| 6（缓冲） | 任务计划程序自启 + 崩溃重启；「拉回」；远程 Esc / Ctrl-C / kill；安装向导；文档。代码签名先不做，Defender 误报靠文档说明 |

**5.2 非目标**：Windows 服务化（Session 0 + `CreateProcessAsUser` 与 ConPTY 组合的 microsoft/terminal #11865 仍 Open）、E2E 加密、WebRTC 直连、多中转节点、原生 App、Web Push、多用户 / 审计 / 只读观察者、用量面板、跨机接续、录制回放、文件传输、Codex app-server、MiniMax / Kimi / Grok / OpenCode 适配、IM 完整聊天桥接、批量审批规则。

**5.3 验收标准（可脚本或真机复现）**

- A 会话保持与重连：A1 关浏览器 24 小时后重开，会话仍在、可见最后 ≥2000 行；A2 断网 60 秒恢复后与 Agent 端逐字节一致（`seq 1 20000` 比对）；A3 恢复到可输入 ≤5 秒；A4 8 个标签同时打开、1 个持续输出，切换无 >100 ms 掉帧；A5 `taskkill` 杀 Agent 后 ≤60 秒自动上线，PC 重启登录后 ≤60 秒上线；A6 Agent 离线期间绝不显示「已连接」，进程退出 ≤15 秒显示「已退出」。
- B 审批闭环（Claude Code）：B1 hook 触发到 Webhook 送达 ≤5 秒、`/inbox` 出现 ≤3 秒；B2 点允许到 Claude 继续 ≤3 秒，拒绝后 Claude 收到 deny 并继续对话；B3 挂起 30 分钟后回答仍生效（验证 timeout 真实有效，不足则启用兜底文案）；B4 不装 hooks 时产品仍完整可用。
- C 手机可用：C1 Android Chrome + GBoard 与 iOS Safari 各能完成：中文提示词 + Enter、Esc、Ctrl-C、↑ 调历史、Tab；C2 连续 50 个中文字符无重复无乱序；C3 软键盘弹出后提示符仍可见；C4 PWA 加到主屏后从通知直达 `/inbox` 卡片。
- D 安全默认值：D1 Agent 无 0.0.0.0 监听（`netstat -ano`），仅 127.0.0.1 hooks 端口；D2 配对码 5 分钟 / 一次 / 错 5 次锁 15 分钟，吊销后 ≤15 秒断连且无法重连；D3 中转仅 443/TLS，无密码不可访问任何页，无 token 的 WS 被拒；D4 中转日志不含终端内容，只含设备 / 会话 / 字节数 / 时间。
- E 性能与兼容：E1 pwsh / cmd 首屏 ≤1 秒（系统 ConPTY 路径）；E2 `claude` 与 `codex` TUI 在 80 / 120 / 40 列各正常渲染与 resize；E3 两台 Windows 同时在线互不串扰。

**5.4 为什么敢先不做 E2E**：MVP 用户自己部署中转、自己一个人用，攻击面是 VPS 被入侵与 Web 密码泄露，不是「运营者窥探」——运营者是自己。帧格式第一天就把 payload 当不透明字节，1.1 加会话级 AES-GCM 时不改协议。**一旦对外托管或多用户，E2E 是前置条件。**

## 6. 后续路线图（每项注明 MVP 为何砍掉）

- **v1.1（MVP 后 4–6 周，「敢给朋友用」）**：会话级 E2E + fail-closed（吸取 RustDesk 验签失败降级明文教训）；Windows 服务化（服务做看门狗、用户会话内跑 PTY host 的双进程，砍掉是因 #11865 需实测周期）；Web Push；Codex `app-server`（`--listen ws://`、服务端发起审批、`thread/resume`）实现一键审批；用量面板 v0（解析 statusLine / `/cost`，额度耗尽自动续跑，回应 #35744）。
- **v1.2（多机 fleet + 工具无关）**：设备页聚合列（等你数、运行数、离线告警）与分组；会话按项目 / worktree 分组命名；ACP 适配层一次接入 Kimi / Gemini / Qwen / OpenCode / Grok Build；MiniMax 作「供应商预设」一键切端点；跨机接续仍不做（调研结论：几乎无人做成）。
- **v2（多端与团队）**：iOS / Android 壳（Expo/Capacitor 包 PWA，为推送与后台连接稳定，避开 Termius 后台 30 秒断）；Tauri 桌面控制端；多用户、机器授权、审批分级、审计日志、只读观察者链接；多中转节点、可选 WebRTC 直连；Linux / macOS 被控端（PTY 层换标准 pty 即可）。

## 7. 商业化 / 开源策略

**建议：开源 Agent + 开源中转（Apache-2.0 / MIT），收费在托管中转与团队功能。**

1. 不选 AGPL：Paseo（Apache-2.0）、Happy、cc-connect（MIT）都是宽松许可，claudecodeui 的 AGPL 被记为对商用二开不友好。被控端是持有用户凭据的常驻进程，在国内「远控 = 涉诈」语境下（企业 / 高校禁装向日葵 / ToDesk），**开源是信任前提**。
2. MVP 阶段零收费、零托管，只有「自建中转」一种形态，验证「国内可达 + Windows 保活 + 任意 API 配置」是否真能让人放弃 Happy / Paseo。失败信号：Paseo 补上机器面板与 Windows 服务化，或 Anthropic 放开自定义 `ANTHROPIC_BASE_URL`（#76653）。
3. v1.2 起收费点（锚点 CloudCLI €7/月、Omnara $9/月、MobileCLI $19.99/年，待核实）：托管中转（国内节点、含 Push 与 IM 通道）个人 ¥15–29/月量级（待验证）；团队版按席位（授权、分级审批、审计、观察者）；私有化部署 + 支持（面向被官方排除的 ZDR / Bedrock 团队）。
4. 不做：代理 API 转售（法律与账号风险，且与「不碰模型链路」冲突）；免费额度递减式套路（ToDesk 削减额度的口碑代价，数字待核实但方向明确）。
5. 社区：GitHub + V2EX / 少数派实测文；README 第一屏正面回答「为什么不用 Paseo」——真终端、Windows 自启保活、国内一条命令自建、任何 API 配置；同时诚实写「Mac 上只用 Claude 且能直连官方的人，用官方 Remote Control 就好」。

## 8. 风险与开放问题

| 风险 | 概率 | 应对 |
|---|---|---|
| `PermissionRequest` hook 最大挂起时长不够（默认 60 秒量级，上限待实测） | 中 | 第 4 周先测（B3）；不够则退化为通知 + 打开终端 + 键盘栏一键 |
| Codex 无法一键审批 | 高 | MVP 只承诺通知；1.1 走 app-server |
| 未签名 exe 被 Defender / SmartScreen 拦 | 高 | 文档 + 误报提交；有收入再买证书 |
| 手机输入层调不稳 | 中 | 第 5 周整周预留；不达标则手机端降级为「看 + 审批 + 简单命令」 |
| Paseo 6 周内补齐 Windows 服务化 + 真终端 | 低–中 | 退守「国内可达 + 中文 IM 通道 + 审批持久化」 |
| 官方放开非官方 base URL 的 RC | 低 | 亮点 1 消失，2 / 3 / 5 仍在 |

## 附：一手来源（2026-09-03 复核）

- Claude Code · Remote Control（API key 不支持、`ANTHROPIC_BASE_URL` 限制、tmux 建议、server 模式约 10 分钟退出、`dialogExpiry`、ZDR 不可用）：https://code.claude.com/docs/en/remote-control
- Claude Code · Hooks（`PermissionRequest` 输入输出与 `allow|deny|dontAsk`、`Notification` matcher、Windows exec 形式）：https://code.claude.com/docs/en/hooks
- Claude Code · Agent view（六态模型、「Background sessions run on your machine」）：https://code.claude.com/docs/en/agent-view ；CLI reference：https://code.claude.com/docs/en/cli-reference
- Win32-OpenSSH #2291（2024-10-23 开启，Open，无 assignee）：https://github.com/PowerShell/Win32-OpenSSH/issues/2291
- claude-code #34255：https://github.com/anthropics/claude-code/issues/34255 ；#51267：https://github.com/anthropics/claude-code/issues/51267
- happy #496（「Currently unusable on Windows」）：https://github.com/slopus/happy/issues/496
- Paseo（Apache-2.0，15.9k★，5 种 agent，Windows daemon）：https://github.com/getpaseo/paseo
- node-pty（winpty 移除，Win10 1809+）：https://github.com/microsoft/node-pty ；xterm.js #3600：https://github.com/xtermjs/xterm.js/issues/3600
- Codex app-server README（`--listen ws://`、服务端发起审批、`thread/resume`）：https://github.com/openai/codex/tree/main/codex-rs/app-server
- 其余（codex #34584、claude-code #77252 / #29214 / #35744 / #2112 / #76653、happy #1208、VibeTunnel #252、microsoft/terminal #11865、psmux、Zellij）沿用 `docs/research/02–06` 口径。
