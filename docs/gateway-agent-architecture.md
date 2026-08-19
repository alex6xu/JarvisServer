# Jarvis Gateway 与 Agent Runtime 目标架构

## 1. 目标

将 JarvisServer 演进为一个长期运行的本地优先网关：

- Web Chat、Code 对话和管理后台只连接 Gateway，不直接感知具体 Provider 或 Agent 实现。
- Gateway 统一负责认证、配置、会话、工作区、运行调度、事件分发和审计。
- Provider Router 根据模型能力、健康度、成本、延迟和策略生成候选路由，并在安全边界内自动切换。
- Agent Runtime 专注上下文构造、模型流式调用、工具执行、压缩、记忆和子代理。
- 保留现有 Agent 双层循环；在每个 LLM turn 前由 Provider Router 进行一次路由决策。

OpenClaw 的关键借鉴点是“Gateway 是控制面，不是推理实现本身”。JarvisServer 应采用相同边界，但 Provider Router 需要成为独立领域模块，而不是继续留在 HTTP handler 或内存 Provider 表中。

## 2. 分层与职责

### 2.1 接入层

`ChannelAdapter` 将 Web Chat、Web Code、未来的 CLI、Webhook 或 IM 消息转换成统一 `InboundMessage`：

```go
type InboundMessage struct {
    Channel      string
    AccountID    string
    Conversation string
    WorkspaceID  string
    Mode          string // chat | code
    Content       []ContentBlock
    Metadata      map[string]string
    IdempotencyKey string
}
```

Chat 和 Code 不是两套 Agent Runtime。它们是两个 `AgentProfile`：Chat 默认限制工作区工具；Code 装配完整的文件、Shell、Git 和子代理能力。

### 2.2 Gateway 控制面

Gateway 只承担控制面职责：

- Auth/RBAC、限流和请求幂等。
- Channel/Account 到 AgentProfile、Workspace、Session 的绑定。
- 会话命令串行化，同一 Session 同时最多一个写入型 Run。
- 创建、取消和恢复 Run；维护持久事件日志。
- 配置 CRUD、密钥引用、版本发布和热更新通知。
- SSE 用于浏览器事件流；后续可增加 WebSocket JSON-RPC 作为统一控制协议。

Gateway 不应导入具体 OpenAI/Anthropic HTTP 实现，也不应直接执行工具。

### 2.3 Provider 控制层

把当前 `internal/gateway/provider_route.go` 演进为独立 `internal/router` 包：

```go
type RouteRequest struct {
    RunID, SessionID string
    RequestedModel   string
    Required         Capabilities
    PolicyID         string
    Attempt          int
    ExcludeEndpoints []string
}

type RoutePlan struct {
    Primary    Endpoint
    Candidates []Endpoint
    Reason     string
    PolicyRev  int64
}

type Router interface {
    Plan(context.Context, RouteRequest) (RoutePlan, error)
    Observe(context.Context, AttemptResult)
}
```

路由分两步：先做硬过滤，再评分排序。

硬过滤条件包括：Provider 启用、模型存在、工具/图片/Thinking 能力匹配、上下文窗口足够、租户允许、熔断器未打开。

建议初始评分：

```text
score = priority * 100
      + health * 30
      + success_rate * 25
      - latency_p95 * 10
      - normalized_cost * 15
      - active_load * 20
```

管理员可以配置 `balanced`、`quality-first`、`cost-first`、`latency-first` 和固定 Provider 策略。动态健康指标不能直接覆盖管理员的硬约束。

### 2.4 Agent 执行面

保留 `internal/runtime` 的双层循环：

```text
外层 Run 循环
  接收初始消息 / follow-up / steering / stop-hook guidance
  └─ 内层 Tool 循环
       1. Router 为本 turn 选择 Provider + Model
       2. LLM 流式生成
       3. 无 tool_calls -> 结束内层循环
       4. 有 tool_calls -> 执行工具并追加结果
       5. 压缩、提醒、停止判定后进入下一 turn
```

外层循环管理一次用户任务的生命周期；内层循环完成模型与工具之间的 Observe-Think-Act 循环。Provider 路由发生在内层每次 LLM 调用之前，因此下一 turn 可以根据上一次的限流、延迟和错误切换节点。

## 3. 故障切换语义

智能切换不能简单地在任意流错误后换 Provider，否则可能重复文本、重复工具调用或造成重复副作用。

按失败阶段处理：

| 失败阶段 | 行为 |
| --- | --- |
| 建连前、鉴权、限流、首 token 前 | 自动尝试下一候选，保持同一 turn |
| 已输出文本但未产生工具调用 | 默认终止并报告；支持显式的“丢弃部分输出后重试”策略 |
| 已产生工具调用但尚未执行 | 丢弃未提交 assistant message 后可重试 |
| 工具已经执行 | 不透明重放；持久化结果并让同一或新 Provider 从工具结果继续 |
| 上下文不兼容 | 重新选模型，必要时先压缩上下文 |

每次尝试必须记录 `attempt_id`、路由原因、首 token 延迟、token 用量、错误分类和是否产生副作用。重试预算属于 Run，而不是单个 HTTP 请求。

## 4. 持久状态

当前 `MemStore` 和 `RunManager` 应替换为 Repository 接口，首个生产实现可继续使用 SQLite：

- `accounts`, `auth_tokens`, `roles`
- `providers`, `provider_endpoints`, `models`, `credentials`
- `route_policies`, `route_policy_versions`, `health_samples`
- `agent_profiles`, `channel_bindings`, `workspaces`
- `sessions`, `messages`, `runs`, `run_attempts`
- `run_events(seq, run_id, type, payload)`
- `tool_executions`, `audit_logs`

`run_events` 是追加式事实来源。SSE 的 `after_seq` 从数据库回放，进程重启后仍可恢复；内存 subscriber 只负责实时加速。

Provider 密钥只保存加密后的 secret 或外部 Secret 引用。API 返回永不回显完整密钥。

## 5. 配置模型

配置分为三层，并且都带版本：

1. 系统层：监听地址、数据库、加密主密钥、沙箱后端。
2. 管理层：Provider、模型、路由策略、AgentProfile、工具策略。
3. 会话层：本次模型偏好、thinking level、工作区和临时工具限制。

管理更新采用 `draft -> validate -> publish`，发布后生成不可变 revision。Run 启动时锁定 revision；运行中只读取健康度等动态信号，避免一次 Run 的工具权限或系统提示词无审计地变化。

## 6. API 边界

保留现有 `/v1`，但按控制面语义收敛：

- `POST /v1/conversations/{id}/messages`：提交 Chat/Code 消息，返回 `run_id`。
- `GET /v1/runs/{id}/events?after_seq=N`：SSE 回放加实时事件。
- `POST /v1/runs/{id}/cancel`：幂等取消。
- `GET /v1/sessions/{id}`：消息与活跃 Run 快照。
- `GET/POST/PUT /v1/admin/providers`：Provider 与 Endpoint 管理。
- `POST /v1/admin/providers/{id}/probe`：显式健康检查。
- `GET/POST/PUT /v1/admin/route-policies`：路由策略。
- `GET/POST/PUT /v1/admin/agent-profiles`：Chat/Code 等运行配置。
- `GET /v1/admin/runs/{id}/attempts`：Provider 选择与失败切换审计。

统一事件信封：

```json
{
  "run_id": "run_...",
  "seq": 42,
  "type": "model.delta",
  "timestamp": "2026-08-18T10:00:00Z",
  "attempt_id": "attempt_...",
  "payload": {}
}
```

建议事件类型包括 `run.*`、`route.selected`、`model.*`、`tool.*`、`compaction.*` 和 `usage.updated`。

## 7. 安全基线

- Gateway 默认仅监听 loopback；对外暴露必须启用认证和 TLS 反向代理。
- 删除生产配置中的 `AuthMode: none` 与全局 `Approve: true` 组合。
- Code Profile 的 Shell/Write 工具必须绑定 Workspace 根目录和工具策略。
- 管理 API、对话 API、工具审批分别授权，不能只依赖前端隐藏菜单。
- Web fetch、Provider base URL 和插件网络访问实施 SSRF 防护。
- 所有配置变更、Provider 选择和工具副作用进入审计日志。

## 8. 迁移顺序

### Phase 1：稳定控制面

- 引入 SQLite Repository，持久化 Provider、Session、Run 和 RunEvent。
- 将 `RunManager` 改成“数据库日志 + 内存订阅器”。
- Chat/Code 统一为 `AgentProfile`，前端继续使用现有 SSE。

### Phase 2：Provider Router

- 把 `resolveLLM` 移出 gateway，建立 Endpoint、Capability 和 RoutePolicy。
- 增加健康采样、错误分类、熔断和候选路由。
- 先只支持首 token 前故障切换，再逐步开放其他安全重试。

### Phase 3：Runtime 边界

- [x] Runtime 通过 `CompletionRouter` 获取流，不再接收固定 Provider。
- [x] 为每个 turn 持久化 attempt 和 checkpoint。
- [x] 重启后恢复事件与检查点，并将未完成 Run 安全收敛为 `interrupted`；不自动重放可能已有副作用的工具。
- [x] 增加幂等取消、Run 总超时和 `cancelled` / `timed_out` / `interrupted` 状态语义。
- [x] 提供 `GET /v1/admin/runs/{id}/attempts` 路由审计接口。

### Phase 4：网关化扩展

- 增加 WebSocket JSON-RPC 控制协议和 ChannelAdapter 插件。
- 引入 worker/沙箱执行后端，使 Gateway 与 Agent Runtime 可独立部署。
- 增加多 Agent 绑定、调度任务和跨会话消息能力。

## 9. 不建议的设计

- 不要让 React 页面直接保存 Provider API Key 或决定故障切换。
- 不要把 Provider 权重逻辑继续堆在 HTTP handler 和 `MemStore` 中。
- 不要为 Chat 和 Code 复制两套 Agent Loop。
- 不要在已执行工具后自动从头重放整个 turn。
- 不要一开始拆成多个网络微服务；先建立清晰 Go 接口和持久边界，再按负载拆进程。
