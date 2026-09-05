import { useEffect, useRef, type ReactNode } from 'react'
import type { UiMessage } from '../lib/sessionPersist'
import MessageBubble from './MessageBubble'
import ConversationOutline from './ConversationOutline'

type Props = {
  messages: UiMessage[]
  isLoading?: boolean
  empty?: ReactNode
  markdownAssistant?: boolean
}

export default function MessageList({ messages, isLoading, empty, markdownAssistant = true }: Props) {
  const endRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    endRef.current?.scrollIntoView({ behavior: 'smooth' })
  }, [messages, isLoading])

  if (messages.length === 0 && empty) {
    return <>{empty}</>
  }

  return (
    <div className="flex min-h-0 flex-1">
      {markdownAssistant && <ConversationOutline messages={messages} />}
      <div className="min-w-0 flex-1 overflow-auto p-6 space-y-4">
        {messages.map((msg) => (
          <MessageBubble key={msg.id} message={msg} markdownAssistant={markdownAssistant} />
        ))}
        {isLoading && (
          <div className="flex justify-start animate-fade-in">
            <div className="bg-card border border-border rounded-xl px-4 py-3">
              <div className="flex items-center gap-1.5">
                <div className="w-1.5 h-1.5 bg-muted-foreground rounded-full animate-pulse" />
                <div
                  className="w-1.5 h-1.5 bg-muted-foreground rounded-full animate-pulse"
                  style={{ animationDelay: '0.2s' }}
                />
                <div
                  className="w-1.5 h-1.5 bg-muted-foreground rounded-full animate-pulse"
                  style={{ animationDelay: '0.4s' }}
                />
              </div>
            </div>
          </div>
        )}
        <div ref={endRef} />
      </div>
    </div>
  )
}
