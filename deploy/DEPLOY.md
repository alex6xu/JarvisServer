# JarvisServer 部署（Caddy 方案，推荐）

## 拓扑

```
Browser --HTTPS--> Caddy(jarvis.alexuhui.win)
                    ├── 静态文件  --> /opt/jarvis/web   (web/dist)
                    └── /v1 /healthz --> 127.0.0.1:8080 (gateway, 仅回环)
                                          ├── SQLite (/var/lib/jarvis/data)
                                          └── LLM Providers (出站)
```

## 步骤

### 0. 构建（在有 Go 1.27 的机器上，如 EPYC 家用服务器）
```bash
bash deploy/build.sh
# 产出 build/gateway、web/dist/
```

### 1. 上传产物到 VPS
```bash
scp build/gateway  root@<vps>:/opt/jarvis/bin/gateway
scp -r web/dist/.   root@<vps>:/opt/jarvis/web/
scp deploy/gateway.prod.yaml root@<vps>:/etc/jarvis/gateway.yaml
```

### 2. VPS 上建目录与专用账号
```bash
sudo useradd --system --home /var/lib/jarvis --shell /usr/sbin/nologin jarvis
sudo install -d -o root   -g root  -m 0755 /opt/jarvis/bin
sudo install -d -o root   -g root  -m 0755 /opt/jarvis/web
sudo install -d -o root   -g jarvis -m 0750 /etc/jarvis
sudo install -d -o jarvis -g jarvis -m 0700 /var/lib/jarvis/data
sudo install -d -o jarvis -g jarvis -m 0700 /var/lib/jarvis/logs
sudo install -d -o jarvis -g jarvis -m 0700 /var/lib/jarvis/home
sudo install -d -o jarvis -g jarvis -m 0700 /var/lib/jarvis/workspaces
sudo install -o root -g root -m 0755 /opt/jarvis/bin/gateway /opt/jarvis/bin/gateway
sudo chown -R root:root /opt/jarvis/web
sudo chown root:jarvis /etc/jarvis/gateway.yaml && sudo chmod 0640 /etc/jarvis/gateway.yaml
```

### 3. 安装 systemd 服务
```bash
sudo cp deploy/jarvis-gateway.service /etc/systemd/system/
# 编辑 JARVIS_ADMIN_PASSWORD 为长随机串；如需美股社交舆情，同时填写
# SOCIAL_SENTIMENT_API_KEY。新闻舆情至少填写 ANSPIRE_API_KEYS，或选择
# TAVILY_API_KEY、BOCHA_API_KEYS、BRAVE_API_KEY 中任意一个。
sudo systemctl daemon-reload
sudo systemctl enable --now jarvis-gateway
sudo systemctl status jarvis-gateway
```

### 4. Caddy 反代
把 `deploy/Caddyfile` 内容并入 VPS 的 Caddy 配置，DNS 加 `jarvis.alexuhui.win` A 记录，然后：
```bash
sudo caddy reload   # 或 systemctl reload caddy
```
Caddy 自动申请 Let's Encrypt 证书。

### 5. 验证
```bash
curl --fail http://127.0.0.1:8080/healthz
```
首次登录（dev / 初始密码）后立即：改管理员密码、在管理界面加 Provider、建日常 API Token。

## 注意事项
- 单实例：SQLite + 进程内状态，不能多副本。
- 1GB RAM：个人单用户够用（网关只转发，不在本机跑模型）；并发 Coder 任务多则加 RAM。
- 备份：数据库不是全部数据；完整迁移还必须备份文档、Workspace、runtime/JARVIS_HOME、Skills、配置和密钥，见下节。
- 升级：先备份，再替换二进制与 web/dist；回滚用上一版产物 + 数据备份。
- 安全：只开 443；不把 API Key 写进 Git；数据库和备份权限 0600。

## 数据边界、自动备份与迁移

### 只备份数据库够不够？

**不够。** `gateway.db` 保存账户、Token 哈希、Provider 配置、会话、消息、项目、文档元数据、运行和审计记录，但以下数据在 SQLite 外：

- `DocumentsRoot`：用户上传文档的原文件和提取文本；
- `WorkspacesRoot`：Code Workspace、Git 仓库和未提交代码；
- `Agent.Cwd` / runtime：Memory、运行时 checkpoint、信任和本地状态；
- `JARVIS_HOME`：插件、Prompts、主题、包记录和配置；
- `JARVIS_SKILLS_DIR`：内置或用户 Skills；
- `gateway.yaml`、服务环境文件：部署路径、管理员密码和第三方 API 密钥；
- GitHub token 加密密钥等本地密钥文件。没有对应密钥，即使数据库迁移成功，加密凭据也可能无法解密。

源码、Gateway 二进制、`web/dist`、日志和 Go/npm 缓存可以重新构建，通常不需要放进数据备份。

### 手动创建完整备份

仓库提供 `deploy/backup-data.sh`。脚本使用 Python 标准库的 SQLite Online Backup API，在 Gateway 运行时也能生成一致数据库副本，然后与数据库外数据一起打成受限权限的 `tar.gz`：

```bash
cd /root/JarvisServer

# 先查看将包含哪些目录，不写文件
bash deploy/backup-data.sh --dry-run

# 正式备份
bash deploy/backup-data.sh
```

默认归档位置：

```text
/root/JarvisServer/backups/data/jarvis-data-<host>-<UTC时间>.tar.gz
/root/JarvisServer/backups/data/jarvis-data-<host>-<UTC时间>.tar.gz.sha256
```

默认保留最近 14 天，并始终保留最新 7 份。可通过环境变量修改路径和策略，例如：

```bash
JARVIS_DATABASE_PATH=/root/JarvisServer/data/gateway.db \
JARVIS_DOCUMENTS_DIR=/root/JarvisServer/data/documents \
JARVIS_WORKSPACES_DIR=/root/JarvisServer/workspaces \
JARVIS_RUNTIME_DIR=/root/JarvisServer/runtime \
JARVIS_CONFIG_FILE=/root/JarvisServer/etc/gateway.server.yaml \
JARVIS_ENV_FILE=/root/JarvisServer/data/gateway.env \
JARVIS_BACKUP_DIR=/mnt/encrypted/jarvis-backups \
JARVIS_BACKUP_RETENTION_DAYS=30 \
JARVIS_BACKUP_RETENTION_COUNT=10 \
bash deploy/backup-data.sh
```

> 备份包含账户数据、工作区代码和凭据，必须加密存储和传输。脚本生成 SHA-256 校验文件，但校验不等于加密。建议把备份目录放在加密磁盘或再使用 age/GPG 加密，并将副本同步到另一台机器或对象存储；只保存在原服务器不能防磁盘损坏。

### 安装每日 systemd 自动备份

生产模板包括：

```text
deploy/jarvis-backup.service
deploy/jarvis-backup.timer
deploy/backup.env.example
```

安装示例：

```bash
sudo install -d -o jarvis -g jarvis -m 0700 /var/backups/jarvis
sudo install -m 0755 deploy/backup-data.sh /opt/jarvis/bin/backup-data
sudo install -m 0644 deploy/jarvis-backup.service /etc/systemd/system/
sudo install -m 0644 deploy/jarvis-backup.timer /etc/systemd/system/
sudo install -m 0600 deploy/backup.env.example /etc/jarvis/backup.env
sudo editor /etc/jarvis/backup.env
sudo systemctl daemon-reload
sudo systemctl enable --now jarvis-backup.timer
```

验证，不要使用可能进入 pager 的裸 `systemctl status`：

```bash
systemctl list-timers jarvis-backup.timer --no-pager
sudo systemctl start jarvis-backup.service
systemctl show jarvis-backup.service -p Result -p ExecMainStatus --no-pager
ls -lh /var/backups/jarvis
sha256sum -c /var/backups/jarvis/*.sha256
```

Timer 默认每天 03:30 执行，增加最多 30 分钟随机延迟，并设置 `Persistent=true`，机器关机错过执行后会在下次启动补跑。

### 迁移到另一台电脑

1. 在旧机器生成备份并校验 `.sha256`；
2. 安全传输归档和校验文件；
3. 在新机器安装相同或更新版本的 JarvisServer，但先不要启动 Gateway；
4. 解压到临时目录并查看 `manifest.json`；
5. 根据新机器配置路径恢复：
   - `database/gateway.db` → `DatabasePath`；
   - `documents/` → `DocumentsRoot`；
   - `workspaces/` → `WorkspacesRoot`；
   - `runtime/`、`jarvis-home/`、`skills/` → 对应运行目录；
   - `config/` 中配置和环境文件需要人工检查域名、绝对路径、权限和密钥；
6. 将数据目录设为服务用户所有，敏感文件权限设为 `0600`、目录 `0700`；
7. 启动 Gateway，应用数据库迁移并验证 `/healthz`、账户登录、Session、Project 文档和 Workspace。

示例解包（先到临时目录，不要直接覆盖生产目录）：

```bash
sha256sum -c jarvis-data-*.tar.gz.sha256
mkdir -p /tmp/jarvis-restore
chmod 0700 /tmp/jarvis-restore
tar -xzf jarvis-data-*.tar.gz -C /tmp/jarvis-restore
cat /tmp/jarvis-restore/manifest.json
```

不建议提供“无确认直接覆盖生产数据”的自动恢复脚本，因为不同机器绝对路径、服务用户和密钥位置可能不同，误操作会覆盖现有数据库和 Workspace。

## 一键同步工作空间到安装目录（推荐）

在开发工作空间完成修改后，使用 `deploy/sync-install.sh` 可以一次完成测试、生产构建、源码同步、产物替换、systemd 重启和健康检查：

```bash
cd /root/JarvisServer/workspaces/<workspace-id>

# 首先只预览，不复制、不构建、不重启
bash deploy/sync-install.sh --dry-run --skip-tests

# 确认后正式同步到默认安装目录 /root/JarvisServer
bash deploy/sync-install.sh
```

如果已经把完整源码直接放在安装目录中，也可以在安装目录一键执行；脚本会识别源目录和安装目录相同，并跳过源码复制：

```bash
cd /root/JarvisServer
bash deploy/sync-install.sh
```

常用选项：

```bash
# 仍构建和同步，但不重启服务
bash deploy/sync-install.sh --no-restart

# 跳过测试，只执行生产构建、同步和部署
bash deploy/sync-install.sh --skip-tests

# 指定其他安装目录、配置、服务和健康地址
JARVIS_INSTALL_DIR=/opt/jarvis-source \
JARVIS_CONFIG_FILE=/opt/jarvis-source/etc/gateway.server.yaml \
JARVIS_SYSTEMD_UNIT=jarvis-gateway.service \
JARVIS_HEALTH_URL=http://127.0.0.1:8080/healthz \
bash deploy/sync-install.sh
```

脚本的安全规则：

- 不复制或覆盖安装目录的 `.git`，避免两个仓库的 commit/rebase 状态混在一起；
- 保留 `data/`、`runtime/`、`workspaces/`、`backups/`、生产 `gateway.server.yaml` 和环境文件；
- 拒绝受限的 Snap Go，自动寻找 native Go toolchain，也可通过 `JARVIS_GO` 指定；
- 部署前检查磁盘空间，并用文件锁阻止两个同步任务同时运行；
- Gateway 和 Web 先构建到 staging，再原子替换；
- 健康检查失败时自动恢复上一版二进制和前端产物；
- 每次部署备份位于 `<安装目录>/backups/sync-install-<UTC时间>`。

> 脚本不会自动执行 `git pull` 或 `git commit`。请先在工作空间确认代码版本，避免将远端更新、提交历史和生产部署混为同一个操作。

## 拉取代码后使用 screen 更新

服务器直接保留源码仓库、Caddy 指向仓库内 `web/dist` 时，可以在 `git pull` 成功后执行：

```bash
cd /root/JarvisServer
bash deploy/update-screen.sh
```

脚本会依次执行 `npm ci`、前端生产构建、Gateway 二进制构建，然后停止同名旧
`screen` 会话并启动新进程。默认会优先使用 `etc/gateway.server.yaml`，否则使用
`etc/gateway.yaml`；如果 `jarvis-gateway.service` 正在运行，脚本会先停止它以释放端口。

常用覆盖参数：

```bash
JARVIS_CONFIG_FILE=/root/JarvisServer/etc/gateway.server.yaml \
JARVIS_SCREEN_NAME=jarvis-gateway \
JARVIS_HEALTH_URL=http://127.0.0.1:8080/healthz \
bash deploy/update-screen.sh
```

查看进程和日志：

```bash
screen -ls
screen -r jarvis-gateway
tail -f /root/JarvisServer/data/logs/access.log /root/JarvisServer/data/logs/error.log
```

业务日志以 JSON 写入文件并自动按 50 MB 轮转，最多保留 10 个备份和 7 天，旧日志会压缩。每条分布式日志包含 `service`、`environment`、`instance_id`，请求和 Agent 流程还会带 `request_id`、`run_id`、`session_id` 等关联字段。上传失败会记录 `workspace upload failed`，且不会记录令牌或文件内容。

开发/screen 配置默认写入 `/root/JarvisServer/data/logs`，生产配置写入 `/var/lib/jarvis/logs`。可按消息或请求 ID 定位：

```bash
grep 'workspace upload' /root/JarvisServer/data/logs/error.log | tail -n 50
grep 'req_xxxxxxxxxxxx' /root/JarvisServer/data/logs/*.log
```

使用 systemd 和生产配置时：

```bash
tail -f /var/lib/jarvis/logs/access.log /var/lib/jarvis/logs/error.log
journalctl -u jarvis-gateway -f  # 仅查看启动失败和进程级输出
```
