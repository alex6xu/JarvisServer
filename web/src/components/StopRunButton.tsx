import { Square } from 'lucide-react'

type Props = {
  stopping?: boolean
  onStop: () => void
  className?: string
}

export default function StopRunButton({ stopping = false, onStop, className = '' }: Props) {
  return (
    <button
      type="button"
      onClick={onStop}
      disabled={stopping}
      title="停止当前后台任务"
      className={`h-8 px-3 inline-flex items-center gap-1.5 text-[12px] text-destructive border border-destructive/40 rounded-md hover:bg-destructive/10 disabled:opacity-50 transition-colors ${className}`}
    >
      <Square size={12} fill="currentColor" aria-hidden="true" />
      {stopping ? '停止中' : '停止'}
    </button>
  )
}
