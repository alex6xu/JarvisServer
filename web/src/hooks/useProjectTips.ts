import { useCallback, useEffect, useState } from 'react'
import { apiFetch } from '../context/AccountContext'
import type { ProjectTip, TipStatus, TipType } from '../types/tips'

interface CreateTipInput {
  content: string
  type: TipType
  priority?: number
}

interface UpdateTipInput {
  type?: TipType
  status?: TipStatus
  title?: string
  content?: string
  priority?: number
  due_at?: string
}

export function useProjectTips(accountId: number | undefined, projectId: string) {
  const [tips, setTips] = useState<ProjectTip[]>([])
  const [loading, setLoading] = useState(false)
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')

  const load = useCallback(async () => {
    if (!accountId || !projectId) {
      setTips([])
      return
    }
    setLoading(true)
    setError('')
    try {
      const response = await apiFetch(`/v1/projects/${encodeURIComponent(projectId)}/tips`, {}, accountId)
      const body = await response.json().catch(() => ({}))
      if (!response.ok) throw new Error(body.error || 'Tips 加载失败')
      setTips(Array.isArray(body.tips) ? body.tips : [])
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : 'Tips 加载失败')
    } finally {
      setLoading(false)
    }
  }, [accountId, projectId])

  useEffect(() => {
    void load()
  }, [load])

  const create = async (input: CreateTipInput) => {
    if (!accountId || !projectId) return
    setSaving(true)
    setError('')
    try {
      const response = await apiFetch(`/v1/projects/${encodeURIComponent(projectId)}/tips`, {
        method: 'POST',
        body: JSON.stringify(input),
      }, accountId)
      const body = await response.json().catch(() => ({}))
      if (!response.ok) throw new Error(body.error || 'Tip 创建失败')
      setTips((current) => [body.tip, ...current])
      return body.tip as ProjectTip
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : 'Tip 创建失败')
      throw reason
    } finally {
      setSaving(false)
    }
  }

  const update = async (tip: ProjectTip, input: UpdateTipInput) => {
    if (!accountId || !projectId) return
    setSaving(true)
    setError('')
    try {
      const response = await apiFetch(`/v1/projects/${encodeURIComponent(projectId)}/tips/${encodeURIComponent(tip.id)}`, {
        method: 'PATCH',
        body: JSON.stringify({ ...input, version: tip.version }),
      }, accountId)
      const body = await response.json().catch(() => ({}))
      if (!response.ok) throw new Error(body.error || 'Tip 更新失败')
      setTips((current) => current.map((item) => item.id === tip.id ? body.tip : item))
      return body.tip as ProjectTip
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : 'Tip 更新失败')
      if (reason instanceof Error && reason.message.includes('another request')) void load()
      throw reason
    } finally {
      setSaving(false)
    }
  }

  const remove = async (tip: ProjectTip) => {
    if (!accountId || !projectId) return
    setSaving(true)
    setError('')
    try {
      const response = await apiFetch(`/v1/projects/${encodeURIComponent(projectId)}/tips/${encodeURIComponent(tip.id)}`, {
        method: 'DELETE',
      }, accountId)
      const body = await response.json().catch(() => ({}))
      if (!response.ok) throw new Error(body.error || 'Tip 删除失败')
      setTips((current) => current.filter((item) => item.id !== tip.id))
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : 'Tip 删除失败')
      throw reason
    } finally {
      setSaving(false)
    }
  }

  return { tips, loading, saving, error, setError, load, create, update, remove }
}
