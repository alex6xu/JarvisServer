import { ChevronDown, Folder } from 'lucide-react'
import type { ProjectSummary } from '../types/documents'
import { chatHref, type SidebarSession } from '../lib/sidebarSessions'

export default function SidebarProject({ project, sessions, expanded, onToggle, currentSession, search = '' }: {
  project: ProjectSummary; sessions: SidebarSession[]; expanded: boolean; onToggle: () => void; currentSession: string; search?: string
}) {
  const visible = sessions.filter((session) => (session.title || session.id).toLocaleLowerCase().includes(search.toLocaleLowerCase()))
  const panelId = `project-chats-${project.id}`
  return <div className="workbench-project">
    <div className="workbench-project-row">
      <button type="button" aria-label={`${expanded ? '收起' : '展开'}项目 ${project.name}`} aria-expanded={expanded} aria-controls={panelId} onClick={onToggle}><ChevronDown size={16} className={expanded ? '' : 'is-collapsed'} /></button>
      <a href={`/?new=1&project=${encodeURIComponent(project.id)}`} className="workbench-nav-item" title={project.name}><Folder size={16} /><span className="truncate">{project.name}</span></a>
    </div>
    {expanded && <div id={panelId} className="workbench-project-chats">
      {visible.map((session) => <a key={session.id} href={chatHref(session.id)} className="workbench-nav-item workbench-session" aria-current={currentSession === session.id ? 'page' : undefined} title={session.title || session.id}><span className="truncate">{session.title || session.id}</span></a>)}
      {!visible.length && <p className="workbench-empty">{sessions.length ? '没有匹配对话' : '暂无对话'}</p>}
    </div>}
  </div>
}
