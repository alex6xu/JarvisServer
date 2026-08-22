import { describe, expect, it } from 'vitest'

import type { MessageSegment } from '../lib/sessionPersist'
import { groupToolSegments } from './MessageBubble'

describe('MessageBubble tool grouping', () => {
  it('groups adjacent tools while preserving text boundaries and offsets', () => {
    const read = { id: '1', tool: 'read', args: '{}', result: 'a', status: 'done' }
    const grep = { id: '2', tool: 'grep', args: '{}', result: 'b', status: 'done' }
    const edit = { id: '3', tool: 'edit', args: '{}', result: 'ok', status: 'done' }
    const segments: MessageSegment[] = [
      { type: 'text', content: 'checking' },
      { type: 'tool', step: read },
      { type: 'tool', step: grep },
      { type: 'text', content: 'editing' },
      { type: 'tool', step: edit },
    ]

    expect(groupToolSegments(segments)).toEqual([
      { type: 'text', content: 'checking', sourceIndex: 0 },
      { type: 'tools', steps: [read, grep], sourceIndex: 1, stepOffset: 1 },
      { type: 'text', content: 'editing', sourceIndex: 3 },
      { type: 'tools', steps: [edit], sourceIndex: 4, stepOffset: 3 },
    ])
  })
})
