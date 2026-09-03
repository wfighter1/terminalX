# 方案 A · AI 原生监工台：把 terminalX 定义为「多 agent 监工控制台」

> 日期：2026-09-03 ｜ 角度：AI 原生 ｜ 依据：`docs/01-市场调研总览与痛点分析.md`、`docs/research/03/04/05/06`，并于本日重新核对 Claude Code 官方文档（hooks / statusline / remote-control / agent-view / cli-reference / interactive-mode / settings-reference / costs）、Codex `app-server` README、Win32-OpenSSH #2291、claude-code #29214/#35744/#30154、Paseo 与 Happy 仓库。WebSearch 配额已耗尽，本文核实全部通过 WebFetch 打开原文完成；无法打开的条目标「待核实」。
> 一句话：**终端只是底层，产品卖的是「谁在等我、我一键放行、夜里它自己跑、早上给我账单」。**

---

## 0. 先回答两个最硬的问题

**为什么不用 Paseo？** Paseo（Apache-2.0，15.9k★）是「单 daemon + 多 agent + 可选 E2E relay」，与 terminalX 架构同构。本方案的差异不在架构，而在**一等对象**：Paseo 的一等对象是「会话」，terminalX 的一等对象是「待确认事项（Approval）」和「机器」。Paseo 的 README 没有跨机器收件箱、成本面板、无人值守策略这三样（本日核对其仓库首页，未见提及；细节待核实），而这三样正是画像 B「20 个会话里哪 3 个在等我」和画像 A「凌晨 3 点额度耗尽停摆」的直接答案。

**Anthropic 自己补上怎么办？** 已经在补：Agent View 有 Working/Needs input/Idle 状态模型；v2.1.234 起 claude.ai 订阅用户额度耗尽可自动等待续跑（`autoContinueAtUsageLimit`）；hooks 已有 `quota_auto_resume_*` 通知类型。但官方文档同时写明三条边界：Agent View「Sessions are local」（单机）；自动续跑「API keys、cloud providers、无 claude.ai 登录的 LLM gateway 一律不提供」；Remote Control 对 API Key / 自定义 `ANTHROPIC_BASE_URL` / Bedrock / ZDR 不可用。**terminalX 的护城河 = 跨机器 × 跨工具 × 走中转 API 的那群人**，而不是「比官方更好的 Claude 遥控」。

---

## 1. 定位、目标用户、核心场景

**一句话定位**：terminalX 是一个自建的「多 agent 监工控制台」——在一个 Web 页面（和手机）上看清多台机器上 Claude Code / Codex / 其他 AI CLI 的状态，一键处理它们的「等你确认」，夜里让它们按你的规则自己跑，早上收到账单和夜报；需要时随时下潜到真终端。

**目标用户**（按优先级）：
1. 画像 A「盯盘的独立开发者」——Windows 台式机 + 中转 API Key + 手机；官方遥控对他直接不可用。
2. 画像 B「多机多 agent 监工」——2–4 台机器 10–20 个会话，要「谁在等我」总览与用量。
3. 画像 C「合规敏感的团队用户」——Bedrock/网关/ZDR，需要自建、审计、只读观察者（第三阶段）。

**核心场景：小周的一天**

| 时刻 | 场景 | terminalX 页面 | 没有 terminalX 时 |
|---|---|---|---|
| 08:30 | 在台式机上起 4 个 worktree 任务（3 个 Claude Code、1 个 Codex），出门 | 会话列表：4 张卡片，命名、目录、权限模式一目了然 | 4 个终端窗口 + tmux/WSL |
| 09:40 地铁 | 手机锁屏推送：「auth-refactor 想执行 `npm test`」→ 长按「允许」 | 收件箱 → 审批卡片 | ToDesk 看不清；等审批 40 分钟无人知（#13024 👍81） |
| 11:15 会议间隙 | 看一眼首页：3 运行中 / 1 等我 / 0 出错；等我的那个是 `git push`，点开看 diff，拒绝 | 首页状态条 + 收件箱 | 不知道哪个在等（Agent View 只在电脑本地） |
| 14:00 咖啡馆 | Codex 卡在一个交互式选项，需要按方向键 | 终端视图 + 虚拟键条（Esc/↑↓/Ctrl） | 手机键盘没有 Esc/方向键 |
| 18:00 | 成本面板：今天 $6.4，5 小时额度 71%；`db-migrate` 会话烧了一半 | 成本面板 | `/cost` 逐个会话看 |
| 23:30 | 给 `db-migrate` 开「通宵模式」：只允许读/测试/编辑，遇到 push 与 rm 等我；额度耗尽等重置续跑 | 会话 → 无人值守策略 | `--dangerously-skip-permissions` + 祈祷 |
| 07:30 | 手机收到夜报：跑了 6 小时 12 分，完成 3/4 步，1 个待确认，花了 $3.1，额度重置后自动续跑过 1 次 | 夜报推送 → 收件箱 | 早上才发现凌晨 3 点停了（#35744） |

---

## 2. 产品亮点（诚实标注强弱）

### 亮点 1（强）：跨机器、跨工具的「待确认」收件箱
- **是什么**：所有 agent 的权限请求、提问（AskUserQuestion）、计划确认、额度耗尽、出错，汇成一个按优先级排序的收件箱；每一项是持久化对象（不过期、可回溯、可批量），带工具名、命令/diff、目录、机器、风险标签。
- **为什么现有方案没做到**：官方 Remote Control 一进程一会话、只管 Claude、审批以外的转发对话框 5 分钟过期（`dialogExpiry`）；远程端不继承本地 `--dangerously-skip-permissions`（#29214，2026-02-27 开启，本日核对仍 Open、无官方回复）；Happy 审批久未确认会断连（#1208）；OpenCode 多 agent 时「不知道是哪个在要审批」（#15332）。没有人把「审批」做成跨工具的一等对象，因为每家只需要管自己。
- **不改 CLI 怎么实现**：见第 3 节「感知层」。Claude Code 用 `PermissionRequest` hook（可返回 `allow/deny` 决策）+ `Notification(permission_prompt)`；Codex 用 `app-server` 的 `item/commandExecution/requestApproval` 与 `item/fileChange/requestApproval`（本日核对其 README）；其他工具用 command hook 打本地 HTTP 端点；都没有时退回 PTY 文本探测并把 `y/n/Enter` 注入 PTY。

### 亮点 2（强）：机器为一级实体的 fleet 视图 + agent 状态感知
- **是什么**：设备列表按机器聚合「在线/离线/几个在跑/几个等我/几个出错/今日用量」；会话列表跨机器、跨工具，状态模型对齐官方 Agent View（Working / Needs input / Idle / Completed / Failed / Stopped）再加一个诚实的 **Unknown（信号陈旧）**。
- **为什么没做到**：官方文档「Sessions are local」；Happy/Happier 只有多机会话列表；Paseo 单 daemon 视角。
- **弱点**：多机器要到第二阶段才真正兑现，第一阶段只有一台 Windows；但数据模型第一天就是 `machine → session → event`。

### 亮点 3（中强）：手机上一键批准，且**继承本地权限模式**
- **是什么**：推送通知直接带「允许 / 拒绝」动作（Android Web Push actions；iOS PWA 只能点开再按，待真机验证）；terminalX 不在远程端叠加自己的审批层——你用什么 permission mode 启动，远程看到的就是什么。
- **为什么没做到**：官方把远程当成独立客户端，#29214 至今未修；Codex 手机接续降级权限（#30485）。
- **弱点**：「一键批准」本身 Happy/官方都有；差异只在跨工具 + 不降级 + 不过期。

### 亮点 4（中）：成本与额度面板
- **是什么**：每机器/每会话/每工具的 token、估算美元、5h/7d 额度百分比与重置时间；80% 告警。
- **数据源已核实**：Claude Code `statusLine` 命令收到的 JSON 含 `cost.total_cost_usd`、`context_window.used_percentage`、`rate_limits.five_hour/seven_day.used_percentage/resets_at`（仅 claude.ai 订阅或 Claude apps gateway 才有 `rate_limits`）；`-p` 模式的 `result` 事件带 usage；Codex `turn/completed` 带 usage（无美元）。
- **弱点**：Claude 官方也声明美元是「客户端按牌价估算」；中转 API 用户的真实价目要手填；Codex 的 5h/周额度没有程序化接口（待核实），面板会有「—」。

### 亮点 5（中，但对国内用户是强）：通宵无人值守模式
- **是什么**：每会话一份策略：自动放行规则（allowlist）、风险分级（高风险必须人批）、额度耗尽等待重置后自动续跑、进程崩溃 watchdog 拉起、超时上限、早晨夜报。
- **为什么官方不做**：v2.1.234 起 Claude 已有原生自动续跑，**但官方文档明确「API keys、cloud providers、无 claude.ai 登录的 gateway 不提供」**——这恰好是 terminalX 的核心人群；Codex 与其他工具则完全没有。
- **弱点**：对 claude.ai 订阅用户，这一项与官方重叠；terminalX 的增量是跨工具 + 策略 + 夜报。

### 弱亮点（不作为卖点）：任务看板
Cline 官网已有 Kanban，Agent View 本身就是列表。第一阶段「任务」不做独立实体，只是「带目标文本的会话」按状态分列；等第二阶段有多机器与定时拉起后再决定要不要真正的看板。

---

## 3. 感知层：不修改 CLI 的前提下如何知道 agent 在干什么

### 3.1 三层接入，逐工具取最高可用层

```
┌───────────── terminalX Agent（Windows 被控端，Rust，仅出站 WSS）─────────────┐
│  L2 结构化驱动  Claude: claude -p --input-format stream-json                   │
│                  --output-format stream-json --permission-prompt-tool mcp__tx  │
│                 Codex: codex app-server (JSON-RPC, stdio)                       │
│  L1 旁路信号    本地 HTTP 端点 127.0.0.1:<port>/hook/<tool>                     │
│                 Claude/Qwen: http 型 hook（零脚本）  Codex: notify + hooks.json  │
│                 Gemini/Kimi/Cline/Droid: command hook = 一行 curl.exe            │
│                 Claude statusLine 命令 → POST 成本 JSON                          │
│  L0 PTY 兜底    ConPTY 字节流 → xterm 视图 + 启发式状态机                        │
└──────────────────────────────────────────────────────────────────────────────┘
```

- **L1 是第一阶段的主力**（交互式 TUI 不变，用户在电脑前的体验零改动）。配对时向导自动写入：Claude `~/.claude/settings.json` 的 `hooks`（`PermissionRequest`、`Notification`、`Stop`、`StopFailure`、`SessionStart/End`，全部 `http` 类型，配合 `allowedHttpHookUrls` 限定 localhost）与 `statusLine`；Codex `config.toml` 的 `notify` 与 `hooks.json`（`PermissionRequest`/`Stop`）。
- **L2 用于 terminalX 自己拉起的「托管会话」**：Claude 的 `--permission-prompt-tool` 把审批变成一次 MCP 工具调用，terminalX 内置的 MCP server 返回 `allow/deny`；Codex 走 `app-server`，决策值 `accept / acceptForSession / decline`。L2 会话在终端视图里看到的是 terminalX 渲染的事件流而非原生 TUI，因此第一阶段只对「无人值守模式」默认开启 L2。
- **L0 永远存在**：真终端是兜底与调试视图。

### 3.2 状态机与置信度

| 状态 | 高置信信号（hook/协议） | 低置信信号（PTY 启发式） |
|---|---|---|
| Working | `PreToolUse`/`turn/started`、stream 有 `assistant` 增量 | 输出持续变化 |
| Needs input · permission | `PermissionRequest` / `Notification(permission_prompt)` / `requestApproval` | 匹配「Do you want to」「(y/n)」「❯ 1. Yes」等模式且 5 s 无输出 |
| Needs input · question | `Notification(agent_needs_input)`、`elicitation_dialog`、`AskUserQuestion` | 同上 |
| Idle | `Notification(idle_prompt)`、`Stop`、`turn/completed` 后无新 turn | 光标停在提示符 >30 s |
| Failed | `StopFailure`、`turn/failed`、进程退出码非 0 | 退出码 |
| Quota wait | `Notification(quota_auto_resume_*)`；额度文本匹配 | 「limit reached · resets」文本 |
| Unknown | 心跳正常但 >N 分钟无任何信号 | — |

规则：只用低置信信号得到的「等你确认」在 UI 上标「疑似」，推送延迟 15 s 去抖；高置信信号即时推送。启发式模式库按工具维护、可热更新。

### 3.3 审批的回写路径（决定「一键批准」是否真的一键）

| 级别 | 工具 | 回写方式 | 已知限制 |
|---|---|---|---|
| A 可编程 | Claude（hook 决策 / prompt tool）、Codex（app-server）、OpenCode（REST） | 直接返回决策 | hook 有超时（默认约 60 s，可配，待核实上限）：超时未决策时 Claude 回落到终端提示，此时转 B 级 |
| B 注入 | Codex 交互式、Gemini/Kimi/Cline | 向 PTY 注入 `y`/`Enter`/方向键，并回读屏幕确认已消费 | 需要提示符仍在屏幕上；注入后 2 s 内未见变化则告警 |
| C 探测 | Aider、Amp、Grok Build | 同 B，但触发也靠文本探测 | 标「疑似」 |

因此「不过期」的实现是：**收件箱记录不过期，终端里的提示由 CLI 自己保持打开（Claude 文档写明权限提示与 AskUserQuestion 会一直等）**，用户随时回来处理时 terminalX 选择 A 或 B 路径回写。

---

## 4. 信息架构与关键页面

```
Web 控制台
├─ 首页 = 收件箱（Inbox）      ← 默认落地页，不是终端
├─ 设备（Fleet）
├─ 会话（Sessions）  ── 会话详情 = 终端视图 ⇄ 事件流 双栏
├─ 成本（Usage）
└─ 设置 / 配对 / 通知渠道 / 无人值守策略模板
手机端（PWA，第一阶段）：收件箱 → 会话卡片 → 终端（带键条）
```

### 4.1 收件箱（首页）
```
┌ terminalX ── 3 运行中 · 1 等我 · 0 出错 · 今日 $6.4 · 5h 71% ────────────┐
│ [待处理 1] [疑似 0] [已处理] [静音]                     [批准全部低风险] │
│ ┌─────────────────────────────────────────────────────────────────────┐ │
│ │ ● 等你确认 · 2 分钟前            win-desktop / auth-refactor / Claude│ │
│ │ Bash:  git push origin feat/auth        cwd: D:\work\app     [高风险]│ │
│ │ 「这会推送 3 个提交到远端」                                            │ │
│ │ [允许]  [本会话一直允许]  [拒绝]  [打开终端]                          │ │
│ └─────────────────────────────────────────────────────────────────────┘ │
│ ○ 已完成 · 08:52  win-desktop / write-tests / Codex  「38 个测试通过」   │
│ ○ 额度等待 · 03:12→05:00  db-migrate 已自动续跑                         │
└─────────────────────────────────────────────────────────────────────────┘
```
元素：全局状态条；筛选标签；审批卡片（来源、机器/会话/工具、工具名与参数摘要、diff 预览（Edit/Write 类）、风险标签、四个动作）；提问卡片（选项按钮 + 自由文本）；夜报卡片；批量动作。

### 4.2 设备列表（Fleet）
```
┌ 机器 ───────────────────────────────────────────────────────────────────┐
│ ● win-desktop   Windows 11 · Agent 0.3.1 · 在线 · 心跳 3s 前              │
│   会话 4（3 运行 / 1 等我）· 今日 $6.4 · CPU 34% · 内存 12/32G             │
│   [新建会话] [终端] [重启 Agent] [关机保护:开]                            │
│ ○ office-mac    上次在线 昨天 19:02 · 离线时会话元数据保留 4h（第二阶段）  │
│ [+ 添加机器：生成配对码]                                                  │
└─────────────────────────────────────────────────────────────────────────┘
```
元素：机器卡片（OS、Agent 版本、在线状态与最近心跳、会话计数按状态、今日用量、资源）、机器级操作、添加机器入口。

### 4.3 会话列表（Sessions）
```
筛选：[全部机器▾] [全部工具▾] [状态: 等我 ✓ 运行中 ✓ 空闲 ✓ 已结束]  排序：等我优先
┌────────────────────────────────────────────────────────────────────────┐
│ ● 等我   auth-refactor   Claude · acceptEdits · D:\work\app (wt: auth)  │
│          最近：想执行 git push …            $2.1 · ctx 43% · 2h13m        │
│ ◐ 运行中 write-tests     Codex · workspace-write · D:\work\app (wt: tests)│
│ ◌ 空闲   db-migrate      Claude · 夜间策略 · 上次输出 12 分钟前            │
│ ◇ 已结束 fix-issue-88    Claude · 已完成 · [恢复会话] [归档]               │
└────────────────────────────────────────────────────────────────────────┘
```
元素：会话卡片（状态图标、名称、工具、权限模式、目录/worktree、最近一条 assistant 摘要、本会话费用、上下文占用、时长）、批量操作（重命名、分组、结束、清理孤儿进程）、「恢复历史会话」（扫描 `~/.claude/projects`、`~/.codex/sessions` 生成可恢复列表）。

### 4.4 终端视图（会话详情，双栏）
```
┌ auth-refactor · win-desktop · Claude · acceptEdits ── [Esc][Ctrl-C][重启会话][结束] ┐
│ ┌── xterm.js 真终端（PTY 字节流）───────┐ ┌── 事件流（hook/协议）──────────────┐ │
│ │ ❯ Running npm test …                  │ │ 09:41 PreToolUse Bash npm test     │ │
│ │                                       │ │ 09:41 [等你确认] git push …  [允许]│ │
│ │                                       │ │ 09:39 Edit src/auth.ts (+12 −3)    │ │
│ └───────────────────────────────────────┘ │ 成本 $2.1 · ctx 43% · 5h 71%       │ │
│ [文本输入框 → 发送到 PTY]   手机键条: [Esc][Tab][Ctrl][↑][↓][←][→][/][粘贴]   │
└──────────────────────────────────────────────────────────────────────────────┘
```
元素：标题栏（会话元数据 + 解卡按钮 Esc/Ctrl-C/重启/结束）、xterm 面板（固定列宽、横向滚动、滚回缓冲、搜索）、事件流面板（可折叠，手机默认只显示它）、输入区（自建输入层：隐藏 input + 虚拟键条 + 中文 IME）、连接状态徽标（Live / 重连中 / Agent 离线，最后心跳）。

### 4.5 成本面板
按天/按机器/按会话/按工具四个维度的柱状与表格；额度环（Claude 5h/7d，Codex 待核实）；告警阈值；价目表（中转站自填）。

### 4.6 设置 / 配对
配对码与二维码、通知渠道（Web Push、飞书/企微 webhook、Telegram、ntfy/Bark）、通知规则（按事件类型 × 会话 × 时段）、供应商预设（官方 Anthropic / 中转站 / MiniMax / Bedrock 的环境变量模板）、无人值守策略模板、安全（E2E 指纹、只读链接、审计元数据导出）。

### 4.7 手机端形态
第一阶段 PWA：底部三个 tab（收件箱 / 会话 / 设置）；锁屏推送带动作按钮；终端视图默认事件流，横屏才展开 xterm。第二阶段 Android 原生（FCM 可靠推送）与 iOS TestFlight。

---

## 5. 核心交互流程

### 5.1 首次配对
1. Web 端「添加机器」→ 生成 8 位一次性配对码 + 二维码（含中转地址、配对密钥，5 分钟有效、一次有效）。
2. Windows 上运行安装器（winget 或单 exe）→ 输入配对码 → Agent 与浏览器完成密钥协商（配对根密钥 → HKDF → AES-GCM，中转只见帧头）；换取长期设备凭据；登录自启（任务计划程序）。
3. 向导检测已安装的 CLI（`claude`、`codex`、Git Bash、pwsh、WSL），逐项展示「将写入的 hook/statusLine 配置」并征得同意后写入；对每个工具做一次自检（起一个 `-p` 会话触发 `SessionStart` hook 回到本地端点）。
4. 页面显示机器卡片「在线」；发送一条测试推送到手机。失败路径：配对码过期、端口被占、Defender 拦截，各给明确文案。

### 5.2 启动一个 Claude Code 会话
机器 → 新建会话 → 表单：目录（最近使用 / worktree 新建）、工具（Claude Code / Codex / 纯终端 pwsh/cmd/Git Bash/WSL）、供应商预设（继承机器默认，可选 MiniMax 等）、权限模式（default / acceptEdits / plan / bypass，直接映射 `--permission-mode`）、会话名（映射 `--name`，本地 `/resume` 同名）、首条提示（可空）、是否无人值守。提交后 Agent 以 ConPTY 启动，1 s 内出现在会话列表，`SessionStart` hook 到达后状态由「启动中」变「Working/Idle」。

### 5.3 手机上批准一次权限确认
hook 到达 Agent → 加密上送中转 → 浏览器/PWA 更新收件箱 → Web Push（Android 带「允许/拒绝」动作；iOS 点开）→ 用户长按「允许」→ 回写：若 hook 仍在等待（A 级）直接返回 `allow`；若已超时，Agent 确认终端仍停在提示符后注入 `y`+Enter（B 级）并回读屏幕；收件箱条目变「已允许 · 09:42 · 来自手机」；同一事项在电脑端终端里已被处理则收件箱自动关闭（去重靠 `tool_use_id`）。

### 5.4 断线重连（三层分别可见）
- 浏览器 ↔ 中转：指数退避 2→16 s，徽标「重连中」，恢复后按序号补差，不丢字。
- Agent ↔ 中转：Agent 心跳 15 s；中转 60 s 未见心跳即向所有端广播「Agent 离线（最后心跳 hh:mm）」，会话元数据保留 ≥4 h；Agent 恢复后自动重连并同步状态。
- Agent 进程死亡：任务计划程序「失败即重启」+ 进程内 watchdog；PTY 子进程随 Agent 死亡的会话标记「Stopped · 可恢复」，一键 `--resume <id>`。
- 会话静默挂起（#51267 类）：Unknown 状态 + 「远程解卡」按钮（Esc / Ctrl-C / 重启会话保留 session id）。
明确不做的承诺：不声称「永不掉线」，而是保证**掉线一定可见，且有一个按钮能救**。

### 5.5 切换机器
顶部机器切换器或 URL `/m/<machine>/<session>`；收件箱与会话列表默认跨机器聚合，切换只影响「新建会话」的默认目标与终端视图。离线机器保留卡片与最近状态，禁用操作按钮。

---

## 6. 第一阶段 MVP：范围、非目标、验收标准

**范围（6–8 周，一人）**
1. Windows Agent（Rust + portable-pty，用户态自启，仅出站 WSS）：pwsh/cmd/Git Bash/WSL 四种 shell；ConPTY 会话保活、环形缓冲、序号补差、多端附着。
2. 单节点中转（配对、路由、心跳、离线元数据、审计元数据、TLS）+ E2E v1。
3. Web 控制台五个页面（收件箱、设备（单机）、会话、终端双视图、设置/配对）+ 成本面板 Claude-only。
4. 感知：L0 全量；L1 覆盖 Claude Code（hooks + statusLine）与 Codex（notify + hooks.json）；审批回写 A/B 两级；状态机含 Unknown。
5. 通知：Web Push + 一个国内 IM 渠道（飞书群 webhook，含「回到控制台」深链）+ ntfy。
6. 无人值守 v1：allowlist 自动放行 + 高风险必审 + 额度耗尽自动续跑（Claude 中转/API Key 场景，用 `StopFailure`/文本探测 + 定时注入 `continue`）+ watchdog + 夜报。
7. 手机 PWA + 自建输入层。

**非目标（明确写进 README）**：多机器同时在线（模型支持，UI 只做列表）；macOS/Linux Agent；Windows 服务化与用户会话切换；Codex app-server L2 与 ACP 适配；iOS/Android 原生；团队/RBAC/SSO；跨机器接续；录制回放；P2P；完整 IM 聊天桥接（不与 cc-connect 竞争）；GUI 画面远程。

**验收标准**
- 配对：新机器从下载到「在线」≤5 分钟，无需公网 IP、无需管理员权限。
- 感知：Claude Code 与 Codex 的权限提示在 ≤3 s 内出现在收件箱与手机推送（高置信路径）；PTY 启发式误报率 <10%（内部样本 50 例）。
- 审批：手机端处理后 ≤2 s 终端可见进展；A 级回写成功率 ≥99%，B 级 ≥95% 且失败必告警。
- 权限一致：以 `bypassPermissions` 启动的会话，远程端零审批弹窗（对照 #29214）。
- 断线：拔网线 60 s 内浏览器显示「Agent 离线」；恢复后 30 s 内自动回到 Live 且输出无丢字；Agent 进程被 kill 后 ≤30 s 自启。
- 无人值守：模拟额度耗尽后，重置时间到达 ≤2 分钟内自动续跑；夜报在设定时间推送。
- 成本：`statusLine` 数据到面板延迟 ≤5 s；估算值与 `/usage` 显示一致。
- 移动端：iOS Safari / Android Chrome 真机可输入 Ctrl-C、方向键、中文。
- 安全：中转抓包只见帧头；错误密钥 fail-closed；Defender 无拦截（已签名）。

---

## 7. 后续路线图

| 阶段 | 时间 | 内容 | 兑现的亮点 |
|---|---|---|---|
| 二 | +3 个月 | macOS/Linux Agent；多机器 fleet 视图与聚合告警；Windows 服务 + 用户会话 PTY host 双进程；Codex app-server L2；ACP 适配（Gemini/Qwen/Kimi/Cline/Grok Build）；Android 原生（FCM）与 iOS TestFlight；成本面板多工具；任务看板决策 | 亮点 2 全量、亮点 1 扩到 9 种工具 |
| 三 | +6 个月 | 团队版：组织/成员、机器与会话的授权（谁能控哪台）、审批分级与代批、只读观察者链接、审计日志与导出、SSO/Passkey；企业自托管包（Docker 一条命令） | 画像 C |
| 四 | +9 个月 | 跨机器接续（转录同步）、录制回放、WebRTC 直连、定时拉起与「早班」任务模板 | 画像 B 的定时批量 |

失败信号（触发重新定位）：Paseo 加上机器面板与 Windows 服务化；Anthropic 放开自定义 `ANTHROPIC_BASE_URL` 的 Remote Control（#76653）并把 Agent View 做成多机；此时保留「工具无关 + 国内可达 + 合规自建」三项继续。

---

## 8. 商业化 / 开源策略

**建议：开源核心 + 托管中转 + 团队版收费（open-core），不做纯 SaaS。**
- 开源（Apache-2.0，避开 AGPL 依赖）：Agent、Web 控制台、单节点中转、所有工具适配器与启发式模式库。理由：目标用户是「不信任厂商云」和「境外 relay 连不上」的人，闭源中转会重演 Happy #105 的信任问题；开源也是对抗 Paseo 的唯一姿势。
- 收费点一：托管中转（国内节点、免运维、多节点就近），个人版定价锚点：CloudCLI €7/月、Omnara $9/月、MobileCLI $19.99/年（均待核实），建议 ¥19–29/月或 ¥199/年，含推送通道与夜报；自建永远免费。
- 收费点二：团队版（第三阶段功能：授权、审计、只读观察者、SSO、代批），按席位收费；企业自托管授权。
- 不收费：工具适配器数量、机器数量（个人版建议 ≤5 台软限制）。
- 反面教材要写进定价页：ToDesk 反复削减免费额度（待核实）、RustDesk 强制登录——terminalX 承诺自建版功能不阉割。
- 风险：多工具付费意愿未验证（高热度需求集中在 Claude 生态）；先用中文社区（V2EX/即刻/飞书群）验证「中转 API 用户 × Windows」这一人群的付费转化，再决定托管中转是否投入多节点。

---

## 9. 引用修正与待核实清单
- `docs/01` 与 `06` 把 claude-code #30154 引作「多会话总览」证据；本日打开原文，其标题为「Multi-window support in Claude Code Desktop」（2026-03-02，Open），是 Desktop 多窗口需求，与「多会话并排」相关但不是「谁在等我」的直接证据，建议后续文档改引 #2112「guess the session roulette」与 #33979。
- Claude `PermissionRequest` hook 的最大等待时长与 `--permission-prompt-tool` 的 MCP 调用超时上限：待核实（影响 A 级回写窗口）。
- Codex `notify` 的 `approval-requested` 事件、`approval_policy`/`sandbox_mode` 取值：developers.openai.com 被代理拦截，沿用 `docs/research/04` 的二手描述，待核实。
- Web Push 通知动作在 iOS PWA 的可用性：待真机验证。
- Paseo 是否有审批收件箱/成本面板：仓库首页未见，待读源码核实。

## 10. 主要来源（本日核实）
- Claude Code hooks（事件列表、`PermissionRequest` 决策、`Notification` matcher 含 `quota_auto_resume_*`、Windows PowerShell hook）：https://code.claude.com/docs/en/hooks
- Claude Code statusLine JSON 字段：https://code.claude.com/docs/en/statusline
- Claude Code Agent View（状态模型、Sessions are local）：https://code.claude.com/docs/en/agent-view
- Claude Code Remote Control（API Key / `ANTHROPIC_BASE_URL` / Bedrock / ZDR 限制、tmux、dialogExpiry、推送策略）：https://code.claude.com/docs/en/remote-control
- Claude Code CLI reference（`--permission-prompt-tool`、`--bg`、`agents --json`）：https://code.claude.com/docs/en/cli-reference
- Claude Code interactive mode「Wait for a usage limit to reset」（v2.1.234，API Key/gateway 不提供）：https://code.claude.com/docs/en/interactive-mode
- Claude Code settings reference（`autoContinueAtUsageLimit`、`dialogExpiry`、`allowedHttpHookUrls`、`modelPricing`）：https://code.claude.com/docs/en/settings-reference
- Claude Agent SDK permissions（模式与 canUseTool 评估顺序）：https://code.claude.com/docs/en/agent-sdk/permissions
- Codex app-server README（`item/commandExecution/requestApproval`、`item/fileChange/requestApproval`、WebSocket 实验性）：https://github.com/openai/codex/blob/main/codex-rs/app-server/README.md
- Win32-OpenSSH #2291（2024-10-23，Open）：https://github.com/PowerShell/Win32-OpenSSH/issues/2291
- claude-code #29214（Open，无官方回复）：https://github.com/anthropics/claude-code/issues/29214 ｜ #35744（Open，duplicate 标签）：https://github.com/anthropics/claude-code/issues/35744 ｜ #30154：https://github.com/anthropics/claude-code/issues/30154
- Paseo：https://github.com/getpaseo/paseo ｜ Happy：https://github.com/slopus/happy
- 本仓库调研：`docs/01-市场调研总览与痛点分析.md`、`docs/research/03`、`04`、`05`、`06`
