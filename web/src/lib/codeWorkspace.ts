import type { ProjectSummary } from '../types/documents'

/** Explicit navigation never falls back to another project's saved directory. */
export function resolveWorkspace(opts: {
  blank: boolean; projectId: string; workspaceId: string; sessionWorkspace: string
  savedWorkspace: string; projects: ProjectSummary[]; workspaces: { id: string }[]
}): string {
  if (opts.blank) return ''
  const project = opts.projects.find((item) => item.id === opts.projectId)
  if (opts.projectId && !project) throw new Error('项目不存在或无权访问')
  const linked = project?.linked_workspace_id || ''
  const explicit = opts.sessionWorkspace || opts.workspaceId || linked
  if (opts.sessionWorkspace && opts.workspaceId && opts.sessionWorkspace !== opts.workspaceId) throw new Error('会话与工作目录不匹配')
  if (linked && explicit && linked !== explicit) throw new Error('项目与工作目录不匹配，请从全部会话打开历史')
  if (explicit) {
    if (!opts.workspaces.some((item) => item.id === explicit)) throw new Error('工作目录不存在或无权访问，请重新选择或导入')
    return explicit
  }
  if (opts.projectId) return ''
  return opts.workspaces.some((item) => item.id === opts.savedWorkspace) ? opts.savedWorkspace : ''
}
