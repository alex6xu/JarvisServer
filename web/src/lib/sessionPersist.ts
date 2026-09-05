/** Browser keys and helpers for restoring Chat / Coder sessions. */
import type { MessageDocument } from '../types/documents'

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

export type ToolStep = {
  tool: string
  args: string
  result: string
  id?: string
  status?: 'running' | 'done' | 'error' | string
}

/** Chronological assistant timeline: text and tool calls in call order. */
export type MessageSegment =
  | { type: 'text'; content: string }
  | { type: 'tool'; step: ToolStep }

export type UiMessage = {
  id: string
  role: ChatMessageRole
  content: string
  timestamp: Date
  model?: string
  toolSteps?: ToolStep[]
  /** Preferred render path for assistants; falls back to content + toolSteps. */
  segments?: MessageSegment[]
  documents?: MessageDocument[]
}

export function appendTextSegment(segments: MessageSegment[], text: string): MessageSegment[] {
  if (!text) return segments
  const next = segments.slice()
  const last = next[next.length - 1]
  if (last?.type === 'text') {
    next[next.length - 1] = { type: 'text', content: last.content + text }
  } else {
    next.push({ type: 'text', content: text })
  }
  return next
}

export function appendToolSegment(segments: MessageSegment[], step: ToolStep): MessageSegment[] {
  return [...segments, { type: 'tool', step }]
}

/** Insert or update a tool segment by step.id (start→end lifecycle). */
export function upsertToolSegment(segments: MessageSegment[], step: ToolStep): MessageSegment[] {
  if (step.id) {
    const next = segments.slice()
    for (let i = 0; i < next.length; i++) {
      const seg = next[i]
      if (seg.type === 'tool' && seg.step.id === step.id) {
        next[i] = { type: 'tool', step: { ...seg.step, ...step } }
        return next
      }
    }
  }
  return appendToolSegment(segments, step)
}

export function upsertToolStep(steps: ToolStep[], step: ToolStep): ToolStep[] {
  if (step.id) {
    const idx = steps.findIndex((s) => s.id === step.id)
    if (idx >= 0) {
      const next = steps.slice()
      next[idx] = { ...next[idx], ...step }
      return next
    }
  }
  return [...steps, step]
}

/** Rebuild flat fields / fallback timeline when only content+tools are known. */
export function segmentsFromContentAndTools(content: string, steps?: ToolStep[]): MessageSegment[] {
  const segs: MessageSegment[] = []
  if (content) segs.push({ type: 'text', content })
  for (const step of steps || []) segs.push({ type: 'tool', step })
  return segs
}

export function contentFromSegments(segments: MessageSegment[] | undefined): string {
  if (!segments?.length) return ''
  return segments
    .filter((s): s is { type: 'text'; content: string } => s.type === 'text')
    .map((s) => s.content)
    .join('')
}

export function toolStepsFromSegments(segments: MessageSegment[] | undefined): ToolStep[] {
  if (!segments?.length) return []
  return segments.filter((s): s is { type: 'tool'; step: ToolStep } => s.type === 'tool').map((s) => s.step)
}

/** Merge final tool_steps (with results) onto existing tool segments by index. */
export function syncToolResultsInSegments(segments: MessageSegment[], steps: ToolStep[]): MessageSegment[] {
  let toolIdx = 0
  return segments.map((seg) => {
    if (seg.type !== 'tool') return seg
    const updated = steps[toolIdx++]
    return updated ? { type: 'tool' as const, step: updated } : seg
  })
}

export type RestoredSessionMessage = {
  id: string
  role: string
  content: string
  model?: string
  created_at?: string
  tool_steps?: ToolStep[]
  toolSteps?: ToolStep[]
  documents?: MessageDocument[]
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
    type?: 'chat' | 'code'
    platform?: string
    message_count?: number
    workspace_id?: string
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
  return (messages || []).map((m) => {
    const toolSteps = m.tool_steps || m.toolSteps
    const content = m.content || ''
    return {
      id: m.id,
      role: (m.role as ChatMessageRole) || 'assistant',
      content,
      timestamp: m.created_at ? new Date(m.created_at) : new Date(),
      model: m.model,
      documents: m.documents,
      toolSteps,
      segments:
        m.role === 'assistant' || (!m.role && content)
          ? segmentsFromContentAndTools(content, toolSteps)
          : undefined,
    }
  })
}

/** Attach tool steps from a finished/active run onto the last assistant message when missing. */
export function attachToolStepsToMessages<
  T extends { id: string; role: string; content?: string; toolSteps?: ToolStep[]; segments?: MessageSegment[] },
>(messages: T[], steps?: ToolStep[]): T[] {
  if (!steps?.length) return messages
  for (let i = messages.length - 1; i >= 0; i--) {
    if (messages[i].role === 'assistant') {
      if (messages[i].toolSteps?.length || messages[i].segments?.some((s) => s.type === 'tool')) {
        return messages
      }
      const next = [...messages]
      const content = messages[i].content || ''
      next[i] = {
        ...next[i],
        toolSteps: steps,
        segments: segmentsFromContentAndTools(content, steps),
      }
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
  pinned?: boolean
  queue_version?: number
  queue_item?: {
    id: string
    event_type: 'enqueue' | 'pin' | 'steer'
    status: string
  }
  queue_items?: Array<{
    id: string
    event_type: 'enqueue' | 'pin' | 'steer'
    status: string
  }>
}
