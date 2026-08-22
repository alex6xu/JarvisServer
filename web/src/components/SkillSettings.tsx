import { useCallback, useEffect, useState } from 'react'
import { AlertCircle, CheckCircle2, Loader2, Pencil, Plus, Puzzle, RefreshCw, Save, Trash2 } from 'lucide-react'
import { apiFetch } from '../context/AccountContext'

interface SkillSummary {
  name: string
  description: string
  allowed_tools: string[]
  source: 'builtin' | 'custom' | 'file'
  enabled: boolean
  account_enabled: boolean
  revision: number
  content_sha256: string
  validation_error?: string
  disable_model_invocation: boolean
  updated_at: string
}

interface SkillEditorState {
  open: boolean
  creating: boolean
  name: string
  revision: number
  content: string
}

const emptyEditor: SkillEditorState = { open: false, creating: false, name: '', revision: 0, content: '' }

function skillTemplate(): string {
  return `---
name: my-skill
description: Describe when the model should use this skill
allowed-tools:
  - websearch
---

# My Skill

Describe the workflow, input rules, tool usage, and expected output.
`
}

async function responseError(response: Response, fallback: string): Promise<string> {
  const body = await response.json().catch(() => ({}))
  return typeof body.error === 'string' && body.error ? body.error : fallback
}

export default function SkillSettings({ accountId, isAdmin }: { accountId?: number; isAdmin: boolean }) {
  const [skills, setSkills] = useState<SkillSummary[]>([])
  const [directory, setDirectory] = useState('')
  const [loading, setLoading] = useState(true)
  const [busyName, setBusyName] = useState('')
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')
  const [editor, setEditor] = useState<SkillEditorState>(emptyEditor)
  const [editorBusy, setEditorBusy] = useState(false)
  const [validation, setValidation] = useState<string[]>([])

  const loadSkills = useCallback(async () => {
    if (!accountId) {
      setSkills([])
      setLoading(false)
      return
    }
    setLoading(true)
    setError('')
    try {
      const endpoint = isAdmin ? '/v1/admin/skills' : '/v1/skills'
      const response = await apiFetch(endpoint, {}, accountId)
      if (!response.ok) throw new Error(await responseError(response, '无法读取 Skills'))
      const body = await response.json() as { skills?: SkillSummary[]; directory?: string }
      setSkills(body.skills || [])
      setDirectory(body.directory || '')
    } catch (loadError) {
      setError(loadError instanceof Error ? loadError.message : '无法读取 Skills')
    } finally {
      setLoading(false)
    }
  }, [accountId, isAdmin])

  useEffect(() => {
    void loadSkills()
  }, [loadSkills])

  const setAccountEnabled = async (skill: SkillSummary, enabled: boolean) => {
    setBusyName(skill.name)
    setError('')
    try {
      const response = await apiFetch(`/v1/skills/${encodeURIComponent(skill.name)}/status`, {
        method: 'PUT', body: JSON.stringify({ enabled }),
      }, accountId)
      if (!response.ok) throw new Error(await responseError(response, '更新 Skill 状态失败'))
      setSkills((current) => current.map((item) => item.name === skill.name ? { ...item, account_enabled: enabled } : item))
    } catch (updateError) {
      setError(updateError instanceof Error ? updateError.message : '更新 Skill 状态失败')
    } finally {
      setBusyName('')
    }
  }

  const setGlobalEnabled = async (skill: SkillSummary, enabled: boolean) => {
    setBusyName(skill.name)
    setError('')
    try {
      const response = await apiFetch(`/v1/admin/skills/${encodeURIComponent(skill.name)}/status`, {
        method: 'PUT', body: JSON.stringify({ enabled }),
      }, accountId)
      if (!response.ok) throw new Error(await responseError(response, '更新全局状态失败'))
      setSkills((current) => current.map((item) => item.name === skill.name ? { ...item, enabled } : item))
    } catch (updateError) {
      setError(updateError instanceof Error ? updateError.message : '更新全局状态失败')
    } finally {
      setBusyName('')
    }
  }

  const openCreate = () => {
    setValidation([])
    setEditor({ open: true, creating: true, name: '', revision: 0, content: skillTemplate() })
  }

  const openEdit = async (skill: SkillSummary) => {
    setBusyName(skill.name)
    setError('')
    setValidation([])
    try {
      const response = await apiFetch(`/v1/admin/skills/${encodeURIComponent(skill.name)}`, {}, accountId)
      if (!response.ok) throw new Error(await responseError(response, '无法读取 Skill 内容'))
      const body = await response.json() as { content: string; skill: SkillSummary }
      setEditor({ open: true, creating: false, name: skill.name, revision: body.skill.revision, content: body.content })
    } catch (loadError) {
      setError(loadError instanceof Error ? loadError.message : '无法读取 Skill 内容')
    } finally {
      setBusyName('')
    }
  }

  const validateEditor = async (): Promise<boolean> => {
    setEditorBusy(true)
    setValidation([])
    setError('')
    try {
      const response = await apiFetch('/v1/admin/skills/validate', {
        method: 'POST', body: JSON.stringify({ content: editor.content }),
      }, accountId)
      const body = await response.json().catch(() => ({})) as { error?: string; warnings?: string[]; skill?: SkillSummary }
      if (!response.ok) throw new Error(body.error || 'Skill 校验失败')
      setValidation(body.warnings?.length ? body.warnings : ['校验通过'])
      return true
    } catch (validationError) {
      setError(validationError instanceof Error ? validationError.message : 'Skill 校验失败')
      return false
    } finally {
      setEditorBusy(false)
    }
  }

  const saveEditor = async () => {
    if (!await validateEditor()) return
    setEditorBusy(true)
    setError('')
    try {
      const endpoint = editor.creating ? '/v1/admin/skills' : `/v1/admin/skills/${encodeURIComponent(editor.name)}`
      const response = await apiFetch(endpoint, {
        method: editor.creating ? 'POST' : 'PUT',
        body: JSON.stringify({ content: editor.content, revision: editor.revision }),
      }, accountId)
      if (!response.ok) throw new Error(await responseError(response, '保存 Skill 失败'))
      setEditor(emptyEditor)
      setMessage('Skill 已保存，下一次对话运行生效')
      await loadSkills()
    } catch (saveError) {
      setError(saveError instanceof Error ? saveError.message : '保存 Skill 失败')
    } finally {
      setEditorBusy(false)
    }
  }

  const deleteSkill = async (skill: SkillSummary) => {
    if (!window.confirm(`确定删除 Skill “${skill.name}”？文件将移动到归档目录。`)) return
    setBusyName(skill.name)
    setError('')
    try {
      const response = await apiFetch(`/v1/admin/skills/${encodeURIComponent(skill.name)}?revision=${skill.revision}`, { method: 'DELETE' }, accountId)
      if (!response.ok) throw new Error(await responseError(response, '删除 Skill 失败'))
      await loadSkills()
    } catch (deleteError) {
      setError(deleteError instanceof Error ? deleteError.message : '删除 Skill 失败')
    } finally {
      setBusyName('')
    }
  }

  const reload = async () => {
    setBusyName('__reload__')
    setError('')
    try {
      const response = await apiFetch('/v1/admin/skills/reload', { method: 'POST' }, accountId)
      if (!response.ok) throw new Error(await responseError(response, '重新加载失败'))
      setMessage('Skill 目录已重新加载')
      await loadSkills()
    } catch (reloadError) {
      setError(reloadError instanceof Error ? reloadError.message : '重新加载失败')
    } finally {
      setBusyName('')
    }
  }

  return (
    <section className="overflow-hidden rounded-lg border border-border bg-card">
      <div className="flex flex-wrap items-start justify-between gap-3 p-5">
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <Puzzle className="h-4 w-4 text-primary" />
            <h3 className="text-sm font-semibold text-foreground">Skills</h3>
          </div>
          {directory && <p className="mt-1 truncate text-[11px] text-muted-foreground">{directory}</p>}
        </div>
        {isAdmin && (
          <div className="flex items-center gap-2">
            <button type="button" title="重新加载 Skill 目录" onClick={() => void reload()} disabled={busyName === '__reload__'} className="inline-flex h-8 w-8 items-center justify-center rounded-md border border-border text-muted-foreground hover:bg-accent hover:text-foreground disabled:opacity-50">
              <RefreshCw className={`h-3.5 w-3.5 ${busyName === '__reload__' ? 'animate-spin' : ''}`} />
            </button>
            <button type="button" onClick={openCreate} className="inline-flex h-8 items-center gap-1.5 rounded-md bg-primary px-3 text-[12px] font-medium text-primary-foreground hover:bg-primary/90">
              <Plus className="h-3.5 w-3.5" />新增
            </button>
          </div>
        )}
      </div>

      {error && <div className="mx-5 mb-4 flex items-start gap-2 text-[12px] text-red-500"><AlertCircle className="mt-0.5 h-3.5 w-3.5 shrink-0" /><span className="break-all">{error}</span></div>}
      {message && <div className="mx-5 mb-4 flex items-center gap-2 text-[12px] text-green-500"><CheckCircle2 className="h-3.5 w-3.5" /><span>{message}</span></div>}

      <div className="border-t border-border">
        {loading ? (
          <div className="flex h-24 items-center justify-center text-muted-foreground"><Loader2 className="h-4 w-4 animate-spin" /></div>
        ) : skills.length === 0 ? (
          <p className="px-5 py-8 text-center text-[12px] text-muted-foreground">暂无可用 Skill</p>
        ) : (
          <div className="divide-y divide-border">
            {skills.map((skill) => {
              const busy = busyName === skill.name
              return (
                <div key={skill.name} className="flex flex-col gap-3 px-5 py-4 sm:flex-row sm:items-center">
                  <div className="min-w-0 flex-1">
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="text-[13px] font-medium text-foreground">{skill.name}</span>
                      <span className="rounded border border-border px-1.5 py-0.5 text-[9px] uppercase text-muted-foreground">{skill.source}</span>
                      <span className="text-[10px] text-muted-foreground">rev {skill.revision}</span>
                    </div>
                    <p className="mt-1 text-[11px] leading-5 text-muted-foreground">{skill.description}</p>
                    {skill.allowed_tools.length > 0 && <p className="mt-1 text-[10px] text-muted-foreground">Tools: {skill.allowed_tools.join(', ')}</p>}
                  </div>
                  <div className="flex shrink-0 flex-wrap items-center gap-3">
                    {isAdmin && (
                      <label className="flex items-center gap-1.5 text-[11px] text-muted-foreground">
                        <input type="checkbox" checked={skill.enabled} disabled={busy} onChange={(event) => void setGlobalEnabled(skill, event.target.checked)} className="h-4 w-4 accent-primary" />全局
                      </label>
                    )}
                    <label className="flex items-center gap-1.5 text-[11px] text-muted-foreground">
                      <input type="checkbox" checked={skill.account_enabled} disabled={busy || !skill.enabled} onChange={(event) => void setAccountEnabled(skill, event.target.checked)} className="h-4 w-4 accent-primary" />当前账号
                    </label>
                    {isAdmin && skill.source !== 'builtin' && (
                      <>
                        <button type="button" title={`编辑 ${skill.name}`} disabled={busy} onClick={() => void openEdit(skill)} className="inline-flex h-7 w-7 items-center justify-center rounded-md text-muted-foreground hover:bg-accent hover:text-foreground disabled:opacity-50"><Pencil className="h-3.5 w-3.5" /></button>
                        <button type="button" title={`删除 ${skill.name}`} disabled={busy} onClick={() => void deleteSkill(skill)} className="inline-flex h-7 w-7 items-center justify-center rounded-md text-muted-foreground hover:bg-destructive/10 hover:text-destructive disabled:opacity-50"><Trash2 className="h-3.5 w-3.5" /></button>
                      </>
                    )}
                  </div>
                </div>
              )
            })}
          </div>
        )}
      </div>

      {editor.open && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4" role="dialog" aria-modal="true" aria-label={editor.creating ? '新增 Skill' : `编辑 ${editor.name}`}>
          <div className="flex max-h-[90vh] w-full max-w-3xl flex-col overflow-hidden rounded-lg border border-border bg-background shadow-2xl">
            <div className="flex items-center justify-between border-b border-border px-5 py-4">
              <div><h4 className="text-sm font-semibold text-foreground">{editor.creating ? '新增 Skill' : editor.name}</h4>{!editor.creating && <p className="mt-1 text-[10px] text-muted-foreground">revision {editor.revision}</p>}</div>
              <button type="button" onClick={() => setEditor(emptyEditor)} className="text-[12px] text-muted-foreground hover:text-foreground">关闭</button>
            </div>
            <textarea value={editor.content} onChange={(event) => setEditor((current) => ({ ...current, content: event.target.value }))} spellCheck={false} className="min-h-80 flex-1 resize-none bg-background p-5 font-mono text-[12px] leading-5 text-foreground outline-none" />
            {validation.length > 0 && <div className="border-t border-border px-5 py-2 text-[11px] text-muted-foreground">{validation.join(' · ')}</div>}
            <div className="flex justify-end gap-2 border-t border-border px-5 py-3">
              <button type="button" disabled={editorBusy} onClick={() => void validateEditor()} className="h-8 rounded-md border border-border px-3 text-[12px] text-foreground hover:bg-accent disabled:opacity-50">校验</button>
              <button type="button" disabled={editorBusy} onClick={() => void saveEditor()} className="inline-flex h-8 items-center gap-1.5 rounded-md bg-primary px-3 text-[12px] font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50">{editorBusy ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Save className="h-3.5 w-3.5" />}保存</button>
            </div>
          </div>
        </div>
      )}
    </section>
  )
}
