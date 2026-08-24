import { useCallback, useEffect, useState } from 'react'
import { apiFetch } from '../context/AccountContext'

export type QueueEventType = 'enqueue' | 'pin' | 'steer'
export type QueueItemStatus =
  | 'pending'
  | 'injecting'
  | 'injected'
  | 'executing'
  | 'completed'
  | 'answered'
  | 'failed'
  | 'cancelled'
  | 'dropped'

export type RunMessageQueueItem = {
  id: string
  session_id: string
  run_id: string
  content: string
  event_type: QueueEventType
  position: number
  status: QueueItemStatus
  created_at: string
  updated_at: string
}

export type RunMessageQueueSnapshot = {
  run_id: string
  version: number
  items: RunMessageQueueItem[]
}

export function createQueueIdempotencyKey() {
  if (typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function') {
    return crypto.randomUUID()
  }
  return `queue-${Date.now()}-${Math.random().toString(36).slice(2)}`
}

export function pendingQueueOrder(snapshot: RunMessageQueueSnapshot): string[] {
  return snapshot.items.filter((item) => item.status === 'pending').map((item) => item.id)
}

export function movePendingMessage(
  snapshot: RunMessageQueueSnapshot,
  messageId: string,
  direction: -1 | 1,
): string[] {
  const order = pendingQueueOrder(snapshot)
  const index = order.indexOf(messageId)
  const target = index + direction
  if (index < 0 || target < 0 || target >= order.length) return order
  const next = order.slice()
  ;[next[index], next[target]] = [next[target], next[index]]
  return next
}

type QueueResponse = RunMessageQueueSnapshot | { queue?: RunMessageQueueSnapshot }

export class QueueUnavailableError extends Error {
  constructor(public status: number, message: string) {
    super(message)
    this.name = 'QueueUnavailableError'
  }
}

export function isQueueUnavailableError(error: unknown): error is QueueUnavailableError {
  return error instanceof QueueUnavailableError && (error.status === 404 || error.status === 409)
}

function normalizedSnapshot(body: QueueResponse): RunMessageQueueSnapshot | null {
  if ('queue' in body) return body.queue || null
  const snapshot = body as RunMessageQueueSnapshot
  return typeof snapshot.version === 'number' && Array.isArray(snapshot.items) ? snapshot : null
}

export function useRunMessageQueue(accountId: number | undefined, runId: string, sessionId = '') {
  const [snapshot, setSnapshot] = useState<RunMessageQueueSnapshot>({
    run_id: '',
    version: 0,
    items: [],
  })
  const [mode, setMode] = useState<QueueEventType>('enqueue')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  const request = useCallback(
    async (path: string, init?: RequestInit) => {
      const response = await apiFetch(path, init || {}, accountId)
      const body = (await response.json().catch(() => ({}))) as QueueResponse & { error?: string }
      if (!response.ok) {
        throw new QueueUnavailableError(response.status, body.error || `HTTP ${response.status}`)
      }
      const next = normalizedSnapshot(body)
      if (next) setSnapshot(next)
      setError('')
      return next
    },
    [accountId],
  )

  const refresh = useCallback(async (targetRunId = runId) => {
    if (!accountId || !targetRunId) return null
    try {
      return await request(`/v1/agent/runs/${encodeURIComponent(targetRunId)}/messages/queue`)
    } catch (queueError) {
      setError(queueError instanceof Error ? queueError.message : '无法读取消息队列')
      return null
    }
  }, [accountId, request, runId])

  useEffect(() => {
    if (!accountId || !runId) {
      setSnapshot({ run_id: '', version: 0, items: [] })
      setError('')
      setMode('enqueue')
      return
    }
    void refresh()
  }, [accountId, refresh, runId])

  const submit = useCallback(
    async (content: string, eventType = mode) => {
      if (!sessionId) throw new Error('当前会话尚未准备好')
      setBusy(true)
      try {
        return await request(`/v1/agent/sessions/${encodeURIComponent(sessionId)}/messages/queue`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            content,
            event_type: eventType,
            idempotency_key: createQueueIdempotencyKey(),
          }),
        })
      } catch (queueError) {
        const message = queueError instanceof Error ? queueError.message : '消息排队失败'
        setError(message)
        throw queueError
      } finally {
        setBusy(false)
      }
    },
    [mode, request, sessionId],
  )

  const pin = useCallback(
    async (messageId: string) => {
      setBusy(true)
      try {
        await request(
          `/v1/agent/runs/${encodeURIComponent(runId)}/messages/${encodeURIComponent(messageId)}/pin`,
          { method: 'POST' },
        )
      } catch (queueError) {
        setError(queueError instanceof Error ? queueError.message : '置顶失败')
      } finally {
        setBusy(false)
      }
    },
    [request, runId],
  )

  const reorder = useCallback(
    async (order: string[]) => {
      setBusy(true)
      try {
        await request(`/v1/agent/runs/${encodeURIComponent(runId)}/messages/queue/order`, {
          method: 'PUT',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ version: snapshot.version, order }),
        })
      } catch (queueError) {
        await refresh()
        setError(
          queueError instanceof Error && queueError.message.includes('version conflict')
            ? '队列已在其他页面更新，请重新排序'
            : queueError instanceof Error
              ? queueError.message
              : '排序失败',
        )
      } finally {
        setBusy(false)
      }
    },
    [refresh, request, runId, snapshot.version],
  )

  const move = useCallback(
    async (messageId: string, direction: -1 | 1) => {
      const order = movePendingMessage(snapshot, messageId, direction)
      if (order.join('\0') === pendingQueueOrder(snapshot).join('\0')) return
      await reorder(order)
    },
    [reorder, snapshot],
  )

  const cancel = useCallback(
    async (messageId: string) => {
      setBusy(true)
      try {
        await request(
          `/v1/agent/runs/${encodeURIComponent(runId)}/messages/${encodeURIComponent(messageId)}`,
          { method: 'DELETE' },
        )
      } catch (queueError) {
        setError(queueError instanceof Error ? queueError.message : '取消失败')
      } finally {
        setBusy(false)
      }
    },
    [request, runId],
  )

  return { snapshot, mode, setMode, error, busy, refresh, submit, pin, move, cancel }
}
