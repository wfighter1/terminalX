# terminalX

**你自己部署的 AI 代理监工台。** Windows 上的 Claude Code / Codex / Grok Build（MiniMax 作为供应商预设）会话常驻不掉线，手机上一屏看清谁在等你、一键处理，任何 API 配置都能远程。

> 状态：设计定稿，第一阶段实现进行中（2026-09）。已有：协议层、中转 tx-relay（含测试与 Docker 部署）、被控端 tx-agent（PTY 会话常驻、断线补差、hooks 端点、配对、登录自启安装）、Web 控制台；三者在 Linux 上已跑通端到端冒烟测试（配对 → 上线 → 开会话 → 输入回显 → 刷新后快照回放 → 关闭），Windows 目标已交叉编译通过。尚未：Windows 真机验证、supervisor / ptyhost 双进程、代码签名、发布版本。代码目录结构见 [架构文档 §7](docs/04-第一阶段技术架构.md)，部署见 [deploy/README.md](deploy/README.md)。

## 结论先行

1. **亮点成立，但窄。** 它是四项的交集：Windows 被控端常驻保活 × 任意 API 配置 × 国内可达的自建中转 × 真终端与 agent 感知双视图。任一单项都已有人做；四项同时兑现才有存在理由。
2. **它解决的 AI 时代痛点**是「起任务 → 走开 → 手机盯 → 回来验收」这个新工作方式下的三条：断线后静默失效、agent 等审批时无人知道、Windows 被控端没人管好。
3. **前提是目标人群真实存在**：用 Windows 台式机跑 AI CLI、走中转站 Key、白天不在电脑前的开发者。动工前先访谈 20 人验证。

三个反方视角（模拟的官方产品负责人、已有 SSH + tmux 的老手、技术合伙人）逐条反驳后的结论与被砍掉的口号，见 [红队评审](docs/03-红队评审与亮点判断.md)。

## 它解决什么

开发者的工作正在从「写代码」变成「监工多个 agent」：早上起几个任务出门，手机上盯，回来验收。现有工具在同一批地方失败：

- **官方 Claude Code Remote Control** 不支持 API Key，`ANTHROPIC_BASE_URL` 指向中转站即禁用，Bedrock / 网关 / ZDR 组织不可用，只有聊天 UI 没有终端，本地进程必须常驻且建议 tmux——Windows 没有 tmux。
- **ToDesk / 向日葵 / TeamViewer / RustDesk** 传的是像素：手机上终端字看不清、虚拟键盘没有 Esc / Ctrl、没有「谁在等我」。ToDesk 免费额度反复缩水，向日葵 / ToDesk 被企业禁装，TeamViewer 商业用途检测误判且国内连接差，RustDesk 能自建但仍传像素。
- **网易 UU远程**是例外，也是最实质的对手：零配置终端、会话分离与重新接入、CLI、端口映射，全免费且国内节点。但它是个**哑终端**——没有「谁在等我确认」、没有多机总览、不是 Web 页面、不可自建。逐款结论见 [设计文档 §4.1](docs/02-产品设计.md)，机制拆解见 [调研 07](docs/research/07-UU远程终端实现分析.md)。
- **SSH + tmux + Tailscale** 在 Windows 被控端上是二等公民：sshd 断线子进程被回收（Win32-OpenSSH #2291 四年未解）、mosh / Tailscale SSH 没有 Windows 服务端、手机 SSH 客户端不知道「这是一个在等审批的 agent」。
- **Happy / Happier / Paseo** 这类开源伴侣：境外中转国内连不上，Windows 稳定性差，必须用包装器启动。

terminalX 只做一件事：**只传字节与事件，不传像素。** 一个 Windows 被控端常驻托管终端会话；一个自建中转只做路由；一个 Web 页面（和手机 PWA）看所有机器、所有会话，处理「等你确认」，需要时下潜到真终端。

场景就是远程办公：人在公司或路上，Windows 在家里或工位，只要被控端能出站 443（企业代理也行），手机或任何浏览器就能接管。

## 数据流向

对应三种形态：**控制端** = 浏览器 / 手机 PWA；**服务端** = 自建中转；**被控端** = Windows 上的单 exe。

```
浏览器 / 手机 PWA  ◄── WSS ──►  自建中转（你的 VPS，只存元数据）  ◄── WSS 出站 443 ──  Windows 被控端（登录自启，只出站）
                                                                                              │ ConPTY
                                                                                              ▼
                                                                                    claude · codex · grok-build · pwsh
状态事件（Claude http hooks · Codex hooks.json · statusLine 两个数）沿同一通道回推 → 收件箱 + 推送（飞书 / 企微 / Bark / ntfy）
第一阶段：TLS 到中转，内容不落盘；帧内容按不透明字节设计，逐帧端到端加密在 1.1 实装（校验失败即断，不降级）。
```

## 如果你已经在用……

| 你在用 | 你多得到的 | 该继续用它的情况 |
|---|---|---|
| 官方 Claude Code Remote Control / Codex Remote | 中转 Key / Bedrock / 网关也能远程；一个面板管多家工具；真终端与远程 Esc | **Mac + 订阅 + 能直连官方 + 只用一家工具：请直接用官方** |
| Happier / Paseo / Happy | Windows 被控端保活与重启自动拉回、国内一条命令中转、免包装器附着、远程 ≤ 本地 | 你主要在 macOS / Linux |
| VibeAround（国内，Windows x64） | 自建中转与审批收件箱 | 你更需要 IM 渠道数量 |
| Tailscale + WSL2/tmux 或 Zellij / psmux + Termius | 只有三样：Windows 常驻保活与重启自动拉回、免包装器跨工具收件箱、一条命令国内中转 | 你已经配好且不嫌烦 |
| ToDesk / 向日葵 / RDP | 文本不糊、KB/s 带宽、不用解锁屏幕、有「谁在等我」 | 你需要看 GUI |

诚实地说：差异化成立，但窄。它是四项的交集——Windows 被控端常驻保活 × 任意 API 配置 × 国内可达自建中转 × 真终端与 agent 感知双视图。详见 [红队评审](docs/03-红队评审与亮点判断.md)。

## 第一阶段范围

- Windows 被控端：单 exe，登录自启（用户态，不是服务），ConPTY 会话常驻，断线补差，重启后一键拉回
- 自建中转：单二进制 + Docker 一条命令，配对码 + 指纹比对，只存元数据
- Web 控制台：收件箱首页、设备列表、会话列表、多标签终端、三信号连接状态、远程解卡（Esc / Ctrl-C / kill & resume）
- Claude Code：http hooks 两种审批模式（默认通知，可选远程优先）；Codex：hooks.json 登记 + 打开终端；Grok Build：终端 + hooks 尽力
- MiniMax：本次核实 MiniMax 没有编码 CLI（MiniMax Code 是桌面 App），因此建模为「供应商预设」，一键让 Claude Code / Codex 走 MiniMax 端点。如你指的是别的工具请纠正，见 [设计文档 §1](docs/02-产品设计.md)
- 手机 PWA：只读终端 + 键条 + 独立输入框；webhook 通知

不做：GUI 画面远程（永不）、Windows 服务化（无官方路径）、E2E 实装（1.1 硬门槛）、成本面板与额度续跑、只读分享、回放、团队权限。完整清单见设计文档 §12。

## 文档

| 文档 | 内容 |
|---|---|
| [01 · 市场调研总览与痛点分析](docs/01-市场调研总览与痛点分析.md) | 三类方案全景、痛点排序、用户画像、市场空白的诚实评估 |
| [02 · 产品设计](docs/02-产品设计.md) | **主文档**：需求对照、定位、亮点分级、为什么不用 X、审批模式、信息架构、范围进 / 不进、验收、里程碑、路线图 |
| [03 · 红队评审与亮点判断](docs/03-红队评审与亮点判断.md) | 评审打分、三视角红队反驳、被砍掉的口号、引用修正 |
| [04 · 第一阶段技术架构](docs/04-第一阶段技术架构.md) | 选型（Go）、协议、会话保持、安全、Windows 细节、AI 感知层、里程碑、风险 |
| [research/](docs/research/) | 七份原始调研：GUI 远控、终端 / SSH 方案、AI 代理远程控制赛道、AI CLI 能力盘点、技术可行性、用户声音、UU远程终端实现分析 |
| [design-proposals/](docs/design-proposals/) | 三份独立产品方案（A · AI 原生监工台 / B · 务实 MVP / C · 安全自建与团队） |
| [architecture-options/](docs/architecture-options/) | 两份候选架构（Go / Node + Rust） |
| [prototype/index.html](prototype/index.html) | 可交互原型：控制台、收件箱、手机端、配对、架构与范围 |

看不懂的词（ConPTY、hooks、ZDR、PWA、1.1 等）见 [设计文档附录 A 术语表](docs/02-产品设计.md)。

## 下一步

1. 动工前访谈 20 个「Windows + 中转 Key + 手机」用户，本次国内社区证据均为二手。
2. 第 1 周三个技术验证：ConPTY 起 `claude` 在 xterm.js 渲染；原生 Windows 上 http 型 hook 触发与回写；Android 真机输入框中文到 PTY。任一失败即降级而非顺延。
3. 每月复查失败信号：Anthropic 是否放开自定义 base URL 的 Remote Control；Codex 是否修好 Windows daemon；Happier / Paseo 是否做稳 Windows。

## 许可

计划 Apache-2.0。自建永久免费且包含全部安全能力。
