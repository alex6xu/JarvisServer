# 项目消息优化 88859c9 生产部署报告

- 日期：2026-09-11（Asia/Shanghai）
- 提交：`88859c989dac42647d5d3c2edf79c31c62f31843`
- 生产仓库：`/root/JarvisServer`
- 生产服务：`jarvis-gateway`
- 结论：**生产替换 PASS；认证接口回归 BLOCKED**

## 安全边界与前置证据

通过用户指定的 PTY SSH（StrictHostKeyChecking、BatchMode、ConnectTimeout=10）执行。未在生产工作树执行 `pull`、`clean`、`stash`、`reset`、`rebase`，未修改或覆盖既有 dirty 内容；部署前后工作树均保持：

```text
 M deploy/build.sh
?? backups/
?? data/
?? etc/gateway.server.yaml
?? etc/gateway.yaml.1
?? etc/gateway.yaml.2
?? jarvisserver
?? releases/
?? runtime/
?? web/dist.previous/
```

生产 HEAD 部署前为基线 `db5735833442e5332a1e55f0dfbda48c7a7c7ba3`。服务实际路径核验为：

- ExecStart：`/root/JarvisServer/build/gateway -f /root/JarvisServer/etc/gateway.server.yaml`
- WorkingDirectory：`/root/JarvisServer`
- 服务监听：`127.0.0.1:20128`
- 静态 root：由活动 `/etc/caddy/Caddyfile` 确认为 `/root/JarvisServer/web/dist`
- Caddy 与 OpenClaw 未重启
- `NeedDaemonReload=yes` 是部署前已存在的 systemd 提示；未执行 `daemon-reload`
- 配置确认：数据库为 `/root/JarvisServer/data/gateway.db`，未复制、迁移、DDL、ANALYZE、VACUUM 或修改配置/运行目录

### 活跃 run 检查

以 SQLite URI `mode=ro` 只读检查。`runs` 状态中没有未结束状态：

```text
runs: cancelled=1, done=165, interrupted=11, timed_out=4
```

另发现 `run_message_queue_items` 有 `executing=2`；未修改数据库，也未伪造为零。该队列状态属于既有运行时状态，部署采取的后端原子替换不涉及数据库。此项应作为运维观察项保留。

## 隔离 staging、构建与校验

独立目录（从已推送 commit 归档，不使用 dirty worktree）：

```text
/root/JarvisServer/releases/staging-88859c989dac-20260911T020153Z
```

在 staging 完成：

- `go test ./...`：PASS
- Web `npm ci`：完成；npm audit 报告既有依赖树有 4 moderate、2 high，未执行 audit fix
- Web `npm test -- --run`：PASS，16 files / 62 tests
- Web `npm run build`：PASS，先通过 `tsc`；存在既有单 chunk >500 kB 警告
- `deploy/build.sh`：作为构建脚本运行并 PASS；脚本保持可执行权限 `0755`
- Go 产物：静态 linux/amd64 ELF，`ldd` 返回 not a dynamic executable，已记录至备份 `new-binary-deps.txt`

关键 hash：

```text
f245c77734a0dfd73ba4cd873c9781cd473834ac7addd6fcc9c54da9670657f7  staging/build/gateway
6711bb690acefb227e88d9725544a94d19011a259f42ccf338d4589f2f2ba7b9  staging/web/dist/index.html
3e039ea50a486031e7d2a58e110320d60e670bee2331b40c24d742e234228a05  staging/web-dist.sha256
```

Staging 自带 `web-dist.sha256`、`artifact-sha256`；发布后在生产静态目录执行校验，全部新资源通过。

## 备份与安装证据

新建备份目录（未删除既有目录）：

```text
/root/JarvisServer/backups/deploy-88859c989dac-20260911T020913Z
```

只备份了当前运行后端、当前 index，以及当前 index 引用的旧静态资源；没有复制大数据库、配置或 runtime：

```text
gateway 35246240 bytes
index.html 591 bytes
assets/favicon.svg
manifest.sha256.txt
installed.sha256.txt
new-binary-deps.txt
```

安装动作：

1. 以同文件系统临时文件 `build/.gateway.<commit>.tmp` 安装新二进制，再 `mv -T` 原子替换实际 ExecStart 路径；权限/属主为 `0755 root:root`。
2. 将新 hash 资源复制到实际静态 root，未删除旧 hash chunks 或用户文件。
3. 以临时 index 文件再 `mv -T` 原子替换实际 `web/dist/index.html`。
4. 仅执行 `systemctl restart jarvis-gateway`；未重启 Caddy、OpenClaw 或共享服务。

发布后后端与 staging hash 一致：

```text
f245c77734a0dfd73ba4cd873c9781cd473834ac7addd6fcc9c54da9670657f7  /root/JarvisServer/build/gateway
f245c77734a0dfd73ba4cd873c9781cd473834ac7addd6fcc9c54da9670657f7  staging/build/gateway
```

index hash 一致：

```text
6711bb690acefb227e88d9725544a94d19011a259f42ccf338d4589f2f2ba7b9  /root/JarvisServer/web/dist/index.html
6711bb690acefb227e88d9725544a94d19011a259f42ccf338d4589f2f2ba7b9  staging/web/dist/index.html
```

## 发布后验证

服务：

```text
ActiveState=active
SubState=running
MainPID=718161
ExecMainStartTimestamp=Fri 2026-09-11 02:09:15 UTC
NeedDaemonReload=yes (既存提示，未 daemon-reload)
```

健康与公开非敏感端点：

- `GET http://127.0.0.1:20128/healthz`：HTTP 200
- `GET http://127.0.0.1:20128/v1/auth/config`：HTTP 200，返回 `{"registration_enabled":false}`；未读取 token/secret
- 活动 Caddy 路由 `web.alexuhui.win`：HTTPS 首页 HTTP 200，返回新 index；新 JS、CSS、favicon 均 HTTP 200，hash 资源可访问
- 直接访问 gateway 的 `/` 返回 404，符合活动 Caddy 将静态 root 与 API reverse proxy 分流的配置，不作为失败

未执行：

- `GET /v1/projects`：无合法认证会话，**BLOCKED**；未伪造 token，不声称 PASS
- `GET /v1/agent/sessions/<id>`：无合法认证会话/目标 session，**BLOCKED**；未获取对话正文，不声称 PASS
- 真实模型调用、压测、认证回归：未执行

## 回滚证据与路径

本次切换后未发现服务失败，因此未执行回滚。回滚材料已在新备份目录：

- 旧后端：`/root/JarvisServer/backups/deploy-88859c989dac-20260911T020913Z/gateway`
- 旧 index：同目录 `index.html`
- 旧 index 引用资源：同目录 `assets/`

若需要回滚，应在确认无活跃运行风险后，将备份二进制以同样临时文件 + 原子 rename 恢复、恢复旧 index，并仅受控重启 `jarvis-gateway`，再核验 `active/running` 与 `/healthz`。不应删除新 staging、旧 hash chunks 或用户既有文件作为回滚前提。

## 最终状态

| 项目 | 状态 |
|---|---|
| Commit `88859c9` 已从 origin fetch/archive | PASS |
| 隔离 staging | PASS |
| Go 全量测试 | PASS |
| Web 62 tests + tsc/build | PASS |
| 备份、manifest、hash | PASS |
| 原子后端替换 | PASS |
| 新前端资源与 index 发布 | PASS |
| `jarvis-gateway` active/running | PASS |
| `/healthz` | PASS |
| 首页及 hash 资源经 Caddy | PASS |
| `/v1/projects` | BLOCKED（无合法会话） |
| `/v1/agent/sessions/<id>` | BLOCKED（无合法会话） |
| 生产工作树/用户保留内容 | 保留，未清理/覆盖 |
