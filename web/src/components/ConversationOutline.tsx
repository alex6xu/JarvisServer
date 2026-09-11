import { useEffect, useMemo, useState } from 'react'
import type { UiMessage } from '../lib/sessionPersist'
import {
  extractConversationHeadings,
  type ConversationHeading,
} from '../lib/conversationOutline'

export { conversationHeadingId, conversationMessageId, extractConversationHeadings } from '../lib/conversationOutline'
export type { ConversationHeading }

/** A compact question navigator rendered inside the conversation's left edge.
 * Each horizontal mark represents one user question; hover reveals emphasis and
 * the native title tooltip, while click scrolls directly to that question. */
export default function ConversationOutline({ messages }: { messages: UiMessage[] }) {
  const questions = useMemo(
    () => extractConversationHeadings(messages).filter((item) => item.kind === 'question'),
    [messages],
  )
  const [activeId, setActiveId] = useState('')

  useEffect(() => {
    if (!questions.length) {
      setActiveId('')
      return
    }
    const elements = questions
      .map((question) => document.getElementById(question.id))
      .filter((element): element is HTMLElement => element !== null)
    if (!elements.length) return

    if (typeof IntersectionObserver === 'undefined') {
      setActiveId(questions[0].id)
      return
    }
    const observer = new IntersectionObserver(
      (entries) => {
        const visible = entries
          .filter((entry) => entry.isIntersecting)
          .sort((a, b) => a.boundingClientRect.top - b.boundingClientRect.top)
        if (visible[0]?.target.id) setActiveId(visible[0].target.id)
      },
      { rootMargin: '-10% 0px -75% 0px', threshold: [0, 1] },
    )
    elements.forEach((element) => observer.observe(element))
    return () => observer.disconnect()
  }, [questions])

  if (!questions.length) return null

  const scrollTo = (question: ConversationHeading) => {
    document.getElementById(question.id)?.scrollIntoView({ behavior: 'smooth', block: 'start' })
    setActiveId(question.id)
  }

  return (
    <aside
      className="flex w-9 shrink-0 items-start justify-center border-r border-border/60 bg-card/20"
      aria-label="问题快速导航"
    >
      <nav className="flex max-h-full w-full flex-col items-center gap-2 overflow-y-auto py-4">
        {questions.map((question) => {
          const active = activeId === question.id
          return (
            <button
              key={question.id}
              type="button"
              title={`${question.ordinal}. ${question.text}`}
              aria-label={`跳转到问题 ${question.ordinal}：${question.text}`}
              aria-current={active ? 'location' : undefined}
              onClick={() => scrollTo(question)}
              className="group flex h-3 w-full shrink-0 items-center justify-center"
            >
              <span
                className={`block rounded-full transition-all duration-150 group-hover:h-1 group-hover:w-6 group-focus-visible:h-1 group-focus-visible:w-6 ${
                  active
                    ? 'h-0.5 w-5 bg-primary'
                    : 'h-px w-3 bg-muted-foreground/50 group-hover:bg-primary'
                }`}
              />
            </button>
          )
        })}
      </nav>
    </aside>
  )
}
