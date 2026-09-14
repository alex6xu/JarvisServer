import { useRef, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { restoreTipRun, tipHasActiveRun } from '../lib/tipExecution'
import { Archive, Check, Circle, Lightbulb, ListTodo, MessageCircleQuestion, NotebookPen, Pencil, Trash2 } from 'lucide-react'
import { useProjectTips } from '../hooks/useProjectTips'
import type { ProjectTip, TipRun, TipStatus, TipType } from '../types/tips'

const typeMeta: Record<TipType, { label: string; icon: typeof Lightbulb; className: string }> = {
  idea: { label: '想法', icon: Lightbulb, className: 'text-amber-500' },
  todo: { label: '待办', icon: ListTodo, className: 'text-blue-500' },
  question: { label: '问题', icon: MessageCircleQuestion, className: 'text-violet-500' },
  note: { label: '记录', icon: NotebookPen, className: 'text-emerald-500' },
}

const statusMeta: Array<{ value: TipStatus; label: string }> = [
  { value: 'inbox', label: '待整理' },
  { value: 'planned', label: '计划中' },
  { value: 'doing', label: '进行中' },
  { value: 'done', label: '已完成' },
  { value: 'archived', label: '已归档' },
]

export default function ProjectTips({ accountId, projectId }: { accountId?: number; projectId: string }) {
  return <ProjectTipsPanel key={`${accountId}:${projectId}`} accountId={accountId} projectId={projectId} />
}

function ProjectTipsPanel({ accountId, projectId }: { accountId?: number; projectId: string }) {
  const tips = useProjectTips(accountId, projectId)
  const navigate = useNavigate()
  const requestKeys = useRef<Record<string, string>>({})
  const openRun = (run: TipRun) => {
    if (!accountId || !run.session_id) return
    const location = restoreTipRun(accountId, run)
    if (location) navigate(location)
  }
  const execute = async (tip: ProjectTip, mode: 'analyze' | 'execute') => {
    const message = mode === 'analyze'
      ? '启动只读分析？Agent 将仅分析 Tip 并提出方案（不使用工具或修改文件）。'
      : '确认交给 Agent 执行？绑定工作区的项目将进入 Code，Agent 可修改文件、运行命令；无工作区时进入 Chat。成功启动后标为进行中，不会自动完成。'
    if (tips.saving || !window.confirm(message)) return
    const slot = `${tip.id}:${mode}`
    // Retain the key on network failure so retry cannot accidentally launch twice.
    const key = requestKeys.current[slot] ||= crypto.randomUUID()
    try {
      const run = await tips.execute(tip, mode, key)
      if (run?.session_id) { delete requestKeys.current[slot]; openRun(run) }
      else if (run) void tips.load()
    } catch {
      // A durable failed launch may be retried with a fresh key after refreshing history.
      void tips.load()
    }
  }
  const [content, setContent] = useState('')
  const [type, setType] = useState<TipType>('note')
  const [priority, setPriority] = useState(0)
  const [showArchived, setShowArchived] = useState(false)

  const add = async () => {
    const value = content.trim()
    if (!value || tips.saving) return
    try {
      await tips.create({ content: value, type, priority })
      setContent('')
    } catch {
      // The hook exposes the error inline.
    }
  }

  const updateStatus = async (tip: ProjectTip, status: TipStatus) => {
    try {
      await tips.update(tip, { status })
    } catch {
      // The hook exposes the error inline.
    }
  }

  const edit = async (tip: ProjectTip) => {
    const value = window.prompt('编辑 Tip', tip.content)
    if (value === null || !value.trim() || value.trim() === tip.content) return
    try {
      await tips.update(tip, { content: value.trim() })
    } catch {
      // The hook exposes the error inline.
    }
  }

  const remove = async (tip: ProjectTip) => {
    if (!window.confirm('确定删除这条 Tip？')) return
    try {
      await tips.remove(tip)
    } catch {
      // The hook exposes the error inline.
    }
  }

  const visible = tips.tips.filter((tip) => showArchived || tip.status !== 'archived')

  return (
    <section className="border-b border-border px-5 py-4">
      <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
        <div>
          <h4 className="text-[12px] font-medium text-foreground">Tips</h4>
          <p className="mt-0.5 text-[10px] text-muted-foreground">快速记录项目想法、待办和待思考问题</p>
        </div>
        <button type="button" onClick={() => setShowArchived((value) => !value)} className="text-[10px] text-muted-foreground hover:text-foreground">
          {showArchived ? '隐藏归档' : '显示归档'}
        </button>
      </div>

      <div className="rounded-lg border border-border bg-background p-2.5">
        <textarea
          value={content}
          onChange={(event) => setContent(event.target.value)}
          onKeyDown={(event) => {
            if (event.key === 'Enter' && (event.ctrlKey || event.metaKey)) {
              event.preventDefault()
              void add()
            }
          }}
          rows={2}
          maxLength={20000}
          placeholder="记下一个想法、待办或问题…（Ctrl/⌘ + Enter 保存）"
          className="w-full resize-none bg-transparent text-[12px] leading-relaxed outline-none placeholder:text-muted-foreground"
        />
        <div className="mt-2 flex items-center justify-between gap-2">
          <div className="flex items-center gap-1.5">
            <select value={type} onChange={(event) => setType(event.target.value as TipType)} className="h-7 rounded-md border border-border bg-card px-2 text-[10px]">
              {Object.entries(typeMeta).map(([value, meta]) => <option key={value} value={value}>{meta.label}</option>)}
            </select>
            <select value={priority} onChange={(event) => setPriority(Number(event.target.value))} className="h-7 rounded-md border border-border bg-card px-2 text-[10px]">
              <option value={0}>无优先级</option>
              <option value={1}>低优先级</option>
              <option value={2}>中优先级</option>
              <option value={3}>高优先级</option>
            </select>
          </div>
          <button type="button" disabled={!content.trim() || tips.saving} onClick={() => void add()} className="h-7 rounded-md bg-primary px-3 text-[10px] font-medium text-primary-foreground disabled:opacity-50">
            {tips.saving ? '保存中…' : '添加'}
          </button>
        </div>
      </div>

      {tips.error && <p className="mt-2 text-[11px] text-red-500">{tips.error}</p>}
      {tips.loading && <p className="py-3 text-[11px] text-muted-foreground">加载 Tips…</p>}
      {!tips.loading && visible.length === 0 && <p className="py-3 text-[11px] text-muted-foreground">暂无 Tips，先快速记下一条。</p>}

      <div className="mt-3 max-h-72 space-y-3 overflow-auto">
        {statusMeta.map((group) => {
          const grouped = visible.filter((tip) => tip.status === group.value)
          if (grouped.length === 0) return null
          return (
            <div key={group.value}>
              <p className="mb-1.5 text-[10px] font-medium text-muted-foreground">{group.label} · {grouped.length}</p>
              <div className="space-y-1.5">
                {grouped.map((tip) => {
                  const meta = typeMeta[tip.type]
                  const Icon = meta.icon
                  return (
                    <div key={tip.id} className="group flex items-start gap-2 rounded-md border border-border px-2.5 py-2">
                      <Icon className={`mt-0.5 h-3.5 w-3.5 shrink-0 ${meta.className}`} />
                      <div className="min-w-0 flex-1">
                        {tip.title && <p className="text-[11px] font-medium text-foreground">{tip.title}</p>}
                        <p className={`whitespace-pre-wrap text-[11px] leading-relaxed ${tip.status === 'done' ? 'text-muted-foreground line-through' : 'text-foreground'}`}>{tip.content}</p>
                        <div className="mt-2 flex flex-wrap gap-2 text-[10px]">
                          <button type="button" disabled={tips.saving || tipHasActiveRun(tip.runs)} onClick={() => void execute(tip, 'analyze')} className="text-primary disabled:opacity-40">只读分析</button>
                          <button type="button" disabled={tips.saving || tipHasActiveRun(tip.runs)} onClick={() => void execute(tip, 'execute')} className="text-primary disabled:opacity-40">交给 Agent 执行</button>
                        </div>
                        {tip.runs && tip.runs.length > 0 && <details className="mt-2 text-[10px] text-muted-foreground">
                          <summary className="cursor-pointer">Agent 历史 · {tip.runs.length}</summary>
                          <button type="button" onClick={() => void tips.load()} className="text-primary">刷新状态</button>
                          {tip.runs.map((run) => <div key={run.id} className="mt-2">
                            <button type="button" disabled={!run.session_id} onClick={() => openRun(run)} className="text-primary disabled:text-muted-foreground">
                              {run.mode === 'analyze' ? '分析' : '执行'} · {run.status} · {new Date(run.created_at).toLocaleString()} {run.session_id ? '→ 打开会话' : ''}
                            </button>
                            {run.error && <p className="text-red-500">{run.error}</p>}
                            {run.status === 'failed' && <button type="button" onClick={() => { delete requestKeys.current[`${tip.id}:${run.mode}`]; void execute(tip, run.mode) }} disabled={tips.saving} className="text-primary">重新尝试</button>}
                            <details><summary>启动时的 Tip (v{run.snapshot.version})</summary><p className="whitespace-pre-wrap">{run.snapshot.title}
{run.snapshot.content}</p></details>
                          </div>)}
                        </details>}
                        <div className="mt-1 flex flex-wrap items-center gap-2 text-[9px] text-muted-foreground">
                          <span>{meta.label}</span>
                          {tip.priority > 0 && <span>{['', '低', '中', '高'][tip.priority]}优先级</span>}
                          <span>{new Date(tip.updated_at).toLocaleString('zh-CN', { hour12: false })}</span>
                        </div>
                      </div>
                      <div className="flex shrink-0 items-center gap-0.5 opacity-70 group-hover:opacity-100">
                        <button title="编辑" type="button" onClick={() => void edit(tip)} className="rounded p-1 text-muted-foreground hover:bg-accent hover:text-foreground"><Pencil className="h-3 w-3" /></button>
                        {tip.status !== 'planned' && tip.status !== 'done' && tip.status !== 'archived' && <button title="加入计划" type="button" onClick={() => void updateStatus(tip, 'planned')} className="rounded p-1 text-muted-foreground hover:bg-accent hover:text-foreground"><Circle className="h-3 w-3" /></button>}
                        {tip.status !== 'doing' && tip.status !== 'done' && tip.status !== 'archived' && <button title="开始处理" type="button" onClick={() => void updateStatus(tip, 'doing')} className="rounded p-1 text-muted-foreground hover:bg-accent hover:text-primary"><ListTodo className="h-3 w-3" /></button>}
                        {tip.status !== 'done' && tip.status !== 'archived' && <button title="完成" type="button" onClick={() => void updateStatus(tip, 'done')} className="rounded p-1 text-muted-foreground hover:bg-emerald-500/10 hover:text-emerald-500"><Check className="h-3 w-3" /></button>}
                        {tip.status !== 'archived' && <button title="归档" type="button" onClick={() => void updateStatus(tip, 'archived')} className="rounded p-1 text-muted-foreground hover:bg-accent hover:text-foreground"><Archive className="h-3 w-3" /></button>}
                        <button title="删除" type="button" onClick={() => void remove(tip)} className="rounded p-1 text-muted-foreground hover:bg-red-500/10 hover:text-red-500"><Trash2 className="h-3 w-3" /></button>
                      </div>
                    </div>
                  )
                })}
              </div>
            </div>
          )
        })}
      </div>
    </section>
  )
}
