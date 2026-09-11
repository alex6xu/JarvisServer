export type DocumentStatus = 'processing' | 'ready' | 'failed' | 'deleted' | string

export interface ProjectDocument {
  id: string
  project_id: string
  filename: string
  mime_type: string
  size_bytes: number
  sha256: string
  status: DocumentStatus
  extracted_bytes: number
  parser?: string
  parser_version?: string
  error_code?: string
  metadata_json?: string
  created_at: string
  updated_at: string
}

export interface DocumentLimits {
  upload_max_bytes: number
  project_max_bytes: number
  extracted_text_max_bytes: number
  allowed_extensions: string[]
}

export interface ProjectSummary {
  id: string
  name: string
  slug?: string
  description?: string
  source?: 'user' | 'workspace'
  status?: string
  linked_workspace_id?: string
  session_count?: number
  message_count?: number
  updated_at?: string
}

/** Stable metadata returned on restored messages and safe to keep in UI state. */
export type MessageDocument = Pick<
  ProjectDocument,
  'id' | 'project_id' | 'filename' | 'mime_type' | 'size_bytes' | 'status'
>
