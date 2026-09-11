import { describe, expect, it } from 'vitest'
import { resolveWorkspace } from './codeWorkspace'
import { projectHref, projectSessionHref, groupSidebarSessions } from './sidebarSessions'

const base = { blank: false, projectId: '', workspaceId: '', sessionWorkspace: '', savedWorkspace: 'old', projects: [{ id: 'linked', name: 'Linked', linked_workspace_id: 'ws' }, { id: 'manual', name: 'Manual' }], workspaces: [{ id: 'old' }, { id: 'ws' }] }
describe('code workspace navigation and initialization', () => {
  it('new project ignores saved state and explicit parameters', () => {
    expect(resolveWorkspace({ ...base, blank: true, workspaceId: 'ws', sessionWorkspace: 'old' })).toBe('')
  })
  it('opens linked projects and keeps manual projects blank until initialized', () => {
    expect(resolveWorkspace({ ...base, projectId: 'linked' })).toBe('ws')
    expect(resolveWorkspace({ ...base, projectId: 'manual' })).toBe('')
    expect(resolveWorkspace({ ...base, projectId: 'manual', workspaceId: 'ws' })).toBe('ws')
    expect(resolveWorkspace({ ...base, projectId: 'manual', sessionWorkspace: 'ws' })).toBe('ws')
  })
  it('never falls back silently for stale or conflicting routes', () => {
    expect(() => resolveWorkspace({ ...base, projectId: 'deleted' })).toThrow()
    expect(() => resolveWorkspace({ ...base, workspaceId: 'deleted' })).toThrow()
    expect(() => resolveWorkspace({ ...base, workspaceId: 'old', sessionWorkspace: 'ws' })).toThrow()
    expect(() => resolveWorkspace({ ...base, projectId: 'linked', workspaceId: 'old' })).toThrow()
    expect(resolveWorkspace({ ...base, savedWorkspace: 'missing' })).toBe('')
  })
  it('preserves project and session IDs and routes chats separately', () => {
    const session = { id: 's&1', type: 'code', workspace_id: 'w/1' }
    const url = new URL(projectSessionHref(session, 'p?1'), 'https://fixture.test')
    expect(url.pathname).toBe('/code')
    expect(Object.fromEntries(url.searchParams)).toEqual({ session: 's&1', project: 'p?1', workspace: 'w/1' })
    expect(projectSessionHref({ ...session, type: 'chat' }, 'p')).toBe('/?session=s%261')
    expect(projectHref('p&1')).toBe('/code?project=p%261')
    expect(groupSidebarSessions([session, { id: 'ordinary', type: 'chat' }], { p: [session] }).recent.map(s => s.id)).toEqual(['ordinary'])
  })
})
