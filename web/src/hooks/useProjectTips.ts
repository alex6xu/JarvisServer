import { useCallback, useEffect, useRef, useState } from 'react'
import { apiFetch } from '../context/AccountContext'
import type { ProjectTip, TipRun, TipStatus, TipType } from '../types/tips'

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
  const scope = `${accountId}:${projectId}`
  const currentScope = useRef(scope)
  currentScope.current = scope
  const mounted = useRef(true)
  const loadVersion = useRef(0)
  useEffect(() => { mounted.current = true; return () => { mounted.current = false } }, [])
  const isCurrent = () => mounted.current && currentScope.current === scope

  const load = useCallback(async () => {
    if (!accountId || !projectId) {
      setTips([])
      return
    }
    const version = ++loadVersion.current
    setLoading(true)
    setError('')
    try {
      const response = await apiFetch(`/v1/projects/${encodeURIComponent(projectId)}/tips`, {}, accountId)
      const body = await response.json().catch(() => ({}))
      if (!response.ok) throw new Error(body.error || 'Tips 加载失败')
      if (isCurrent() && version === loadVersion.current) setTips(Array.isArray(body.tips) ? body.tips : [])
    } catch (reason) {
      if (isCurrent() && version === loadVersion.current) setError(reason instanceof Error ? reason.message : 'Tips 加载失败')
    } finally {
      if (isCurrent() && version === loadVersion.current) setLoading(false)
    }
  }, [accountId, projectId])

  useEffect(() => {
    setTips([])
    setSaving(false)
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
      if (isCurrent()) setTips((current) => [body.tip, ...current])
      return body.tip as ProjectTip
    } catch (reason) {
      if (isCurrent()) setError(reason instanceof Error ? reason.message : 'Tip 创建失败')
      throw reason
    } finally {
      if (isCurrent()) setSaving(false)
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
      if (isCurrent()) setTips((current) => current.map((item) => item.id === tip.id ? body.tip : item))
      return body.tip as ProjectTip
    } catch (reason) {
      if (isCurrent()) setError(reason instanceof Error ? reason.message : 'Tip 更新失败')
      if (reason instanceof Error && reason.message.includes('another request')) void load()
      throw reason
    } finally {
      if (isCurrent()) setSaving(false)
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
      if (isCurrent()) setTips((current) => current.filter((item) => item.id !== tip.id))
    } catch (reason) {
      if (isCurrent()) setError(reason instanceof Error ? reason.message : 'Tip 删除失败')
      throw reason
    } finally {
      if (isCurrent()) setSaving(false)
    }
  }

  const executing = useRef(false)
  const execute = async (tip: ProjectTip, mode: 'analyze' | 'execute', idempotencyKey: string) => {
    if (!accountId || !projectId || executing.current) return
    executing.current = true
    setSaving(true)
    setError('')
    try {
      const response = await apiFetch(`/v1/projects/${encodeURIComponent(projectId)}/tips/${encodeURIComponent(tip.id)}/execute`, {
        method: 'POST', body: JSON.stringify({ mode, idempotency_key: idempotencyKey }),
      }, accountId)
      const body = await response.json().catch(() => ({}))
      if (isCurrent() && body.tip) setTips((current) => current.map((item) => item.id === tip.id ? body.tip : item))
      if (!response.ok) throw new Error(body.error || body.execution?.error || 'Agent 启动失败')
      if (isCurrent()) return body.execution as TipRun
    } catch (reason) {
      if (isCurrent()) setError(reason instanceof Error ? reason.message : 'Agent 启动失败')
      throw reason
    } finally {
      executing.current = false
      if (isCurrent()) setSaving(false)
    }
  }

  return { execute, tips, loading, saving, error, setError, load, create, update, remove }
}
