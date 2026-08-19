import { describe, expect, it } from 'vitest'

import {
  appendTextSegment,
  attachToolStepsToMessages,
  contentFromSegments,
  mapRestoredMessages,
  readSessionQueryParam,
  toolStepsFromSegments,
  upsertToolSegment,
  type MessageSegment,
} from './sessionPersist'

describe('session persistence helpers', () => {
  it('keeps streamed text chronological without mutating previous segments', () => {
    const initial: MessageSegment[] = [{ type: 'text', content: 'hello' }]
    const next = appendTextSegment(initial, ' world')

    expect(initial).toEqual([{ type: 'text', content: 'hello' }])
    expect(next).toEqual([{ type: 'text', content: 'hello world' }])
    expect(contentFromSegments(next)).toBe('hello world')
  })

  it('updates a tool lifecycle in place and preserves timeline order', () => {
    const running = upsertToolSegment([], {
      id: 'tool-1',
      tool: 'read',
      args: '{"path":"README.md"}',
      result: '',
      status: 'running',
    })
    const done = upsertToolSegment(running, {
      id: 'tool-1',
      tool: 'read',
      args: '{"path":"README.md"}',
      result: 'content',
      status: 'done',
    })

    expect(running[0].type === 'tool' && running[0].step.status).toBe('running')
    expect(toolStepsFromSegments(done)).toEqual([
      expect.objectContaining({ id: 'tool-1', status: 'done', result: 'content' }),
    ])
  })

  it('restores assistant segments and attaches missing active-run tools', () => {
    const restored = mapRestoredMessages([
      { id: 'u1', role: 'user', content: 'question' },
      { id: 'a1', role: 'assistant', content: 'answer' },
    ])
    const attached = attachToolStepsToMessages(restored, [
      { id: 'tool-1', tool: 'search', args: '{}', result: 'found', status: 'done' },
    ])

    expect(attached[0].segments).toBeUndefined()
    expect(attached[1].segments).toEqual([
      { type: 'text', content: 'answer' },
      { type: 'tool', step: expect.objectContaining({ id: 'tool-1' }) },
    ])
    expect(restored[1].toolSteps).toBeUndefined()
  })

  it('prefers the session query parameter and supports resume links', () => {
    expect(readSessionQueryParam('?resume=old&session=current')).toBe('current')
    expect(readSessionQueryParam('?resume=old')).toBe('old')
    expect(readSessionQueryParam('%%%')).toBe('')
  })
})
