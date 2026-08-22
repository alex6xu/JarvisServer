import { describe, expect, it } from 'vitest'
import {
	CRYPTO_ASSETS,
  formatMarketNumber,
  formatPrice,
  formatSigned,
  formatSentimentScore,
  isUSStockSymbol,
  loadStockWatchlist,
  quoteDirection,
  saveStockWatchlist,
  stockWatchlistKey,
} from './stocks'

describe('stock helpers', () => {
	it('includes BTC, ETH, and ETC market detail assets', () => {
		expect(CRYPTO_ASSETS.map((asset) => asset.short)).toEqual(['BTC', 'ETH', 'ETC'])
	})

  it('loads only valid, unique watchlist entries', () => {
    const storage = {
      getItem: () => JSON.stringify([
        { symbol: '105.aapl', code: 'AAPL', name: '苹果', market: '美股' },
        { symbol: '105.AAPL', code: 'AAPL', name: '重复', market: '美股' },
        { symbol: 'https://bad', code: 'BAD', name: '无效', market: '' },
        null,
      ]),
    }
    expect(loadStockWatchlist(storage, 7)).toEqual([
      { symbol: '105.AAPL', code: 'AAPL', name: '苹果', market: '美股' },
    ])
  })

  it('falls back to an empty list for corrupt storage', () => {
    expect(loadStockWatchlist({ getItem: () => '{bad json' }, 1)).toEqual([])
  })

  it('saves to an account-scoped key', () => {
    let key = ''
    let value = ''
    saveStockWatchlist({ setItem: (nextKey, nextValue) => { key = nextKey; value = nextValue } }, 42, [
      { symbol: '1.600519', code: '600519', name: '贵州茅台', market: '沪A' },
    ])
    expect(key).toBe(stockWatchlistKey(42))
    expect(JSON.parse(value)).toHaveLength(1)
  })

  it('formats market values and directions', () => {
    expect(quoteDirection(1)).toBe('up')
    expect(quoteDirection(-1)).toBe('down')
    expect(quoteDirection(null)).toBe('flat')
    expect(formatPrice(1500.2)).toBe('1,500.20')
    expect(formatSigned(1.256, '%')).toBe('+1.26%')
    expect(formatSigned(null)).toBe('--')
    expect(formatMarketNumber(123_450_000, '元')).toBe('1.23亿元')
    expect(formatSentimentScore(61.234)).toBe('61.2')
    expect(formatSentimentScore(null)).toBe('--')
    expect(isUSStockSymbol('105.AAPL')).toBe(true)
    expect(isUSStockSymbol('1.600519')).toBe(false)
  })
})
