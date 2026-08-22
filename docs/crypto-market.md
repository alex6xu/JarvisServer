# 数字资产实时行情与 K 线

Gateway 使用 Binance 和 OKX 的公开市场接口提供 BTC、ETH、ETC 的 USDT 现货行情，
不需要交易所 API Key。

## 配置

```yaml
MarketData:
  BinanceWSURL: "wss://data-stream.binance.vision/stream"
  OKXWSURL: "wss://ws.okx.com:8443/ws/v5/public"
  BinanceRESTURL: "https://data-api.binance.vision"
  OKXRESTURL: "https://www.okx.com"
```

WebSocket 地址用于最新价格和 24 小时行情，REST 地址用于历史及当前蜡烛。如果服务器
无法直连某个交易所，可以替换为协议兼容的市场数据镜像。

## 实时行情

登录后通过 SSE 订阅：

```http
GET /v1/crypto/stream?symbols=BTC-USDT,ETH-USDT,ETC-USDT
```

服务会同时连接 Binance 和 OKX，并发送 `status` 和 `ticker` 事件。连接中断后自动重连。

## K 线

登录后请求：

```http
GET /v1/crypto/candles?exchange=binance&symbol=BTC-USDT&interval=15m&limit=300
```

参数：

- `exchange`：`binance` 或 `okx`。
- `symbol`：例如 `BTC-USDT`、`ETH-USDT`、`ETC-USDT`。
- `interval`：`1m`、`5m`、`15m`、`1h`、`4h`、`1d`。
- `limit`：默认及最大值均为 300，兼容两个交易所的单次返回限制。

响应把两个交易所统一为按时间正序的 `open`、`high`、`low`、`close`、`volume`、
`turnover` 和 `confirmed`。前端每 5 秒重新获取当前蜡烛，并使用 SSE 最新成交价即时更新
未结束蜡烛的最高、最低和收盘价。

详情页地址：

- `/stock/crypto/BTC`
- `/stock/crypto/ETH`
- `/stock/crypto/ETC`
