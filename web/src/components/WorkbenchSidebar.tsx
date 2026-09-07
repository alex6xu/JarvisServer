import { useEffect, useRef, useState } from 'react'
import { Link, useLocation, useNavigate } from 'react-router-dom'
import { Folder, History, LogOut, PanelLeftClose, PanelLeftOpen, Plus, Search, Settings, SquarePen, X } from 'lucide-react'
import { apiFetch, useAccount } from '../context/AccountContext'
import { useAuth } from '../context/AuthContext'
import type { ProjectSummary } from '../types/documents'

import { navigationGroups, isNavigationActive } from '../lib/navigation'

type RecentSession = { id: string; title?: string }

export default function WorkbenchSidebar() {
  const { currentAccount, accounts, setCurrentAccountId, loading } = useAccount()
  const { user, isAdmin, logout } = useAuth()
  const location = useLocation()
  const navigate = useNavigate()
  const sidebarRef = useRef<HTMLElement>(null)
  const [open, setOpen] = useState(false)
  const [searching, setSearching] = useState(false)
  const [search, setSearch] = useState('')
  const [projects, setProjects] = useState<ProjectSummary[]>([])
  const [sessions, setSessions] = useState<RecentSession[]>([])
  const [busy, setBusy] = useState(true)
  const [error, setError] = useState('')
  const [revision, setRevision] = useState(0)

  useEffect(() => setOpen(false), [location.pathname, location.search])
  useEffect(() => {
    if (!open) return
    const sidebar = sidebarRef.current
    const previous = document.activeElement as HTMLElement | null
    const focusable = () => Array.from(sidebar?.querySelectorAll<HTMLElement>('a[href], button:not(:disabled), input:not(:disabled), select:not(:disabled)') || []).filter((element) => element.getClientRects().length > 0)
    focusable()[0]?.focus()
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setOpen(false)
      if (event.key !== 'Tab') return
      const items = focusable()
      const first = items[0], last = items[items.length - 1]
      if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus() }
      else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus() }
    }
    const media = window.matchMedia('(max-width: 760px)')
    const closeOnDesktop = () => { if (!media.matches) setOpen(false) }
    window.addEventListener('keydown', closeOnEscape)
    media.addEventListener('change', closeOnDesktop)
    return () => {
      window.removeEventListener('keydown', closeOnEscape)
      media.removeEventListener('change', closeOnDesktop)
      previous?.focus()
    }
  }, [open])
  useEffect(() => {
    if (!currentAccount?.id) return
    let cancelled = false
    const load = async () => {
      setBusy(true)
      try {
        const responses = await Promise.all([
          apiFetch('/v1/projects', {}, currentAccount.id),
          apiFetch('/v1/agent/sessions/recent?mode=chat&limit=12', {}, currentAccount.id),
        ])
        if (responses.some((response) => !response.ok)) throw new Error('项目与会话加载失败')
        const [projectData, sessionData] = await Promise.all(responses.map((response) => response.json()))
        if (cancelled) return
        setProjects(projectData.projects || [])
        setSessions(sessionData.sessions || [])
        setError('')
      } catch {
        if (!cancelled) setError('项目与会话加载失败')
      } finally { if (!cancelled) setBusy(false) }
    }
    setProjects([])
    setSessions([])
    void load()
    window.addEventListener('focus', load)
    window.addEventListener('jarvis:sessions-changed', load)
    return () => {
      cancelled = true
      window.removeEventListener('focus', load)
      window.removeEventListener('jarvis:sessions-changed', load)
    }
  }, [currentAccount?.id, location.pathname, revision])

  const matches = (value: string) => value.toLocaleLowerCase().includes(search.toLocaleLowerCase())
  return (
    <>
      <button type="button" aria-controls="app-sidebar" className="workbench-menu-toggle" title="打开侧栏" aria-label="打开侧栏" aria-expanded={open} onClick={() => setOpen(true)}><PanelLeftOpen size={18} /></button>
      {open && <button className="workbench-scrim" aria-label="关闭侧栏" onClick={() => setOpen(false)} />}
      <aside ref={sidebarRef} id="app-sidebar" aria-label="工作区导航" role={open ? 'dialog' : undefined} aria-modal={open ? true : undefined} className={`workbench-sidebar ${open ? 'is-open' : ''}`}>
        <div className="workbench-brand">
          <Link to="/" className="flex min-w-0 items-center gap-2"><span className="control-brand-mark" aria-hidden="true">J</span><span>Jarvis<span className="control-brand-caption">AI 工作区</span></span></Link>
          <div className="flex items-center gap-1">
            <button type="button" title="搜索项目与最近会话" aria-label="搜索项目与最近会话" onClick={() => setSearching(!searching)}><Search size={17} /></button>
            <button type="button" className="workbench-close" title="关闭侧栏" aria-label="关闭侧栏" onClick={() => setOpen(false)}><PanelLeftClose size={17} /></button>
          </div>
        </div>
        {searching && <div className="workbench-search"><Search size={14} /><input autoFocus aria-label="搜索项目与会话" placeholder="搜索项目与会话" value={search} onChange={(event) => setSearch(event.target.value)} /><button title="清除搜索" aria-label="清除搜索" onClick={() => setSearch('')}><X size={14} /></button></div>}
        <nav aria-label="主导航" className="workbench-navigation">
          <a href="/?new=1" className="workbench-nav-item"><SquarePen size={17} />新对话</a>
          {navigationGroups.map((group) => <div key={group.label} className="control-nav-group">
            <div className="workbench-section-title">{group.label}</div>
            {group.items.filter((item) => !item.adminOnly || isAdmin).map(({ href, label, icon: Icon }) => {
              const active = isNavigationActive(location.pathname, href)
              return <Link key={href} to={href} className={`workbench-nav-item ${active ? 'is-active' : ''}`} aria-current={active ? 'page' : undefined}><Icon size={16} aria-hidden="true" />{label}</Link>
            })}
          </div>)}
        </nav>
        <div className="workbench-history">
          <div className="workbench-section-title"><span>项目</span><Link to="/projects" title="新建项目" aria-label="新建项目"><Plus size={15} /></Link></div>
          {projects.filter((project) => matches(project.name)).map((project) => <a key={project.id} href={`/?new=1&project=${encodeURIComponent(project.id)}`} className="workbench-nav-item" title={project.name}><Folder size={16} /><span className="truncate">{project.name}</span></a>)}
          {!busy && !error && projects.length === 0 && <p className="workbench-empty">暂无项目</p>}
          <div className="workbench-section-title"><span>最近</span><Link to="/sessions" title="全部会话" aria-label="全部会话"><History size={15} /></Link></div>
          {sessions.filter((session) => matches(session.title || session.id)).map((session) => <a key={session.id} href={`/?session=${encodeURIComponent(session.id)}`} aria-current={location.pathname === '/' && new URLSearchParams(location.search).get('session') === session.id ? 'page' : undefined} className="workbench-nav-item workbench-session" title={session.title || session.id}><span className="truncate">{session.title || session.id}</span></a>)}
          {busy && <p className="workbench-empty" role="status">加载中...</p>}
          {!busy && !error && sessions.length === 0 && <p className="workbench-empty">暂无会话</p>}
          {search && !projects.some((project) => matches(project.name)) && !sessions.some((session) => matches(session.title || session.id)) && <p className="workbench-empty">没有匹配结果</p>}
          {error && <button className="workbench-empty" onClick={() => setRevision((value) => value + 1)}>{error}，重试</button>}
        </div>
        <div className="workbench-account">
          {isAdmin && <select aria-label="切换账号" value={currentAccount?.id || ''} disabled={loading || !accounts.length} onChange={(event) => setCurrentAccountId(Number(event.target.value))}>{accounts.map((account) => <option key={account.id} value={account.id}>{account.username}</option>)}</select>}
          <div className="flex items-center gap-2">
            <Link to="/settings" className="workbench-nav-item min-w-0 flex-1" title="设置"><Settings size={17} /><span className="truncate">{user?.username || '设置'}</span></Link>
            <button title="退出登录" aria-label="退出登录" onClick={async () => { await logout(); navigate('/login', { replace: true }) }}><LogOut size={16} /></button>
          </div>
        </div>
      </aside>
    </>
  )
}
