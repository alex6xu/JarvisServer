# JarvisServer 已部署服务器实测方案

本文定义对已部署 JarvisServer Gateway 与 Web 的实际验收方法。它补充本地和 CI 测试，重点验证反向代理、认证、Provider 路由、Agent 双层循环、SSE、SQLite 审计、恢复能力和真实运行环境。

## 1. 测试目标

服务器实测必须回答以下问题：

1. Web、Gateway、SQLite 和 Provider 链路是否能在真实部署环境中完整工作。
2. 普通用户、管理员和不同账号之间是否严格隔离。
3. Chat、Coder、代码分析、代码执行和上下文压缩是否选择符合能力与成本策略的 Provider。
4. Chat、Provider 请求、Run、Attempt 和 SSE Event 是否完整记录，且不泄露 API Key。
5. 断线、取消、Provider 失败、Gateway 重启和 SQLite 备份恢复时是否保持一致。
6. 在约定并发和预算内，延迟、错误率、资源使用和 Provider 成本是否可接受。

## 2. 授权和准入信息

执行前由服务器所有者填写以下信息。密码、Bearer Token、Provider Key 和 SSH 私钥不得写入本文档或提交到 Git；应使用临时账号、临时 Token 或独立安全通道提供。

| 项目 | 必填内容 |
| --- | --- |
| 目标地址 | Web HTTPS 地址和 Gateway 地址；通常二者同源 |
| 环境 | production / staging；是否存在真实用户和真实数据 |
| 授权声明 | 确认目标归属及允许测试的域名、IP、时间窗口 |
| 部署版本 | Git commit、Gateway 版本、Web 构建版本、配置版本 |
| 临时身份 | 一个管理员账号和两个普通测试账号，测试后可撤销 |
| Provider | 可测试的 Provider、模型、预算上限和禁止调用的线路 |
| 容量边界 | 最大并发、最大 RPS、最大测试时长、最大 Provider 调用次数或金额 |
| 运维访问 | 可选的只读 SSH、`journalctl`、监控和脱敏 SQLite 查询能力 |
| 变更权限 | 是否允许创建/删除临时账号、Token、Provider、Workspace 和 Route Policy |
| 故障权限 | 是否允许重启 Gateway、断开 Provider、恢复数据库备份 |
| 禁止事项 | 不允许访问的路径、账号、Provider、数据和时间段 |

没有明确授权时，只执行公开页面、`GET /healthz` 和用户明确允许的低流量冒烟测试。

## 3. 风险等级

| 等级 | 允许环境 | 内容 | 默认策略 |
| --- | --- | --- | --- |
| L0 | 生产 | TLS、静态资源、健康检查、单次登录、只读 API | 可直接执行 |
| L1 | 生产或预发布 | 临时账号、少量 Chat、只读 Coder、SSE 重连、审计核对 | 需临时数据和 Provider 预算 |
| L2 | 优先预发布 | Workspace 上传删除、取消 Run、账号隔离、Provider 切换、有限并发 | 需测试窗口和清理权限 |
| L3 | 仅隔离环境 | Gateway 重启、Provider 故障注入、长上下文、压测、备份恢复、安全边界探测 | 每项单独批准 |

生产环境不执行密码爆破、漏洞利用、任意命令执行、数据库破坏、无上限压测或真实 Workspace 写入。

## 4. 测试数据设计

使用固定前缀 `codex-test-<日期>-<随机后缀>` 创建所有临时资源，包括账号、Token、Session、Task、Workspace 和 Route Policy。测试数据必须与真实数据可区分并可批量清理。

准备两个普通账号 `user-a`、`user-b` 和一个临时管理员。Workspace 使用不含凭据的最小项目，至少包含：

- 一个可读取的 README。
- 一个可稳定通过的单元测试。
- 一个带有明确失败测试的独立分支或副本。
- 一个大文本夹具，用于经批准的上下文压缩测试。

Chat 使用短且可判定的提示词；Coder 使用测试 Workspace，不以自然语言回答是否“看起来正确”作为唯一判据，而以文件差异、测试结果、工具事件和审计数据为准。

### 4.1 L0 只读检查命令

以下命令只验证入口、认证和模型列表，不创建 Chat 或修改配置。Token 应通过无回显输入临时放入当前 Shell，不写入脚本和历史记录。

```bash
export JARVIS_TEST_BASE_URL="https://example.test"
read -rsp "Temporary bearer token: " JARVIS_TEST_TOKEN
echo

curl --fail-with-body --silent --show-error \
  "$JARVIS_TEST_BASE_URL/healthz"

curl --fail-with-body --silent --show-error \
  -H "Authorization: Bearer $JARVIS_TEST_TOKEN" \
  "$JARVIS_TEST_BASE_URL/v1/auth/me"

curl --fail-with-body --silent --show-error \
  -H "Authorization: Bearer $JARVIS_TEST_TOKEN" \
  "$JARVIS_TEST_BASE_URL/v1/models"

unset JARVIS_TEST_TOKEN
```

## 5. 分阶段执行

每个阶段结束后先保存证据并判断是否继续。前一阶段出现 P0/P1 问题时停止后续写入、可靠性和容量测试。

### 阶段 A：部署基线与边缘链路（L0）

| ID | 检查 | 验收标准 |
| --- | --- | --- |
| A01 | HTTPS、证书链、域名和重定向 | 无证书错误；HTTP 按部署策略跳转 HTTPS |
| A02 | `GET /healthz` | 返回 2xx；连续 20 次无 5xx |
| A03 | Web 首页和静态资源 | JS/CSS 无 404；刷新任意前端路由不会由 Nginx 返回 404 |
| A04 | `/v1` 反向代理 | API 不返回 Web HTML；保留状态码和 JSON Content-Type |
| A05 | SSE 代理 | 不被代理缓冲或普通请求超时提前关闭 |
| A06 | 安全响应头 | 记录 HSTS、CSP、X-Content-Type-Options 和 Referrer-Policy；缺失项形成问题单 |
| A07 | 进程与目录 | Gateway 使用专用账号；SQLite、配置和 Workspace 权限符合部署文档 |
| A08 | 版本基线 | 记录 commit、二进制校验值、配置摘要、SQLite schema 版本和 Provider 清单 |

### 阶段 B：认证、Token 与账号隔离（L0/L1）

| ID | 检查 | 验收标准 |
| --- | --- | --- |
| B01 | 无 Token 访问受保护 API | 返回 401；响应不暴露内部路径和 SQL |
| B02 | 普通用户登录、`/v1/auth/me`、退出 | 登录产生临时 Token；退出后 Token 失效 |
| B03 | 错误密码和不存在用户 | 不泄露可用于枚举账号的敏感差异；日志不记录密码 |
| B04 | 普通用户访问 `/v1/admin/*` | 全部返回 403，不能依靠前端隐藏菜单实现授权 |
| B05 | 管理员访问管理 API | 允许访问且所有写操作产生可追踪记录 |
| B06 | `X-Account-ID` 篡改 | `user-a` 不能读取、修改或删除 `user-b` 的 Session、Task、Workspace 和日志 |
| B07 | Token 过期、撤销和改密 | 旧 Token 按设计失效；新 Token 可用；明文 Token 不出现在列表和数据库日志中 |
| B08 | 注册策略 | 公开注册行为与生产策略一致；若生产不允许开放注册但接口仍开放，判定为 P1 |

### 阶段 C：Web 关键路径（L1）

使用桌面和移动视口各执行一次，保存关键步骤截图和浏览器控制台日志。

| ID | 页面/流程 | 验收标准 |
| --- | --- | --- |
| C01 | 登录、刷新、退出 | 刷新后会话恢复；失效 Token 返回登录页；无无限跳转 |
| C02 | Dashboard、Accounts、Settings | 数据与 API 一致；普通用户看不到且不能调用管理能力 |
| C03 | Chat | 创建 Session、流式展示、刷新恢复、继续对话均正常 |
| C04 | Coder | 选择测试 Workspace、显示工具步骤、取消和恢复状态正确 |
| C05 | Sessions、Tags、Tasks | 列表、详情、筛选和刷新后的状态一致 |
| C06 | Provider 管理 | Key 不回显；模型拉取、探测、启停和默认设置反馈明确 |
| C07 | Workspace | 上传、列表、下载和删除临时 Workspace；文件名和目录结构保持正确 |
| C08 | 异常反馈 | 401、403、Provider 失败、断网和超时均显示可理解错误，页面不白屏 |
| C09 | 控制台和网络 | 无未处理异常、无限重试、重复 POST、敏感 Header 输出 |

### 阶段 D：Chat、SSE 与 Agent 双层循环（L1/L2）

| ID | 场景 | 验收标准 |
| --- | --- | --- |
| D01 | `mode=chat` 单轮对话 | POST 快速返回 `session_id`、`run_id`；SSE 最终进入唯一终态 |
| D02 | 同 Session 多轮对话 | 历史正确传入；消息顺序一致；不存在重复 assistant 消息 |
| D03 | SSE 中途断开后使用 `after_seq` 重连 | Seq 严格递增；无缺失、无重复；流最终正常结束 |
| D04 | Run 取消 | 取消幂等；5 秒内进入 `cancelled` 或约定终态；Provider 与工具停止继续产生副作用 |
| D05 | 同 Session 并发写入 | 最多一个写入型 Run 被接受或执行；拒绝结果明确 |
| D06 | 内层工具循环 | 工具请求、运行中、完成/失败事件顺序正确；结果只回填一次 |
| D07 | 外层 follow-up 循环 | Follow-up 仅在内层收敛后执行；无消息时唯一出口结束，不空转 |
| D08 | Run 超时 | 达到配置超时后进入 `timed_out`；SSE、SQLite 和页面状态一致 |
| D09 | 页面刷新恢复活跃 Run | Session 详情返回 ActiveRun 和 LastEventSeq；恢复后不重新提交原请求 |

### 阶段 E：Provider 能力路由与故障切换（L1/L3）

至少配置三个独立测试候选：低成本 Chat、强推理/工具模型、性价比 Coding 模型。固定模型请求和 `auto` 路由分别验证。

| ID | Purpose | 验收标准 |
| --- | --- | --- |
| E01 | `chat` | 仅选择 `chat=true`、质量等级至少 1 的候选；策略倾向低成本 |
| E02 | `code_analysis` | 必须支持 Reasoning 和 Tools、质量等级至少 3；策略倾向高质量 |
| E03 | `code_execution` | 必须支持 Coding 和 Tools、质量等级至少 2；策略兼顾成本和质量 |
| E04 | `compaction` | 必须支持 Chat、质量等级至少 2；不占用不必要的高价分析模型 |
| E05 | Context Window 过滤 | 候选窗口至少覆盖估算上下文加 2048 token；不足者不能进入计划 |
| E06 | Route Preview | Preview 的顺序、原因、策略版本和实际 `run_attempts` 一致 |
| E07 | 首 Token 前失败 | 在批准的故障环境中切换到下一候选；Attempt 记录错误分类和耗时 |
| E08 | 已产生输出后失败 | 不透明重放到另一 Provider，避免重复文本和重复工具副作用 |
| E09 | 熔断与恢复 | 连续 3 次可归责失败后线路熔断约 30 秒；恢复后重新参与路由 |
| E10 | 显式模型 | 用户指定模型时不被 `auto` 策略悄悄替换，除非约定的故障切换规则允许 |

LLM 内容本身存在随机性，路由验收以能力过滤、Attempt、Purpose、模型、错误分类、Token 和成本记录为准。

### 阶段 F：智能上下文压缩（L2/L3）

该阶段会产生较多 Token，只在设定预算后执行。分别选择较小和较大 Context Window 的测试 Provider。

| ID | 检查 | 验收标准 |
| --- | --- | --- |
| F01 | 自适应窗口 | `auto` 使用当前模式所有合格候选中的安全窗口，不超过实际候选能力 |
| F02 | 压缩触发 | 接近窗口保留区时触发一次 Compaction，不先收到 Provider 的超窗错误 |
| F03 | 压缩路由 | 产生 `purpose=compaction` 的独立 Attempt，并使用符合低成本策略的候选 |
| F04 | 信息保持 | 摘要保留目标、关键决定、未完成任务和必要文件状态；最近消息仍可用 |
| F05 | Checkpoint | 压缩后 checkpoint 可用于继续 Run；重启后不会从头重复高成本历史 |
| F06 | 失败降级 | 摘要 Provider 失败不会破坏原会话；错误可见且不进入无限压缩循环 |
| F07 | 成本效果 | 对比压缩前后 input token；压缩后的后续请求 Token 明显下降并记录完整 |

### 阶段 G：SQLite、审计与隐私（L1/L2）

对每个测试 Run，用 `session_id`、`run_id` 和 `attempt_id` 做端到端关联。

| ID | 数据 | 验收标准 |
| --- | --- | --- |
| G01 | `sessions`、`session_entries` | 用户消息和最终回复顺序完整，账号归属正确 |
| G02 | `runs`、`run_events` | 唯一终态；Event Seq 严格递增；重连不新增重复事件 |
| G03 | `run_attempts` | 每次 Provider 尝试记录 Purpose、模型、延迟、首 Token、Token 数和错误分类 |
| G04 | `chat_exchanges` | 每次 Chat 请求和最终响应可关联，不遗漏错误与取消请求 |
| G05 | `provider_exchanges` | Provider 请求、响应、状态码、Token 和耗时完整，正文遵守截断上限 |
| G06 | 管理日志 API | `/v1/admin/request-logs` 与 SQLite 抽样一致，普通用户不能读取 |
| G07 | 凭据保护 | API Key、密码、Bearer Token、OAuth Token 不出现在响应正文、错误、日志或截图中 |
| G08 | 保留策略 | 到期审计数据按配置清理；运行中 Run 和未到期 Token 不被误删 |
| G09 | 迁移幂等 | 同一数据库重复启动不会重复执行或破坏迁移；schema 版本单调递增 |

数据库证据优先使用只读连接或备份副本查询。报告只保存计数、ID 和脱敏片段。

### 阶段 H：可靠性与恢复（L2/L3）

| ID | 场景 | 验收标准 |
| --- | --- | --- |
| H01 | 浏览器断网 10 秒后恢复 | SSE 从最后 Seq 继续；页面无重复消息 |
| H02 | 慢客户端 | 不阻塞其他 Session；内存不持续增长；超时行为符合配置 |
| H03 | Gateway 正常重启 | 已提交事件仍可读取；活跃 Run 进入明确终态，不永久显示 running |
| H04 | Provider 超时、429、5xx | 正确分类；仅在安全边界内切换；熔断健康状态更新 |
| H05 | SQLite 锁竞争 | 有限重试或明确错误；无部分写入、无事件乱序、无数据库损坏 |
| H06 | 磁盘空间预警 | 测试监控和停止条件有效；不通过填满生产磁盘验证 |
| H07 | 在线备份 | 备份可完成且不阻断正常只读请求，文件权限不放宽 |
| H08 | 隔离恢复演练 | 新实例从备份启动；账号、Provider 元数据、Session 和审计抽样一致 |

### 阶段 I：受控容量测试（L3）

容量测试只在明确并发、费用和维护窗口后执行。建议按 `1 -> 3 -> 5 -> 10` 并发逐级运行，每级 5 分钟；每级通过后才进入下一级。Provider 调用应使用低成本测试线路或 Mock Provider，业务链路与 Provider 链路分别测量。

初始验收线如下，正式值应由服务器配置和业务 SLO 确认：

| 指标 | 初始验收线 |
| --- | --- |
| `/healthz` 和本地只读 API | 5 并发下 p95 小于 500 ms，5xx 为 0 |
| Chat 创建请求 | p95 小于 1 秒，不包含 Provider 首 Token 时间 |
| SSE 序号正确率 | 100%，不允许缺失或重复 |
| 非预期 Gateway 5xx | 0 |
| Provider TTFT | 按 Provider 分组记录；超过 30 秒无事件判定单次超时 |
| SQLite busy/locked | 0 个未恢复错误 |
| Run 审计完整率 | 100% |
| 进程恢复 | 测试结束后内存回落到基线 +20% 以内，无持续 goroutine 增长迹象 |

### 阶段 J：非破坏性安全检查（L1/L3）

| ID | 检查 | 验收标准 |
| --- | --- | --- |
| J01 | RBAC 与对象归属 | 横向越权、管理员接口绕过均失败 |
| J02 | Workspace 路径 | `../`、绝对路径、符号链接和 Zip Slip 不能逃逸 WorkspacesRoot |
| J03 | Provider Base URL | 私网、回环、云元数据和不允许协议按 SSRF 策略拒绝 |
| J04 | Markdown 与错误文本 | Provider 输出和用户名不能注入可执行脚本 |
| J05 | 请求体边界 | 超大 JSON、压缩包和音频按限制拒绝，不导致内存耗尽 |
| J06 | Header 与日志 | Authorization、Cookie、API Key 和密码被脱敏 |
| J07 | CORS/同源 | 仅允许部署策略定义的 Origin，不能被任意网站调用管理 API |
| J08 | Coder 工具边界 | 未信任 Workspace 不执行写文件或命令；测试仅使用无害固定命令 |

本阶段不自动利用发现的漏洞。出现潜在命令执行、路径逃逸或认证绕过时立即停止，保存最小证据并报告。

## 6. 停止条件

命中任一条件立即停止新增流量和写操作：

- 真实用户受到影响，或出现非测试数据变化。
- 5 分钟窗口内非预期错误率超过 5%，或连续出现 3 个 Gateway 5xx。
- CPU 持续 3 分钟高于 85%，内存高于限制的 80%，磁盘可用空间低于 20%。
- 出现未恢复的 SQLite busy/locked、数据库损坏或事件序号异常。
- API Key、密码、Token 或用户隐私进入响应、日志或测试产物。
- Provider 调用次数、Token 或费用达到约定上限的 80%。
- 发现认证绕过、路径逃逸、任意命令执行或重复工具副作用。
- 实际目标、部署版本或环境与授权范围不一致。

停止后保留现场，不重启、不清库、不修改 Provider，除非恢复步骤已获得授权。

## 7. 证据采集

每个用例至少记录：

- 用例 ID、开始/结束时间、执行人和目标版本。
- 脱敏请求、状态码、关键响应字段和延迟。
- `account_id`、`session_id`、`run_id`、`attempt_id`、Event Seq 范围。
- 浏览器截图、控制台错误和网络请求摘要。
- 对应 Gateway 日志时间窗口和脱敏 SQLite 查询结果。
- Provider、模型、Purpose、首 Token、输入/输出 Token、错误分类和是否切换。
- 实际结果、预期结果、Pass/Fail/Blocked 和问题编号。

测试产物不得包含密钥、完整对话隐私、生产 Workspace 内容或未脱敏数据库副本。

## 8. 缺陷等级与发布判断

| 等级 | 定义 | 发布判断 |
| --- | --- | --- |
| P0 | 数据损坏、密钥泄露、任意命令执行、大面积不可用 | 立即停止，禁止发布 |
| P1 | 认证绕过、跨账号访问、重复副作用、审计丢失、主要流程不可用 | 修复并完整回归后发布 |
| P2 | 有替代路径的功能错误、明显性能退化、恢复不完整 | 明确负责人和期限后决定 |
| P3 | 文案、样式、低影响兼容性和可观测性改进 | 可进入后续迭代 |

发布通过要求：A-D、G 阶段全部通过；Provider 路由至少完成 E01-E08；无未关闭 P0/P1；所有失败用例有证据和问题单。L3 阶段未获授权时标记 `Not Run`，不能写成 `Pass`。

## 9. 首轮执行建议

首轮不要直接做全量压测。建议顺序：

1. 在生产执行 A 和 B 的只读部分，确认入口、版本和授权边界。
2. 使用临时账号执行一次 Chat、一次只读 Coder、一次 SSE 重连并核对审计。
3. 在预发布环境执行账号隔离、Workspace、取消、Provider 故障切换和上下文压缩。
4. 确认监控和费用限制后执行 1/3/5 并发基线；是否进入 10 并发由结果决定。
5. 最后执行 Gateway 重启、SQLite 备份恢复和安全边界测试。

## 10. 首轮测试报告模板

```text
目标：
环境与授权窗口：
部署 commit / 版本：
Gateway / Web / schema 版本：
测试账号与 Provider（仅写脱敏名称）：
允许并发 / 调用次数 / 费用：

执行范围：A / B / C / D / E / F / G / H / I / J
通过：
失败：
阻塞：
未执行及原因：

核心指标：
- healthz p95：
- Chat 创建 p95：
- Provider TTFT p50/p95：
- SSE 缺失/重复：
- Gateway 5xx：
- SQLite busy/locked：
- 审计完整率：
- Provider 调用与费用：

P0/P1 问题：
P2/P3 问题：
清理结果：
发布结论：Go / Conditional Go / No-Go
```

## 11. 清理与复核

测试结束后，通过现有管理 API 撤销临时 Token，删除临时账号、Provider 和 Workspace，并且只恢复本次测试明确修改的 Provider 字段。当前 Session、Task 和 Route Policy 没有公开删除端点：生产环境不得为清理测试数据直接执行 SQL，应使用统一测试前缀保留标记并等待保留策略处理；预发布环境可在批准后恢复测试前快照。

清理前导出所需脱敏证据。清理后使用保留的运维验证账号重新执行 `/healthz`、登录、Provider 列表和一条只读 Chat 冒烟，确认系统回到测试前状态。
