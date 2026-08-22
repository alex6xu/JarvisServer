import { useEffect, useRef } from 'react'
import {
  CandlestickSeries,
  ColorType,
  createChart,
  HistogramSeries,
  type IChartApi,
  type ISeriesApi,
  type UTCTimestamp,
} from 'lightweight-charts'
import type { CryptoCandle } from '../lib/stocks'

interface CryptoCandlestickChartProps {
  candles: CryptoCandle[]
  livePrice?: number | null
}

export default function CryptoCandlestickChart({ candles, livePrice }: CryptoCandlestickChartProps) {
  const containerRef = useRef<HTMLDivElement | null>(null)
  const chartRef = useRef<IChartApi | null>(null)
  const candleSeriesRef = useRef<ISeriesApi<'Candlestick'> | null>(null)
  const volumeSeriesRef = useRef<ISeriesApi<'Histogram'> | null>(null)
  const fittedRef = useRef(false)

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
      timeScale: {
        borderColor: 'rgba(143, 155, 170, 0.2)',
        timeVisible: true,
        secondsVisible: false,
        rightOffset: 4,
      },
      localization: { locale: 'zh-CN' },
      crosshair: {
        vertLine: { color: 'rgba(148, 163, 184, 0.45)', labelBackgroundColor: '#334155' },
        horzLine: { color: 'rgba(148, 163, 184, 0.45)', labelBackgroundColor: '#334155' },
      },
    })
    const candleSeries = chart.addSeries(CandlestickSeries, {
      upColor: '#22c55e',
      downColor: '#ef4444',
      borderVisible: false,
      wickUpColor: '#22c55e',
      wickDownColor: '#ef4444',
      priceScaleId: 'right',
    })
    candleSeries.priceScale().applyOptions({ scaleMargins: { top: 0.08, bottom: 0.28 } })
    const volumeSeries = chart.addSeries(HistogramSeries, {
      priceScaleId: '',
      priceFormat: { type: 'volume' },
    })
    volumeSeries.priceScale().applyOptions({ scaleMargins: { top: 0.78, bottom: 0.02 } })

    chartRef.current = chart
    candleSeriesRef.current = candleSeries
    volumeSeriesRef.current = volumeSeries
    const resizeObserver = new ResizeObserver(() => {
      chart.resize(container.clientWidth, container.clientHeight)
    })
    resizeObserver.observe(container)

    return () => {
      resizeObserver.disconnect()
      chart.remove()
      chartRef.current = null
      candleSeriesRef.current = null
      volumeSeriesRef.current = null
      fittedRef.current = false
    }
  }, [])

  useEffect(() => {
    const candleSeries = candleSeriesRef.current
    const volumeSeries = volumeSeriesRef.current
    if (!candleSeries || !volumeSeries) return
    candleSeries.setData(candles.map((candle) => ({
      time: candle.time as UTCTimestamp,
      open: candle.open,
      high: candle.high,
      low: candle.low,
      close: candle.close,
    })))
    volumeSeries.setData(candles.map((candle) => ({
      time: candle.time as UTCTimestamp,
      value: candle.volume,
      color: candle.close >= candle.open ? 'rgba(34, 197, 94, 0.35)' : 'rgba(239, 68, 68, 0.35)',
    })))
    if (!fittedRef.current && candles.length > 0) {
      chartRef.current?.timeScale().fitContent()
      fittedRef.current = true
    }
  }, [candles])

  useEffect(() => {
    const latest = candles[candles.length - 1]
    if (!latest || latest.confirmed || typeof livePrice !== 'number' || !Number.isFinite(livePrice)) return
    candleSeriesRef.current?.update({
      time: latest.time as UTCTimestamp,
      open: latest.open,
      high: Math.max(latest.high, livePrice),
      low: Math.min(latest.low, livePrice),
      close: livePrice,
    })
  }, [candles, livePrice])

  return <div ref={containerRef} className="h-[360px] w-full sm:h-[440px]" aria-label="实时 K 线图" />
}
