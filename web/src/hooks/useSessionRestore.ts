import { useCallback, useRef } from 'react'
import { apiFetch } from '../context/AccountContext'
import {
  attachToolStepsToMessages,
  mapRestoredMessages,
  readLocal,
  readSessionQueryParam,
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
  /** Prefer URL ?session= / ?resume= over localStorage when true (default). */
  preferUrl?: boolean
  /** Explicit session override (e.g. from navigation). */
  sessionId?: string
  consumeActiveRun?: (
    runId: string,
    assistantId: string,
    afterSeq: number,
    model?: string,
  ) => Promise<void>
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

  const restoreSession = useCallback(
    async (opts: RestoreOpts): Promise<RestoreResult | null> => {
      const gen = ++genRef.current
      const saved = resolveSessionId(opts)
      if (!saved) return null

      const res = await apiFetch(`/v1/agent/sessions/${encodeURIComponent(saved)}`, {}, opts.accountId)
      if (gen !== genRef.current) return null
      if (!res.ok) {
        // Stale localStorage / ?session= ids produce noisy 404s on every Chat mount.
        if (res.status === 404 || res.status === 403) {
          clearPersistedSession(opts.storageKey)
        }
        return null
      }
      const data = (await res.json()) as SessionRestorePayload
      if (gen !== genRef.current) return null

      persistSessionId(opts.storageKey, saved)

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
            },
          ]
        } else {
          messages = attachToolStepsToMessages(messages, data.active_run_tool_steps)
        }
        const afterSeq = typeof data.last_event_seq === 'number' ? 0 : 0
        if (opts.consumeActiveRun) {
          await opts.consumeActiveRun(active.id, assistantId, afterSeq, active.model)
          return {
            sessionId: saved,
            messages,
            workspaceId,
          }
        }
        return {
          sessionId: saved,
          messages,
          activeRunId: active.id,
          activeModel: active.model,
          afterSeq,
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
    [clearPersistedSession, persistSessionId, resolveSessionId],
  )

  const isCurrentGeneration = useCallback((gen: number) => gen === genRef.current, [])
  const nextGeneration = useCallback(() => ++genRef.current, [])

  return {
    restoreSession,
    persistSessionId,
    clearPersistedSession,
    resolveSessionId,
    genRef,
    isCurrentGeneration,
    nextGeneration,
  }
}
