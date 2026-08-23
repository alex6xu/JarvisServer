import { useState, useEffect, useCallback } from 'react'
import { apiFetch, useAccount } from '../context/AccountContext'
import VoiceInputButton from '../components/VoiceInputButton'
import MessageList from '../components/MessageList'
import RecentSessionSelect from '../components/RecentSessionSelect'
import { useVoiceInput } from '../hooks/useVoiceInput'
import { useRunEventStream } from '../hooks/useRunEventStream'
import { useSessionRestore } from '../hooks/useSessionRestore'
import { chatSessionKey, type UiMessage } from '../lib/sessionPersist'

export default function ChatPage() {
  const { currentAccount } = useAccount()
  const [messages, setMessages] = useState<UiMessage[]>([])
  const [input, setInput] = useState('')
  const [isLoading, setIsLoading] = useState(false)
  const [connected, setConnected] = useState(false)
  const [sessionId, setSessionId] = useState('')
  const [runId, setRunId] = useState('')

  const storageKey = currentAccount?.id ? chatSessionKey(currentAccount.id) : ''
  const { consumeRunEvents, abortRunStream } = useRunEventStream()
  const { restoreSession, persistSessionId, clearPersistedSession, clearServerActiveSession } =
    useSessionRestore()

  useEffect(() => {
    if (!currentAccount?.id || !storageKey) return

    let cancelled = false
    ;(async () => {
      try {
        const probe = await apiFetch('/v1/models', {}, currentAccount.id)
        if (!cancelled) setConnected(probe.ok)
      } catch {
        if (!cancelled) setConnected(false)
      }

      try {
        const result = await restoreSession({
          accountId: currentAccount.id,
          storageKey,
          mode: 'chat',
        })
        if (cancelled) return
        if (!result) {
          setMessages([])
          setSessionId('')
          setRunId('')
          setIsLoading(false)
          return
        }
        setSessionId(result.sessionId)
        setMessages(result.messages)
        if (result.activeRunId) {
          setRunId(result.activeRunId)
          setIsLoading(true)
          await consumeRunEvents(result.activeRunId, `run-${result.activeRunId}`, {
            accountId: currentAccount.id,
            afterSeq: result.afterSeq,
            fallbackModel: result.activeModel,
            onSessionId: setSessionId,
            setMessages,
            setIsLoading,
            setRunId,
          })
        } else {
          setRunId('')
          setIsLoading(false)
        }
      } catch (e) {
        if (!cancelled) console.error('restore chat session failed', e)
      }
    })()

    return () => {
      cancelled = true
      abortRunStream()
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [currentAccount?.id, storageKey])

  useEffect(() => {
    if (sessionId && storageKey) persistSessionId(storageKey, sessionId)
  }, [sessionId, storageKey, persistSessionId])

  const appendVoiceText = useCallback((text: string) => {
    setInput((prev) => {
      const base = prev.trimEnd()
      if (!base) return text
      const needsSpace = !/[\s\n]$/.test(base) && !/^[，。！？、,.!?]/.test(text)
      return base + (needsSpace ? ' ' : '') + text
    })
  }, [])

  const voice = useVoiceInput({
    lang: 'zh-CN',
    accountId: currentAccount?.id,
    onTranscript: (text, meta) => {
      if (meta.final) appendVoiceText(text)
    },
  })

  const sendMessage = async () => {
    if (!input.trim() || isLoading) return
    if (voice.listening) {
      await voice.stop()
    }

    const text = input
    const userMessage: UiMessage = {
      id: Date.now().toString(),
      role: 'user',
      content: text,
      timestamp: new Date(),
    }

    setMessages((prev) => [...prev, userMessage])
    setInput('')
    setIsLoading(true)

    try {
      const postChat = (sid: string) =>
        apiFetch(
          '/v1/agent/chat',
          {
            method: 'POST',
            body: JSON.stringify({
              message: text,
              session_id: sid || undefined,
              mode: 'chat',
              stream: false,
            }),
          },
          currentAccount?.id,
        )

      let response = await postChat(sessionId)
      let data = await response.json().catch(() => ({}))

      // Stale session_id → 404; drop it and start a fresh session once.
      if (response.status === 404 && sessionId) {
        setSessionId('')
        if (storageKey) clearPersistedSession(storageKey)
        response = await postChat('')
        data = await response.json().catch(() => ({}))
      }

      if (!response.ok) throw new Error(data.error || `HTTP ${response.status}`)

      if (data.session_id) {
        setSessionId(data.session_id)
        if (storageKey) persistSessionId(storageKey, data.session_id)
      }

      const assistantId = data.run_id ? `run-${data.run_id}` : Date.now().toString()
      setMessages((prev) => [
        ...prev,
        {
          id: assistantId,
          role: 'assistant',
          content: '',
          timestamp: new Date(),
          toolSteps: [],
          segments: [],
        },
      ])

      if (data.run_id) {
        setRunId(data.run_id)
        await consumeRunEvents(data.run_id, assistantId, {
          accountId: currentAccount?.id,
          onSessionId: setSessionId,
          setMessages,
          setIsLoading,
          setRunId,
        })
      } else {
        setMessages((prev) =>
          prev.map((m) =>
            m.id === assistantId ? { ...m, content: data.response || data.error || 'No response' } : m,
          ),
        )
        setIsLoading(false)
      }
    } catch (err) {
      setMessages((prev) => [
        ...prev,
        {
          id: Date.now().toString(),
          role: 'assistant',
          content: err instanceof Error ? `Error: ${err.message}` : 'Error: Failed to send message',
          timestamp: new Date(),
        },
      ])
      setIsLoading(false)
    }
  }

  const clearChat = () => {
    const clearedSessionId = sessionId
    abortRunStream()
    setMessages([])
    setSessionId('')
    setRunId('')
    setIsLoading(false)
    if (storageKey) clearPersistedSession(storageKey)
    if (currentAccount?.id && storageKey) {
      void clearServerActiveSession(
        { accountId: currentAccount.id, storageKey, mode: 'chat' },
        clearedSessionId,
      )
    }
  }

  return (
    <div className="flex flex-col h-full">
      <header className="h-14 flex items-center justify-between px-6 border-b border-border">
        <div>
          <h2 className="text-sm font-semibold text-foreground">Chat</h2>
          <p className="text-[11px] text-muted-foreground">
            {connected ? (
              <span className="flex items-center gap-1.5">
                <span className="w-1.5 h-1.5 rounded-full bg-success"></span>
                Connected
                {sessionId && <span className="ml-2">Session: {sessionId.substring(0, 8)}...</span>}
                {runId && <span className="ml-2 text-amber-600">运行中</span>}
              </span>
            ) : (
              <span className="flex items-center gap-1.5">
                <span className="w-1.5 h-1.5 rounded-full bg-destructive"></span>
                Disconnected
              </span>
            )}
          </p>
        </div>
        <div className="flex items-center gap-2">
          <RecentSessionSelect
            accountId={currentAccount?.id}
            mode="chat"
            currentSessionId={sessionId}
          />
          <button
            onClick={clearChat}
            className="h-8 px-3 text-[12px] text-muted-foreground hover:text-foreground border border-border rounded-md hover:bg-accent transition-colors"
          >
            Clear
          </button>
        </div>
      </header>

      <MessageList
        messages={messages}
        isLoading={isLoading}
        empty={
          <div className="flex-1 flex items-center justify-center">
            <div className="text-center animate-fade-in">
              <div className="w-12 h-12 rounded-xl bg-primary/10 flex items-center justify-center mx-auto mb-4">
                <svg
                  width="24"
                  height="24"
                  viewBox="0 0 24 24"
                  fill="none"
                  stroke="#3b82f6"
                  strokeWidth="2"
                  strokeLinecap="round"
                  strokeLinejoin="round"
                >
                  <path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z" />
                </svg>
              </div>
              <h3 className="text-base font-semibold text-foreground mb-1.5">Start a conversation</h3>
              <p className="text-[13px] text-muted-foreground mb-4 max-w-sm">
                Ask anything. Switching pages keeps this session. Open history from Sessions to resume.
              </p>
              <div className="text-left bg-card border border-border rounded-xl p-4 max-w-sm mx-auto">
                <p className="text-[12px] font-medium text-foreground mb-2">Tips:</p>
                <ul className="text-[12px] text-muted-foreground space-y-1.5">
                  <li>1. Go to Providers page and add your API provider</li>
                  <li>2. Set a provider as default</li>
                  <li>3. Come back and start chatting</li>
                </ul>
              </div>
            </div>
          </div>
        }
      />

      <div className="p-4 border-t border-border">
        <div className="flex gap-2 max-w-3xl mx-auto">
          <textarea
            value={input}
            onChange={(e) => setInput(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter' && !e.shiftKey) {
                e.preventDefault()
                void sendMessage()
              }
            }}
            placeholder="Type a message..."
            rows={1}
            className="flex-1 px-4 py-2.5 bg-card border border-border rounded-xl text-[13px] text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-ring resize-none"
          />
          <VoiceInputButton
            listening={voice.listening}
            supported={voice.supported}
            disabled={isLoading}
            title={
              voice.engine === 'server'
                ? '语音输入（服务端 ASR）'
                : '语音输入（浏览器 Web Speech）'
            }
            onClick={() => void voice.toggle()}
          />
          <button
            onClick={() => void sendMessage()}
            disabled={!input.trim() || isLoading}
            className="h-10 px-4 bg-primary text-primary-foreground rounded-xl text-[13px] font-medium hover:bg-primary/90 disabled:opacity-50 transition-colors"
          >
            Send
          </button>
        </div>
      </div>
    </div>
  )
}
