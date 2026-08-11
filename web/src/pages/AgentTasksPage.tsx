import { useCallback, useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { apiFetch, useAccount } from '../context/AccountContext'

interface RouteProfile {
  id: number
  name: string
  purpose: 'coding' | 'documentation' | 'general'
  models: string[]
}

interface WorkspaceInfo {
  id: string
  name: string
}

interface AgentTask {
  id: string
  workspace_id: string
  route_profile_id: number
  type: 'code_change' | 'documentation'
  prompt: string
  status: 'queued' | 'running' | 'succeeded' | 'failed'
  result: string
  error: string
  tool_steps: { tool: string; args: string; result: string }[]
  created_at: string
  finished_at?: string
}

function errorMessage(data: unknown, fallback: string) {
  if (data && typeof data === 'object' && 'error' in data && typeof data.error === 'string') {
    return data.error
  }
  return fallback
}

function taskStatusClass(status: AgentTask['status']) {
  if (status === 'succeeded') return 'text-success bg-success/10'
  if (status === 'failed') return 'text-destructive bg-destructive/10'
  if (status === 'running') return 'text-primary bg-primary/10'
  return 'text-amber-600 bg-amber-500/10'
}

export default function AgentTasksPage() {
  const { currentAccount } = useAccount()
  const [profiles, setProfiles] = useState<RouteProfile[]>([])
  const [workspaces, setWorkspaces] = useState<WorkspaceInfo[]>([])
  const [tasks, setTasks] = useState<AgentTask[]>([])
  const [profileName, setProfileName] = useState('')
  const [profilePurpose, setProfilePurpose] = useState<RouteProfile['purpose']>('coding')
  const [profileModels, setProfileModels] = useState('')
  const [workspaceId, setWorkspaceId] = useState('')
  const [profileNameForTask, setProfileNameForTask] = useState('')
  const [taskType, setTaskType] = useState<AgentTask['type']>('code_change')
  const [prompt, setPrompt] = useState('')
  const [error, setError] = useState('')
  const [savingProfile, setSavingProfile] = useState(false)
  const [submittingTask, setSubmittingTask] = useState(false)

  const loadProfiles = useCallback(async () => {
    const response = await apiFetch('/v1/admin/route-profiles', {}, currentAccount?.id)
    if (!response.ok) throw new Error(errorMessage(await response.json().catch(() => null), 'Unable to load route profiles'))
    const data = await response.json()
    const list: RouteProfile[] = data.route_profiles || []
    setProfiles(list)
    setProfileNameForTask((previous) => previous || list[0]?.name || '')
  }, [currentAccount?.id])

  const loadWorkspaces = useCallback(async () => {
    const response = await apiFetch('/v1/workspaces', {}, currentAccount?.id)
    if (!response.ok) throw new Error(errorMessage(await response.json().catch(() => null), 'Unable to load workspaces'))
    const data = await response.json()
    const list: WorkspaceInfo[] = data.workspaces || []
    setWorkspaces(list)
    setWorkspaceId((previous) => (previous && list.some((workspace) => workspace.id === previous) ? previous : list[0]?.id || ''))
  }, [currentAccount?.id])

  const loadTasks = useCallback(async () => {
    const response = await apiFetch('/v1/agent/tasks', {}, currentAccount?.id)
    if (!response.ok) throw new Error(errorMessage(await response.json().catch(() => null), 'Unable to load agent tasks'))
    const data = await response.json()
    setTasks(data.tasks || [])
  }, [currentAccount?.id])

  const loadPage = useCallback(async () => {
    try {
      setError('')
      await Promise.all([loadProfiles(), loadWorkspaces(), loadTasks()])
    } catch (loadError) {
      setError(loadError instanceof Error ? loadError.message : 'Unable to load agent tasks')
    }
  }, [loadProfiles, loadTasks, loadWorkspaces])

  useEffect(() => {
    if (currentAccount) void loadPage()
  }, [currentAccount?.id, loadPage])

  useEffect(() => {
    if (!tasks.some((task) => task.status === 'queued' || task.status === 'running')) return
    const poll = window.setInterval(() => void loadTasks().catch(() => undefined), 3000)
    return () => window.clearInterval(poll)
  }, [loadTasks, tasks])

  const createProfile = async () => {
    const models = profileModels.split(',').map((model) => model.trim()).filter(Boolean)
    if (!profileName.trim() || models.length === 0) {
      setError('A profile name and at least one model are required.')
      return
    }
    setSavingProfile(true)
    setError('')
    try {
      const response = await apiFetch('/v1/admin/route-profiles', {
        method: 'POST',
        body: JSON.stringify({ name: profileName, purpose: profilePurpose, models }),
      }, currentAccount?.id)
      const data = await response.json().catch(() => null)
      if (!response.ok) throw new Error(errorMessage(data, 'Unable to create route profile'))
      setProfileName('')
      setProfileModels('')
      await loadProfiles()
    } catch (saveError) {
      setError(saveError instanceof Error ? saveError.message : 'Unable to create route profile')
    } finally {
      setSavingProfile(false)
    }
  }

  const submitTask = async () => {
    if (!workspaceId || !profileNameForTask || !prompt.trim()) {
      setError('Select a workspace and route profile, then describe the task.')
      return
    }
    setSubmittingTask(true)
    setError('')
    try {
      const response = await apiFetch('/v1/agent/tasks', {
        method: 'POST',
        body: JSON.stringify({
          workspace_id: workspaceId,
          route_profile: profileNameForTask,
          type: taskType,
          prompt,
        }),
      }, currentAccount?.id)
      const data = await response.json().catch(() => null)
      if (!response.ok) throw new Error(errorMessage(data, 'Unable to queue agent task'))
      setPrompt('')
      await loadTasks()
    } catch (submitError) {
      setError(submitError instanceof Error ? submitError.message : 'Unable to queue agent task')
    } finally {
      setSubmittingTask(false)
    }
  }

  return (
    <div className="p-6 space-y-6">
      <div className="flex items-center justify-between gap-4">
        <div>
          <h2 className="text-base font-semibold text-foreground">Agent Tasks</h2>
          <p className="text-[13px] text-muted-foreground mt-0.5">
            Queue work for {currentAccount?.username || 'the current account'} and monitor its progress. Tasks only change the selected cloud workspace.
          </p>
        </div>
        <button onClick={() => void loadPage()} className="h-9 px-3 text-[13px] text-muted-foreground border border-border rounded-lg hover:bg-accent transition-colors">
          Refresh
        </button>
      </div>

      {error && <div className="px-3 py-2 rounded-lg border border-destructive/30 bg-destructive/10 text-[13px] text-destructive">{error}</div>}

      <div className="grid gap-6 xl:grid-cols-2">
        <section className="bg-card border border-border rounded-xl p-5">
          <h3 className="text-sm font-semibold text-foreground">Route Profiles</h3>
          <p className="text-[12px] text-muted-foreground mt-1">Ordered model candidates only; provider credentials remain in Providers.</p>
          <div className="grid gap-3 mt-4 sm:grid-cols-2">
            <input value={profileName} onChange={(event) => setProfileName(event.target.value)} placeholder="Profile name" className="h-9 px-3 bg-background border border-border rounded-lg text-[13px]" />
            <select value={profilePurpose} onChange={(event) => setProfilePurpose(event.target.value as RouteProfile['purpose'])} className="h-9 px-3 bg-background border border-border rounded-lg text-[13px]">
              <option value="coding">Coding</option>
              <option value="documentation">Documentation</option>
              <option value="general">General</option>
            </select>
            <input value={profileModels} onChange={(event) => setProfileModels(event.target.value)} placeholder="Models, comma separated" className="h-9 px-3 bg-background border border-border rounded-lg text-[13px] sm:col-span-2" />
          </div>
          <button onClick={() => void createProfile()} disabled={savingProfile} className="mt-3 h-9 px-4 bg-primary text-primary-foreground rounded-lg text-[13px] font-medium disabled:opacity-50">
            {savingProfile ? 'Creating…' : 'Create Profile'}
          </button>
          <div className="mt-4 space-y-2">
            {profiles.length === 0 ? <p className="text-[12px] text-muted-foreground">No route profiles yet.</p> : profiles.map((profile) => (
              <div key={profile.id} className="border border-border rounded-lg px-3 py-2">
                <div className="flex items-center justify-between gap-2"><span className="text-[13px] font-medium text-foreground">{profile.name}</span><span className="text-[11px] text-muted-foreground">{profile.purpose}</span></div>
                <p className="mt-1 text-[12px] text-muted-foreground break-words">{profile.models.join(' → ')}</p>
              </div>
            ))}
          </div>
        </section>

        <section className="bg-card border border-border rounded-xl p-5">
          <h3 className="text-sm font-semibold text-foreground">Queue a Task</h3>
          <p className="text-[12px] text-muted-foreground mt-1">Tasks never publish, push, or create pull requests automatically.</p>
          <div className="space-y-3 mt-4">
            <select value={workspaceId} onChange={(event) => setWorkspaceId(event.target.value)} className="w-full h-9 px-3 bg-background border border-border rounded-lg text-[13px]">
              <option value="">Select a cloud workspace</option>
              {workspaces.map((workspace) => <option key={workspace.id} value={workspace.id}>{workspace.name}</option>)}
            </select>
            <select value={profileNameForTask} onChange={(event) => setProfileNameForTask(event.target.value)} className="w-full h-9 px-3 bg-background border border-border rounded-lg text-[13px]">
              <option value="">Select a route profile</option>
              {profiles.map((profile) => <option key={profile.id} value={profile.name}>{profile.name} ({profile.purpose})</option>)}
            </select>
            <select value={taskType} onChange={(event) => setTaskType(event.target.value as AgentTask['type'])} className="w-full h-9 px-3 bg-background border border-border rounded-lg text-[13px]">
              <option value="code_change">Code change</option>
              <option value="documentation">Documentation</option>
            </select>
            <textarea value={prompt} onChange={(event) => setPrompt(event.target.value)} rows={5} placeholder="Describe the change or documentation work to perform…" className="w-full px-3 py-2 bg-background border border-border rounded-lg text-[13px] resize-y" />
          </div>
          <button onClick={() => void submitTask()} disabled={submittingTask || !workspaces.length || !profiles.length} className="mt-3 h-9 px-4 bg-primary text-primary-foreground rounded-lg text-[13px] font-medium disabled:opacity-50">
            {submittingTask ? 'Queueing…' : 'Queue Task'}
          </button>
        </section>
      </div>

      <section className="bg-card border border-border rounded-xl overflow-hidden">
        <div className="px-5 py-4 border-b border-border"><h3 className="text-sm font-semibold text-foreground">Task History</h3></div>
        {tasks.length === 0 ? <p className="px-5 py-10 text-center text-[13px] text-muted-foreground">No tasks have been queued for this account.</p> : (
          <div className="divide-y divide-border">
            {tasks.map((task) => (
              <article key={task.id} className="px-5 py-4">
                <div className="flex flex-wrap items-center justify-between gap-2">
                  <div className="flex items-center gap-2"><span className="text-[13px] font-medium text-foreground">{task.type === 'code_change' ? 'Code change' : 'Documentation'}</span><span className={`px-2 py-0.5 rounded-md text-[11px] font-medium ${taskStatusClass(task.status)}`}>{task.status}</span></div>
                  <div className="flex items-center gap-3">
                    <Link to={`/coder?workspace=${encodeURIComponent(task.workspace_id)}`} className="text-[11px] text-primary hover:underline">Open workspace</Link>
                    <span className="text-[11px] text-muted-foreground">{new Date(task.created_at).toLocaleString()}</span>
                  </div>
                </div>
                <p className="mt-2 text-[13px] text-foreground whitespace-pre-wrap">{task.prompt}</p>
                {task.result && <pre className="mt-3 p-3 bg-background border border-border rounded-lg text-[12px] text-foreground whitespace-pre-wrap overflow-auto">{task.result}</pre>}
                {task.error && <p className="mt-3 text-[12px] text-destructive whitespace-pre-wrap">{task.error}</p>}
                {task.tool_steps?.length > 0 && (
                  <details className="mt-3 text-[12px] text-muted-foreground">
                    <summary className="cursor-pointer">Tool steps ({task.tool_steps.length})</summary>
                    <div className="mt-2 space-y-2">
                      {task.tool_steps.map((step, index) => (
                        <div key={`${task.id}-${index}`} className="p-3 bg-background border border-border rounded-lg">
                          <p className="font-medium text-foreground">{step.tool}</p>
                          {step.args && <pre className="mt-1 whitespace-pre-wrap overflow-auto">{step.args}</pre>}
                          {step.result && <pre className="mt-1 whitespace-pre-wrap overflow-auto">{step.result}</pre>}
                        </div>
                      ))}
                    </div>
                  </details>
                )}
              </article>
            ))}
          </div>
        )}
      </section>
    </div>
  )
}
