import { describe, expect, it } from 'vitest'
import { mergeRestoredMessages, type UiMessage } from './sessionPersist'

const message = (id: string, seq: number): UiMessage => ({ id, seq, role: 'user', content: id, timestamp: new Date(0) })

describe('mergeRestoredMessages', () => {
  it('deduplicates by stable entry id and sorts by sequence', () => {
    expect(mergeRestoredMessages([message('b', 2), message('a', 1)], [message('a', 1), message('c', 3)]).map((m) => m.id)).toEqual(['a', 'b', 'c'])
  })
})
