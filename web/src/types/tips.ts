export type TipType = 'idea' | 'todo' | 'question' | 'note'
export type TipStatus = 'inbox' | 'planned' | 'doing' | 'done' | 'archived'

export interface ProjectTip {
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
