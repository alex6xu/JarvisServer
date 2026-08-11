import { ToolStep } from '../lib/sessionPersist'

type Props = {
  messageId: string
  steps: ToolStep[]
  defaultOpen?: boolean
}

export default function ToolStepCard({ messageId, steps, defaultOpen = true }: Props) {
  if (!steps.length) return null
  return (
    <details open={defaultOpen} className="mt-3 text-[12px] text-muted-foreground border-t border-border/60 pt-2">
      <summary className="cursor-pointer text-foreground/80 font-medium">
        执行过程 · {steps.length} 步工具调用
      </summary>
      <div className="mt-2 space-y-2">
        {steps.map((step, idx) => (
          <details
            key={`${messageId}-tool-${idx}`}
            className="rounded-lg border border-border/70 bg-background/50 px-2.5 py-2"
          >
            <summary className="cursor-pointer font-mono text-[11px] text-foreground break-all">
              {idx + 1}. {step.tool}
            </summary>
            {step.args && (
              <pre className="mt-2 whitespace-pre-wrap break-words text-[11px] opacity-90">{step.args}</pre>
            )}
            {step.result && (
              <pre className="mt-2 max-h-[28rem] overflow-auto whitespace-pre-wrap break-words rounded-md bg-muted/40 p-2 text-[11px] text-foreground/90">
                {step.result}
              </pre>
            )}
          </details>
        ))}
      </div>
    </details>
  )
}
