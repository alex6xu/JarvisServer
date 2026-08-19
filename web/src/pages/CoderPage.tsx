import { useState, useEffect, useRef, ChangeEvent, useCallback } from 'react'
import { apiFetch, useAccount } from '../context/AccountContext'
import VoiceInputButton from '../components/VoiceInputButton'
import MessageBubble from '../components/MessageBubble'
import { useVoiceInput } from '../hooks/useVoiceInput'
import { useRunEventStream } from '../hooks/useRunEventStream'
import { useSessionRestore } from '../hooks/useSessionRestore'
import {
  coderSessionKey,
  coderWorkspaceKey,
  readLocal,
  readSessionQueryParam,
  writeLocal,
  type UiMessage,
} from '../lib/sessionPersist'

type Message = UiMessage

interface ModelOption {
  id: string
}

interface WorkspaceInfo {
  id: string
  name: string
  file_count: number
  size_bytes: number
  created_at: string
  updated_at: string
  source?: string
  github_full_name?: string
  github_default_branch?: string
}

interface GitHubRepo {
  id: number
  full_name: string
  name: string
  owner: string
  private: boolean
  description: string
  default_branch: string
  html_url: string
  updated_at: string
}

const quickTasks = [
  {
    id: 'implement',
    title: '实现功能',
    prompt: '请在当前项目中实现以下功能，直接修改/新增文件，并说明改动点：\n\n',
  },
  {
    id: 'review',
    title: '代码审查',
    prompt: '请审查当前项目代码，指出 bug、安全风险、性能问题，必要时直接提交修复文件。重点关注：\n\n',
  },
  {
    id: 'refactor',
    title: '重构优化',
    prompt: '请重构当前项目相关模块，提升可读性与可维护性，保持行为不变，并写入修改后的文件。范围：\n\n',
  },
  {
    id: 'debug',
    title: '排查 Bug',
    prompt: '以下报错/现象有问题，请在项目中定位原因并直接修复文件：\n\n',
  },
  {
    id: 'explain',
    title: '解释结构',
    prompt: '请先浏览项目目录，解释整体结构、关键模块与数据流。',
  },
  {
    id: 'tests',
    title: '补测试',
    prompt: '请为当前项目补充单元测试，覆盖主流程与边界情况，并写入测试文件。目标模块：\n\n',
  },
]

function formatBytes(n: number) {
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  return `${(n / 1024 / 1024).toFixed(1)} MB`
}

export default function CoderPage() {
  const { currentAccount } = useAccount()
  const [messages, setMessages] = useState<Message[]>([])
  const [input, setInput] = useState('')
  const [isLoading, setIsLoading] = useState(false)
  const [sessionId, setSessionId] = useState('')
  const [models, setModels] = useState<ModelOption[]>([])
  const [selectedModel, setSelectedModel] = useState('')
  const [workspaces, setWorkspaces] = useState<WorkspaceInfo[]>([])
  const [workspaceId, setWorkspaceId] = useState('')
  const [uploading, setUploading] = useState(false)
  const [uploadError, setUploadError] = useState('')
  const [ghConfigured, setGhConfigured] = useState(false)
  const [ghConnected, setGhConnected] = useState(false)
  const [ghLogin, setGhLogin] = useState('')
  const [ghRepos, setGhRepos] = useState<GitHubRepo[]>([])
  const [ghPanelOpen, setGhPanelOpen] = useState(false)
  const [ghLoading, setGhLoading] = useState(false)
  const [ghImporting, setGhImporting] = useState('')
  const [ghError, setGhError] = useState('')
  const [ghSyncing, setGhSyncing] = useState<'pull' | 'push' | ''>('')
  const messagesEndRef = useRef<HTMLDivElement>(null)
  const textareaRef = useRef<HTMLTextAreaElement>(null)
  const dirInputRef = useRef<HTMLInputElement>(null)

  const activeWorkspace = workspaces.find((w) => w.id === workspaceId) || null

  const [runId, setRunId] = useState('')
  const { consumeRunEvents, abortRunStream } = useRunEventStream()
  const { restoreSession, persistSessionId, clearPersistedSession } = useSessionRestore()

  const sessionStorageKey =
    currentAccount?.id && workspaceId ? coderSessionKey(currentAccount.id, workspaceId) : ''

  useEffect(() => {
    void fetchModels()
    void fetchWorkspaces()
    void fetchGitHubStatus()

    const params = new URLSearchParams(window.location.search)
    const gh = params.get('github')
    if (gh === 'connected') {
      setGhPanelOpen(true)
      setMessages((prev) => [
        ...prev,
        {
          id: Date.now().toString(),
          role: 'system',
          content: `GitHub 已授权${params.get('login') ? `（@${params.get('login')}）` : ''}。可从仓库列表导入项目到云端工作区。`,
          timestamp: new Date(),
        },
      ])
      window.history.replaceState({}, '', window.location.pathname)
    } else if (gh === 'error') {
      setUploadError(params.get('message') || 'GitHub 授权失败')
      window.history.replaceState({}, '', window.location.pathname)
    }
  }, [currentAccount?.id])

  // Persist selected workspace so returning to /code restores the same project.
  useEffect(() => {
    if (!currentAccount?.id || !workspaceId) return
    writeLocal(coderWorkspaceKey(currentAccount.id), workspaceId)
  }, [currentAccount?.id, workspaceId])

  // Restore session + active run after workspace is known (?session= preferred).
  useEffect(() => {
    if (!currentAccount?.id || !workspaceId || !sessionStorageKey) {
      setMessages([])
      setSessionId('')
      setRunId('')
      setIsLoading(false)
      return
    }

    let cancelled = false
    ;(async () => {
      try {
        const result = await restoreSession({
          accountId: currentAccount.id,
          storageKey: sessionStorageKey,
          consumeActiveRun: async (targetRunId, assistantId, afterSeq, model) => {
            if (cancelled) return
            setRunId(targetRunId)
            setIsLoading(true)
            await consumeRunEvents(targetRunId, assistantId, {
              accountId: currentAccount.id,
              afterSeq,
              fallbackModel: model || selectedModel,
              onSessionId: setSessionId,
              onUserInjected: (content) => {
                setMessages((prev) => {
                  if (prev.some((m) => m.content === content && m.role === 'user')) return prev
                  return [
                    ...prev,
                    {
                      id: `inj-${Date.now()}`,
                      role: 'system',
                      content: `已加入本轮上下文：${content}`,
                      timestamp: new Date(),
                    },
                  ]
                })
              },
              setMessages,
              setIsLoading,
              setRunId,
            })
          },
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
        } else {
          setRunId('')
          setIsLoading(false)
        }
      } catch (e) {
        if (!cancelled) console.error('restore session failed', e)
      }
    })()
    return () => {
      cancelled = true
      abortRunStream()
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [currentAccount?.id, workspaceId, sessionStorageKey])

  useEffect(() => {
    if (sessionId && sessionStorageKey) persistSessionId(sessionStorageKey, sessionId)
  }, [sessionId, sessionStorageKey, persistSessionId])

  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages, isLoading])

  const fetchModels = async () => {
    try {
      const response = await apiFetch('/v1/models', {}, currentAccount?.id)
      if (!response.ok) return
      const data = await response.json()
      const list: ModelOption[] = (data.data || []).map((m: { id: string }) => ({ id: m.id }))
      setModels(list)
      if (list.length > 0) {
        const preferred =
          list.find((m) => m.id.includes('mimo')) ||
          list.find((m) => m.id.includes('glm')) ||
          list.find((m) => m.id.includes('coder') || m.id.includes('code')) ||
          list[0]
        setSelectedModel(preferred.id)
      } else {
        setSelectedModel('')
      }
    } catch (error) {
      console.error('Failed to fetch models:', error)
    }
  }

  const fetchWorkspaces = async () => {
    try {
      const response = await apiFetch('/v1/workspaces', {}, currentAccount?.id)
      if (!response.ok) return
      const data = await response.json()
      const list: WorkspaceInfo[] = data.workspaces || []
      setWorkspaces(list)
      if (list.length > 0) {
        const params = new URLSearchParams(window.location.search)
        const requestedWorkspaceID = params.get('workspace')
        const sessionFromUrl = readSessionQueryParam()
        let workspaceFromSession = ''
        if (sessionFromUrl && currentAccount?.id) {
          try {
            const sessRes = await apiFetch(
              `/v1/agent/sessions/${encodeURIComponent(sessionFromUrl)}`,
              {},
              currentAccount.id,
            )
            if (sessRes.ok) {
              const sessData = await sessRes.json()
              workspaceFromSession =
                sessData.workspace_id ||
                sessData.active_run?.workspace_id ||
                sessData.latest_run?.workspace_id ||
                ''
            } else if (sessRes.status === 404) {
              // Drop stale ?session= so Chat/Coder stop refetching a missing id.
              const url = new URL(window.location.href)
              url.searchParams.delete('session')
              url.searchParams.delete('resume')
              window.history.replaceState({}, '', `${url.pathname}${url.search}${url.hash}`)
            }
          } catch {
            /* ignore */
          }
        }
        const savedWorkspaceID = currentAccount?.id ? readLocal(coderWorkspaceKey(currentAccount.id)) : ''
        setWorkspaceId((prev) => {
          if (requestedWorkspaceID && list.some((workspace) => workspace.id === requestedWorkspaceID)) {
            return requestedWorkspaceID
          }
          if (workspaceFromSession && list.some((workspace) => workspace.id === workspaceFromSession)) {
            return workspaceFromSession
          }
          if (prev && list.some((workspace) => workspace.id === prev)) return prev
          if (savedWorkspaceID && list.some((workspace) => workspace.id === savedWorkspaceID)) {
            return savedWorkspaceID
          }
          return list[0].id
        })
      } else {
        setWorkspaceId('')
      }
    } catch (error) {
      console.error('Failed to fetch workspaces:', error)
    }
  }

  const fetchGitHubStatus = async (): Promise<{ configured: boolean; connected: boolean }> => {
    try {
      const response = await apiFetch('/v1/github/status', {}, currentAccount?.id)
      if (!response.ok) return { configured: false, connected: false }
      const data = await response.json()
      setGhConfigured(!!data.configured)
      setGhConnected(!!data.connected)
      setGhLogin(data.github_login || '')
      return { configured: !!data.configured, connected: !!data.connected }
    } catch (error) {
      console.error('Failed to fetch github status:', error)
      return { configured: false, connected: false }
    }
  }

  const connectGitHub = async () => {
    setGhError('')
    try {
      const response = await apiFetch('/v1/github/authorize', {}, currentAccount?.id)
      const data = await response.json()
      if (!response.ok) {
        setGhError(data.error || '无法开始 GitHub 授权')
        return
      }
      if (data.authorize_url) {
        window.location.href = data.authorize_url
      }
    } catch {
      setGhError('无法开始 GitHub 授权')
    }
  }

  const disconnectGitHub = async () => {
    setGhError('')
    try {
      await apiFetch('/v1/github/disconnect', { method: 'DELETE' }, currentAccount?.id)
      setGhConnected(false)
      setGhLogin('')
      setGhRepos([])
    } catch {
      setGhError('断开失败')
    }
  }

  const loadGitHubRepos = async () => {
    setGhLoading(true)
    setGhError('')
    try {
      const response = await apiFetch('/v1/github/repos?per_page=50', {}, currentAccount?.id)
      const data = await response.json()
      if (!response.ok) {
        setGhError(data.error || '加载仓库失败')
        return
      }
      setGhRepos(data.repos || [])
    } catch {
      setGhError('加载仓库失败')
    } finally {
      setGhLoading(false)
    }
  }

  const openGitHubPanel = async () => {
    setGhPanelOpen(true)
    setGhError('')
    const status = await fetchGitHubStatus()
    if (status.connected) {
      await loadGitHubRepos()
    }
  }

  const importGitHubRepo = async (repo: GitHubRepo) => {
    setGhImporting(repo.full_name)
    setGhError('')
    try {
      const response = await apiFetch(
        '/v1/github/import',
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            owner: repo.owner,
            repo: repo.name,
            branch: repo.default_branch || undefined,
            name: repo.name,
          }),
        },
        currentAccount?.id,
      )
      const data = await response.json()
      if (!response.ok) {
        setGhError(data.error || '导入失败')
        return
      }
      await fetchWorkspaces()
      if (data.workspace?.id) {
        setWorkspaceId(data.workspace.id)
        setGhPanelOpen(false)
        setMessages((prev) => [
          ...prev,
          {
            id: Date.now().toString(),
            role: 'system',
            content: `已从 GitHub 克隆「${data.workspace.github_full_name || data.workspace.name}」到云端工作区（${data.workspace.file_count} 个文件，${formatBytes(data.workspace.size_bytes)}）。可用 Pull 同步远端，修改后可用 Push 推回。`,
            timestamp: new Date(),
          },
        ])
      }
    } catch {
      setGhError('导入失败，请重试')
    } finally {
      setGhImporting('')
    }
  }

  const pullGitHubWorkspace = async () => {
    if (!workspaceId || !activeWorkspace || activeWorkspace.source !== 'github') return
    setGhSyncing('pull')
    setUploadError('')
    try {
      const response = await apiFetch(
        `/v1/github/workspaces/${workspaceId}/pull`,
        { method: 'POST' },
        currentAccount?.id,
      )
      const data = await response.json().catch(() => ({}))
      if (!response.ok) {
        setUploadError(data.error || 'Pull 失败')
        return
      }
      await fetchWorkspaces()
      setMessages((prev) => [
        ...prev,
        {
          id: Date.now().toString(),
          role: 'system',
          content: data.result?.message
            ? `${data.result.message}${data.result.head ? ` @ ${data.result.head}` : ''}`
            : '已从 GitHub Pull 最新代码',
          timestamp: new Date(),
        },
      ])
    } catch {
      setUploadError('Pull 失败，请重试')
    } finally {
      setGhSyncing('')
    }
  }

  const pushGitHubWorkspace = async () => {
    if (!workspaceId || !activeWorkspace || activeWorkspace.source !== 'github') return
    const message = window.prompt('提交说明（commit message）', 'Update from CodeGateway')
    if (message === null) return
    setGhSyncing('push')
    setUploadError('')
    try {
      const response = await apiFetch(
        `/v1/github/workspaces/${workspaceId}/push`,
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            message: message.trim() || 'Update from CodeGateway',
            branch: activeWorkspace.github_default_branch || undefined,
          }),
        },
        currentAccount?.id,
      )
      const data = await response.json().catch(() => ({}))
      if (!response.ok) {
        setUploadError(data.error || 'Push 失败')
        return
      }
      await fetchWorkspaces()
      setMessages((prev) => [
        ...prev,
        {
          id: Date.now().toString(),
          role: 'system',
          content: data.result?.message
            ? `${data.result.message}${data.result.head ? ` @ ${data.result.head}` : ''}`
            : '已 Push 到 GitHub',
          timestamp: new Date(),
        },
      ])
    } catch {
      setUploadError('Push 失败，请重试')
    } finally {
      setGhSyncing('')
    }
  }

  const onSelectDirectory = async (e: ChangeEvent<HTMLInputElement>) => {
    const files = e.target.files
    if (!files || files.length === 0) return

    setUploading(true)
    setUploadError('')
    try {
      const { buildWorkspaceZipFromDirectory, formatUploadSkipSummary } = await import('../lib/workspaceUpload')
      const built = await buildWorkspaceZipFromDirectory(files)

      const form = new FormData()
      form.append('name', built.name)
      form.append('archive', built.blob, `${built.name}.zip`)

      const response = await apiFetch('/v1/workspaces/upload', {
        method: 'POST',
        body: form,
      }, currentAccount?.id)
      const data = await response.json().catch(() => ({}))
      if (!response.ok) {
        setUploadError(data.error || '上传失败')
        return
      }
      await fetchWorkspaces()
      if (data.workspace?.id) {
        setWorkspaceId(data.workspace.id)
        const skipHint = formatUploadSkipSummary(built)
        setMessages((prev) => [
          ...prev,
          {
            id: Date.now().toString(),
            role: 'system',
            content: `已上传云端工作区「${data.workspace.name}」（${data.workspace.file_count} 个文件，${formatBytes(data.workspace.size_bytes)}；本地打包 ${built.included} 个文件${skipHint}）。现在可以直接描述要改的功能，Agent 会在云端目录里读改文件。`,
            timestamp: new Date(),
          },
        ])
      }
    } catch (error) {
      console.error(error)
      setUploadError(error instanceof Error ? error.message : '上传失败，请重试')
    } finally {
      setUploading(false)
      if (dirInputRef.current) dirInputRef.current.value = ''
    }
  }

  const applyQuickTask = (prompt: string) => {
    setInput(prompt)
    requestAnimationFrame(() => {
      textareaRef.current?.focus()
      const len = prompt.length
      textareaRef.current?.setSelectionRange(len, len)
    })
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
    if (!input.trim()) return
    if (voice.listening) {
      await voice.stop()
    }

    const content = input
    const userMessage: Message = {
      id: Date.now().toString(),
      role: 'user',
      content,
      timestamp: new Date(),
    }

    setMessages((prev) => [...prev, userMessage])
    setInput('')

    // While a run is active, enqueue into inbox (no new assistant bubble / stream).
    if (isLoading && runId) {
      try {
        const response = await apiFetch(
          '/v1/agent/chat',
          {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
              message: content,
              session_id: sessionId,
              mode: 'coder',
              model: selectedModel || undefined,
              workspace_id: workspaceId || undefined,
              stream: false,
            }),
          },
          currentAccount?.id,
        )
        const data = await response.json().catch(() => ({}))
        if (!response.ok) throw new Error(data.error || `HTTP ${response.status}`)
        if (data.session_id) setSessionId(data.session_id)
        setMessages((prev) => [
          ...prev,
          {
            id: `queued-${Date.now()}`,
            role: 'system',
            content: '已排队：将在当前工具轮结束后注入本轮上下文',
            timestamp: new Date(),
          },
        ])
      } catch (err) {
        setMessages((prev) => [
          ...prev,
          {
            id: Date.now().toString(),
            role: 'system',
            content: err instanceof Error ? `排队失败: ${err.message}` : '排队失败',
            timestamp: new Date(),
          },
        ])
      }
      return
    }

    setIsLoading(true)
    const provisionalAssistantId = `pending-${Date.now()}`
    setMessages((prev) => [
      ...prev,
      {
        id: provisionalAssistantId,
        role: 'assistant',
        content: '',
        timestamp: new Date(),
        model: selectedModel || undefined,
        toolSteps: [],
        segments: [],
      },
    ])

    try {
      const response = await apiFetch(
        '/v1/agent/chat',
        {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({
            message: content,
            session_id: sessionId,
            mode: 'coder',
            model: selectedModel || undefined,
            workspace_id: workspaceId || undefined,
            stream: false,
          }),
        },
        currentAccount?.id,
      )
      const data = await response.json().catch(() => ({}))
      if (!response.ok) throw new Error(data.error || `HTTP ${response.status}`)
      if (data.session_id) {
        setSessionId(data.session_id)
        if (sessionStorageKey) persistSessionId(sessionStorageKey, data.session_id)
      }
      const assistantId = data.run_id ? `run-${data.run_id}` : provisionalAssistantId
      if (assistantId !== provisionalAssistantId) {
        setMessages((prev) =>
          prev.map((m) => (m.id === provisionalAssistantId ? { ...m, id: assistantId } : m)),
        )
      }
      if (data.run_id) {
        setRunId(data.run_id)
        await consumeRunEvents(data.run_id, assistantId, {
          accountId: currentAccount?.id,
          fallbackModel: selectedModel,
          onSessionId: setSessionId,
          onUserInjected: (injected) => {
            setMessages((prev) => {
              if (prev.some((m) => m.content === injected && m.role === 'user')) return prev
              return [
                ...prev,
                {
                  id: `inj-${Date.now()}`,
                  role: 'system',
                  content: `已加入本轮上下文：${injected}`,
                  timestamp: new Date(),
                },
              ]
            })
          },
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
      setMessages((prev) =>
        prev.map((m) =>
          m.id === provisionalAssistantId || m.id.startsWith('run-')
            ? {
                ...m,
                content:
                  err instanceof Error
                    ? `Error: ${err.message}`
                    : 'Error: Failed to send message. Is the backend running?',
              }
            : m,
        ),
      )
      setIsLoading(false)
    }
  }

  const clearChat = () => {
    abortRunStream()
    setMessages([])
    setSessionId('')
    setRunId('')
    setIsLoading(false)
    if (sessionStorageKey) clearPersistedSession(sessionStorageKey)
  }

  const downloadWorkspace = () => {
    if (!workspaceId) return
    const token = localStorage.getItem('codegateway_auth_token')
    const url = `/v1/workspaces/${workspaceId}/download`
    // open with token via fetch blob
    fetch(url, {
      headers: {
        ...(token ? { Authorization: `Bearer ${token}` } : {}),
        ...(currentAccount?.id ? { 'X-Account-ID': String(currentAccount.id) } : {}),
      },
    })
      .then((r) => r.blob())
      .then((blob) => {
        const a = document.createElement('a')
        a.href = URL.createObjectURL(blob)
        a.download = `${activeWorkspace?.name || 'project'}.zip`
        a.click()
        URL.revokeObjectURL(a.href)
      })
      .catch((err) => console.error(err))
  }

  const deleteWorkspace = async () => {
    if (!workspaceId || !activeWorkspace) return
    const label = activeWorkspace.github_full_name || activeWorkspace.name
    if (!confirm(`确认删除云端工作区「${label}」？\n将同时删除数据库记录与服务器上的文件，且不可恢复。`)) {
      return
    }
    setUploadError('')
    try {
      const response = await apiFetch(`/v1/workspaces/${workspaceId}`, { method: 'DELETE' }, currentAccount?.id)
      const data = await response.json().catch(() => ({}))
      if (!response.ok) {
        setUploadError(data.error || '删除工作区失败')
        return
      }
      if (sessionStorageKey) clearPersistedSession(sessionStorageKey)
      setSessionId('')
      setRunId('')
      setIsLoading(false)
      abortRunStream()
      await fetchWorkspaces()
      setMessages([
        {
          id: Date.now().toString(),
          role: 'system',
          content: `已删除云端工作区「${label}」。`,
          timestamp: new Date(),
        },
      ])
    } catch (err) {
      setUploadError(err instanceof Error ? err.message : '删除工作区失败')
    }
  }

  const onKeyDown = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault()
      sendMessage()
    }
  }

  return (
    <div className="flex flex-col h-full">
      <header className="h-14 flex items-center justify-between px-6 border-b border-border gap-3">
        <div className="min-w-0">
          <h2 className="text-sm font-semibold text-foreground">Code</h2>
          <p className="text-[11px] text-muted-foreground truncate">
            上传本地目录或授权 GitHub 仓库到云端，用自然语言让 Agent 改代码
            {sessionId && <span className="ml-2">Session: {sessionId.substring(0, 8)}...</span>}
          </p>
        </div>
        <div className="flex items-center gap-2 flex-shrink-0">
          <select
            value={selectedModel}
            onChange={(e) => setSelectedModel(e.target.value)}
            className="h-8 max-w-[180px] px-2 bg-card border border-border rounded-md text-[12px] text-foreground focus:outline-none focus:ring-2 focus:ring-ring"
          >
            {models.length === 0 ? (
              <option value="">默认模型</option>
            ) : (
              models.map((m) => (
                <option key={m.id} value={m.id}>
                  {m.id}
                </option>
              ))
            )}
          </select>
          <button
            onClick={clearChat}
            className="h-8 px-3 text-[12px] text-muted-foreground hover:text-foreground border border-border rounded-md hover:bg-accent transition-colors"
          >
            New Task
          </button>
        </div>
      </header>

      <div className="px-6 py-3 border-b border-border bg-card/40 flex flex-wrap items-center gap-2">
        <input
          ref={dirInputRef}
          type="file"
          className="hidden"
          // @ts-expect-error webkitdirectory is supported in Chromium
          webkitdirectory=""
          multiple
          onChange={onSelectDirectory}
        />
        <button
          onClick={() => dirInputRef.current?.click()}
          disabled={uploading}
          className="h-8 px-3 text-[12px] bg-primary text-primary-foreground rounded-md hover:bg-primary/90 disabled:opacity-50"
        >
          {uploading ? '打包上传中…' : '选择本地目录并上传云端'}
        </button>
        <span className="text-[11px] text-muted-foreground">
          保留工程配置；跳过凭据、版本库目录和超过 3MB 的文件，总量上限 100MB
        </span>
        <button
          onClick={() => {
            if (ghConnected) openGitHubPanel()
            else {
              setGhPanelOpen(true)
              setGhError('')
            }
          }}
          className="h-8 px-3 text-[12px] border border-border rounded-md hover:bg-accent text-foreground"
        >
          {ghConnected ? `GitHub${ghLogin ? ` @${ghLogin}` : ''}` : '连接 GitHub'}
        </button>
        <select
          value={workspaceId}
          onChange={(e) => setWorkspaceId(e.target.value)}
          className="h-8 min-w-[180px] px-2 bg-card border border-border rounded-md text-[12px]"
        >
          <option value="">未选择工作区</option>
          {workspaces.map((w) => (
            <option key={w.id} value={w.id}>
              {w.source === 'github' ? 'GH · ' : ''}
              {w.github_full_name || w.name} ({w.file_count} files)
            </option>
          ))}
        </select>
        {workspaceId && (
          <>
            {activeWorkspace?.source === 'github' && (
              <>
                <button
                  onClick={() => void pullGitHubWorkspace()}
                  disabled={!!ghSyncing || !ghConnected}
                  className="h-8 px-3 text-[12px] border border-border rounded-md hover:bg-accent text-foreground disabled:opacity-50"
                  title={!ghConnected ? '请先连接 GitHub' : '从 GitHub 拉取最新代码'}
                >
                  {ghSyncing === 'pull' ? 'Pulling…' : 'Pull'}
                </button>
                <button
                  onClick={() => void pushGitHubWorkspace()}
                  disabled={!!ghSyncing || !ghConnected}
                  className="h-8 px-3 text-[12px] border border-primary/30 text-primary rounded-md hover:bg-primary/10 disabled:opacity-50"
                  title={!ghConnected ? '请先连接 GitHub' : '提交并推送到 GitHub'}
                >
                  {ghSyncing === 'push' ? 'Pushing…' : 'Push'}
                </button>
              </>
            )}
            <button
              onClick={downloadWorkspace}
              className="h-8 px-3 text-[12px] border border-border rounded-md hover:bg-accent text-muted-foreground"
            >
              下载修改后的 zip
            </button>
            <button
              onClick={() => void deleteWorkspace()}
              className="h-8 px-3 text-[12px] border border-border rounded-md hover:bg-destructive/10 text-destructive"
            >
              删除工作区
            </button>
          </>
        )}
        {activeWorkspace && (
          <span className="text-[11px] text-muted-foreground">
            云端：{activeWorkspace.github_full_name || activeWorkspace.name} · {activeWorkspace.file_count}{' '}
            文件 · {formatBytes(activeWorkspace.size_bytes)}
            {activeWorkspace.source === 'github' ? ' · GitHub' : ''}
          </span>
        )}
        {uploadError && <span className="text-[12px] text-red-500">{uploadError}</span>}
      </div>

      {ghPanelOpen && (
        <div className="px-6 py-4 border-b border-border bg-background">
          <div className="flex items-center justify-between gap-3 mb-3">
            <div>
              <h3 className="text-sm font-medium text-foreground">从 GitHub 导入仓库</h3>
              <p className="text-[11px] text-muted-foreground mt-0.5">
                授权后可克隆仓库到云端工作区；Agent 改完代码后可用 Pull / Push 与远端同步
              </p>
            </div>
            <button
              onClick={() => setGhPanelOpen(false)}
              className="h-7 px-2 text-[12px] text-muted-foreground hover:text-foreground"
            >
              关闭
            </button>
          </div>

          {!ghConfigured && (
            <p className="text-[12px] text-amber-600 dark:text-amber-400 mb-2">
              服务端尚未配置 GitHub OAuth。请在 `codegateway.yaml` 设置 `github.client_id` /
              `client_secret`，或环境变量 `GITHUB_CLIENT_ID` / `GITHUB_CLIENT_SECRET`，并在 GitHub
              App/OAuth App 中将回调设为 `/v1/github/callback`。
            </p>
          )}

          <div className="flex flex-wrap items-center gap-2 mb-3">
            {!ghConnected ? (
              <button
                onClick={connectGitHub}
                disabled={!ghConfigured}
                className="h-8 px-3 text-[12px] bg-foreground text-background rounded-md disabled:opacity-50"
              >
                授权 GitHub
              </button>
            ) : (
              <>
                <button
                  onClick={loadGitHubRepos}
                  disabled={ghLoading}
                  className="h-8 px-3 text-[12px] border border-border rounded-md hover:bg-accent"
                >
                  {ghLoading ? '加载中…' : '刷新仓库列表'}
                </button>
                <button
                  onClick={disconnectGitHub}
                  className="h-8 px-3 text-[12px] text-muted-foreground border border-border rounded-md hover:bg-accent"
                >
                  断开授权
                </button>
              </>
            )}
            {ghError && <span className="text-[12px] text-red-500">{ghError}</span>}
          </div>

          {ghConnected && (
            <div className="max-h-56 overflow-auto border border-border rounded-md divide-y divide-border">
              {ghRepos.length === 0 && !ghLoading && (
                <div className="px-3 py-4 text-[12px] text-muted-foreground">暂无仓库，点击刷新加载</div>
              )}
              {ghRepos.map((repo) => (
                <div key={repo.id} className="flex items-center justify-between gap-3 px-3 py-2">
                  <div className="min-w-0">
                    <div className="text-[12px] font-medium text-foreground truncate">
                      {repo.full_name}
                      {repo.private ? (
                        <span className="ml-2 text-[10px] text-muted-foreground">private</span>
                      ) : null}
                    </div>
                    <div className="text-[11px] text-muted-foreground truncate">
                      {repo.description || repo.default_branch}
                    </div>
                  </div>
                  <button
                    onClick={() => importGitHubRepo(repo)}
                    disabled={!!ghImporting}
                    className="h-7 px-2.5 text-[11px] bg-primary text-primary-foreground rounded-md disabled:opacity-50 flex-shrink-0"
                  >
                    {ghImporting === repo.full_name ? '导入中…' : '导入'}
                  </button>
                </div>
              ))}
            </div>
          )}
        </div>
      )}

      <div className="flex-1 overflow-auto p-6 space-y-4">
        {messages.length === 0 ? (
          <div className="flex items-center justify-center h-full">
            <div className="text-center animate-fade-in max-w-2xl w-full">
              <div className="w-12 h-12 rounded-xl bg-primary/10 flex items-center justify-center mx-auto mb-4">
                <svg width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="#3b82f6" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                  <polyline points="16 18 22 12 16 6" />
                  <polyline points="8 6 2 12 8 18" />
                </svg>
              </div>
              <h3 className="text-base font-semibold text-foreground mb-1.5">AI 编码工作流</h3>
              <p className="text-[13px] text-muted-foreground mb-4 text-left leading-relaxed">
                1. 上传本地目录，或连接 GitHub 并导入仓库到云端工作区<br />
                2. 用快捷任务或自然语言描述需求（例如：给 user 模块加分页 API）<br />
                3. Agent 会在云端目录里 list/read/grep/write 文件完成修改<br />
                4. GitHub 工作区可用 Pull 同步远端、Push 推回仓库，或下载 zip
              </p>
              {!workspaceId && (
                <p className="text-[12px] text-amber-600 mb-4">尚未选择工作区：仍可聊天要代码片段，但无法直接改你的项目文件。</p>
              )}
              <div className="grid grid-cols-2 md:grid-cols-3 gap-2 text-left">
                {quickTasks.map((task) => (
                  <button
                    key={task.id}
                    onClick={() => applyQuickTask(task.prompt)}
                    className="px-3 py-3 rounded-lg border border-border bg-card hover:bg-accent transition-colors text-[13px] font-medium text-foreground"
                  >
                    {task.title}
                  </button>
                ))}
              </div>
            </div>
          </div>
        ) : (
          messages.map((msg) => <MessageBubble key={msg.id} message={msg} />)
        )}

        {isLoading && (
          <div className="flex justify-start animate-fade-in">
            <div className="bg-card border border-border rounded-xl px-4 py-3">
              <div className="flex items-center gap-1.5">
                <div className="w-2 h-2 rounded-full bg-muted-foreground animate-bounce"></div>
                <div className="w-2 h-2 rounded-full bg-muted-foreground animate-bounce" style={{ animationDelay: '0.1s' }}></div>
                <div className="w-2 h-2 rounded-full bg-muted-foreground animate-bounce" style={{ animationDelay: '0.2s' }}></div>
              </div>
            </div>
          </div>
        )}

        <div ref={messagesEndRef} />
      </div>

      {messages.length > 0 && (
        <div className="px-4 pt-3 flex flex-wrap gap-2">
          {quickTasks.map((task) => (
            <button
              key={task.id}
              onClick={() => applyQuickTask(task.prompt)}
              className="h-7 px-2.5 text-[11px] text-muted-foreground border border-border rounded-md hover:bg-accent hover:text-foreground transition-colors"
            >
              {task.title}
            </button>
          ))}
        </div>
      )}

      <div className="p-4 border-t border-border">
        {(voice.interim || voice.error) && (
          <div className="mb-2 text-[11px]">
            {voice.listening && voice.interim && (
              <span className="text-muted-foreground">识别中：{voice.interim}</span>
            )}
            {voice.error && <span className="text-red-500">{voice.error}</span>}
          </div>
        )}
        <div className="flex gap-2 items-end">
          <textarea
            ref={textareaRef}
            value={input}
            onChange={(e) => setInput(e.target.value)}
            onKeyDown={onKeyDown}
            placeholder={
              isLoading
                ? '生成中也可继续输入，将排队注入本轮…'
                : workspaceId
                  ? '描述要改的功能，或点麦克风口述…（Enter 发送）'
                  : '先上传/导入项目，或直接粘贴代码提问…'
            }
            rows={3}
            className="flex-1 px-4 py-2.5 bg-card border border-border rounded-lg text-[13px] text-foreground placeholder:text-muted-foreground focus:outline-none focus:ring-2 focus:ring-ring focus:border-transparent transition-colors resize-none font-mono"
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
            onClick={sendMessage}
            disabled={!input.trim()}
            className="h-10 px-4 bg-primary text-primary-foreground rounded-lg text-[13px] font-medium hover:bg-primary/90 disabled:opacity-50 disabled:cursor-not-allowed transition-colors flex items-center gap-2"
          >
            {isLoading ? 'Queue' : 'Run'}
          </button>
        </div>
      </div>
    </div>
  )
}
