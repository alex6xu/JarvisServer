import { useState, useEffect, useCallback, useRef } from 'react'
import { ArrowUp, Bot, Folder, MessagesSquare, Plus, SquarePen, X } from 'lucide-react'
import { useAppearance } from '../context/AppearanceContext'
import WorkbenchWelcome from '../components/WorkbenchWelcome'
import { apiFetch, useAccount } from '../context/AccountContext'
import VoiceInputButton from '../components/VoiceInputButton'
import MessageList from '../components/MessageList'
import RecentSessionSelect from '../components/RecentSessionSelect'
import StopRunButton from '../components/StopRunButton'
import RunMessageQueue, { QueueModeControl } from '../components/RunMessageQueue'
import { useVoiceInput } from '../hooks/useVoiceInput'
import { useRunEventStream } from '../hooks/useRunEventStream'
import { useRunStop } from '../hooks/useRunStop'
import { isQueueUnavailableError, useRunMessageQueue } from '../hooks/useRunMessageQueue'
import { useSessionRestore } from '../hooks/useSessionRestore'
import { chatModelKey, chatSessionKey, readLocal, writeLocal, type UiMessage } from '../lib/sessionPersist'
import DocumentPicker from '../components/DocumentPicker'
import DocumentChips from '../components/DocumentChips'
import type { ProjectDocument, ProjectSummary } from '../types/documents'

type ModelOption = { id: string }

export default function ChatPage() {
  const { currentAccount } = useAccount()
  const { homeLayout } = useAppearance()
  const workbench = homeLayout === 'workbench'
  const inputRef = useRef<HTMLTextAreaElement>(null)
  const startNew = useRef(new URLSearchParams(window.location.search).get('new') === '1')
  const [creatingProject, setCreatingProject] = useState(false)
  const [projectError, setProjectError] = useState('')
  const [projectBusy, setProjectBusy] = useState(false)
  const [messages, setMessages] = useState<UiMessage[]>([])
  const [input, setInput] = useState('')
  const [isLoading, setIsLoading] = useState(false)
  const [restoring, setRestoring] = useState(true)
  const [connected, setConnected] = useState(false)
  const [sessionId, setSessionId] = useState('')
  const [runId, setRunId] = useState('')
  const [models, setModels] = useState<ModelOption[]>([])
  const [selectedModel, setSelectedModel] = useState('')
  const [projects, setProjects] = useState<ProjectSummary[]>([])
  const [projectId, setProjectId] = useState('')
  const [selectedDocuments, setSelectedDocuments] = useState<ProjectDocument[]>([])
  const [projectName, setProjectName] = useState('')
  const queue = useRunMessageQueue(currentAccount?.id, runId, sessionId)

  const storageKey = currentAccount?.id ? chatSessionKey(currentAccount.id) : ''
  const { consumeRunEvents, abortRunStream } = useRunEventStream()
  const { isStopping, stopRun } = useRunStop({
    accountId: currentAccount?.id,
    runId,
    abortRunStream,
    setRunId,
    setIsLoading,
    setMessages,
  })
  const { restoreSession, persistSessionId, clearPersistedSession, clearServerActiveSession } =
    useSessionRestore()

  useEffect(() => {
    if (!currentAccount?.id || !storageKey) return

    let cancelled = false
    setRestoring(true)
    setInput('')
    setMessages([])
    setSessionId('')
    setRunId('')
    setIsLoading(false)
    setProjectId(new URLSearchParams(window.location.search).get('project') || '')
    setSelectedDocuments([])
    const savedModel = readLocal(chatModelKey(currentAccount.id))
    setSelectedModel(savedModel)
    ;(async () => {
      try {
        const probe = await apiFetch('/v1/models', {}, currentAccount.id)
        if (cancelled) return
        setConnected(probe.ok)
        if (probe.ok) {
          const data = await probe.json()
          const available: ModelOption[] = (data.data || []).map((model: { id: string }) => ({ id: model.id }))
          setModels(available)
          const preferred = available.find((model) => model.id === savedModel)
            || available.find((model) => model.id === (data.default || 'auto'))
            || available[0]
          setSelectedModel(preferred?.id || '')
        }
      } catch {
        if (!cancelled) {
          setConnected(false)
          setModels([])
        }
      }

      try {
        if (startNew.current) {
          clearPersistedSession(storageKey)
          await clearServerActiveSession({ accountId: currentAccount.id, storageKey, mode: 'chat' })
          if (cancelled) return
          startNew.current = false
          const url = new URL(window.location.href)
          url.searchParams.delete('new')
          window.history.replaceState({}, '', `${url.pathname}${url.search}`)
          return
        }
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
        if (result.activeModel) setSelectedModel(result.activeModel)
        try {
          const assignmentResponse = await apiFetch(
            `/v1/agent/sessions/${encodeURIComponent(result.sessionId)}/project`,
            {},
            currentAccount.id,
          )
          if (assignmentResponse.ok && !cancelled) {
            const assignmentData = await assignmentResponse.json()
            setProjectId(assignmentData.assignment?.project?.id || '')
          }
        } catch {
          // A session without a project remains valid.
        }
        setRestoring(false)
        if (result.activeRunId) {
          setRunId(result.activeRunId)
          setIsLoading(true)
          await consumeRunEvents(result.activeRunId, `run-${result.activeRunId}`, {
            accountId: currentAccount.id,
            afterSeq: result.afterSeq,
            fallbackModel: result.activeModel,
            onSessionId: setSessionId,
            onQueueChanged: () => void queue.refresh(result.activeRunId),
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
      } finally {
        if (!cancelled) setRestoring(false)
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

  useEffect(() => {
    if (currentAccount?.id && selectedModel) writeLocal(chatModelKey(currentAccount.id), selectedModel)
  }, [currentAccount?.id, selectedModel])

  useEffect(() => {
    if (!currentAccount?.id) return
    let cancelled = false
    setProjects([])
    setProjectError('')
    void (async () => {
      try {
        const response = await apiFetch('/v1/projects', {}, currentAccount.id)
        if (!response.ok) throw new Error('项目加载失败')
        const body = await response.json()
        if (!cancelled) setProjects(Array.isArray(body.projects) ? body.projects : [])
      } catch {
        if (!cancelled) setProjectError('项目加载失败')
      }
    })()
    return () => { cancelled = true }
  }, [currentAccount?.id])

  const createProject = async () => {
    if (!currentAccount?.id || !projectName.trim() || projectBusy) return
    setProjectBusy(true)
    setProjectError('')
    try {
      const response = await apiFetch('/v1/projects', { method: 'POST', body: JSON.stringify({ name: projectName.trim() }) }, currentAccount.id)
      const body = await response.json().catch(() => ({}))
      if (!response.ok || !body.project) throw new Error(body.error || '项目创建失败')
      setProjects((current) => [body.project, ...current])
      if (sessionId) {
        const assignmentResponse = await apiFetch(
          `/v1/agent/sessions/${encodeURIComponent(sessionId)}/project`,
          { method: 'PUT', body: JSON.stringify({ project_id: body.project.id, pinned: true }) },
          currentAccount.id,
        )
        if (!assignmentResponse.ok) {
          const assignmentBody = await assignmentResponse.json().catch(() => ({}))
          throw new Error(assignmentBody.error || '新项目已创建，但会话关联失败')
        }
      }
      setProjectId(body.project.id)
      setProjectName('')
      setSelectedDocuments([])
      setCreatingProject(false)
      window.dispatchEvent(new Event('jarvis:sessions-changed'))
    } catch (err) {
      setProjectError(err instanceof Error ? err.message : '项目创建失败')
    } finally { setProjectBusy(false) }
  }

  const changeProject = async (nextProjectId: string) => {
    if (!currentAccount?.id || projectBusy || isLoading) return
    const previousProjectId = projectId
    setProjectId(nextProjectId)
    setSelectedDocuments([])
    if (!sessionId) return
    setProjectBusy(true)
    setProjectError('')
    try {
      const response = await apiFetch(
        `/v1/agent/sessions/${encodeURIComponent(sessionId)}/project`,
        nextProjectId
          ? { method: 'PUT', body: JSON.stringify({ project_id: nextProjectId, pinned: true }) }
          : { method: 'DELETE' },
        currentAccount.id,
      )
      const body = await response.json().catch(() => ({}))
      if (!response.ok) throw new Error(body.error || '会话项目关联失败')
      window.dispatchEvent(new Event('jarvis:sessions-changed'))
    } catch (err) {
      setProjectId(previousProjectId)
      setProjectError(err instanceof Error ? err.message : '会话项目关联失败')
    } finally {
      setProjectBusy(false)
    }
  }

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
    if (!input.trim() || restoring || !currentAccount?.id || queue.busy) return
    // The run inbox currently accepts text only. Do not create an optimistic
    // message that cannot be persisted with its attachments.
    if (isLoading && runId && selectedDocuments.length > 0) return
    if (voice.listening) {
      await voice.stop()
    }

    const text = input
    const userMessage: UiMessage = {
      id: Date.now().toString(),
      role: 'user',
      content: text,
      timestamp: new Date(),
      documents: selectedDocuments,
    }

    setMessages((prev) => [...prev, userMessage])
    setInput('')

    if (isLoading && runId) {
      try {
        await queue.submit(text)
        return
      } catch (err) {
        if (isQueueUnavailableError(err)) {
          // The previous run ended while Send was being pressed. Continue below
          // as a normal follow-up instead of surfacing a misleading queue 404.
          setRunId('')
          setIsLoading(false)
        } else {
          setMessages((prev) => [
            ...prev,
            {
              id: `queue-error-${Date.now()}`,
              role: 'system',
              content: err instanceof Error ? `排队失败: ${err.message}` : '排队失败',
              timestamp: new Date(),
            },
          ])
          return
        }
      }
    }
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
              model: selectedModel || undefined,
              project_id: projectId || undefined,
              document_ids: selectedDocuments.map((document) => document.id),
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
      setSelectedDocuments([])

      if (data.session_id) {
        setSessionId(data.session_id)
        if (storageKey) persistSessionId(storageKey, data.session_id)
        window.dispatchEvent(new Event('jarvis:sessions-changed'))
      }

      const assistantId = data.run_id ? `run-${data.run_id}` : Date.now().toString()
      setMessages((prev) => [
        ...prev,
        {
          id: assistantId,
          role: 'assistant',
          content: '',
          timestamp: new Date(),
          model: selectedModel || undefined,
          toolSteps: [],
          segments: [],
        },
      ])

      if (data.run_id) {
        setRunId(data.run_id)
        await consumeRunEvents(data.run_id, assistantId, {
          accountId: currentAccount?.id,
          fallbackModel: selectedModel,
          onSessionId: setSessionId,
          onQueueChanged: () => void queue.refresh(data.run_id),
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
    setInput('')
    setSelectedDocuments([])
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
    <div className={workbench ? 'workbench-chat' : 'flex flex-col h-full'}>
      <header className={workbench ? 'workbench-chat-header' : 'h-14 flex items-center justify-between px-6 border-b border-border'}>
        <div>
          <h2 className="text-sm font-semibold text-foreground">{workbench ? '工作台' : 'Chat'}</h2>
          <p className="text-[11px] text-muted-foreground">
            {connected ? (
              <span className="flex items-center gap-1.5">
                <span className="w-1.5 h-1.5 rounded-full bg-success"></span>
                {workbench ? '已连接' : 'Connected'}
                {sessionId && <span className="ml-2">Session: {sessionId.substring(0, 8)}...</span>}
                {runId && <span className="ml-2 text-amber-600">运行中</span>}
              </span>
            ) : (
              <span className="flex items-center gap-1.5" title="模型服务未连接">
                <span className="w-1.5 h-1.5 rounded-full bg-destructive"></span>
                {workbench ? '未连接' : 'Disconnected'}
              </span>
            )}
          </p>
        </div>
        <div className="flex items-center gap-2">
          {!workbench && <RecentSessionSelect
            accountId={currentAccount?.id}
            mode="chat"
            currentSessionId={sessionId}
          />}
          {!workbench && <select aria-label="当前模型" value={selectedModel} onChange={(event) => setSelectedModel(event.target.value)} disabled={isLoading} className="h-8 max-w-[200px] rounded-md border border-border bg-card px-2 text-[12px] text-foreground">
            {selectedModel && !models.some((model) => model.id === selectedModel) && <option value={selectedModel}>{selectedModel}</option>}
            {models.length === 0 && !selectedModel ? <option value="">默认模型</option> : models.map((model) => <option key={model.id} value={model.id}>{model.id === 'auto' ? '智能路由' : model.id}</option>)}
          </select>}
          {runId && <StopRunButton stopping={isStopping} onStop={() => void stopRun()} />}
          <button
            onClick={clearChat}
            disabled={restoring}
            title="新对话"
            aria-label="新对话"
            className="h-8 px-3 text-[12px] text-muted-foreground hover:text-foreground border border-border rounded-md hover:bg-accent transition-colors"
          >
            {workbench ? <SquarePen size={16} /> : 'Clear'}
          </button>
        </div>
      </header>

      <MessageList
        messages={messages}
        isLoading={isLoading}
        empty={workbench ? <WorkbenchWelcome projectName={projects.find((project) => project.id === projectId)?.name} onSelect={(prompt) => { setInput(prompt); inputRef.current?.focus() }} /> : (
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
        )}
      />

      <div className={workbench ? 'workbench-composer-area' : 'p-4 border-t border-border'}>
        <div className={workbench ? 'workbench-composer-wrap' : 'max-w-3xl mx-auto'}>
          {runId && (
            <RunMessageQueue
              snapshot={queue.snapshot}
              busy={queue.busy}
              error={queue.error}
              onPin={(id) => void queue.pin(id)}
              onMove={(id, direction) => void queue.move(id, direction)}
              onCancel={(id) => void queue.cancel(id)}
            />
          )}
          {isLoading && runId && (
            <div className="mb-2">
              <QueueModeControl value={queue.mode} onChange={queue.setMode} disabled={queue.busy} />
            </div>
          )}
          <div className={workbench ? 'workbench-context-bar' : 'mb-2 flex flex-wrap items-center gap-2'}>
            {workbench && <span className="workbench-context-group"><Folder size={15} /><span>项目</span></span>}
            <select aria-label="当前项目" value={projectId} onChange={(event) => void changeProject(event.target.value)} disabled={projectBusy || isLoading} className={workbench ? 'workbench-context-select workbench-project-select' : 'h-8 max-w-52 rounded-md border border-border bg-card px-2 text-[11px]'}>
              <option value="">{workbench ? '未关联项目' : '不使用项目'}</option>
              {projects.map((project) => <option key={project.id} value={project.id}>{project.name}</option>)}
            </select>
            {workbench && <button type="button" title={creatingProject ? '取消新建项目' : '新建项目'} aria-label={creatingProject ? '取消新建项目' : '新建项目'} onClick={() => setCreatingProject(!creatingProject)} className="workbench-icon-button">{creatingProject ? <X size={15} /> : <Plus size={15} />}</button>}
            {(!workbench || creatingProject) && <>
              <input aria-label="新项目名称" value={projectName} onChange={(event) => setProjectName(event.target.value)} placeholder="新项目" className="h-8 w-28 rounded-md border border-border bg-card px-2 text-[11px]" />
              <button type="button" disabled={!projectName.trim() || projectBusy} onClick={() => void createProject()} className="h-8 rounded-md border border-border px-2 text-[11px] disabled:opacity-50">{projectBusy ? '创建中...' : '创建'}</button>
            </>}
            {workbench && <span className="workbench-context-divider" />}
            {workbench && <span className="workbench-context-group"><MessagesSquare size={15} /><span>会话</span></span>}
            {workbench && <RecentSessionSelect accountId={currentAccount?.id} mode="chat" currentSessionId={sessionId} variant="context" />}
            {workbench && <span className="workbench-context-divider" />}
            {workbench && <span className="workbench-context-group"><Bot size={15} /><span>模型</span></span>}
            {workbench && <select aria-label="当前模型" title="选择本会话使用的模型" value={selectedModel} onChange={(event) => setSelectedModel(event.target.value)} disabled={isLoading} className="workbench-context-select workbench-model-select">
              {selectedModel && !models.some((model) => model.id === selectedModel) && <option value={selectedModel}>{selectedModel}</option>}
              {models.length === 0 && !selectedModel ? <option value="">默认模型</option> : models.map((model) => <option key={model.id} value={model.id}>{model.id === 'auto' ? '智能路由' : model.id}</option>)}
            </select>}
            <DocumentPicker accountId={currentAccount?.id} projectId={projectId} selected={selectedDocuments} onChange={setSelectedDocuments} />
            {workbench && <span className="workbench-context-mode"><span />{selectedModel === 'auto' ? '自动路由' : '已指定模型'}</span>}
          </div>
          {projectError && <p role="alert" className="mb-2 text-[12px] text-destructive">{projectError}</p>}
          {selectedDocuments.length > 0 && <div className="mb-2"><DocumentChips documents={selectedDocuments} onRemove={(id) => setSelectedDocuments((current) => current.filter((document) => document.id !== id))} /></div>}
          {isLoading && runId && selectedDocuments.length > 0 && <p className="mb-2 text-[11px] text-amber-600">运行中不能发送附件，请停止或等待当前运行结束。</p>}
          <div className={workbench ? 'workbench-composer' : 'flex gap-2'}>
            <textarea
              ref={inputRef}
              aria-label="消息"
              value={input}
              onChange={(e) => setInput(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === 'Enter' && e.shiftKey && !e.nativeEvent.isComposing) {
                  e.preventDefault()
                  void sendMessage()
                }
              }}
              placeholder={workbench ? '随心输入，开始一起构建...' : 'Type a message...（Enter 换行，Shift+Enter 发送）'}
              rows={workbench ? 2 : 1}
              className="flex-1 px-4 py-2.5 bg-card border border-border rounded-xl text-[13px] text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-ring resize-none"
            />
            <VoiceInputButton
              listening={voice.listening}
              supported={voice.supported}
              disabled={false}
              title={
                voice.engine === 'server'
                  ? '语音输入（服务端 ASR）'
                  : '语音输入（浏览器 Web Speech）'
              }
              onClick={() => void voice.toggle()}
            />
            <button
              onClick={() => void sendMessage()}
              title={isLoading && runId ? '加入消息队列' : '发送消息'}
              aria-label={isLoading && runId ? '加入消息队列' : '发送消息'}
              disabled={restoring || !currentAccount?.id || !input.trim() || queue.busy || Boolean(isLoading && runId && selectedDocuments.length)}
              className="h-10 px-4 bg-primary text-primary-foreground rounded-xl text-[13px] font-medium hover:bg-primary/90 disabled:opacity-50 transition-colors"
            >
              {workbench ? <ArrowUp size={20} /> : isLoading && runId
                ? queue.mode === 'pin'
                  ? '置顶'
                  : queue.mode === 'steer'
                    ? '立即加入'
                    : '排队'
                : 'Send'}
            </button>
          </div>
        </div>
      </div>
    </div>
  )
}
