/** API SessionMeta.type is canonicalized by the server for legacy persisted headers. */
export type SidebarSession = { id: string; title?: string; type?: string; updated_at?: string }
export type ProjectSessions = Record<string, SidebarSession[]>

export function groupSidebarSessions(sessions: SidebarSession[], members: ProjectSessions) {
  const assigned = new Set(Object.values(members).flat().map((session) => session.id))
  const updatedAt = (session: SidebarSession) => {
    const timestamp = Date.parse(session.updated_at || '')
    return Number.isFinite(timestamp) ? timestamp : -Infinity
  }
  const chats = (items: SidebarSession[]) => items.filter((session) => session.type === 'chat')
    .slice().sort((a, b) => {
      const first = updatedAt(a), second = updatedAt(b)
      return first === second ? 0 : first > second ? -1 : 1
    })
  return {
    recent: chats(sessions).filter((session) => !assigned.has(session.id)).slice(0, 12),
    projects: Object.fromEntries(Object.entries(members).map(([id, items]) => [id, chats(items)])),
  }
}

export function chatHref(id: string) {
  return `/?${new URLSearchParams({ session: id })}`
}
