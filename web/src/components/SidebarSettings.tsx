import { useEffect, useRef, useState } from 'react'
import { Link, useLocation } from 'react-router-dom'
import { Settings } from 'lucide-react'
import { isNavigationActive, settingsNavigation } from '../lib/navigation'

export function SettingsMenuItems({ isAdmin, pathname }: { isAdmin: boolean; pathname: string }) {
  return <>{settingsNavigation(isAdmin).map(({ href, label, icon: Icon }) => <Link key={href} to={href} role="menuitem" className="workbench-nav-item" aria-current={isNavigationActive(pathname, href) ? 'page' : undefined}><Icon size={16} />{label}</Link>)}</>
}

export default function SidebarSettings({ isAdmin }: { isAdmin: boolean }) {
  const [open, setOpen] = useState(false)
  const root = useRef<HTMLDivElement>(null)
  const trigger = useRef<HTMLButtonElement>(null)
  const location = useLocation()
  useEffect(() => setOpen(false), [location.pathname, location.search])
  useEffect(() => {
    if (!open) return
    root.current?.querySelector<HTMLElement>('[role="menuitem"]')?.focus()
    const outside = (event: PointerEvent) => { if (!root.current?.contains(event.target as Node)) setOpen(false) }
    document.addEventListener('pointerdown', outside)
    return () => document.removeEventListener('pointerdown', outside)
  }, [open])
  return <div ref={root} className="workbench-settings" onBlur={(event) => { if (!event.currentTarget.contains(event.relatedTarget as Node | null)) setOpen(false) }} onKeyDown={(event) => {
    if (!open) return
    const items = Array.from(root.current?.querySelectorAll<HTMLElement>('[role="menuitem"]') || [])
    const index = items.indexOf(document.activeElement as HTMLElement)
    if (event.key === 'Escape') { event.preventDefault(); event.stopPropagation(); setOpen(false); trigger.current?.focus() }
    else if (['ArrowDown', 'ArrowUp', 'Home', 'End'].includes(event.key)) {
      event.preventDefault()
      const next = event.key === 'Home' ? 0 : event.key === 'End' ? items.length - 1 : (index + (event.key === 'ArrowDown' ? 1 : -1) + items.length) % items.length
      items[next]?.focus()
    }
  }}>
    <button ref={trigger} type="button" className="workbench-nav-item workbench-settings-trigger" aria-haspopup="menu" aria-expanded={open} aria-controls="sidebar-settings-menu" onClick={() => setOpen(!open)} onKeyDown={(event) => { if (!open && event.key === 'ArrowDown') { event.preventDefault(); setOpen(true) } }}><Settings size={17} /><span>设置</span></button>
    {open && <div id="sidebar-settings-menu" role="menu" aria-label="设置与管理" className="workbench-settings-menu" onClick={() => setOpen(false)}><SettingsMenuItems isAdmin={isAdmin} pathname={location.pathname} /></div>}
  </div>
}
