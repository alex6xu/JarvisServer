import type { UiMessage } from './sessionPersist'

export interface ConversationHeading {
  id: string
  messageId: string
  level: number
  text: string
  kind: 'question' | 'heading'
  ordinal?: number
}

export interface MarkdownHeading {
  level: number
  text: string
}

const headingPattern = /^(#{1,6})[ \t]+(.+?)[ \t]*#*[ \t]*$/
const fencePattern = /^[ \t]*(```|~~~)/

export function parseMarkdownHeadings(markdown: string): MarkdownHeading[] {
  const headings: MarkdownHeading[] = []
  let fence = ''
  for (const line of markdown.split(/\r?\n/)) {
    const fenceMatch = line.match(fencePattern)
    if (fenceMatch) {
      if (!fence) fence = fenceMatch[1]
      else if (fence === fenceMatch[1]) fence = ''
      continue
    }
    if (fence) continue
    const match = line.match(headingPattern)
    if (!match) continue
    const text = match[2]
      .replace(/\s+#+\s*$/, '')
      .replace(/\[(.*?)\]\([^)]*\)/g, '$1')
      .replace(/[*_`~]/g, '')
      .trim()
    if (text) headings.push({ level: match[1].length, text })
  }
  return headings
}

export function conversationHeadingId(messageId: string, ordinal: number): string {
  return `conversation-heading-${encodeURIComponent(messageId)}-${ordinal}`
}

export function conversationMessageId(messageId: string): string {
  return `conversation-message-${encodeURIComponent(messageId)}`
}

export function questionOutlineText(content: string, maxLength = 80): string {
  const text = content.replace(/\s+/g, ' ').trim()
  if (text.length <= maxLength) return text
  return `${text.slice(0, maxLength).trimEnd()}…`
}

/** Build a turn-oriented outline: every user question is a top-level marker and
 * markdown headings from its following answer are nested below it. */
export function extractConversationHeadings(messages: UiMessage[]): ConversationHeading[] {
  const outline: ConversationHeading[] = []
  let questionOrdinal = 0
  for (const message of messages) {
    if (message.role === 'user') {
      const text = questionOutlineText(message.content)
      if (!text) continue
      questionOrdinal += 1
      outline.push({
        id: conversationMessageId(message.id),
        messageId: message.id,
        level: 1,
        text,
        kind: 'question',
        ordinal: questionOrdinal,
      })
      continue
    }
    if (message.role !== 'assistant' || !message.content) continue
    parseMarkdownHeadings(message.content).forEach((heading, ordinal) => {
      outline.push({
        id: conversationHeadingId(message.id, ordinal),
        messageId: message.id,
        level: Math.min(heading.level + 1, 6),
        text: heading.text,
        kind: 'heading',
      })
    })
  }
  return outline
}
