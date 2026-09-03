# 01 · 传统 GUI 远程桌面工具调研

> 调研日期：2026-09-03　｜　调研对象：ToDesk、向日葵(Sunlogin)、TeamViewer、网易 UU远程、RustDesk、AnyDesk、Parsec、Chrome Remote Desktop、Windows 远程桌面(RDP)
> 目的：为 terminalX（多端远程操作 PC 上 Claude Code / Codex CLI / Grok CLI / MiniMax Code 等 AI CLI 工具）的产品设计提供对照。
> 说明：本文优先采信 2025–2026 年的公开报道与官方文档；调研环境无法直接打开 ToDesk / 向日葵 / TeamViewer / AnyDesk / Parsec 官网定价页，价格类数字均来自二手报道并标注「待核实」。

---

## 0. 一页结论

1. **GUI 远程是「像素流」，AI CLI 需要的是「文本流 + 事件流」。** 传统远控把整块屏幕编码成视频推给手机，带宽以 Mbps 计、清晰度受帧率/分辨率档位限制；而一个 Claude Code 会话本质是几 KB/s 的文本与少量结构化事件（权限确认、任务完成、报错）。用 GUI 远程看终端是拿高成本手段解决低带宽问题，且体验反而更差。
2. **免费权益全面收紧、商业化压力外溢到个人开发者。** ToDesk 2024–2025 年三次削减免费额度（至每月 200 次 / 80 小时，再到不公布具体数字），TeamViewer 「商业用途检测」误判频发，AnyDesk 2024→2026 价格涨幅 40%–118%。只有网易 UU远程在 2025–2026 年以「全免费」进场，但可持续性存疑。
3. **远控软件已成为电诈标配工具，被反诈体系「污名化」。** 向日葵 / ToDesk / TeamViewer / AnyDesk 都被诈骗剧本点名；RustDesk 因此对公共服务器强制登录、主动从 Google Play 下架、国内公共节点暂停；国内企业与高校陆续发文禁装向日葵 / ToDesk。这意味着「远程控制」四个字本身在企业安全策略里是减分项，terminalX 需要用「终端 + 审计 + 最小权限」的叙事切割开。
4. **头部远控厂商正在向「终端」靠拢。** RustDesk 1.4.1（2025-08）加入 Terminal 模式并在移动端加浮动快捷键；网易 UU远程 2026 年上线「零配置终端」与「端口映射」；ToDesk 被控端支持 CMD（但手机端不支持）；向日葵把 CMD/SSH 放在精英版及以上。但它们都是把终端当作远控的附属功能，没有为「多台机器 × 多个 AI 会话 × 通知/审批」建模。
5. **Anthropic 原生 Remote Control（2026-02）是最直接的竞品，也是最好的参照。** 它免费随 Pro/Max/Team/Enterprise 提供，但只覆盖 Claude Code、要求本地进程持续存活、不支持 API Key / Bedrock / 自定义 `ANTHROPIC_BASE_URL`（即国内常见的中转 API 用户直接不可用）、Windows 上仍有会话恢复类 bug。terminalX 的差异化空间在：多 CLI 厂商、多机器聚合、自建中转、进程托管与保活、国内网络可达性。

---

## 1. 逐产品分析

### 1.1 ToDesk（国产，海南有趣科技）

- **定位**：国内装机量最大的通用远控之一，主打「免费、流畅、国内节点多」（宣称国内 200+ 节点、P2P 直连 1–3 ms，厂商口径，待核实）。
- **定价**：个人免费版；专业版约 ¥24/月 或 ¥158/年；游戏版、设计版约 ¥42/月（年付约 ¥298）；性能版约 ¥95/月（年付约 ¥638）。（2025 年报道口径，待核实）
- **免费版限制（2024–2025 变化）**：
  - 画质最高 1080p / 30 FPS；专业版 60 FPS，游戏/设计版 240 FPS，性能版 360 FPS。
  - 2024-05 首次在未提前通知的情况下限制免费时长与次数；2024-06 起每月 300 次 / 120 小时；2024-07 个人版全线涨价，隐私屏、虚拟鼠标移出免费序列。
  - 2025-02/03 再降至每月 200 次 / 80 小时；2025-03 底再次公告削减且「不再告知具体时长」；额度用完后到次月 1 日重置或付费。
  - 用户还投诉「免费中转节点繁忙」提示、被控端 GPU 占用过高、卡顿模糊。
- **传输架构**：P2P 直连优先，失败走 ToDesk 中转；不提供自建服务器（企业版有私有化选项，价格待核实）。
- **安全与口碑**：
  - 反诈措施：新登录设备 24 小时内不可被远控手机；打开网银/支付宝等 App 时被控端自动黑屏；2025 年新增二次验证。
  - 用户协议保留封号/冻结权；有企业和学校发文「限期卸载向日葵、ToDesk」，理由是「安全风险」。
  - 官方宣称有 ISO 27001 认证、上线以来无安全事故（厂商口径）。
- **对开发者的不足**：手机端不支持 CMD 终端（仅 Win/Mac/Linux 被控端有 CMD 功能）；多机管理是设备列表 + 逐台连入，没有「会话」概念；后台保活依赖被控端不休眠；免费额度以「连接次数」计费，对「一天看几十次 AI 进度」的使用方式极不友好。

### 1.2 向日葵 Sunlogin（贝锐 Oray）

- **定位**：国内老牌远控，产品线覆盖个人/企业/硬件（开机棒、控控）。
- **定价**：个人免费；瓜子会员约 ¥128/年；超级会员约 ¥298/年（4K 144 帧、真彩）。（2025 年报道，待核实）
- **免费版限制**：普通服务器、仅点对点 + 自动转发（付费才有强制转发）、桌面会话数 1、PC 帧率最高 30、文件传输限速。
- **终端能力**：有「远程 CMD / SSH」，手机、平板可发起，但要求**精英版及以上**（面向企业/服务版本）。
- **传输架构**：P2P + 贝锐中转；企业版有私有化部署方案（价格待核实）。
- **安全与口碑**：
  - 2022-02 CNVD-2022-10270 / CNVD-2022-03672：Windows 个人版 ≤ 11.0.0.33 存在未授权远程代码执行，被广泛复现，是国产远控最著名的安全事件。
  - 2025 年横评称向日葵尚无二次验证（待核实）。
  - 反诈通报中「引导受害人安装向日葵/ToDesk/TeamViewer 并交出控制码」是典型剧本；2025-12、2026 年多地仍持续通报此类案件。
- **对开发者的不足**：终端功能锁在企业版，个人版只能整屏远控；免费版帧率与转发策略限制大；同样存在被企业安全策略禁用的问题。

### 1.3 TeamViewer（德国）

- **定位**：全球商用远控/远程支持标杆，企业版 Tensor。
- **定价（2026）**：Remote Access 约 $24.90/月起，Business 约 $50.90/月，Premium 约 $112.90/月，Corporate 约 $229.90/月，均按年计费；个人非商用免费。（第三方汇总口径，待核实）
- **免费版痛点**：自动「商业用途检测」误判严重——帮同事、连公司电脑、自由职业都可能被判定，随后会话被限时/强制断开，申诉需提交「私人使用声明」并人工审核，大量用户反映被驳回。这是用户流失与差评的首要来源。
- **传输架构**：TeamViewer 全球路由服务器 + P2P；无自建（Tensor 也是云托管）。国内节点少，2025 年仍有「大陆设备无法国际连接」的社区反馈。
- **安全事件**：
  - 2024-06-26 内部企业 IT 环境被 APT29（俄罗斯 SVR 关联）入侵，官方称产品环境与客户数据未受影响（公告 TV-2024-1005）。
  - CVE-2025-0065（CVSS 7.8，Windows 本地提权，15.62 修复）、CVE-2025-36537（15.67 修复）。
- **对开发者的不足**：价格面向企业 IT，个人开发者用免费版会被商业检测「误伤」；国内连接质量不稳；手机端仍是整屏触控操作。

### 1.4 网易 UU远程

- **定位**：网易 2024 年入场，2025–2026 年以「真 4K、真免费、无会员等级」做差异化，把 4K 144 帧、手柄、隐私屏、多屏等全部免费开放。
- **面向开发者的新功能（2026）**：
  - **终端**：2026-05 前后上线「零配置终端命令行」，被控设备在线即可直接进入其命令行，无需 SSH 配置或内网穿透。
  - **端口映射**：2026-04/05 上线，选一台在线设备作跳板，把远端 MySQL/Redis/NAS/Web 服务的 TCP 端口映射到本地，纯图形化操作。
- **传输架构**：P2P + 网易中转；不可自建。
- **不足与口碑**：早期评测指出「实际帧率难以超过 20 帧」「持续连接不稳定、易掉线」「macOS/TV 端仍在开发」「安全防护评分低」（第三方评测口径，部分可能已随版本更新变化，待核实）；免费模式的可持续性（是否会走 ToDesk 的老路）是用户最大的顾虑。
- **意义**：证明「远控厂商正在把终端、端口映射当成开发者卖点」，但其终端仍是「单机单终端」，没有会话持久化、AI 事件通知、多机聚合。

### 1.5 RustDesk（开源，AGPL-3.0）

- **定位**：开源、可自建的 TeamViewer 替代，稳定版 1.4.6（2026-03-05），之后仍在快速迭代（1.4.7–1.4.9）。
- **定价**：客户端与社区版服务器免费；Server Pro 按年授权自建：Individual 约 $9.90/月（$118.80/年，1 账号 20 设备），Basic 约 $19.90/月（10 用户 100 设备）。（待核实）
- **传输架构**：hbbs（ID/信令）+ hbbr（中转），端到端加密；自建是主流用法，尤其在中国大陆。
- **终端能力**：1.4.1（2025-08）加入 Terminal 模式；1.4.5 为移动端加终端浮动快捷键；1.4.9 支持 shell 退出自动关闭终端标签。
- **安全与滥用**：
  - 因诈骗与僵尸网络滥用，公共服务器对控制端强制登录（通过第三方身份提供商，免费）；官方主动从 Google Play 下架。
  - 国内报道称 RustDesk 因涉诈暂停了大陆公共节点访问，通过公共服务器连接大陆主机会收到禁止提示（自媒体口径，待核实）；由此催生大量「NAS/VPS 自建 RustDesk」教程。
  - 官方 FAQ 明确：自建不等于免疫诈骗，骗子同样可以自建服务器 + 分发定制客户端。
- **对开发者的不足**：需要自己维护 VPS/域名/证书/放行端口；Pro 功能（Web 控制台、2FA、OIDC、地址簿）要付费；终端模式仍是「一台机器一个 shell」，没有 AI 会话语义。

### 1.6 AnyDesk（德国）

- **定价**：Solo / Standard / Advanced / Ultimate 四档，约 $22.90–$79.90/月（年付）；2024→2026 年 Solo 涨约 94%、Standard 涨约 118%、Advanced 涨约 40%；个人非商用免费。（待核实）
- **传输架构**：P2P + AnyDesk 中转；企业可购买 On-Premises 私有部署。
- **安全事件与口碑**：
  - 2024-01 生产系统被入侵，源码与代码签名私钥泄露，证书吊销并换发；事件在 2024-02-02 才公开（距入侵约 6 周）；随后有 18,000+ 条 AnyDesk 凭据在暗网出售的报道（待核实）。
  - 是「技术支持诈骗」中被点名最多的工具之一，FBI 曾专门发布警示；AnyDesk 2023 年成立反诈工作组。
  - 2022 年 FBI/CISA 通报 AvosLocker 勒索团伙利用 AnyDesk 作为远程管理工具。
- **对开发者的不足**：价格面向 MSP/企业；因涉诈名声在企业终端管理（EDR/MDM）中经常被直接拉黑；无终端模式。

### 1.7 Parsec（Unity）

- **定位**：面向游戏与创意行业的低延迟远控，Unity 2021 年以 3.2 亿美元收购。
- **定价**：个人免费；Warp 约 $9.99/月（4:4:4 色彩、多显示器等）；Teams 约 $30/用户/月；Enterprise 可自建高性能中转（HPR）。（待核实）
- **传输架构**：自研 BUD（UDP）协议 P2P 优先，STUN 打洞，官方称成功率约 97%；失败回落 Unity 托管中转。
- **平台限制**：被控端仅 Windows / macOS（macOS 需 Metal）；Linux 只能作控制端；**无 iOS 客户端**（含 Safari 网页端）。
- **对开发者的不足**：依赖 GPU 硬件编码，无头/无显卡机器体验差；免费版限个人用途，工作场景要 Teams；iPhone 用户无法使用；无终端模式；对国内网络的 P2P 打洞质量无公开数据（待核实）。

### 1.8 Chrome Remote Desktop（Google）

- **定位**：免费、基于 Google 账号的轻量远控，WebRTC 传输 + Google 中转。
- **限制**：
  - 「远程支持」模式（临时访问码）每 30 分钟要求被控端确认，否则断开；
  - 无内建聊天、多会话、打印等企业功能；文件传输平台差异大；
  - 手机端缺 Command / Control / Option 等修饰键，快捷键几乎不可用；
  - Curtain（幕帘）模式在部分环境会导致会话立即断开；
  - 企业可通过 Chrome 策略整体禁用；
  - **中国大陆无法直接使用**（Google 服务被封）。
- **对开发者的不足**：国内用户基本排除；手机端修饰键缺失对终端操作是致命的；无终端模式。

### 1.9 Windows 远程桌面 / RDP（Microsoft）

- **定位**：Windows 内置，被控端需 Pro 及以上（Home 版无法作为 RDP 主机，社区常用 RDP Wrapper 破解，有合规与安全风险）。
- **传输架构**：直连 TCP/UDP 3389 或 RD Gateway；跨公网需要 VPN / 内网穿透 / 端口映射，否则直接暴露。
- **安全事件（2025）**：CVE-2025-32710（RDS 未认证 RCE，CVSS 8.1，2025-06 修复）、CVE-2025-48817（RDP 客户端连接恶意服务器触发 RCE，2025-07 修复）、CVE-2025-53722（RDS DoS）。公网暴露的 RDP 一直是勒索软件初始入口（BlueKeep 先例）。
- **体验痛点（和终端使用强相关）**：
  - 断开连接后被控端自动回到锁屏，桌面 GUI 自动化会失效，需要 `tscon ... /dest:console` 之类技巧，且在新版 Windows 10/11 上不稳定；
  - 无显示器/关显示器后 Windows 重建显示环境导致黑屏，需要「显卡欺骗器」（dummy plug）；
  - 老显卡驱动 + WDDM 远程显示驱动导致黑屏，需要改组策略；
  - 被控端休眠/待机即断连；
  - 手机端 RD Client 是整屏触控 + 虚拟键盘，终端操作困难。
- **对开发者的优点**：文本清晰、带宽低于视频类远控；但仍然是「整屏」，且需要自己解决公网可达与安全加固。

---

## 2. 横向对比表

| 产品 | 定位 | 个人定价（2025–2026，待核实） | 商业定价（待核实） | 传输架构 | 可自建 | 终端/CLI 能力 | 手机端终端体验 | 国内可用性 | 2024–2026 风险事件 / 口碑 |
|---|---|---|---|---|---|---|---|---|---|
| ToDesk | 国产通用远控 | 免费（每月 200 次/80 h，后不再公布）；专业版 ¥158/年 | 企业版按席位，私有化需询价 | P2P + 自有中转 | 否（企业私有化） | 被控端 CMD（Win/Mac/Linux） | 手机端无 CMD | 好（节点多） | 免费额度三连砍；用户协议封号权；企业禁装；涉诈剧本工具 |
| 向日葵 | 国产老牌远控 | 免费（30 FPS/1 会话/限速）；瓜子 ¥128/年；超级 ¥298/年 | 精英版及以上；私有化需询价 | P2P + 自有中转，付费强制转发 | 否（企业私有化） | 远程 CMD/SSH（精英版+） | 手机可发起 CMD/SSH（需付费版） | 好 | 2022 RCE（CNVD-2022-10270）；涉诈剧本工具；企业禁装 |
| TeamViewer | 全球商用远控 | 个人免费但「商业用途检测」误判频繁 | $24.90–$229.90/月（年付） | 全球路由服务器 + P2P | 否 | 无 | 整屏触控 | 一般（国内节点少，国际连接问题） | 2024-06 APT29 入侵企业 IT；CVE-2025-0065 / 36537 |
| 网易 UU远程 | 网易免费远控 | 全功能免费（4K144） | 无 | P2P + 网易中转 | 否 | 2026 零配置终端 + 端口映射 | 待核实（终端为新功能） | 好 | 稳定性/掉线投诉；免费可持续性存疑 |
| RustDesk | 开源自建远控 | 免费（公共服务器需登录） | Server Pro $118.80/年起 | 自建 hbbs/hbbr | **是** | Terminal 模式（1.4.1+） | 1.4.5+ 浮动快捷键 | 公共节点受限，自建为主 | 因诈骗下架 Google Play、强制登录、国内公共节点暂停 |
| AnyDesk | 商用远控/MSP | 个人非商用免费 | 约 $22.90–$79.90/月，两年涨 40–118% | P2P + 自有中转 | 企业 On-Prem | 无 | 整屏触控 | 一般 | 2024 源码与签名证书泄露；FBI 反诈点名 |
| Parsec | 游戏/创意低延迟 | 免费；Warp $9.99/月 | Teams $30/用户/月；Enterprise HPR 自建中转 | BUD/UDP P2P + Unity 中转 | 企业 HPR | 无 | **无 iOS 客户端** | 待核实 | 依赖 GPU；Linux 不能作被控 |
| Chrome Remote Desktop | 免费轻量远控 | 免费 | 无 | WebRTC P2P + Google 中转 | 否 | 无 | 缺修饰键，快捷键不可用 | **不可用** | 30 分钟确认；Curtain 模式断连 |
| Windows RDP | 系统内置 | 随 Pro 版 | 随 Windows/RDS CAL | 直连 3389 / RD Gateway | **是（自管）** | 无（但文本清晰） | RD Client 整屏触控 | 需自行解决公网 | CVE-2025-32710 / 48817 / 53722；断开即锁屏；黑屏/无显示器问题 |

---

## 3. 专项：用 GUI 远程操作 Claude Code / Codex 的具体痛苦

以下按维度归纳，证据来自上文各产品的公开限制，以及 2026 年开发者「用手机跑 Claude Code」的实践文章（Zilliz、dev.to、MobileCLI、Tom Girou 等，均指出：SSH 方案打字「痛苦地慢」、屏幕太小看终端输出费劲、SSH 没有推送通知、断线要靠 tmux 兜底）。

| 维度 | GUI 远程的表现 | 对 AI CLI 场景的后果 |
|---|---|---|
| **手机看终端** | 手机屏幕上显示一块 1080p/2K 桌面，终端字体只有几像素高；免费档 30 FPS + 压缩使细小文字发糊；需要不断双指缩放。 | Claude Code 的 diff、tool 调用日志、权限提示都是密集文本，远控画面根本读不清；等于「用望远镜读报纸」。 |
| **输入效率** | 手机虚拟键盘缺 Ctrl/Esc/Tab/方向键（Chrome Remote Desktop 甚至缺修饰键）；触控板模式移动光标再点击；中文输入法与远端输入法双重冲突。 | Claude Code 的 `Esc` 中断、`Shift+Tab` 切模式、`/` 命令、方向键选权限选项，在触屏远控里每一步都要开快捷面板；长 prompt 打字体验极差。 |
| **通知** | 远控只有「连上/断开」通知，不知道远端终端发生了什么。 | Claude 等你批准一个 `rm`/`git push`，你在手机上毫无感知，只能定时连进去「看一眼」——ToDesk 免费版按连接次数计费，这种「看一眼」模式最快耗光额度。 |
| **断线与会话** | 远控断线不影响远端进程，但 RDP 断开会锁屏；机器休眠即断连；TeamViewer 商业检测会强制掐断；Chrome 远程支持模式 30 分钟要确认。 | 若 Claude Code 跑在远控会话里打开的终端窗口，重连后要重新找窗口、翻滚动缓冲；若是 RDP 会话注销则进程直接结束；没有 tmux 的用户会丢会话。 |
| **显卡 / 黑屏** | 无显示器或关显示器后 Windows 重建显示环境 → 黑屏，要买 dummy plug；老驱动 + WDDM 远程驱动黑屏；Parsec 强依赖 GPU 编码。 | 一台专门跑 AI Agent 的无头 Windows 主机，为了「看几行文字」要为 GPU 编码和显示器欺骗器操心，属于本末倒置。 |
| **登录 / 锁屏** | 远控必须先在手机上输 Windows 密码/PIN 解锁；RDP 断开自动锁屏；企业策略强制锁屏时长。 | 每次「看一眼」都要解锁；tscon 之类保持不锁屏的技巧在新版 Windows 不稳定且降低安全性。 |
| **带宽 / 延迟** | 1080p60 桌面视频通常几 Mbps 到 30 Mbps（2560×1600 全屏视频约 30 Mbps，社区实测，待核实）；免费档中转节点拥堵、帧率 30。 | 移动网络下卡顿掉帧；而一个终端会话的文本流只需 KB/s 级别，事件（完成/待批准）只需几十字节。 |
| **多机 / 多会话管理** | 设备列表 + 逐台连入，一次只能看一台机器的一块屏；付费才有多会话（向日葵 3/5 会话）。 | 开发者常见形态是「3 台机器 × 每台 2–4 个 Claude/Codex 会话」，远控无法给出「所有会话状态一览」。 |
| **无人值守 / 保活** | 需要关闭休眠、保持登录、自动开机脚本；远程开机依赖 WOL + BIOS 设置。 | AI 任务动辄跑 30 分钟以上，任何一次休眠/锁屏/更新重启都可能让任务中断且无人知晓。 |
| **公司安全策略** | 向日葵/ToDesk/AnyDesk 常被企业 EDR 拉黑或发文禁装；RDP 暴露公网被安全部门禁止；TeamViewer/AnyDesk 自身出过供应链级事故。 | 开发者在公司电脑上装远控＝违规；而 terminalX 若以「出站长连接 + 终端级最小权限 + 审计日志」实现，更容易通过安全评审。 |

**结论**：GUI 远程解决的是「看到桌面」，而 AI CLI 远程办公真正需要的是「看到会话状态、收到需要我介入的事件、用最少的按键给出决定、断线后会话仍在」。这四项在所有传统远控中都不是一等公民。

---

## 4. 行业动态与竞品边界（2025–2026）

- **远控厂商补终端**：RustDesk 1.4.1 Terminal（2025-08）→ 1.4.5 移动端终端浮动键；网易 UU远程 2026 年上线零配置终端与端口映射；向日葵 CMD/SSH 仍锁在精英版；ToDesk 手机端无 CMD。方向一致，但都停留在「单机 shell」。
- **Anthropic 原生 Remote Control**（2026-02-24/25 发布，2026-08-22 可靠性升级，Claude 手机 App 的 Code 标签显示设备卡片）：
  - 免费随 Pro/Max/Team/Enterprise（Team/Enterprise 默认关闭需管理员开启）；**不支持 API Key**；**不支持 Bedrock / Vertex / Foundry / 自定义 `ANTHROPIC_BASE_URL`（LLM 网关、中转代理）**——这直接排除了国内大量用中转 API 的用户。
  - 本地 `claude` 进程必须持续运行，关闭终端即离线；服务器模式遇 403 约 10 分钟后退出；心跳失败约 30 分钟断开；官方建议用 tmux/screen 保活。
  - 每个交互进程只能有一个远程会话（server 模式可多会话）；`/plugin`、`/resume` 等命令只能在本地终端执行。
  - 对话记录存储在 Anthropic 服务器；Team/Enterprise 可开 Trusted Devices（18 小时内重新验证）。
  - Windows 侧 2026-05 仍有「PC 重启后 owner 进程消失、Desktop 无法恢复远程会话」的 issue（#60790）。
  - 只覆盖 Claude Code，不覆盖 Codex CLI / Grok CLI / MiniMax Code。
- **反诈与合规压力持续**：2025-12 与 2026 年国内多地仍在通报「冒充客服诱导开启屏幕共享 / 安装远控 App」案件；ToDesk 以 24 小时新设备保护、支付 App 黑屏、二次验证应对；RustDesk 以强制登录、下架应用商店应对。任何「远程控制」产品在国内都要预设反诈合规设计（新设备冷却期、敏感操作二次确认、审计日志）。
- **免费模型不可持续**：ToDesk 两年内三次砍免费额度并引发大规模「求平替」讨论；AnyDesk 两年涨价近一倍；UU远程以补贴换市场。开发者对「免费突然收费」高度敏感，开源/自建成为避险选项（RustDesk 自建教程在 2025 年大量涌现）。

---

## 5. 对 terminalX 的启示与机会点

1. **把「会话」而不是「屏幕」作为一等对象**：Web 页面聚合多台机器 × 多个 AI CLI 会话的状态（运行中 / 等待批准 / 完成 / 出错），点进去才是终端画面。这是所有远控和 Anthropic Remote Control 都没有做的「控制塔」视角。
2. **事件通知与一键决策**：把 Claude Code / Codex 的权限询问、任务完成、错误变成推送通知；手机端提供「允许 / 拒绝 / 输入一句话」的大按钮，而不是在虚拟键盘上找方向键。
3. **被控端做进程托管与保活**：被控端 Agent 自己负责在 tmux/ConPTY 里拉起 CLI、断线重连后续接缓冲区、检测机器休眠/锁屏并告警，用户无需懂 tmux。
4. **自建中转 + 出站长连接**：对齐 RustDesk 的自建优势（数据不经第三方、国内可达），同时像 Quick Assist 一样只需被控端出站连接、不开入站端口，规避 RDP 暴露与公司防火墙问题；提供 Docker 一键部署。
5. **移动端为终端专门设计的输入层**：常驻 Esc/Tab/Ctrl/方向键/斜杠命令条、命令历史、语音转文字输入 prompt、粘贴板同步；文本渲染用真正的 xterm 而非视频，任意缩放不糊。
6. **多 CLI 厂商适配**：Claude Code、Codex CLI、Grok CLI、MiniMax Code 的会话检测、输出解析与「等待输入」识别做成插件；国内用中转 API 的用户也能用（Remote Control 的空白区）。
7. **反诈与企业合规内建**：新设备冷却期、敏感命令二次确认、完整审计日志、只读观察者角色、可按机器/目录限制权限——把「远程终端」从被禁装名单里区分出来。
8. **定价透明、承诺可自建**：明确「自建永远免费/开源核心」，付费只卖托管中转与团队功能，避免 ToDesk 式信任崩塌。

---

## 6. 来源链接

### ToDesk
- [远控工具ToDesk免费用户权益再缩减！每月最多连接80小时（新浪科技，2025-04-09）](https://finance.sina.com.cn/tech/roll/2025-04-09/doc-inesqerr2866698.shtml)
- [ToDesk免费版再受限：每月仅限200连80小时（搜狐）](https://www.sohu.com/a/881803575_362225)
- [Remote Control Tool ToDesk Introduces Monthly Connection Limits（Landian News）](https://landian.news/article/2041.html)
- [从宣传免费到多次限制免费时长，远控软件ToDesk还值得用么？（CSDN）](https://blog.csdn.net/TechVoyager/article/details/146258041)
- [时隔仅一月，ToDesk再发公告削减免费权益，每月时长不再透明（知乎）](https://zhuanlan.zhihu.com/p/1891076947829843111)
- [Todesk吃相是否太难看了? 免费用户到底还给用么?（LINUX DO）](https://linux.do/t/topic/1126796?tl=zh_CN)
- [ToDesk 周年庆定价（极客公园）](https://www.geekpark.net/news/347213)
- [ToDesk 免费版和专业版区别详解（ToDeskGuide）](https://todeskguide.com/blog/todesk-free-vs-paid/)
- [ToDesk 定价页（官网）](https://www.todesk.com/pricing.html?product=remote&type=individual)
- [ToDesk 用户协议（官网）](https://www.todesk.com/licence.html)
- [谨防远程控制沦为诈骗工具，ToDesk安全远控防诈提醒（CSDN）](https://blog.csdn.net/ToDesk_Official/article/details/142596322)
- [公司不让用ToDesk了，这个年轻的远控软件真的安全么？（腾讯云开发者社区）](https://cloud.tencent.com/developer/news/929787)
- [关于禁用向日葵、Todesk等远程软件的通知（海贝达）](https://edudigital123.com/article/28080/)
- [手机远程控制横测ToDesk、向日葵、TeamViewer（新浪众测）](https://zhongce.sina.com.cn/iframe/article/view/173614/)

### 向日葵
- [向日葵收费和免费有啥区别? 向日葵收费标准（IDCTalk）](https://www.idctalk.com/16340.html)
- [远控软件如何规避收费"套路"？向日葵和Todesk哪个会员更实惠？（知乎）](https://zhuanlan.zhihu.com/p/710391640)
- [手机/平板如何远程CMD或SSH（贝锐向日葵官网）](https://sunlogin.oray.com/news/17788.html)
- [电脑如何远程CMD/SSH（贝锐向日葵官网）](https://sunlogin.oray.com/news/17617.html)
- [向日葵RCE复现 CNVD-2022-10270 / CNVD-2022-03672（腾讯云开发者社区）](https://cloud.tencent.com/developer/article/2261971)
- [公司不让用向日葵，我找到了替代方案（CSDN）](https://blog.csdn.net/zhangxianhau/article/details/156150478)
- [远控安全进阶之战：TeamViewer/ToDesk/向日葵设备安全策略对比（CSDN）](https://blog.csdn.net/Morse_Chen/article/details/148176728)

### TeamViewer
- [TeamViewer Pricing 2026: 5 Plans from $24.90–$229.90/month（CostBench）](https://costbench.com/software/remote-desktop/teamviewer/)
- [How To Fix TeamViewer Commercial Use Detected in 2026?（HelpWire）](https://www.helpwire.app/blog/teamviewer-commercial-use-detected/)
- [TeamViewer's corporate network was breached in alleged APT hack（BleepingComputer）](https://www.bleepingcomputer.com/news/security/teamviewers-corporate-network-was-breached-in-alleged-apt-hack/)
- [TeamViewer Security Bulletin TV-2024-1005](https://www.teamviewer.com/en-us/resources/trust-center/security-bulletins/tv-2024-1005/)
- [CVE-2025-0065: TeamViewer Patches Privilege Escalation Vulnerability（SecurityOnline）](https://securityonline.info/cve-2025-0065-teamviewer-patches-privilege-escalation-vulnerability-in-windows-clients/)
- [CVE-2025-36537: TeamViewer Remote Management Flaw（The Cyber Express）](https://thecyberexpress.com/cve-2025-36537-teamviewer-remote-management/)
- [无法进行国际连接（TeamViewer 中文社区）](https://community.teamviewer.com/Chinese/discussion/141920/)

### 网易 UU远程
- [网易UU远程端口映射功能上线（UU远程官方博客，2026-05-07）](https://uuyc.163.com/blog/20260507-dkys.html)
- [UU远程端口映射教程（UU远程帮助中心，2026-04-23）](https://uuyc.163.com/help/20260423/40220_1297526.html)
- [2026 年五月新体验：告别 SSH，UU 远程终端一键连通设备（CSDN）](https://blog.csdn.net/lbbxmx111/article/details/161367250)
- [2026年6月最新远程软件横评：终端+端口映射+隐私屏实测（CSDN）](https://blog.csdn.net/a_hong_sen/article/details/153797066)
- [告别frp和ngrok：UU远程端口映射（腾讯云开发者社区）](https://cloud.tencent.com/developer/article/2663788)
- [别再被"轻便"骗了：网易UU远程深度使用数月，我受影响好几次（知乎）](https://zhuanlan.zhihu.com/p/2073395510615061462)
- [2025年远程控制软件横评：UU远程、ToDesk、向日葵（博客园）](https://www.cnblogs.com/gccbuaa/p/19218443)

### RustDesk
- [RustDesk and Remote Access Scams: What We Are Doing（RustDesk 官方博客）](https://rustdesk.com/blog/rustdesk-and-remote-access-scams/)
- [RustDesk FAQ（GitHub Wiki）](https://github.com/rustdesk/rustdesk/wiki/FAQ)
- [RustDesk 1.4.1 Remote Desktop Adds Terminal and Stylus Support（Linuxiac）](https://linuxiac.com/rustdesk-1-4-1-remote-desktop-adds-terminal-and-stylus-support/)
- [RustDesk Releases（GitHub）](https://github.com/rustdesk/rustdesk/releases)
- [RustDesk Pricing（官网）](https://rustdesk.com/pricing/)
- [RustDesk涉欺诈关闭国内访问，用NAS部署自建节点（什么值得买）](https://post.smzdm.com/p/ag56xz76/)
- [RustDesk 自建服务器部署和使用教程（云原生实验室）](https://icloudnative.io/posts/how-to-set-up-rustdesk-server/)

### AnyDesk
- [AnyDesk Acknowledges Breach, Implements Security Measures（Trend Micro，2024-02）](https://news.trendmicro.com/2024/02/10/anydesk-data-breach/)
- [AnyDesk code signing certificate compromised and revoked（Quorum Cyber）](https://www.quorumcyber.com/threat-intelligence/anydesk-code-signing-certificate-compromised-and-revoked/)
- [AnyDesk Pricing Changes（PriceTimeline）](https://pricetimeline.com/data/price/anydesk)
- [AnyDesk Pricing - Plans, Costs, and Alternatives（Splashtop）](https://www.splashtop.com/blog/anydesk-pricing-comparison)
- [FBI Warns Public to Beware of Tech Support Scammers Using Remote Desktop Software（FBI）](https://www.fbi.gov/contact-us/field-offices/boston/news/press-releases/fbi-warns-public-to-beware-of-tech-support-scammers-targeting-financial-accounts-using-remote-desktop-software)
- [AnyDesk Scams: The Good, the Bad, and the Risky（Guardio）](https://guard.io/blog/anydesk-scams-the-good-the-bad-and-the-risky)

### Parsec
- [Parsec Warp（官网）](https://parsec.app/warp)
- [Parsec Pricing 2026（ToolRadar）](https://toolradar.com/tools/parsec/pricing)
- [Hardware and Software Compatibility（Parsec 支持中心）](https://support.parsec.app/hc/en-us/articles/32381568346644-Hardware-and-Software-Compatibility)
- [Configure Parsec Relay Server（Parsec 支持中心）](https://support.parsec.app/hc/en-us/articles/32381057878292-Configure-Parsec-Relay-Server)
- [Unity to acquire Parsec（TechCrunch，2021）](https://techcrunch.com/2021/08/10/unity-to-acquire-parsec-in-its-biggest-acquisition-to-date/)

### Chrome Remote Desktop
- [2026 Full Review: Chrome Remote Desktop Limitations（AnyViewer）](https://www.anyviewer.com/how-to/chrome-remote-desktop-limitations-2578.html)
- [Can the 30 min limit per session be expanded?（Google Chrome Community）](https://support.google.com/chrome/thread/125308787/)
- [Remote session terminates immediately with curtain mode enabled（Chrome Enterprise Community）](https://support.google.com/chrome/a/thread/173044342/)
- [3 Ways to Use Chrome Remote Desktop Keyboard Shortcuts（AirDroid）](https://www.airdroid.com/remote-support/chrome-remote-desktop-keyboard-shortcuts/)
- [Guidance for those working from China（Imperial College London）](https://www.imperial.ac.uk/admin-services/ict/self-service/connect-communicate/remote-access/censorship/guidance-for-those-working-from-china/)

### Windows RDP
- [Serious vulnerability found in Microsoft Remote Desktop Client CVE-2025-48817（Devolutions）](https://devolutions.net/blog/tech-news-serious-vulnerability-found-in-microsoft-remote-desktop-client-cve-2025-48817/)
- [Windows Remote Desktop Services Vulnerability Allows RCE（CyberSecurityNews）](https://cybersecuritynews.com/windows-remote-desktop-services-rce-vulnerability/)
- [Remote Desktop Connection Without Locking Remote Computer Session on Disconnect（Tech Journey）](https://techjourney.net/remote-desktop-connection-without-locking-remote-computer-session-on-disconnect/)
- [关闭显示器后无法远程控制或黑屏怎么办？RDP 疑难杂症（知乎）](https://zhuanlan.zhihu.com/p/2042902843963789451)
- [微软远程桌面黑屏（Microsoft Q&A）](https://learn.microsoft.com/zh-cn/answers/questions/2198214/question-2198214)
- [windows远程桌面断开连接锁屏问题处理（CSDN）](https://blog.csdn.net/m0_61218662/article/details/147246026)
- [Remote Desktop Windows 11: RDP quirks and free Home（Tenvo）](https://tenvoai.com/blog/remote-desktop-for-windows-11)

### AI CLI 远程办公 / Claude Code Remote Control
- [Continue local sessions from any device with Remote Control（Claude Code Docs）](https://code.claude.com/docs/en/remote-control)
- [Anthropic just released a mobile version of Claude Code called Remote Control（VentureBeat）](https://venturebeat.com/orchestration/anthropic-just-released-a-mobile-version-of-claude-code-called-remote)
- [Code tab: "remote control disconnected" when resuming any session whose CLI owner is gone（GitHub Issue #60790）](https://github.com/anthropics/claude-code/issues/60790)
- [How to control Claude Code from your phone (2026)（explainx.ai）](https://www.explainx.ai/blog/claude-code-mobile-remote-control-phone-guide-2026)
- [3 Ways to Run Claude Code from Your Phone (2026)（Zilliz）](https://zilliz.com/blog/3-easiest-ways-to-use-claude-code-on-your-mobile-phone)
- [Running Claude Code from iPhone via SSH + tmux（DEV Community）](https://dev.to/shimo4228/running-claude-code-from-iphone-via-ssh-tmux-4c10)
- [Claude Code From My Phone: Tailscale, Termius（Tom Girou）](https://tom-girou.dev/blog/claude-code-from-my-phone/)
- [The Complete Guide to Using Claude Code on Your Phone in 2026（MobileCLI）](https://www.mobilecli.app/blog/claude-code-phone-guide)
- [Achieving low latency remote development（Coder）](https://coder.com/blog/achieving-low-latency-remote-development)

### 反诈与行业
- [识破屏幕共享操控骗局，中原消金筑牢金融消保屏障（新华报业网）](https://www.xhby.net/content/s6a1fd2f2e4b0b6e57b5a297c.html)
- [只因用了这种软件，我差点"进局子"（少数派）](https://pwa.sspai.com/post/83022)
- [2025年远程控制软件排行榜：安全性能哪家强？（阿里云开发者社区）](https://developer.aliyun.com/article/1693504)
- [远程桌面优化避坑指南（腾讯云开发者社区）](https://cloud.tencent.com/developer/article/2026653)
