import { useCallback, useRef } from 'react'
import { apiFetch } from '../context/AccountContext'
import {
  attachToolStepsToMessages,
  mapRestoredMessages,
  readLocal,
  readSessionQueryParam,
  segmentsFromContentAndTools,
  writeLocal,
  writeSessionQueryParam,
  type SessionRestorePayload,
  type UiMessage,
} from '../lib/sessionPersist'

type RestoreResult = {
  sessionId: string
  messages: UiMessage[]
  activeRunId?: string
  activeModel?: string
  afterSeq?: number
  workspaceId?: string
}

type RestoreOpts = {
  accountId: number
  storageKey: string
  mode: 'chat' | 'coder'
  workspaceId?: string
  /** Prefer URL ?session= / ?resume= over localStorage when true (default). */
  preferUrl?: boolean
  /** Explicit session override (e.g. from navigation). */
  sessionId?: string
}

/**
 * Loads a session transcript from the API and optionally resumes an active run.
 * Callers own React state; this hook only owns the restore generation token.
 */
export function useSessionRestore() {
  const genRef = useRef(0)

  const resolveSessionId = useCallback((opts: RestoreOpts): string => {
    if (opts.sessionId) return opts.sessionId
    if (opts.preferUrl !== false) {
      const fromUrl = readSessionQueryParam()
      if (fromUrl) return fromUrl
    }
    return (
      readLocal(opts.storageKey) ||
      (typeof sessionStorage !== 'undefined' ? sessionStorage.getItem(opts.storageKey) || '' : '')
    )
  }, [])

  const persistSessionId = useCallback((storageKey: string, sessionId: string) => {
    if (!sessionId) return
    writeLocal(storageKey, sessionId)
    writeSessionQueryParam(sessionId)
    try {
      sessionStorage.setItem(storageKey, sessionId)
    } catch {
      /* ignore */
    }
  }, [])

  const clearPersistedSession = useCallback((storageKey: string) => {
    writeLocal(storageKey, '')
    writeSessionQueryParam('')
    try {
      sessionStorage.removeItem(storageKey)
    } catch {
      /* ignore */
    }
  }, [])

  const scopeQuery = useCallback((mode: 'chat' | 'coder', workspaceId?: string) => {
    const params = new URLSearchParams({ mode })
    if (workspaceId) params.set('workspace_id', workspaceId)
    return params.toString()
  }, [])

  const getServerActiveSession = useCallback(
    async (opts: RestoreOpts): Promise<string> => {
      const res = await apiFetch(
        `/v1/agent/sessions/active?${scopeQuery(opts.mode, opts.workspaceId)}`,
        {},
        opts.accountId,
      )
      if (!res.ok) return ''
      const data = (await res.json()) as { session?: { session_id?: string } | null }
      return data.session?.session_id || ''
    },
    [scopeQuery],
  )

  const setServerActiveSession = useCallback(async (opts: RestoreOpts, sessionId: string) => {
    if (!sessionId) return
    await apiFetch(
      '/v1/agent/sessions/active',
      {
        method: 'PUT',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          session_id: sessionId,
          mode: opts.mode,
          workspace_id: opts.workspaceId || undefined,
        }),
      },
      opts.accountId,
    )
  }, [])

  const clearServerActiveSession = useCallback(
    async (opts: RestoreOpts, sessionId?: string) => {
      const query = new URLSearchParams(scopeQuery(opts.mode, opts.workspaceId))
      if (sessionId) query.set('session_id', sessionId)
      await apiFetch(
        `/v1/agent/sessions/active?${query}`,
        { method: 'DELETE' },
        opts.accountId,
      )
    },
    [scopeQuery],
  )

  const restoreSession = useCallback(
    async (opts: RestoreOpts): Promise<RestoreResult | null> => {
      const gen = ++genRef.current
      let saved = resolveSessionId(opts)
      const hadBrowserSession = !!saved
      if (!saved) saved = await getServerActiveSession(opts)
      if (gen !== genRef.current) return null
      if (!saved) return null

      let res = await apiFetch(`/v1/agent/sessions/${encodeURIComponent(saved)}`, {}, opts.accountId)
      if (gen !== genRef.current) return null
      if (!res.ok) {
        // Stale localStorage / ?session= ids produce noisy 404s on every Chat mount.
        if (res.status === 404 || res.status === 403) {
          clearPersistedSession(opts.storageKey)
          if (hadBrowserSession) {
            const serverSession = await getServerActiveSession(opts)
            if (gen !== genRef.current) return null
            if (serverSession && serverSession !== saved) {
              saved = serverSession
              res = await apiFetch(
                `/v1/agent/sessions/${encodeURIComponent(saved)}`,
                {},
                opts.accountId,
              )
              if (gen !== genRef.current) return null
              if (!res.ok) return null
            } else {
              return null
            }
          } else {
            return null
          }
        } else {
          return null
        }
      }
      const data = (await res.json()) as SessionRestorePayload
      if (gen !== genRef.current) return null

      const restoredWorkspace = data.workspace_id || data.session?.workspace_id || ''
      const restoredMode = data.session?.platform || (restoredWorkspace ? 'coder' : 'chat')
      if (restoredMode !== opts.mode || (opts.mode === 'coder' && restoredWorkspace !== opts.workspaceId)) {
        clearPersistedSession(opts.storageKey)
        return null
      }

      persistSessionId(opts.storageKey, saved)
      await setServerActiveSession(opts, saved)
      if (gen !== genRef.current) return null

      let messages = mapRestoredMessages(data.messages)
      const active = data.active_run
      const workspaceId = data.workspace_id || active?.workspace_id || data.latest_run?.workspace_id

      if (active && (active.status === 'running' || active.status === 'queued')) {
        const assistantId = `run-${active.id}`
        if (!messages.some((m) => m.id === assistantId)) {
          messages = [
            ...messages,
            {
              id: assistantId,
              role: 'assistant',
              content: '',
              timestamp: new Date(),
              model: active.model,
              toolSteps: data.active_run_tool_steps || [],
              segments: segmentsFromContentAndTools('', data.active_run_tool_steps || []),
            },
          ]
        } else {
          messages = attachToolStepsToMessages(messages, data.active_run_tool_steps)
        }
        return {
          sessionId: saved,
          messages,
          activeRunId: active.id,
          activeModel: active.model,
          // Partial active-run output is not in the session transcript, so
          // replay the run after the placeholder has been mounted.
          afterSeq: 0,
          workspaceId,
        }
      }

      messages = attachToolStepsToMessages(messages, data.latest_run_tool_steps)
      return {
        sessionId: saved,
        messages,
        workspaceId,
      }
    },
    [clearPersistedSession, getServerActiveSession, persistSessionId, resolveSessionId, setServerActiveSession],
  )

  const isCurrentGeneration = useCallback((gen: number) => gen === genRef.current, [])
  const nextGeneration = useCallback(() => ++genRef.current, [])

  return {
    restoreSession,
    persistSessionId,
    clearPersistedSession,
    clearServerActiveSession,
    setServerActiveSession,
    resolveSessionId,
    genRef,
    isCurrentGeneration,
    nextGeneration,
  }
}
