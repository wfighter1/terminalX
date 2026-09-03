# 部署 terminalX 中转（tx-relay）

中转是一个静态编译的单二进制：配对、登录、路由、SQLite 元数据、审计、webhook，以及内置的 Web 控制台。
它**只读 18 字节帧头**，终端内容不落盘。这个目录提供 Docker Compose 一条命令部署（relay + Caddy 自动 HTTPS）。

## 一条命令部署

前置条件：一台有公网 IP 的 Linux 机器，装好 Docker（含 `docker compose`），一个已解析到这台机器的域名，80/443 端口对外开放。

```bash
git clone https://github.com/wfighter1/terminalX.git
cd terminalX/deploy
cp .env.example .env
$EDITOR .env            # 至少改 TX_DOMAIN 和 TX_ADMIN_PASSWORD
docker compose up -d --build
```

构建会在容器里完成：`node:22` 打包 `web/` → 复制到 `internal/webdist/dist` → `golang:1.27` 编译出带控制台的 `tx-relay`。首次构建需要几分钟。

验证：

```bash
docker compose ps                       # relay 与 caddy 都应为 running / healthy
curl -s https://$TX_DOMAIN/healthz      # {"ok":true,"devices_online":0,"clients":0}
```

浏览器打开 `https://<TX_DOMAIN>`，用 `TX_ADMIN_PASSWORD` 登录。

升级：

```bash
git pull
docker compose up -d --build            # 数据在 ./data，升级不丢
```

## 环境变量

`.env` 里的变量给 compose 用；容器内的 `tx-relay` 直接读取 `TX_*` 环境变量（也都有对应的命令行参数）。

| 变量 | 必填 | 默认 | 说明 |
|---|---|---|---|
| `TX_DOMAIN` | 是 | — | 公网域名。Caddy 用它签证书；relay 用 `https://$TX_DOMAIN` 作为 `TX_PUBLIC_URL` 拼通知里的链接 |
| `TX_ADMIN_PASSWORD` | 是 | — | 控制台密码（第一阶段单用户）。错 5 次按来源 IP 锁 15 分钟 |
| `TX_ACME_EMAIL` | 否 | 空 | 证书到期 / 签发失败通知邮箱 |
| `TX_VERSION` | 否 | `dev` | 镜像标签，也写入二进制的版本号 |
| `TX_LOG_LEVEL` | 否 | `info` | `debug` / `info` / `warn` / `error` |
| `TX_ALLOW_ORIGIN` | 否 | 空 | 额外允许连接 `/ws/client` 的来源（逗号分隔）。只有本地用 Vite 开发前端时才需要，生产留空（同源总是允许） |

`tx-relay` 自身还认这些（compose 已固定好，一般不用改）：

| 变量 / 参数 | 默认 | 说明 |
|---|---|---|
| `TX_LISTEN` / `--listen` | `:8080` | 监听地址。compose 里只在内网暴露，由 Caddy 反代 |
| `TX_DATA` / `--data` | `./data` | 数据目录，里面是 `relay.db`（SQLite，WAL 模式） |
| `TX_PUBLIC_URL` / `--public-url` | 空 | 对外基址，webhook 通知里的「打开审批」链接用它 |
| `TX_WEB_DIR` / `--web-dir` | 内置 | 从目录而不是内置资源提供控制台（前端联调用） |

不用 Docker 也可以：`scripts/build.sh` 会产出 `bin/tx-relay`（已内置控制台），然后
`TX_ADMIN_PASSWORD=... ./bin/tx-relay --listen 127.0.0.1:8080 --data /var/lib/terminalx --public-url https://tx.example.com`，
再让任意反代把 443 转到 8080（反代要求见 `Caddyfile` 末尾的注释）。

## 备份 `data` 目录

所有需要保留的状态都在 `deploy/data/`（容器内 `/data`）里的 `relay.db`：

- `devices`：已配对设备、token 的 SHA-256（明文 token 只在 Agent 上）、指纹
- `approvals`、`audit`：审批记录与操作审计（只有元数据，没有终端内容）
- `settings`：webhook 地址
- `pair_codes`：一次性配对码（5 分钟过期，可丢）

登录会话在内存里，重启后需要重新登录；离线设备的会话列表由 Agent 重连时的 `agent.hello` 重建。

SQLite 开着 WAL，直接 `cp` 正在写入的库可能拿到不一致的快照。推荐用 SQLite 自己的在线备份：

```bash
# 热备份（不停机）
docker run --rm -v "$PWD/data:/data" -v "$PWD/backup:/backup" alpine:3.20 \
  sh -c 'apk add -q sqlite && sqlite3 /data/relay.db ".backup /backup/relay-$(date +%F).db"'

# 或者停机冷备份
docker compose stop relay && tar czf backup/data-$(date +%F).tgz data && docker compose start relay
```

恢复：停 relay，把备份文件放回 `data/relay.db`（删掉旧的 `relay.db-wal` / `relay.db-shm`），再启动。
备份文件里有设备 token 的哈希与审计记录，请按敏感数据保管。

Caddy 的证书在命名卷 `caddy_data` 里，丢了会自动重新签发，不必备份。

## 配对第一台设备

配对是「控制台生成一次性码 → 在被控端输入」的单向流程，码走 HTTPS 不经第三方；错 5 次按 IP **和** 按码各锁 15 分钟。

1. 浏览器登录控制台 → 「设备」→「添加设备」，得到一个 8 位码（形如 `A7K3-9QZP`，5 分钟内有效，不区分大小写和连字符）。
2. 在 Windows 被控端（管理员 PowerShell 或普通终端都可以）：

   ```powershell
   .\tx-agent.exe pair --relay https://tx.example.com --code A7K3-9QZP --name "办公室台式机"
   ```

   Agent 用这个码换取长期 token（DPAPI 加密保存在本机），然后打印一个指纹（如 `A7K3-9QZP` 格式的 8 位串）。
3. 回到控制台，设备列表里会出现新设备并显示同样的指纹；**两边指纹一致才算配对成功**（防止有人抢先用了你的码）。不一致就在控制台上「吊销」它。
4. 之后 `tx-agent install` 注册开机自启（计划任务 ONLOGON），或直接 `tx-agent run` 前台跑。设备上线后控制台会显示「在线」，就可以新建会话了。

没有图形界面的机器也一样：`curl -X POST https://tx.example.com/api/pair/redeem -d '{"code":"A7K39QZP","name":"box"}'` 会返回 `device_token`，Agent 配置文件里填上它即可。

吊销设备：控制台「设备」→「吊销」。中转会立刻断开该 Agent 的连接并作废 token，被吊销的设备要重新配对。

## 常见问题

- **证书签不下来**：确认域名已解析、80/443 没被别的服务占用、云厂商安全组放行。看 `docker compose logs caddy`。
- **手机上 WebSocket 一会儿就断**：是中间的反代 / CDN 有空闲超时。用本目录的 Caddyfile（已关闭超时），或者不要在前面再套 CDN 的 WebSocket 代理。
- **忘记密码**：改 `.env` 里的 `TX_ADMIN_PASSWORD` 后 `docker compose up -d`；已配对设备不受影响。
- **审批通知**：控制台「设置」里填一个 webhook URL（ntfy、Bark、企业微信机器人等能收 JSON POST 的都行），字段为 `title / body / device / session / key / url`。
