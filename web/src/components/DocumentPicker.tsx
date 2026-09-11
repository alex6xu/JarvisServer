import { useRef, useState } from 'react'
import { Paperclip, Upload } from 'lucide-react'
import type { ProjectDocument } from '../types/documents'
import { useProjectDocuments } from '../hooks/useProjectDocuments'
import DocumentChips from './DocumentChips'

export default function DocumentPicker({
  accountId,
  projectId,
  selected,
  onChange,
  disabled = false,
}: {
  accountId?: number
  projectId: string
  selected: ProjectDocument[]
  onChange: (documents: ProjectDocument[]) => void
  disabled?: boolean
}) {
  const inputRef = useRef<HTMLInputElement>(null)
  const [open, setOpen] = useState(false)
  const docs = useProjectDocuments(accountId, projectId)
  const selectedIds = new Set(selected.map((item) => item.id))

  const toggle = (document: ProjectDocument) => {
    if (selectedIds.has(document.id)) onChange(selected.filter((item) => item.id !== document.id))
    else if (selected.length < 10) onChange([...selected, document])
  }

  const upload = async (file?: File) => {
    if (!file) return
    try {
      const document = await docs.upload(file)
      if (document.status === 'ready' && selected.length < 10) onChange([...selected, document])
    } catch {
      // Hook exposes a concise inline error.
    } finally {
      if (inputRef.current) inputRef.current.value = ''
    }
  }

  if (!projectId) return null
  return (
    <div className="relative">
      <button
        type="button"
        disabled={disabled}
        onClick={() => setOpen((value) => !value)}
        className="inline-flex h-8 items-center gap-1.5 rounded-md border border-border px-2.5 text-[11px] text-muted-foreground hover:bg-accent disabled:opacity-50"
      >
        <Paperclip className="h-3.5 w-3.5" />
        文档{selected.length ? ` (${selected.length})` : ''}
      </button>
      {open && (
        <div className="absolute bottom-10 left-0 z-30 w-80 rounded-lg border border-border bg-card p-3 shadow-xl">
          <div className="mb-2 flex items-center justify-between gap-2">
            <span className="text-[12px] font-medium">项目文档</span>
            <button
              type="button"
              disabled={docs.uploading || disabled}
              onClick={() => inputRef.current?.click()}
              className="inline-flex items-center gap-1 text-[11px] text-primary disabled:opacity-50"
            >
              <Upload className="h-3 w-3" />{docs.uploading ? '上传中…' : '上传'}
            </button>
            <input
              ref={inputRef}
              type="file"
              className="hidden"
              accept={docs.limits?.allowed_extensions?.join(',')}
              onChange={(event) => void upload(event.target.files?.[0])}
            />
          </div>
          {docs.error && <p className="mb-2 text-[11px] text-red-500">{docs.error}</p>}
          <div className="max-h-52 space-y-1 overflow-auto">
            {docs.loading && <p className="text-[11px] text-muted-foreground">加载中…</p>}
            {!docs.loading && !docs.documents.length && <p className="text-[11px] text-muted-foreground">暂无文档，可上传 txt、md、csv、json、docx、xlsx。</p>}
            {docs.documents.map((document) => (
              <label key={document.id} className={`flex items-center gap-2 rounded px-2 py-1.5 text-[11px] ${document.status === 'ready' ? 'cursor-pointer hover:bg-accent' : 'opacity-50'}`}>
                <input
                  type="checkbox"
                  checked={selectedIds.has(document.id)}
                  disabled={document.status !== 'ready' || (!selectedIds.has(document.id) && selected.length >= 10)}
                  onChange={() => toggle(document)}
                />
                <span className="min-w-0 flex-1 truncate">{document.filename}</span>
                <span className="text-[9px] text-muted-foreground">{document.status}</span>
              </label>
            ))}
          </div>
          {selected.length > 0 && <div className="mt-2 border-t border-border pt-2"><DocumentChips documents={selected} onRemove={(id) => onChange(selected.filter((item) => item.id !== id))} compact /></div>}
        </div>
      )}
    </div>
  )
}
