import { describe, expect, it, vi } from 'vitest'
import { renderToStaticMarkup } from 'react-dom/server'
vi.mock('react-router-dom', () => ({ useLocation: () => ({ key: 'new-project' }), useNavigate: () => vi.fn(), Link: ({ to, children }: { to: string; children: React.ReactNode }) => <a href={to}>{children}</a> }))
vi.mock('../context/AccountContext', () => ({ useAccount: () => ({ currentAccount: { id: 1 } }), apiFetch: vi.fn() }))
vi.mock('../hooks/useVoiceInput', () => ({ useVoiceInput: () => ({ supported: false }) }))
vi.mock('../hooks/useRunEventStream', () => ({ useRunEventStream: () => ({}) }))
vi.mock('../hooks/useRunStop', () => ({ useRunStop: () => ({}) }))
vi.mock('../hooks/useRunMessageQueue', () => ({ useRunMessageQueue: () => ({}), isQueueUnavailableError: () => false }))
vi.mock('../hooks/useSessionRestore', () => ({ useSessionRestore: () => ({}) }))
import CoderPage from './CoderPage'
describe('blank code project controls', () => {
  it('requires a directory and puts model controls below messages, without duplicate history', () => {
    vi.stubGlobal('window', { location: { search: '?new=1' } })
    const html = renderToStaticMarkup(<CoderPage />)
    expect(html).toContain('选择工作目录，开始代码项目')
    expect(html).toContain('选择本地目录并上传云端')
    expect(html).toContain('连接 GitHub')
    expect(html).toMatch(/aria-label="代码任务消息" disabled=""/)
    expect(html.match(/aria-label="当前模型"/g)).toHaveLength(1)
    expect(html.indexOf('aria-label="当前模型"')).toBeGreaterThan(html.indexOf('</header>'))
    expect(html).not.toContain('最近会话')
    expect(html).toContain('项目资料与管理')
    vi.unstubAllGlobals()
  })
})
