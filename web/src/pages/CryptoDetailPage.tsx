import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { Activity, AlertCircle, ArrowLeft, Bitcoin, Coins, RefreshCw, Radio } from 'lucide-react'
import CryptoCandlestickChart from '../components/CryptoCandlestickChart'
import { apiFetch, useAccount } from '../context/AccountContext'
import { useCryptoTickerStream } from '../hooks/useCryptoTickerStream'
import {
  CRYPTO_ASSETS,
  type CryptoCandle,
  type CryptoCandleResponse,
  type CryptoExchange,
  type CryptoInterval,
  formatMarketNumber,
  formatSigned,
} from '../lib/stocks'

const CRYPTO_INTERVALS: Array<{ value: CryptoInterval; label: string }> = [
  { value: '1m', label: '1分' },
  { value: '5m', label: '5分' },
  { value: '15m', label: '15分' },
  { value: '1h', label: '1时' },
  { value: '4h', label: '4时' },
  { value: '1d', label: '日线' },
]

function formatCryptoPrice(value: number | null | undefined): string {
  if (typeof value !== 'number' || !Number.isFinite(value)) return '--'
  return value.toLocaleString('en-US', {
    minimumFractionDigits: 2,
    maximumFractionDigits: value >= 1_000 ? 2 : 4,
  })
}

function directionClass(value: number | null | undefined): string {
  if (typeof value !== 'number' || value === 0) return 'text-muted-foreground'
  return value > 0 ? 'text-emerald-400' : 'text-red-400'
}

function displayDateTime(value: string | undefined): string {
  if (!value) return '--'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '--'
  return date.toLocaleString('zh-CN', { hour12: false })
}

async function responseError(response: Response): Promise<string> {
  const body = await response.json().catch(() => ({}))
  return typeof body.error === 'string' && body.error ? body.error : `K 线请求失败（HTTP ${response.status}）`
}

export default function CryptoDetailPage() {
  const { asset = '' } = useParams()
  const { currentAccount } = useAccount()
  const selectedAsset = CRYPTO_ASSETS.find((item) => item.short === asset.toUpperCase())
  const [exchange, setExchange] = useState<CryptoExchange>('binance')
  const [interval, setIntervalValue] = useState<CryptoInterval>('15m')
  const [candles, setCandles] = useState<CryptoCandle[]>([])
  const [fetchedAt, setFetchedAt] = useState('')
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [error, setError] = useState('')
  const requestSequence = useRef(0)
  const streamSymbols = useMemo(() => selectedAsset ? [selectedAsset.symbol] : [], [selectedAsset])
  const { tickers, providers, streamError } = useCryptoTickerStream(currentAccount?.id, streamSymbols)
  const ticker = selectedAsset ? tickers[`${exchange}:${selectedAsset.symbol}`] : undefined

  const loadCandles = useCallback(async (background = false) => {
    if (!currentAccount?.id || !selectedAsset) return
    const sequence = ++requestSequence.current
    if (background) setRefreshing(true)
    else setLoading(true)
    try {
      const params = new URLSearchParams({
        exchange,
        symbol: selectedAsset.symbol,
        interval,
        limit: '300',
      })
      const response = await apiFetch(`/v1/crypto/candles?${params.toString()}`, {}, currentAccount.id)
      if (!response.ok) throw new Error(await responseError(response))
      const data = await response.json() as CryptoCandleResponse
      if (sequence !== requestSequence.current) return
      setCandles(data.candles || [])
      setFetchedAt(data.fetched_at)
      setError('')
    } catch (loadError) {
      if (sequence !== requestSequence.current) return
      setError(loadError instanceof Error ? loadError.message : 'K 线请求失败')
    } finally {
      if (sequence === requestSequence.current) {
        setLoading(false)
        setRefreshing(false)
      }
    }
  }, [currentAccount?.id, exchange, interval, selectedAsset])

  useEffect(() => {
    let active = true
    let timer = 0
    setCandles([])
    setFetchedAt('')
    setError('')
    const poll = async (background: boolean) => {
      await loadCandles(background)
      if (active) timer = window.setTimeout(() => void poll(true), 5_000)
    }
    void poll(false)
    return () => {
      active = false
      window.clearTimeout(timer)
      requestSequence.current++
    }
  }, [loadCandles])

  if (!selectedAsset) {
    return (
      <div className="mx-auto flex min-h-full max-w-3xl flex-col items-center justify-center px-6 py-12 text-center">
        <AlertCircle className="h-7 w-7 text-amber-400" />
        <h2 className="mt-3 text-base font-semibold text-foreground">不支持该数字资产</h2>
        <Link to="/stock" className="mt-4 text-[12px] text-primary hover:underline">返回行情页</Link>
      </div>
    )
  }

  const provider = providers[exchange]

  return (
    <div className="mx-auto w-full max-w-[1500px] px-4 py-5 sm:px-6 lg:px-8">
      <header className="mb-5 flex flex-col gap-4 border-b border-border pb-5 lg:flex-row lg:items-end lg:justify-between">
        <div>
          <Link to="/stock" title="返回行情" className="mb-3 inline-flex h-8 w-8 items-center justify-center rounded-md border border-border text-muted-foreground hover:bg-accent hover:text-foreground">
            <ArrowLeft className="h-4 w-4" />
          </Link>
          <div className="flex items-center gap-3">
            <div className="flex h-10 w-10 items-center justify-center rounded-md bg-amber-500/10 text-amber-400">
              {selectedAsset.short === 'BTC' ? <Bitcoin className="h-5 w-5" /> : <Coins className="h-5 w-5" />}
            </div>
            <div>
              <h2 className="text-xl font-semibold text-foreground">{selectedAsset.name}</h2>
              <p className="mt-0.5 text-[11px] text-muted-foreground">{selectedAsset.symbol} · 公开市场实时行情</p>
            </div>
          </div>
        </div>

        <div className="flex flex-wrap items-center gap-1 rounded-md border border-border bg-card p-1">
          {CRYPTO_ASSETS.map((item) => (
            <Link
              key={item.short}
              to={`/stock/crypto/${item.short}`}
              className={`flex h-8 min-w-14 items-center justify-center rounded px-3 text-[11px] font-medium ${item.short === selectedAsset.short ? 'bg-primary text-primary-foreground' : 'text-muted-foreground hover:bg-accent hover:text-foreground'}`}
            >
              {item.short}
            </Link>
          ))}
        </div>
      </header>

      <section className="mb-5 grid grid-cols-2 border-y border-border sm:grid-cols-3 lg:grid-cols-6">
        <div className="border-b border-r border-border px-3 py-3 sm:border-b-0">
          <p className="text-[9px] text-muted-foreground">最新价格</p>
          <p className="mt-1 text-lg font-semibold tabular-nums text-foreground">{formatCryptoPrice(ticker?.price)}</p>
        </div>
        <div className="border-b border-border px-3 py-3 sm:border-b-0 sm:border-r">
          <p className="text-[9px] text-muted-foreground">24h 涨跌</p>
          <p className={`mt-1 text-sm font-semibold tabular-nums ${directionClass(ticker?.change_percent)}`}>{formatSigned(ticker?.change_percent, '%')}</p>
        </div>
        <div className="border-b border-r border-border px-3 py-3 sm:border-b-0">
          <p className="text-[9px] text-muted-foreground">24h 最高</p>
          <p className="mt-1 text-sm font-medium tabular-nums text-foreground">{formatCryptoPrice(ticker?.high)}</p>
        </div>
        <div className="border-b border-border px-3 py-3 lg:border-b-0 lg:border-r">
          <p className="text-[9px] text-muted-foreground">24h 最低</p>
          <p className="mt-1 text-sm font-medium tabular-nums text-foreground">{formatCryptoPrice(ticker?.low)}</p>
        </div>
        <div className="border-r border-border px-3 py-3">
          <p className="text-[9px] text-muted-foreground">24h 成交量</p>
          <p className="mt-1 text-sm font-medium tabular-nums text-foreground">{formatMarketNumber(ticker?.volume ?? null, selectedAsset.short)}</p>
        </div>
        <div className="px-3 py-3">
          <p className="text-[9px] text-muted-foreground">买一 / 卖一</p>
          <p className="mt-1 text-[11px] font-medium tabular-nums text-foreground">{formatCryptoPrice(ticker?.bid)} / {formatCryptoPrice(ticker?.ask)}</p>
        </div>
      </section>

      <section aria-labelledby="crypto-chart-title" className="overflow-hidden rounded-md border border-border bg-card">
        <div className="flex flex-col gap-3 border-b border-border px-3 py-3 sm:flex-row sm:items-center sm:justify-between">
          <div className="flex items-center gap-3">
            <div>
              <div className="flex items-center gap-2">
                <Activity className="h-4 w-4 text-primary" />
                <h3 id="crypto-chart-title" className="text-[13px] font-semibold text-foreground">实时 K 线</h3>
              </div>
              <div className="mt-1 flex items-center gap-1.5 text-[9px] text-muted-foreground">
                <Radio className={`h-3 w-3 ${provider.state === 'connected' ? 'text-emerald-400' : 'text-amber-400'}`} />
                <span>{exchange.toUpperCase()} {provider.state === 'connected' ? '实时流已连接' : '实时流连接中'}</span>
                <span>·</span>
                <span>{candles.length} 根</span>
              </div>
            </div>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <div className="flex rounded-md border border-border p-0.5">
              {(['binance', 'okx'] as CryptoExchange[]).map((item) => (
                <button key={item} type="button" onClick={() => setExchange(item)} className={`h-7 rounded px-2.5 text-[10px] font-medium uppercase ${exchange === item ? 'bg-accent text-foreground' : 'text-muted-foreground hover:text-foreground'}`}>
                  {item}
                </button>
              ))}
            </div>
            <div className="flex rounded-md border border-border p-0.5">
              {CRYPTO_INTERVALS.map((item) => (
                <button key={item.value} type="button" onClick={() => setIntervalValue(item.value)} className={`h-7 rounded px-2 text-[10px] ${interval === item.value ? 'bg-primary text-primary-foreground' : 'text-muted-foreground hover:text-foreground'}`}>
                  {item.label}
                </button>
              ))}
            </div>
            <button type="button" onClick={() => void loadCandles()} disabled={loading || refreshing} title="刷新 K 线" aria-label="刷新 K 线" className="inline-flex h-8 w-8 items-center justify-center rounded-md border border-border text-muted-foreground hover:bg-accent hover:text-foreground disabled:opacity-50">
              <RefreshCw className={`h-3.5 w-3.5 ${loading || refreshing ? 'animate-spin' : ''}`} />
            </button>
          </div>
        </div>

        {(error || streamError) && (
          <div className="flex items-center gap-2 border-b border-border px-3 py-2 text-[10px] text-amber-400">
            <AlertCircle className="h-3.5 w-3.5 shrink-0" />
            <span>{error || streamError}</span>
          </div>
        )}

        <div className="relative">
          <CryptoCandlestickChart key={`${exchange}-${interval}-${selectedAsset.symbol}`} candles={candles} livePrice={ticker?.price} />
          {loading && candles.length === 0 && (
            <div className="absolute inset-0 flex items-center justify-center bg-card/80 text-[11px] text-muted-foreground">正在加载 K 线...</div>
          )}
          {!loading && candles.length === 0 && !error && (
            <div className="absolute inset-0 flex items-center justify-center text-[11px] text-muted-foreground">暂无 K 线数据</div>
          )}
        </div>
        <div className="flex flex-wrap items-center justify-between gap-2 border-t border-border px-3 py-2 text-[9px] text-muted-foreground">
          <span>蜡烛与成交量来自 {exchange === 'binance' ? 'Binance' : 'OKX'} 公开接口，每 5 秒同步</span>
          <span>更新时间 {displayDateTime(fetchedAt)}{refreshing ? ' · 同步中' : ''}</span>
        </div>
      </section>
    </div>
  )
}
