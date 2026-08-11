/** Browser keys and helpers for restoring Chat / Coder sessions. */

export function coderWorkspaceKey(accountId: number) {
  return `cg_coder_workspace_${accountId}`
}

export function coderSessionKey(accountId: number, workspaceId: string) {
  return `cg_coder_session_${accountId}_${workspaceId || 'none'}`
}

export function chatSessionKey(accountId: number) {
  return `cg_chat_session_${accountId}`
}

export function readLocal(key: string): string {
  try {
    return localStorage.getItem(key) || ''
  } catch {
    return ''
  }
}

export function writeLocal(key: string, value: string) {
  try {
    if (value) localStorage.setItem(key, value)
    else localStorage.removeItem(key)
  } catch {
    /* ignore quota / private mode */
  }
}

export function clearLocal(key: string) {
  writeLocal(key, '')
}

export function readSessionQueryParam(search = typeof window !== 'undefined' ? window.location.search : ''): string {
  try {
    return new URLSearchParams(search).get('session') || new URLSearchParams(search).get('resume') || ''
  } catch {
    return ''
  }
}

/** Only write ?session= when continuing an existing conversation; avoid leaving
 *  stale ids in the URL that cause restore 404 loops on next visit. */
export function writeSessionQueryParam(sessionId: string) {
  try {
    const url = new URL(window.location.href)
    const current = url.searchParams.get('session') || url.searchParams.get('resume') || ''
    if (sessionId) {
      // Keep URL in sync when already deep-linked or explicitly continuing.
      if (current && current !== sessionId) {
        url.searchParams.set('session', sessionId)
        url.searchParams.delete('resume')
        window.history.replaceState({}, '', `${url.pathname}${url.search}${url.hash}`)
      } else if (current === sessionId) {
        url.searchParams.delete('resume')
        if (url.searchParams.has('resume')) {
          window.history.replaceState({}, '', `${url.pathname}${url.search}${url.hash}`)
        }
      }
      // If there was no session query, do not inject one on every chat turn.
      return
    }
    url.searchParams.delete('session')
    url.searchParams.delete('resume')
    window.history.replaceState({}, '', `${url.pathname}${url.search}${url.hash}`)
  } catch {
    /* ignore */
  }
}

export type ChatMessageRole = 'user' | 'assistant' | 'system'

export type ToolStep = { tool: string; args: string; result: string }

export type UiMessage = {
  id: string
  role: ChatMessageRole
  content: string
  timestamp: Date
  model?: string
  toolSteps?: ToolStep[]
}

export type RestoredSessionMessage = {
  id: string
  role: string
  content: string
  model?: string
  created_at?: string
  tool_steps?: ToolStep[]
  toolSteps?: ToolStep[]
}

export type ActiveRunInfo = {
  id: string
  status: string
  last_seq: number
  model?: string
  workspace_id?: string
}

export type SessionRestorePayload = {
  session?: {
    id?: string
    title?: string
    platform?: string
    message_count?: number
  }
  messages: RestoredSessionMessage[]
  workspace_id?: string
  active_run?: ActiveRunInfo
  active_run_tool_steps?: ToolStep[]
  latest_run?: ActiveRunInfo
  latest_run_tool_steps?: ToolStep[]
  last_event_seq?: number
}

export function mapRestoredMessages(messages: RestoredSessionMessage[] | undefined): UiMessage[] {
  return (messages || []).map((m) => ({
    id: m.id,
    role: (m.role as ChatMessageRole) || 'assistant',
    content: m.content || '',
    timestamp: m.created_at ? new Date(m.created_at) : new Date(),
    model: m.model,
    toolSteps: m.tool_steps || m.toolSteps,
  }))
}

/** Attach tool steps from a finished/active run onto the last assistant message when missing. */
export function attachToolStepsToMessages<T extends { id: string; role: string; toolSteps?: ToolStep[] }>(
  messages: T[],
  steps?: ToolStep[],
): T[] {
  if (!steps?.length) return messages
  for (let i = messages.length - 1; i >= 0; i--) {
    if (messages[i].role === 'assistant') {
      if (messages[i].toolSteps?.length) return messages
      const next = [...messages]
      next[i] = { ...next[i], toolSteps: steps }
      return next
    }
  }
  return messages
}

export type AgentStreamEvent = {
  type?: string
  content?: string
  session_id?: string
  model?: string
  step?: ToolStep
  tool_steps?: ToolStep[]
}
