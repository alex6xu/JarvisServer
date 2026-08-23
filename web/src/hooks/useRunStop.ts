import { useCallback, useEffect, useState, type Dispatch, type SetStateAction } from 'react'
import { apiFetch } from '../context/AccountContext'
import type { UiMessage } from '../lib/sessionPersist'

type Options = {
  accountId?: number
  runId: string
  abortRunStream: () => void
  setRunId: (value: string) => void
  setIsLoading: (value: boolean) => void
  setMessages: Dispatch<SetStateAction<UiMessage[]>>
  onStopped?: () => void
}

export function useRunStop(options: Options) {
  const [isStopping, setIsStopping] = useState(false)

  useEffect(() => {
    if (!options.runId) setIsStopping(false)
  }, [options.runId])

  const stopRun = useCallback(async () => {
    if (!options.runId || isStopping) return
    const targetRunId = options.runId
    setIsStopping(true)
    try {
      const response = await apiFetch(
        `/v1/agent/runs/${encodeURIComponent(targetRunId)}/cancel`,
        { method: 'POST' },
        options.accountId,
      )
      const data = await response.json().catch(() => ({}))
      if (!response.ok) throw new Error(data.error || `HTTP ${response.status}`)
      options.abortRunStream()
      options.setRunId('')
      options.setIsLoading(false)
      options.onStopped?.()
      options.setMessages((prev) => [
        ...prev,
        { id: `stopped-${Date.now()}`, role: 'system', content: '当前任务已停止', timestamp: new Date() },
      ])
    } catch (error) {
      options.setMessages((prev) => [
        ...prev,
        {
          id: `stop-error-${Date.now()}`,
          role: 'system',
          content: error instanceof Error ? `停止失败: ${error.message}` : '停止失败',
          timestamp: new Date(),
        },
      ])
    } finally {
      setIsStopping(false)
    }
  }, [isStopping, options])

  return { isStopping, stopRun }
}
