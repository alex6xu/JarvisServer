import { Cable, Code2, Folder, Gauge, History, LineChart, MessageSquare, Settings, Tags, Users } from 'lucide-react'

// Existing destinations only. Shared by the sidebar and the location breadcrumb.
export const navigationGroups = [
  { label: '工作区', placement: 'sidebar', items: [
    { href: '/', label: '对话', icon: MessageSquare, adminOnly: false },
    { href: '/code', label: '代码工作区', icon: Code2, adminOnly: false },
    { href: '/sessions', label: '全部会话', icon: History, adminOnly: false },
    { href: '/projects', label: '项目管理', icon: Folder, adminOnly: false },
  ] },
  { label: '管理', placement: 'settings', items: [
    { href: '/dashboard', label: '概览', icon: Gauge, adminOnly: false },
    { href: '/stock', label: '行情', icon: LineChart, adminOnly: false },
    { href: '/providers', label: '模型服务', icon: Cable, adminOnly: false },
    { href: '/tags', label: '标签', icon: Tags, adminOnly: false },
    { href: '/accounts', label: '账号', icon: Users, adminOnly: true },
    { href: '/settings', label: '设置', icon: Settings, adminOnly: false },
  ] },
]

export function isNavigationActive(pathname: string, href: string): boolean {
  const canonical = pathname.replace(/^\/coder(?=\/|$)/, '/code').replace(/^\/channels(?=\/|$)/, '/providers')
  return canonical === href || (href !== '/' && canonical.startsWith(`${href}/`))
}

export const sidebarNavigationGroups = navigationGroups.filter((group) => group.placement === 'sidebar')

export function settingsNavigation(isAdmin: boolean) {
  return navigationGroups.filter((group) => group.placement === 'settings').flatMap((group) => group.items).filter((item) => !item.adminOnly || isAdmin)
}
