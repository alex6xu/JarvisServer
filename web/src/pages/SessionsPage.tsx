import { useState, useEffect, useRef, ChangeEvent } from 'react'
import { useNavigate } from 'react-router-dom'
import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import { Code2, MessageSquare } from 'lucide-react'
import { apiFetch, useAccount } from '../context/AccountContext'
import ToolStepCard from '../components/ToolStepCard'
import StopRunButton from '../components/StopRunButton'
import {
  chatSessionKey,
  coderSessionKey,
  coderWorkspaceKey,
  writeLocal,
  type ToolStep,
} from '../lib/sessionPersist'

interface Session {
  id: string
  type: 'chat' | 'code'
  title: string
  platform: string
  message_count: number
  created_at: string
  updated_at: string
  preview?: string
  workspace_id?: string
  active_run_status?: string
  parent_session?: string
  worktree_branch?: string
  base_commit?: string
}

type SessionType = 'chat' | 'code'

interface Message {
  id: string
  role: string
  content: string
  model?: string
  provider?: string
  created_at: string
  tool_steps?: ToolStep[]
}

interface PreviewMsg {
  role: string
  content: string
}

export default function SessionsPage() {
  const { currentAccount } = useAccount()
  const navigate = useNavigate()
  const [sessions, setSessions] = useState<Session[]>([])
  const [sessionType, setSessionType] = useState<SessionType>('chat')
  const [selectedSession, setSelectedSession] = useState<string | null>(null)
  const [selectedMeta, setSelectedMeta] = useState<Session | null>(null)
  const [messages, setMessages] = useState<Message[]>([])
  const [detailWorkspaceId, setDetailWorkspaceId] = useState('')
  const [loading, setLoading] = useState(false)
  const [importOpen, setImportOpen] = useState(false)
  const [importText, setImportText] = useState('')
  const [importTitle, setImportTitle] = useState('')
  const [preview, setPreview] = useState<{ title: string; messages: PreviewMsg[] } | null>(null)
  const [importError, setImportError] = useState('')
  const [importing, setImporting] = useState(false)
  const [branchBusy, setBranchBusy] = useState(false)
  const [branchError, setBranchError] = useState('')
  const [branchDiff, setBranchDiff] = useState('')
  const [detailRunId, setDetailRunId] = useState('')
  const [stoppingRun, setStoppingRun] = useState(false)
  const fileRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    if (currentAccount) {
      setSelectedSession(null)
      setSelectedMeta(null)
      setMessages([])
      void fetchSessions()
    }
  }, [currentAccount?.id, sessionType])

  const fetchSessions = async () => {
    try {
      const response = await apiFetch(
        `/v1/agent/sessions?type=${encodeURIComponent(sessionType)}`,
        {},
        currentAccount?.id,
      )
      if (response.ok) {
        const data = await response.json()
        setSessions(data.sessions || [])
      }
    } catch (error) {
      console.error('Failed to fetch sessions:', error)
    }
  }

  const fetchSessionDetail = async (session: Session) => {
    setLoading(true)
    setBranchError('')
    setBranchDiff('')
    setSelectedSession(session.id)
    setSelectedMeta(session)
    setDetailWorkspaceId(session.workspace_id || '')
    setDetailRunId('')
    try {
      const response = await apiFetch(`/v1/agent/sessions/${session.id}`, {}, currentAccount?.id)
      if (response.ok) {
        const data = await response.json()
        setMessages(data.messages || [])
        if (data.workspace_id) setDetailWorkspaceId(data.workspace_id)
        setDetailRunId(
          data.active_run && (data.active_run.status === 'running' || data.active_run.status === 'queued')
            ? data.active_run.id || ''
            : '',
        )
        if (data.session) {
          setSelectedMeta({
            ...session,
            ...data.session,
            active_run_status: data.session.active_run_status || '',
          })
        }
      }
    } catch (error) {
      console.error('Failed to fetch session detail:', error)
    } finally {
      setLoading(false)
    }
  }

  const forkSession = async (entryId?: string) => {
    if (!selectedSession || !currentAccount?.id) return
    setBranchBusy(true)
    setBranchError('')
    try {
      const response = await apiFetch(
        `/v1/agent/sessions/${encodeURIComponent(selectedSession)}/fork`,
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify(entryId ? { entry_id: entryId, position: 'at' } : {}),
        },
        currentAccount.id,
      )
      const data = await response.json().catch(() => ({}))
      if (!response.ok) throw new Error(data.error || `HTTP ${response.status}`)
      const session = data.session as Session | undefined
      const workspaceId = data.workspace_id || session?.workspace_id || detailWorkspaceId
      if (!session?.id || !workspaceId) throw new Error('分支会话缺少工作区信息')
      writeLocal(coderWorkspaceKey(currentAccount.id), workspaceId)
      writeLocal(coderSessionKey(currentAccount.id, workspaceId), session.id)
      navigate(
        `/code?workspace=${encodeURIComponent(workspaceId)}&session=${encodeURIComponent(session.id)}`,
      )
    } catch (error) {
      setBranchError(error instanceof Error ? error.message : '创建分支失败')
    } finally {
      setBranchBusy(false)
    }
  }

  const loadBranchDiff = async () => {
    if (!selectedSession) return
    setBranchBusy(true)
    setBranchError('')
    try {
      const response = await apiFetch(
        `/v1/agent/sessions/${encodeURIComponent(selectedSession)}/diff`,
        {},
        currentAccount?.id,
      )
      const data = await response.json().catch(() => ({}))
      if (!response.ok) throw new Error(data.error || `HTTP ${response.status}`)
      setBranchDiff(data.diff || 'No changes')
    } catch (error) {
      setBranchError(error instanceof Error ? error.message : '读取变更失败')
    } finally {
      setBranchBusy(false)
    }
  }

  const mergeBranch = async () => {
    if (!selectedSession || !confirm('将此分支的代码变更合并到主工作区？')) return
    setBranchBusy(true)
    setBranchError('')
    try {
      const response = await apiFetch(
        `/v1/agent/sessions/${encodeURIComponent(selectedSession)}/merge`,
        { method: 'POST' },
        currentAccount?.id,
      )
      const data = await response.json().catch(() => ({}))
      if (!response.ok) throw new Error(data.error || `HTTP ${response.status}`)
      setBranchDiff(data.message || '已合并到主工作区')
      await fetchSessions()
    } catch (error) {
      setBranchError(error instanceof Error ? error.message : '合并失败')
    } finally {
      setBranchBusy(false)
    }
  }

  const continueSession = (session: Session) => {
    if (!currentAccount?.id) return
    const accountId = currentAccount.id
    if (session.type === 'code') {
      const workspaceId = detailWorkspaceId || session.workspace_id || ''
      if (workspaceId) {
        writeLocal(coderWorkspaceKey(accountId), workspaceId)
        writeLocal(coderSessionKey(accountId, workspaceId), session.id)
        navigate(
          `/code?workspace=${encodeURIComponent(workspaceId)}&session=${encodeURIComponent(session.id)}`,
        )
        return
      }
    }
    writeLocal(chatSessionKey(accountId), session.id)
    navigate(`/?session=${encodeURIComponent(session.id)}`)
  }

  const stopDetailRun = async () => {
    if (!detailRunId || stoppingRun) return
    setStoppingRun(true)
    try {
      const response = await apiFetch(
        `/v1/agent/runs/${encodeURIComponent(detailRunId)}/cancel`,
        { method: 'POST' },
        currentAccount?.id,
      )
      const data = await response.json().catch(() => ({}))
      if (!response.ok) throw new Error(data.error || `HTTP ${response.status}`)
      setDetailRunId('')
      setSelectedMeta((prev) => (prev ? { ...prev, active_run_status: 'cancelled' } : prev))
      setSessions((prev) =>
        prev.map((session) =>
          session.id === selectedSession ? { ...session, active_run_status: 'cancelled' } : session,
        ),
      )
    } catch (error) {
      setBranchError(error instanceof Error ? `停止失败: ${error.message}` : '停止失败')
    } finally {
      setStoppingRun(false)
    }
  }

  const onPickFile = async (e: ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    if (!file) return
    const text = await file.text()
    setImportText(text)
    if (!importTitle) {
      setImportTitle(file.name.replace(/\.(md|markdown)$/i, ''))
    }
    setPreview(null)
    setImportError('')
    if (fileRef.current) fileRef.current.value = ''
  }

  const previewImport = async () => {
    setImportError('')
    setPreview(null)
    if (!importText.trim()) {
      setImportError('请粘贴 Markdown 或选择 .md 文件')
      return
    }
    try {
      const res = await apiFetch(
        '/v1/agent/sessions/import/preview',
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ content: importText, title: importTitle || undefined }),
        },
        currentAccount?.id,
      )
      const data = await res.json()
      if (!res.ok) {
        setImportError(data.error || '解析失败')
        return
      }
      setPreview({ title: data.title, messages: data.messages || [] })
    } catch {
      setImportError('预览失败')
    }
  }

  const confirmImport = async () => {
    setImporting(true)
    setImportError('')
    try {
      const res = await apiFetch(
        '/v1/agent/sessions/import',
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ content: importText, title: importTitle || undefined }),
        },
        currentAccount?.id,
      )
      const data = await res.json()
      if (!res.ok) {
        setImportError(data.error || '导入失败')
        return
      }
      setImportOpen(false)
      setImportText('')
      setImportTitle('')
      setPreview(null)
      if (data.session?.id) {
        const imported: Session = {
          id: data.session.id,
          type: 'chat',
          title: data.session.title || 'Imported',
          platform: data.session.platform || 'import',
          message_count: data.session.message_count || 0,
          created_at: data.session.created_at || new Date().toISOString(),
          updated_at: data.session.updated_at || new Date().toISOString(),
        }
        if (sessionType !== 'chat') {
          setSessionType('chat')
        } else {
          await fetchSessions()
          await fetchSessionDetail(imported)
        }
      } else {
        await fetchSessions()
      }
    } catch {
      setImportError('导入失败')
    } finally {
      setImporting(false)
    }
  }

  const platformIcons: Record<string, string> = {
    chat: '💬',
    web: '🌐',
    coder: '🛠️',
    telegram: '📱',
    terminal: '💻',
    wechat: '💬',
    import: '📥',
  }

  const formatDate = (dateStr: string) => {
    const date = new Date(dateStr)
    return (
      date.toLocaleDateString() +
      ' ' +
      date.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
    )
  }

  return (
    <div className="flex h-full">
      <div className={`border-r border-border ${selectedSession ? 'w-80' : 'flex-1'} overflow-auto`}>
        <div className="p-4 border-b border-border">
          <div className="flex items-start justify-between gap-2">
            <div>
              <h2 className="text-base font-semibold text-foreground">Sessions</h2>
              <p className="text-[13px] text-muted-foreground mt-0.5">
                {sessions.length} sessions · {currentAccount?.username || 'account'}
              </p>
            </div>
            <button
              onClick={() => {
                setImportOpen((v) => !v)
                setImportError('')
                setPreview(null)
              }}
              className="h-8 px-3 text-[12px] border border-border rounded-md hover:bg-accent flex-shrink-0"
            >
              {importOpen ? '关闭导入' : '导入 MD'}
            </button>
          </div>
          <div className="mt-3 inline-flex h-8 p-0.5 bg-muted border border-border rounded-md" role="tablist">
            {([
              { value: 'chat' as const, label: 'Chat', icon: MessageSquare },
              { value: 'code' as const, label: 'Code', icon: Code2 },
            ]).map((tab) => {
              const Icon = tab.icon
              const selected = sessionType === tab.value
              return (
                <button
                  key={tab.value}
                  type="button"
                  role="tab"
                  aria-selected={selected}
                  onClick={() => {
                    setSessionType(tab.value)
                    setSessions([])
                    setSelectedSession(null)
                    setSelectedMeta(null)
                    setMessages([])
                    setDetailRunId('')
                  }}
                  className={`h-6 px-3 inline-flex items-center gap-1.5 rounded text-[12px] transition-colors ${
                    selected ? 'bg-background text-foreground shadow-sm' : 'text-muted-foreground hover:text-foreground'
                  }`}
                >
                  <Icon size={13} aria-hidden="true" />
                  {tab.label}
                </button>
              )
            })}
          </div>
        </div>

        {importOpen && (
          <div className="p-4 border-b border-border bg-card/40 space-y-3">
            <p className="text-[11px] text-muted-foreground leading-relaxed">
              支持 <code className="text-[10px]">## User</code> / <code className="text-[10px]">## Assistant</code>
              等 Markdown 对话格式。
            </p>
            <input
              value={importTitle}
              onChange={(e) => setImportTitle(e.target.value)}
              placeholder="会话标题（可选）"
              className="w-full h-8 px-2 bg-card border border-border rounded-md text-[12px]"
            />
            <textarea
              value={importText}
              onChange={(e) => {
                setImportText(e.target.value)
                setPreview(null)
              }}
              placeholder={'# 标题\n\n## User\n你好\n\n## Assistant\n你好！'}
              rows={8}
              className="w-full px-2 py-2 bg-card border border-border rounded-md text-[12px] font-mono resize-y"
            />
            <div className="flex flex-wrap gap-2">
              <input
                ref={fileRef}
                type="file"
                accept=".md,.markdown,text/markdown"
                className="hidden"
                onChange={onPickFile}
              />
              <button
                onClick={() => fileRef.current?.click()}
                className="h-8 px-3 text-[12px] border border-border rounded-md hover:bg-accent"
              >
                选择 .md 文件
              </button>
              <button
                onClick={() => void previewImport()}
                className="h-8 px-3 text-[12px] border border-border rounded-md hover:bg-accent"
              >
                预览解析
              </button>
              <button
                onClick={() => void confirmImport()}
                disabled={importing || !importText.trim()}
                className="h-8 px-3 text-[12px] bg-primary text-primary-foreground rounded-md disabled:opacity-50"
              >
                {importing ? '导入中…' : '确认导入'}
              </button>
            </div>
            {importError && <p className="text-[12px] text-red-500">{importError}</p>}
            {preview && (
              <div className="border border-border rounded-md max-h-48 overflow-auto divide-y divide-border">
                <div className="px-2 py-1.5 text-[11px] text-muted-foreground">
                  预览：{preview.title} · {preview.messages.length} 条消息
                </div>
                {preview.messages.slice(0, 8).map((m, i) => (
                  <div key={i} className="px-2 py-1.5 text-[11px]">
                    <span className="font-medium text-foreground">{m.role}</span>
                    <span className="text-muted-foreground ml-2">
                      {m.content.length > 80 ? m.content.slice(0, 80) + '…' : m.content}
                    </span>
                  </div>
                ))}
              </div>
            )}
          </div>
        )}

        <div className="divide-y divide-border">
          {sessions.length === 0 ? (
            <div className="p-8 text-center">
              <p className="text-[13px] text-muted-foreground">暂无会话，可导入 Markdown 对话记录</p>
            </div>
          ) : (
            sessions.map((session) => (
              <div
                key={session.id}
                onClick={() => void fetchSessionDetail(session)}
                className={`p-4 cursor-pointer hover:bg-accent/50 transition-colors ${
                  selectedSession === session.id ? 'bg-accent' : ''
                }`}
              >
                <div className="flex items-start justify-between mb-1">
                  <h3 className="text-[13px] font-medium text-foreground truncate flex-1">
                    {session.title || 'Untitled Session'}
                  </h3>
                  <span className="text-sm ml-2">{platformIcons[session.platform] || '📋'}</span>
                </div>
                <div className="flex items-center gap-2 text-[12px] text-muted-foreground">
                  <span>{session.message_count} messages</span>
                  <span>·</span>
                  <span>{session.type === 'code' ? 'Code' : 'Chat'}</span>
                  {session.active_run_status && (
                    <>
                      <span>·</span>
                      <span className="text-amber-600">{session.active_run_status}</span>
                    </>
                  )}
                </div>
                {session.preview && (
                  <p className="text-[11px] text-muted-foreground/80 mt-1 line-clamp-2">{session.preview}</p>
                )}
                <p className="text-[11px] text-muted-foreground/60 mt-1">{formatDate(session.updated_at)}</p>
              </div>
            ))
          )}
        </div>
      </div>

      {selectedSession && (
        <div className="flex-1 flex flex-col">
          <div className="p-4 border-b border-border flex items-center justify-between gap-2">
            <h3 className="text-sm font-semibold text-foreground">Session Detail</h3>
            <div className="flex items-center gap-2">
              {selectedMeta && (
                <button
                  onClick={() => continueSession(selectedMeta)}
                  className="h-8 px-3 text-[12px] bg-primary text-primary-foreground rounded-md hover:bg-primary/90"
                >
                  {selectedMeta.type === 'code' ? '在 Code 继续' : '在 Chat 继续'}
                </button>
              )}
              {detailRunId && (
                <StopRunButton stopping={stoppingRun} onStop={() => void stopDetailRun()} />
              )}
              {selectedMeta?.type === 'code' && (
                <button
                  onClick={() => void forkSession()}
                  disabled={branchBusy || selectedMeta.active_run_status === 'running'}
                  className="h-8 px-3 text-[12px] border border-border rounded-md hover:bg-accent disabled:opacity-50"
                >
                  创建分支
                </button>
              )}
              {selectedMeta?.worktree_branch && (
                <>
                  <button
                    onClick={() => void loadBranchDiff()}
                    disabled={branchBusy}
                    className="h-8 px-3 text-[12px] border border-border rounded-md hover:bg-accent disabled:opacity-50"
                  >
                    查看变更
                  </button>
                  <button
                    onClick={() => void mergeBranch()}
                    disabled={branchBusy || selectedMeta.active_run_status === 'running'}
                    className="h-8 px-3 text-[12px] border border-border rounded-md hover:bg-accent disabled:opacity-50"
                  >
                    合并到工作区
                  </button>
                </>
              )}
              <button
                onClick={() => {
                  setSelectedSession(null)
                  setSelectedMeta(null)
                  setMessages([])
                  setBranchError('')
                  setBranchDiff('')
                  setDetailRunId('')
                }}
                className="text-[12px] text-muted-foreground hover:text-foreground"
              >
                Close
              </button>
            </div>
          </div>

          {(branchError || branchDiff) && (
            <div className="border-b border-border px-4 py-3 bg-card/40">
              {branchError && <p className="text-[12px] text-red-500">{branchError}</p>}
              {branchDiff && (
                <pre className="text-[11px] text-muted-foreground whitespace-pre-wrap overflow-auto max-h-40">
                  {branchDiff}
                </pre>
              )}
            </div>
          )}

          <div className="flex-1 overflow-auto p-4 space-y-3">
            {loading ? (
              <div className="flex items-center justify-center h-full">
                <div className="w-5 h-5 border-2 border-primary border-t-transparent rounded-full animate-spin" />
              </div>
            ) : messages.length === 0 ? (
              <div className="flex items-center justify-center h-full">
                <p className="text-[13px] text-muted-foreground">No messages in this session</p>
              </div>
            ) : (
              messages.map((msg) => (
                <div key={msg.id} className={`group flex ${msg.role === 'user' ? 'justify-end' : 'justify-start'}`}>
                  <div
                    className={`max-w-[80%] rounded-xl px-4 py-2.5 ${
                      msg.role === 'user'
                        ? 'bg-primary text-primary-foreground'
                        : 'bg-card border border-border text-foreground'
                    }`}
                  >
                    <div className="text-[11px] opacity-60 mb-1 flex items-center gap-2">
                      <span className="capitalize">{msg.role}</span>
                      {msg.model && <span>· {msg.model}</span>}
                    </div>
                    {msg.role === 'assistant' ? (
                      <div className="prose prose-sm dark:prose-invert max-w-none text-[13px]">
                        <ReactMarkdown remarkPlugins={[remarkGfm]}>{msg.content}</ReactMarkdown>
                      </div>
                    ) : (
                      <p className="text-[13px] whitespace-pre-wrap">{msg.content}</p>
                    )}
                    {msg.tool_steps && msg.tool_steps.length > 0 && (
                      <ToolStepCard messageId={msg.id} steps={msg.tool_steps} defaultOpen={false} />
                    )}
                    {selectedMeta?.type === 'code' && msg.role === 'assistant' && (
                      <button
                        onClick={() => void forkSession(msg.id)}
                        disabled={branchBusy || selectedMeta.active_run_status === 'running'}
                        className="mt-2 text-[11px] opacity-60 hover:opacity-100 disabled:opacity-30"
                      >
                        从此处分支
                      </button>
                    )}
                  </div>
                </div>
              ))
            )}
          </div>
        </div>
      )}
    </div>
  )
}
