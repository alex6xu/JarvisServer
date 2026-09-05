import { describe, expect, it } from 'vitest'
import {
  isQueueUnavailableError,
  movePendingMessage,
  pendingQueueOrder,
  QueueUnavailableError,
  visibleQueueItems,
  type RunMessageQueueSnapshot,
} from './useRunMessageQueue'

const snapshot: RunMessageQueueSnapshot = {
  run_id: 'run-1',
  version: 3,
  items: [
    { id: 'done', run_id: 'run-1', session_id: 's', content: 'done', event_type: 'enqueue', position: 0, status: 'completed', created_at: '', updated_at: '' },
    { id: 'a', run_id: 'run-1', session_id: 's', content: 'A', event_type: 'enqueue', position: 1, status: 'pending', created_at: '', updated_at: '' },
    { id: 'b', run_id: 'run-1', session_id: 's', content: 'B', event_type: 'pin', position: 2, status: 'pending', created_at: '', updated_at: '' },
    { id: 'injecting', run_id: 'run-1', session_id: 's', content: 'loading', event_type: 'steer', position: 3, status: 'injecting', created_at: '', updated_at: '' },
    { id: 'injected', run_id: 'run-1', session_id: 's', content: 'read', event_type: 'steer', position: 4, status: 'injected', created_at: '', updated_at: '' },
    { id: 'executing', run_id: 'run-1', session_id: 's', content: 'running', event_type: 'enqueue', position: 5, status: 'executing', created_at: '', updated_at: '' },
    { id: 'answered', run_id: 'run-1', session_id: 's', content: 'answered', event_type: 'enqueue', position: 6, status: 'answered', created_at: '', updated_at: '' },
  ],
}

describe('queue availability errors', () => {
  it('recognizes stale-run responses that should fall back to a normal send', () => {
    expect(isQueueUnavailableError(new QueueUnavailableError(404, 'run not found'))).toBe(true)
    expect(isQueueUnavailableError(new QueueUnavailableError(409, 'run closed'))).toBe(true)
    expect(isQueueUnavailableError(new QueueUnavailableError(500, 'failed'))).toBe(false)
    expect(isQueueUnavailableError(new Error('HTTP 404'))).toBe(false)
  })
})

describe('run message queue visibility', () => {
  it('removes executing and terminal messages from the waiting queue', () => {
    expect(visibleQueueItems(snapshot).map((item) => item.id)).toEqual(['a', 'b', 'injecting', 'injected'])
  })
})

describe('run message queue ordering', () => {
  it('includes pending messages only', () => {
    expect(pendingQueueOrder(snapshot)).toEqual(['a', 'b'])
  })

  it('moves a pending message without including terminal items', () => {
    expect(movePendingMessage(snapshot, 'b', -1)).toEqual(['b', 'a'])
    expect(movePendingMessage(snapshot, 'a', -1)).toEqual(['a', 'b'])
  })
})
