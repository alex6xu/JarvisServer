import { describe, expect, it } from 'vitest'
import {
  movePendingMessage,
  pendingQueueOrder,
  type RunMessageQueueSnapshot,
} from './useRunMessageQueue'

const snapshot: RunMessageQueueSnapshot = {
  run_id: 'run-1',
  version: 3,
  items: [
    { id: 'done', run_id: 'run-1', session_id: 's', content: 'done', event_type: 'enqueue', position: 0, status: 'completed', created_at: '', updated_at: '' },
    { id: 'a', run_id: 'run-1', session_id: 's', content: 'A', event_type: 'enqueue', position: 1, status: 'pending', created_at: '', updated_at: '' },
    { id: 'b', run_id: 'run-1', session_id: 's', content: 'B', event_type: 'pin', position: 2, status: 'pending', created_at: '', updated_at: '' },
    { id: 'injected', run_id: 'run-1', session_id: 's', content: 'read', event_type: 'steer', position: 3, status: 'injected', created_at: '', updated_at: '' },
  ],
}

describe('run message queue ordering', () => {
  it('includes pending messages only', () => {
    expect(pendingQueueOrder(snapshot)).toEqual(['a', 'b'])
  })

  it('moves a pending message without including terminal items', () => {
    expect(movePendingMessage(snapshot, 'b', -1)).toEqual(['b', 'a'])
    expect(movePendingMessage(snapshot, 'a', -1)).toEqual(['a', 'b'])
  })
})
