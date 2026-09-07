/** API SessionMeta.type is canonicalized by the server for legacy persisted headers. */
export type SidebarSession = { id: string; title?: string; type?: string; updated_at?: string }
export type ProjectSessions = Record<string, SidebarSession[]>

export function groupSidebarSessions(sessions: SidebarSession[], members: ProjectSessions) {
  const assigned = new Set(Object.values(members).flat().map((session) => session.id))
  const chats = (items: SidebarSession[]) => items.filter((session) => session.type === 'chat')
    .slice().sort((a, b) => (b.updated_at || '').localeCompare(a.updated_at || ''))
  return {
    recent: chats(sessions).filter((session) => !assigned.has(session.id)).slice(0, 12),
    projects: Object.fromEntries(Object.entries(members).map(([id, items]) => [id, chats(items)])),
  }
}

export function chatHref(id: string) {
  return `/?${new URLSearchParams({ session: id })}`
}
