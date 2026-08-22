import { useEffect, useState } from 'react'
import { apiFetch } from '../context/AccountContext'
import { splitSSEFrames } from '../lib/sse'
import type { CryptoExchange, CryptoStreamEvent, CryptoTicker } from '../lib/stocks'

export type CryptoProviderState = 'connecting' | 'connected' | 'disconnected'

export interface CryptoProviderStatus {
  state: CryptoProviderState
  message?: string
}

const initialProviders: Record<CryptoExchange, CryptoProviderStatus> = {
  binance: { state: 'connecting' },
  okx: { state: 'connecting' },
}

function isExchange(value: string): value is CryptoExchange {
  return value === 'binance' || value === 'okx'
}

function reconnectDelay(signal: AbortSignal): Promise<void> {
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

export function useCryptoTickerStream(accountId: number | undefined, symbols: string[]) {
  const symbolsKey = symbols.join(',')
  const [tickers, setTickers] = useState<Record<string, CryptoTicker>>({})
  const [providers, setProviders] = useState<Record<CryptoExchange, CryptoProviderStatus>>(initialProviders)
  const [streamError, setStreamError] = useState('')

  useEffect(() => {
    if (!accountId || !symbolsKey) return
    const controller = new AbortController()
    setTickers({})
    setProviders(initialProviders)
    setStreamError('')

    const applyEvent = (event: CryptoStreamEvent) => {
      if (!event || !isExchange(event.exchange)) return
      if (event.type === 'ticker' && event.ticker) {
        const ticker = event.ticker
        setTickers((current) => ({ ...current, [`${ticker.exchange}:${ticker.symbol}`]: ticker }))
        setProviders((current) => ({ ...current, [event.exchange]: { state: 'connected' } }))
        return
      }
      if (event.type === 'status' && event.state) {
        setProviders((current) => ({
          ...current,
          [event.exchange]: { state: event.state!, message: event.message },
        }))
      }
    }

    const consume = async () => {
      const response = await apiFetch(
        `/v1/crypto/stream?symbols=${encodeURIComponent(symbolsKey)}`,
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
        if (done) throw new Error('实时行情连接已断开')
        buffer += decoder.decode(value, { stream: true })
        const frames = splitSSEFrames(buffer)
        buffer = frames.remainder
        for (const payload of frames.payloads) {
          try {
            applyEvent(JSON.parse(payload) as CryptoStreamEvent)
          } catch {
            // Ignore one malformed provider event and keep the stream alive.
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
          setStreamError(error instanceof Error ? error.message : '实时行情连接失败')
          setProviders({
            binance: { state: 'disconnected', message: 'Gateway 实时流中断' },
            okx: { state: 'disconnected', message: 'Gateway 实时流中断' },
          })
          await reconnectDelay(controller.signal)
        }
      }
    }

    void run()
    return () => controller.abort()
  }, [accountId, symbolsKey])

  return { tickers, providers, streamError }
}
