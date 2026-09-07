import { describe, expect, it } from 'vitest'
import { chatHref, groupSidebarSessions } from './sidebarSessions'

describe('sidebar session grouping', () => {
  it('uses canonical API type, never names or IDs, including normalized historical chats', () => {
    const sessions = [{ id: 'code-looking-id', type: 'chat', title: 'task' }, { id: 'c', type: 'code', title: 'Chat' }, { id: 't', type: 'task' }, { id: 'u', type: 'future' }, { id: 'missing' }, { id: 'legacy', type: 'chat' }]
    expect(groupSidebarSessions(sessions, {}).recent.map((s) => s.id)).toEqual(['code-looking-id', 'legacy'])
  })
  it('excludes project members before limiting and sorts without mutation', () => {
    const sessions = Array.from({ length: 16 }, (_, i) => ({ id: `${i}`, type: 'chat', updated_at: `2026-09-${String(i + 1).padStart(2, '0')}` }))
    const result = groupSidebarSessions(sessions, { p: [sessions[15], sessions[14], { id: 'code', type: 'code' }], empty: [] })
    expect(result.recent).toHaveLength(12)
    expect(result.recent[0].id).toBe('13')
    expect(result.recent.some((s) => ['14', '15'].includes(s.id))).toBe(false)
    expect(result.projects.p.map((s) => s.id)).toEqual(['15', '14'])
    expect(result.projects.empty).toEqual([])
    expect(sessions[0].id).toBe('0')
  })
  it('encodes chat links without stale project or new-session parameters', () => {
    const url = new URL(chatHref('a/b &?#'), 'https://example.test')
    expect(url.pathname).toBe('/')
    expect(Array.from(url.searchParams)).toEqual([['session', 'a/b &?#']])
  })
})
