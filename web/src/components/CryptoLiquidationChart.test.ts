import { describe, expect, it } from 'vitest'
import { aggregateLiquidations, formatCompact } from './CryptoLiquidationChart'
import type { CryptoLiquidation } from '../lib/stocks'

const now = Date.parse('2026-08-24T12:00:00Z')
const events: CryptoLiquidation[] = [
  { id: '1', exchange: 'binance', symbol: 'BTC-USDT-SWAP', side: 'long', price: 60_000, quantity: 1, notional: 60_000, currency: 'USDT', occurred_at: '2026-08-24T11:59:10Z', received_at: '2026-08-24T11:59:11Z' },
  { id: '2', exchange: 'okx', symbol: 'BTC-USDT-SWAP', side: 'short', price: 60_100, quantity: 2, notional: 120_200, currency: 'USDT', occurred_at: '2026-08-24T11:59:40Z', received_at: '2026-08-24T11:59:41Z' },
  { id: '3', exchange: 'binance', symbol: 'BTC-USDT-SWAP', side: 'long', price: 59_000, quantity: 1, notional: 59_000, currency: 'USDT', occurred_at: '2026-08-24T09:00:00Z', received_at: '2026-08-24T09:00:01Z' },
]

describe('crypto liquidation chart aggregation', () => {
  it('aggregates long and short notionals into time buckets', () => {
    expect(aggregateLiquidations(events, 'all', 60, now)).toEqual([
      { time: Date.parse('2026-08-24T11:59:00Z') / 1000, long: 60_000, short: 120_200, count: 2 },
    ])
  })

  it('filters by exchange and formats compact values', () => {
    expect(aggregateLiquidations(events, 'binance', 60, now)[0].short).toBe(0)
    expect(formatCompact(1_250_000)).toBe('1.25M')
  })
})
