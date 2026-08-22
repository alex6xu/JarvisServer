import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import {
  Activity,
  AlertCircle,
  Bitcoin,
  Check,
  Clock3,
  Coins,
	ExternalLink,
  LineChart,
  MessageCircle,
	Newspaper,
  Plus,
  RefreshCw,
  Radio,
  Search,
  Trash2,
} from 'lucide-react'
import { apiFetch, useAccount } from '../context/AccountContext'
import { useCryptoTickerStream } from '../hooks/useCryptoTickerStream'
import {
  MARKET_OVERVIEW,
	CRYPTO_ASSETS,
  StockQuote,
  StockSearchResult,
	StockNewsSentimentResponse,
  StockSentimentResponse,
  StockSentimentSnapshot,
  WatchlistItem,
  formatMarketNumber,
  formatPrice,
  formatSentimentScore,
  formatSigned,
	isUSStockSymbol,
  loadStockWatchlist,
	quoteDirection,
	stockWatchlistKey,
} from '../lib/stocks'

const REFRESH_INTERVAL_MS = 30_000
const STALE_AFTER_MS = 90_000
const SENTIMENT_SOURCE_LABELS = {
  reddit: 'Reddit',
  x: 'X',
  polymarket: 'Polymarket',
} as const

const NEWS_PROVIDER_LABELS = {
	anspire: 'Anspire',
	tavily: 'Tavily',
	bocha: 'Bocha',
	brave: 'Brave',
} as const

function newsToneClass(tone: 'positive' | 'negative' | 'neutral'): string {
	if (tone === 'positive') return 'text-red-400'
	if (tone === 'negative') return 'text-emerald-400'
	return 'text-muted-foreground'
}

function directionClass(value: number | null | undefined): string {
  const direction = quoteDirection(value)
  if (direction === 'up') return 'text-red-400'
  if (direction === 'down') return 'text-emerald-400'
  return 'text-muted-foreground'
}

function cryptoDirectionClass(value: number | null | undefined): string {
  const direction = quoteDirection(value)
  if (direction === 'up') return 'text-emerald-400'
  if (direction === 'down') return 'text-red-400'
  return 'text-muted-foreground'
}

function formatCryptoPrice(value: number | null | undefined): string {
  if (typeof value !== 'number' || !Number.isFinite(value)) return '--'
  return value.toLocaleString('en-US', {
    minimumFractionDigits: 2,
    maximumFractionDigits: value >= 1_000 ? 2 : 4,
  })
}

function cryptoSpread(prices: Array<number | null | undefined>): string {
  const available = prices.filter((price): price is number => typeof price === 'number' && Number.isFinite(price))
  if (available.length < 2) return '--'
  const highest = Math.max(...available)
  const lowest = Math.min(...available)
  const middle = (highest + lowest) / 2
  return `${(highest - lowest).toFixed(2)} USDT · ${((highest - lowest) / middle * 100).toFixed(3)}%`
}

function displayTime(value: string | null | undefined): string {
  if (!value) return '--'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '--'
  return date.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit', second: '2-digit' })
}

async function responseError(response: Response, fallback: string): Promise<string> {
  const body = await response.json().catch(() => ({}))
  return typeof body.error === 'string' && body.error ? body.error : fallback
}

export default function StockPage() {
  const { currentAccount } = useAccount()
  const [watchlist, setWatchlist] = useState<WatchlistItem[]>([])
  const [quotes, setQuotes] = useState<Record<string, StockQuote>>({})
  const [query, setQuery] = useState('')
  const [searchResults, setSearchResults] = useState<StockSearchResult[]>([])
  const [searching, setSearching] = useState(false)
  const [searchError, setSearchError] = useState('')
  const [loading, setLoading] = useState(true)
  const [refreshing, setRefreshing] = useState(false)
  const [quoteError, setQuoteError] = useState('')
  const [watchlistError, setWatchlistError] = useState('')
  const [fetchedAt, setFetchedAt] = useState<string | null>(null)
	const [sentimentTicker, setSentimentTicker] = useState('')
	const [sentiment, setSentiment] = useState<StockSentimentSnapshot | null>(null)
	const [sentimentEnabled, setSentimentEnabled] = useState(true)
	const [sentimentLoading, setSentimentLoading] = useState(false)
	const [sentimentError, setSentimentError] = useState('')
	const [newsSymbol, setNewsSymbol] = useState('')
	const [newsSentiment, setNewsSentiment] = useState<StockNewsSentimentResponse | null>(null)
	const [newsLoading, setNewsLoading] = useState(false)
	const [newsError, setNewsError] = useState('')
  const requestSequence = useRef(0)
	const sentimentRequestSequence = useRef(0)
	const newsRequestSequence = useRef(0)
  const {
    tickers: cryptoTickers,
    providers: cryptoProviders,
    streamError: cryptoStreamError,
  } = useCryptoTickerStream(currentAccount?.id, CRYPTO_ASSETS.map((asset) => asset.symbol))

  useEffect(() => {
    const accountId = currentAccount?.id
    if (!accountId) {
      setWatchlist([])
      return
    }
    let active = true
    setWatchlist([])
    setWatchlistError('')
    void apiFetch('/v1/stocks/watchlist', {}, accountId)
      .then(async (response) => {
        if (!response.ok) throw new Error(await responseError(response, '自选股加载失败'))
        const body = await response.json() as { items?: WatchlistItem[] }
        let items = body.items || []
        if (items.length === 0) {
          const localItems = loadStockWatchlist(localStorage, accountId)
          if (localItems.length > 0) {
            const migration = await apiFetch('/v1/stocks/watchlist', {
              method: 'POST',
              body: JSON.stringify({
                items: localItems.map((item, index) => ({ ...item, asset_type: 'stock', sort_order: index })),
              }),
            }, accountId)
            if (!migration.ok) throw new Error(await responseError(migration, '本地自选股迁移失败'))
            const migrated = await migration.json() as { items?: WatchlistItem[] }
            items = migrated.items || []
            localStorage.removeItem(stockWatchlistKey(accountId))
          }
        }
        if (active) {
          setWatchlist(items)
        }
      })
      .catch((error: unknown) => {
        if (active) setWatchlistError(error instanceof Error ? error.message : '自选股加载失败')
      })
    return () => {
      active = false
    }
  }, [currentAccount?.id])

  const symbolsKey = useMemo(() => {
    const symbols = [...MARKET_OVERVIEW, ...watchlist].map((item) => item.symbol)
    return [...new Set(symbols)].join(',')
  }, [watchlist])

	const sentimentCandidates = useMemo(
		() => watchlist.filter((item) => isUSStockSymbol(item.symbol)),
		[watchlist],
	)

	useEffect(() => {
		if (sentimentCandidates.length === 0) {
			setSentimentTicker('')
			setSentiment(null)
			setSentimentError('')
			return
		}
		if (!sentimentCandidates.some((item) => item.code.toUpperCase() === sentimentTicker)) {
			setSentiment(null)
			setSentimentTicker(sentimentCandidates[0].code.toUpperCase())
		}
	}, [sentimentCandidates, sentimentTicker])

	const refreshSentiment = useCallback(async () => {
		if (!currentAccount?.id || !sentimentTicker) return
		const sequence = ++sentimentRequestSequence.current
		setSentimentLoading(true)
		setSentimentError('')
		try {
			const response = await apiFetch(
				`/v1/stocks/sentiment?symbols=${encodeURIComponent(sentimentTicker)}`,
				{},
				currentAccount.id,
			)
			if (!response.ok) throw new Error(await responseError(response, '舆情加载失败'))
			const data = await response.json() as StockSentimentResponse
			if (sequence !== sentimentRequestSequence.current) return
			setSentimentEnabled(data.enabled)
			setSentiment(data.snapshots?.[0] || null)
			if (!data.enabled && data.message) setSentimentError(data.message)
		} catch (error) {
			if (sequence !== sentimentRequestSequence.current) return
			setSentimentError(error instanceof Error ? error.message : '舆情加载失败')
		} finally {
			if (sequence === sentimentRequestSequence.current) setSentimentLoading(false)
		}
	}, [currentAccount?.id, sentimentTicker])

	useEffect(() => {
		void refreshSentiment()
	}, [refreshSentiment])

	const newsCandidate = useMemo(
		() => watchlist.find((item) => item.symbol === newsSymbol) || null,
		[newsSymbol, watchlist],
	)

	useEffect(() => {
		if (watchlist.length === 0) {
			setNewsSymbol('')
			setNewsSentiment(null)
			setNewsError('')
			return
		}
		if (!watchlist.some((item) => item.symbol === newsSymbol)) {
			setNewsSentiment(null)
			setNewsSymbol(watchlist[0].symbol)
		}
	}, [newsSymbol, watchlist])

	const refreshNewsSentiment = useCallback(async () => {
		if (!currentAccount?.id || !newsCandidate) return
		const sequence = ++newsRequestSequence.current
		setNewsLoading(true)
		setNewsError('')
		try {
			const params = new URLSearchParams({
				symbol: newsCandidate.symbol,
				name: newsCandidate.name,
				days: '3',
				limit: '12',
			})
			const response = await apiFetch(`/v1/stocks/news-sentiment?${params.toString()}`, {}, currentAccount.id)
			if (!response.ok) throw new Error(await responseError(response, '新闻舆情加载失败'))
			const data = await response.json() as StockNewsSentimentResponse
			if (sequence !== newsRequestSequence.current) return
			setNewsSentiment(data)
		} catch (error) {
			if (sequence !== newsRequestSequence.current) return
			setNewsError(error instanceof Error ? error.message : '新闻舆情加载失败')
		} finally {
			if (sequence === newsRequestSequence.current) setNewsLoading(false)
		}
	}, [currentAccount?.id, newsCandidate])

	useEffect(() => {
		void refreshNewsSentiment()
	}, [refreshNewsSentiment])

  const refreshQuotes = useCallback(async (background = false) => {
    if (!currentAccount?.id || !symbolsKey) return
    const sequence = ++requestSequence.current
    if (background) setRefreshing(true)
    else setLoading(true)

    try {
      const response = await apiFetch(
        `/v1/stocks/quotes?symbols=${encodeURIComponent(symbolsKey)}`,
        {},
        currentAccount.id,
      )
      if (!response.ok) throw new Error(await responseError(response, '行情加载失败'))
      const data = await response.json() as { quotes?: StockQuote[]; fetched_at?: string }
      if (sequence !== requestSequence.current) return
      const next: Record<string, StockQuote> = {}
      for (const quote of data.quotes || []) next[quote.symbol] = quote
      setQuotes(next)
      setFetchedAt(data.fetched_at || new Date().toISOString())
      setQuoteError('')
    } catch (error) {
      if (sequence !== requestSequence.current) return
      setQuoteError(error instanceof Error ? error.message : '行情加载失败')
    } finally {
      if (sequence === requestSequence.current) {
        setLoading(false)
        setRefreshing(false)
      }
    }
  }, [currentAccount?.id, symbolsKey])

  useEffect(() => {
    void refreshQuotes(false)
    const timer = window.setInterval(() => {
      if (document.visibilityState === 'visible') void refreshQuotes(true)
    }, REFRESH_INTERVAL_MS)
    const handleVisibility = () => {
      if (document.visibilityState === 'visible') void refreshQuotes(true)
    }
    document.addEventListener('visibilitychange', handleVisibility)
    return () => {
      window.clearInterval(timer)
      document.removeEventListener('visibilitychange', handleVisibility)
    }
  }, [refreshQuotes])

  useEffect(() => {
    const keyword = query.trim()
    if (!keyword) {
      setSearchResults([])
      setSearchError('')
      setSearching(false)
      return
    }

    const controller = new AbortController()
    setSearching(true)
    setSearchResults([])
    const timer = window.setTimeout(async () => {
      setSearchError('')
      try {
        const response = await apiFetch(
          `/v1/stocks/search?q=${encodeURIComponent(keyword)}`,
          { signal: controller.signal },
          currentAccount?.id,
        )
        if (!response.ok) throw new Error(await responseError(response, '搜索失败'))
        const data = await response.json() as { results?: StockSearchResult[] }
        setSearchResults(data.results || [])
      } catch (error) {
        if (controller.signal.aborted) return
        setSearchResults([])
        setSearchError(error instanceof Error ? error.message : '搜索失败')
      } finally {
        if (!controller.signal.aborted) setSearching(false)
      }
    }, 300)

    return () => {
      window.clearTimeout(timer)
      controller.abort()
    }
  }, [currentAccount?.id, query])

  const watchlistSymbols = useMemo(() => new Set(watchlist.map((item) => item.symbol)), [watchlist])
  const watchlistFull = watchlist.length >= 26
  const isStale = fetchedAt ? Date.now() - new Date(fetchedAt).getTime() > STALE_AFTER_MS : false

  const addToWatchlist = async (result: StockSearchResult) => {
    if (watchlistFull || !currentAccount?.id || watchlistSymbols.has(result.symbol)) return
    setWatchlistError('')
    try {
      const response = await apiFetch('/v1/stocks/watchlist', {
        method: 'POST',
        body: JSON.stringify({ items: [{
          symbol: result.symbol,
          code: result.code,
          name: result.name,
          market: result.market,
          asset_type: 'stock',
          sort_order: watchlist.length,
        }] }),
      }, currentAccount.id)
      if (!response.ok) throw new Error(await responseError(response, '添加自选股失败'))
      const body = await response.json() as { items?: WatchlistItem[] }
      setWatchlist(body.items || [])
      setQuery('')
      setSearchResults([])
    } catch (error) {
      setWatchlistError(error instanceof Error ? error.message : '添加自选股失败')
    }
  }

  const removeFromWatchlist = async (symbol: string) => {
    if (!currentAccount?.id) return
    setWatchlistError('')
    try {
      const response = await apiFetch(`/v1/stocks/watchlist/${encodeURIComponent(symbol)}`, { method: 'DELETE' }, currentAccount.id)
      if (!response.ok) throw new Error(await responseError(response, '移除自选股失败'))
      setWatchlist((current) => current.filter((item) => item.symbol !== symbol))
    } catch (error) {
      setWatchlistError(error instanceof Error ? error.message : '移除自选股失败')
    }
  }

  return (
    <div className="min-h-full bg-background">
      <header className="border-b border-border px-4 py-4 sm:px-6">
        <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <div>
            <div className="flex items-center gap-2">
              <LineChart className="h-5 w-5 text-primary" aria-hidden="true" />
              <h2 className="text-base font-semibold text-foreground">Stock 行情</h2>
            </div>
            <p className="mt-1 text-[12px] text-muted-foreground">
              {currentAccount?.username || '当前账户'} · {fetchedAt ? `更新于 ${displayTime(fetchedAt)}` : '正在获取最新行情'}
            </p>
          </div>
          <button
            type="button"
            onClick={() => void refreshQuotes(true)}
            disabled={refreshing || loading}
            title="刷新行情"
            aria-label="刷新行情"
            className="inline-flex h-9 w-9 items-center justify-center self-end rounded-md border border-border text-muted-foreground transition-colors hover:bg-accent hover:text-foreground disabled:cursor-not-allowed disabled:opacity-50 sm:self-auto"
          >
            <RefreshCw className={`h-4 w-4 ${refreshing ? 'animate-spin' : ''}`} />
          </button>
        </div>
      </header>

      <div className="space-y-7 px-4 py-5 sm:px-6">
        {(quoteError || watchlistError) && (
          <div className="flex items-start gap-2 rounded-md border border-amber-500/30 bg-amber-500/10 px-3 py-2.5 text-[12px] text-amber-300">
            <AlertCircle className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
            <span>{watchlistError || quoteError}{quoteError && Object.keys(quotes).length > 0 ? '，当前展示上次获取的数据。' : ''}</span>
          </div>
        )}

        <section aria-labelledby="market-overview-title">
          <div className="mb-3 flex items-center justify-between">
            <h3 id="market-overview-title" className="text-sm font-semibold text-foreground">市场概览</h3>
            <div className={`flex items-center gap-1.5 text-[11px] ${isStale ? 'text-amber-400' : 'text-muted-foreground'}`}>
              <Clock3 className="h-3.5 w-3.5" aria-hidden="true" />
              {isStale ? '数据可能已过期' : '30 秒自动刷新'}
            </div>
          </div>
          <div className="grid grid-cols-1 gap-2 sm:grid-cols-2 xl:grid-cols-4">
            {MARKET_OVERVIEW.map((item) => {
              const quote = quotes[item.symbol]
              return (
                <div key={item.symbol} className="rounded-lg border border-border bg-card px-4 py-3.5">
                  <div className="flex items-start justify-between gap-3">
                    <div className="min-w-0">
                      <p className="truncate text-[13px] font-medium text-foreground">{quote?.name || item.name}</p>
                      <p className="mt-0.5 text-[10px] text-muted-foreground">{item.code}</p>
                    </div>
                    <span className="rounded border border-border px-1.5 py-0.5 text-[10px] text-muted-foreground">
                      {quote?.market || item.market}
                    </span>
                  </div>
                  <div className="mt-3 flex items-end justify-between gap-3">
                    <span className={`text-xl font-semibold tabular-nums ${directionClass(quote?.change_percent)}`}>
                      {loading && !quote ? '...' : formatPrice(quote?.price)}
                    </span>
                    <div className={`text-right text-[11px] tabular-nums ${directionClass(quote?.change_percent)}`}>
                      <p>{formatSigned(quote?.change)}</p>
                      <p>{formatSigned(quote?.change_percent, '%')}</p>
                    </div>
                  </div>
                </div>
              )
            })}
          </div>
        </section>

		<section aria-labelledby="sentiment-title" className="border-y border-border py-5">
			<div className="mb-4 flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between">
				<div>
					<div className="flex items-center gap-2">
						<Activity className="h-4 w-4 text-cyan-400" aria-hidden="true" />
						<h3 id="sentiment-title" className="text-sm font-semibold text-foreground">舆情分析</h3>
					</div>
					<p className="mt-1 text-[11px] text-muted-foreground">美股社交情绪 · Reddit / X / Polymarket</p>
				</div>
				<div className="flex items-center gap-2">
					<select
						value={sentimentTicker}
						onChange={(event) => {
							setSentiment(null)
							setSentimentTicker(event.target.value)
						}}
						disabled={sentimentCandidates.length === 0}
						aria-label="选择舆情分析股票"
						className="h-8 min-w-36 rounded-md border border-border bg-card px-2 text-[11px] text-foreground outline-none focus:border-primary disabled:opacity-50"
					>
						{sentimentCandidates.length === 0 ? <option value="">暂无美股自选</option> : null}
						{sentimentCandidates.map((item) => (
							<option key={item.symbol} value={item.code.toUpperCase()}>{item.name} · {item.code}</option>
						))}
					</select>
					<button
						type="button"
						onClick={() => void refreshSentiment()}
						disabled={!sentimentTicker || sentimentLoading}
						title="刷新舆情"
						aria-label="刷新舆情"
						className="inline-flex h-8 w-8 items-center justify-center rounded-md border border-border text-muted-foreground transition-colors hover:bg-accent hover:text-foreground disabled:opacity-50"
					>
						<RefreshCw className={`h-3.5 w-3.5 ${sentimentLoading ? 'animate-spin' : ''}`} />
					</button>
				</div>
			</div>

			{sentimentError ? (
				<div className="mb-3 flex items-center gap-2 text-[11px] text-amber-400">
					<AlertCircle className="h-3.5 w-3.5 shrink-0" aria-hidden="true" />
					<span>{sentimentError}</span>
				</div>
			) : null}

			{sentiment ? (
				<div className="grid grid-cols-1 gap-4 xl:grid-cols-[15rem_minmax(0,1fr)]">
					<div className="rounded-md border border-border bg-card p-4">
						<div className="flex items-start justify-between gap-3">
							<div>
								<p className="text-[10px] uppercase text-muted-foreground">综合情绪</p>
								<p className="mt-1 text-3xl font-semibold tabular-nums text-foreground">
									{formatSentimentScore(sentiment.score)}
									<span className="ml-1 text-[11px] font-normal text-muted-foreground">/ 100</span>
								</p>
							</div>
							<span className="rounded border border-border px-2 py-1 text-[11px] text-cyan-300">{sentiment.label}</span>
						</div>
						<div className="mt-4 h-1.5 overflow-hidden rounded bg-muted">
							<div className="h-full bg-cyan-400" style={{ width: `${Math.max(0, Math.min(100, sentiment.score ?? 0))}%` }} />
						</div>
						<div className="mt-4 grid grid-cols-2 gap-3 text-[10px] text-muted-foreground">
							<p>热度 <span className="block pt-1 text-[13px] tabular-nums text-foreground">{formatSentimentScore(sentiment.buzz_score)}</span></p>
							<p>提及 <span className="block pt-1 text-[13px] tabular-nums text-foreground">{sentiment.mentions.toLocaleString('zh-CN')}</span></p>
						</div>
						<p className="mt-4 text-[9px] leading-4 text-muted-foreground">
							{sentiment.cached ? '缓存数据' : '实时获取'} · {displayTime(sentiment.fetched_at)}{sentiment.stale ? ' · 已过期' : ''}
						</p>
					</div>

					<div className="grid grid-cols-1 gap-2 md:grid-cols-3">
						{sentiment.sources.map((source) => {
							const diagnostic = sentiment.diagnostics.find((item) => item.provider === source.provider)
							return (
								<div key={source.provider} className="min-w-0 rounded-md border border-border bg-card px-3 py-3">
									<div className="flex items-center justify-between gap-2">
										<span className="text-[12px] font-medium text-foreground">{SENTIMENT_SOURCE_LABELS[source.provider]}</span>
										<span className={`h-1.5 w-1.5 rounded-full ${source.status === 'ok' ? 'bg-emerald-400' : diagnostic?.status === 'error' ? 'bg-red-400' : 'bg-muted-foreground'}`} />
									</div>
									<p className="mt-3 text-xl font-semibold tabular-nums text-foreground">{formatSentimentScore(source.normalized_score)}</p>
									<p className="mt-1 truncate text-[9px] text-muted-foreground" title={diagnostic?.message}>
										{source.raw_score !== undefined ? `原始 ${source.raw_score} (${source.raw_scale})` : diagnostic?.message || '暂无情绪分'}
									</p>
									<div className="mt-3 flex justify-between text-[9px] text-muted-foreground">
										<span>热度 {formatSentimentScore(source.buzz_score)}</span>
										<span>{diagnostic?.latency_ms ? `${diagnostic.latency_ms} ms` : diagnostic?.status || source.status}</span>
									</div>
								</div>
							)
						})}
					</div>
					{sentiment.sources.flatMap((source) => source.items || []).length > 0 ? (
						<div className="xl:col-start-2">
							<div className="mb-2 flex items-center gap-1.5 text-[10px] text-muted-foreground">
								<MessageCircle className="h-3.5 w-3.5" aria-hidden="true" />
								<span>热门讨论</span>
							</div>
							<div className="divide-y divide-border border-y border-border">
								{sentiment.sources.flatMap((source) => source.items || []).slice(0, 5).map((item, index) => (
									<div key={`${item.text}-${index}`} className="py-2.5 text-[11px] leading-5 text-secondary-foreground">
										<p>{item.text}</p>
										<p className="mt-0.5 text-[9px] text-muted-foreground">{item.community ? `r/${item.community}` : '外部来源'}{item.engagement ? ` · ${item.engagement} 互动` : ''}</p>
									</div>
								))}
							</div>
						</div>
					) : null}
				</div>
			) : (
				<div className="flex min-h-24 items-center justify-center text-[11px] text-muted-foreground">
					{sentimentLoading ? '正在加载舆情...' : sentimentCandidates.length === 0 ? '暂无美股自选' : sentimentEnabled ? '暂无舆情数据' : '舆情服务未配置'}
				</div>
			)}

			<div className="mt-6 border-t border-border pt-5">
				<div className="mb-4 flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between">
					<div>
						<div className="flex items-center gap-2">
							<Newspaper className="h-4 w-4 text-amber-400" aria-hidden="true" />
							<h4 className="text-[13px] font-semibold text-foreground">最新新闻舆情</h4>
						</div>
						<div className="mt-1 flex flex-wrap items-center gap-1.5">
							{newsSentiment?.diagnostics.map((diagnostic) => (
								<span key={diagnostic.provider} title={diagnostic.message} className="inline-flex items-center gap-1 text-[9px] text-muted-foreground">
									<span className={`h-1.5 w-1.5 rounded-full ${diagnostic.status === 'ok' ? 'bg-emerald-400' : 'bg-red-400'}`} />
									{NEWS_PROVIDER_LABELS[diagnostic.provider]} · {diagnostic.item_count}
								</span>
							))}
							{!newsSentiment?.diagnostics.length ? <span className="text-[9px] text-muted-foreground">Anspire / Tavily / Bocha / Brave</span> : null}
						</div>
					</div>
					<div className="flex items-center gap-2">
						<select
							value={newsSymbol}
							onChange={(event) => {
								setNewsSentiment(null)
								setNewsSymbol(event.target.value)
							}}
							disabled={watchlist.length === 0}
							aria-label="选择新闻舆情证券"
							className="h-8 min-w-36 rounded-md border border-border bg-card px-2 text-[11px] text-foreground outline-none focus:border-primary disabled:opacity-50"
						>
							{watchlist.length === 0 ? <option value="">暂无自选证券</option> : null}
							{watchlist.map((item) => <option key={item.symbol} value={item.symbol}>{item.name} · {item.code}</option>)}
						</select>
						<button
							type="button"
							onClick={() => void refreshNewsSentiment()}
							disabled={!newsCandidate || newsLoading}
							title="刷新新闻舆情"
							aria-label="刷新新闻舆情"
							className="inline-flex h-8 w-8 items-center justify-center rounded-md border border-border text-muted-foreground transition-colors hover:bg-accent hover:text-foreground disabled:opacity-50"
						>
							<RefreshCw className={`h-3.5 w-3.5 ${newsLoading ? 'animate-spin' : ''}`} />
						</button>
					</div>
				</div>

				{newsError || newsSentiment?.message ? (
					<div className="mb-3 flex items-center gap-2 text-[11px] text-amber-400">
						<AlertCircle className="h-3.5 w-3.5 shrink-0" aria-hidden="true" />
						<span>{newsError || newsSentiment?.message}</span>
					</div>
				) : null}

				{newsSentiment ? (
					<div>
						<div className="mb-3 flex flex-wrap items-center gap-x-4 gap-y-2 border-y border-border py-2.5 text-[10px] text-muted-foreground">
							<span>关键词情绪 <strong className="ml-1 text-[13px] font-semibold tabular-nums text-foreground">{formatSentimentScore(newsSentiment.sentiment_score)}</strong></span>
							<span className={newsSentiment.sentiment_score !== null && newsSentiment.sentiment_score >= 60 ? 'text-red-400' : newsSentiment.sentiment_score !== null && newsSentiment.sentiment_score <= 40 ? 'text-emerald-400' : 'text-muted-foreground'}>{newsSentiment.sentiment_label}</span>
							<span>{newsSentiment.items.length} 条去重结果</span>
							<span>{newsSentiment.cached ? '缓存' : '实时'} · {displayTime(newsSentiment.fetched_at)}{newsSentiment.stale ? ' · 已过期' : ''}</span>
						</div>
						{newsSentiment.items.length > 0 ? (
							<div className="divide-y divide-border border-b border-border">
								{newsSentiment.items.map((item) => (
									<div key={item.url} className="grid gap-2 py-3 sm:grid-cols-[minmax(0,1fr)_auto] sm:items-start">
										<div className="min-w-0">
											<div className="flex min-w-0 items-start gap-2">
												<span className={`mt-0.5 shrink-0 text-[9px] font-medium uppercase ${newsToneClass(item.tone)}`}>{item.tone === 'positive' ? '正面' : item.tone === 'negative' ? '负面' : '中性'}</span>
												<p className="min-w-0 text-[12px] font-medium leading-5 text-foreground">{item.title}</p>
											</div>
											{item.snippet ? <p className="mt-1 line-clamp-2 text-[10px] leading-5 text-muted-foreground">{item.snippet}</p> : null}
											<p className="mt-1 text-[9px] text-muted-foreground">{item.source || '未知来源'}{item.published_at ? ` · ${item.published_at}` : ''} · {item.providers.map((provider) => NEWS_PROVIDER_LABELS[provider]).join(' / ')}</p>
										</div>
										<a href={item.url} target="_blank" rel="noopener noreferrer" title="打开新闻" aria-label={`打开新闻：${item.title}`} className="inline-flex h-7 w-7 items-center justify-center rounded-md text-muted-foreground hover:bg-accent hover:text-foreground">
											<ExternalLink className="h-3.5 w-3.5" />
										</a>
									</div>
								))}
							</div>
						) : <div className="flex min-h-20 items-center justify-center text-[11px] text-muted-foreground">暂无最新新闻舆情</div>}
					</div>
				) : <div className="flex min-h-20 items-center justify-center text-[11px] text-muted-foreground">{newsLoading ? '正在获取最新新闻...' : watchlist.length === 0 ? '暂无自选证券' : '等待新闻舆情数据'}</div>}
			</div>
		</section>

        <section aria-labelledby="crypto-market-title">
          <div className="mb-3 flex flex-col gap-2 sm:flex-row sm:items-end sm:justify-between">
            <div>
              <div className="flex items-center gap-2">
                <Bitcoin className="h-4 w-4 text-amber-400" aria-hidden="true" />
                <h3 id="crypto-market-title" className="text-sm font-semibold text-foreground">数字资产实时行情</h3>
              </div>
              <p className="mt-1 text-[11px] text-muted-foreground">BTC / ETH / ETC 现货 · USDT 计价</p>
            </div>
            <div className="flex items-center gap-2">
              {(['binance', 'okx'] as const).map((exchange) => {
                const provider = cryptoProviders[exchange]
                const connected = provider.state === 'connected'
                return (
                  <span
                    key={exchange}
                    title={provider.message || `${exchange} ${provider.state}`}
                    className="inline-flex h-6 items-center gap-1.5 rounded border border-border px-2 text-[10px] uppercase text-muted-foreground"
                  >
                    <span className={`h-1.5 w-1.5 rounded-full ${connected ? 'bg-emerald-400' : provider.state === 'connecting' ? 'bg-amber-400' : 'bg-red-400'}`} />
                    {exchange}
                  </span>
                )
              })}
            </div>
          </div>

          {cryptoStreamError && (
            <div className="mb-2 flex items-center gap-2 text-[11px] text-amber-400">
              <AlertCircle className="h-3.5 w-3.5 shrink-0" aria-hidden="true" />
              <span>{cryptoStreamError}，正在自动重连。</span>
            </div>
          )}

          <div className="grid grid-cols-1 gap-2 xl:grid-cols-2">
            {CRYPTO_ASSETS.map((asset) => {
              const binance = cryptoTickers[`binance:${asset.symbol}`]
              const okx = cryptoTickers[`okx:${asset.symbol}`]
              const updates = [binance?.updated_at, okx?.updated_at]
                .filter((value): value is string => Boolean(value))
                .sort()
              const latestUpdate = updates[updates.length - 1]
              return (
                <div key={asset.symbol} className="overflow-hidden rounded-lg border border-border bg-card">
                  <div className="flex items-center justify-between border-b border-border px-4 py-3">
                    <div className="flex items-center gap-2.5">
                      <div className="flex h-8 w-8 items-center justify-center rounded-md bg-amber-500/10 text-amber-400">
                        {asset.short === 'BTC' ? <Bitcoin className="h-4 w-4" /> : <Coins className="h-4 w-4" />}
                      </div>
                      <div>
                        <Link to={`/stock/crypto/${asset.short}`} className="group inline-flex items-center gap-1 text-[13px] font-medium text-foreground hover:text-primary">
                          {asset.name}
                          <LineChart className="h-3.5 w-3.5 text-muted-foreground group-hover:text-primary" aria-hidden="true" />
                        </Link>
                        <p className="text-[10px] text-muted-foreground">{asset.symbol} · 实时 K 线</p>
                      </div>
                    </div>
                    <div className="text-right">
                      <p className="text-[10px] text-muted-foreground">交易所价差</p>
                      <p className="mt-0.5 text-[11px] font-medium tabular-nums text-foreground">
                        {cryptoSpread([binance?.price, okx?.price])}
                      </p>
                    </div>
                  </div>

                  <div className="divide-y divide-border">
                    {([
                      { id: 'binance', label: 'Binance', ticker: binance },
                      { id: 'okx', label: 'OKX', ticker: okx },
                    ] as const).map((row) => {
                      const status = cryptoProviders[row.id]
                      return (
                        <div key={row.id} className="px-4 py-3">
                          <div className="grid grid-cols-[5.5rem_minmax(0,1fr)_5rem] items-center gap-2">
                            <div className="flex items-center gap-1.5">
                              <Radio className={`h-3.5 w-3.5 ${status.state === 'connected' ? 'text-emerald-400' : 'text-muted-foreground'}`} aria-hidden="true" />
                              <span className="text-[11px] font-medium text-foreground">{row.label}</span>
                            </div>
                            <p className="truncate text-right text-[15px] font-semibold tabular-nums text-foreground">
                              {row.ticker ? formatCryptoPrice(row.ticker.price) : (
                                <span className={status.state === 'disconnected' ? 'text-[10px] font-normal text-red-400' : 'text-[10px] font-normal text-muted-foreground'}>
                                  {status.state === 'disconnected' ? '重连中' : '连接中'}
                                </span>
                              )}
                              {row.ticker && <span className="ml-1 text-[9px] font-normal text-muted-foreground">USDT</span>}
                            </p>
                            <p className={`text-right text-[11px] font-medium tabular-nums ${cryptoDirectionClass(row.ticker?.change_percent)}`}>
                              {formatSigned(row.ticker?.change_percent, '%')}
                            </p>
                          </div>
                          <div className="mt-2 grid grid-cols-3 gap-2 text-[9px] text-muted-foreground">
                            <p>24h 高 <span className="ml-1 tabular-nums text-foreground/80">{formatCryptoPrice(row.ticker?.high)}</span></p>
                            <p>24h 低 <span className="ml-1 tabular-nums text-foreground/80">{formatCryptoPrice(row.ticker?.low)}</span></p>
                            <p className="text-right">买 / 卖 <span className="ml-1 tabular-nums text-foreground/80">{formatCryptoPrice(row.ticker?.bid)} / {formatCryptoPrice(row.ticker?.ask)}</span></p>
                          </div>
                        </div>
                      )
                    })}
                  </div>
                  <div className="flex items-center justify-between border-t border-border bg-muted/20 px-4 py-2 text-[9px] text-muted-foreground">
                    <span>公开 WebSocket 行情</span>
                    <span className="flex items-center gap-1">
                      <Radio className="h-3 w-3" aria-hidden="true" />
                      {latestUpdate ? displayTime(latestUpdate) : '等待实时数据'}
                    </span>
                  </div>
                </div>
              )
            })}
          </div>
        </section>

        <section aria-labelledby="watchlist-title">
          <div className="mb-3 flex flex-col gap-3 sm:flex-row sm:items-end sm:justify-between">
            <div>
              <h3 id="watchlist-title" className="text-sm font-semibold text-foreground">我的自选</h3>
              <p className="mt-1 text-[11px] text-muted-foreground">按名称、代码或拼音搜索，最多添加 26 项</p>
            </div>
            <div className="relative w-full sm:w-80">
              <Search className="pointer-events-none absolute left-3 top-2.5 h-4 w-4 text-muted-foreground" aria-hidden="true" />
              <input
                value={query}
                onChange={(event) => setQuery(event.target.value)}
                placeholder="搜索股票或指数"
                aria-label="搜索股票或指数"
                className="h-9 w-full rounded-md border border-border bg-card pl-9 pr-3 text-[13px] text-foreground outline-none transition-colors placeholder:text-muted-foreground focus:border-primary focus:ring-1 focus:ring-primary"
              />
              {query.trim() && (
                <div className="absolute right-0 top-11 z-20 max-h-80 w-full overflow-y-auto rounded-md border border-border bg-card shadow-xl shadow-black/30">
                  {searching ? (
                    <div className="px-3 py-4 text-center text-[12px] text-muted-foreground">正在搜索...</div>
                  ) : searchError ? (
                    <div className="px-3 py-4 text-center text-[12px] text-red-400">{searchError}</div>
                  ) : searchResults.length === 0 ? (
                    <div className="px-3 py-4 text-center text-[12px] text-muted-foreground">未找到匹配证券</div>
                  ) : searchResults.map((result) => {
                    const added = watchlistSymbols.has(result.symbol)
                    return (
                      <button
                        key={result.symbol}
                        type="button"
                        onClick={() => !added && void addToWatchlist(result)}
                        disabled={added || watchlistFull}
                        title={watchlistFull && !added ? '自选列表已达上限' : undefined}
                        className="flex w-full items-center gap-3 border-b border-border px-3 py-2.5 text-left transition-colors last:border-b-0 hover:bg-accent disabled:cursor-default"
                      >
                        <div className="min-w-0 flex-1">
                          <p className="truncate text-[12px] font-medium text-foreground">{result.name}</p>
                          <p className="mt-0.5 text-[10px] text-muted-foreground">{result.code} · {result.market}</p>
                        </div>
                        {added ? (
                          <Check className="h-4 w-4 text-emerald-400" />
                        ) : watchlistFull ? (
                          <span className="text-[10px] text-muted-foreground">已达上限</span>
                        ) : (
                          <Plus className="h-4 w-4 text-primary" />
                        )}
                      </button>
                    )
                  })}
                </div>
              )}
            </div>
          </div>

          {watchlist.length === 0 ? (
            <div className="flex min-h-44 flex-col items-center justify-center border-y border-dashed border-border px-4 text-center">
              <LineChart className="mb-3 h-7 w-7 text-muted-foreground" aria-hidden="true" />
              <p className="text-[13px] font-medium text-foreground">暂无自选证券</p>
              <p className="mt-1 text-[11px] text-muted-foreground">使用右上方搜索框添加股票、指数或基金</p>
            </div>
          ) : (
            <div className="overflow-hidden rounded-lg border border-border bg-card">
              <table className="hidden w-full table-fixed border-collapse md:table">
                <thead>
                  <tr className="border-b border-border bg-muted/50 text-left text-[10px] font-medium uppercase text-muted-foreground">
                    <th className="w-[22%] px-4 py-2.5">证券</th>
                    <th className="w-[12%] px-3 py-2.5 text-right">最新价</th>
                    <th className="w-[13%] px-3 py-2.5 text-right">涨跌幅</th>
                    <th className="w-[20%] px-3 py-2.5 text-right">今开 / 最高 / 最低</th>
                    <th className="w-[14%] px-3 py-2.5 text-right">成交量</th>
                    <th className="w-[14%] px-3 py-2.5 text-right">成交额</th>
                    <th className="w-[5%] px-3 py-2.5"><span className="sr-only">操作</span></th>
                  </tr>
                </thead>
                <tbody>
                  {watchlist.map((item) => {
                    const quote = quotes[item.symbol]
                    return (
                      <tr key={item.symbol} className="border-b border-border last:border-b-0 hover:bg-accent/40">
                        <td className="px-4 py-3">
                          <p className="truncate text-[12px] font-medium text-foreground">{quote?.name || item.name}</p>
                          <p className="mt-0.5 truncate text-[10px] text-muted-foreground">{item.code} · {quote?.market || item.market}</p>
                        </td>
                        <td className={`px-3 py-3 text-right text-[13px] font-medium tabular-nums ${directionClass(quote?.change_percent)}`}>
                          {formatPrice(quote?.price)}
                        </td>
                        <td className={`px-3 py-3 text-right text-[11px] tabular-nums ${directionClass(quote?.change_percent)}`}>
                          <p>{formatSigned(quote?.change_percent, '%')}</p>
                          <p className="mt-0.5 opacity-80">{formatSigned(quote?.change)}</p>
                        </td>
                        <td className="px-3 py-3 text-right text-[11px] text-muted-foreground tabular-nums">
                          {formatPrice(quote?.open)} / {formatPrice(quote?.high)} / {formatPrice(quote?.low)}
                        </td>
                        <td className="px-3 py-3 text-right text-[11px] text-muted-foreground tabular-nums">
                          {formatMarketNumber(quote?.volume, '股')}
                        </td>
                        <td className="px-3 py-3 text-right text-[11px] text-muted-foreground tabular-nums">
                          {formatMarketNumber(quote?.turnover, '元')}
                        </td>
                        <td className="px-3 py-3 text-right">
                          <button
                            type="button"
                            onClick={() => void removeFromWatchlist(item.symbol)}
                            title={`移除 ${item.name}`}
                            aria-label={`移除 ${item.name}`}
                            className="inline-flex h-7 w-7 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-red-500/10 hover:text-red-400"
                          >
                            <Trash2 className="h-3.5 w-3.5" />
                          </button>
                        </td>
                      </tr>
                    )
                  })}
                </tbody>
              </table>

              <div className="divide-y divide-border md:hidden">
                {watchlist.map((item) => {
                  const quote = quotes[item.symbol]
                  return (
                    <div key={item.symbol} className="px-3 py-3.5">
                      <div className="flex items-start justify-between gap-3">
                        <div className="min-w-0">
                          <p className="truncate text-[12px] font-medium text-foreground">{quote?.name || item.name}</p>
                          <p className="mt-0.5 text-[10px] text-muted-foreground">{item.code} · {quote?.market || item.market}</p>
                        </div>
                        <div className="flex items-start gap-2">
                          <div className={`text-right tabular-nums ${directionClass(quote?.change_percent)}`}>
                            <p className="text-[14px] font-medium">{formatPrice(quote?.price)}</p>
                            <p className="text-[10px]">{formatSigned(quote?.change_percent, '%')}</p>
                          </div>
                          <button
                            type="button"
                            onClick={() => void removeFromWatchlist(item.symbol)}
                            title={`移除 ${item.name}`}
                            aria-label={`移除 ${item.name}`}
                            className="inline-flex h-7 w-7 items-center justify-center rounded-md text-muted-foreground hover:bg-red-500/10 hover:text-red-400"
                          >
                            <Trash2 className="h-3.5 w-3.5" />
                          </button>
                        </div>
                      </div>
                      <div className="mt-3 grid grid-cols-3 gap-2 text-[10px] text-muted-foreground">
                        <div><span className="block opacity-70">今开</span><span className="mt-0.5 block tabular-nums">{formatPrice(quote?.open)}</span></div>
                        <div><span className="block opacity-70">最高 / 最低</span><span className="mt-0.5 block tabular-nums">{formatPrice(quote?.high)} / {formatPrice(quote?.low)}</span></div>
                        <div className="text-right"><span className="block opacity-70">昨收</span><span className="mt-0.5 block tabular-nums">{formatPrice(quote?.previous_close)}</span></div>
                      </div>
                    </div>
                  )
                })}
              </div>
            </div>
          )}
        </section>

        <p className="text-[10px] leading-5 text-muted-foreground">
          股票与数字资产行情来自公开市场数据接口，可能存在延迟，仅供信息参考，不构成投资建议。
        </p>
      </div>
    </div>
  )
}
