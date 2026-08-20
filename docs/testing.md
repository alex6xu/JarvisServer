# JarvisServer 测试与质量门禁

本文档定义网关、Provider Router、Agent Runtime、SQLite 和 Web 的统一测试入口与合入要求。测试不得访问真实 Provider，也不得依赖开发机已有的 `.jarvis` 数据。

对已经部署的服务器进行验收、可靠性、容量和安全边界测试时，使用 [`server-test-plan.md`](server-test-plan.md)。该方案将生产可执行检查与只能在隔离环境执行的故障注入和压测明确分开。

## 本地命令

核心包快速验证：

```bash
go run ./scripts/quality -mode=core
```

格式门禁失败时，使用项目 `go.mod` 对应的 Go 工具链修复新增或已修改文件，然后继续执行核心质量检查：

```bash
go run ./scripts/quality -fix-format -mode=core
```

不要依赖系统路径中的裸 `gofmt`；其版本可能与 CI 使用的项目工具链不同。`-fix-format` 不会改写 `scripts/quality/gofmt-baseline.txt` 中记录的历史格式欠账。

提交前完整验证：

```bash
go run ./scripts/quality -mode=full -race
cd web
npm test
npm run build
npm audit --audit-level=critical
```

`-mode=full` 当前以 Linux/CI 为准。Windows 本地使用 `-mode=core`；全仓 Windows 测试仍有若干历史用例依赖 Unix 路径、POSIX 权限和 Shell 插件行为，这些失败必须作为跨平台测试债务单独清理，不能加入忽略列表。

质量命令依次执行 Go 格式检查、`go vet`、随机顺序测试、可选竞态检查和覆盖率检查。覆盖率报告写入仓库根目录的 `coverage.out`。核心合并防回退下限为 60%，并分别要求 Gateway 40%、Router 55%、Runtime 80%、Provider 85%。提高下限时应先在 Linux 和 Windows CI 上验证。

历史未格式化文件记录在 `scripts/quality/gofmt-baseline.txt`。门禁不允许增加新条目；修复历史文件后必须同步删除对应条目。

## 测试层次

| 层级 | 主要验证 | 运行时机 |
| --- | --- | --- |
| 单元 | 路由过滤/评分、错误分类、状态转换、Web 纯函数 | 每个 PR |
| 组件 | SQLite Repository、迁移、Provider 流、Runtime 双层循环 | 每个 PR |
| API | Chat、SSE 回放、取消、认证、管理接口 | 每个 PR |
| 可靠性 | 并发 Session、重启恢复、故障切换、竞态 | 每个 PR / Nightly |
| 压力 | 多会话、慢客户端、SQLite 锁竞争、长时间运行 | Nightly / Release |

## 关键不变量

- 同一 Session 同时最多一个写入型 Run。
- Run 事件序号严格递增；`after_seq` 回放不丢失、不重复。
- 每个 Run 只能进入一个终态：`done`、`error`、`cancelled`、`timed_out` 或 `interrupted`。
- 首 token 前可以安全切换 Provider；输出或工具调用提交后不得透明重放。
- 每个 Provider 请求和路由 attempt 都必须可审计，且不能记录完整 API Key。
- 进程重启保留事件和 checkpoint，但不能自动重放可能已有副作用的工具。
- 所有已发布 SQLite schema 版本必须能升级到最新版本，重复启动迁移必须幂等。

## 编写测试约束

- 使用 `t.TempDir()`、`httptest.Server`、假时钟和脚本化 Provider，禁止依赖公网。
- 不使用固定 `time.Sleep` 等待异步结果；使用 channel、context 和有上限的超时。
- 文件路径断言必须使用 `filepath`，同时支持 Windows 和 Unix。
- 故障测试必须明确发生阶段：建连前、首 token 前、输出后、工具调用后。
- 修复缺陷时先增加能稳定复现的测试，再修改实现。

## 发布前稳定性

发布候选版本至少执行一次 30 分钟并发稳定性测试和一次数据库备份恢复演练。验收要求：无数据竞态、无事件丢失或重复、无重复工具副作用、审计记录完整，且稳定负载结束后的 goroutine 和内存回到基线附近。

## 已知质量债务

- Web 依赖审计当前包含 Vite 的高危开发服务器公告和 React Router 的中危公告，自动修复需要主版本升级。CI 先阻断 Critical；Vite/React Router 升级应作为独立兼容性任务处理，并在升级后把门禁提高到 `--audit-level=high`。
- `gofmt-baseline.txt` 中的历史格式问题应逐步清理，禁止新增。
