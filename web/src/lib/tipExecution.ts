import type { TipRun } from '../types/tips'
import { chatSessionKey, coderSessionKey, coderWorkspaceKey, writeLocal } from './sessionPersist'

export function tipRunLocation(run: TipRun): string | undefined {
  if (!run.session_id) return undefined
  if (run.session_mode === 'coder' && run.workspace_id) {
    return `/code?workspace=${encodeURIComponent(run.workspace_id)}&session=${encodeURIComponent(run.session_id)}`
  }
  return `/?session=${encodeURIComponent(run.session_id)}`
}

export function restoreTipRun(accountId: number, run: TipRun): string | undefined {
  const location = tipRunLocation(run)
  if (!location || !run.session_id) return undefined
  if (run.session_mode === 'coder' && run.workspace_id) {
    writeLocal(coderWorkspaceKey(accountId), run.workspace_id)
    writeLocal(coderSessionKey(accountId, run.workspace_id), run.session_id)
  } else {
    writeLocal(chatSessionKey(accountId), run.session_id)
  }
  return location
}

export function tipHasActiveRun(runs: TipRun[] = []): boolean {
  return runs.some((run) => run.status === 'starting' || run.status === 'running')
}
