# 04 · AI CLI 工具「远程 / 自动化控制」能力盘点

> 调研日期：2026-09-03 ｜ 对象：Claude Code、OpenAI Codex CLI、xAI Grok Build（含社区 grok-cli）、MiniMax（MiniMax Code 桌面端 / mmx-cli / M2–M3 系列模型）、Gemini CLI、Qwen Code、Kimi Code CLI、Aider、OpenCode、Factory Droid、Amp、Cline CLI
> 目的：确定 terminalX 的「AI 感知」功能（需要你确认、会话列表、成本面板、手机推送）可以建立在哪些**确定存在**的接口上，而不是靠猜屏幕文字。
> 说明：本调研环境中 developers.openai.com、geminicli.com、docs.x.ai、opencode.ai、docs.factory.ai、ampcode.com、docs.cline.bot、aider.chat 被代理拦截，改用各项目 GitHub 仓库内的文档 / 源码与二手资料交叉核实；无法核实的条目一律标「待核实」。所有 flag 名以核实到的原文为准。

---

## 0. 一页结论

1. **「headless + JSONL 事件流 + 会话 ID 恢复」已是主流 CLI 的标配。** 12 个目标里 10 个具备（Claude Code、Codex、Gemini、Qwen、Kimi、Grok Build、OpenCode、Droid、Amp、Cline）。Claude Code 的 `claude -p --output-format stream-json` 事实上成了行业格式：Amp 直接声明输出「Claude Code compatible stream JSON」[S27]，Qwen Code 沿用 `system / assistant / result` 事件结构 [S17]，Gemini、Droid 也是同一思路。Aider 只有 `--message` 一次性文本输出；MiniMax 没有自己的编码 CLI。
2. **「需要你确认」有三种可编程通道，且大多数工具至少有一种。** ① hooks（Claude Code `PermissionRequest`/`Notification(permission_prompt)`、Qwen `PermissionRequest`、Gemini `Notification(ToolPermission)`、Codex `PermissionRequest`、Kimi/Droid/Cline 的 `PreToolUse`）；② 进程内协议（Codex `app-server` JSON-RPC 的服务端发起审批请求、OpenCode `serve` 的 `permission.asked` SSE + `POST /session/:id/permissions/:id`、ACP 的权限请求）；③ Claude Code 独有的 `--permission-prompt-tool <MCP 工具>`——把权限决策交给一个 MCP 工具，terminalX 可以用它把审批直接转到手机 [S3]。只有 Aider、Amp、Grok Build（hook 事件名待核实）需要退回到 PTY 文本探测。
3. **ACP（Agent Client Protocol）是现成的统一适配层。** 官方注册表已包含 `claude-acp`、`codex-acp`、`gemini`、`qwen-code`、`kimi`、`opencode`、`cline`、`amp-acp`、`grok-build` [S33]——12 个目标中 9 个可以用同一个 JSON-RPC-over-stdio 协议驱动，拿到统一的会话 / 提示 / 工具调用 / 权限请求事件。Droid 走 ACP 的口径来自其 README（JetBrains/Zed 集成），注册表里未见同名条目，待核实。
4. **TUI 实现分四派，对远程流的影响截然不同。** Ink 主缓冲全量重绘（Claude Code）在 tmux / xterm.js 下闪烁、resize 时滚动区重复（issue #37076 / #51868 / #51828）[S8][S9]；ratatui / OpenTUI 全屏 alt-screen（Codex、Grok Build、OpenCode）在多路复用器下丢滚动历史，Codex 为此加了 `tui.alternate_screen` 与 `--no-alt-screen`（v0.81.0，2026-01）[S15]；Gemini / Qwen 是启用了 alt-screen 的 Ink；Aider 逐行输出最「远程友好」。结论：**结构化流为主、PTY 为兜底视图**。
5. **Windows 落地差异大。** Claude Code、Codex、Grok Build、Droid 提供原生 PowerShell 一行安装器；Kimi 强依赖 Git for Windows 的 Git Bash [S19]；Amp 旧文档要求 WSL（2026 状态待核实）[S28]；Codex 的 Windows 原生沙箱截至 2026-03 仍标 experimental [S14]。hooks 脚本跨平台是坑：Claude Code 与 Qwen Code 支持 `http` 类型 hook [S2][S18]，其他工具的 command hook 要靠 `curl`（Win10+ 自带）打本地端口——**terminalX 被控端应暴露本地 HTTP 端点，让所有 hook 变成一行 curl。**
6. **MiniMax 应建模为「模型供应商配置」而非 CLI。** MiniMax Code 是 macOS / Windows 桌面 App（v3.0.66，2026-08-19），内置终端和「App Remote Control」手机遥控，无 CLI / headless [S24][S25]；`mmx-cli` 是文本 / 图像 / 视频 / 语音生成工具，其 `mmx agent setup --agent claude-code|codex|grok-build|opencode|hermes|pi` 只是把 MiniMax 模型配置进别的 agent [S26]；M2.5 / M2.7 / M3 通过 Coding Plan 挂在 Claude Code、Codex、Droid、Cline 等上使用 [S23]。而 Claude Code 官方 Remote Control 在自定义 `ANTHROPIC_BASE_URL` 时直接不可用 [S6]——**MiniMax / 各类中转 API 用户是 terminalX 最明确的目标人群。**
7. **官方远程方案的边界已清楚。** Claude Code Remote Control：所有付费计划可用，但要求 claude.ai 登录（不支持 API Key / Bedrock / 网关）、本地进程必须存活（官方建议 tmux）、转录存 Anthropic 服务器、每个交互进程只能一条远程会话（server 模式默认 32 条）[S6]。Codex `app-server` 2026-03 起支持 WebSocket + bearer token 远程 TUI [S13]。OpenCode 自带 `serve` / `web` / `attach` [S30]。社区 grok-cli 甚至做了 Telegram 配对遥控 + 语音转写 [S22]。这些都是单工具、单厂商 relay；「多 CLI × 多机器 × 自建中转」仍是空位。

---

## 1. 逐工具能力清单

### 1.1 Claude Code（Anthropic）

- **安装 / Windows**：原生安装器 `irm https://claude.ai/install.ps1 | iex`、`winget install Anthropic.ClaudeCode`、npm（v2.1.198 起需 Node 22，实际下载的是原生二进制）。Windows 10 1809+ 原生支持；Git for Windows 可选——有则用 Git Bash 提供 Bash 工具，无则用 PowerShell 工具；沙箱功能仅 WSL2 支持 [S1]。
- **非交互 / 结构化输出**：`claude -p`，`--output-format text|json|stream-json`，`--input-format stream-json`（双向 JSONL），`--include-partial-messages`，`--json-schema`，`--bare`（跳过 hooks / MCP / CLAUDE.md 发现，推荐用于脚本）。`json` 结果含 `session_id`、`total_cost_usd`、按模型的用量；`stream-json` 首条 `system/init` 带 model、tools、mcp_servers、`capabilities` 数组，末条 `result` 带 cost 与 usage；还有 `system/api_retry`、`hook_started/hook_progress/hook_response`、子代理消息的 `parent_tool_use_id` [S3]。进程语义对托管很重要：SIGINT 结束当前 turn，SIGTERM 退出码 143 且该 turn 不记录结果，恢复时会继续未完成的 turn [S3]。
- **会话恢复**：`--continue`、`--resume <id|name>`、`--session-id <uuid>`、`--fork-session`、`--name`；v2.1.223 起可从任意目录按 ID 恢复 [S3][S4]。
- **权限机制**：模式 `default|acceptEdits|plan|auto|dontAsk|bypassPermissions`；`--allowedTools "Bash(git diff *)"` 规则语法；`--dangerously-skip-permissions`；**`--permission-prompt-tool`：非交互模式下由指定 MCP 工具处理权限提示** [S4]。
- **hooks / 通知**：30+ 事件，含 `PreToolUse`、`PostToolUse`、`PermissionRequest`（可返回 allow/deny 决策）、`PermissionDenied`、`Notification`（matcher：`permission_prompt`、`idle_prompt`、`agent_needs_input`、`agent_completed`、`elicitation_*` 等）、`Stop`、`StopFailure`、`SessionStart/End`、`SubagentStart/Stop`、`TaskCreated/Completed`、`PreCompact/PostCompact`、`PreModelSwitch/PostModelSwitch`。hook 类型：`command`、**`http`（POST JSON 到 URL）**、`mcp_tool`、`prompt`、`agent` [S2]。
- **状态行 / 成本**：`statusLine` 脚本从 stdin 收 JSON：`model.display_name`、`cost.total_cost_usd`、`cost.total_duration_ms`、`context_window.used_percentage`、`rate_limits.five_hour/seven_day.used_percentage`、`session_id`、`transcript_path` [S5]；`/cost` 命令。
- **多模型 / MCP**：`--model`、`--fallback-model`、`/model`；第三方模型经 `ANTHROPIC_BASE_URL`（代价是 Remote Control 失效）；`--mcp-config`、`--strict-mcp-config`。
- **TUI**：Ink，写主缓冲、不用 alt-screen、每次状态变化整块重绘；tmux / VS Code 终端 / Windows Terminal / xterm.js 均有闪烁或滚动区重复报告 [S8][S9]。
- **官方远程**：`claude remote-control`（server 模式，`--capacity` 默认 32、`--permission-mode`、`--name`、`--sandbox`）、`claude --remote-control`、`/remote-control`；权限提示与 `AskUserQuestion` 会转发到手机并保持打开；停止后约 4 小时内可 `--continue` / `--session-id` 找回；2026-08 起可从手机发起新会话 [S6][S7]。

### 1.2 OpenAI Codex CLI

- **安装 / Windows**：npm `@openai/codex`（Rust 二进制）。2026-03 起 Windows 原生 PowerShell 支持与 Windows 原生沙箱（elevated 用受限用户 + ACL + 防火墙，unelevated 用 restricted token），官方标 experimental；此前建议 WSL [S14]。
- **非交互 / 结构化输出**：`codex exec "..."`，`--json` 输出 JSONL。事件名（源码 `exec_events.rs`）：`thread.started`（含 thread_id）、`turn.started`、`turn.completed`（usage）、`turn.failed`、`item.started/updated/completed`、`error`；item 类型 `agent_message`、`reasoning`、`command_execution`、`file_change`、`mcp_tool_call`、`web_search`、`todo_list` [S12]。另有 `--output-last-message`、`--output-schema`、`--full-auto`、`--sandbox`、`-a/--ask-for-approval`、`--dangerously-bypass-approvals-and-sandbox`（来自二手资料 [S10][S11]，flag 组合待核实）。进度写 stderr，最终消息写 stdout [S11]。
- **会话恢复**：`codex resume --last | <SESSION_ID>`、`codex exec resume --last | <id>`；早期 `--json` 不吐会话 ID（issue #3817），现由 `thread.started.thread_id` 提供 [S10][S12]。
- **权限机制**：`approval_policy = untrusted|on-request|on-failure|never`，`sandbox_mode = read-only|workspace-write|danger-full-access`（二手资料，待核实原文）。exec 模式无法交互回答审批，只能靠沙箱兜底或全自动。
- **hooks / 通知**：① `notify = ["程序"]`（config.toml），turn 结束时以 JSON 调用外部程序，payload 至少含 `type: "agent-turn-complete"` 与 `last-assistant-message`；2026-04 起有 `approval-requested` 事件（待核实）；只通知、不能阻塞 agent [S16]。② 生命周期 hooks：`~/.codex/hooks.json` / `.codex/hooks.json`，v0.114（2026-03）首发，事件含 `SessionStart`、`UserPromptSubmit`、`PreToolUse`、`PostToolUse`、`PermissionRequest`、`PreCompact/PostCompact`、`SubagentStart/Stop`、`Stop`；0.150.1（2026-08-27）称有 12 个事件、非托管 hook 需先信任；`PreToolUse` 可 deny [S16b][S16c]。
- **app-server**：`codex app-server` 暴露 JSON-RPC 2.0（stdio；2026-03 起 WebSocket + bearer token + health endpoint），服务端可主动发起审批请求并暂停 turn，决策值 `accept | acceptForSession | decline | cancel | acceptWithExecpolicyAmendment`；VS Code / JetBrains / 桌面 App 都跑在它上面 [S13]。
- **状态 / 成本**：`/status`（5 小时 / 周限额）、`/usage`；`turn.completed.usage` 给 token；无美元估算 [S37]。
- **多模型 / MCP**：`/model`、`model_provider` 自定义供应商；MCP 支持。
- **TUI**：Rust ratatui，默认 alt-screen；`tui.alternate_screen = auto|always|never`，`--no-alt-screen`，检测到 Zellij 自动关闭 [S15]。

### 1.3 xAI Grok Build（官方）与 superagent-ai/grok-cli（社区）

- **Grok Build**：2026-05-14 发布，05-25 向全部 SuperGrok / X Premium+ 开放并发布 Windows PowerShell 安装器（`irm https://x.ai/cli/install.ps1 | iex`）；2026-07-15 以 Apache 2.0 开源（Rust，ratatui 全屏 TUI）[S20][S21]。headless：`grok -p "..."`，`--output-format plain|json|streaming-json`；会话 `-s/--session-id`、`-r/--resume <id>`、`-c/--continue`；`XAI_API_KEY` 用于无浏览器登录；`/plan`、`/yolo`、`/model`、`/inspect`；最多 8 个子代理跑在 git worktree；MCP、ACP、skills / plugins / hooks 扩展系统（hook 事件名待核实）；配置 `~/.grok/config.toml`（Windows `%USERPROFILE%\.grok\config.toml`）[S20][S21]。需要付费订阅，无免费档。
- **社区 grok-cli**：Bun + OpenTUI；`--prompt` headless；`--session latest`；MCP；**Telegram 遥控**：TUI 中 `/remote-control` → 机器人 `/pair` → 6 位配对码，之后可在 Telegram 私聊里驱动 agent，语音消息经 Grok STT 转写 [S22]。与 xAI 无关，但是一个现成的「配对码 + IM 遥控」交互范本。

### 1.4 MiniMax（MiniMax Code / mmx-cli / M2 系列）

- **MiniMax Code**：macOS / Windows 桌面 App，集成聊天、本地项目上下文、文件操作、终端会话、浏览器预览、skills、memory、自动化；「App Remote Control」允许手机直连桌面 Agent，文件 / 终端 / 运行环境留在电脑；GitHub 仓库仅为 issue tracker，无 CLI、无 headless [S24][S25]。**不可被 terminalX 以 CLI 方式托管。**
- **mmx-cli**（2026-04）：`npm i -g mmx-cli`，多模态生成工具（text/image/video/speech/music/search），`--output json` / `MINIMAX_OUTPUT=json`；`mmx agent setup --agent claude-code|codex|grok-build|opencode|hermes|pi` 一键把 MiniMax 模型配置进这些 agent [S26]。
- **M2.5 / M2.7 / M3**：MiniMax 官方口径是用于 Claude Code、Cursor、Cline、Kilo Code、Droid 等；Coding Plan 按 5 小时窗口配额计费 [S23]。对 terminalX 来说，「MiniMax Code CLI」= 某个宿主 CLI + MiniMax 端点；能力矩阵继承宿主。

### 1.5 Gemini CLI（Google）

- **安装 / Windows**：npm `@google/gemini-cli`（Node 20+）；Windows 通过 Node 原生运行（细节待核实）。
- **非交互**：`-p/--prompt`，`--output-format json|stream-json`（v0.11.0，2025-10）；stream-json 事件 `init`（含 session id）、`message`、`tool_use`、`tool_result`、`error`、`result`（stats）；退出码 0 / 1 / 42（非法输入）/ 53（超 turn 上限）[S31][S32]。headless JSON 起初不含 session id（issue #14435，PR #14504 已合并，字段位置待核实）。
- **会话恢复**：`--resume [latest|索引|UUID]`、`--list-sessions`、`--delete-session`、`/resume`；存储在 `~/.gemini/tmp/<project_hash>/chats/` [S31]。
- **权限**：`--yolo`、`--approval-mode`、`--allowed-tools`；v0.11 引入 message bus + policy engine 与 `ASK_USER` 决策 [S32]。
- **hooks**：`SessionStart/End`、`BeforeAgent/AfterAgent`、`BeforeModel/AfterModel`、`BeforeToolSelection`、`BeforeTool/AfterTool`、`Notification`（类型 `ToolPermission`）、`PreCompress`；stdin/stdout JSON，输出 `decision: allow|deny`、`systemMessage`、`continue` [S31b]。
- **成本**：`/stats` 只给 token 与配额，不算美元 [S37]。多模型：仅 Gemini；MCP 支持；ACP 是首个外部集成方 [S33]。TUI：Ink，但启用了 alt-screen（issue #51868 对比）[S9]。

### 1.6 Qwen Code（阿里 / 百炼；Gemini CLI 分支）

- **安装 / Windows**：`npm install -g @qwen-code/qwen-code@latest`（Node ≥20）；Qwen OAuth 已于 2026-04-15 停用，需百炼 API Key / Coding Plan [S34]。
- **非交互**：`-p`，`--output-format text|json|stream-json`，`--input-format stream-json`，`--include-partial-messages`，`--json-schema`；事件 `system(session_start)`、`assistant`、`result`、`goal_state`；结果含 `session_id`、`stats.models`（按模型 token）、`stats.tools` [S17]。
- **会话恢复**：`--continue`、`--resume <sessionId>`，headless 下可用 [S17]。
- **权限**：`--yolo/-y`，`--approval-mode plan|default|auto-edit|auto|yolo`，`--allowed-tools` [S17]。
- **hooks**：与 Claude Code 高度同构——`PreToolUse/PostToolUse/PostToolUseFailure`、`SessionStart/End/Delete`、`UserPromptSubmit`、`SubagentStart/Stop`、`PreCompact/PostCompact`、`MessageDisplay`、`Stop/StopFailure`、`PermissionRequest/PermissionDenied`、`Notification`（`permission_prompt`、`idle_prompt`、`auth_success`）、`TodoCreated/Completed`；类型 `command` 与 **`http`** [S18]。
- 多模型：OpenAI 兼容端点（待核实）；MCP、ACP 均有；TUI 为 Ink（继承 Gemini）。

### 1.7 Kimi Code CLI（Moonshot）

- **安装 / Windows**：官方脚本（下载二进制并校验）或 npm；**Windows 必须先装 Git for Windows，用 Git Bash 作 shell**，非标准路径设 `KIMI_SHELL_PATH`；数据在 `~/.kimi-code/`（`KIMI_CODE_HOME`）[S19]。近期版本改为 TypeScript / Node 实现（待核实；仓库仍含 Python 工程文件）。
- **非交互**：`kimi --print -p "..."`，`--output-format text|stream-json`，`--input-format stream-json`（stdin 逐条喂 user 消息直到关闭），`--final-message-only`，`--quiet`；JSON 采用 OpenAI chat 风格的 `role: user|assistant|tool` + `tool_calls`，**不是** Claude 风格；退出码 0 / 1（永久失败）/ 75（可重试的瞬时失败）。**print 模式隐含 `--afk`：自动批准所有工具调用、自动打发 `AskUserQuestion` 与计划模式切换** [S19b]。
- **会话恢复**：`--session/-S <id>`、`--continue/-C` [S19c]。
- **权限**：`--yolo/-y`、`default_yolo = true`；YOLO 只免审批仍会用 `AskUserQuestion` 问你，AFK 则连问题都自动处理 [S19d]。
- **hooks（Beta）**：13 个事件（`PreToolUse/PostToolUse/PostToolUseFailure`、`UserPromptSubmit`、`Stop/StopFailure`、`SessionStart/End`、`SubagentStart/Stop`、`PreCompact/PostCompact`、`Notification`），`~/.kimi/config.toml` 的 `[[hooks]]`，仅 shell 命令，退出码 0 允许 / 2 阻止 [S19e]。
- **其他**：`kimi acp`（多会话 ACP 服务器）、`kimi mcp add`、`--model`、`--thinking`、Ctrl-X 进 shell 模式、Zsh 插件 [S19c]。成本显示待核实。

### 1.8 Aider

- **安装 / Windows**：Python，`pip install aider-chat` 或 `aider-install`（uv 隔离）；Windows 原生 pip 可用，官方更推荐 WSL2；aider-install 最近发布 2026-02，主仓库 1–3 周一版，仍在维护 [S35]。
- **非交互**：`--message/-m`、`--message-file`、`--yes-always`（自动回答所有确认）、`--no-auto-commits`、`--dry-run`、`--commit`；Python API `Coder.create(...).run(...)` 官方声明不受支持 [S36]。**无 JSON 事件流、无会话 ID、无 hooks、无 MCP**。
- **确认点**：把文件加入对话、执行 `/run` 命令、git 操作、抓取 URL 等都会问 y/n，只能靠 `--yes-always` 或 PTY 探测。
- **多模型**：经 LiteLLM 接任意供应商（最强）。**TUI**：prompt_toolkit 逐行输出，无全屏重绘，是最适合远程日志流的一类。`--watch-files` 支持 IDE 内 `AI!` 注释触发。

### 1.9 OpenCode（anomalyco，原 sst/opencode）

- **安装 / Windows**：npm / curl / brew；Windows 原生运行，`OPENCODE_GIT_BASH_PATH` 指定 Git Bash [S29]。
- **非交互**：`opencode run "..."`，`--format json`（原始事件），`--continue/-c`、`--session/-s`、`--fork`、`--share`、`--agent`、`--model provider/model`、`--attach http://localhost:4096`、`--title`、`--dir`、`--file`；`--auto` 自动批准未显式拒绝的权限；文档称非交互模式下权限自动通过 [S29][S29b]。
- **服务端（最「远程原生」）**：`opencode serve --port 4096 --hostname 127.0.0.1 --cors --mdns`，`OPENCODE_SERVER_PASSWORD` 鉴权，`/doc` 给 OpenAPI 3.1；`GET /global/event` SSE；`POST /session`、`POST /session/:id/message`、`POST /session/:id/prompt_async`、**`POST /session/:id/permissions/:permissionID`** 回复审批；事件 `permission.asked`、`question.asked`、`session.idle`、`session.error`、`message.part.updated`；`opencode web` 自带 Web UI、`opencode attach` 让终端接到已运行后端；官方 SDK `@opencode-ai/sdk` [S30][S30b]。
- **插件 / hooks**：TS 插件放 `.opencode/plugin/` 或 `~/.config/opencode/plugin/`，钩子 `tool.execute.before/after`、`event`（`session.idle`、`permission.asked` 等 25+ 事件）；`permission.ask` 插件钩子曾不触发（issue #9229）[S30c]。
- **其他**：`opencode acp`；75+ 模型供应商；MCP；TUI 基于 OpenTUI（Solid + Zig 渲染器，Bun 运行时，全屏）[S38]。

### 1.10 Factory Droid

- **安装 / Windows**：`curl -fsSL https://app.factory.ai/cli | sh`，**Windows `irm https://app.factory.ai/cli/windows | iex`**，npm `droid`；0.176.0（2026-07-20）加 Windows ARM64 原生构建 [S39][S39b]。
- **非交互**：`droid exec "..."`，`--auto low|medium|high`（默认偏只读；low 仅安全编辑，medium 常规开发，high 含部署类操作），`--skip-permissions-unsafe`，`-o/--output-format text|json|stream-json|stream-jsonrpc`，`-s/--session-id` 恢复（flag 待核实），`-m/--model` [S40]。
- **hooks**：Claude 风格（`PreToolUse/PostToolUse/Notification/Stop/SessionStart` 等，完整列表待核实），可与 autonomy level 叠加；脚本用 `$FACTORY_PROJECT_DIR` 绝对路径 [S40b]。
- **多模型**：BYOK 多厂商（Anthropic / OpenAI / Google / MiniMax 等）；MCP；ACP（JetBrains / Zed）[S39]。成本 / TUI 实现待核实。

### 1.11 Amp（Sourcegraph，2025-12 分拆）

- **安装 / Windows**：npm 包已迁到 `@ampcode/cli`（原 `@sourcegraph/amp`）；旧文档要求 Windows 走 WSL（2026 状态待核实）[S28]。
- **非交互**：`amp -x/--execute "..."`，`--stream-json`（Claude Code 兼容 JSONL：`system/init`、`assistant`、`user`、`result`），`--stream-json-input`（stdin JSONL），`--dangerously-allow-all`；`amp.permissions` 规则 allow/ask/reject；`amp threads new|continue <id>|fork|list|share|compact`；只有 smart 模式支持 `--stream-json` [S27][S27b][S28b]。
- **通知**：`amp.notifications.enabled`、`amp.notifications.system.enabled`（桌面通知）；未见 hooks 文档（待核实）。MCP `amp.mcpServers`；ACP `amp-acp`。模型由 Amp 托管、不支持 BYOK（待核实）。成本在 ampcode.com 线程页显示（待核实）。

### 1.12 Cline CLI

- **安装 / Windows**：`npm i -g cline`，自动拉取平台二进制（macOS / Linux / **Windows x64 & arm64**）[S41]。
- **非交互**：`--json`（NDJSON）、stdin 管道、输出重定向三者任一触发 headless；**默认工具自动批准**，`--auto-approve false` 才要人工审核；`-y/--yolo` 跳过审批并禁用 spawn / team 工具；`-P` provider、`-m` model、`-k` key；`cline mcp` [S41]。
- **会话恢复**：`cline --id <session_id> "..."`；`cline history`；3.0.7 版 `--json` + `--id` 组合报错（issue #10856，PR #11630）[S42]。
- **hooks**：`TaskStart`、`TaskResume`、`UserPromptSubmit`、`PreToolUse`、`PostToolUse`、`PreCompact`、`TaskComplete`、`TaskCancel`（CLI ≥3.0.47），脚本放 `~/Documents/Cline/Rules/Hooks/` 或 `.clinerules/hooks/`，文件名即事件名，stdin JSON [S43]。
- 官网另有「Kanban board」多任务面板；ACP 注册表有 `cline`。

---

## 2. 远程控制能力矩阵

### 2.1 运行与集成面

| 工具 | 形态 / 语言 | Windows | 无头模式 & 结构化输出 | 会话恢复 | TUI 实现（远程流影响） |
|---|---|---|---|---|---|
| Claude Code | 原生二进制（Node 打包） | 原生安装器 / winget / npm；Git Bash 可选 | `-p --output-format json\|stream-json`，`--input-format stream-json` 双向，`--json-schema`，`--bare` | `--continue` / `--resume <id\|name>` / `--session-id` / `--fork-session` | Ink，主缓冲全量重绘（闪烁、滚动区重复） |
| Codex CLI | Rust | 原生 PowerShell + Windows 沙箱（experimental） | `codex exec --json`（thread/turn/item 事件），`--output-schema`；`app-server` JSON-RPC/WebSocket | `codex resume --last\|<id>`，`codex exec resume` | ratatui，alt-screen（可 `--no-alt-screen`） |
| Grok Build | Rust（已开源） | 原生 PowerShell 安装器 | `grok -p --output-format plain\|json\|streaming-json` | `-s` / `-r <id>` / `-c` | ratatui 全屏 |
| MiniMax Code | 桌面 App | macOS / Windows | 无 | 无 | 非终端应用 |
| Gemini CLI | Node / Ink | npm | `-p --output-format json\|stream-json`（init/message/tool_use/tool_result/result） | `--resume [latest\|idx\|id]`、`--list-sessions` | Ink + alt-screen |
| Qwen Code | Node / Ink（Gemini 分支） | npm | 同 Claude 风格 stream-json，`--input-format stream-json`，`--json-schema` | `--continue` / `--resume <id>` | Ink + alt-screen |
| Kimi Code CLI | 二进制（TS/Node，待核实） | 脚本 / npm；**必须 Git Bash** | `--print --output-format stream-json`（OpenAI chat 风格），`--input-format stream-json` | `--session/-S <id>` / `--continue/-C` | 自研交互 shell（待核实） |
| Aider | Python | pip 原生可用，推荐 WSL2 | `--message` + `--yes-always`，纯文本 | 无会话 ID（`--restore-chat-history` 待核实） | prompt_toolkit 逐行输出（最友好） |
| OpenCode | TS / Bun | 原生（Git Bash 路径可配） | `opencode run --format json`；`serve` REST+SSE；`web`；`attach` | `-c` / `-s <id>` / `--fork`；REST `/session` | OpenTUI（Zig 渲染，全屏） |
| Factory Droid | 二进制 | 原生 PowerShell 安装器，ARM64 | `droid exec -o json\|stream-json\|stream-jsonrpc` | `-s <session-id>`（待核实） | 待核实 |
| Amp | TS | 旧文档要求 WSL（待核实） | `amp -x --stream-json`（Claude 兼容），`--stream-json-input` | `amp threads continue <id>` | 待核实 |
| Cline CLI | TS + 平台二进制 | 原生 x64 / arm64 | `--json` NDJSON；stdin 管道即 headless | `--id <session_id>`（与 `--json` 组合有 bug） | 待核实 |

### 2.2 感知与控制面

| 工具 | 权限 / 审批机制 | hooks / 通知通道 | 成本 / 状态 | 多模型 | MCP | ACP | 官方远程 |
|---|---|---|---|---|---|---|---|
| Claude Code | 6 种 permission-mode；`--allowedTools` 规则；**`--permission-prompt-tool` MCP 接管审批** | 30+ 事件；`PermissionRequest` 可 allow/deny；`Notification(permission_prompt/idle_prompt/agent_needs_input)`；**http hook** | statusLine JSON（USD、上下文 %、5h/7d 限额）；`result.total_cost_usd`；`/cost` | `--model` / `--fallback-model`；`ANTHROPIC_BASE_URL`（Remote Control 失效） | 是 | claude-acp | Remote Control（全计划、需 claude.ai 登录、进程需存活） |
| Codex CLI | `approval_policy` 4 档 × `sandbox_mode` 3 档；exec 不能交互审批；app-server 可回复审批 | `notify`（turn 完成 / 审批请求，不可阻塞）；hooks.json 12 事件（`PreToolUse` 可 deny） | `/status`（5h/周限额）、`/usage`；`turn.completed.usage` | `/model`、自定义 `model_provider` | 是 | codex-acp | app-server WebSocket 远程 TUI（2026-03） |
| Grok Build | Plan Mode、`/yolo`；审批细节待核实 | hooks 扩展系统（事件名待核实） | 待核实 | Grok 系列；per-model `base_url` 可配 | 是 | grok-build | 无（社区 grok-cli 有 Telegram 遥控） |
| MiniMax | — | — | — | 作为供应商挂在其他 CLI 上 | — | — | 桌面 App 自带手机遥控 |
| Gemini CLI | `--yolo` / `--approval-mode`；policy engine `ASK_USER` | `Notification(ToolPermission)`、`BeforeTool/AfterTool`、`AfterAgent`；command hook | `/stats` 仅 token | 仅 Gemini | 是 | gemini | 无 |
| Qwen Code | `--approval-mode` 5 档 / `--yolo` | Claude 同构：`PermissionRequest`、`Notification(permission_prompt/idle_prompt)`、`Stop`；**http hook** | `result.stats.models` token | OpenAI 兼容（待核实） | 是 | qwen-code | 无 |
| Kimi Code CLI | `--yolo`；print 隐含 AFK 全自动 | 13 事件 Beta（`PreToolUse`、`Notification`、`Stop`），TOML 配置，仅 shell | 待核实 | `--model` | 是 | kimi（`kimi acp`） | 无 |
| Aider | y/n 文本提示；`--yes-always` | 无 | 每轮输出中打印费用（文本） | LiteLLM 任意 | 否 | 否 | 无 |
| OpenCode | 权限规则引擎；非交互自动通过；`permission.asked` SSE + REST 回复 | 插件 `tool.execute.before/after`、`event(session.idle…)` | 会话含 cost（待核实） | 75+ 供应商 | 是 | opencode | `serve` / `web` / `attach`（自建） |
| Factory Droid | `--auto low\|medium\|high`；`--skip-permissions-unsafe` | Claude 风格 hooks（列表待核实） | 待核实 | BYOK 多厂商 | 是 | 是（README） | 无 |
| Amp | `amp.permissions` allow/ask/reject；`--dangerously-allow-all` | 桌面通知开关；hooks 待核实 | 线程页（待核实） | Amp 托管 | 是 | amp-acp | 无 |
| Cline CLI | 默认自动批准；`--auto-approve false`；`--yolo` | 8 事件脚本 hooks（`PreToolUse`、`TaskComplete`） | history 含 token | `-P/-m` 任意 | 是 | cline | 无 |

---

## 3. TUI 渲染四派对「Web 远程终端」的影响

| 派别 | 工具 | 行为 | 通过 PTY→WebSocket→xterm.js 远程时的症状 | 应对 |
|---|---|---|---|---|
| Ink 主缓冲重绘 | Claude Code | 每个 token / 工具状态变化触发整块 `cursor-up + erase-line` 重绘，不进 alt-screen，缺 DECSET 2026 同步输出 | tmux / xterm.js 闪烁；视口比动态区矮时旧内容被推进滚动区形成重复；手机窄屏 resize 触发重排风暴 [S8][S9] | 固定 PTY 列数、客户端横向滚动；xterm.js 开启 `synchronizedOutput`（若可用）；AI 面板走 stream-json 而不是读屏 |
| Ink + alt-screen | Gemini CLI、Qwen Code | 进入备用屏，重绘限定在视口 | 多路复用器内丢滚动历史；与主缓冲切换时闪一下 | 用 `--resume` / stream-json 取历史，不依赖终端 scrollback |
| ratatui / OpenTUI 全屏 | Codex、Grok Build、OpenCode | 双缓冲 diff、alt-screen、鼠标捕获 | 同上；鼠标事件被捕获导致手机滑动无法滚回 | Codex 用 `--no-alt-screen`；OpenCode 直接用 `serve` API |
| 逐行输出 | Aider | 无全屏重绘 | 无特殊问题 | 直接当日志流 |

**设计结论**：Web 终端的 PTY 视图应被定位为「兜底 / 调试视图」；「AI 感知」功能一律从结构化通道取数据。手机端默认打开的应是结构化会话视图（消息 / 工具调用 / 审批卡片），而不是 xterm。

---

## 4. terminalX 可基于这些能力做的通用抽象

### 4.1 三层接入模型（每个 CLI 按能力落到最高可用层）

| 层 | 机制 | 适用工具 | 得到什么 |
|---|---|---|---|
| L0 PTY 原始层 | ConPTY / pty 启动进程，转发字节流 | 全部 | 能看、能敲；只能靠正则猜「(y/n)」 |
| L1 旁路信号层 | 工具的 hooks / notify 通过 `curl` 或 http hook 打到被控端本地 HTTP 端点（如 `127.0.0.1:port/hook`） | Claude Code、Qwen（http hook 免脚本）；Codex（notify + hooks.json）；Gemini、Kimi、Droid、Cline（command hook）；OpenCode（TS 插件） | 「需要确认」「已完成」「空闲」「会话开始 / 结束」事件，部分可直接 allow/deny |
| L2 结构化驱动层 | 由 terminalX 以程序方式驱动 CLI，替代人类终端 | Claude `-p --input-format stream-json --output-format stream-json` + `--permission-prompt-tool`；Codex `app-server`（JSON-RPC / WebSocket）；OpenCode `serve`（REST + SSE）；其余走 ACP（Gemini、Qwen、Kimi、Cline、Amp、Grok Build、Codex、Claude 都有 ACP 端） | 完整消息 / 工具调用 / usage / 权限请求，可在手机上回复审批 |

第一阶段（Web 页面控 Windows 终端）建议：L0 全量 + L1 覆盖 Claude Code / Codex / Qwen；L2 先做 Claude Code 与 OpenCode 两条（接口最稳定、文档最全）。

### 4.2 统一事件模型（terminalX Event Schema）

建议内部事件：`session.started`、`turn.started`、`assistant.delta`、`tool.started`、`tool.completed`、`permission.requested`、`question.asked`、`turn.completed(usage)`、`session.idle`、`session.ended`、`error`。映射示例：

| terminalX 事件 | Claude Code | Codex | Gemini / Qwen | OpenCode | Kimi | Cline |
|---|---|---|---|---|---|---|
| `session.started` | `system/init`；hook `SessionStart` | `thread.started`；hook `SessionStart` | `init`；hook `SessionStart` | `session.created` | hook `SessionStart` | hook `TaskStart` |
| `permission.requested` | hook `PermissionRequest` / `Notification(permission_prompt)` / `--permission-prompt-tool` | notify `approval-requested`；hook `PermissionRequest`；app-server 审批请求 | Gemini `Notification(ToolPermission)`；Qwen `PermissionRequest` | SSE `permission.asked` | hook `PreToolUse`（仅能 allow/deny，非提问） | hook `PreToolUse` |
| `turn.completed` | `result`（含 usage / cost）；hook `Stop` | `turn.completed(usage)`；notify `agent-turn-complete`；hook `Stop` | `result(stats)`；hook `AfterAgent` | `session.idle` | hook `Stop` | hook `TaskComplete` |
| `session.idle`（等你输入） | `Notification(idle_prompt)` | notify `agent-turn-complete` 后无新 turn | Qwen `Notification(idle_prompt)` | `session.idle` | PTY 静默计时 | PTY 静默计时 |

### 4.3 统一的「需要你确认」事件

- **来源分级**：A 级（可编程回复）——Claude `PermissionRequest` hook 决策 / `--permission-prompt-tool`、Codex app-server、OpenCode REST 回复、ACP 权限请求；B 级（只能通知，回复要注入按键）——Codex notify、Kimi / Gemini / Cline 的 command hook；C 级（文本探测）——Aider、Amp 交互模式、Grok Build。
- **手机侧交互**：审批卡片显示工具名 + 参数摘要（hook 输入里的 `tool_name` / `tool_input.command`）+ 「允许 / 本会话允许 / 拒绝 / 打开终端」；A 级直接回写决策，B/C 级把 `y` / `n` / `Enter` 注入 PTY 并回读屏幕确认。
- **防误批**：给出工具与工作目录、diff 预览（Claude Remote Control 已这么做 [S6]）；对 `rm -rf`、`git push --force` 等做本地规则拦截（Claude / Qwen / Codex 的 `PreToolUse` deny）。

### 4.4 统一会话列表

- 被控端扫描本地会话存储：Claude `~/.claude/projects/*.jsonl`（statusLine 的 `transcript_path`）、Codex `~/.codex/sessions`、Gemini `~/.gemini/tmp/<hash>/chats/`、Kimi `~/.kimi-code/`、OpenCode `session list` / REST、Cline `cline history`；映射到各自的恢复命令（`claude --resume <id>`、`codex resume <id>`、`gemini --resume <id>`、`qwen --resume <id>`、`kimi -S <id>`、`opencode run -s <id>`、`cline --id <id>`）。
- 每条会话卡片：工具、机器、目录、最近一条 assistant 消息、状态（运行中 / 等待确认 / 空闲 / 已结束）、本会话 token / 费用。
- 「一键续聊」= 在同一台机器、同一目录起 PTY 并执行恢复命令；对 Claude 还可传 `--name` 让本地 `/resume` 列表与 terminalX 名称一致。

### 4.5 成本 / token 面板

- 数据源：Claude statusLine JSON（`cost.total_cost_usd`、`context_window.used_percentage`、`rate_limits.five_hour/seven_day`）与 `result.usage`；Codex `turn.completed.usage`（无美元）；Gemini `result.stats`；Qwen `stats.models`；Cline history token；Aider 输出文本中的费用行；OpenCode 会话 cost。
- 归一化：`input / output / cached tokens` + 按价目表估算 USD（标注「估算」，Claude 官方也声明为客户端估算 [S3]）；额外展示订阅限额窗口（Claude 5h/7d、Codex 5h/周），这是中转 API 用户最关心的「今天还能用多少」。

### 4.6 用 hooks 主动推送手机通知

- 被控端 Agent 提供 `POST http://127.0.0.1:<port>/hook/<tool>`，安装向导为每个 CLI 写入最小配置：Claude / Qwen 用 `http` hook（零脚本、天然跨 Windows）；Codex 写 `notify = ["curl", "-s", "-X", "POST", ...]` 与 `hooks.json`；Gemini / Kimi / Cline / Droid 写一行 `curl` 的 command hook。
- 推送策略：`permission_prompt` 即时推；`idle_prompt` / `agent-turn-complete` 合并去抖（社区实践里 ntfy / Bark / Pushover 都是这么做的 [S44]）；对没有 hooks 的工具用 PTY 静默 + 光标停在提示符的启发式判「在等你」。
- 这一层与 Claude 官方 Remote Control 的「Push when actions required」是同构的 [S6]，但 terminalX 不依赖 claude.ai 登录、支持自定义 base URL 与多工具。

### 4.7 Windows 被控端落地清单

1. 优先用各家原生安装器（Claude `install.ps1`、Codex npm、Grok `install.ps1`、Droid `cli/windows`、Cline npm），并检测 Git for Windows（Kimi 必需、Claude / OpenCode 推荐）。
2. hooks 一律走 HTTP：避免 `.sh` 在 PowerShell 下不可执行；Claude / Qwen 用 `http` 类型，其余用 `curl.exe`。
3. 进程托管：用 ConPTY 启动，退出时先 SIGINT（结束 turn）再 SIGTERM（Claude 退出码 143，恢复时续跑）[S3]；Windows 无 tmux，terminalX 的常驻 Agent 本身承担「保活」角色（对应 02 号调研的结论）。
4. 已知坑：Claude v2.1.211 前 Windows 上 stdin 不可读会崩溃 [S3]；Codex Windows 沙箱 experimental；Amp 可能只支持 WSL。

### 4.8 第一阶段优先级建议

| 优先级 | 工具 | 理由 |
|---|---|---|
| P0 | Claude Code | 接口最全（stream-json 双向、http hook、permission-prompt-tool、statusLine）；中转 API 用户无法用官方 Remote Control |
| P0 | Codex CLI | 用户量大；`notify` + `hooks.json` + `app-server` 三条路 |
| P1 | OpenCode、Qwen Code | 前者自带 REST/SSE，后者与 Claude 同构、国内可用 |
| P1 | MiniMax（作为供应商预设） | 一键把 Claude Code / Codex / OpenCode 切到 MiniMax 端点 |
| P2 | Gemini、Kimi、Droid、Cline、Grok Build | 走 ACP 统一适配 |
| P3 | Aider、Amp | 仅 PTY + 文本探测 |

---

## 5. 来源

- [S1] Claude Code · Advanced setup（Windows 原生安装 / Git Bash / WSL 表）：https://code.claude.com/docs/en/setup
- [S2] Claude Code · Hooks reference（事件、Notification matcher、hook 类型）：https://code.claude.com/docs/en/hooks
- [S3] Claude Code · Run Claude Code programmatically（`-p`、stream-json、`--bare`、SIGTERM、`--resume`）：https://code.claude.com/docs/en/headless
- [S4] Claude Code · CLI reference（`--permission-prompt-tool`、`--permission-mode`、`--fork-session`、`--bg`、`--teleport`）：https://code.claude.com/docs/en/cli-reference
- [S5] Claude Code · Customize your status line（JSON 字段）：https://code.claude.com/docs/en/statusline
- [S6] Claude Code · Remote Control（计划、限制、推送、会话找回）：https://code.claude.com/docs/en/remote-control
- [S7] VentureBeat · Anthropic just released a mobile version of Claude Code called Remote Control（2026-02）：https://venturebeat.com/orchestration/anthropic-just-released-a-mobile-version-of-claude-code-called-remote
- [S8] anthropics/claude-code issue #37076 · Terminal rendering causes severe flickering in tmux：https://github.com/anthropics/claude-code/issues/37076
- [S9] anthropics/claude-code issue #51868 · Thinking spinner causes flicker in tmux（主缓冲 vs alt-screen，对比 Gemini CLI）：https://github.com/anthropics/claude-code/issues/51868 ；issue #51828（scrollback duplication）：https://github.com/anthropics/claude-code/issues/51828
- [S10] openai/codex issue #3817 · No way to resume in non-interactive mode when session id is not outputted：https://github.com/openai/codex/issues/3817
- [S11] 博客园 · Codex CLI 完全使用手册（exec 的 stderr/stdout 分工、resume）：https://www.cnblogs.com/knqiufan/p/20094616 ；菜鸟教程 · Codex 非交互模式：https://www.runoob.com/codex/codex-noninteractive.html
- [S12] openai/codex 源码 `codex-rs/exec/src/exec_events.rs`（`thread.started` 等事件与 item 类型）：https://github.com/openai/codex/blob/main/codex-rs/exec/src/exec_events.rs
- [S13] OpenAI · Unlocking the Codex harness: how we built the App Server：https://openai.com/index/unlocking-the-codex-harness/ ；Codex App Server 文档：https://developers.openai.com/codex/app-server ；Codex Knowledge Base · App Server remote WebSocket：https://codex.danielvaughan.com/2026/03/31/codex-cli-app-server-remote-websocket/
- [S14] Codex Knowledge Base · Codex CLI on Windows: Native Sandbox, WSL Integration（2026-04-01）：https://codex.danielvaughan.com/2026/04/01/codex-cli-windows-native-sandbox-wsl/ ；Windows Forum · OpenAI Codex Arrives on Windows with Native Sandbox：https://windowsforum.com/threads/openai-codex-arrives-on-windows-with-native-sandbox-and-agentic-workflows.404026/
- [S15] openai/codex PR #8555 · tui.alternate_screen 与 --no-alt-screen（2026-01-09 合并）：https://github.com/openai/codex/pull/8555
- [S16] backgrind · Codex CLI notifications: how the notify hook actually works：https://backgrind.com/blog/codex-cli-notifications/ ；Stovoy/codex-notify-chime（notify 配置示例）：https://github.com/Stovoy/codex-notify-chime
- [S16b] Codex Hooks 官方文档：https://developers.openai.com/codex/hooks ；shanraisshan/codex-cli-best-practice · codex-hooks.md：https://github.com/shanraisshan/codex-cli-best-practice/blob/main/best-practice/codex-hooks.md
- [S16c] HookStack · OpenAI Codex Hooks: Setup, Config, and Examples（0.150.1 十二事件）：https://www.hookstack.app/guides/openai-codex-hooks ；openai/codex issue #14882：https://github.com/openai/codex/issues/14882
- [S17] Qwen Code · Headless Mode：https://github.com/QwenLM/qwen-code/blob/main/docs/users/features/headless.md （站点版：https://qwenlm.github.io/qwen-code-docs/en/users/features/headless/ ）
- [S18] Qwen Code · Hooks：https://github.com/QwenLM/qwen-code/blob/main/docs/users/features/hooks.md
- [S19] Kimi Code CLI 安装与快速入门（Windows 需 Git Bash、KIMI_SHELL_PATH）：https://www.kimi.com/zh-cn/help/kimi-code/cli-getting-started ；GitHub：https://github.com/MoonshotAI/kimi-cli
- [S19b] Kimi Code CLI · Print 模式：https://github.com/MoonshotAI/kimi-cli/blob/main/docs/en/customization/print-mode.md
- [S19c] Kimi Code CLI · kimi 命令参考：https://github.com/MoonshotAI/kimi-cli/blob/main/docs/en/reference/kimi-command.md
- [S19d] Kimi Code CLI · Interaction and Input（YOLO / AFK）：https://moonshotai.github.io/kimi-cli/en/guides/interaction.html
- [S19e] Kimi Code CLI · Hooks (Beta)：https://github.com/MoonshotAI/kimi-cli/blob/main/docs/en/customization/hooks.md
- [S20] xAI · Introducing Grok Build：https://x.ai/news/grok-build-cli ；Grok Build 文档：https://docs.x.ai/build/overview
- [S21] dadamingmax/Grok-Build-Guide-2026（中文，headless / streaming-json / 命令表）：https://github.com/dadamingmax/Grok-Build-Guide-2026 ；MarkTechPost · SpaceXAI Open-Sources Grok Build（Rust / ratatui，2026-07-15）：https://www.marktechpost.com/2026/07/15/spacexai-open-sources-grok-build-the-rust-agent-harness-tui-and-tool-layer-behind-its-coding-cli/ ；Grok Build CLI Cheatsheet（-s/-r/-c、--output-format）：https://www.scriptbyai.com/grok-build-cheat-sheet/
- [S22] superagent-ai/grok-cli（社区，Telegram 遥控）：https://github.com/superagent-ai/grok-cli
- [S23] MiniMax · M2.5 模型页 / Coding Plan：https://www.minimax.io/models/text ；MiniMax API Docs · Other Tools：https://platform.minimax.io/docs/token-plan/other-tools
- [S24] MiniMax-AI/minimax-code（桌面 App issue tracker）：https://github.com/MiniMax-AI/minimax-code ；MiniMax Agent Changelog：https://agent.minimax.io/docs/changelog
- [S25] minimax-ai.chat · MiniMax Code: Free Download, Token Plans and BYOK Setup（桌面 App、App Remote Control）：https://minimax-ai.chat/guide/minimax-code/
- [S26] MiniMax-AI/cli（mmx-cli，`mmx agent setup`）：https://github.com/MiniMax-AI/cli ；MarkTechPost · MiniMax Releases MMX-CLI：https://www.marktechpost.com/2026/04/12/minimax-releases-mmx-cli-a-command-line-interface-that-gives-ai-agents-native-access-to-image-video-speech-music-vision-and-search/
- [S27] Amp · Streaming JSON：https://ampcode.com/news/streaming-json
- [S27b] jdorfman/awesome-amp-code · amp_cli_docs.md（-x、--stream-json-input、amp.permissions）：https://github.com/jdorfman/awesome-amp-code/blob/main/docs/amp_cli_docs.md
- [S28] npm · @sourcegraph/amp（迁至 @ampcode/cli；Windows 需 WSL）：https://www.npmjs.com/package/@sourcegraph/amp
- [S28b] sourcegraph/amp-examples-and-guides · CLI guide：https://github.com/sourcegraph/amp-examples-and-guides/blob/main/guides/cli/README.md
- [S29] OpenCode · CLI 文档（run / serve / web / acp / attach）：https://github.com/anomalyco/opencode/blob/dev/packages/web/src/content/docs/cli.mdx （站点版：https://opencode.ai/docs/cli/ ）
- [S29b] OpenCode CLI Guide（非交互权限自动通过）：https://open-code.ai/en/docs/cli
- [S30] OpenCode · Server 文档（端点、SSE、鉴权）：https://github.com/anomalyco/opencode/blob/dev/packages/web/src/content/docs/server.mdx （站点版：https://opencode.ai/docs/server/ ）
- [S30b] anomalyco/opencode issue #11616 · Web Interface Client Interaction Architecture（permission.asked / question.asked）：https://github.com/anomalyco/opencode/issues/11616
- [S30c] OpenCode Plugins Guide（gist）：https://gist.github.com/CypherpunkSamurai/30dc0b7683c06560a74f783097c5f912 ；issue #9229 permission.ask 未触发：https://github.com/anomalyco/opencode/issues/9229
- [S31] Gemini CLI · headless.md：https://github.com/google-gemini/gemini-cli/blob/main/docs/cli/headless.md ；session-management.md：https://github.com/google-gemini/gemini-cli/blob/main/docs/cli/session-management.md
- [S31b] Gemini CLI · hooks/reference.md：https://github.com/google-gemini/gemini-cli/blob/main/docs/hooks/reference.md
- [S32] google-gemini/gemini-cli PR #10883 · stream-json for headless：https://github.com/google-gemini/gemini-cli/pull/10883 ；issue #14435 · headless JSON 需含 session ID：https://github.com/google-gemini/gemini-cli/issues/14435
- [S33] Agent Client Protocol 注册表：https://github.com/agentclientprotocol/registry ；ACP 介绍：https://agentclientprotocol.com/get-started/introduction
- [S34] 阿里云百炼 · 安装与配置 Qwen Code：https://help.aliyun.com/zh/model-studio/qwen-code
- [S35] PyPI · aider-install：https://pypi.org/project/aider-install/ ；aider 安装文档：https://aider.chat/docs/install.html
- [S36] Aider · Scripting（--message / --yes / Python API）：https://github.com/Aider-AI/aider/blob/main/aider/website/docs/scripting.md
- [S37] viberank · How to Check Codex Usage: /usage, /status：https://www.viberank.app/blog/how-to-check-codex-usage ；whoburnedmore · Check Gemini CLI usage（/stats）：https://whoburnedmore.com/guides/check-gemini-cli-usage
- [S38] anomalyco/opentui：https://github.com/anomalyco/opentui ；DeepWiki · OpenCode TUI：https://deepwiki.com/sst/opencode/6.2-terminal-user-interface-(tui)
- [S39] Factory-AI/factory README（安装命令含 Windows）：https://github.com/Factory-AI/factory
- [S39b] New API docs · Factory Droid CLI（Windows 安装、0.176.0 ARM64）：https://docs.newapi.ai/en/docs/apps/factory-droid-cli
- [S40] Factory · Droid Exec (Headless)：https://docs.factory.ai/droid-exec/overview ；CLI Reference：https://docs.factory.ai/reference/cli-reference
- [S40b] Factory · Hooks guide：https://docs.factory.ai/cli/configuration/hooks-guide
- [S41] cline/cline · apps/cli/README.md：https://github.com/cline/cline/blob/main/apps/cli/README.md ；Cline CLI Overview：https://docs.cline.bot/usage/cli-overview
- [S42] cline/cline issue #10856 · --json 模式无法用 --id 恢复：https://github.com/cline/cline/issues/10856
- [S43] Cline v3.36 Hooks 公告：https://cline.bot/blog/cline-v3-36-hooks ；Hooks 文档：https://docs.cline.bot/features/hooks
- [S44] Zenn · Claude Code の /hooks で承認依頼とタスク完了時にスマホへ通知（ntfy / Pushover / Bark 实践）：https://zenn.dev/keit0728/articles/bfb68f669755a7 ；RonitSachdev/ccnudge：https://github.com/RonitSachdev/ccnudge
- [S45] 知乎 · 实测｜春节我用三种姿势在手机上用 Claude Code：https://zhuanlan.zhihu.com/p/2014298246411990758 ；V2EX · 多账号并发 AI 编程工作台：https://global.v2ex.co/t/1197148 ；AgentPort（手机远程操控 Claude Code / Codex）：https://www.80aj.com/2026/07/24/agentport-claude-code-remote/ ；Claude Squad 实战指南：https://blog.rainlib.com/blog/claude-squad-multi-agent-workflow
