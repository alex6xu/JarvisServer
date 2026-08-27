import { FileText, X } from 'lucide-react'
import type { MessageDocument, ProjectDocument } from '../types/documents'
import { formatBytes } from '../hooks/useProjectDocuments'

type ChipDocument = ProjectDocument | MessageDocument

export default function DocumentChips({
  documents,
  onRemove,
  compact = false,
}: {
  documents: ChipDocument[]
  onRemove?: (id: string) => void
  compact?: boolean
}) {
  if (!documents.length) return null
  return (
    <div className="flex flex-wrap gap-1.5">
      {documents.map((document) => (
        <span
          key={document.id}
          title={`${document.filename} · ${formatBytes(document.size_bytes)}`}
          className={`inline-flex max-w-full items-center gap-1 rounded-md border border-border bg-background/60 ${compact ? 'px-1.5 py-0.5 text-[10px]' : 'px-2 py-1 text-[11px]'}`}
        >
          <FileText className="h-3 w-3 shrink-0" />
          <span className="max-w-48 truncate">{document.filename}</span>
          {document.status !== 'ready' && <span className="opacity-60">({document.status})</span>}
          {onRemove && (
            <button
              type="button"
              aria-label={`移除 ${document.filename}`}
              onClick={() => onRemove(document.id)}
              className="ml-0.5 rounded p-0.5 hover:bg-accent"
            >
              <X className="h-3 w-3" />
            </button>
          )}
        </span>
      ))}
    </div>
  )
}
