/** API SessionMeta.type is canonicalized by the server for legacy persisted headers. */
export type SidebarSession = { id: string; title?: string; type?: string; updated_at?: string; workspace_id?: string }
export type ProjectSessions = Record<string, SidebarSession[]>

export function groupSidebarSessions(sessions: SidebarSession[], members: ProjectSessions) {
  const assigned = new Set(Object.values(members).flat().map((session) => session.id))
  const updatedAt = (session: SidebarSession) => {
    const timestamp = Date.parse(session.updated_at || '')
    return Number.isFinite(timestamp) ? timestamp : -Infinity
  }
  const sorted = (items: SidebarSession[]) => items
    .slice().sort((a, b) => {
      const first = updatedAt(a), second = updatedAt(b)
      return first === second ? 0 : first > second ? -1 : 1
    })
  return {
    recent: sorted(sessions.filter((session) => session.type === 'chat')).filter((session) => !assigned.has(session.id)).slice(0, 12),
    projects: Object.fromEntries(Object.entries(members).map(([id, items]) => [id, sorted(items.filter((session) => session.type === 'chat' || session.type === 'code'))])),
  }
}

export function chatHref(id: string) {
  return `/?${new URLSearchParams({ session: id })}`
}

export function projectHref(projectId: string) {
  return `/code?${new URLSearchParams({ project: projectId })}`
}

export function projectSessionHref(session: SidebarSession, projectId: string) {
  if (session.type !== 'code') return chatHref(session.id)
  const params = new URLSearchParams({ project: projectId, session: session.id })
  if (session.workspace_id) params.set('workspace', session.workspace_id)
  return `/code?${params}`
}
