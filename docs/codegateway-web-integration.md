# CodeGateway Web 与 pigo agentcore 集成设计

> 版本：v0.1 · 日期：2026-08-12  
> 目标：将 `web/`（CodeGateway React 前端）接入本仓库的 `agentcore` / `runtime`，使浏览器端 Chat/Coder 能驱动 pigo agent 运行。

---

## 1. 背景与现状

### 1.1 项目内两套 Web 体系

| 体系 | 位置 | 状态 | 说明 |
|------|------|------|------|
| **CodeGateway React SPA** | `web/` | 前端完整，后端缺失 | Vite + React，期望 `:8080/v1/*` HTTP API |
| **Remote Control 嵌入式 SPA** | `internal/remotecontrol/web/` | 已实现 | CLI `/remote-control` 的终端镜像，协议不同 |

### 1.2 当前断点

```
web/ (React :3000)
    │  fetch /v1/*
    ▼
CodeGateway HTTP 服务 (:8080)   ← 【本仓库未实现】
    │
    ▼
agentcore + runtime             ← 【已实现，仅 CLI 暴露】
```

- `web/vite.config.ts` 将 `/v1` 代理到 `http://localhost:8080`
- Go 侧（pigo）目前只有 REPL / headless / TUI / remotecontrol / subagent RPC，**没有 HTTP 网关**
- `web/` 与 `internal/agentcore` 之间缺少适配层

### 1.3 pigo 已有的 agent 运行接缝

CLI 三种驱动方式均收敛到同一核心：

```
runtime.StartRun(ctx, agentCtx, runCfg)
    → runtime.DrainStream(ctx, stream, StreamHandler{ OnText, OnTurnEnd, OnEvent })
```

| 驱动 | 参考文件 | 事件消费方式 |
|------|----------|--------------|
| REPL | `internal/cli/repl/repl.go` | 同步渲染到终端 |
| Headless | `internal/runtime/headless.go` | NDJSON `stream-json` 到 stdout |
| TUI | `internal/cli/tui/bridge.go` | goroutine → buffered chan → Bubble Tea |

**Web 网关应参考 TUI bridge + headless stream-json**：异步 pump 事件，对外输出 SSE。

---

## 2. 目标与范围

### 2.1 总体目标

1. 在本仓库新增 HTTP 服务，实现 `web/` 依赖的核心 API
2. 内部复用 `cli/run.SetupEnv`、`runtime.StartRun`、`session.Store`，不重复实现 agent 逻辑
3. 提供 **AgentEvent → Web SSE 事件** 的翻译层
4. 支持 Chat 页最小闭环：发消息 → 流式回答 → 会话恢复

### 2.2 分阶段范围

| 阶段 | 范围 | 验收 |
|------|------|------|
| **MVP** | 单用户、无完整 admin | Chat 页可对话，SSE 流式输出 |
| **Phase 2** | 会话 CRUD、Coder workspace | Sessions / Coder 页可用 |
| **Phase 3** | Provider 路由、多账户 | Channels / Admin 页可用 |
| **Phase 4** | GitHub / ASR / OAuth | Settings 等高级功能 |

本文档以 **MVP → Phase 2** 为主，Phase 3/4 仅列接口占位。

### 2.3 非目标（初期）

- 不替换 `internal/remotecontrol`（终端镜像仍走 WebSocket）
- 不重写 `web/` 前端协议（优先适配现有 SSE 格式）
- 不一次性实现 CodeGateway 全部 admin / 多租户能力

---

## 3. 目标架构

### 3.1 分层图

```
┌─────────────────────────────────────────────────────────────┐
│  web/ (React)                                               │
│  ChatPage / CoderPage / useRunEventStream                   │
└───────────────────────────┬─────────────────────────────────┘
                            │ HTTP /v1/*
                            ▼
┌─────────────────────────────────────────────────────────────┐
│  cmd/gateway/main.go          ← 新入口（或 pigo --serve）    │
│  internal/gateway/                                          │
│    server.go      路由、中间件、静态资源（可选）              │
│    auth.go        MVP: 单用户 token / 跳过认证                │
│    handler_agent.go   POST /v1/agent/chat                   │
│    handler_session.go GET  /v1/agent/sessions/{id}          │
│    handler_stream.go  GET  /v1/agent/runs/{id}/events (SSE) │
│    run_manager.go   Run 生命周期、并发、取消                  │
│    translate.go     AgentEvent → AgentStreamEvent           │
└───────────────────────────┬─────────────────────────────────┘
                            │
        ┌───────────────────┼───────────────────┐
        ▼                   ▼                   ▼
 internal/cli/run    internal/runtime     internal/session
 SetupEnv/NewConfig  StartRun/DrainStream  JSONL 持久化
 InstallDriverHooks  RunHeadless           Load/Save
        │                   │
        └─────────┬─────────┘
                  ▼
         internal/agentcore
         AgentContext / AgentEvent / MessageList
                  │
                  ▼
         internal/provider + agenttool + trust
```

### 3.2 建议新增包结构

```
internal/gateway/
├── doc.go
├── server.go           // http.Server 启动、/v1 路由注册
├── middleware.go       // CORS、认证、account 注入
├── auth/
│   └── auth.go         // MVP stub；Phase 3 扩展 JWT/多账户
├── agent/
│   ├── handler.go      // POST /v1/agent/chat
│   ├── stream.go       // GET  /v1/agent/runs/{id}/events
│   ├── session.go      // GET  /v1/agent/sessions*
│   └── types.go        // 请求/响应 DTO（对齐 web 前端）
├── run/
│   ├── manager.go      // RunRegistry：runID → RunState
│   └── pump.go         // StartRun + DrainStream goroutine
└── translate/
    └── sse.go          // AgentEvent → SSE payload + seq
```

入口二选一（推荐 A）：

- **A. 独立二进制** `cmd/gateway/main.go`：专门跑 Web 服务，内部 import pigo 库
- **B. 扩展 pigo** `pigo serve --addr :8080`：与 CLI 共用 `cmd/pigo/main.go`

MVP 推荐 **A**，避免 CLI 与 HTTP 服务生命周期纠缠。

---

## 4. 前端协议（web/ 期望）

### 4.1 核心对话流程

```
1. POST /v1/agent/chat
   Body: { message, session_id?, stream: false, model?, workspace_id? }
   Response: { session_id, run_id, model? }

2. GET /v1/agent/runs/{runId}/events?after_seq=0   (SSE)
   data: {"type":"delta","content":"..."}
   data: {"type":"tool_step","step":{"tool","args","result"}}
   data: {"type":"done","content":"...","tool_steps":[...],"model":"..."}
   data: [DONE]

3. GET /v1/agent/sessions/{sessionId}
   Response: { session, messages[], active_run?, last_event_seq? }
```

参考：`web/src/pages/ChatPage.tsx`、`web/src/hooks/useRunEventStream.ts`

### 4.2 SSE 事件类型（前端已定义）

```typescript
// web/src/lib/sessionPersist.ts
type AgentStreamEvent = {
  type?: 'delta' | 'tool_step' | 'done' | 'error' | 'user_injected'
  content?: string
  session_id?: string
  model?: string
  step?: { tool: string; args: string; result: string }
  tool_steps?: ToolStep[]
}
```

### 4.3 前端调用的完整 API 清单

| 优先级 | 方法 | 路径 | 页面 |
|--------|------|------|------|
| P0 | POST | `/v1/agent/chat` | Chat, Coder |
| P0 | GET | `/v1/agent/runs/{id}/events` | Chat, Coder |
| P0 | GET | `/v1/agent/sessions/{id}` | 会话恢复 |
| P1 | GET | `/v1/agent/sessions` | Sessions |
| P1 | GET | `/v1/models` | Chat 连通性探测 |
| P1 | GET/POST | `/v1/workspaces*` | Coder |
| P2 | POST | `/v1/auth/login` 等 | Login |
| P2 | GET/POST | `/v1/admin/*` | Admin 各页 |
| P3 | GET/POST | `/v1/github/*`, `/v1/asr/*`, `/v1/claude/oauth/*` | Settings |

MVP 只需实现 **P0**；P1 的 `/v1/models` 可返回固定列表 stub。

---

## 5. agentcore 事件 → Web SSE 映射

### 5.1 协议差异

| pigo `AgentEvent` | Web SSE |
|-------------------|---------|
| `message_update` + OnText delta | `{ type: "delta", content }` |
| `tool_execution_start/end` | `{ type: "tool_step", step }` |
| `turn_end` / `agent_end` | `{ type: "done", content, tool_steps }` |
| run 失败 | `{ type: "error", content }` |
| steering 注入（若有） | `{ type: "user_injected", content }` |

pigo 原生 stream-json 使用 `eventEnvelope()`（`internal/runtime/headless.go`），字段名与 web 不同，**不能直接透传**。

### 5.2 推荐翻译实现（参考 TUI bridge）

```go
// internal/gateway/translate/sse.go

type Publisher func(seq int64, ev WebEvent)

func NewStreamHandler(pub Publisher, model string, steps *ToolStepCollector) runtime.StreamHandler {
    var full strings.Builder
    return runtime.StreamHandler{
        OnText: func(delta string) {
            full.WriteString(delta)
            pub(nextSeq(), WebEvent{Type: "delta", Content: delta, Model: model})
        },
        OnTurnEnd: func(msg agentcore.AssistantMessage, results []agentcore.ToolResultMessage) {
            // tool results 已在 OnEvent 中收集；此处可补全 done 前状态
        },
        OnEvent: func(ev agentcore.AgentEvent) {
            switch e := ev.(type) {
            case agentcore.ToolExecutionStartEvent:
                steps.Start(e.ToolCallID, e.ToolName, e.Args)
            case agentcore.ToolExecutionEndEvent:
                step := steps.End(e.ToolCallID, e.Result, e.IsError)
                pub(nextSeq(), WebEvent{Type: "tool_step", Step: step})
            case agentcore.AgentEndEvent:
                pub(nextSeq(), WebEvent{
                    Type:      "done",
                    Content:   full.String(),
                    ToolSteps: steps.All(),
                    Model:     model,
                })
            case agentcore.TurnEndEvent:
                if e.Message.StopReason == "error" || e.Message.StopReason == "aborted" {
                    pub(nextSeq(), WebEvent{Type: "error", Content: e.Message.StopReason})
                }
            }
        },
    }
}
```

### 5.3 序号（after_seq）与断点续传

前端 `useRunEventStream` 支持 `after_seq` 重连；pigo stream-json **无内置 seq**。

方案：

1. `RunManager` 为每个 run 维护 **append-only 事件 log**（内存，MVP；Phase 2 可落盘）
2. 每条 SSE 带单调递增 `seq`
3. `GET .../events?after_seq=N` 先 replay `log[N+1:]`，再 subscribe 实时流

```go
type RunState struct {
    ID        string
    SessionID string
    Status    string // running | done | error | cancelled
    Events    []StoredEvent // {Seq, Payload}
    Subs      []chan StoredEvent
    Cancel    context.CancelFunc
}
```

---

## 6. Run 生命周期设计

### 6.1 POST /v1/agent/chat 内部流程

```
1. 解析请求 { message, session_id?, model?, workspace_id? }
2. 鉴权（MVP 可跳过或固定 API key）
3. 会话：
   - session_id 为空 → session.Store.Create()
   - 否则 → session.Store.Load(session_id) → 重建 AgentContext
4. 环境组装（复用 CLI）：
   env, _ := run.SetupEnv(cwd, model, ...)
   runCfg := run.NewConfig(env, ...)
   run.InstallDriverHooks(ctx, &runCfg, ...)
5. append UserMessage 到 agentCtx.Messages
6. 创建 RunState，生成 run_id
7. goroutine:
   stream := runtime.StartRun(ctx, agentCtx, runCfg)
   runtime.DrainStream(ctx, stream, translateHandler)
   持久化 turn → session.Store.Save(...)
   标记 RunState.Status = done
8. 立即返回 { session_id, run_id }
```

关键：**HTTP 请求快速返回**，SSE 异步消费；与 ChatPage 逻辑一致。

### 6.2 会话持久化

复用 `internal/session` JSONL：

| Web 概念 | pigo 概念 |
|----------|-----------|
| `session_id` | `SessionHeader.ID` |
| `messages[]` | JSONL 行 → `AgentContext.Messages` |
| `active_run` | RunManager 中 status=running 的 run |
| `last_event_seq` | RunState.Events 最后 seq |

`GET /v1/agent/sessions/{id}` 响应需对齐 `SessionRestorePayload`（`web/src/lib/sessionPersist.ts`）：

```go
type SessionDetailResponse struct {
    Session   SessionMeta          `json:"session"`
    Messages  []RestoredMessage    `json:"messages"`
    ActiveRun *ActiveRunInfo       `json:"active_run,omitempty"`
    LastEventSeq int64             `json:"last_event_seq,omitempty"`
}
```

消息格式需从 `agentcore.MessageList` 转为 web 的 `{ id, role, content, model, tool_steps, created_at }`。

### 6.3 工具确认（trust）

REPL/TUI 通过 stdin 或 TUI 弹窗做 `trust.BeforeToolCall` 确认；Web 端暂无等价 UI。

MVP 策略（择一）：

| 策略 | 说明 |
|------|------|
| **A. 自动批准** | gateway 启动时 `--approve`，适合开发/单用户 |
| **B. 拒绝副作用工具** | `--no-tools` 或禁用 bash/write/edit |
| **C. WebSocket 确认** | Phase 2：扩展 SSE 或 WS 发送 confirm 帧 |

推荐 MVP 用 **A + 可配置 cwd 沙箱**。

### 6.4 Coder 模式与 workspace

`CoderPage` 额外依赖：

- `GET/POST /v1/workspaces`
- workspace 上传/下载
- 可选 GitHub 集成

MVP 可简化为：

- `workspace_id` 映射到服务器本地目录（配置项 `gateway.workspaces_root`）
- `run.SetupEnv` 的 `cwd` 设为 workspace 路径
- 文件工具自然在该目录下工作

---

## 7. 关键复用点（避免重复实现）

### 7.1 必须从 CLI 复用的函数

```go
// internal/cli/run/run.go
run.SetupEnv(...)        // provider、tools、skills、plugins、memory
run.NewConfig(...)       // RunConfig 骨架
run.InstallDriverHooks(...) // hooks + plugins
run.ResolveThinkingLevel(...)

// internal/cli/headless/headless.go
openHeadlessSession(resumeID, model, provider, sysPrompt)

// internal/cli/persist.go（或 session 包）
PersistTurn(store, agentCtx, ...)  // turn 结束后写 JSONL
```

### 7.2 可参考但不必直接复用

| 组件 | 原因 |
|------|------|
| `remotecontrol` | 终端字节镜像，非结构化 agent 事件 |
| `headless.Run` 整体 | 阻塞式 stdout，不适合 HTTP 异步模型 |
| `jsonrpc/subagent_rpc` | stdio 子进程协议，非 HTTP |

### 7.3 推荐调用链（单 run）

```go
func (m *RunManager) Start(ctx context.Context, req ChatRequest) (runID, sessionID string, err error) {
    agentCtx, sessionID, err := m.loadOrCreateSession(req)
    env, err := run.SetupEnv(m.opts)
    cfg := run.NewConfig(env, ...)
    _, onEvent := run.InstallDriverHooks(ctx, &cfg, m.slash, m.hookDeps, "gateway", m.notifier)

    runID = newRunID()
    runCtx, cancel := context.WithCancel(ctx)
    state := m.registry.Register(runID, sessionID, cancel)

    go func() {
        defer cancel()
        stream := runtime.StartRun(runCtx, agentCtx, cfg)
        h := translate.NewHandler(state.Publish, req.Model, onEvent)
        _, drainErr := runtime.DrainStream(runCtx, stream, h)
        m.persistSession(sessionID, agentCtx)
        state.Finish(drainErr)
    }()

    return runID, sessionID, nil
}
```

---

## 8. HTTP 服务骨架

### 8.1 路由表（MVP）

```go
mux := http.NewServeMux()

// Agent
mux.HandleFunc("POST /v1/agent/chat", h.HandleChat)
mux.HandleFunc("GET  /v1/agent/runs/{runId}/events", h.HandleRunEvents)
mux.HandleFunc("GET  /v1/agent/sessions/{sessionId}", h.HandleGetSession)

// Stub（Chat 连通性）
mux.HandleFunc("GET /v1/models", h.HandleModels)
mux.HandleFunc("GET /v1/auth/me", h.HandleAuthMe)  // 返回固定用户

// 健康检查
mux.HandleFunc("GET /healthz", func(w,r){ w.WriteHeader(200) })
```

### 8.2 SSE Handler 要点

```go
func (h *Handler) HandleRunEvents(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type", "text/event-stream")
    w.Header().Set("Cache-Control", "no-cache")
    w.Header().Set("Connection", "keep-alive")

    flusher, _ := w.(http.Flusher)
    runID := r.PathValue("runId")
    afterSeq, _ := strconv.ParseInt(r.URL.Query().Get("after_seq"), 10, 64)

    for ev := range h.runs.Subscribe(runID, afterSeq) {
        fmt.Fprintf(w, "data: %s\n\n", ev.JSON())
        flusher.Flush()
    }
    fmt.Fprintf(w, "data: [DONE]\n\n")
    flusher.Flush()
}
```

### 8.3 CORS（开发）

`web/` dev server 在 `:3000`，gateway 在 `:8080`，需启用 CORS 或通过 Vite proxy（已配置，生产需网关统一托管静态资源）。

### 8.4 静态资源（可选）

生产部署：

```
gateway :8080
  /v1/*     → API
  /*        → embed web/dist（go:embed）
```

开发仍用 `cd web && npm run dev` + proxy。

---

## 9. 配置

### 9.1 gateway 配置项（建议 `gateway.toml` 或环境变量）

```toml
[server]
addr = ":8080"

[agent]
cwd = "."                    # 默认工作目录
model = "openrouter/..."     # 默认模型
approve = true               # MVP 自动批准工具
no_tools = false

[session]
dir = ""                     # 默认 ~/.pigo/sessions

[auth]
mode = "none"                # none | token | jwt
api_key = "dev-key"

[workspaces]
root = "./workspaces"        # Coder 模式根目录
```

环境变量可复用 pigo 现有：`OPENROUTER_API_KEY`、`PIGO_HOME` 等。

---

## 10. 分阶段实施计划

### Phase 0 — 脚手架（1–2 天）

- [ ] 创建 `internal/gateway/` 包与 `cmd/gateway/main.go`
- [ ] 实现 `GET /healthz`、`GET /v1/models` stub
- [ ] 本地验证：`go run ./cmd/gateway` + `cd web && npm run dev`

### Phase 1 — Chat MVP（3–5 天）

- [ ] `POST /v1/agent/chat` + `GET /v1/agent/runs/{id}/events`
- [ ] `translate/sse.go`：delta / tool_step / done / error
- [ ] `RunManager`：内存事件 log + after_seq
- [ ] 复用 `run.SetupEnv` + `runtime.StartRun`
- [ ] Chat 页端到端：发消息 → 流式显示

### Phase 2 — 会话持久化（2–3 天）

- [ ] `GET /v1/agent/sessions/{id}` 对齐 `SessionRestorePayload`
- [ ] `GET /v1/agent/sessions` 列表
- [ ] `useSessionRestore` 完整流程（刷新页面恢复对话）
- [ ] turn 结束写 JSONL

### Phase 3 — Coder + Workspace（3–5 天）

- [ ] `/v1/workspaces` CRUD（本地目录）
- [ ] CoderPage 对接 workspace_id
- [ ] 上传/下载（可复用 `web/src/lib/workspaceUpload.ts` 协议）

### Phase 4 — Admin / 多租户（按需）

- [ ] 认证：`/v1/auth/*`
- [ ] Provider 管理：`/v1/admin/providers`
- [ ] 多账户、`X-Account-ID` 路由
- [ ] 对接 `internal/provider` 凭据存储

### Phase 5 — 高级功能（按需）

- GitHub OAuth、Claude OAuth、ASR
- Web 端工具确认（WebSocket）
- Agent Tasks 队列

---

## 11. 测试策略

| 层级 | 内容 |
|------|------|
| 单元测试 | `translate/sse_test.go`：每种 AgentEvent 映射 |
| 集成测试 | `httptest`：POST chat → 读 SSE 直到 done |
| 端到端 | ChatPage + gateway 手工；后续 Playwright |
| 回归 | 确保 `go test ./...` 不因 gateway 包破坏 CLI |

---

## 12. 风险与决策

| 风险 | 缓解 |
|------|------|
| 事件协议长期双轨 | 中期可考虑让 web 直接消费 stream-json，减少 translate 层 |
| 工具确认无 Web UI | MVP `--approve`；Phase 2 加 WS confirm |
| 多租户与 pigo 单用户模型冲突 | gateway 层做 account → provider 路由，不动 agentcore |
| Run 泄漏 / goroutine 泄漏 | RunManager 超时取消；ctx 传播；客户端断开取消 pump |
| session 格式与 web 消息模型不一致 | 单独写 `session_to_api.go` 转换层 |

---

## 13. 与 remotecontrol 的关系

| | remotecontrol | gateway |
|---|---------------|---------|
| 用途 | 远程镜像 CLI 终端 | 结构化 agent API |
| 协议 | WebSocket 文本帧 | HTTP + SSE |
| 事件 | 渲染后字节 | delta / tool_step |
| 用户 | 配对 QR 单客户端 | 多 tab / 多用户（Phase 4） |

两者可并存：开发者在终端用 `/remote-control`，产品用户用 `web/` Chat。

---

## 14. 最小验证命令（目标态）

```bash
# 终端 1：启动 gateway
go run ./cmd/gateway --addr :8080 --approve

# 终端 2：启动前端
cd web && npm run dev

# 浏览器打开 http://localhost:3000
# 发送消息 → 应看到流式回答
```

```bash
# 无前端，curl 验证
curl -s -X POST http://localhost:8080/v1/agent/chat \
  -H 'Content-Type: application/json' \
  -d '{"message":"hello","stream":false}' 

# 用上一步返回的 run_id
curl -N "http://localhost:8080/v1/agent/runs/{run_id}/events?after_seq=0"
```

---

## 15. 参考文件索引

| 主题 | 路径 |
|------|------|
| Web 前端 SSE 消费 | `web/src/hooks/useRunEventStream.ts` |
| Web 会话恢复 | `web/src/hooks/useSessionRestore.ts` |
| Web 事件类型 | `web/src/lib/sessionPersist.ts` |
| Web Chat 流程 | `web/src/pages/ChatPage.tsx` |
| Vite 代理 | `web/vite.config.ts` |
| agent 事件定义 | `internal/agentcore/event.go` |
| 运行入口 | `internal/runtime/loop.go` |
| stream-json 序列化 | `internal/runtime/headless.go` |
| TUI 事件桥接 | `internal/cli/tui/bridge.go` |
| Headless 会话 | `internal/cli/headless/headless.go` |
| 运行组装 | `internal/cli/run/run.go` |
| 会话持久化 | `internal/session/session.go` |
| CLI Host 契约 | `internal/cli/host.go` |
| 远程终端（非本方案） | `internal/remotecontrol/` |

---

## 16. 实现状态

| 阶段 | 状态 | 说明 |
|------|------|------|
| Phase 0 | ✅ | `cmd/gateway` + `internal/gateway`，`/healthz`、`/v1/models`、auth stub |
| Phase 1 | ✅ | `POST /v1/agent/chat` + SSE `.../events` + translate + RunManager |
| Phase 2 | ✅ 基础 | `GET /v1/agent/sessions`、`GET /v1/agent/sessions/{id}` + JSONL 持久化 |
| Phase 3+ | ✅ 接口已齐 | Admin/Workspace/Tags/Tasks 可用；GitHub/Claude OAuth/ASR 为 stub（返回未配置） |

### 启动

```bash
# go-zero rest 入口（配置见 etc/gateway.yaml）
go run ./cmd/gateway -f etc/gateway.yaml

cd web && npm run dev
# 浏览器打开 http://localhost:3000 ，任意账号登录后即可 Chat
```

HTTP 层使用 go-zero `rest.Server`（薄路由/CORS/配置）；`Service` / `RunManager` / `translate` 业务逻辑不变。

Providers 页面写入的通道会持久化到 `~/.pigo/gateway-providers.json`，`POST /v1/agent/chat` 通过 `resolveLLM` 选用默认/匹配模型的 Provider（key、base_url、协议）调用真实 LLM；未配置可用 Provider 时回退到 `etc/gateway.yaml` / 环境变量。
