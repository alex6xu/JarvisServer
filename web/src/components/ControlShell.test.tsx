import { describe, expect, it, vi } from 'vitest'
import { renderToStaticMarkup } from 'react-dom/server'
import Layout from './Layout'
import MessageBubble from './MessageBubble'
import { isNavigationActive, navigationGroups } from '../lib/navigation'

const state = vi.hoisted(() => ({ pathname: '/coder', search: '', admin: false, layout: 'classic', loading: false }))
vi.mock('react-router-dom', () => ({
  useLocation: () => ({ pathname: state.pathname, search: state.search }),
  useNavigate: () => vi.fn(),
  Link: ({ to, children, ...props }: { to: string; children: React.ReactNode }) => <a href={to} {...props}>{children}</a>,
  Outlet: () => <div>existing route content</div>,
}))
vi.mock('../context/AccountContext', () => ({
  useAccount: () => ({ currentAccount: { id: 7, username: 'test-account' }, accounts: [{ id: 7, username: 'test-account' }], loading: false, setCurrentAccountId: vi.fn() }),
  apiFetch: vi.fn(),
}))
vi.mock('../context/AuthContext', () => ({ useAuth: () => ({ user: { username: 'test-user' }, isAdmin: state.admin, logout: vi.fn() }) }))
vi.mock('../context/AppearanceContext', () => ({ useAppearance: () => ({ homeLayout: state.layout, loading: state.loading }) }))

describe('Control shell presentation contracts', () => {
  it('preserves aliases and nested route selection without prefix collisions', () => {
    expect(isNavigationActive('/coder', '/code')).toBe(true)
    expect(isNavigationActive('/coder/task', '/code')).toBe(true)
    expect(isNavigationActive('/channels', '/providers')).toBe(true)
    expect(isNavigationActive('/stock/crypto/BTC', '/stock')).toBe(true)
    expect(isNavigationActive('/code-other', '/code')).toBe(false)
    expect(isNavigationActive('/sessions', '/')).toBe(false)
    expect(navigationGroups.flatMap((g) => g.items).map((i) => i.href)).toEqual([
      '/', '/code', '/sessions', '/projects', '/dashboard', '/stock', '/providers', '/tags', '/accounts', '/settings',
    ])
  })

  it.each(['classic', 'workbench'])('uses the same accessible navigation and content for %s', (layout) => {
    state.layout = layout
    state.admin = false
    const html = renderToStaticMarkup(<Layout />)
    expect(html).toContain(layout === 'workbench' ? 'workbench-theme' : 'control-dark')
    expect(html).toContain('href="#main-content"')
    expect(html).toContain('id="main-content" tabindex="-1"')
    expect(html).toContain('aria-controls="app-sidebar"')
    expect(html).toContain('aria-expanded="false"')
    expect(html).toMatch(/href="\/code"[^>]*aria-current="page"/)
    expect(html).not.toContain('href="/accounts"')
    expect(html).not.toContain('aria-label="切换账号"')
    expect(html).toContain('existing route content')
    expect(html).toContain('test-account')
  })

  it('keeps administrator navigation and account selection', () => {
    state.admin = true
    const html = renderToStaticMarkup(<Layout />)
    expect(html).toContain('href="/accounts"')
    expect(html).toContain('aria-label="切换账号"')
    expect(html).toContain('value="7" selected=""')
    expect(html).toContain('aria-label="退出登录"')
    state.admin = false
  })

  it('does not mount the shell before appearance settings resolve', () => {
    state.loading = true
    expect(renderToStaticMarkup(<Layout />)).toContain('role="status"')
    expect(renderToStaticMarkup(<Layout />)).not.toContain('id="app-sidebar"')
    state.loading = false
  })

  it('retains message text, markdown, model metadata and role styling hooks', () => {
    const timestamp = new Date('2026-01-01T10:00:00Z')
    const assistant = renderToStaticMarkup(<MessageBubble message={{ id: 'a', role: 'assistant', content: '**Result**\n\n```ts\nconst x = 1\n```', model: 'test-model', timestamp }} />)
    expect(assistant).toContain('data-message-role="assistant"')
    expect(assistant).toContain('<strong>Result</strong>')
    expect(assistant).toContain('const x = 1')
    expect(assistant).toContain('test-model')
    const user = renderToStaticMarkup(<MessageBubble message={{ id: 'u', role: 'user', content: '<script>not html</script>', timestamp }} />)
    expect(user).toContain('data-message-role="user"')
    expect(user).toContain('&lt;script&gt;not html&lt;/script&gt;')
  })
})
