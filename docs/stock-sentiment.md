# 股票舆情接口

Stock 页面由 Gateway 的 Go 服务提供两类舆情数据，不依赖 Python 运行时：

- 美股社交舆情：Reddit、X、Polymarket，仅支持美股代码。
- 最新新闻舆情：Anspire、Tavily、博查、Brave，支持 A 股、港股、美股、指数和基金。

所有功能均为可选配置。没有配置对应密钥时接口返回 `disabled`，不影响行情功能。

## 新闻舆情配置

至少配置一个 Provider 的环境变量即可启用。变量值支持用英文逗号分隔多个 Key；当前
Key 请求失败时会自动尝试下一个 Key。

```bash
ANSPIRE_API_KEYS=key_one,key_two

# 以下来源均为可选
TAVILY_API_KEY=replace_with_key
BOCHA_API_KEYS=replace_with_key
BRAVE_API_KEY=replace_with_key
```

每个来源同时支持单数和复数形式：

- `ANSPIRE_API_KEYS` / `ANSPIRE_API_KEY`
- `TAVILY_API_KEYS` / `TAVILY_API_KEY`
- `BOCHA_API_KEYS` / `BOCHA_API_KEY`
- `BRAVE_API_KEYS` / `BRAVE_API_KEY`

缓存、最大返回条数和 Provider 地址在 `etc/gateway.yaml` 中配置：

```yaml
MarketData:
  NewsSentiment:
    CacheTTLSeconds: 1800
    MaxResults: 12
    Anspire:
      APIURL: "https://plugin.anspire.cn/api/ntsearch/search"
    Tavily:
      APIURL: "https://api.tavily.com/search"
    Bocha:
      APIURL: "https://api.bocha.cn/v1/web-search"
    Brave:
      APIURL: "https://api.search.brave.com/res/v1/web/search"
```

也可以在各 Provider 下配置逗号分隔的 `APIKeys`，但生产环境不建议把密钥写入仓库。
配置多个 Provider 后会并发请求，某个来源失败不会阻塞其他来源。

## 新闻舆情 API

登录后请求：

```http
GET /v1/stocks/news-sentiment?symbol=1.600519&name=贵州茅台&days=3&limit=12
```

参数说明：

- `symbol`：必填，股票、指数或基金代码，最长 32 个字符。
- `name`：可选，证券名称，用于提高搜索准确率。
- `days`：可选，检索最近天数，默认 3，最大 30。
- `limit`：可选，合并去重后的最大条数，不超过 `MaxResults`。

响应中的 `items` 已统一为标题、摘要、URL、来源和发布时间。相同 URL 会合并，URL 中
常见跟踪参数会被移除，最终按发布时间倒序排列。`providers` 表示命中同一新闻的所有
搜索来源，`diagnostics` 表示各 Provider 的成功或失败状态和耗时。

`sentiment_method: keyword_v1` 是确定性的关键词提示：单条新闻分类为 `positive`、
`negative` 或 `neutral`，再聚合为 `0~100` 分。它不调用 LLM，也不是投资结论；没有
足够命中词时结果通常为中性。

## 美股社交舆情配置与 API

推荐通过服务环境变量提供密钥：

```bash
SOCIAL_SENTIMENT_API_KEY=replace_with_key
SOCIAL_SENTIMENT_API_URL=https://api.adanos.org
```

缓存时间在 YAML 中配置：

```yaml
MarketData:
  StockSentimentAPIURL: "https://api.adanos.org"
  StockSentimentCacheTTLSeconds: 86400
```

登录后请求：

```http
GET /v1/stocks/sentiment?symbols=AAPL,TSLA
```

单次最多查询 10 个代码。支持 `AAPL`、`AAPL.US`、`US:AAPL`、`105.AAPL`、
`106.BRK.B` 等形式。A 股、港股和指数代码会返回 `400`，避免错误消耗请求额度。

第三方 `-1~1` 情绪分按 `(raw + 1) * 50` 转换。热度分不用于推断方向，只作为关注度
展示。综合分 `>=65` 为偏多，`<=35` 为偏空，其余为中性。

## 安全、缓存与持久化

外部帖子和新闻均按不可信数据处理。提供给后续 LLM 的 `analysis_context` 使用以下边界，
并明确要求忽略外部文本中的指令：

- 社交舆情：`BEGIN_UNTRUSTED_STOCK_SENTIMENT` / `END_UNTRUSTED_STOCK_SENTIMENT`
- 新闻舆情：`BEGIN_UNTRUSTED_STOCK_NEWS` / `END_UNTRUSTED_STOCK_NEWS`

美股社交舆情快照存放在 Gateway SQLite 的 `stock_sentiment_snapshots` 表中，新闻舆情缓存
存放在 `stock_news_sentiment_cache` 表中。进程重启后仍可使用未过期缓存；上游全部失败
或密钥被移除时，可以返回最近缓存，并通过 `cached`、`stale`、`status` 和 `message`
标明降级状态。
