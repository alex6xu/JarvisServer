import { ChevronDown, ChevronUp, ListEnd, Pin, Trash2, Zap } from 'lucide-react'
import type {
  QueueEventType,
  RunMessageQueueSnapshot,
} from '../hooks/useRunMessageQueue'

const modes: Array<{ value: QueueEventType; label: string; icon: typeof ListEnd; title: string }> = [
  { value: 'enqueue', label: '排队', icon: ListEnd, title: '当前任务结束或进入后续执行点后处理' },
  { value: 'pin', label: '置顶', icon: Pin, title: '排在普通待处理消息之前' },
  { value: 'steer', label: '立即加入', icon: Zap, title: '当前工具结束后，在下一次模型请求前加入上下文' },
]

const typeLabels: Record<QueueEventType, string> = {
  enqueue: '排队',
  pin: '置顶',
  steer: '立即加入',
}

const statusLabels: Record<string, string> = {
  pending: '等待中',
  injecting: '正在注入',
  injected: '已进入上下文',
  executing: '执行中',
  completed: '已完成',
  answered: '已回复',
  failed: '处理失败',
  cancelled: '已取消',
  dropped: '已丢弃',
}

export function QueueModeControl({
  value,
  onChange,
  disabled = false,
}: {
  value: QueueEventType
  onChange: (mode: QueueEventType) => void
  disabled?: boolean
}) {
  return (
    <div className="inline-flex h-8 items-center rounded-md border border-border bg-muted/30 p-0.5" role="group">
      {modes.map((mode) => {
        const Icon = mode.icon
        return (
          <button
            key={mode.value}
            type="button"
            title={mode.title}
            disabled={disabled}
            aria-pressed={value === mode.value}
            onClick={() => onChange(mode.value)}
            className={`flex h-7 items-center gap-1 px-2 text-[11px] transition-colors disabled:opacity-50 ${
              value === mode.value
                ? 'bg-background text-foreground shadow-sm'
                : 'text-muted-foreground hover:text-foreground'
            }`}
          >
            <Icon className="h-3.5 w-3.5" />
            <span>{mode.label}</span>
          </button>
        )
      })}
    </div>
  )
}

export default function RunMessageQueue({
  snapshot,
  busy,
  error,
  onPin,
  onMove,
  onCancel,
}: {
  snapshot: RunMessageQueueSnapshot
  busy: boolean
  error: string
  onPin: (id: string) => void
  onMove: (id: string, direction: -1 | 1) => void
  onCancel: (id: string) => void
}) {
  const activeItems = snapshot.items.filter((item) =>
    ['pending', 'injecting', 'injected', 'executing'].includes(item.status),
  )
  const recentTerminalItems = snapshot.items
    .filter((item) => ['answered', 'failed', 'dropped', 'cancelled'].includes(item.status))
    .slice(-3)
  const visibleItems = [...activeItems, ...recentTerminalItems]
  if (!visibleItems.length && !error) return null

  const pendingIds = visibleItems.filter((item) => item.status === 'pending').map((item) => item.id)
  return (
    <div className="mb-3 border-b border-border pb-3">
      <div className="mb-2 flex items-center justify-between">
        <span className="text-[11px] font-medium text-foreground">排队消息状态</span>
        <span className="text-[10px] text-muted-foreground">v{snapshot.version}</span>
      </div>
      <div className="max-h-40 space-y-1 overflow-y-auto">
        {visibleItems.map((item) => {
          const pending = item.status === 'pending'
          const pendingIndex = pendingIds.indexOf(item.id)
          return (
            <div key={item.id} className="flex min-h-8 items-center gap-2 bg-muted/35 px-2 py-1.5 text-[11px]">
              <span className="w-14 shrink-0 text-muted-foreground">{typeLabels[item.event_type]}</span>
              <span className="min-w-0 flex-1 truncate text-foreground" title={item.content}>
                {item.content}
              </span>
              <span className="shrink-0 text-[10px] text-muted-foreground">{statusLabels[item.status]}</span>
              <div className="flex h-6 shrink-0 items-center">
                <button
                  type="button"
                  title="置顶"
                  disabled={!pending || busy || item.event_type === 'pin'}
                  onClick={() => onPin(item.id)}
                  className="h-6 w-6 text-muted-foreground hover:text-foreground disabled:opacity-25"
                >
                  <Pin className="mx-auto h-3.5 w-3.5" />
                </button>
                <button
                  type="button"
                  title="上移"
                  disabled={!pending || busy || pendingIndex <= 0}
                  onClick={() => onMove(item.id, -1)}
                  className="h-6 w-6 text-muted-foreground hover:text-foreground disabled:opacity-25"
                >
                  <ChevronUp className="mx-auto h-3.5 w-3.5" />
                </button>
                <button
                  type="button"
                  title="下移"
                  disabled={!pending || busy || pendingIndex < 0 || pendingIndex === pendingIds.length - 1}
                  onClick={() => onMove(item.id, 1)}
                  className="h-6 w-6 text-muted-foreground hover:text-foreground disabled:opacity-25"
                >
                  <ChevronDown className="mx-auto h-3.5 w-3.5" />
                </button>
                <button
                  type="button"
                  title="取消"
                  disabled={!pending || busy}
                  onClick={() => onCancel(item.id)}
                  className="h-6 w-6 text-muted-foreground hover:text-destructive disabled:opacity-25"
                >
                  <Trash2 className="mx-auto h-3.5 w-3.5" />
                </button>
              </div>
            </div>
          )
        })}
      </div>
      {error && <p className="mt-2 text-[11px] text-destructive">{error}</p>}
    </div>
  )
}
