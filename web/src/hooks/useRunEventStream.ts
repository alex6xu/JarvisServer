import { useCallback, useRef, type Dispatch, type SetStateAction } from 'react'
import { apiFetch } from '../context/AccountContext'
import {
  appendTextSegment,
  syncToolResultsInSegments,
  upsertToolSegment,
  upsertToolStep,
  type AgentStreamEvent,
  type MessageSegment,
  type ToolStep,
  type UiMessage,
} from '../lib/sessionPersist'

type ConsumeOpts = {
  accountId?: number
  afterSeq?: number
  fallbackModel?: string
  onSessionId?: (sessionId: string) => void
  onUserInjected?: (content: string, pinned: boolean) => void
  onQueueChanged?: () => void
  setMessages: Dispatch<SetStateAction<UiMessage[]>>
  setIsLoading?: (v: boolean) => void
  setRunId?: (v: string) => void
}

/**
 * Shared SSE consumer for session runs. Aborting a previous stream when a new
 * one starts keeps Chat/Coder from racing after restore or rapid sends.
 */
export function useRunEventStream() {
  const abortRef = useRef<AbortController | null>(null)

  const abort = useCallback(() => {
    abortRef.current?.abort()
    abortRef.current = null
  }, [])

  const consumeRunEvents = useCallback(
    async (targetRunId: string, assistantId: string, opts: ConsumeOpts) => {
      abort()
      const ac = new AbortController()
      abortRef.current = ac

      const response = await apiFetch(
        `/v1/agent/runs/${targetRunId}/events?after_seq=${opts.afterSeq ?? 0}`,
        { signal: ac.signal },
        opts.accountId,
      )
      if (!response.ok || !response.body) {
        const data = await response.json().catch(() => ({}))
        throw new Error(data.error || `HTTP ${response.status}`)
      }

      const reader = response.body.getReader()
      const decoder = new TextDecoder()
      let buffer = ''
      let fullText = ''
      const steps: ToolStep[] = []
      let segments: MessageSegment[] = []

      const applyAssistant = (patch: Partial<UiMessage>) => {
        opts.setMessages((prev) => prev.map((m) => (m.id === assistantId ? { ...m, ...patch } : m)))
      }

      const publish = (model?: string) => {
        applyAssistant({
          content: fullText,
          model: model || opts.fallbackModel,
          toolSteps: steps.length ? [...steps] : undefined,
          segments: [...segments],
        })
      }

      try {
        while (true) {
          const { done, value } = await reader.read()
          if (done) break
          buffer += decoder.decode(value, { stream: true })
          const parts = buffer.split('\n\n')
          buffer = parts.pop() || ''
          for (const part of parts) {
            const line = part.trim()
            if (!line.startsWith('data:')) continue
            const payload = line.replace(/^data:\s*/, '')
            if (payload === '[DONE]') continue
            let ev: AgentStreamEvent
            try {
              ev = JSON.parse(payload)
            } catch {
              continue
            }
            if (ev.session_id) opts.onSessionId?.(ev.session_id)
            if (ev.type?.startsWith('queue.')) opts.onQueueChanged?.()
            if (ev.type === 'delta' && ev.content) {
              fullText += ev.content
              segments = appendTextSegment(segments, ev.content)
              publish(ev.model)
            } else if (ev.type === 'tool_step' && ev.step) {
              const merged = upsertToolStep(steps, ev.step)
              steps.splice(0, steps.length, ...merged)
              segments = upsertToolSegment(segments, ev.step)
              publish(ev.model)
            } else if (ev.type === 'user_injected' && ev.content) {
              opts.onUserInjected?.(ev.content, !!ev.pinned)
            } else if (ev.type === 'done') {
              if (ev.content && !fullText) {
                fullText = ev.content
                if (!segments.some((s) => s.type === 'text')) {
                  segments = appendTextSegment(segments, ev.content)
                }
              } else if (ev.content) {
                fullText = ev.content
              }
              if (ev.tool_steps?.length) {
                steps.splice(0, steps.length, ...ev.tool_steps)
                if (segments.some((s) => s.type === 'tool')) {
                  segments = syncToolResultsInSegments(segments, steps)
                } else {
                  for (const step of steps) segments = upsertToolSegment(segments, step)
                }
              }
              publish(ev.model)
            } else if (ev.type === 'error') {
              const errText = ev.content || 'error'
              if (fullText) {
                const note = `\n\n⚠️ ${errText}`
                fullText = `${fullText}${note}`
                segments = appendTextSegment(segments, note)
              } else {
                fullText = errText
                segments = appendTextSegment(segments, errText)
              }
              publish()
            }
          }
        }
      } catch (err) {
        if ((err as Error).name === 'AbortError') return
        throw err
      } finally {
        if (abortRef.current === ac) {
          abortRef.current = null
          opts.setIsLoading?.(false)
          opts.setRunId?.('')
        }
      }

      if (!fullText && steps.length === 0) {
        applyAssistant({ content: 'No response', segments: [{ type: 'text', content: 'No response' }] })
      } else if (fullText.includes('no available channel')) {
        const msg = '⚠️ 暂无可用提供商。请先到 Providers 页面添加 API Provider。'
        applyAssistant({ content: msg, segments: [{ type: 'text', content: msg }] })
      }
    },
    [abort],
  )

  return { consumeRunEvents, abortRunStream: abort, runAbortRef: abortRef }
}
