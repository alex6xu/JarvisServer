import { describe, expect, it } from 'vitest'
import type { UiMessage } from './sessionPersist'
import {
  conversationHeadingId,
  conversationMessageId,
  extractConversationHeadings,
  parseMarkdownHeadings,
  questionOutlineText,
} from './conversationOutline'

function message(id: string, role: UiMessage['role'], content: string): UiMessage {
  return { id, role, content, timestamp: new Date('2026-01-01T00:00:00Z') }
}

describe('conversation outline', () => {
  it('uses user questions as top-level markers and nests answer headings', () => {
    const messages = [
      message('q/1', 'user', '如何实现上传功能？'),
      message('a/1', 'assistant', '# 方案\n正文\n## API\n更多内容'),
      message('notice', 'system', 'system notice'),
      message('q2', 'user', '怎么验证？'),
      message('a2', 'assistant', '没有 Markdown 标题的回答'),
    ]

    expect(extractConversationHeadings(messages)).toEqual([
      {
        id: conversationMessageId('q/1'),
        messageId: 'q/1',
        level: 1,
        text: '如何实现上传功能？',
        kind: 'question',
        ordinal: 1,
      },
      {
        id: conversationHeadingId('a/1', 0),
        messageId: 'a/1',
        level: 2,
        text: '方案',
        kind: 'heading',
      },
      {
        id: conversationHeadingId('a/1', 1),
        messageId: 'a/1',
        level: 3,
        text: 'API',
        kind: 'heading',
      },
      {
        id: conversationMessageId('q2'),
        messageId: 'q2',
        level: 1,
        text: '怎么验证？',
        kind: 'question',
        ordinal: 2,
      },
    ])
  })

  it('normalizes and truncates long questions', () => {
    expect(questionOutlineText('  第一行\n\n第二行  ', 20)).toBe('第一行 第二行')
    expect(questionOutlineText('abcdefghijklmnopqrstuvwxyz', 10)).toBe('abcdefghij…')
  })

  it('ignores headings inside fenced code blocks', () => {
    expect(parseMarkdownHeadings('# visible\n```md\n# hidden\n```\n## shown')).toEqual([
      { level: 1, text: 'visible' },
      { level: 2, text: 'shown' },
    ])
  })
})
