import { beforeEach, describe, expect, it, vi } from 'vitest'
const fixture = vi.hoisted(() => ({ fetch: vi.fn(), write: vi.fn(), local: 'saved-session' }))
vi.mock('react', () => ({ useCallback: (fn: unknown) => fn, useRef: (current: unknown) => ({ current }) }))
vi.mock('../context/AccountContext', () => ({ apiFetch: fixture.fetch }))
vi.mock('../lib/sessionPersist', async (importOriginal) => ({
  ...await importOriginal<typeof import('../lib/sessionPersist')>(),
  readLocal: () => fixture.local,
  readSessionQueryParam: () => '',
  writeLocal: fixture.write,
  writeSessionQueryParam: fixture.write,
}))
import { useSessionRestore } from './useSessionRestore'
const opts = { accountId: 1, storageKey: 'workspace-cache', mode: 'coder' as const, workspaceId: 'ws', projectId: 'manual' }
const response = (data: unknown, ok = true) => ({ ok, json: async () => data })
beforeEach(() => { vi.clearAllMocks() })
describe('project-scoped code session restore', () => {
  it('does not resume another project sharing a workspace or overwrite its active pointer', async () => {
    fixture.fetch.mockResolvedValueOnce(response({ workspace_id: 'ws', session: { platform: 'coder' } }))
      .mockResolvedValueOnce(response({ assignment: { project: { id: 'other' } } }))
    expect(await useSessionRestore().restoreSession(opts)).toBeNull()
    expect(fixture.write).not.toHaveBeenCalled()
    expect(fixture.fetch).toHaveBeenCalledTimes(2)
  })
  it('restores the requested project and updates its active session only after verifying membership', async () => {
    fixture.fetch.mockResolvedValueOnce(response({ workspace_id: 'ws', session: { platform: 'coder' }, messages: [] }))
      .mockResolvedValueOnce(response({ assignment: { project: { id: 'manual' } } }))
      .mockResolvedValueOnce(response({}))
    expect((await useSessionRestore().restoreSession(opts))?.sessionId).toBe('saved-session')
    expect(fixture.fetch.mock.calls[2][1].method).toBe('PUT')
  })
  it('fails closed when project membership cannot be fetched', async () => {
    fixture.fetch.mockResolvedValueOnce(response({ workspace_id: 'ws', session: { platform: 'coder' } }))
      .mockResolvedValueOnce(response({}, false))
    await expect(useSessionRestore().restoreSession(opts)).rejects.toThrow('会话项目关联加载失败')
    expect(fixture.write).not.toHaveBeenCalled()
  })
})
