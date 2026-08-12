import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import type { Components } from 'react-markdown'
import {
  segmentsFromContentAndTools,
  type MessageSegment,
  type UiMessage,
} from '../lib/sessionPersist'
import ToolStepCard from './ToolStepCard'

type Props = {
  message: UiMessage
  markdownAssistant?: boolean
}

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

export default function MessageBubble({ message, markdownAssistant = true }: Props) {
  const isUser = message.role === 'user'
  const isSystem = message.role === 'system'
  const segments = resolveSegments(message)
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
            {segments.map((seg, idx) => {
              if (seg.type === 'text') {
                if (!seg.content) return null
                return markdownAssistant ? (
                  <AssistantMarkdown key={`${message.id}-t-${idx}`} text={seg.content} />
                ) : (
                  <p key={`${message.id}-t-${idx}`} className="text-[13px] whitespace-pre-wrap">
                    {seg.content}
                  </p>
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

        <p className={`text-[11px] mt-1.5 ${isUser ? 'text-primary-foreground/60' : 'text-muted-foreground'}`}>
          {message.timestamp.toLocaleTimeString()}
        </p>
      </div>
    </div>
  )
}
