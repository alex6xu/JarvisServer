import { describe, expect, it, vi } from 'vitest'
import { renderToStaticMarkup } from 'react-dom/server'
import SidebarProject from './SidebarProject'
import { SettingsMenuItems } from './SidebarSettings'
import { settingsNavigation, sidebarNavigationGroups } from '../lib/navigation'

vi.mock('react-router-dom', () => ({ Link: ({ to, children, ...props }: { to: string; children: React.ReactNode }) => <a href={to} {...props}>{children}</a> }))

describe('sidebar settings and projects', () => {
  it.each([false, true])('preserves destinations and admin permissions (%s)', (admin) => {
    const html = renderToStaticMarkup(<SettingsMenuItems isAdmin={admin} pathname="/channels" />)
    expect(html.includes('href="/accounts"')).toBe(admin)
    for (const href of ['/settings', '/dashboard', '/stock', '/providers', '/tags']) expect(html).toContain(`href="${href}"`)
    expect(html).toMatch(/href="\/providers"[^>]*aria-current="page"/)
    expect(html).toContain('role="menuitem"')
    expect(sidebarNavigationGroups.flatMap((g) => g.items).some((i) => settingsNavigation(admin).some((s) => s.href === i.href))).toBe(false)
  })
  it('keeps the project entry accessible while collapsed and renders expanded chats and empty states', () => {
    const props = { project: { id: 'p&1', name: 'Project' }, sessions: [{ id: 'chat&1', title: 'Conversation', type: 'chat' }], currentSession: 'chat&1', onToggle: vi.fn() }
    const closed = renderToStaticMarkup(<SidebarProject {...props} expanded={false} />)
    expect(closed).toContain('aria-expanded="false"')
    expect(closed).toContain('project=p%261')
    expect(closed).not.toContain('Conversation')
    const opened = renderToStaticMarkup(<SidebarProject {...props} expanded />)
    expect(opened).toContain('aria-expanded="true"')
    expect(opened).toContain('aria-controls="project-chats-p&amp;1"')
    expect(opened).toContain('id="project-chats-p&amp;1"')
    expect(opened).toContain('session=chat%261')
    expect(opened).toContain('aria-current="page"')
    expect(renderToStaticMarkup(<SidebarProject {...props} sessions={[]} expanded />)).toContain('暂无对话')
    expect(renderToStaticMarkup(<SidebarProject {...props} expanded search="absent" />)).toContain('没有匹配对话')
  })
})
