import { useCallback, useEffect, useState } from 'react'
import { AlertCircle, CheckCircle2, Download, Loader2, Plug, RefreshCw, Trash2 } from 'lucide-react'
import { apiFetch } from '../context/AccountContext'

interface PluginSummary {
  id: string
  name?: string
  package?: string
  version?: string
  enabled: boolean
  status: 'ready' | 'disabled' | 'load_error' | string
  tools: Array<{ name: string; description?: string }>
  commands: string[]
  events: string[]
  last_error?: string
}

async function responseError(response: Response, fallback: string) {
  const body = await response.json().catch(() => ({}))
  return typeof body.error === 'string' && body.error ? body.error : fallback
}

export default function PluginSettings({ accountId, isAdmin }: { accountId?: number; isAdmin: boolean }) {
  const [plugins, setPlugins] = useState<PluginSummary[]>([])
  const [directory, setDirectory] = useState('')
  const [loading, setLoading] = useState(true)
  const [busyId, setBusyId] = useState('')
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')
  const [installRef, setInstallRef] = useState('')

  const load = useCallback(async () => {
    if (!accountId || !isAdmin) {
      setPlugins([])
      setLoading(false)
      return
    }
    setLoading(true)
    setError('')
    try {
      const response = await apiFetch('/v1/admin/plugins', {}, accountId)
      if (!response.ok) throw new Error(await responseError(response, '无法读取插件'))
      const body = await response.json() as { plugins?: PluginSummary[]; directory?: string }
      setPlugins(body.plugins || [])
      setDirectory(body.directory || '')
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : '无法读取插件')
    } finally {
      setLoading(false)
    }
  }, [accountId, isAdmin])

  useEffect(() => { void load() }, [load])
  if (!isAdmin) return null

  const setEnabled = async (plugin: PluginSummary, enabled: boolean) => {
    setBusyId(plugin.id)
    setError('')
    setMessage('')
    try {
      const response = await apiFetch(`/v1/admin/plugins/${encodeURIComponent(plugin.id)}/status`, {
        method: 'PUT', body: JSON.stringify({ enabled }),
      }, accountId)
      if (!response.ok) throw new Error(await responseError(response, '更新插件状态失败'))
      const body = await response.json() as { plugin?: PluginSummary }
      if (body.plugin) setPlugins((current) => current.map((item) => item.id === plugin.id ? body.plugin! : item))
      setMessage(`插件已${enabled ? '启用' : '停用'}，下一次对话运行生效`)
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : '更新插件状态失败')
    } finally {
      setBusyId('')
    }
  }

  const install = async () => {
    const reference = installRef.trim()
    if (!reference) return
    setBusyId('__install__')
    setError('')
    setMessage('')
    try {
      const response = await apiFetch('/v1/admin/plugins/install', {
        method: 'POST', body: JSON.stringify({ reference }),
      }, accountId)
      if (!response.ok) throw new Error(await responseError(response, '安装插件失败'))
      const body = await response.json() as { plugins?: PluginSummary[]; directory?: string; package?: { name?: string; version?: string } }
      setPlugins(body.plugins || [])
      if (body.directory) setDirectory(body.directory)
      setInstallRef('')
      setMessage(`已安装 ${body.package?.name || reference}${body.package?.version ? `@${body.package.version}` : ''}，下一次对话运行生效`)
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : '安装插件失败')
    } finally {
      setBusyId('')
    }
  }

  const uninstall = async (plugin: PluginSummary) => {
    if (!plugin.package || !window.confirm(`确定卸载插件包 “${plugin.package}”？`)) return
    setBusyId(plugin.id)
    setError('')
    setMessage('')
    try {
      const response = await apiFetch('/v1/admin/plugins/package', {
        method: 'DELETE', body: JSON.stringify({ package: plugin.package }),
      }, accountId)
      if (!response.ok) throw new Error(await responseError(response, '卸载插件失败'))
      const body = await response.json() as { plugins?: PluginSummary[]; directory?: string }
      setPlugins(body.plugins || [])
      if (body.directory) setDirectory(body.directory)
      setMessage(`已卸载 ${plugin.package}，下一次对话运行生效`)
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : '卸载插件失败')
    } finally {
      setBusyId('')
    }
  }

  const reload = async () => {
    setBusyId('__reload__')
    setError('')
    setMessage('')
    try {
      const response = await apiFetch('/v1/admin/plugins/reload', { method: 'POST' }, accountId)
      if (!response.ok) throw new Error(await responseError(response, '重新加载插件失败'))
      const body = await response.json() as { plugins?: PluginSummary[]; loaded?: number; failed?: number; directory?: string }
      setPlugins(body.plugins || [])
      if (body.directory) setDirectory(body.directory)
      setMessage(`插件检查完成：可用 ${body.loaded || 0}，失败 ${body.failed || 0}；下一次对话运行生效`)
    } catch (reason) {
      setError(reason instanceof Error ? reason.message : '重新加载插件失败')
    } finally {
      setBusyId('')
    }
  }

  return (
    <section className="overflow-hidden rounded-lg border border-border bg-card">
      <div className="flex items-start justify-between gap-3 p-5">
        <div className="min-w-0">
          <div className="flex items-center gap-2"><Plug className="h-4 w-4 text-primary" /><h3 className="text-sm font-semibold">Plugins</h3></div>
          <p className="mt-1 text-[11px] text-muted-foreground">外部插件以独立 JSON-RPC 进程运行；只安装可信来源。</p>
          {directory && <p className="mt-1 truncate text-[10px] text-muted-foreground/70">{directory}</p>}
        </div>
        <button type="button" title="重新检查插件" disabled={busyId !== ''} onClick={() => void reload()} className="inline-flex h-8 w-8 items-center justify-center rounded-md border border-border text-muted-foreground hover:bg-accent disabled:opacity-50">
          <RefreshCw className={`h-3.5 w-3.5 ${busyId === '__reload__' ? 'animate-spin' : ''}`} />
        </button>
      </div>
      <div className="mx-5 mb-4 rounded-md border border-border bg-background/50 p-3">
        <label className="mb-1.5 block text-[11px] font-medium text-foreground">从 npm 安装插件</label>
        <div className="flex gap-2">
          <input
            value={installRef}
            onChange={(event) => setInstallRef(event.target.value)}
            onKeyDown={(event) => { if (event.key === 'Enter') { event.preventDefault(); void install() } }}
            placeholder="npm:package-name@1.2.3"
            disabled={busyId !== ''}
            className="h-9 min-w-0 flex-1 rounded-md border border-border bg-card px-3 text-[12px] outline-none focus:ring-2 focus:ring-ring disabled:opacity-50"
          />
          <button type="button" onClick={() => void install()} disabled={busyId !== '' || !installRef.trim()} className="inline-flex h-9 items-center gap-1.5 rounded-md bg-primary px-3 text-[12px] font-medium text-primary-foreground disabled:opacity-50">
            {busyId === '__install__' ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Download className="h-3.5 w-3.5" />}安装
          </button>
        </div>
        <p className="mt-1.5 text-[10px] text-muted-foreground">必须指定明确版本；安装会执行插件代码进行初始化校验，失败自动回滚。</p>
      </div>
      {error && <div className="mx-5 mb-4 flex items-start gap-2 text-[12px] text-red-500"><AlertCircle className="mt-0.5 h-3.5 w-3.5 shrink-0" /><span className="break-all">{error}</span></div>}
      {message && <div className="mx-5 mb-4 flex items-start gap-2 text-[12px] text-green-500"><CheckCircle2 className="mt-0.5 h-3.5 w-3.5 shrink-0" /><span>{message}</span></div>}
      <div className="border-t border-border">
        {loading ? <div className="flex h-24 items-center justify-center"><Loader2 className="h-4 w-4 animate-spin text-muted-foreground" /></div>
          : plugins.length === 0 ? <p className="px-5 py-8 text-center text-[12px] text-muted-foreground">插件目录中没有可执行插件</p>
            : <div className="divide-y divide-border">{plugins.map((plugin) => {
              const busy = busyId === plugin.id
              const label = plugin.name || plugin.id
              return <div key={plugin.id} className="flex flex-col gap-3 px-5 py-4 sm:flex-row sm:items-start">
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-center gap-2"><span className="text-[13px] font-medium">{label}</span>{plugin.version && <span className="text-[10px] text-muted-foreground">v{plugin.version}</span>}<span className={`rounded px-1.5 py-0.5 text-[9px] uppercase ${plugin.status === 'ready' ? 'bg-green-500/10 text-green-500' : plugin.status === 'load_error' ? 'bg-red-500/10 text-red-500' : 'bg-muted text-muted-foreground'}`}>{plugin.status}</span></div>
                  <p className="mt-1 text-[10px] text-muted-foreground">ID: {plugin.id}</p>
                  {plugin.tools.length > 0 && <p className="mt-1 text-[10px] text-muted-foreground">Tools: {plugin.tools.map((tool) => tool.name).join(', ')}</p>}
                  {(plugin.commands.length > 0 || plugin.events.length > 0) && <p className="mt-1 text-[10px] text-muted-foreground">Commands: {plugin.commands.length} · Events: {plugin.events.length}</p>}
                  {plugin.last_error && <p className="mt-2 break-all rounded bg-red-500/10 px-2 py-1.5 text-[10px] text-red-500">{plugin.last_error}</p>}
                </div>
                <div className="flex shrink-0 items-center gap-2">
                  <label className="flex items-center gap-2 text-[11px] text-muted-foreground">
                    <input type="checkbox" checked={plugin.enabled} disabled={busy || busyId !== ''} onChange={(event) => void setEnabled(plugin, event.target.checked)} className="h-4 w-4 accent-primary" />启用
                  </label>
                  {plugin.package && <button type="button" title={`卸载 ${plugin.package}`} disabled={busy || busyId !== ''} onClick={() => void uninstall(plugin)} className="inline-flex h-7 w-7 items-center justify-center rounded-md text-muted-foreground hover:bg-destructive/10 hover:text-destructive disabled:opacity-50"><Trash2 className="h-3.5 w-3.5" /></button>}
                </div>
              </div>
            })}</div>}
      </div>
    </section>
  )
}
