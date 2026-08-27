import { useRef } from 'react'
import { Download, Trash2, Upload } from 'lucide-react'
import { useProjectDocuments, formatBytes } from '../hooks/useProjectDocuments'
import { apiFetch } from '../context/AccountContext'

export default function ProjectDocumentList({ accountId, projectId }: { accountId?: number; projectId: string }) {
  const inputRef = useRef<HTMLInputElement>(null)
  const docs = useProjectDocuments(accountId, projectId)

  const upload = async (file?: File) => {
    if (!file) return
    try {
      await docs.upload(file)
    } catch {
      // rendered below
    } finally {
      if (inputRef.current) inputRef.current.value = ''
    }
  }

  const download = async (documentId: string, filename: string) => {
    if (!accountId) return
    try {
      const response = await apiFetch(`/v1/projects/${encodeURIComponent(projectId)}/documents/${encodeURIComponent(documentId)}/download`, {}, accountId)
      if (!response.ok) throw new Error('下载失败')
      const url = URL.createObjectURL(await response.blob())
      const anchor = document.createElement('a')
      anchor.href = url
      anchor.download = filename
      anchor.click()
      URL.revokeObjectURL(url)
    } catch (reason) {
      docs.setError(reason instanceof Error ? reason.message : '下载失败')
    }
  }

  return (
    <section className="border-b border-border px-5 py-4">
      <div className="mb-3 flex items-center justify-between gap-3">
        <div>
          <h4 className="text-[12px] font-medium text-foreground">项目文档</h4>
          <p className="mt-0.5 text-[10px] text-muted-foreground">
            {docs.limits ? `单文件 ${formatBytes(docs.limits.upload_max_bytes)}，项目 ${formatBytes(docs.limits.project_max_bytes)}` : '可在 Chat / Code 中附加到消息'}
          </p>
        </div>
        <button type="button" disabled={docs.uploading} onClick={() => inputRef.current?.click()} className="inline-flex h-8 items-center gap-1.5 rounded-md border border-border px-2.5 text-[11px] text-primary hover:bg-accent disabled:opacity-50">
          <Upload className="h-3.5 w-3.5" />{docs.uploading ? '上传中…' : '上传文档'}
        </button>
        <input ref={inputRef} className="hidden" type="file" accept={docs.limits?.allowed_extensions?.join(',')} onChange={(event) => void upload(event.target.files?.[0])} />
      </div>
      {docs.error && <p className="mb-2 text-[11px] text-red-500">{docs.error}</p>}
      <div className="max-h-44 overflow-auto rounded-md border border-border">
        {docs.loading && <p className="p-3 text-[11px] text-muted-foreground">加载中…</p>}
        {!docs.loading && docs.documents.length === 0 && <p className="p-3 text-[11px] text-muted-foreground">暂无文档</p>}
        {docs.documents.map((document) => (
          <div key={document.id} className="flex items-center gap-2 border-b border-border px-3 py-2 last:border-0">
            <div className="min-w-0 flex-1">
              <p className="truncate text-[11px] font-medium">{document.filename}</p>
              <p className="text-[9px] text-muted-foreground">{formatBytes(document.size_bytes)} · {document.status}{document.error_code ? ` · ${document.error_code}` : ''}</p>
            </div>
            {document.status === 'ready' && <button type="button" aria-label={`下载 ${document.filename}`} onClick={() => void download(document.id, document.filename)} className="rounded p-1.5 text-muted-foreground hover:bg-accent hover:text-foreground"><Download className="h-3.5 w-3.5" /></button>}
            <button type="button" aria-label={`删除 ${document.filename}`} onClick={() => void docs.remove(document.id).catch((reason) => docs.setError(reason instanceof Error ? reason.message : '删除失败'))} className="rounded p-1.5 text-muted-foreground hover:bg-red-500/10 hover:text-red-500"><Trash2 className="h-3.5 w-3.5" /></button>
          </div>
        ))}
      </div>
    </section>
  )
}
