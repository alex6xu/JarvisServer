# Skill Registry 与 Stock Latest Digest 设计

> 实施状态（2026-08-23）：Phase 1-3 已完成。已实现 Go 聚合 Tool、服务端自选股、
> Skill Registry/API、账号启停、动态 `skill_load`、通知幂等和 Settings 管理界面。
> Phase 4 的远程 Plugin 安装仍保持为独立的后续安全项目。

## 1. 背景

JarvisServer 已经具备两类扩展基础：

- `internal/runtime/skills.go` 可以解析扁平 Markdown Skill 和
  `<skill-name>/SKILL.md`，并支持 `name`、`description`、
  `allowed-tools`、`model`、`disable-model-invocation`。
- `internal/plugin` 可以启动外部可执行程序，并通过 stdio JSON-RPC 注册
  Tool、Slash Command 和 Agent 生命周期事件。

当前 Gateway 还不能把这些能力完整提供给网页普通对话：

- Chat Tool 白名单只有 `websearch` 和 `webfetch`。
- Skill 依赖通用 `read` Tool 加载正文，而普通 Chat 没有 `read`。
- Stock、新闻舆情、社交舆情和通知是 Gateway 内部 Go Service，尚未封装成
  Agent Tool。
- 通知订阅只支持 `run_done` 和 `run_failed`。
- 自选股保存在浏览器 Local Storage，服务端 Tool 无法读取账号自选股。
- Session 会持久化首次生成的 System Prompt，Skill 修改不会自然更新已有会话。

本文设计一个通用 Skill Registry，并以 `stock-latest-digest` 作为首个完整落地
场景。

## 2. 目标与非目标

### 2.1 目标

- 管理员可以创建、校验、启停、更新和删除自定义 Skill。
- 普通账号可以查看可用 Skill，并按账号启停允许的 Skill。
- Gateway Chat 和 Coder 都能发现 Skill；普通 Chat 不需要获得任意文件读取权限。
- Skill 只能组合当前账号允许的 Tool，不能通过 Markdown 扩大权限。
- 内部业务能力以 Go Tool 暴露，并能获得可信的 `account_id`、`run_id` 和
  `tool_call_id`。
- `stock-latest-digest` 一次 Tool 调用完成行情、新闻、舆情和可选通知推送。
- Skill、Tool 和通知执行都有可审计、可测试的失败语义。

### 2.2 第一阶段非目标

- 不允许普通用户上传或运行任意可执行 Plugin。
- 不在 Skill 正文中保存 API Key、Webhook、Token 等 Secret。
- 不实现公共 Skill 市场、在线安装或自动执行第三方安装脚本。
- 不让 Skill 自己执行 Go 代码；Skill 是工作流指令，Tool 才是执行能力。
- 不把全部 Skill 正文注入每次 System Prompt。

## 3. 总体架构

```text
Settings / Admin API
        |
        v
SkillRegistry ---- Skill files under JARVIS_SKILLS_DIR
        |             + SQLite metadata / account enablement
        |
        +---- Skill catalog ----> System Prompt
        +---- skill_load Tool ---> selected SKILL.md body

User message
    |
    v
Gateway Chat -> LLM -> stock_latest_digest Go Tool
                            |
                            +-> WatchlistRepository
                            +-> StockService
                            +-> CryptoService
                            +-> StockNewsSentimentService
                            +-> StockSentimentService
                            +-> NotificationService.Publish(stock_digest)
```

三个概念必须保持分离：

- **Skill**：说明何时使用能力、缺少参数时如何处理、结果如何组织。
- **Tool**：执行真实业务逻辑，校验参数、权限、超时和幂等。
- **Plugin**：在独立进程中提供额外 Tool。安装属于服务器管理操作。

## 4. Skill Registry

### 4.1 文件布局

第一阶段继续使用文件作为 Skill 正文的事实来源，兼容现有加载器：

```text
${JARVIS_SKILLS_DIR}/
  stock-latest-digest/
    SKILL.md
  weather/
    SKILL.md
```

服务端只创建嵌套布局，不再由管理 API 创建扁平 `*.md`。已有扁平文件仍可只读
导入。

路径安全规则：

- Skill 名称必须通过现有 `^[a-z0-9-]+$` 校验。
- API 根据名称生成路径，不接受客户端传入绝对路径或相对路径。
- 拒绝符号链接和任何解析后逃出 `JARVIS_SKILLS_DIR` 的路径。
- 更新使用同目录临时文件、`fsync` 和原子 `rename`。
- Skill 文件建议 `0600`，目录建议 `0750`，由 Gateway 运行账号持有。
- 删除默认改为禁用；管理员明确删除时移动到 Skills 根目录之外的归档目录。

### 4.2 SQLite 元数据

新增 migration 14：

```sql
CREATE TABLE IF NOT EXISTS skills (
    name TEXT PRIMARY KEY,
    relative_path TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL,
    allowed_tools_json TEXT NOT NULL DEFAULT '[]',
    source TEXT NOT NULL DEFAULT 'custom',
    enabled INTEGER NOT NULL DEFAULT 1,
    revision INTEGER NOT NULL DEFAULT 1,
    content_sha256 TEXT NOT NULL,
    validation_error TEXT NOT NULL DEFAULT '',
    created_by INTEGER REFERENCES accounts(id) ON DELETE SET NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS account_skills (
    account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    skill_name TEXT NOT NULL REFERENCES skills(name) ON DELETE CASCADE,
    enabled INTEGER NOT NULL DEFAULT 1,
    updated_at TEXT NOT NULL,
    PRIMARY KEY(account_id, skill_name)
);
```

第一阶段数据库不保存 Skill 正文。`skills` 保存发现结果、校验状态、内容摘要和
修订号；启动和显式 Reload 时同步文件系统。后续若需要在线版本回滚，再增加
`skill_revisions`，避免第一阶段同时维护两个正文事实来源。

内置 Skill 的 `source` 为 `builtin`。管理员可以禁用内置 Skill，但不能通过 API
覆盖或删除其原始文件。

### 4.3 Go 接口

新增 `internal/gateway/skill_registry.go`：

```go
type SkillRegistry interface {
    List(context.Context, int) ([]SkillSummary, error)
    Snapshot(context.Context, int) (SkillSnapshot, error)
    Validate(context.Context, []byte) (SkillValidation, error)
    Create(context.Context, int, []byte) (SkillSummary, error)
    Update(context.Context, int, string, int64, []byte) (SkillSummary, error)
    SetGlobalEnabled(context.Context, string, bool) error
    SetAccountEnabled(context.Context, int, string, bool) error
    Delete(context.Context, string, int64) error
    Reload(context.Context) (ReloadResult, error)
}
```

`Update` 和 `Delete` 必须携带期望 `revision`，不匹配时返回 `409 Conflict`，防止
两个管理员互相覆盖。

`SkillSnapshot` 是一次 Run 的不可变视图，包含：

- Registry generation。
- 当前账号启用且全局启用的 Skill。
- 已验证的 Frontmatter、正文和内容摘要。
- 经过当前 Tool Policy 过滤后的最终 `allowed-tools`。

文件变化只影响下一次 Run，不改变已经开始的 Run。

### 4.4 校验规则

复用 `runtime.ParseSkill`，并增加 Registry 级校验：

- `name` 必须与目录名相同。
- `description` 必须存在且不超过现有限制。
- 正文不能为空，单个文件默认不超过 256 KiB。
- `allowed-tools` 中每个名称必须存在于系统 Tool Catalog。
- Skill 允许声明系统不存在但当前被禁用的 Tool 时，状态为 `unavailable`，不能
  自动启用；名称拼写错误是校验错误。
- Frontmatter 中的未知字段返回 warning，不静默当作权限配置。
- `disable-model-invocation: true` 的 Skill 只允许显式 `/skill-name` 使用。
- 正文中出现疑似 Secret 只给出 warning；真正的 Secret 必须由服务端配置提供。

### 4.5 加载方式

普通 Chat 不开放通用 `read`。新增只读 Tool `skill_load`：

```json
{
  "type": "object",
  "properties": {
    "name": {"type": "string"}
  },
  "required": ["name"],
  "additionalProperties": false
}
```

它只能返回本次 `SkillSnapshot` 中的 Skill，不能接收路径。Coder 仍可兼容现有
`read` 加载方式，但也优先使用相同的 Registry Snapshot，确保账号启停生效。

System Prompt 只注入名称、描述和 `skill_load` 使用方法。Skill 正文作为 Tool
结果进入上下文，其优先级低于系统安全提示词，不能覆盖 Tool Policy。

显式 `/stock-latest-digest AAPL` 由 Gateway 在调用 LLM 前展开为 Skill 指令和用户
参数，不额外消耗一次 `skill_load` 调用。

### 4.6 Session 与热更新

动态 Skill Catalog 不应继续固化在 Session 的 `SystemPrompt` 字段中。System
Prompt 拆为：

```text
persistent base prompt
    + current environment block
    + current account skill catalog
```

Session 只保存 `persistent base prompt`。每次新 Run 根据 Skill Snapshot 组合最终
Prompt。这样更新 Skill 后已有会话的下一条消息即可看到新 revision，而已经运行
的任务仍使用启动时快照。

兼容旧 Session 时，只剥离由系统生成且完整匹配的 `<available_skills>` 块；不能
对任意用户文本做宽松字符串删除。

## 5. Skill 管理 API

管理员定义管理：

```text
GET    /v1/admin/skills
GET    /v1/admin/skills/:name
POST   /v1/admin/skills/validate
POST   /v1/admin/skills
PUT    /v1/admin/skills/:name
PUT    /v1/admin/skills/:name/status
DELETE /v1/admin/skills/:name?revision=N
POST   /v1/admin/skills/reload
```

账号可见性管理：

```text
GET /v1/skills
PUT /v1/skills/:name/status
```

创建请求示例：

```json
{
  "content": "---\nname: stock-latest-digest\n...",
  "enabled": true
}
```

更新请求示例：

```json
{
  "revision": 3,
  "content": "---\nname: stock-latest-digest\n..."
}
```

所有 `/v1/admin/skills` 路由复用现有 `/v1/admin/` 管理员中间件。普通账号只能
启停管理员已经发布的 Skill，不能编辑正文或安装 Plugin。

## 6. Gateway Tool 装配

### 6.1 装配顺序

Gateway 需要在 Tool Policy 生效前附加账号相关 Tool。建议扩展 `run.SetupEnvAt`
的参数结构，使顺序固定为：

```text
builtin tools
  -> memory tool
  -> external plugin tools
  -> gateway account-scoped tools
  -> validate tool names
  -> apply profile/account tool policy
  -> build skill catalog
```

不能在 `SetupEnvAt` 已经应用 Tool Policy 后直接 `append` Gateway Tool，否则新 Tool
会绕过结构性权限边界。

Chat Profile 第一阶段允许：

```text
websearch
webfetch
memory_search
skill_load
stock_latest_digest
```

`stock_latest_digest` 内部调用 Watchlist、Stock 和 Notification Service，不需要再
向模型暴露 `watchlist_list`、`stock_search`、`notification_publish`。以后有明确的
通用场景再拆分细粒度 Tool。

### 6.2 Tool 请求上下文

Tool 构造时由 Gateway 注入，不接受模型传入以下可信字段：

```go
type GatewayToolContext struct {
    AccountID int
    RunID string
    SessionID string
    WorkspaceID string
    SkillGeneration int64
}
```

`account_id`、`run_id`、渠道目标和 API Key 都不能出现在 Tool JSON Schema 中。

## 7. Stock Latest Digest

### 7.1 聚合服务

新增 `internal/gateway/stock_digest.go`：

```go
type StockDigestService struct {
    stocks        *StockService
    crypto        *CryptoService
    news          *StockNewsSentimentService
    social        *StockSentimentService
    watchlists    WatchlistRepository
    notifications *NotificationService
    store          *GatewayStore
}

func (s *StockDigestService) Latest(
    ctx context.Context,
    accountID int,
    request StockDigestRequest,
) (StockDigestResult, error)
```

聚合和确定性裁剪放在 Go Service 中。LLM 只根据结构化结果写自然语言总结，不负责
计算涨跌幅、去重新闻或判断数据是否过期。

### 7.2 Tool Schema

Tool 名称：`stock_latest_digest`。

```json
{
  "type": "object",
  "properties": {
    "symbols": {
      "type": "array",
      "items": {"type": "string"},
      "maxItems": 10,
      "description": "证券代码、名称或数字货币交易对；为空时使用账号自选股"
    },
    "days": {"type": "integer", "minimum": 1, "maximum": 30, "default": 3},
    "limit": {"type": "integer", "minimum": 1, "maximum": 20, "default": 10},
    "include_sentiment": {"type": "boolean", "default": true},
    "delivery": {
      "type": "string",
      "enum": ["never", "configured", "required"],
      "default": "never"
    }
  },
  "additionalProperties": false
}
```

语义：

- `never`：只返回对话结果。
- `configured`：向启用且订阅 `stock_digest` 的渠道发送；没有渠道时仍视为查询成功。
- `required`：必须至少成功发送到一个订阅渠道，否则 Tool 返回带数据的部分失败。

Skill 只在用户明确要求“推送、通知、发送到手机”，或账号以后启用独立的自动推送
偏好时使用 `configured/required`。普通“搜索最新股票消息”必须使用 `never`。

### 7.3 标的解析

按以下顺序解析：

1. 规范化明确代码，例如 `1.600519`、`AAPL`、`BTC-USDT`。
2. 对名称调用现有 `StockService.Search`，只在唯一或高置信匹配时自动选择。
3. 有多个候选时返回 `needs_clarification` 和候选列表，不猜测。
4. `symbols` 为空时读取账号服务端自选股。
5. 自选股也为空时返回 `needs_input`。

数字货币需要为 `CryptoService` 增加一次性 REST `Tickers` 方法。不能为一次摘要启动
长期 WebSocket Stream；K 线继续复用 `Candles`，首版摘要不默认返回 K 线数组。

### 7.4 数据获取与降级

每个标的的请求使用总超时，内部来源可并发：

```text
quote/ticker ----+
news sentiment --+--> normalize -> freshness -> deterministic digest
social sentiment +
```

建议总超时 15 秒，单来源沿用自己的更短超时。规则：

- 行情失败不影响新闻和舆情返回，但结果状态为 `partial`。
- 新闻源全部未配置时返回 `disabled`，不是空新闻。
- 某个新闻源失败时保留 diagnostics，不把失败解释为“没有负面新闻”。
- 新闻按规范化 URL 和标题去重，保留来源与发布时间。
- 所有价格、新闻和舆情均携带 `fetched_at` 或原始发布时间。
- Go Service 不生成投资建议，只输出事实和数据质量信息。

### 7.5 结果结构

```go
type StockDigestResult struct {
    Status       string                `json:"status"` // ok|partial|needs_input|needs_clarification
    Symbols      []StockDigestItem     `json:"symbols"`
    Diagnostics  []ProviderDiagnostic  `json:"diagnostics"`
    Delivery     DigestDeliveryResult  `json:"delivery"`
    FetchedAt    string                `json:"fetched_at"`
}
```

每个 `StockDigestItem` 至少包含：

- 规范化代码、名称、市场和资产类型。
- 最新报价、涨跌、涨跌幅、更新时间和来源。
- 去重后的新闻条目和新闻情绪结果。
- 可选社交舆情快照。
- `stale`、`partial` 和来源 diagnostics。

Tool 返回结构化 JSON 文本，同时把精简结构放入 `AgentToolResult.Details`，便于未来
前端渲染专用行情组件。

## 8. 服务端自选股

新增 migration 15：

```sql
CREATE TABLE IF NOT EXISTS watchlist_items (
    account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    symbol TEXT NOT NULL,
    code TEXT NOT NULL DEFAULT '',
    name TEXT NOT NULL DEFAULT '',
    market TEXT NOT NULL DEFAULT '',
    asset_type TEXT NOT NULL,
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY(account_id, symbol)
);
```

新增 API：

```text
GET    /v1/stocks/watchlist
POST   /v1/stocks/watchlist
DELETE /v1/stocks/watchlist/:symbol
PUT    /v1/stocks/watchlist/order
```

前端首次加载时，如果服务端为空且 Local Storage 有数据，提示并执行一次幂等迁移；
迁移成功后服务端为事实来源。不要在每次加载时双向合并，否则已删除标的会重新出现。

## 9. 通知扩展

### 9.1 通用发布接口

将只面向 Run 的 `NotifyRun` 扩展为：

```go
type NotificationMessage struct {
    Event string
    Title string
    Body string
    IdempotencyKey string
    Metadata map[string]string
}

func (s *NotificationService) Publish(
    ctx context.Context,
    accountID int,
    message NotificationMessage,
) NotificationPublishResult
```

`NotifyRun` 改为构造 `run_done/run_failed` 消息后调用 `Publish`。新增合法事件
`stock_digest`，复用微信、Telegram 和钉钉发送实现。

### 9.2 推送幂等和审计

新增 migration 16：

```sql
CREATE TABLE IF NOT EXISTS notification_deliveries (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    event TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    channel_kind TEXT NOT NULL,
    status TEXT NOT NULL,
    error TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE(account_id, idempotency_key, channel_kind)
);
```

Stock Tool 使用 `run_id + tool_call_id + digest_hash` 生成幂等键。Provider 重试或
LLM 重放同一个 Tool Call 时，不重复向同一渠道发送。

通知消息包含行情更新时间、新闻来源和“仅供信息参考”提示。渠道 Secret 继续使用
现有加密存储，不出现在 Skill、Tool 参数、日志或 Tool 结果中。

## 10. stock-latest-digest Skill

目标文件：

```text
${JARVIS_SKILLS_DIR}/stock-latest-digest/SKILL.md
```

推荐正文：

```markdown
---
name: stock-latest-digest
description: 查询股票或数字货币的最新行情、新闻和舆情，并在用户明确要求时推送到已配置的通知渠道
allowed-tools:
  - stock_latest_digest
---

# Stock Latest Digest

从用户消息中提取证券名称、代码或数字货币交易对。用户未指定标的时，允许工具读取
账号自选股。未指定时间范围时使用最近 3 天，新闻最多 10 条。

调用 `stock_latest_digest` 获取结构化数据。不要自行编造价格、涨跌幅、新闻时间、
来源或舆情数据。

普通查询将 `delivery` 设置为 `never`。只有用户明确要求“发送到手机”“推送”或
“通知我”时，才设置为 `required`。如果用户表达“有渠道就发，没有也继续”，设置为
`configured`。

回复依次包含最新行情、重要新闻、舆情方向、数据缺失或过期情况和通知发送结果。
当数据源不可用时明确标注，不要把缺失数据解释为中性。
```

Skill 不需要列出 `websearch`。新闻查询由受控的 Stock News Service 完成，避免模型
跳过缓存、来源适配和请求安全限制。

## 11. 配置

沿用当前环境变量：

```ini
JARVIS_SKILLS_DIR=/opt/jarvis/skills
ANSPIRE_API_KEYS=...
TAVILY_API_KEY=...
BOCHA_API_KEYS=...
BRAVE_API_KEY=...
SOCIAL_SENTIMENT_API_KEY=...
```

`etc/gateway.yaml`：

```yaml
Agent:
  NoSkills: false

MarketData:
  NewsSentiment:
    CacheTTLSeconds: 1800
    MaxResults: 12
```

第一阶段不新增 `Skills.Dir` YAML 字段，避免 `JARVIS_SKILLS_DIR` 和 YAML 出现两个
优先级不清晰的目录来源。设置页面显示服务端实际解析出的只读目录。

## 12. 前端设计

Settings 增加两个不嵌套的设置区：

- **Skills**：列表、搜索、状态、来源、修订号、校验状态；管理员可创建和编辑。
- **通知订阅**：在现有完成/失败事件旁增加“股票摘要”。

Skill 编辑使用全屏或宽抽屉式编辑器，提供：

- Frontmatter 表单与 Markdown 正文编辑。
- Tool allow-list 多选，不允许自由输入未知 Tool。
- 校验结果和 warning。
- 保存时携带 revision；冲突时提示重新加载，不自动覆盖。

普通账号只能看到名称、描述和启用开关。Plugin 安装不放在普通 Settings 中。

Stock 页面改为使用服务端 Watchlist API，并保留一次性 Local Storage 迁移入口。

## 13. 安全边界

- 管理员上传 Skill 不等于安装可执行代码。
- Plugin 目录仍由服务器运维管理，不提供普通上传 API。
- `allowed-tools` 只能缩小 Tool 集，不能扩大 Profile 或账号权限。
- `skill_load` 按账号 Snapshot 返回正文，不能读取路径。
- Stock Tool 只能使用构造时注入的账号，忽略模型提供的任何账号标识。
- 通知只发往当前账号已配置、启用且订阅 `stock_digest` 的渠道。
- API Key 和通知 Secret 只从配置或加密数据库读取。
- 新闻正文视为不可信外部数据；进入模型上下文前保留现有不可信内容边界。
- 所有 Tool 输入设置数量、字符串长度、超时和响应体大小上限。

## 14. 可观测性

增加结构化日志字段：

```text
skill_name, skill_revision, skill_generation
tool_name, tool_call_id, account_id, run_id
symbols_count, news_provider, result_status
delivery_event, delivery_channel, delivery_status
```

日志不记录 Skill 全文、新闻全文、API Key、Webhook 或聊天正文。指标至少包括：

- Skill Reload 成功/失败次数。
- Skill 校验失败数。
- Digest 总耗时和各来源耗时。
- Digest `ok/partial/error` 数量。
- 各通知渠道成功率和幂等命中次数。

## 15. 测试策略

### 15.1 单元测试

- Skill 名称、路径、符号链接、大小和 Tool allow-list 校验。
- Registry Reload 对坏文件隔离，合法 Skill 仍可用。
- revision 冲突和原子更新。
- 账号启停不能扩大 Tool Policy。
- `skill_load` 不能加载未授权或不存在的 Skill。
- 股票名称唯一匹配、多候选、空自选股。
- 行情/新闻/舆情部分失败时的 `partial` 结果。
- 通知事件校验、格式化和幂等。

### 15.2 集成测试

- 普通 Chat 从 Skill Catalog 自动调用 `stock_latest_digest`。
- `/stock-latest-digest` 显式调用。
- Skill 更新后已有 Session 的下一次 Run 使用新 revision。
- 不订阅 `stock_digest` 时不推送。
- 微信、Telegram、钉钉分别成功和部分失败。
- Local Storage Watchlist 一次性迁移后不会复活已删除标的。

网络 Provider 测试使用 `httptest.Server` 或 fake Service。部署环境无法访问外部网络时
可以跳过真实连通性测试，但不能跳过参数校验、聚合、权限和通知幂等测试。

## 16. 实施顺序

### Phase 1：内部 Stock Tool

1. 增加服务端 Watchlist 表、Repository、API 和前端迁移。
2. 增加 Crypto REST Tickers。
3. 实现 `StockDigestService` 和 `stock_latest_digest` Tool。
4. 重构 Gateway Tool 装配顺序，确保 Tool Policy 最后统一生效。
5. 为通知增加 `stock_digest`、通用 `Publish` 和幂等表。

完成后，即使没有通用 Skill 管理页面，也可以使用服务器预置的
`stock-latest-digest/SKILL.md` 验证完整流程。

### Phase 2：Skill Registry

1. 增加 Registry 元数据表和账号启停表。
2. 实现扫描、校验、Snapshot、原子写入和 Reload。
3. 实现 `skill_load` 和动态 System Prompt 组合。
4. 增加 Admin/Account API。
5. 处理旧 Session 的动态 Skill Catalog 兼容。

### Phase 3：管理界面

1. Settings 增加 Skill 列表和账号启停。
2. 管理员增加编辑、校验和冲突处理。
3. 通知订阅增加股票摘要。
4. 增加端到端测试和部署文档。

### Phase 4：通用 Plugin 管理强化

1. 增加管理员只读 Plugin 状态页。
2. 配置允许执行文件的固定白名单和内容摘要。
3. 增加启动失败、崩溃和 Tool 清单审计。
4. 是否开放远程安装单独做安全设计，不与 Skill 上传共用接口。

## 17. 验收标准

- 管理员能发布一个合法 Skill，错误 Frontmatter 不会影响其他 Skill。
- 普通 Chat 不拥有通用文件读取权限，也能加载账号允许的 Skill。
- 用户说“搜索 AAPL 最新股票消息”时，模型能调用一次
  `stock_latest_digest` 并返回带时间和来源的摘要。
- 用户说“搜索 AAPL 最新消息并发到手机”时，只向当前账号订阅
  `stock_digest` 的渠道发送一次。
- 未配置新闻 API 时明确显示 `disabled`；单源失败时返回 `partial` 和 diagnostics。
- Tool Policy、账号 Skill 状态和通知订阅任一不允许时，都不能产生越权调用或推送。
- Skill 更新在下一次 Run 生效，不改变正在运行任务使用的 Snapshot。
- 网络不通不影响离线单元测试和权限、聚合、幂等测试通过。
