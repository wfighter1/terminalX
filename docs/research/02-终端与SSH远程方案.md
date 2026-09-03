# 02 · 终端级远程方案调研：SSH / 多路复用 / 穿透 / Web 终端 / AI CLI 原生远程

> 调研日期：2026-09-03 ｜ 面向：terminalX 第一阶段（Web 页面控制 Windows 电脑上的终端 + AI CLI）
> 结论先行：**现有终端级远程方案没有一个同时满足「Windows 被控端开箱即用 + 会话保活 + 免穿透 + 移动端友好 + Agent 感知通知 + 多机多会话统一面板」六项**。Windows 上的短板尤其明显：没有原生 tmux、mosh/Eternal Terminal/Tailscale SSH 都没有 Windows 服务端、sshd 断开后进程生命周期不可控、ConPTY 兼容问题多。AI 厂商的原生远程（Claude Code Remote Control、Codex 手机遥控）体验最好，但绑定单一工具、依赖官方 relay，并且在使用第三方 API 中转（`ANTHROPIC_BASE_URL`）时直接不可用。这正是 terminalX 的切入空间。

---

## 1. 方案全景：按「层」拆解

远程操作一个终端，实际上要解决五层问题。绝大多数工具只做其中一层，用户需要自己「拼装」：

| 层 | 要解决的问题 | 典型工具 |
|---|---|---|
| 网络层（可达） | NAT/防火墙穿透、公司内网 | Tailscale / Headscale、Cloudflare Tunnel、ngrok、frp |
| 传输层（连接） | 加密通道、断线容忍 | OpenSSH、mosh、Eternal Terminal |
| 会话层（保活） | 断线后进程不死、可重连、多窗格 | tmux、Zellij、psmux、WezTerm mux、screen |
| 呈现层（UI） | 浏览器/手机怎么看和输入 | ttyd / wetty / GoTTY、sshx、tmate / upterm、Termius / Blink、VS Code Tunnels、code-server、JetBrains Gateway、Warp |
| Agent 层（感知） | 知道 AI 什么时候在等我、推送、审批 | Claude Code Remote Control、Codex 手机遥控、Happy Coder、Tactic Remote、VibeTunnel、Zellij 0.45 通知、hooks + ntfy |

terminalX 的定位是把这五层收进「一个被控端 Agent + 一个自建中转 + 一个 Web 页面」。

---

## 2. 各方案逐项分析（重点：Windows 被控端）

### 2.1 SSH + 终端多路复用（tmux / Zellij / psmux）

- **Windows OpenSSH Server**：Windows 10 1809+/Server 2019+ 内置，作为可选功能安装（`Add-WindowsCapability`），默认 shell 是 `cmd.exe`，需要改注册表 `HKLM\SOFTWARE\OpenSSH\DefaultShell` 才能换成 PowerShell / pwsh / WSL bash [S1][S2]。管理员账户的公钥要放在 `administrators_authorized_keys` 而非用户目录，这是新手最常踩的坑 [S2]。
- **PTY 实现**：Win10 1809+ 使用 ConPTY；旧系统靠 `ssh-shellhost.exe` 做 VT100 翻译。官方 wiki 列出的已知问题包括：resize 不生效、vim 偶尔进入 Replace 模式、PowerShell 错误信息显示不全等 [S3]。
- **会话不能保活是硬伤**：Win32-OpenSSH 官方 issue #2291（2024-10 开启，仍 Open）明确写道「Linux 上可以用 tmux 保持长期会话，Windows 上没有等价物」，维护者的设想是做一个新的 sshd 子系统来重新连接注册的 PowerShell 会话，但未实现 [S4]。同时，sshd 断开后子进程的处理不一致：有 issue 报告子进程被 job object 一起杀掉（#1418/#1642），也有报告进程变成孤儿一直占资源（#1751，milestone vNext 仍 Open）[S5][S6]。也就是说 **在 Windows 上，SSH 断开后你的 Claude Code 是死是活并不确定**。
- **tmux 在 Windows 的现状**：官方 tmux 只能跑在 WSL2 / MSYS2 / Cygwin 里，而且这些环境下的 tmux 只能托管各自环境的 shell，托管原生 PowerShell 需要额外桥接 [S7]。原生替代品在 2025-2026 年冒出来：
  - **psmux**（Rust，直接用 ConPTY，兼容 90+ tmux 命令并读取 `.tmux.conf`，`winget install psmux`，支持 detach/attach，宣称对 Claude Code agent teams 有一等支持）[S8]；
  - **bitcode/tmux-windows**（原生 Win32 移植，仅 16 次提交，处于早期）[S9]；
  - **itmux**（Cygwin + tmux + mintty 的打包，本质仍是 Cygwin 环境）[S10]；
  - **Zellij 0.44.0（2026-03-23）原生支持 Windows**，同版本加入 `zellij attach https://…` 远程附加和只读分享 token；0.45.0（2026-08-20）加入移动端 Web 界面和桌面通知；0.44.1 修了 Windows 移植问题 [S11][S12]。Zellij 是目前 **唯一同时具备「原生 Windows + 内置 Web 客户端 + 移动端适配」** 的多路复用器，但仍需用户自己处理网络暴露、TLS 与账号。
- 结论：SSH + 多路复用在 Linux/macOS 是成熟路线，在 Windows 上要么靠 WSL2 绕道，要么押注 psmux / Zellij 这类 2026 年才成熟的新工具；对普通开发者来说门槛没有降低。

### 2.2 断线容忍 shell（mosh / Eternal Terminal）

- **mosh**：基于 UDP + 状态同步，能扛住网络切换和手机休眠，是移动端 SSH 玩家的标配。但 **没有原生 Windows 服务端**（最新 1.4.0 发布于 2022-10），Windows 只能通过 WSL/Cygwin 跑 mosh-server [S13][S14]。UDP 60000-61000 端口在公司网络常被封。
- **Eternal Terminal（ET）**：TCP 上做可续连，官方 Windows 只支持 WSL；2026-07 出现了一个 Rust 写的 Windows 优先 **客户端**（AArnott/eternal-terminal-client），但 **服务端仍不支持 Windows** [S15][S16]。
- 结论：两者都解决「连接层断线」而非「会话层保活」，且在 Windows 被控端都不可用。

### 2.3 网络穿透（Tailscale / Headscale、Cloudflare Tunnel、ngrok、frp）

- **Tailscale**：WireGuard 网状 VPN，免端口映射，是海外「手机连电脑跑 Claude Code」文章的默认选择 [S17]。但 **Tailscale SSH 服务端不支持 Windows**（issue #4697 / #14942 / #17261 长期 Open），Windows 上只能把 Tailscale 当网络层再叠加 Windows OpenSSH 或 WSL sshd [S18][S19]。2026-04-08 起免费 Personal 计划扩展到 6 用户、不限用户设备（待核实）[S20]。**中国大陆没有官方 DERP 中继**，打洞失败时需自建 DERP 或使用 2025-10 推出的 Peer Relays [S21]。**Headscale** 可自建控制面，但功能范围窄、需自己运维 [S22]。
- **Cloudflare Tunnel**：`cloudflared` 可作为 Windows 服务运行，配合 Zero Trust Access 能在浏览器里渲染 SSH 终端（免费版 50 用户）[S23][S24]。但浏览器 SSH 依赖短期证书（`TrustedUserCAKeys`），在 Windows OpenSSH 上多次被报告不工作，生成的 ssh 配置甚至假定 ProxyCommand 用 bash（issue #375 / #584）[S25]。此外 Cloudflare 在中国的可达性受网络环境影响。
- **ngrok**：2026-02 大幅收紧免费额度：3 个在线端点、1GB 带宽、5K TCP 连接、随机域名 + 插页警告（部分数字待核实）[S26]。TCP 隧道暴露 SSH 22 端口等于把 Windows sshd 直接放到公网。
- **frp**：国内最流行的自建穿透，需要一台有公网 IP 的 VPS，frps/frpc 手工配置，暴露 22 端口后还要自己管密钥和 fail2ban [S27]。
- 结论：穿透只解决「可达」，不解决会话和 UI；而且 Tailscale/Cloudflare 在国内网络下并非稳定可用，自建（Headscale / frp / 自建 DERP）又把运维压力转嫁给用户。**terminalX 用「被控端主动出站连接自建中转节点」的模型，可以天然绕开这一整层。**

### 2.4 Web 终端与终端分享（ttyd / wetty / GoTTY、sshx、tmate / upterm、Zellij web）

| 工具 | 技术 | Windows 被控端 | 说明 |
|---|---|---|---|
| ttyd | C + libwebsockets + xterm.js | 1.7.0 起原生支持（依赖 ConPTY，Win10+）[S28] | 单进程、basic auth、SSL；本身不持久化会话，每个页面连接对应一个新进程，需搭配 tmux 类工具（待核实细节） |
| GoTTY | Go + xterm.js/hterm | README 明示「Windows is not supported now」 [S29] | 原项目多年未更新，社区 fork 为主 |
| wetty | Node + xterm.js，默认 ssh 到 localhost | 未文档化，依赖 Unix 构建链 [S30] | 本质是 Web 版 SSH 客户端 |
| sshx | Rust，E2E 加密（Argon2 + AES），全球 relay（Fly.io） | 提供 Windows x64/x86/ARM64 二进制 [S31] | 多窗格协作、自动重连；**官方声明「暂不支持自托管」** |
| tmate | tmux 旧版 fork | 无原生 Windows | 会话在 tmate 侧持久，可自建 tmate-ssh-server [S32] |
| upterm | Go，反向 SSH 隧道到 uptermd | scoop 可装 Windows 版 [S33] | 可自建 uptermd，GitHub/GitLab 公钥鉴权；定位是「分享一条命令」 |
| Zellij web | Rust，内置 Web server | 0.44 起原生 Windows | 0.45 移动端 Web 界面、桌面通知 [S11][S12] |

结论：这一类最接近「Web 页面控制终端」，但都是 **单机、单入口、无账号体系、无通知**；能自建的（ttyd/upterm/tmate/Zellij）要自己搞 TLS 和穿透，好用的（sshx）不能自建。

### 2.5 IDE 远程（VS Code Remote Tunnels、code-server、JetBrains Gateway）

- **VS Code Remote Tunnels**：`code tunnel service install` 可在 Windows 上注册为服务，通过 GitHub/Microsoft 账号鉴权，浏览器打开 `vscode.dev/tunnel/<机器名>` 即可用内置终端；每账号最多 10 条隧道，一个 server 实例同时只服务一个客户端 [S34][S35]。社区反馈服务会「过一段时间自己死掉」（issue #11176 / #11713）[S36]。终端只是 IDE 的附属，手机上体验很差。
- **code-server**：自建 Web 版 VS Code，官方构建面向 Linux/macOS，Windows 主机通常走 WSL（待核实）。
- **JetBrains Gateway**：后端 **只支持 Linux**（YouTrack CWM-3739）；较新的 Toolbox App 方案宣称支持 Windows/macOS 主机 [S37]。
- 结论：这些方案面向「远程写代码」，不面向「远程盯着多个 AI 会话」；资源占用大、手机不可用。

### 2.6 终端厂商方案（Warp、WezTerm、Termius / Blink / Moshi）

- **Warp Remote Control / Session Sharing**：把会话状态上传到 Warp 云端生成链接，浏览器/手机可看可控，支持 Claude Code 等第三方 agent；托管 relay 有每日会话数、并发观看数配额（`NoUserQuotaRemaining`），社区因此做了自建 relay，同时指出全屏 TUI 每个按键都要走 relay 往返，「感觉很卡」[S38][S39]。绑定 Warp 终端本身。
- **WezTerm SSH 多路复用**：`SSHMUX:` 域会在远端拉起 `wezterm-mux-server`，会话持久在远端；要求两端都装 WezTerm，属于重度用户配置项 [S40]。Windows 远端通过 SSH 拉起 mux-server 的可用性待核实。
- **手机 SSH 客户端**：Termius Pro 约 $10/月（年付，待核实）、Blink Shell 约 $19.99/年（待核实），Blink 以原生 mosh 见长 [S41]。**iOS 的根本限制是 app 进入后台 20-30 秒即被挂起、SSH 断开**，只有 mosh 能缓解，而 mosh 在 Windows 没有服务端 [S42][S43]。Termius 2026 年提供 Startup Command 自动拉起 agent、Live Activities，但「缺少 agent 感知能力：Claude Code 卡在审批时不会推送、没有 diff 查看」[S44]。新出现的 Moshi、Secure ShellFish 2026.4 等开始针对 agent 做 hooks 通知、Live Activities、diff 查看 [S45]。

### 2.7 AI CLI 原生远程与第三方 Agent 中继

- **Claude Code Remote Control**（2026-02-24/25 研究预览，先 Max 后全计划）：`claude remote-control`（服务模式）/ `claude --remote-control` / `/remote-control` 三种启动方式，扫码后手机 app 或 claude.ai/code 与本地会话双向同步；代码不出本机，转录存在 Anthropic 服务器；断网自动重连并排队消息、权限提示；服务模式离线约 10 分钟后退出，停掉服务后约 4 小时内可 `--continue` 恢复 [S46]。官方限制中明确写着：**「本地进程必须持续运行……要在远程机器上保持会话，请在 tmux 或 screen 里启动」**——在 Windows 上这句话没有现成答案。此外 **需要 claude.ai 登录（API key 不行），设置了 `ANTHROPIC_BASE_URL`、Bedrock/Vertex 或 `DISABLE_TELEMETRY` 等变量时功能直接不可用** [S46]，这意味着国内大量走第三方中转的用户无法使用。2026-08-21/22 的更新允许从手机直接新建会话、机器以「设备卡片」形式列出 [S47]。
- **Codex 手机遥控**：2026-05-14 ChatGPT 移动端上线，先只支持 Mac 主机；2026-05-29 Codex app v26.527.1 加入 Windows 主机；至今没有 Linux 主机路径 [S48][S49]。依赖 Codex 桌面 app 常驻。另有 Codex Desktop Remote SSH 到 Windows 时因默认 shell 为 PowerShell 而失败的 issue（#22757，2026-05 Open）[S50]。
- **Happy Coder**（MIT，约 23.6k stars）：`happy claude` / `happy codex` 包装 CLI，经中继同步到 iOS/Android/Web，E2E 加密、推送通知；Windows 支持未在 README 中说明（待核实）[S51]。
- **Tactic Remote**：原生 iOS app 直连本机 server（局域网无云），1.8.0（2026-05）加入 Windows 桌面端预览 [S52]。
- **VibeTunnel**：macOS 优先，npm 版支持 Linux，Windows 尚不可用 [S53]。**Orca**：桌面 ADE + 手机伴侣，macOS/Windows/Linux [S54]。
- **通知拼装**：Claude Code hooks（Notification / Stop）+ ntfy.sh 可以做到手机上 Allow/Deny，Windows 有 PowerShell 版 hook 脚本，但全部要用户自己配 [S55]。
- 结论：AI 原生远程证明了「手机上盯 agent」是真实需求，但 **每家只管自家工具、依赖各自 relay、对 Windows 支持滞后、对国内网络/第三方 API 中转不友好**，且都是「单会话 / 单机」视角，没有多机多会话的统一面板。

---

## 3. 横向对比表

评分：● 良好 ◐ 部分/需绕道 ○ 不支持或不适用。「Win 被控」指作为被控端在 Windows 上原生运行的可行性。

| 方案 | Win 被控 | 会话保活 | 免穿透 | 手机 UI | Agent 通知 | 多机多会话面板 | 可自建 | 成本 |
|---|---|---|---|---|---|---|---|---|
| Windows OpenSSH + PowerShell | ● 内置 | ○（#2291 Open） | ○ | ○ | ○ | ○ | ● | 免费 |
| SSH + tmux（WSL2） | ◐ 只托管 WSL shell | ● | ○ | ○ | ○ | ○ | ● | 免费 |
| SSH + psmux / tmux-windows | ◐ 新项目 | ● | ○ | ○ | ○ | ○ | ● | 免费 |
| Zellij 0.44+（含 web client） | ● 原生 | ● | ○ | ◐ 0.45 移动端 | ◐ 桌面通知 | ○ 单机 | ● | 免费 |
| mosh | ○ 无 Win 服务端 | ○（需配 tmux） | ○ | ◐（Blink/Termius） | ○ | ○ | ● | 免费 |
| Eternal Terminal | ○ 服务端不支持 | ○ | ○ | ○ | ○ | ○ | ● | 免费 |
| Tailscale SSH / Headscale | ○ SSH 服务端不支持 Win；◐ 作网络层 | ○ | ● | ○ | ○ | ○ | ◐ Headscale | 免费 6 用户（待核实） |
| Cloudflare Tunnel 浏览器 SSH | ◐ 短期证书在 Win 有 issue | ○ | ● | ◐ 浏览器 | ○ | ○ | ○ | 免费 ≤50 用户 |
| ngrok TCP | ◐ 直接暴露 sshd | ○ | ● | ○ | ○ | ○ | ○ | 免费额度大幅收紧 |
| frp | ● | ○ | ●（需 VPS） | ○ | ○ | ○ | ● | VPS 费用 |
| ttyd | ● 1.7+ ConPTY | ○（需配多路复用） | ○ | ◐ 浏览器 | ○ | ○ | ● | 免费 |
| GoTTY / wetty | ○ / ◐ | ○ | ○ | ◐ | ○ | ○ | ● | 免费 |
| sshx | ● 有二进制 | ◐ 自动重连 | ● | ◐ 浏览器 | ○ | ○ | ○ 官方不支持 | 免费 |
| tmate / upterm | ○ / ● scoop | ● / ◐ | ● | ○ | ○ | ○ | ● | 免费 |
| Warp Remote Control | ● Warp 有 Win 版 | ◐ 依赖 Warp 进程 | ● | ◐ 浏览器，逐键延迟 | ◐ | ○ | ◐ 社区 relay | 免费有配额 |
| WezTerm SSHMUX | ◐ 待核实 | ● | ○ | ○ | ○ | ○ | ● | 免费 |
| VS Code Remote Tunnels | ● 可装服务 | ◐ 服务易掉 | ● | ○ | ○ | ◐ 10 隧道列表 | ○ | 免费 |
| code-server / JetBrains Gateway | ◐ WSL / ○ Linux only | ◐ | ○ | ○ | ○ | ○ | ● / ○ | 免费 / 订阅 |
| Termius / Blink（客户端） | — | 依赖服务端 | — | ◐ 键盘/后台 20-30s | ○（Termius 无 agent 感知） | ◐ 主机列表 | — | $10/月 / $20/年（待核实） |
| Claude Code Remote Control | ◐ 原生 Win 版可用（待核实），需 tmux 保活 | ◐ 4 小时内可恢复 | ● 官方 relay | ● Claude app | ● 推送 | ○ 单工具 | ○ | 需订阅，API/中转不可用 |
| Codex 手机遥控 | ● 2026-05-29 起 | ◐ 依赖桌面 app | ● 官方 relay | ● ChatGPT app | ● | ○ 单工具 | ○ | 需 ChatGPT 账号 |
| Happy Coder | ? 未说明 | ◐ | ● 中继 | ● | ● | ◐ 多会话，单工具族 | ◐ | 免费开源 |
| Tactic Remote / VibeTunnel | ◐ 预览 / ○ | ◐ | ○ 局域网 / ○ | ● / ◐ | ● / ◐ | ◐ / ○ | ● / ● | 付费 app / 开源 |

---

## 4. Windows 被控端专题：为什么「在 Windows 上远程跑 AI CLI」格外难

1. **没有会话守护层**。Linux 的 sshd → tmux → 进程 三层里，Windows 缺了中间那层。Win32-OpenSSH #2291 仍 Open，psmux/Zellij for Windows 都是 2025-2026 年才出现的新事物 [S4][S8][S11]。
2. **进程生命周期不可控**。sshd 用 job object 管理子进程，断线后有时全杀、有时留孤儿（#1418/#1642/#1751），行为与 Linux 的 SIGHUP 语义不同，`nohup`/`start` 这类习惯性手段无效 [S5][S6]。
3. **ConPTY 是唯一正确的 PTY 路径，但兼容性有裂缝**。resize、vim 模式、错误输出截断等问题在官方 wiki 有记录 [S3]；Claude Code 2.1.30 曾在「Termius iOS → Windows OpenSSH → PowerShell」链路下输入框完全不接受键盘输入（issue #22948，2026-02）[S56]；Codex Desktop Remote SSH 因假设远端是 POSIX shell 而在 PowerShell 上直接报 ParserError（#22757）[S50]。
4. **三种 shell 三种世界**。cmd / PowerShell（5.1 与 7 又不同）/ WSL 的路径、编码（GBK vs UTF-8）、转义、环境变量互不相同；Claude Code 2026-05 起在 Windows 上默认走原生 PowerShell 工具、不再强依赖 Git Bash [S57]，但 Codex 等其它 CLI 仍偏好 POSIX shell。被控端必须允许用户 **按会话选择 shell**。
5. **NAT 与公司网络**。家用宽带无公网 IPv4、公司网络封 UDP（mosh）和非常规端口（ssh 2022、DERP）；Tailscale/Cloudflare 在国内可达性不稳，自建 frp/Headscale 需要 VPS 与运维。出站 WebSocket/HTTPS 到自建中转是唯一「哪里都能通」的路径。
6. **移动端 SSH 的物理限制**。iOS 后台 20-30 秒断连、软键盘没有 Ctrl/Esc/方向键、TUI 在 6 英寸屏幕上重绘混乱、复制粘贴困难，这些问题不会因为换客户端而消失 [S42][S43][S44]。

---

## 5. 为什么大多数普通开发者没有用这些方案远程跑 AI CLI

1. **配置门槛是「乘法」不是「加法」**：装 sshd → 改 DefaultShell → 配公钥（管理员还要改 ACL）→ 开防火墙 → 装 Tailscale/frp → 装 tmux/psmux → 手机装 Termius → 配 hooks + ntfy。任何一步出错都要查 GitHub issue。海外教程默认 Mac/Linux 主机，Windows 用户几乎没有一条走通的「官方路径」。
2. **没有会话管理**：SSH 只有「连接」没有「会话」概念；Windows 上连 tmux 都没有，断线就等于赌运气。
3. **没有移动端友好 UI**：SSH 客户端只是终端仿真器，不理解「这是一个正在等审批的 agent」。
4. **没有通知**：SSH 天然无推送，agent 什么时候完成、什么时候卡在权限提示，只能自己盯屏幕。
5. **没有多机 / 多会话视图**：家里台式机 + 公司工作站 + 云服务器 + 4 个并行 agent，没有任何工具把它们放到一个面板里；Claude RC、Codex 遥控各管各的工具。
6. **原生方案的硬约束**：需要官方订阅登录、走官方 relay、对第三方 API 中转和国内网络不友好；Windows 支持普遍比 Mac 晚 2-6 个月。

---

## 6. 对 terminalX 的启示与机会点

1. **被控端 Agent = Windows 版「tmux 守护进程」**：直接持有 ConPTY，进程脱离 sshd/job object，会话持久 + 滚动缓冲回放 + 多窗格；这是 Windows 生态里最大的空白（#2291 四年未解）。可参考 psmux 的「一个 ConPTY 一个 pane + 预热池」设计 [S8]。
2. **出站连接自建中转，彻底不做穿透**：借鉴 Happy Coder / sshx 的 relay + E2E 模型，被控端主动 WebSocket 出站，公司网络与家用 NAT 都可通；中转节点自建，规避 Tailscale DERP / Anthropic relay 在国内的不确定性。
3. **按会话选 shell**：PowerShell 7 / Windows PowerShell 5.1 / cmd / WSL 分发；对 Codex 这类假设 POSIX 的工具默认给 WSL 或 Git Bash，避开 #22757 类问题。
4. **Agent 感知而非纯终端**：识别 Claude Code / Codex / Grok / MiniMax 的「等待输入 / 权限提示 / 完成」状态（hooks + 输出模式匹配），推送到手机，提供一键 Allow/Deny；对接 Claude Code hooks 与 `CLAUDE_CLIENT_PRESENCE_FILE` 这类官方机制 [S46][S55]。
5. **移动端优先的输入层**：快捷键条（Esc/Ctrl/Tab/方向键）、常用命令按钮、语音输入、diff 查看，参考 Moshi / Termius「8 tips」中被反复提及的痛点 [S44][S45]。
6. **多机多会话面板**：设备卡片 + 会话列表 + 状态徽章（运行中 / 等待审批 / 已完成 / 离线），这是所有现有方案都没有的「控制塔」视角；Claude RC 2026-08 的设备卡片说明官方也在往这个方向走 [S47]。
7. **兼容而非替代**：保留 SSH/tmux 作为逃生通道（例如把 terminalX 会话映射为可 `attach` 的 psmux/Zellij 会话），并允许在 terminalX 里启动 `claude --remote-control`，不与厂商原生方案冲突。
8. **国内可用性作为一等特性**：不依赖 claude.ai 登录、支持第三方 API 中转环境、中转节点可部署在国内 VPS。

---

## 7. 来源链接

- [S1] Microsoft Learn：OpenSSH 服务器配置（DefaultShell 注册表）— https://learn.microsoft.com/zh-cn/windows-server/administration/openssh/openssh-server-configuration
- [S2] Microsoft Learn：开始使用 Windows 的 OpenSSH 服务器 — https://learn.microsoft.com/zh-cn/windows-server/administration/openssh/openssh_install_firstuse
- [S3] Win32-OpenSSH wiki：TTY/PTY support in Windows OpenSSH — https://github.com/PowerShell/Win32-OpenSSH/wiki/TTY-PTY-support-in-Windows-OpenSSH
- [S4] Win32-OpenSSH #2291：Support long lived sessions that can survive a disconnect — https://github.com/PowerShell/Win32-OpenSSH/issues/2291
- [S5] Win32-OpenSSH #1751：Child processes is NOT killed on disconnect — https://github.com/PowerShell/Win32-OpenSSH/issues/1751
- [S6] Win32-OpenSSH #1642 / #1418：sshd 结束会话后杀子进程 — https://github.com/PowerShell/Win32-OpenSSH/issues/1642 ｜ https://github.com/PowerShell/Win32-OpenSSH/issues/1418
- [S7] tmux.app：How to Install tmux on Windows（WSL2 / Cygwin / MSYS2）— https://tmux.app/install/windows/
- [S8] psmux：native tmux for Windows（ConPTY, Rust）— https://github.com/psmux/psmux
- [S9] bitcode/tmux-windows：Native Win32 port of tmux — https://github.com/bitcode/tmux-windows
- [S10] itmux（Cygwin 打包）— https://github.com/itefixnet/itmux
- [S11] Zellij Releases（v0.44.0 2026-03-23 原生 Windows；v0.45.0 2026-08-20 移动端 Web）— https://github.com/zellij-org/zellij/releases
- [S12] Zellij 0.44.0 公告：Remote Sessions, Windows Support, CLI Automation — https://zellij.dev/news/remote-sessions-windows-cli/
- [S13] mosh 官网 — https://mosh.org/
- [S14] Using mosh in Windows 10（WSL 路线）— https://ideawrights.com/mosh-windows-wsl/
- [S15] Eternal Terminal 官网（Windows 仅 WSL）— https://eternalterminal.dev/
- [S16] AArnott/eternal-terminal-client（Rust，Windows 优先客户端，2026-07）— https://github.com/AArnott/eternal-terminal-client
- [S17] Twingate：Run Claude Code from Your Phone Securely（Termius + tmux + Tailscale）— https://www.twingate.com/blog/claude-code-termius-tmux
- [S18] Tailscale #14942：FR: The Tailscale SSH server supported on windows — https://github.com/tailscale/tailscale/issues/14942
- [S19] Tailscale + SSH setup for Windows + WSL2（2026-03）— https://benjijang.com/posts/2026/03/tailscale-wsl2-ssh/
- [S20] Tailscale pricing update（2026-04-08 计划调整）— https://tailscale.com/blog/pricing-v4
- [S21] Tailscale DERP servers / Peer Relays — https://tailscale.com/docs/reference/derp-servers ｜ https://tailscale.com/docs/features/peer-relay
- [S22] Headscale — https://github.com/juanfont/headscale
- [S23] Cloudflare One：Browser-rendered terminal — https://developers.cloudflare.com/cloudflare-one/access-controls/applications/non-http/browser-rendering/
- [S24] Cloudflare Zero Trust 免费 50 用户限制（社区）— https://community.cloudflare.com/t/50-user-limit-on-free-plan/546057
- [S25] cloudflared #375 / #584：Access SSH short-lived cert 在 Windows 不工作 — https://github.com/cloudflare/cloudflared/issues/375 ｜ https://github.com/cloudflare/cloudflared/issues/584
- [S26] ngrok Free Plan Limits — https://ngrok.com/docs/pricing-limits/free-plan-limits
- [S27] frp 实现内网穿透（Windows 版）— https://www.cnblogs.com/cxfs/p/13071969.html
- [S28] ttyd 1.7.0 release notes（native Windows, requires ConPTY）— https://newreleases.io/project/github/tsl0922/ttyd/release/1.7.0 ｜ https://github.com/tsl0922/ttyd
- [S29] GoTTY README（Windows is not supported now）— https://github.com/yudai/gotty
- [S30] wetty — https://github.com/butlerx/wetty
- [S31] sshx（Windows 二进制；官方不支持自托管）— https://github.com/ekzhang/sshx
- [S32] tmate-ssh-server 断线重连讨论 — https://github.com/tmate-io/tmate-ssh-server/issues/62
- [S33] upterm（scoop 安装 Windows，uptermd 自建）— https://github.com/owenthereal/upterm
- [S34] VS Code Remote Tunnels 文档（10 tunnels/account, service install）— https://github.com/microsoft/vscode-docs/blob/main/docs/remote/tunnels.md
- [S35] VS Code Server 文档 — https://code.visualstudio.com/docs/remote/vscode-server
- [S36] vscode-remote-release #11176 / #11713：tunnel service 自行退出 — https://github.com/microsoft/vscode-remote-release/issues/11176 ｜ https://github.com/microsoft/vscode-remote-release/issues/11713
- [S37] JetBrains Gateway doesn't Support Windows as Remote Server（CWM-3739）— https://youtrack.jetbrains.com/issue/CWM-3739 ｜ FAQ — https://www.jetbrains.com/help/idea/faq-about-remote-development.html
- [S38] Warp Remote Control 文档 — https://docs.warp.dev/agent-platform/cli-agents/remote-control/
- [S39] b1nhm1nh/warp-server：自建 Warp 分享 relay（说明托管配额与逐键延迟）— https://github.com/b1nhm1nh/warp-server
- [S40] WezTerm Multiplexing（SSH / SSHMUX 域）— https://wezterm.org/multiplexing.html
- [S41] Blink Shell vs Termius 2026（定价、mosh）— https://getmoshi.app/articles/blink-vs-termius
- [S42] Termius 帮助中心：iOS 后台保活限制 — https://docs.termius.com/help-center/faq/how-can-i-keep-termius-sessions-alive-in-the-background-on-ios-ipados
- [S43] blinksh/blink #1122：iOS 后台几秒即断连 — https://github.com/blinksh/blink/issues/1122
- [S44] Termius Blog：8 tips for using AI agents on mobile — https://termius.com/blog/8-tips-for-using-ai-agents-on-mobile-in-termius
- [S45] 少数派：移动端 SSH + CLI Coding Agent 的实践与体验优化 — https://sspai.com/post/105621 ｜ Moshi：Best iOS Terminal App for AI Coding Agents 2026 — https://getmoshi.app/articles/best-ios-terminal-app-coding-agent
- [S46] Claude Code 官方文档：Remote Control（要求、限制、恢复、隐私）— https://code.claude.com/docs/en/remote-control
- [S47] Nimbalyst：Best Mobile Apps for Claude Code 2026（含 2026-08 RC 更新）— https://nimbalyst.com/blog/best-mobile-apps-for-claude-code-2026/
- [S48] OpenAI：Work with Codex from anywhere（2026-05-14）— https://openai.com/index/work-with-codex-from-anywhere/
- [S49] 9to5Mac：ChatGPT for iOS can now start Codex work on Windows（2026-05-29）— https://9to5mac.com/2026/05/29/chatgpt-for-ios-can-now-start-codex-work-on-windows/
- [S50] openai/codex #22757：Remote SSH to Windows OpenSSH fails when default shell is PowerShell — https://github.com/openai/codex/issues/22757
- [S51] Happy（slopus/happy）— https://github.com/slopus/happy
- [S52] Tactic Remote 1.8.0：Windows 支持 — https://tacticremote.com/blog/2026-05-15-cross-platform-design-choices/
- [S53] VibeTunnel — https://vibecodinghub.org/tools/vibetunnel
- [S54] Orca ADE — https://vibecodinghub.org/tools/orca
- [S55] claude-ntfy-hook（手机 Allow/Deny）— https://github.com/nickknissen/claude-ntfy-hook ｜ lihaoz-barry/claude-code-hooks（Windows PowerShell hooks）— https://github.com/lihaoz-barry/claude-code-hooks
- [S56] anthropics/claude-code #22948：SSH 会话（Windows OpenSSH + Termius iOS）键盘输入失效 — https://github.com/anthropics/claude-code/issues/22948
- [S57] Claude Code on Windows：native PowerShell tool（2026-05）— https://claudcod.com/blog/claude-code-windows-powershell/
- [S58] 知乎：手机如何远程操控 Claude Code 和 Codex — https://zhuanlan.zhihu.com/p/2014378628566262798
- [S59] Codex Knowledge Base：Codex CLI Remote Control and Mobile Pairing（2026-06）— https://codex.danielvaughan.com/2026/06/06/codex-cli-remote-control-mobile-pairing-app-server-v2-remodex/

> 标注「待核实」的数字（定价、免费额度、部分平台支持）来自二手报道或搜索摘要，未能打开原文核对，落地前请以官方页面为准。
