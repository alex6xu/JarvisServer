import { useEffect, useMemo, useRef } from 'react'
import {
  ColorType,
  createChart,
  HistogramSeries,
  type IChartApi,
  type ISeriesApi,
  type UTCTimestamp,
} from 'lightweight-charts'
import type { CryptoExchange, CryptoLiquidation } from '../lib/stocks'

export type LiquidationExchangeFilter = 'all' | CryptoExchange

interface Props {
  events: CryptoLiquidation[]
  exchange: LiquidationExchangeFilter
  rangeMinutes: number
}

export interface LiquidationBucket {
  time: number
  long: number
  short: number
  count: number
}

export function aggregateLiquidations(
  events: CryptoLiquidation[],
  exchange: LiquidationExchangeFilter,
  rangeMinutes: number,
  now = Date.now(),
): LiquidationBucket[] {
  const bucketSeconds = rangeMinutes <= 60 ? 60 : rangeMinutes <= 240 ? 300 : 900
  const cutoff = now - rangeMinutes * 60_000
  const buckets = new Map<number, LiquidationBucket>()
  for (const event of events) {
    if (exchange !== 'all' && event.exchange !== exchange) continue
    const milliseconds = Date.parse(event.occurred_at)
    if (!Number.isFinite(milliseconds) || milliseconds < cutoff || milliseconds > now + 60_000) continue
    const time = Math.floor(milliseconds / 1000 / bucketSeconds) * bucketSeconds
    const bucket = buckets.get(time) || { time, long: 0, short: 0, count: 0 }
    bucket[event.side] += event.notional
    bucket.count++
    buckets.set(time, bucket)
  }
  return [...buckets.values()].sort((a, b) => a.time - b.time)
}

export default function CryptoLiquidationChart({ events, exchange, rangeMinutes }: Props) {
  const containerRef = useRef<HTMLDivElement | null>(null)
  const chartRef = useRef<IChartApi | null>(null)
  const longRef = useRef<ISeriesApi<'Histogram'> | null>(null)
  const shortRef = useRef<ISeriesApi<'Histogram'> | null>(null)
  const buckets = useMemo(
    () => aggregateLiquidations(events, exchange, rangeMinutes),
    [events, exchange, rangeMinutes],
  )

  useEffect(() => {
    const container = containerRef.current
    if (!container) return
    const chart = createChart(container, {
      width: container.clientWidth,
      height: container.clientHeight,
      layout: {
        background: { type: ColorType.Solid, color: 'transparent' },
        textColor: '#8f9baa',
        attributionLogo: false,
      },
      grid: {
        vertLines: { color: 'rgba(143, 155, 170, 0.08)' },
        horzLines: { color: 'rgba(143, 155, 170, 0.08)' },
      },
      rightPriceScale: { borderColor: 'rgba(143, 155, 170, 0.2)' },
      timeScale: { borderColor: 'rgba(143, 155, 170, 0.2)', timeVisible: true, secondsVisible: false },
      localization: { locale: 'zh-CN', priceFormatter: (value: number) => `$${formatCompact(value)}` },
    })
    const longSeries = chart.addSeries(HistogramSeries, {
      color: '#f97316',
      priceFormat: { type: 'custom', formatter: (value: number) => `$${formatCompact(value)}` },
      title: '多单清算',
    })
    const shortSeries = chart.addSeries(HistogramSeries, {
      color: '#38bdf8',
      priceFormat: { type: 'custom', formatter: (value: number) => `$${formatCompact(value)}` },
      title: '空单清算',
    })
    chartRef.current = chart
    longRef.current = longSeries
    shortRef.current = shortSeries
    const observer = new ResizeObserver(() => chart.resize(container.clientWidth, container.clientHeight))
    observer.observe(container)
    return () => {
      observer.disconnect()
      chart.remove()
      chartRef.current = null
      longRef.current = null
      shortRef.current = null
    }
  }, [])

  useEffect(() => {
    longRef.current?.setData(buckets.map((bucket) => ({ time: bucket.time as UTCTimestamp, value: -bucket.long, color: '#f97316' })))
    shortRef.current?.setData(buckets.map((bucket) => ({ time: bucket.time as UTCTimestamp, value: bucket.short, color: '#38bdf8' })))
    if (buckets.length) chartRef.current?.timeScale().fitContent()
  }, [buckets])

  return (
    <div className="relative">
      <div ref={containerRef} className="h-[300px] w-full sm:h-[360px]" aria-label="实时清算金额时间图" />
      {!buckets.length && (
        <div className="pointer-events-none absolute inset-0 flex items-center justify-center text-[11px] text-muted-foreground">
          正在等待公开清算事件…
        </div>
      )}
    </div>
  )
}

export function formatCompact(value: number): string {
  const absolute = Math.abs(value)
  if (absolute >= 1_000_000_000) return `${(absolute / 1_000_000_000).toFixed(2)}B`
  if (absolute >= 1_000_000) return `${(absolute / 1_000_000).toFixed(2)}M`
  if (absolute >= 1_000) return `${(absolute / 1_000).toFixed(1)}K`
  return absolute.toFixed(0)
}
