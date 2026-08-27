import { useEffect, useState } from 'react'
import { FolderKanban, Plus } from 'lucide-react'
import { useNavigate } from 'react-router-dom'
import { apiFetch, useAccount } from '../context/AccountContext'
import ProjectDocumentList from '../components/ProjectDocumentList'
import {
  chatSessionKey,
  coderSessionKey,
  coderWorkspaceKey,
  writeLocal,
} from '../lib/sessionPersist'

interface Project {
  id: string
  name: string
  slug: string
  description?: string
  source: 'user' | 'workspace'
  status: string
  linked_workspace_id?: string
  session_count: number
  message_count: number
  updated_at: string
}

interface ProjectSession {
  id: string
  type: 'chat' | 'code'
  title: string
  preview?: string
  workspace_id?: string
  message_count: number
  updated_at: string
}

interface ProjectTag {
  id: string
  slug: string
  name: string
  kind: string
  use_count: number
}

interface ProjectDetail {
  project: Project
  sessions: ProjectSession[]
  tags: ProjectTag[]
}

export default function ProjectsPage() {
  const { currentAccount } = useAccount()
  const navigate = useNavigate()
  const [projects, setProjects] = useState<Project[]>([])
  const [selected, setSelected] = useState('')
  const [detail, setDetail] = useState<ProjectDetail | null>(null)
  const [name, setName] = useState('')
  const [creating, setCreating] = useState(false)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    setSelected('')
    setDetail(null)
    void loadProjects()
  }, [currentAccount?.id])

  const loadProjects = async () => {
    if (!currentAccount?.id) return
    setLoading(true)
    setError('')
    try {
      const response = await apiFetch('/v1/projects', {}, currentAccount.id)
      const body = await response.json().catch(() => ({}))
      if (!response.ok) throw new Error(body.error || '项目加载失败')
      setProjects(body.projects || [])
    } catch (loadError) {
      setError(loadError instanceof Error ? loadError.message : '项目加载失败')
    } finally {
      setLoading(false)
    }
  }

  const openProject = async (id: string) => {
    if (!currentAccount?.id) return
    setSelected(id)
    setLoading(true)
    setError('')
    try {
      const response = await apiFetch(`/v1/projects/${encodeURIComponent(id)}`, {}, currentAccount.id)
      const body = await response.json().catch(() => ({}))
      if (!response.ok) throw new Error(body.error || '项目详情加载失败')
      setDetail({
        ...body,
        sessions: Array.isArray(body.sessions) ? body.sessions : [],
        tags: Array.isArray(body.tags) ? body.tags : [],
      } as ProjectDetail)
    } catch (loadError) {
      setDetail(null)
      setError(loadError instanceof Error ? loadError.message : '项目详情加载失败')
    } finally {
      setLoading(false)
    }
  }

  const createProject = async () => {
    if (!currentAccount?.id || !name.trim()) return
    setCreating(true)
    setError('')
    try {
      const response = await apiFetch('/v1/projects', {
        method: 'POST',
        body: JSON.stringify({ name: name.trim() }),
      }, currentAccount.id)
      const body = await response.json().catch(() => ({}))
      if (!response.ok) throw new Error(body.error || '创建项目失败')
      setName('')
      await loadProjects()
      if (body.project?.id) await openProject(body.project.id)
    } catch (createError) {
      setError(createError instanceof Error ? createError.message : '创建项目失败')
    } finally {
      setCreating(false)
    }
  }

  const continueSession = (session: ProjectSession) => {
    if (!currentAccount?.id) return
    if (session.type === 'code' && session.workspace_id) {
      writeLocal(coderWorkspaceKey(currentAccount.id), session.workspace_id)
      writeLocal(coderSessionKey(currentAccount.id, session.workspace_id), session.id)
      navigate(`/code?workspace=${encodeURIComponent(session.workspace_id)}&session=${encodeURIComponent(session.id)}`)
      return
    }
    writeLocal(chatSessionKey(currentAccount.id), session.id)
    navigate(`/?session=${encodeURIComponent(session.id)}`)
  }

  return (
    <div className="flex h-full min-h-0 flex-col p-6">
      <div className="mb-5 flex flex-wrap items-start justify-between gap-3">
        <div>
          <div className="flex items-center gap-2">
            <FolderKanban className="h-5 w-5 text-primary" />
            <h2 className="text-base font-semibold text-foreground">项目</h2>
          </div>
          <p className="mt-1 text-[13px] text-muted-foreground">
            Code 会话按 Workspace 自动归档；Chat 会话可在 Sessions 页面手动加入项目。
          </p>
        </div>
        <div className="flex items-center gap-2">
          <input
            value={name}
            onChange={(event) => setName(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === 'Enter') void createProject()
            }}
            placeholder="新项目名称"
            className="h-9 w-48 rounded-md border border-border bg-card px-3 text-[13px]"
          />
          <button
            type="button"
            disabled={!name.trim() || creating}
            onClick={() => void createProject()}
            className="inline-flex h-9 items-center gap-2 rounded-md bg-primary px-3 text-[12px] font-medium text-primary-foreground disabled:opacity-50"
          >
            <Plus className="h-3.5 w-3.5" />
            创建
          </button>
        </div>
      </div>

      {error && <p className="mb-3 text-[12px] text-red-500">{error}</p>}

      <div className="grid min-h-0 flex-1 grid-cols-1 gap-4 lg:grid-cols-[340px_1fr]">
        <div className="overflow-auto rounded-xl border border-border bg-card">
          {loading && projects.length === 0 && <p className="p-4 text-[13px] text-muted-foreground">加载中…</p>}
          {!loading && projects.length === 0 && <p className="p-4 text-[13px] text-muted-foreground">暂无项目。创建一个项目，或先在 Code 中打开工作区。</p>}
          {projects.map((project) => (
            <button
              key={project.id}
              type="button"
              onClick={() => void openProject(project.id)}
              className={`w-full border-b border-border px-4 py-3 text-left last:border-b-0 ${selected === project.id ? 'bg-primary/10' : 'hover:bg-accent'}`}
            >
              <div className="flex items-center justify-between gap-2">
                <span className="truncate text-[13px] font-medium text-foreground">{project.name}</span>
                <span className="shrink-0 text-[10px] text-muted-foreground">{project.source === 'workspace' ? 'Workspace' : '手动'}</span>
              </div>
              <p className="mt-1 text-[11px] text-muted-foreground">{project.session_count} 个会话 · {project.message_count} 条消息</p>
              {project.description && <p className="mt-1 line-clamp-2 text-[11px] text-muted-foreground">{project.description}</p>}
            </button>
          ))}
        </div>

        <div className="min-h-0 overflow-hidden rounded-xl border border-border bg-card">
          {!detail && <div className="flex h-full items-center justify-center p-8 text-[13px] text-muted-foreground">选择一个项目查看相关会话</div>}
          {detail && (
            <div className="flex h-full min-h-0 flex-col">
              <div className="border-b border-border px-5 py-4">
                <div className="flex flex-wrap items-start justify-between gap-2">
                  <div>
                    <h3 className="text-sm font-semibold text-foreground">{detail.project.name}</h3>
                    <p className="mt-1 text-[11px] text-muted-foreground">{detail.project.description || '暂无项目描述'}</p>
                  </div>
                  {detail.project.linked_workspace_id && <span className="rounded bg-accent px-2 py-1 text-[10px] text-muted-foreground">{detail.project.linked_workspace_id}</span>}
                </div>
                <div className="mt-3 flex flex-wrap gap-1.5">
                  {detail.tags.map((tag) => (
                    <button key={tag.id} type="button" onClick={() => navigate(`/tags`)} className="rounded bg-accent px-2 py-1 text-[10px] text-muted-foreground hover:text-foreground">
                      {tag.name} · {tag.use_count}
                    </button>
                  ))}
                </div>
              </div>
              <ProjectDocumentList accountId={currentAccount?.id} projectId={detail.project.id} />
              <div className="min-h-0 flex-1 overflow-auto divide-y divide-border">
                {detail.sessions.length === 0 && <p className="p-5 text-[13px] text-muted-foreground">项目中还没有会话</p>}
                {detail.sessions.map((session) => (
                  <div key={session.id} className="px-5 py-4">
                    <div className="flex flex-wrap items-start justify-between gap-3">
                      <div className="min-w-0 flex-1">
                        <div className="flex items-center gap-2">
                          <span className="rounded bg-accent px-1.5 py-0.5 text-[9px] uppercase text-muted-foreground">{session.type}</span>
                          <p className="truncate text-[13px] font-medium text-foreground">{session.title || session.id}</p>
                        </div>
                        <p className="mt-1 line-clamp-2 text-[12px] text-muted-foreground">{session.preview || '暂无预览'}</p>
                        <p className="mt-2 text-[10px] text-muted-foreground">{session.message_count} 条消息 · {new Date(session.updated_at).toLocaleString('zh-CN', { hour12: false })}</p>
                      </div>
                      <button type="button" onClick={() => continueSession(session)} className="h-8 rounded-md border border-border px-3 text-[11px] text-primary hover:bg-accent">
                        {session.type === 'code' ? '在 Code 继续' : '在 Chat 继续'}
                      </button>
                    </div>
                  </div>
                ))}
              </div>
            </div>
          )}
        </div>
      </div>
    </div>
  )
}
