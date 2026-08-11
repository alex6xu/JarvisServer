import ReactMarkdown from 'react-markdown'
import remarkGfm from 'remark-gfm'
import type { UiMessage } from '../lib/sessionPersist'
import ToolStepCard from './ToolStepCard'

type Props = {
  message: UiMessage
  markdownAssistant?: boolean
}

export default function MessageBubble({ message, markdownAssistant = true }: Props) {
  const isUser = message.role === 'user'
  const isSystem = message.role === 'system'

  return (
    <div className={`flex ${isUser ? 'justify-end' : 'justify-start'} animate-fade-in`}>
      <div
        className={`max-w-[80%] rounded-xl px-4 py-2.5 ${
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
        {markdownAssistant && message.role === 'assistant' ? (
          <div className="prose prose-sm dark:prose-invert max-w-none text-[13px]">
            <ReactMarkdown remarkPlugins={[remarkGfm]}>{message.content || ''}</ReactMarkdown>
          </div>
        ) : (
          <p className="text-[13px] whitespace-pre-wrap">{message.content}</p>
        )}
        {message.toolSteps && message.toolSteps.length > 0 && (
          <ToolStepCard messageId={message.id} steps={message.toolSteps} />
        )}
        <p className={`text-[11px] mt-1.5 ${isUser ? 'text-primary-foreground/60' : 'text-muted-foreground'}`}>
          {message.timestamp.toLocaleTimeString()}
        </p>
      </div>
    </div>
  )
}
