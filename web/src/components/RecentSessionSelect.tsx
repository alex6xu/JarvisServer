import { useEffect, useState } from 'react'
import { apiFetch } from '../context/AccountContext'

type RecentSession = {
  id: string
  title?: string
  updated_at?: string
  active_run_status?: string
}

type Props = {
  accountId?: number
  mode: 'chat' | 'coder'
  workspaceId?: string
  currentSessionId?: string
  variant?: 'default' | 'context'
}

export default function RecentSessionSelect({
  accountId,
  mode,
  workspaceId,
  currentSessionId,
  variant = 'default',
}: Props) {
  const [sessions, setSessions] = useState<RecentSession[]>([])

  useEffect(() => {
    if (!accountId || (mode === 'coder' && !workspaceId)) {
      setSessions([])
      return
    }
    let cancelled = false
    const params = new URLSearchParams({ mode, limit: '10' })
    if (workspaceId) params.set('workspace_id', workspaceId)
    ;(async () => {
      try {
        const response = await apiFetch(`/v1/agent/sessions/recent?${params}`, {}, accountId)
        if (!response.ok || cancelled) return
        const data = (await response.json()) as { sessions?: RecentSession[] }
        if (!cancelled) setSessions(data.sessions || [])
      } catch {
        if (!cancelled) setSessions([])
      }
    })()
    return () => {
      cancelled = true
    }
  }, [accountId, currentSessionId, mode, workspaceId])

  if (!sessions.length && variant !== 'context') return null

  const openSession = (sessionId: string) => {
    if (!sessionId || sessionId === currentSessionId) return
    const url = new URL(window.location.href)
    url.searchParams.set('session', sessionId)
    url.searchParams.delete('resume')
    if (mode === 'coder' && workspaceId) url.searchParams.set('workspace', workspaceId)
    window.location.assign(`${url.pathname}${url.search}${url.hash}`)
  }

  return (
    <select
      aria-label="最近会话"
      title="最近 10 个会话"
      value={currentSessionId || ''}
      onChange={(event) => openSession(event.target.value)}
      className={variant === 'context'
        ? 'workbench-context-select workbench-session-select'
        : 'h-8 w-[180px] max-w-[32vw] px-2 bg-card border border-border rounded-md text-[12px] text-foreground focus:outline-none focus:ring-2 focus:ring-ring'}
    >
      {!currentSessionId && <option value="">{variant === 'context' ? '新会话' : '最近会话'}</option>}
      {currentSessionId && !sessions.some((session) => session.id === currentSessionId) && (
        <option value={currentSessionId}>当前会话 · {currentSessionId.slice(0, 8)}</option>
      )}
      {sessions.map((session) => (
        <option key={session.id} value={session.id}>
          {session.active_run_status === 'running' || session.active_run_status === 'queued' ? '运行中 · ' : ''}
          {session.title || session.id}
        </option>
      ))}
    </select>
  )
}
