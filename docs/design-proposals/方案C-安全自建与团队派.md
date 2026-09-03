# 方案 C · 安全自建与团队派：把 terminalX 定义为「自己掌握钥匙的 agent 控制塔」

> 日期：2026-09-03 ｜ 立场：**隐私优先、可自建、默认关闭；先服务一个人，但每一个安全决定都为「三个人共用」预留位置。**
> 依据：`docs/01-市场调研总览与痛点分析.md`、`docs/research/03/04/05/06`；本日用 WebFetch 复核了 Claude Code 官方文档（Remote Control、Data usage、Security、Zero data retention、Managed settings、Hooks、Monitoring usage）、claude-code #88094 / #88875 / #76653、Happy / happy-server / Happier / Paseo / sshx / Zellij 仓库与文档。WebSearch 配额已耗尽，无法核实的数字一律标「待核实」。

---

## 0. 先回答三个最硬的问题

**为什么开发者不放心把 agent 会话交给第三方云？** 不是猜测，是官方文档写明的事实：Claude Code Remote Control「While Remote Control is connected, the session transcript, including your messages, Claude's responses, and tool activity, is stored on Anthropic servers」；消费者账号若允许训练则转录保留 5 年、否则 30 天；ZDR 组织「can't enable Remote Control」（Zero data retention 页把 Remote Control 列在「Features disabled under ZDR」表里）。再加上 2026-08 两起 Windows 用户投诉——#88094「Remote Control Being Turned on by Default」、#88875 会话记录里被静默写入 `remoteControlAutoEligible: true`、「the user had no way to know their sessions were RC-bound」——**开发者担心的不只是「谁能看」，还有「我不知道它开着」**。一个 agent 会话里流过的是源码、`.env`、云凭据、内部域名与 API Key，这些在传统远控里只是屏幕上一闪而过的像素，在 agent 遥控里却是结构化、可检索的转录。

**为什么不用 Happier？** 这是本方案最接近的对手，必须诚实。Happier（MIT，1.6k★）README 原话：「End-to-end encrypted. Self-hostable.」「Share a live session with teammates or via view-only public links.」「A global attention center for permission requests… across all sessions and machines at once.」，还支持 GitHub OAuth 组织门禁、OIDC、mTLS。**它已经是「E2E + 自建 + 协作 + 收件箱」的组合**。terminalX 方案 C 的差异只能落在 Happier 没做的五件事：① 以「机器」为授权单元（谁能控哪台、观察者/操作者/审批者三级）；② 审计与回放（谁在何时对哪台机做了什么，agent 卡住时「它当时想做什么」有记录）；③ 远程审批不得高于本地策略、敏感命令二次确认；④ Windows 服务级被控端 + 真终端；⑤ 国内可达的自建中转与一条命令部署、中文 IM 告警。若 Happier 补齐 ①②③，方案 C 只剩 ④⑤。

**为什么个人用户也需要这些？** 因为「默认关闭 + 显式配对 + 中转零知识」对个人用户的代价是零：一次扫码。它不增加步骤，只是把「谁能连我的电脑」从「登录了同一个账号的任何设备」收窄为「我亲手配对过的设备」。画像 A 的小周不会为「审计」付费，但会为「Happy relay 在境外连不上、官方 RC 不支持中转 Key」换工具；方案 C 用同一套安全底座同时服务他和画像 C 的 Lisa。

---

## 1. 定位、目标用户与核心场景

**一句话定位**：terminalX 是一个自己掌握钥匙的 agent 控制塔——被控端只出站、中转看不见内容、每台设备都要亲手配对，一个 Web 页面（和手机）管多台机器上的 Claude Code / Codex / 其他 AI CLI，能审批、能回放、能把会话只读地分享给同事。

**目标用户（按阶段）**
- 第一阶段：画像 A「盯盘的独立开发者」小周——Windows 11 台式机、Claude Code 走国内中转站 Key、Codex Plus、Android 手机。他被官方 Remote Control 排除（「API keys are not supported」「`ANTHROPIC_BASE_URL`… Unset the variable to use Remote Control」），Happy 境外 relay 超时（happy #105）。他要的是一键安装、扫码配对、手机可用。
- 第二阶段：画像 B 老陈——2–4 台机器 10–20 个会话，要「谁在等我」、审批不降级、用量可见。
- 第三阶段：画像 C Lisa——金融科技后端，公司走 Bedrock/网关且要求 ZDR，笔记本受 EDR 管控、向日葵/ToDesk 在禁装名单。她要的是「数据经过谁的服务器、谁能控哪台机、有没有审计」三个问题有确定答案。

**核心场景：Lisa 的一天（穿插小周的对照）**
- 08:50 内网工位。Lisa 在机房 Windows 工作站上用 terminalX 起一个 Claude Code 会话跑数据迁移脚本，供应商预设选「公司网关（Bedrock）」，权限策略选「工作站默认：写文件自动、`git push` / 删除 / 网络命令需确认」。官方 RC 对她根本不可用（Bedrock、ZDR 双重排除）。
- 12:30 食堂。手机收到公司飞书 webhook 推的告警：「migrate-2024q3 等待确认：`psql -f drop_legacy.sql`」。她点开控制台（自建中转在公司 DMZ，只开 443），审批卡片显示命令、目录、diff 和「该操作属于『破坏性』分级，需要二次确认」。她批准，审计日志记下：谁、何时、哪台机、哪个会话、批准了什么。
- 15:00 站会。同事怀疑迁移脚本逻辑。Lisa 生成一条**只读观察链接**（密钥在 URL `#` 片段，中转拿不到），同事在自己浏览器里看同一个终端，光标动作实时可见，但键盘输入被被控端拒绝；同事要接手时点「申请输入权」，Lisa 在手机上批准，观察者升级为操作者，时限 30 分钟。
- 20:00 家里。公司笔记本 VPN + RDP 会断开即锁屏，但 terminalX 被控端是系统服务，锁屏不影响 ConPTY。她用手机看进度，让 agent 通宵跑；夜里 agent 因权限提示卡住，早上她打开回放，看到 03:12 它想执行什么、为何被策略拦下。
- 对照小周：没有安全部门，只有家里一台 Windows + 手机。他的体验是 `winget install terminalX`、浏览器扫码、手机加到主屏，三分钟；他从不需要碰「审计」「分级」，但它们默认在，他的中转站 Key 也不会出现在任何第三方服务器上。

---

## 2. 产品亮点（诚实标注强弱）

### 亮点 1（强）：「数据经过谁的服务器」有确定答案——中转零知识 + fail-closed + 被控端只出站
- 做法：配对时两端协商根密钥，HKDF 派生方向密钥，AES-256-GCM 逐帧加密；中转只见帧头（会话 ID、序号、长度）与路由元数据；密钥/证书校验失败一律断开、绝不降级明文；被控端不开任何入站端口，只出站 443。
- 为什么现有方案没做到：官方 RC 架构上就是把转录存在厂商服务器（文档原文），ZDR 组织直接禁用；Happy / Happier / Paseo 有 E2E，但 RustDesk 的反例说明「有 E2E」和「fail-closed」不是一回事（研究 05：RustDesk 客户端验签失败时「记录错误并继续以未加密方式握手」）；sshx 明确「Self-hosted deployments are not supported at the moment」；Happy 官方 relay 在境外且出过 522 全站故障（#418）。
- 强弱：**强**。这是架构承诺而非功能，官方永远不会做（它需要转录在云上同步）；对 ZDR / Bedrock / 网关用户是唯一可行路径。弱点：Happier / Paseo 也能自建 E2E relay，差异只在 fail-closed 承诺、国内节点和「数据流向文档」——所以要把数据流向图写进 README 第一屏。

### 亮点 2（强）：设备信任链——默认关闭、显式配对、机器级授权、三级权限
- 做法：远程能力默认关闭；每台控制端设备必须扫码配对一次，配对码一次性、5 分钟过期、绑定中转公钥指纹；配对后签发设备证书（内部 CA），撤销即断。授权以「机器」为单元：每个用户对每台机器是 **观察者（只读）/ 操作者（可输入）/ 审批者（可回复权限请求）** 之一，团队版再加「管理员（可配对/撤销设备）」。登录超过 18 小时的控制端在下一次敏感操作（审批、升级权限）前要求 Passkey / Windows Hello 步进——直接对标 Claude Trusted Devices 的「no more than 18 hours old… confirm presence with Face ID, Touch ID, Windows Hello, or a passkey」。
- 为什么现有方案没做到：官方 RC 的信任单元是「账号」（同账号任何设备），Trusted Devices 仅 Team/Enterprise 且默认关闭；#88094 / #88875 证明官方在「默认开启」上摔过；Happy 是个人多设备同步，happy-server 文档「describes multi-device synchronization for individual users only」；Happier 有协作但授权单元是会话不是机器；Zellij 0.44 有只读 token 但单机、无账号；opencode web 不设密码即裸奔。
- 强弱：**强**（对团队）/ **中**（对个人，个人只感知到「扫一次码」）。风险：权限模型做重了会拖慢 MVP，所以第一阶段只落地「配对 + 只读/可写两级」，三级与团队授权放二期，但数据模型第一天就按三级设计。

### 亮点 3（中强）：审计与回放——「它当时想做什么」有记录
- 做法：两层审计。中转层只记元数据：谁、何时、从哪个设备、连了哪台机/哪个会话、字节数、审批决定（允许/拒绝/超时）——不记内容。被控端层可选录制：asciicast 风格的输出流 + 结构化事件流（`permission.requested`、`turn.completed`、`error`），录制文件用用户密钥加密存在被控端或用户指定存储，中转永远拿不到明文；回放页支持按事件跳转（「跳到下一次审批」）。
- 为什么现有方案没做到：官方 Security 文档里「Audit logging: All operations in cloud sessions are logged」只针对云端 VM 会话，Remote Control 段落没有审计；OpenTelemetry 导出有 `claude_code.tool_decision` 等事件但默认脱敏且仅 Claude 一家；VibeTunnel 有 session recording 但「Windows is not yet supported」；Happy / Paseo / Happier README 未见审计或回放。
- 强弱：**中强**。对合规用户是刚需，对个人是「夜里出了什么事」的安心感。弱点：录制引入「我也在被记录」的心理负担，必须默认关闭、按机器开启、被控端本地提示。

### 亮点 4（中）：只读观察者与 pair debugging——把会话分享给同事，钥匙不经过中转
- 做法：会话主人生成观察链接，会话密钥放在 URL `#` 片段（浏览器不会把片段发给服务器，沿用 sshx 做法）；观察者看到同一个 xterm 画面与事件流、可见他人光标，但输入帧在**被控端**被拒绝（不是前端隐藏按钮）；观察者可「申请输入权」，主人在任一端批准后升级为操作者并带时限；链接可随时吊销，吊销即换密钥。
- 为什么现有方案没做到：sshx 有协作光标与只读密码但不可自建；tmate 有只读链接但需 SSH 且无 agent 语义；Zellij 只读 token 单机；Happier 有「view-only public links」——所以这条**不是空白**，只是把它放进「机器级授权 + 审计」的同一模型里。
- 强弱：**中**。团队用户会用，个人用户偶尔用（远程帮朋友看 agent 卡在哪）。不作为首页卖点。

### 亮点 5（中，对国内用户偏强）：远程审批不得越权，敏感命令二次确认
- 做法：审批是持久对象（不过期、跨端可见、可批量）；远程端的决定权 **不高于** 该会话在被控端启动时的本地策略：本地是 `bypassPermissions` 就不该在手机上逐条弹（对应 #29214 👍79 的反向痛点）；本地是逐条审批，手机上也不能一键切到全放行，除非通过 Passkey 步进；命令按分级（读 / 写工作区 / 破坏性 / 网络 / 越出工作区）着色，「破坏性」和「网络」两级默认要二次确认。策略可由团队管理员下发，思路对标 Claude 的 managed settings（`C:\Program Files\ClaudeCode\managed-settings.json`、`allowManagedPermissionRulesOnly`）。
- 为什么现有方案没做到：官方 RC 转发对话框默认 5 分钟 `dialogExpiry`（权限提示与 AskUserQuestion 例外）；#29214 / codex #30485 说明远程端与本地权限模式不一致是双向的；Happy #1208 审批等太久就断连。没有一家把「远程 ≤ 本地」写成规则。
- 强弱：**中**。技术上依赖研究 04 的 A 级通道（Claude `PermissionRequest` hook / `--permission-prompt-tool`、Codex app-server 审批、OpenCode REST），B/C 级工具只能注入按键，分级着色靠解析命令文本，准确率待验证。

### 不作为亮点：「公司电脑合规」
它不是功能，是包装：只出站 443、无入站端口、可关闭、有审计、有数据流向图、不叫「远程控制」而叫「远程终端」。研究 01 指出远控软件已被反诈体系污名化、企业发文禁装向日葵/ToDesk，所以这份文档本身（安全白皮书）是产品的一部分，但不值得在亮点里占位。

---

## 3. 信息架构与关键页面

```
Web 控制台
├─ 收件箱 Inbox（默认首页：等我确认 / 需要输入 / 出错 / 观察申请）
├─ 设备 Machines（一级实体）
│   └─ 机器详情 → 会话列表 Sessions（二级）
│         └─ 会话详情：终端视图 + 事件流 + 审批卡片
├─ 审计 Audit（元数据日志 + 回放入口）
└─ 设置 Settings
     ├─ 配对与设备（控制端设备列表、撤销）
     ├─ 安全（E2E 指纹、步进认证、录制开关、只读链接管理）
     ├─ 供应商预设（官方 / 中转站 / Bedrock / MiniMax… 环境变量模板）
     ├─ 权限策略（分级规则、远程≤本地）
     └─ 通知渠道（Web Push / 飞书 / 企微 / Telegram / ntfy）
```

### 3.1 设备列表（Machines）
元素：机器卡片（名称、OS、被控端版本、在线/离线/休眠、最后心跳、电源状态）；本机会话计数按状态（运行 / 等确认 / 空闲 / 出错）；我在该机的角色徽章（观察者 / 操作者 / 审批者）；E2E 指纹短码（与被控端托盘显示一致，可目视比对）；操作：新建会话、打开审计、配对新控制端、撤销。团队版加「授权成员」列。

```
┌────────────────────────────────────────────────────────────┐
│ 设备                                    [+ 配对新机器]      │
├────────────────────────────────────────────────────────────┤
│ ● home-win11   Windows 11 · v0.3.1 · 心跳 3s 前            │
│   会话 4 ：运行 2 · 等确认 1 · 出错 1     角色：审批者       │
│   指纹 7QK2-…-M9A  [新建会话] [审计] [⋯]                    │
├────────────────────────────────────────────────────────────┤
│ ○ lab-ws-07    Windows 10 · 离线 2h（上次：睡眠）           │
│   会话 0（元数据已保留）                   角色：观察者       │
└────────────────────────────────────────────────────────────┘
```

### 3.2 会话列表（Sessions）
元素：按机器分组，「等确认」置顶（借鉴官方 Agent View 的 Working / Needs input / Idle / Completed / Failed / Stopped）；每行：名称（可改）、工具图标（Claude / Codex / 通用 shell）、供应商预设、权限策略、状态、最后事件摘要、观察者人数、录制中标记；解卡按钮（Esc / Ctrl-C / 重启 / 结束）；筛选：机器、工具、状态、我有权操作的。

### 3.3 终端视图（会话详情，双栏）
元素：标题栏（机器 › 会话、角色徽章、连接状态 Live / 重连中 / 被控端离线 / 密钥不匹配已断开）；左栏 xterm.js 真终端（固定列宽、回滚、搜索、他人光标）；右栏事件流（审批卡片、AskUserQuestion、turn 完成、错误、观察者加入/退出）；输入区（自建隐藏 input + 虚拟键条：Esc / Tab / Ctrl 粘滞 / 方向键 / Ctrl-C / 粘贴；观察者看到的是灰色「只读」条 + 「申请输入权」）；分享按钮（生成只读链接 / 吊销）；录制开关（本机策略允许时）。

```
┌ home-win11 › migrate-2024q3   审批者   ● Live  [分享▾] [⏺] ┐
├──────────────────────────────┬──────────────────────────────┤
│ $ claude --permission-mode … │ 12:31 等待确认  [破坏性]      │
│ ⏺ Bash(psql -f drop_legacy…) │  psql -f drop_legacy.sql      │
│   Do you want to proceed?    │  目录 D:\repo\db  diff +0 -412 │
│ > █                          │  [允许一次] [本会话允许] [拒绝]│
│                              │  ⚠ 需二次确认（破坏性）        │
│                              │ 12:29 turn 完成 · 2.1k tok     │
│                              │ 12:20 同事 wang 以观察者加入   │
├──────────────────────────────┴──────────────────────────────┤
│ [Esc][Tab][Ctrl][↑][↓][←][→][^C][粘贴]  输入…              │
└─────────────────────────────────────────────────────────────┘
```

### 3.4 待确认收件箱（Inbox，首页）
元素：跨机器、跨工具的待办卡片，按「等我审批 → 需要输入 → 出错 → 观察申请」排序；卡片含机器、会话、工具、命令/参数摘要、目录、分级色、等待时长、我的角色（无审批权则只显示「通知审批者」）；批量操作（对同分级同会话的多条一次放行）；「已超出我的权限」提示（远程 ≤ 本地）。

### 3.5 审计（Audit）
元素：时间线（谁 / 设备 / 机器 / 会话 / 动作 / 结果），动作类型：配对、撤销、连接、断开、审批、拒绝、观察者加入/升级、录制开关、策略变更；筛选与导出（JSON/CSV）；回放入口（有录制的会话显示「回放」，按事件跳转）；数据边界说明：「本页只含元数据，内容仅存在被控端录制文件中」。

### 3.6 设置 / 配对
元素：二维码 + 8 位一次性配对码（含中转地址与配对密钥，5 分钟过期）；控制端设备列表（名称、平台、配对日期、上次使用、撤销）；安全：E2E 指纹、步进认证开关（18 小时）、录制默认值、只读链接列表；供应商预设；权限策略编辑器（分级规则表 + 「远程不得高于本地」固定项）；通知渠道与事件规则。

### 3.7 手机端形态
PWA 优先（添加到主屏，Web Push），二期再套原生壳。手机首页 = 收件箱；机器与会话折叠为两级列表；会话详情默认显示事件流，「进入终端」是显式切换；审批卡片大按钮 + 步进认证（Passkey / 指纹）；只读链接在手机上打开即观察者模式。国内通知走飞书 / 企微 webhook + 「一键回到控制台」，不做完整聊天桥接（不与 cc-connect 竞争）。

---

## 4. 核心交互流程

### 4.1 首次配对（个人一键 vs 团队审批）
1. Windows：`winget install terminalX` 或单 exe；安装向导询问「中转地址」（默认托管节点 / 自填自建），生成被控端密钥对，托盘显示指纹短码。
2. 浏览器打开控制台 → 「配对新机器」→ 显示二维码 + 8 位码；被控端托盘「配对」输入 8 位码（或手机扫码后自动填入）。
3. 两端各自显示同一指纹短码，用户目视比对后确认——**这一步是零知识的前提**，中转无法伪造。
4. 签发设备证书；被控端默认策略：远程 **关闭**，需在此刻显式勾选「允许远程」。
5. 团队版：配对请求进入管理员收件箱，管理员批准并指定该机器的角色分配；被控端在本机弹出「此机器已加入组织 X」提示。
失败路径：指纹不一致 → 拒绝并提示可能被中间人；配对码过期 → 重新生成；网络只放行 443 → 仍可用。

### 4.2 启动一个 Claude Code 会话
1. 机器详情 → 新建会话 → 选工具（Claude Code / Codex / 通用 shell）、shell（pwsh / cmd / Git Bash / WSL）、工作目录、供应商预设（官方 / 中转站 / Bedrock…，环境变量在被控端注入，不经中转明文）、权限策略模板。
2. 被控端用 ConPTY 起进程，同时把 hooks 配置写好：Claude 用 `http` 型 `PermissionRequest` / `Notification` hook 打本机 `127.0.0.1` 端点（零脚本、天然跨 Windows），Codex 走 app-server。
3. 会话进入列表，状态「运行」；录制若开启，被控端本地终端标题栏显示 ⏺。
4. 主人可立刻生成只读链接给同事。

### 4.3 手机上批准一次权限确认
1. 被控端收到 `PermissionRequest`（含 `tool_name`、`tool_input.command`）→ 分级 → 加密上报 → 中转投递 Web Push / 飞书。
2. 手机打开收件箱卡片：命令、目录、diff、分级、等待时长。
3. 若我是审批者且登录 <18h：点「允许一次」，决定经 A 级通道回写（hook 决策 JSON / app-server `accept`）；若登录 >18h 或分级为「破坏性 / 网络」：先 Passkey / 指纹步进，再放行。
4. 若我只是操作者 / 观察者：卡片显示「你无审批权，已通知审批者」。
5. 审计写入一条；卡片不会过期（对比官方 5 分钟 `dialogExpiry`），agent 一直等到有人决定或主人在本机处理。

### 4.4 断线重连（fail-closed 且状态显式）
- 浏览器断：指数退避 2s→16s 重连，带上「最后收到序号」补差；期间标题栏「重连中」。
- 被控端断（网络 / 睡眠 / 重启）：中转保留会话元数据 ≥ 4 小时；控制端显示「被控端离线（上次：睡眠 02:14）」而不是假 Connected（对应 #34255 / #77252）；被控端作为 Windows 服务随开机自启并重新附着已存活的 ConPTY 或按策略重启会话。
- 密钥校验失败：直接断开并在审计里记「密钥不匹配」，UI 显示红色「已断开：密钥不匹配，可能被篡改」，绝不降级明文。
- 手机后台：PWA 回前台先补差再渲染，Push 不依赖长连接。

### 4.5 切换机器
设备列表点另一台机器（或收件箱卡片直达）；每台机器独立密钥，切换即切换加密上下文；角色徽章随之变化（在 A 机是审批者，在 B 机可能只是观察者）；跨机器接续会话（把会话迁到另一台）**不做**，只提供「在 B 机用同一模板新建」。

---

## 5. 第一阶段 MVP：范围、非目标、验收标准

**目标**：一个 Web 页面控制一台 Windows 上的多个终端 / AI CLI 会话；从第一天起就是「只出站 + 显式配对 + 中转零知识 + 只读分享」，但团队功能只留数据模型。

**范围（P0）**
1. Windows 被控端（Rust + `portable-pty`，用户态自启；服务化放二期）：pwsh / cmd / Git Bash / WSL 四种 shell，环形缓冲 + 序号补差，远程 Esc / Ctrl-C / 重启。
2. 单节点自建中转（Rust，SQLite，单二进制 / Docker 一条命令，443 + 自动证书）：配对、路由、心跳、离线元数据、审计元数据。
3. E2E v1：配对根密钥 → HKDF → AES-256-GCM；指纹目视比对；fail-closed。
4. 配对与设备管理：二维码 + 8 位码，设备列表与撤销，远程默认关闭。
5. 权限两级：只读 / 可写（数据模型按三级建）；只读链接（密钥在 `#` 片段）+ 吊销。
6. Claude Code 与 Codex 的审批感知与一键回复（Claude `http` hook；Codex app-server），审批持久化不过期，分级着色 v0（读 / 写 / 破坏性 / 网络四类正则）。
7. 收件箱 + 设备 / 会话列表 + 终端双栏视图 + 审计元数据页（无回放）。
8. 手机 PWA：自建输入层 + 虚拟键条 + Web Push；飞书 / 企微 webhook 告警。
9. 供应商预设：官方 / 中转站（自定义 `ANTHROPIC_BASE_URL` + Key）/ MiniMax（OpenAI 兼容端点，待核实）。

**非目标（写进 README）**：多机器同时管理的聚合视图（模型支持，UI 只做列表）；macOS / Linux 被控端；Windows 服务化与用户会话切换（microsoft/terminal #11865 需实测）；录制回放；三级权限与团队组织 / SSO / 管理员审批配对；观察者升级流程；Passkey 步进；WebRTC 直连；多中转节点；跨机器接续；用量面板；GUI 画面远程；完整 IM 聊天桥接。

**验收标准**
- 安全：中转抓包只见帧头；改动任一端密钥后连接在 1 秒内断开且 UI 显示「密钥不匹配」；被控端 `netstat` 无监听端口；配对码 5 分钟后失效、重放无效；吊销设备后 ≤5 秒断开。
- 可用：Windows 断网 60 秒恢复后无丢字、无重复；PC 睡眠唤醒后被控端 ≤30 秒重新上线且会话列表状态正确；iOS Safari / Android Chrome 真机可输入 Ctrl-C、方向键、中文。
- 审批：Claude Code 与 Codex 各触发 20 次权限请求，手机收到推送 ≤5 秒、回写成功率 100%、等待 30 分钟后仍可回复；本地 `bypassPermissions` 会话在手机上 0 次弹窗。
- 分享：只读链接打开者输入 100 次，被控端拒绝 100 次并记 1 条审计（合并）；吊销后链接 ≤5 秒失效。
- 安装：`winget` 一条命令，Defender 无拦截（Authenticode 签名），从安装到手机看到终端 ≤3 分钟。
- 正面回答「为什么不用 Happier / Paseo」：README 首屏给出数据流向图 + 上述五条安全验收结果。

---

## 6. 后续路线图

| 阶段 | 时间 | 内容 | 兑现 |
|---|---|---|---|
| 1.1 | MVP 后 4–6 周 | Windows 服务化（服务看门狗 + 用户会话 PTY host 双进程）；Passkey / Windows Hello 步进；录制（asciicast 风格 + 事件流，用户密钥加密）与回放页；分级着色 v1（命令解析而非正则） | 亮点 3、亮点 5 |
| 2 | +3 个月 | macOS / Linux 被控端；多机 fleet 聚合视图与告警；三级权限（观察者 / 操作者 / 审批者）与观察者升级流程；Codex app-server 深度接入、ACP 适配（Gemini / Qwen / Kimi / Grok Build）；Android（FCM）与 iOS（TestFlight）壳；用量面板 | 亮点 2 全量、亮点 4 |
| 3 | +6 个月 | 团队版：组织 / 成员 / 机器授权矩阵、管理员审批配对、策略下发（对标 managed settings）、审计导出与保留期、SSO（OIDC）、多中转节点与就近路由、可选 WebRTC 直连；安全白皮书与第三方渗透测试报告（待预算） | 画像 C |
| 4 | 视验证 | 企业私有化包（离线安装、内网证书）、SIEM 对接（OTLP 导出审计事件）、跨机器接续 | — |

失败信号：Happier 补上机器级授权 + 审计回放 + Windows 服务化；或 Anthropic 放开自定义 `ANTHROPIC_BASE_URL` 的 Remote Control（#76653 已被作为重复关闭，说明需求存在）并为 ZDR 组织提供不存转录的模式。届时保留「工具无关 + 国内可达 + 真终端 + 审计」继续。

---

## 7. 商业化 / 开源策略

**建议：开源被控端 + 开源中转（Apache-2.0），托管中转与团队 / 企业功能收费；不做纯 SaaS。**

- **必须开源的部分**：被控端、中转、E2E 协议、控制台前端。理由是本方案的全部卖点建立在「你可以自己验证中转看不见内容」之上——闭源的零知识没有说服力；RustDesk / Happy / Paseo 都以开源换信任。避开 AGPL（claudecodeui 的教训）以便企业二次开发。
- **收费点一：托管中转**（国内节点、免运维、多节点就近、含 Push 与 IM 通道）。个人 ¥19–29/月或 ¥199/年量级（锚点：CloudCLI €7/月、Omnara $9/月、MobileCLI $19.99/年，均待核实）。托管中转与自建功能完全一致，承诺写进定价页——反面教材是 ToDesk 反复削减免费额度（待核实）与 RustDesk 公共服务器强制登录。
- **收费点二：团队版**（按席位）：机器授权矩阵、三级权限、审计保留与导出、SSO、策略下发、管理员配对审批。
- **收费点三：企业私有化 + 支持**：面向被官方排除的 ZDR / Bedrock / 网关团队，按年授权 + 渗透测试报告 + SLA。这是唯一一类「官方永远不会服务」的客户，也是方案 C 相对方案 A / B 多出的收入来源。
- **不收费的承诺**：自建版永远包含 E2E、配对、只读分享、审计元数据——安全不是付费墙后的功能，否则「隐私优先」不成立。
- **社区**：README 首屏 = 数据流向图 + 「为什么不用 Happier / Paseo / 官方」三段诚实对比；发布安全白皮书；在 V2EX / 少数派发「自建三分钟」实测文；接受外部审计前不宣称「军用级」之类词。

---

## 8. 风险与开放问题

1. Happier 是最近的对手且迭代快；方案 C 的护城河窄，需要在 MVP 里就把「机器级授权 + 审计 + fail-closed」做成可演示的差异，而不是路线图。
2. 分级着色依赖命令解析，误判「破坏性」会造成审批疲劳；先只做四类保守规则，允许用户按会话关闭二次确认。
3. 录制的心理负担与法律语境（员工监控）：默认关闭、本机可见提示、团队版需在策略里明示；不做键盘输入录制（只录输出与事件）。
4. Windows 服务化与 ConPTY / 用户 token 组合（microsoft/terminal #11865）未验证，MVP 用用户态自启兜底；「国内可达托管节点」的备案成本待核实，MVP 只提供自建；多工具付费意愿未验证（研究 01 §5.2）。

---

## 9. 主要来源（本日复核）

- Claude Code · Remote Control（转录存储、限制、Trusted Devices 18 小时、`dialogExpiry`）：https://code.claude.com/docs/en/remote-control
- Claude Code · Data usage（5 年 / 30 天保留、本地 `~/.claude/projects/` 明文缓存）：https://code.claude.com/docs/en/data-usage
- Claude Code · Zero data retention（「Features disabled under ZDR」含 Remote Control）：https://code.claude.com/docs/en/zero-data-retention
- Claude Code · Security（Remote Control 短期凭据、云端会话审计）：https://code.claude.com/docs/en/security
- Claude Code · Managed settings（Windows 路径、`allowManagedPermissionRulesOnly`）：https://code.claude.com/docs/en/managed-settings
- Claude Code · Hooks（`PermissionRequest`、`http` hook、`Notification` matcher）：https://code.claude.com/docs/en/hooks
- Claude Code · Monitoring usage（OTel 事件默认脱敏）：https://code.claude.com/docs/en/monitoring-usage
- anthropics/claude-code #88094（Remote Control 默认开启，Open）：https://github.com/anthropics/claude-code/issues/88094
- anthropics/claude-code #88875（`remoteControlAutoEligible` 静默绑定，Open）：https://github.com/anthropics/claude-code/issues/88875
- anthropics/claude-code #76653（localhost 代理放行请求，closed as duplicate）：https://github.com/anthropics/claude-code/issues/76653
- anthropics/claude-code #29214 / #34255 / #77252：见 `docs/01`
- Happier README（协作会话、view-only 链接、Inbox、handoff、OIDC/mTLS）：https://github.com/happier-dev/happier
- Happy / happy-server（E2E、自建、仅个人多设备；happy-server 已于 2026-02-14 归档并并入主仓库）：https://github.com/slopus/happy ｜ https://github.com/slopus/happy-server
- Paseo（Apache-2.0，daemon + 可选 E2E relay + Docker）：https://github.com/getpaseo/paseo
- sshx（Argon2 + AES、密钥在 URL 片段、「Self-hosted deployments are not supported」）：https://github.com/ekzhang/sshx
- Zellij releases / web client（0.44 只读 token 与原生 Windows、0.45.x）：https://github.com/zellij-org/zellij/releases ｜ https://github.com/zellij-org/zellij-org.github.io/blob/main/docs/src/web-client.md
- RustDesk 验签失败降级路径、microsoft/terminal #11865、xterm.js 移动端 issue：见 `docs/research/05`
