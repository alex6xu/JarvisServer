import { describe, expect, it } from 'vitest'
import { splitSSEFrames } from './sse'

describe('splitSSEFrames', () => {
  it('keeps an incomplete frame for the next stream chunk', () => {
    const first = splitSSEFrames('data: {"type":"ticker"}\n\ndata: {"type"')
    expect(first.payloads).toEqual(['{"type":"ticker"}'])
    expect(first.remainder).toBe('data: {"type"')

    const second = splitSSEFrames(`${first.remainder}:"status"}\n\n`)
    expect(second.payloads).toEqual(['{"type":"status"}'])
    expect(second.remainder).toBe('')
  })

  it('ignores heartbeat comments and joins multi-line data', () => {
    const result = splitSSEFrames(': heartbeat\n\ndata: first\ndata: second\n\n')
    expect(result.payloads).toEqual(['first\nsecond'])
  })
})
