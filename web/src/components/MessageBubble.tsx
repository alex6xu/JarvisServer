import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import type { Components } from 'react-markdown'
import {
  segmentsFromContentAndTools,
  type MessageSegment,
  type ToolStep,
  type UiMessage,
} from '../lib/sessionPersist'
import ToolStepCard from './ToolStepCard'
import DocumentChips from './DocumentChips'

type Props = {
  message: UiMessage
  markdownAssistant?: boolean
  collapseTools?: boolean
}

type RenderSegment =
  | { type: 'text'; content: string; sourceIndex: number }
  | { type: 'tools'; steps: ToolStep[]; sourceIndex: number; stepOffset: number }

const markdownComponents: Components = {
  table: ({ children }) => (
    <div className="markdown-table-wrap">
      <table>{children}</table>
    </div>
  ),
  a: ({ href, children }) => (
    <a href={href} target="_blank" rel="noreferrer">
      {children}
    </a>
  ),
}

function AssistantMarkdown({ text }: { text: string }) {
  if (!text) return null
  return (
    <div className="markdown-body text-[13px]">
      <ReactMarkdown remarkPlugins={[remarkGfm]} components={markdownComponents}>
        {text}
      </ReactMarkdown>
    </div>
  )
}

function resolveSegments(message: UiMessage): MessageSegment[] {
  if (message.segments?.length) return message.segments
  if (message.role === 'assistant') {
    return segmentsFromContentAndTools(message.content || '', message.toolSteps)
  }
  return message.content ? [{ type: 'text', content: message.content }] : []
}

export function groupToolSegments(segments: MessageSegment[]): RenderSegment[] {
  const grouped: RenderSegment[] = []
  let toolOrdinal = 0
  segments.forEach((segment, sourceIndex) => {
    if (segment.type === 'text') {
      grouped.push({ type: 'text', content: segment.content, sourceIndex })
      return
    }
    toolOrdinal += 1
    const previous = grouped[grouped.length - 1]
    if (previous?.type === 'tools') {
      previous.steps.push(segment.step)
      return
    }
    grouped.push({
      type: 'tools',
      steps: [segment.step],
      sourceIndex,
      stepOffset: toolOrdinal,
    })
  })
  return grouped
}

export default function MessageBubble({ message, markdownAssistant = true, collapseTools = false }: Props) {
  const isUser = message.role === 'user'
  const isSystem = message.role === 'system'
  const segments = resolveSegments(message)
  const renderSegments = collapseTools ? groupToolSegments(segments) : null
  let toolOrdinal = 0

  return (
    <div className={`flex ${isUser ? 'justify-end' : 'justify-start'} animate-fade-in`}>
      <div
        className={`${
          isUser ? 'max-w-[80%]' : 'max-w-[min(92%,48rem)] w-full'
        } rounded-xl px-4 py-2.5 ${
          isUser
            ? 'bg-primary text-primary-foreground'
            : isSystem
              ? 'bg-amber-500/10 border border-amber-500/30'
              : 'bg-card border border-border'
        }`}
      >
        {!isUser && (
          <div className="text-[11px] opacity-60 mb-1 flex items-center gap-2">
            <span className="capitalize">{message.role}</span>
            {message.model && <span>· {message.model}</span>}
          </div>
        )}

        {isUser ? (
          <p className="text-[13px] whitespace-pre-wrap">{message.content}</p>
        ) : isSystem ? (
          <p className="text-[13px] whitespace-pre-wrap">{message.content}</p>
        ) : (
          <div className="space-y-3">
            {(renderSegments || segments).map((seg, idx) => {
              if (seg.type === 'text') {
                if (!seg.content) return null
                return markdownAssistant ? (
                  <AssistantMarkdown key={`${message.id}-t-${'sourceIndex' in seg ? seg.sourceIndex : idx}`} text={seg.content} />
                ) : (
                  <p key={`${message.id}-t-${'sourceIndex' in seg ? seg.sourceIndex : idx}`} className="text-[13px] whitespace-pre-wrap">
                    {seg.content}
                  </p>
                )
              }
              if ('steps' in seg) {
                return (
                  <ToolStepCard
                    key={`${message.id}-tools-${seg.sourceIndex}`}
                    messageId={`${message.id}-${seg.sourceIndex}`}
                    steps={seg.steps}
                    stepOffset={seg.stepOffset}
                    defaultOpen={false}
                  />
                )
              }
              toolOrdinal += 1
              return (
                <ToolStepCard
                  key={`${message.id}-tool-${idx}`}
                  messageId={`${message.id}-${idx}`}
                  steps={[seg.step]}
                  stepOffset={toolOrdinal}
                  compact
                />
              )
            })}
          </div>
        )}

        {message.documents?.length ? <div className="mt-2"><DocumentChips documents={message.documents} compact /></div> : null}

        <p className={`text-[11px] mt-1.5 ${isUser ? 'text-primary-foreground/60' : 'text-muted-foreground'}`}>
          {message.timestamp.toLocaleTimeString()}
        </p>
      </div>
    </div>
  )
}
