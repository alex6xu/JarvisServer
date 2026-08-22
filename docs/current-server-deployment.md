# JarvisServer 当前服务器部署实录

本文记录 `2026-08-22` 对当前生产服务器的只读核查结果，以及本机实际采用的发布、
回滚和排障方法。它描述的是现状，不等同于推荐架构；推荐的低权限目录规划见
[`deployment.md`](deployment.md)。

> 安全边界：本文不记录 root 密码、管理员密码、Provider API Key、GitHub Client Secret
> 或 Token 加密密钥。生产秘密只说明保存位置和变量名。

## 1. 环境快照

| 项目 | 当前值 |
|---|---|
| 公网 IP | `198.44.46.91` |
| Web 域名 | `https://web.alexuhui.win` |
| 操作系统 | Ubuntu 24.04 LTS, Linux amd64 |
| 反向代理 | Caddy `2.6.2` |
| Gateway | systemd `jarvis-gateway.service` |
| Gateway 地址 | `127.0.0.1:20128`，仅回环监听 |
| 源码目录 | `/root/JarvisServer` |
| 当前源码提交 | `d39afc252e26396586ea98eb80935cc28c97d371` |
| Gateway 构建工具链 | Go `1.27.0` |
| 主机默认 Go | Go `1.26.5`，构建时由 Go toolchain 选择 1.27 |
| Node.js / npm | Node.js `24.18.1` / npm `11.16.0` |

当前只运行一个 Gateway 实例。SQLite、运行中的 SSE 连接和 Workspace 都在该服务器
本地，因此不能直接横向扩为多个 Gateway 副本。

## 2. 请求拓扑

```mermaid
flowchart LR
    U[Browser] -->|HTTPS 443| C[Caddy]
    C -->|Static files| W[/root/JarvisServer/web/dist]
    C -->|/v1/*, /healthz, /ws*| G[Gateway 127.0.0.1:20128]
    G --> D[(SQLite gateway.db)]
    G --> S[Session files]
    G --> X[Coder workspaces]
    G --> P[LLM and GitHub APIs]
```

外部监听端口为 `22`、`80` 和 `443`。Gateway 的 `20128` 端口只监听
`127.0.0.1`，不会直接暴露到公网。服务器的 UFW 当前处于 inactive 状态，端口限制依赖
云厂商防火墙和各服务自身的监听地址。

请求处理顺序如下：

1. Caddy 在 `80/443` 接收请求，自动管理 TLS 证书。
2. `/v1/*`、`/healthz` 和 `/ws*` 反向代理到 Gateway。
3. 其他路径从 `web/dist` 返回 React 静态文件；未知前端路由回退到 `index.html`。
4. Gateway 处理 Token 认证、Agent Run、Provider 路由、审计和 Workspace。

## 3. 文件和数据位置

| 路径 | 用途 | 当前权限/说明 |
|---|---|---|
| `/root/JarvisServer/build/gateway` | 当前 Gateway 二进制 | `root:root 0755` |
| `/root/JarvisServer/build/gateway.e91b85b.backup` | 上一个 Gateway 回滚文件 | `root:root 0755` |
| `/root/JarvisServer/web/dist` | Caddy 托管的 Web 生产产物 | root 所有，Caddy 有只读 ACL |
| `/root/JarvisServer/web/dist.previous-20260821-0557` | 2026-08-21 前端发布前的回滚产物 | root 所有，Caddy 有只读 ACL |
| `/root/JarvisServer/etc/gateway.server.yaml` | 实际生产配置 | 服务器专用、未跟踪文件 |
| `/root/JarvisServer/data/gateway.env` | systemd 环境变量和秘密 | `root:root 0600` |
| `/root/JarvisServer/data/gateway.db` | 主 SQLite 数据库 | `root:root 0600` |
| `/root/JarvisServer/data/gateway.db-wal` | SQLite WAL | `root:root 0600`，运行时必须视为数据库一部分 |
| `/root/JarvisServer/data/gateway.db-shm` | SQLite 共享内存文件 | `root:root 0600` |
| `/root/JarvisServer/runtime` | Agent 工作目录 | `root:root 0700` |
| `/root/JarvisServer/runtime/.jarvis/sessions` | Agent Session 文件目录 | 当前目录存在 |
| `/root/JarvisServer/workspaces` | Coder 上传或导入的项目 | `root:root 0700` |
| `/root/.jarvis` | root 用户级 Jarvis 数据，例如 memory | `root:root 0700` |
| `/root/.agents` | root 用户级 Skills | `root:root 0700` |
| `/etc/systemd/system/jarvis-gateway.service` | Gateway systemd unit | 系统配置 |
| `/etc/caddy/Caddyfile` | HTTPS、静态文件和反向代理配置 | 系统配置 |
| `/var/lib/caddy/.local/share/caddy` | Caddy 自动签发的证书和状态 | `caddy:caddy 0700` |

`/root` 默认不能被 Caddy 遍历。当前通过 ACL 给 `caddy` 用户增加了对 `/root` 的执行权限，
并给 `web/dist` 增加只读权限：

```text
/root                         caddy: --x
/root/JarvisServer/web/dist   caddy: r-x
```

## 4. Gateway systemd 配置

实际 unit：

```ini
[Unit]
Description=JarvisServer Gateway
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=root
Group=root
WorkingDirectory=/root/JarvisServer
EnvironmentFile=/root/JarvisServer/data/gateway.env
ExecStart=/root/JarvisServer/build/gateway -f /root/JarvisServer/etc/gateway.server.yaml
Restart=on-failure
RestartSec=3
TimeoutStopSec=30
UMask=0077
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ReadWritePaths=/root/JarvisServer/data /root/JarvisServer/runtime /root/JarvisServer/workspaces
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
```

常用命令：

```bash
systemctl status jarvis-gateway.service --no-pager -l
systemctl restart jarvis-gateway.service
systemctl is-active jarvis-gateway.service
journalctl -u jarvis-gateway.service -f
```

该服务当前 `enabled` 且 `active`。它以 root 身份运行，这是现状，不是推荐做法；迁移到独立
`jarvis` 用户见第 11 节。

## 5. Gateway 生产配置

实际文件是 `/root/JarvisServer/etc/gateway.server.yaml`，脱敏后的有效内容为：

```yaml
Name: gateway
Host: 127.0.0.1
Port: 20128
Timeout: 0
Mode: pro

Log:
  Mode: console
  Level: info

Middlewares:
  Timeout: false
  Shedding: false
  Log: false

Agent:
  Cwd: /root/JarvisServer/runtime
  Model: openrouter/free
  Approve: false
  NoTools: false
  NoSkills: false
  AuthMode: token
  WorkspacesRoot: /root/JarvisServer/workspaces
  DatabasePath: /root/JarvisServer/data/gateway.db
  AuditRetentionDays: 30
  AuditMaxBodyBytes: 1048576
  RunTimeoutSeconds: 1800
  AllowPrivateProviderURLs: false
```

`data/gateway.env` 当前定义以下变量，值不得写入 Git 或文档：

```text
JARVIS_ADMIN_PASSWORD
GITHUB_CLIENT_ID
GITHUB_CLIENT_SECRET
GITHUB_REDIRECT_URL
GITHUB_TOKEN_KEY
```

部署 Skill Registry 和股票摘要功能时，需要在该环境文件新增：

```text
JARVIS_SKILLS_DIR=/root/JarvisServer/data/skills
```

并在重启前创建目录：

```bash
install -d -m 0750 /root/JarvisServer/data/skills
```

新闻和社交舆情按需增加 `ANSPIRE_API_KEYS`、`TAVILY_API_KEY`、
`BOCHA_API_KEYS`、`BRAVE_API_KEY` 和 `SOCIAL_SENTIMENT_API_KEY`。这些值不能写入
`gateway.server.yaml`、Skill 正文或 Git。

`JARVIS_ADMIN_PASSWORD` 只应作为首个管理员初始化手段。账号已经写入数据库后，认证以数据库
中的密码哈希为准。`GITHUB_TOKEN_KEY` 用于加密数据库内的 GitHub 用户凭据，必须和数据库
一起备份，否则已有 GitHub 连接将无法解密。

## 6. Caddy 配置

`/etc/caddy/Caddyfile` 中与 Web 应用相关的配置为：

```caddyfile
web.alexuhui.win {
	encode zstd gzip

	@gateway path /v1/* /healthz /ws*
	handle @gateway {
		reverse_proxy 127.0.0.1:20128 {
			flush_interval -1
		}
	}

	handle {
		root * /root/JarvisServer/web/dist
		try_files {path} /index.html
		file_server
	}

	header {
		Strict-Transport-Security "max-age=31536000; includeSubDomains"
		X-Content-Type-Options "nosniff"
		Referrer-Policy "strict-origin-when-cross-origin"
		Content-Security-Policy "default-src 'self'; connect-src 'self'; img-src 'self' data: https:; media-src 'self' blob:; style-src 'self' 'unsafe-inline'; script-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'"
	}
}
```

Caddy 的 `flush_interval -1` 让 Gateway 的 SSE 事件立即转发。修改配置后执行：

```bash
caddy validate --config /etc/caddy/Caddyfile
systemctl reload caddy.service
curl --fail https://web.alexuhui.win/healthz
```

完整 Caddyfile 还包含 `gateway.alexuhui.win -> localhost:8787`。当前没有进程监听 `8787`，
该域名会返回 `502`；它不属于 `web.alexuhui.win` 的 JarvisServer 请求链路。

## 7. SQLite 数据库

数据库路径由 `Agent.DatabasePath` 显式指定为：

```text
/root/JarvisServer/data/gateway.db
```

Gateway 启动时启用：

```sql
PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;
PRAGMA busy_timeout = 5000;
```

因此运行中可能同时存在 `gateway.db`、`gateway.db-wal` 和 `gateway.db-shm`。不能在服务运行时
只复制 `gateway.db`，否则备份可能缺少 WAL 中尚未 checkpoint 的事务。

数据库包含敏感信息，主要包括：

- `accounts`、`auth_tokens`：账号密码哈希和 Token 哈希。
- `providers`、`provider_endpoints`、`provider_models`：Provider 配置和 API Key。
- `sessions`、`session_entries`：持久化会话和消息。
- `runs`、`run_events`、`run_attempts`、`run_checkpoints`：Agent Run 状态与事件。
- `chat_exchanges`、`provider_exchanges`：Chat 和 Provider 请求审计，可能包含正文。
- `workspace_metadata`：Workspace 名称、来源和账号归属。
- `route_profiles`、`route_policies`、`route_policy_versions`：模型路由配置。
- `github_credentials`：使用 `GITHUB_TOKEN_KEY` 加密的 GitHub Token。
- `skills`、`account_skills`：Skill 修订、全局状态和账号启停，不保存 Skill 正文。
- `watchlist_items`：账号服务端自选股。
- `notification_deliveries`：通知投递状态和幂等键，不保存渠道 Secret。
- `schema_migrations`：数据库迁移版本。

当前服务器没有安装 `sqlite3`，也没有发现 Jarvis 数据库的 cron 或 systemd 定时备份。建议先安装
`sqlite3`，再使用在线备份：

```bash
apt-get update
apt-get install -y sqlite3
install -d -m 0700 /root/JarvisServer/backups
sqlite3 /root/JarvisServer/data/gateway.db \
  ".backup '/root/JarvisServer/backups/gateway-YYYYMMDD-HHMMSS.db'"
chmod 0600 /root/JarvisServer/backups/gateway-*.db
```

数据库备份之外，还必须备份：

- `/root/JarvisServer/data/gateway.env`
- `/root/JarvisServer/runtime/.jarvis/sessions`
- `/root/JarvisServer/workspaces`
- `/root/.jarvis` 和 `/root/.agents`
- `/root/JarvisServer/etc/gateway.server.yaml`

备份必须复制到服务器之外，并加密保存。

## 8. 日志和排障

Gateway 使用 console 日志，由 journald 收集；Caddy 同样写入 journald。当前 journal 总占用约
`128 MiB`。

```bash
# Gateway 实时日志
journalctl -u jarvis-gateway.service -f

# 最近 24 小时请求
journalctl -u jarvis-gateway.service --since '24 hours ago' --no-pager

# Workspace 上传
journalctl -u jarvis-gateway.service --since '24 hours ago' \
  --grep='workspace upload' --no-pager

# HTTP 413 和请求体限制
journalctl -u jarvis-gateway.service --since '24 hours ago' --no-pager \
  | grep -E '413|request entity too large'

# Caddy 代理和证书日志
journalctl -u caddy.service --since '24 hours ago' --no-pager
```

健康检查：

```bash
curl --fail http://127.0.0.1:20128/healthz
curl --fail https://web.alexuhui.win/healthz
ss -lntp
```

## 9. 当前发布流程

服务器工作树包含生产配置和数据目录。发布前必须先检查状态，不能用 `git reset --hard` 或
覆盖服务器专用文件：

```bash
cd /root/JarvisServer
git status --short
git log -1 --oneline
```

当前工作树中已知的服务器专用变更包括：

```text
M  etc/gateway.yaml
?? data/
?? etc/gateway.server.yaml
?? etc/gateway.yaml.1
?? jarvisserver
```

### 9.1 Gateway

```bash
cd /root/JarvisServer

go test -buildvcs=false ./internal/gateway ./cmd/gateway
go build -buildvcs=false -trimpath -ldflags='-s -w' \
  -o build/gateway.next ./cmd/gateway
go version -m build/gateway.next

cp -p build/gateway build/gateway.previous
systemctl stop jarvis-gateway.service
install -m 0755 build/gateway.next build/gateway
systemctl start jarvis-gateway.service

systemctl status jarvis-gateway.service --no-pager -l
curl --fail http://127.0.0.1:20128/healthz
curl --fail https://web.alexuhui.win/healthz
```

不要只更新源码而不重建、重启 Gateway。2026-08-21 的 Workspace `413` 故障就是因为源码已经
包含上传路由上限修复，但运行二进制仍来自旧提交。

### 9.2 Web

```bash
cd /root/JarvisServer/web
npm ci
npm test
npm run build

caddy validate --config /etc/caddy/Caddyfile
systemctl reload caddy.service
curl --fail https://web.alexuhui.win/
```

`npm run build` 会直接重建 `web/dist`。为避免构建失败时破坏线上文件，更稳妥的方式是在临时目录
或 CI 中构建，再原子替换 `dist`。

2026-08-21 曾因 `web/dist` 早于 Workspace 上传过滤源码，导致 `.github` 隐藏目录进入 ZIP 后
被 Gateway 返回 `400`。当前前端已于 `23:23 UTC` 重新构建并原子发布，旧产物保存在
`web/dist.previous-20260821-0557`。发布后的入口 chunk 是 `index-CbYsX0l6.js`，Workspace 上传
chunk 是 `workspaceUpload-BHj_yJ9f.js`。

### 9.3 回滚 Gateway

当前可回滚文件为：

```text
/root/JarvisServer/build/gateway.e91b85b.backup
```

回滚命令：

```bash
cd /root/JarvisServer
systemctl stop jarvis-gateway.service
cp -p build/gateway build/gateway.failed
install -m 0755 build/gateway.e91b85b.backup build/gateway
systemctl start jarvis-gateway.service
curl --fail http://127.0.0.1:20128/healthz
```

旧二进制可能与新数据库迁移不兼容。正式回滚必须同时评估数据库版本，必要时恢复发布前数据库。
此外，`gateway.e91b85b.backup` 本身包含已知的 `1 MiB` 上传限制问题，只适合紧急恢复服务，
不能作为长期运行版本。

## 10. 发布后检查表

```bash
systemctl is-active jarvis-gateway.service
systemctl is-active caddy.service
curl --fail http://127.0.0.1:20128/healthz
curl --fail https://web.alexuhui.win/healthz
journalctl -u jarvis-gateway.service --since '10 minutes ago' --no-pager
journalctl -u caddy.service --since '10 minutes ago' --no-pager
df -h /
```

还应从浏览器完成：

1. 登录并调用 `/v1/auth/me`。
2. 发起一次 Chat，确认 SSE 持续输出。
3. 上传一个超过 `1 MiB` 的合法 Workspace ZIP。
4. 查询 Workspace 列表并删除测试 Workspace。
5. 检查 Provider 请求日志和路由结果。

## 11. 已知风险和改进顺序

1. **立即轮换已暴露的凭据**：root 和 dev 密码曾通过人工渠道传递，应轮换，并改用 SSH Key。
2. **保持 Web 与 Gateway 同步发布**：发布检查必须同时核对源码提交、Gateway 二进制和 Web chunk。
3. **建立自动备份**：目前没有 Jarvis SQLite、Session 或 Workspace 的定时异机备份。
4. **迁移出 `/root`**：按通用部署指南创建 `jarvis` 系统用户，将二进制放在 `/opt/jarvis`，
   数据放在 `/var/lib/jarvis`，配置放在 `/etc/jarvis`。
5. **启用主机防火墙**：确认云防火墙后启用 UFW，只开放管理来源的 SSH 和公网 `80/443`。
6. **清理无效 Caddy 站点**：`gateway.alexuhui.win` 当前代理到无人监听的 `8787`，持续产生 502。
7. **将构建与运行分离**：使用 CI 生成带 commit/version 的 Gateway 和 Web 产物，服务器只安装产物，
   避免源码、秘密、数据库和构建缓存混放在同一 Git 工作树。
8. **增加监控**：至少监控磁盘、systemd 重启次数、HTTP 5xx、Provider 错误率和证书续期。
