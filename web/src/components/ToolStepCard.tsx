import { ToolStep } from '../lib/sessionPersist'

type Props = {
  messageId: string
  steps: ToolStep[]
  defaultOpen?: boolean
  /** 1-based label for the first step when rendering a single inline call. */
  stepOffset?: number
  /** Inline between text segments (no outer “执行过程” wrapper). */
  compact?: boolean
}

/** Pull the most useful one-liner (bash command, path, …) from tool args JSON. */
export function summarizeToolArgs(tool: string, args: string): string {
  const raw = (args || '').trim()
  if (!raw) return ''
  try {
    const o = JSON.parse(raw) as Record<string, unknown>
    const prefsByTool: Record<string, string[]> = {
      bash: ['command'],
      read: ['path', 'file_path'],
      write: ['path', 'file_path'],
      edit: ['path', 'file_path'],
      grep: ['pattern', 'path'],
      find: ['pattern', 'path', 'glob'],
      webfetch: ['url'],
      web_fetch: ['url'],
      websearch: ['query'],
      web_search: ['query'],
      task: ['description', 'prompt'],
      todo: ['todos'],
    }
    const prefs = [
      ...(prefsByTool[tool] || []),
      'command',
      'path',
      'file_path',
      'query',
      'pattern',
      'url',
      'prompt',
      'description',
    ]
    for (const key of prefs) {
      const v = o[key]
      if (typeof v === 'string' && v.trim()) {
        const s = v.trim().replace(/\s+/g, ' ')
        return s.length > 120 ? `${s.slice(0, 117)}…` : s
      }
    }
    const keys = Object.keys(o)
    if (keys.length === 1) {
      const v = o[keys[0]]
      const s = typeof v === 'string' ? v : JSON.stringify(v)
      if (s) return s.length > 120 ? `${s.slice(0, 117)}…` : s
    }
  } catch {
    return raw.length > 120 ? `${raw.slice(0, 117)}…` : raw
  }
  return ''
}

function formatArgsDisplay(args: string): string {
  const raw = (args || '').trim()
  if (!raw) return ''
  try {
    return JSON.stringify(JSON.parse(raw), null, 2)
  } catch {
    return raw
  }
}

function StepBlock({
  step,
  label,
  defaultOpen,
}: {
  step: ToolStep
  label: string
  defaultOpen: boolean
}) {
  const preview = summarizeToolArgs(step.tool, step.args)
  const argsText = formatArgsDisplay(step.args)
  const running = step.status === 'running'
  const errored = step.status === 'error'

  return (
    <details
      open={defaultOpen}
      className={`rounded-lg border px-2.5 py-2 ${
        errored
          ? 'border-destructive/50 bg-destructive/5'
          : 'border-border/70 bg-background/60'
      }`}
    >
      <summary className="cursor-pointer font-mono text-[11px] text-foreground break-all flex items-start gap-2">
        <span className="shrink-0 rounded bg-muted px-1.5 py-0.5 text-[10px] text-muted-foreground mt-0.5">
          {running ? '执行中' : errored ? '失败' : '工具'}
        </span>
        <span className="min-w-0">
          <span className="text-foreground/90">
            {label}. {step.tool}
          </span>
          {preview && (
            <span className="block mt-0.5 text-muted-foreground whitespace-pre-wrap break-all">
              {preview}
            </span>
          )}
        </span>
      </summary>

      {argsText ? (
        <div className="mt-2 border-t border-border/50 pt-2">
          <div className="text-[10px] uppercase tracking-wide text-muted-foreground mb-1">输入 / 命令</div>
          <pre className="whitespace-pre-wrap break-words text-[11px] text-foreground/90">{argsText}</pre>
        </div>
      ) : (
        <div className="mt-2 border-t border-border/50 pt-2 text-[11px] text-muted-foreground">
          （无参数）
        </div>
      )}

      {step.result ? (
        <div className="mt-2">
          <div className="text-[10px] uppercase tracking-wide text-muted-foreground mb-1">输出</div>
          <pre className="max-h-[28rem] overflow-auto whitespace-pre-wrap break-words rounded-md bg-muted/40 p-2 text-[11px] text-foreground/90">
            {step.result}
          </pre>
        </div>
      ) : running ? (
        <div className="mt-2 text-[11px] text-muted-foreground">正在执行…</div>
      ) : null}
    </details>
  )
}

export default function ToolStepCard({
  messageId,
  steps,
  defaultOpen = true,
  stepOffset = 1,
  compact = false,
}: Props) {
  if (!steps.length) return null

  const runningCount = steps.filter((step) => step.status === 'running').length
  const errorCount = steps.filter((step) => step.status === 'error').length
  const toolNames = [...new Set(steps.map((step) => step.tool).filter(Boolean))]
  const toolsLabel = toolNames.slice(0, 3).join(', ') + (toolNames.length > 3 ? ` +${toolNames.length - 3}` : '')
  const statusLabel = runningCount > 0 ? '执行中' : errorCount > 0 ? `${errorCount} 项失败` : '已完成'

  if (compact) {
    return (
      <div className="space-y-2">
        {steps.map((step, idx) => (
          <StepBlock
            key={`${messageId}-tool-${step.id || idx}`}
            step={step}
            label={String(stepOffset + idx)}
            defaultOpen={defaultOpen}
          />
        ))}
      </div>
    )
  }

  return (
    <details open={defaultOpen} className="mt-3 text-[12px] text-muted-foreground border-t border-border/60 pt-2">
      <summary className="cursor-pointer text-foreground/80 font-medium py-0.5">
        <span
          className={`inline-block mr-2 rounded px-1.5 py-0.5 text-[10px] font-normal ${
            errorCount > 0
              ? 'bg-destructive/10 text-destructive'
              : runningCount > 0
                ? 'bg-amber-500/10 text-amber-600'
                : 'bg-muted text-muted-foreground'
          }`}
        >
          {statusLabel}
        </span>
        <span>{steps.length} 步工具调用</span>
        {toolsLabel && <span className="ml-2 font-mono text-[11px] text-muted-foreground">{toolsLabel}</span>}
      </summary>
      <div className="mt-2 space-y-2">
        {steps.map((step, idx) => (
          <StepBlock
            key={`${messageId}-tool-${step.id || idx}`}
            step={step}
            label={String(stepOffset + idx)}
            defaultOpen={false}
          />
        ))}
      </div>
    </details>
  )
}
