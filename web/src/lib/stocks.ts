export interface StockQuote {
  symbol: string
  code: string
  name: string
  market: string
  price: number | null
  change: number | null
  change_percent: number | null
  open: number | null
  high: number | null
  low: number | null
  previous_close: number | null
  volume: number | null
  turnover: number | null
  updated_at?: string
}

export interface StockSearchResult {
  symbol: string
  code: string
  name: string
  market: string
  type: string
}

export interface StockSentimentMention {
  text: string
  community?: string
  score?: number
  engagement?: number
}

export interface StockSentimentSource {
  provider: 'reddit' | 'x' | 'polymarket'
  status: 'ok' | 'no_data'
  raw_score?: number
  raw_scale?: string
  normalized_score?: number
  buzz_score?: number
  mentions?: number
  trend?: string
  items?: StockSentimentMention[]
}

export interface StockSentimentDiagnostic {
  provider: 'reddit' | 'x' | 'polymarket'
  status: 'ok' | 'cached' | 'no_data' | 'error'
  message: string
  latency_ms: number
}

export interface StockSentimentSnapshot {
  ticker: string
  score: number | null
  label: string
  buzz_score: number | null
  mentions: number
  sources: StockSentimentSource[]
  diagnostics: StockSentimentDiagnostic[]
  fetched_at: string
  expires_at: string
  cached: boolean
  stale: boolean
  analysis_context: string
}

export interface StockSentimentResponse {
  enabled: boolean
  status: 'ok' | 'disabled'
  message?: string
  snapshots: StockSentimentSnapshot[]
}

export type StockNewsProvider = 'anspire' | 'tavily' | 'bocha' | 'brave'

export interface StockNewsItem {
  provider: StockNewsProvider
  providers: StockNewsProvider[]
  title: string
  snippet: string
  url: string
  source: string
  published_at?: string
  tone: 'positive' | 'negative' | 'neutral'
  tone_score: -1 | 0 | 1
}

export interface StockNewsProviderDiagnostic {
  provider: StockNewsProvider
  status: 'ok' | 'error'
  message: string
  item_count: number
  latency_ms: number
}

export interface StockNewsSentimentResponse {
  enabled: boolean
  status: 'ok' | 'degraded' | 'unavailable' | 'disabled'
  message?: string
  symbol: string
  name?: string
  query: string
  sentiment_score: number | null
  sentiment_label: string
  sentiment_method: 'keyword_v1'
  items: StockNewsItem[]
  diagnostics: StockNewsProviderDiagnostic[]
  fetched_at?: string
  expires_at?: string
  cached: boolean
  stale: boolean
  analysis_context: string
}

export interface WatchlistItem {
  symbol: string
  code: string
  name: string
  market: string
  asset_type?: 'stock' | 'crypto'
  sort_order?: number
}

export type CryptoExchange = 'binance' | 'okx'
export type CryptoInterval = '1m' | '5m' | '15m' | '1h' | '4h' | '1d'

export const CRYPTO_ASSETS = [
  { symbol: 'BTC-USDT', name: 'Bitcoin', short: 'BTC' },
  { symbol: 'ETH-USDT', name: 'Ethereum', short: 'ETH' },
  { symbol: 'ETC-USDT', name: 'Ethereum Classic', short: 'ETC' },
] as const

export interface CryptoTicker {
  symbol: string
  exchange: CryptoExchange
  price: number | null
  change: number | null
  change_percent: number | null
  high: number | null
  low: number | null
  volume: number | null
  turnover: number | null
  bid: number | null
  ask: number | null
  updated_at: string
}

export interface CryptoStreamEvent {
  type: 'status' | 'ticker'
  exchange: CryptoExchange
  state?: 'connecting' | 'connected' | 'disconnected'
  message?: string
  ticker?: CryptoTicker
}

export interface CryptoCandle {
  time: number
  open: number
  high: number
  low: number
  close: number
  volume: number
  turnover: number
  confirmed: boolean
}

export interface CryptoCandleResponse {
  symbol: string
  exchange: CryptoExchange
  interval: CryptoInterval
  candles: CryptoCandle[]
  fetched_at: string
}

export interface CryptoLiquidation {
  id: string
  exchange: CryptoExchange
  symbol: 'BTC-USDT-SWAP' | 'ETH-USDT-SWAP'
  side: 'long' | 'short'
  price: number
  quantity: number
  notional: number
  currency: 'USDT'
  notional_estimated?: boolean
  occurred_at: string
  received_at: string
}

export interface CryptoLiquidationStreamEvent {
  type: 'status' | 'liquidation'
  exchange: CryptoExchange
  state?: 'connecting' | 'connected' | 'disconnected'
  message?: string
  liquidation?: CryptoLiquidation
}

export const MARKET_OVERVIEW: WatchlistItem[] = [
  { symbol: '1.000001', code: '000001', name: '上证指数', market: '沪市' },
  { symbol: '0.399001', code: '399001', name: '深证成指', market: '深市' },
  { symbol: '0.399006', code: '399006', name: '创业板指', market: '深市' },
  { symbol: '100.HSI', code: 'HSI', name: '恒生指数', market: '港股指数' },
]

const MAX_WATCHLIST_ITEMS = 26
const SYMBOL_PATTERN = /^[0-9]{1,3}\.[A-Za-z0-9][A-Za-z0-9._-]{0,23}$/

export function stockWatchlistKey(accountId: number): string {
  return `codegateway_stock_watchlist_${accountId}`
}

export function loadStockWatchlist(storage: Pick<Storage, 'getItem'>, accountId: number): WatchlistItem[] {
  try {
    const raw = storage.getItem(stockWatchlistKey(accountId))
    if (!raw) return []
    const parsed: unknown = JSON.parse(raw)
    if (!Array.isArray(parsed)) return []

    const seen = new Set<string>()
    const items: WatchlistItem[] = []
    for (const candidate of parsed) {
      if (!candidate || typeof candidate !== 'object') continue
      const value = candidate as Record<string, unknown>
      const symbol = typeof value.symbol === 'string' ? value.symbol.trim().toUpperCase() : ''
      const code = typeof value.code === 'string' ? value.code.trim() : ''
      const name = typeof value.name === 'string' ? value.name.trim() : ''
      const market = typeof value.market === 'string' ? value.market.trim() : ''
      if (!SYMBOL_PATTERN.test(symbol) || !code || !name || seen.has(symbol)) continue
      seen.add(symbol)
      items.push({ symbol, code, name, market })
      if (items.length >= MAX_WATCHLIST_ITEMS) break
    }
    return items
  } catch {
    return []
  }
}

export function saveStockWatchlist(
  storage: Pick<Storage, 'setItem'>,
  accountId: number,
  items: WatchlistItem[],
): void {
  storage.setItem(stockWatchlistKey(accountId), JSON.stringify(items.slice(0, MAX_WATCHLIST_ITEMS)))
}

export function quoteDirection(value: number | null | undefined): 'up' | 'down' | 'flat' {
  if (typeof value !== 'number' || value === 0) return 'flat'
  return value > 0 ? 'up' : 'down'
}

export function formatPrice(value: number | null | undefined): string {
  if (typeof value !== 'number' || !Number.isFinite(value)) return '--'
  return value.toLocaleString('zh-CN', { minimumFractionDigits: 2, maximumFractionDigits: 3 })
}

export function formatSigned(value: number | null | undefined, suffix = ''): string {
  if (typeof value !== 'number' || !Number.isFinite(value)) return '--'
  const prefix = value > 0 ? '+' : ''
  return `${prefix}${value.toFixed(2)}${suffix}`
}

export function formatMarketNumber(value: number | null | undefined, unit: string): string {
  if (typeof value !== 'number' || !Number.isFinite(value)) return '--'
  const absolute = Math.abs(value)
  if (absolute >= 100_000_000) return `${(value / 100_000_000).toFixed(2)}亿${unit}`
  if (absolute >= 10_000) return `${(value / 10_000).toFixed(2)}万${unit}`
  return `${value.toLocaleString('zh-CN', { maximumFractionDigits: 0 })}${unit}`
}

export function formatSentimentScore(value: number | null | undefined): string {
  if (typeof value !== 'number' || !Number.isFinite(value)) return '--'
  return value.toFixed(1)
}

export function isUSStockSymbol(symbol: string): boolean {
  return /^(105|106|107)\./.test(symbol.trim().toUpperCase())
}
