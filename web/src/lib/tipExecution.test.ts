import { describe, expect, it } from 'vitest'
import type { TipRun } from '../types/tips'
import { tipHasActiveRun, tipRunLocation } from './tipExecution'

const run = (changes: Partial<TipRun> = {}): TipRun => ({
  id: 'execution', mode: 'execute', session_mode: 'chat', session_id: 'session /1',
  status: 'done', created_at: '', snapshot: {} as TipRun['snapshot'], ...changes,
})

describe('Tip execution restore', () => {
  it('restores non-workspace sessions to Chat with encoded session IDs', () => {
    expect(tipRunLocation(run())).toBe('/?session=session%20%2F1')
  })
  it('restores workspace analysis and execution to Code using the bound workspace', () => {
    for (const mode of ['analyze', 'execute'] as const) {
      expect(tipRunLocation(run({ mode, session_mode: 'coder', workspace_id: 'ws /1' })))
        .toBe('/code?workspace=ws%20%2F1&session=session%20%2F1')
    }
  })
  it('does not navigate failed or pending launches without a session', () => {
    expect(tipRunLocation(run({ session_id: undefined, status: 'failed' }))).toBeUndefined()
  })
  it('blocks only pending and running executions, not completed history', () => {
    expect(tipHasActiveRun()).toBe(false)
    expect(tipHasActiveRun([run(), run({ status: 'failed' })])).toBe(false)
    expect(tipHasActiveRun([run({ status: 'starting' })])).toBe(true)
    expect(tipHasActiveRun([run({ status: 'running' })])).toBe(true)
  })
})
