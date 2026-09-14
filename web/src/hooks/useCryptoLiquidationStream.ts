import { useEffect, useMemo, useState } from 'react'
import { apiFetch } from '../context/AccountContext'
import { splitSSEFrames } from '../lib/sse'
import type { CryptoExchange, CryptoLiquidation, CryptoLiquidationStreamEvent } from '../lib/stocks'
import type { CryptoProviderStatus } from './useCryptoTickerStream'

const initialProviders: Record<CryptoExchange, CryptoProviderStatus> = {
  binance: { state: 'connecting' },
  okx: { state: 'connecting' },
}

function isExchange(value: string): value is CryptoExchange {
  return value === 'binance' || value === 'okx'
}

function delay(signal: AbortSignal): Promise<void> {
  return new Promise((resolve) => {
    const finish = () => {
      window.clearTimeout(timer)
      signal.removeEventListener('abort', finish)
      resolve()
    }
    const timer = window.setTimeout(finish, 2_000)
    signal.addEventListener('abort', finish, { once: true })
  })
}

export function useCryptoLiquidationStream(accountId: number | undefined, symbols: string[]) {
  const symbolsKey = symbols.join(',')
  const [events, setEvents] = useState<CryptoLiquidation[]>([])
  const [providers, setProviders] = useState<Record<CryptoExchange, CryptoProviderStatus>>(initialProviders)
  const [streamError, setStreamError] = useState('')

  useEffect(() => {
    if (!accountId || !symbolsKey) {
      setEvents([])
      setProviders(initialProviders)
      setStreamError('')
      return
    }
    const controller = new AbortController()
    const seen = new Set<string>()
    setEvents([])
    setProviders(initialProviders)
    setStreamError('')

    const applyEvent = (event: CryptoLiquidationStreamEvent) => {
      if (!event || !isExchange(event.exchange)) return
      if (event.type === 'status' && event.state) {
        setProviders((current) => ({
          ...current,
          [event.exchange]: { state: event.state!, message: event.message },
        }))
        return
      }
      const item = event.liquidation
      if (event.type !== 'liquidation' || !item || seen.has(item.id)) return
      seen.add(item.id)
      setProviders((current) => ({ ...current, [event.exchange]: { state: 'connected' } }))
      setEvents((current) => {
        const next = [...current, item]
        if (next.length <= 2_000) return next
        const removed = next.splice(0, next.length - 2_000)
        for (const old of removed) seen.delete(old.id)
        return next
      })
    }

    const consume = async () => {
      const response = await apiFetch(
        `/v1/crypto/liquidations/stream?symbols=${encodeURIComponent(symbolsKey)}`,
        { signal: controller.signal },
        accountId,
      )
      if (!response.ok || !response.body) {
        const body = await response.json().catch(() => ({}))
        throw new Error(typeof body.error === 'string' ? body.error : `HTTP ${response.status}`)
      }
      setStreamError('')
      const reader = response.body.getReader()
      const decoder = new TextDecoder()
      let buffer = ''
      while (true) {
        const { done, value } = await reader.read()
        if (done) throw new Error('清算数据连接已断开')
        buffer += decoder.decode(value, { stream: true })
        const frames = splitSSEFrames(buffer)
        buffer = frames.remainder
        for (const payload of frames.payloads) {
          try {
            applyEvent(JSON.parse(payload) as CryptoLiquidationStreamEvent)
          } catch {
            // Ignore one malformed provider event.
          }
        }
      }
    }

    const run = async () => {
      while (!controller.signal.aborted) {
        try {
          await consume()
        } catch (error) {
          if (controller.signal.aborted) return
          setStreamError(error instanceof Error ? error.message : '清算数据连接失败')
          await delay(controller.signal)
        }
      }
    }
    void run()
    return () => controller.abort()
  }, [accountId, symbolsKey])

  const lastUpdated = useMemo(() => events[events.length - 1]?.received_at || '', [events])
  return { events, providers, streamError, lastUpdated }
}
