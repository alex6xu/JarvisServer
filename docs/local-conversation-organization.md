# 本地对话自动分类与项目整理方案

## 1. 目标

在不调用外部 LLM API、不将对话内容发送到第三方的前提下，对用户消息和会话进行本地整理：

1. 用户消息写入后自动打上分类、技术、领域和任务标签；
2. 相同主题的消息可在 Tags 页面集中查看，并可返回原会话继续对话；
3. Coder 会话优先归属于其 Workspace 项目；Chat 会话后续通过本地相似度归入项目；
4. 用户手动修正的项目和标签优先于自动结果；
5. 支持历史消息增量回填，任务可重复执行且不产生重复关联。

## 2. 原则

- **全本地**：MVP 使用关键词、短语、路径和错误码规则，不调用 LLM。
- **账号隔离**：标签、关联、项目和统计全部带 `account_id`。
- **实时但不阻塞回复**：消息成功持久化后执行轻量分类；分类失败不影响聊天。
- **可解释**：每个自动标签保存置信度、来源和命中证据。
- **幂等**：同一消息重复分类采用 Upsert，并按分类器版本更新。
- **渐进增强**：Phase 1 规则分类；Phase 2 项目；Phase 3 TF-IDF；Phase 4 可选 ONNX 小模型。

## 3. 当前代码现状

已有：

- `tags` 表和 `Tag` 类型；
- `/v1/agent/tags`、`overview`、详情和 `retag` 路由；
- `web/src/pages/TagsPage.tsx` 页面；
- SQLite 中的 Session、Session Entry 和 Workspace 账号归属。

缺失：

- Tag 接口目前是空壳；
- 标签没有账号隔离；
- 没有 `message_tags` 关联；
- 没有分类器和实时触发；
- 没有项目/主题模型。

## 4. 分阶段实现

### Phase 1：本地标签 MVP（本次开发范围）

新增：

- `account_tags`：账号标签词典；
- `message_tags`：消息与标签关联，保存置信度、来源、证据和分类器版本；
- 本地规则分类器；
- 用户消息实时自动打标；
- Tags 列表、概览、详情、历史回填真实接口；
- 账号隔离和幂等测试。

分类信号：

- 中英文关键词和短语；
- 错误码与错误词；
- 文件扩展名、路径和技术专有词；
- 会话类型（Chat/Coder）和 Workspace 元数据。

自动标签限制：每条消息最多 3 个 category/task/domain 标签和 5 个 technology/topic 标签。低于阈值的候选不落库。

### Phase 2：项目归档

新增 `projects`、`session_projects`：

- Coder 会话以 Workspace 为主项目，置信度 1.0；
- 用户手动归属可锁定，自动分类不得覆盖；
- Chat 会话先进入 Inbox，重复主题达到阈值后建议项目；
- 项目页展示会话、主题、标签和继续对话入口。

### Phase 3：TF-IDF 与主题聚类

- 中文字符 2/3-gram，英文/代码 Token；
- 2048/4096 维 Feature Hashing TF-IDF；
- 余弦相似度匹配项目和 Topic centroid；
- 在线增量聚类，低置信结果进入 Inbox；
- 用户反馈更新别名、负关键词和 centroid。

### Phase 4：可选本地小模型

可选 INT8 ONNX `bge-small-zh-v1.5`，不可用时自动降级 TF-IDF。单 worker、按需加载，不依赖外部 API。

## 5. Phase 1 数据模型

```sql
CREATE TABLE account_tags (
    id TEXT PRIMARY KEY,
    account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    slug TEXT NOT NULL,
    name TEXT NOT NULL,
    kind TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    source TEXT NOT NULL DEFAULT 'system',
    use_count INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE(account_id, slug)
);

CREATE TABLE message_tags (
    account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    entry_id TEXT NOT NULL,
    tag_id TEXT NOT NULL REFERENCES account_tags(id) ON DELETE CASCADE,
    confidence REAL NOT NULL,
    source TEXT NOT NULL,
    evidence_json TEXT NOT NULL DEFAULT '{}',
    classifier_version INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY(session_id, entry_id, tag_id)
);
```

计数不依赖客户端维护；事务写入关联后，根据 `message_tags` 聚合更新 `use_count`。

## 6. 规则分类器

每条规则包含：

- `slug/name/kind`；
- 中英文关键词/短语；
- 可选正则或结构信号；
- 阈值和基础权重。

评分：

```text
score = 0.45 + 命中信号加权 - 冲突惩罚
```

- 首个强短语：+0.35；
- 额外关键词：每个 +0.12；
- Coder/路径信号：+0.10～0.20；
- 最终限制到 0～1；
- `score >= 0.60` 才保存。

初始词典覆盖：编程开发、调试排错、功能开发、重构、测试、部署运维、认证安全、数据库、前端、后端、AI、GitHub、工作区、文档、硬件、股票、加密货币，以及 Go、React/TypeScript、SQLite、Docker、Linux、Git 等技术主题。

## 7. 实时触发

在 `liveSessionWriter.appendLocked` 成功写入用户消息后：

1. 获得稳定的 `session_id + entry_id`；
2. 从 Session Header 取得 `account_id/workspace_id/type`；
3. 调用本地规则分类器；
4. 在一个 SQLite 事务中 Upsert 标签和关联；
5. 失败只记录日志，不回滚消息写入。

为了避免分类器反向依赖 Gateway Writer，使用小型接口/回调：

```go
type MessageClassificationSink interface {
    ClassifyStoredUserMessage(ctx, header, entry) error
}
```

历史回填遍历当前账号的 Session，只处理 UserMessage，并以相同 Upsert 路径写入。

## 8. API

- `GET /v1/agent/tags?limit=80&kind=...`
- `GET /v1/agent/tags/overview?top=12&per_tag=5`
- `GET /v1/agent/tags/:slug?limit=80`
- `POST /v1/agent/tags/retag?limit=200`

所有接口必须先取得 `requestAccountID`，普通用户只能访问自己；管理员使用 `X-Account-ID` 代管所选账号。

## 9. 验收标准

- 新用户消息保存后可在 Tags 页面看到；
- 不发生任何外部模型/API 调用；
- 两个账号看不到彼此标签和消息；
- 相同消息重复回填不增加重复关联；
- Tag `use_count` 与实际关联数一致；
- 标签详情只返回 UserMessage，不泄露 Thinking、密钥或完整工具输出；
- 单条规则分类 P95 小于 20ms；
- 回填 200 条期间不阻塞聊天；
- 分类失败不影响消息持久化和 Agent Run。

## 10. 后续项目功能

Phase 1 稳定后新增 Project 页面和 Workspace 映射。项目自动创建必须保守：只有相同新主题在 30 天内至少出现 3 个会话、聚类内部相似度达到阈值时才创建；否则进入 Inbox 等用户确认。
