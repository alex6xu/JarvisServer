import { useCallback, useEffect, useState } from 'react'
import { apiFetch } from '../context/AccountContext'
import type { DocumentLimits, ProjectDocument } from '../types/documents'

async function responseBody(response: Response) {
  return response.json().catch(() => ({}))
}

export function useProjectDocuments(accountId?: number, projectId = '') {
  const [documents, setDocuments] = useState<ProjectDocument[]>([])
  const [limits, setLimits] = useState<DocumentLimits | null>(null)
  const [loading, setLoading] = useState(false)
  const [uploading, setUploading] = useState(false)
  const [error, setError] = useState('')

  const refresh = useCallback(async () => {
    if (!accountId || !projectId) {
      setDocuments([])
      return
    }
    setLoading(true)
    setError('')
    try {
      const response = await apiFetch(
        `/v1/projects/${encodeURIComponent(projectId)}/documents?limit=100`,
        {},
        accountId,
      )
      const body = await responseBody(response)
      if (!response.ok) throw new Error(body.error || '文档加载失败')
      setDocuments(Array.isArray(body.documents) ? body.documents : [])
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : '文档加载失败')
    } finally {
      setLoading(false)
    }
  }, [accountId, projectId])

  useEffect(() => {
    void refresh()
  }, [refresh])

  useEffect(() => {
    if (!accountId) return
    void (async () => {
      const response = await apiFetch('/v1/projects/documents/limits', {}, accountId)
      if (response.ok) setLimits((await responseBody(response)) as DocumentLimits)
    })()
  }, [accountId])

  const upload = useCallback(
    async (file: File) => {
      if (!accountId || !projectId) throw new Error('请先选择项目')
      if (limits?.upload_max_bytes && file.size > limits.upload_max_bytes) {
        throw new Error(`文件超过 ${formatBytes(limits.upload_max_bytes)} 限制`)
      }
      const extension = `.${file.name.split('.').pop()?.toLowerCase() || ''}`
      if (limits?.allowed_extensions?.length && !limits.allowed_extensions.includes(extension)) {
        throw new Error(`仅支持 ${limits.allowed_extensions.join(', ')}`)
      }
      setUploading(true)
      setError('')
      try {
        const form = new FormData()
        form.append('file', file)
        const response = await apiFetch(
          `/v1/projects/${encodeURIComponent(projectId)}/documents`,
          { method: 'POST', body: form },
          accountId,
        )
        const body = await responseBody(response)
        if (!response.ok) throw new Error(body.error || '上传失败')
        const document = body.document as ProjectDocument
        setDocuments((current) => [document, ...current.filter((item) => item.id !== document.id)])
        return document
      } catch (reason) {
        const message = reason instanceof Error ? reason.message : '上传失败'
        setError(message)
        throw reason
      } finally {
        setUploading(false)
      }
    },
    [accountId, projectId, limits],
  )

  const remove = useCallback(
    async (documentId: string) => {
      if (!accountId || !projectId) return
      const response = await apiFetch(
        `/v1/projects/${encodeURIComponent(projectId)}/documents/${encodeURIComponent(documentId)}`,
        { method: 'DELETE' },
        accountId,
      )
      const body = await responseBody(response)
      if (!response.ok) throw new Error(body.error || '删除失败')
      setDocuments((current) => current.filter((item) => item.id !== documentId))
    },
    [accountId, projectId],
  )

  return { documents, limits, loading, uploading, error, setError, refresh, upload, remove }
}

export function formatBytes(bytes: number) {
  if (!Number.isFinite(bytes) || bytes < 1024) return `${bytes || 0} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`
}
