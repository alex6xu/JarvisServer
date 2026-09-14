export type TipType = 'idea' | 'todo' | 'question' | 'note'
export type TipStatus = 'inbox' | 'planned' | 'doing' | 'done' | 'archived'

export interface TipRun {
  id: string
  mode: 'analyze' | 'execute'
  session_mode: 'chat' | 'coder'
  workspace_id?: string
  session_id?: string
  run_id?: string
  status: string
  error?: string
  created_at: string
  snapshot: ProjectTip
}

export interface ProjectTip {
  runs?: TipRun[]
  id: string
  project_id: string
  type: TipType
  status: TipStatus
  title?: string
  content: string
  priority: number
  source: string
  due_at?: string
  completed_at?: string
  position: number
  version: number
  created_at: string
  updated_at: string
  archived_at?: string
}
