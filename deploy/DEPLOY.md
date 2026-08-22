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
- 备份：用 `sqlite3 ... ".backup"` 在线备份 gateway.db，连同 /var/lib/jarvis/home、workspaces、gateway.yaml 一起加密备份。
- 升级：先停服备份，替换二进制与 web/dist，再启动；回滚用 .previous 产物 + 数据库备份。
- 安全：只开 443；不把 API Key 写进 Git；数据库权限 0600。

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
