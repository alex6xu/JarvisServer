import { useState, useEffect, useRef } from 'react'
import { Link } from 'react-router-dom'
import { apiFetch, useAccount } from '../context/AccountContext'

interface Channel {
  id: number
  name: string
  type: number
  key: string
  base_url: string
  models: string
  status: number
  weight: number
  priority: number
  is_default: number
  auth_mode?: string
}

type ProviderMode = 'api_key' | 'oauth'

const CHANNEL_TYPE_SLUGS: Record<number, string> = {
  1: 'openai',
  2: 'claude',
  3: 'gemini',
  4: 'deepseek',
  5: 'ollama',
  6: 'mimo',
  9: 'agnes',
  10: 'glm',
  99: 'custom',
}

function typeSlug(type: number): string {
  return CHANNEL_TYPE_SLUGS[type] || 'custom'
}

function firstModel(models: string): string {
  const raw = models.trim()
  if (!raw) return ''
  if (raw.startsWith('[')) {
    try {
      const parsed = JSON.parse(raw)
      if (Array.isArray(parsed) && parsed.length > 0) {
        return String(parsed[0]).trim().toLowerCase()
      }
    } catch {
      // fall through to comma parsing
    }
  }
  const first = raw.split(',')[0]?.trim() || ''
  return first.toLowerCase()
}

function defaultChannelName(type: number, models: string): string {
  const slug = typeSlug(type)
  const model = firstModel(models)
  return model ? `${slug}/${model}` : slug
}

const channelTypes: Record<number, { name: string; color: string }> = {
  1: { name: 'OpenAI', color: 'text-green-400 bg-green-500/10' },
  2: { name: 'Claude', color: 'text-purple-400 bg-purple-500/10' },
  3: { name: 'Gemini', color: 'text-blue-400 bg-blue-500/10' },
  4: { name: 'DeepSeek', color: 'text-cyan-400 bg-cyan-500/10' },
  5: { name: 'Ollama', color: 'text-amber-400 bg-amber-500/10' },
  6: { name: 'MiMo', color: 'text-orange-400 bg-orange-500/10' },
  9: { name: 'Agnes', color: 'text-teal-400 bg-teal-500/10' },
  10: { name: 'GLM', color: 'text-sky-400 bg-sky-500/10' },
  99: { name: 'Custom', color: 'text-gray-400 bg-gray-500/10' },
}

const defaultBaseURLs: Record<number, string> = {
  1: 'https://api.openai.com/v1',
  2: 'https://api.anthropic.com',
  3: 'https://generativelanguage.googleapis.com/v1beta',
  4: 'https://api.deepseek.com/v1',
  6: 'https://api.xiaomimimo.com/v1',
  9: 'https://apihub.agnes-ai.com/v1',
  10: 'https://open.bigmodel.cn/api/paas/v4',
}

const channelTypeOptions = [
  { value: 1, label: 'OpenAI' },
  { value: 2, label: 'Claude' },
  { value: 3, label: 'Gemini' },
  { value: 4, label: 'DeepSeek' },
  { value: 5, label: 'Ollama' },
  { value: 6, label: 'MiMo (API)' },
  { value: 9, label: 'Agnes (OpenAI 兼容)' },
  { value: 10, label: 'GLM / 智谱 (OpenAI 兼容)' },
  { value: 99, label: 'Custom' },
]

type Tab = 'endpoints' | 'providers' | 'request-logs'

export default function ChannelsPage() {
  const { currentAccount } = useAccount()
  const [tab, setTab] = useState<Tab>('endpoints')

  return (
    <div className="p-6">
      <div className="flex items-center justify-between mb-6">
        <div>
          <h2 className="text-base font-semibold text-foreground">网关管理</h2>
          <p className="text-[13px] text-muted-foreground mt-0.5">
            Manage gateway endpoints and providers for {currentAccount?.username || 'current account'}
          </p>
        </div>
      </div>

      {/* Tabs */}
      <div className="flex gap-1 mb-5 border-b border-border">
        <TabButton active={tab === 'endpoints'} onClick={() => setTab('endpoints')}>端点管理</TabButton>
        <TabButton active={tab === 'providers'} onClick={() => setTab('providers')}>提供商</TabButton>
        <TabButton active={tab === 'request-logs'} onClick={() => setTab('request-logs')}>请求日志</TabButton>
      </div>

      {tab === 'endpoints' && <EndpointManager accountId={currentAccount?.id} />}
      {tab === 'providers' && <ProvidersPage accountId={currentAccount?.id} />}
      {tab === 'request-logs' && <RequestLogsPage accountId={currentAccount?.id} />}
    </div>
  )
}

function TabButton({ active, onClick, children }: { active: boolean; onClick: () => void; children: React.ReactNode }) {
  return (
    <button
      onClick={onClick}
      className={`px-4 py-2.5 text-[13px] font-medium border-b-2 -mb-px transition-colors ${
        active
          ? 'border-primary text-foreground'
          : 'border-transparent text-muted-foreground hover:text-foreground'
      }`}
    >
      {children}
    </button>
  )
}

// ============ Endpoint Manager ============

interface ApiKey {
  id: number
  name: string
  key: string
  status: number
  unlimited_quota: boolean
  created_at: string
}

function EndpointManager({ accountId }: { accountId?: number }) {
  const gatewayURL = `${window.location.origin}/v1`
  const [keys, setKeys] = useState<ApiKey[]>([])
  const [newKey, setNewKey] = useState<string | null>(null)
  const [creating, setCreating] = useState(false)
  const [copied, setCopied] = useState<string | null>(null)

  const fetchKeys = async () => {
    try {
      const res = await apiFetch('/v1/admin/tokens', {}, accountId)
      if (res.ok) {
        const data = await res.json()
        setKeys(data.tokens || [])
      }
    } catch (e) {
      console.error('Failed to fetch tokens:', e)
    }
  }

  useEffect(() => {
    if (accountId) fetchKeys()
  }, [accountId])

  const copy = (text: string, tag: string) => {
    navigator.clipboard?.writeText(text)
    setCopied(tag)
    setTimeout(() => setCopied(null), 1500)
  }

  const handleCreate = async () => {
    setCreating(true)
    try {
      const res = await apiFetch('/v1/admin/tokens', { method: 'POST', body: JSON.stringify({ name: 'api-key' }) }, accountId)
      if (res.ok) {
        const data = await res.json()
        setNewKey(data.key)
        fetchKeys()
      } else {
        const data = await res.json().catch(() => ({}))
        alert(data.error || '生成失败')
      }
    } catch (e) {
      console.error('Failed to create token:', e)
    } finally {
      setCreating(false)
    }
  }

  const handleToggle = async (k: ApiKey) => {
    try {
      const res = await apiFetch(`/v1/admin/tokens/${k.id}`, {
        method: 'PUT',
        body: JSON.stringify({ status: k.status === 1 ? 0 : 1 }),
      }, accountId)
      if (res.ok) fetchKeys()
    } catch (e) {
      console.error('Failed to update token:', e)
    }
  }

  const handleDelete = async (id: number) => {
    if (!confirm('确认删除该 API Key？删除后使用该 Key 的客户端将无法连接。')) return
    try {
      const res = await apiFetch(`/v1/admin/tokens/${id}`, { method: 'DELETE' }, accountId)
      if (res.ok) fetchKeys()
    } catch (e) {
      console.error('Failed to delete token:', e)
    }
  }

  return (
    <div className="space-y-6">
      {/* Current gateway URL */}
      <div className="bg-card border border-border rounded-xl p-5">
        <p className="text-[13px] font-medium text-foreground mb-1">当前网关地址</p>
        <p className="text-[12px] text-muted-foreground mb-3">
          客户端（Claude Code / 各类 IDE 插件）使用该地址连接到本代理，并配合下方的 API Key 鉴权。
        </p>
        <div className="flex items-center gap-2">
          <code className="flex-1 px-3 py-2 bg-background border border-border rounded-lg text-[13px] font-mono text-foreground">
            {gatewayURL}
          </code>
          <button
            onClick={() => copy(gatewayURL, 'url')}
            className="h-9 px-3 border border-border rounded-lg text-[13px] text-muted-foreground hover:text-foreground hover:bg-accent"
          >
            {copied === 'url' ? '已复制' : '复制'}
          </button>
        </div>
      </div>

      {/* API Keys */}
      <div className="bg-card border border-border rounded-xl">
        <div className="flex items-center justify-between px-5 py-4 border-b border-border">
          <div>
            <p className="text-[13px] font-medium text-foreground">API Key 列表</p>
            <p className="text-[12px] text-muted-foreground mt-0.5">
              客户端用 <code className="text-[11px]">Authorization: Bearer &lt;API Key&gt;</code> 或{' '}
              <code className="text-[11px]">X-API-Key</code>；Web 登录态的 Session Token 也可访问网关，二者任一即可。
            </p>
          </div>
          <button
            onClick={handleCreate}
            disabled={creating}
            className="h-9 px-4 bg-primary text-primary-foreground rounded-lg text-[13px] font-medium hover:bg-primary/90 disabled:opacity-50 flex items-center gap-2"
          >
            <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
              <line x1="12" y1="5" x2="12" y2="19" />
              <line x1="5" y1="12" x2="19" y2="12" />
            </svg>
            新增 API Key
          </button>
        </div>

        {newKey && (
          <div className="px-5 py-4 bg-primary/5 border-b border-border">
            <p className="text-[12px] font-medium text-primary mb-2">新生成的 API Key（仅展示一次，请妥善保存）：</p>
            <div className="flex items-center gap-2">
              <code className="flex-1 px-3 py-2 bg-background border border-primary/30 rounded-lg text-[13px] font-mono text-foreground break-all">
                {newKey}
              </code>
              <button
                onClick={() => copy(newKey, 'newkey')}
                className="h-9 px-3 border border-primary/30 text-primary rounded-lg text-[13px] hover:bg-primary/10"
              >
                {copied === 'newkey' ? '已复制' : '复制'}
              </button>
            </div>
          </div>
        )}

        <table className="w-full">
          <thead>
            <tr className="border-b border-border">
              <th className="px-5 py-3 text-left text-[12px] font-medium text-muted-foreground">ID</th>
              <th className="px-5 py-3 text-left text-[12px] font-medium text-muted-foreground">名称</th>
              <th className="px-5 py-3 text-left text-[12px] font-medium text-muted-foreground">Key</th>
              <th className="px-5 py-3 text-left text-[12px] font-medium text-muted-foreground">配额</th>
              <th className="px-5 py-3 text-left text-[12px] font-medium text-muted-foreground">状态</th>
              <th className="px-5 py-3 text-left text-[12px] font-medium text-muted-foreground">创建时间</th>
              <th className="px-5 py-3 text-left text-[12px] font-medium text-muted-foreground">操作</th>
            </tr>
          </thead>
          <tbody>
            {keys.length === 0 ? (
              <tr>
                <td colSpan={7} className="px-5 py-12 text-center">
                  <p className="text-[13px] text-muted-foreground">暂无 API Key，点击「新增 API Key」生成一个。</p>
                </td>
              </tr>
            ) : (
              keys.map((k) => (
                <tr key={k.id} className="border-b border-border hover:bg-accent/50">
                  <td className="px-5 py-3 text-[13px] text-muted-foreground tabular-nums">{k.id}</td>
                  <td className="px-5 py-3 text-[13px] text-foreground font-medium">{k.name}</td>
                  <td className="px-5 py-3 text-[13px] text-muted-foreground font-mono">
                    {k.key}
                  </td>
                  <td className="px-5 py-3 text-[13px] text-muted-foreground">
                    {k.unlimited_quota ? '无限制' : '有限'}
                  </td>
                  <td className="px-5 py-3">
                    <span className={`inline-flex items-center gap-1.5 text-[12px] font-medium ${k.status === 1 ? 'text-success' : 'text-destructive'}`}>
                      <span className={`w-1.5 h-1.5 rounded-full ${k.status === 1 ? 'bg-success' : 'bg-destructive'}`}></span>
                      {k.status === 1 ? '启用' : '停用'}
                    </span>
                  </td>
                  <td className="px-5 py-3 text-[13px] text-muted-foreground">
                    {new Date(k.created_at).toLocaleString()}
                  </td>
                  <td className="px-5 py-3">
                    <div className="flex items-center gap-2">
                      <button
                        onClick={() => handleToggle(k)}
                        className="text-[13px] text-primary hover:text-primary/80 font-medium"
                      >
                        {k.status === 1 ? '停用' : '启用'}
                      </button>
                      <button
                        onClick={() => handleDelete(k.id)}
                        className="text-[13px] text-destructive hover:text-destructive/80 font-medium"
                      >
                        删除
                      </button>
                    </div>
                  </td>
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>
    </div>
  )
}

// ============ Providers Page (merged: API Key + OAuth) ============

function ProvidersPage({ accountId }: { accountId?: number }) {
  const [channels, setChannels] = useState<Channel[]>([])
  const [claudeConfigured, setClaudeConfigured] = useState(false)
  const [claudeConnected, setClaudeConnected] = useState(false)
  const [claudeBusy, setClaudeBusy] = useState(false)
  const [claudeHint, setClaudeHint] = useState('')
  const [claudePaste, setClaudePaste] = useState('')

  // modal state
  const [modalMode, setModalMode] = useState<ProviderMode>('api_key')
  const [editingId, setEditingId] = useState<number | null>(null)
  const [showModal, setShowModal] = useState(false)
  const [nameTouched, setNameTouched] = useState(false)
  const nameTouchedRef = useRef(false)
  // oauth connect step inside modal
  const [oauthStep, setOauthStep] = useState<'connect' | 'form'>('connect')
  const [oauthHint, setOauthHint] = useState('')
  const [oauthBusy, setOauthBusy] = useState(false)
  const [oauthPaste, setOauthPaste] = useState('')
  const [form, setForm] = useState({
    name: '',
    type: 1,
    key: '',
    base_url: defaultBaseURLs[1],
    models: '',
    weight: 1,
    priority: 0,
    is_default: 0,
    auth_mode: 'api_key' as string,
  })
  const [fetchingModels, setFetchingModels] = useState(false)
  const [availableModels, setAvailableModels] = useState<string[]>([])
  const [modelPick, setModelPick] = useState('')

  const fetchChannels = async () => {
    try {
      const res = await apiFetch('/v1/admin/providers', {}, accountId)
      if (res.ok) {
        const data = await res.json()
        setChannels(data.providers || [])
      }
    } catch (e) {
      console.error('Failed to fetch providers:', e)
    }
  }

  const fetchClaudeStatus = async () => {
    try {
      const res = await apiFetch('/v1/claude/oauth/status', {}, accountId)
      if (!res.ok) return
      const data = await res.json()
      setClaudeConfigured(!!data.configured)
      setClaudeConnected(!!data.connected)
    } catch {
      // ignore
    }
  }

  useEffect(() => {
    if (accountId) {
      fetchChannels()
      fetchClaudeStatus()
    }
  }, [accountId])

  const apiKeyChannels = channels.filter((c) => (c.auth_mode || 'api_key') !== 'oauth')
  // API Key 兼容提供商再按协议拆分为 OpenAI 兼容（type!=2）与 Claude 兼容（type==2）
  const openaiCompatibleChannels = apiKeyChannels.filter((c) => c.type !== 2)
  const claudeCompatibleChannels = apiKeyChannels.filter((c) => c.type === 2)
  const oauthChannels = channels.filter((c) => (c.auth_mode || '') === 'oauth')

  const syncAutoName = (next: typeof form, touched = nameTouchedRef.current) => {
    if (touched) return next
    return { ...next, name: defaultChannelName(next.type, next.models) }
  }

  const resetForm = (defaultType: number = 1) => {
    nameTouchedRef.current = false
    setNameTouched(false)
    setAvailableModels([])
    setModelPick('')
    setForm(syncAutoName({
      name: '',
      type: defaultType,
      key: '',
      base_url: defaultBaseURLs[defaultType],
      models: '',
      weight: 1,
      priority: 0,
      is_default: 0,
      auth_mode: modalMode,
    }, false))
  }

  const openAdd = (mode: ProviderMode, defaultType?: number) => {
    setModalMode(mode)
    setEditingId(null)
    resetForm(defaultType)
    setOauthPaste('')
    setOauthHint('')
    setOauthBusy(false)
    // 新增订阅 OAuth 提供商时，若未连接则先走 Claude 订阅连接步骤
    setOauthStep(mode === 'oauth' && !claudeConnected ? 'connect' : 'form')
    setShowModal(true)
  }

  const openEdit = (channel: Channel) => {
    const mode: ProviderMode = channel.auth_mode === 'oauth' ? 'oauth' : 'api_key'
    setModalMode(mode)
    nameTouchedRef.current = true
    setNameTouched(true)
    setAvailableModels([])
    setModelPick('')
    setForm({
      name: channel.name,
      type: channel.type === 7 ? 6 : channel.type,
      key: channel.key,
      base_url: channel.base_url,
      models: channel.models,
      weight: channel.weight,
      priority: channel.priority,
      is_default: channel.is_default,
      auth_mode: mode,
    })
    setOauthStep('form')
    setOauthPaste('')
    setOauthHint('')
    setEditingId(channel.id)
    setShowModal(true)
  }

  const oauthStartConnect = async () => {
    setOauthBusy(true)
    setOauthHint('')
    try {
      const res = await apiFetch('/v1/claude/oauth/authorize?mode=paste', {}, accountId)
      const data = await res.json()
      if (!res.ok) {
        setOauthHint(data.error || '无法开始授权')
        return
      }
      window.open(data.authorize_url, '_blank', 'noopener,noreferrer')
      setOauthHint('已在新标签页打开授权。请复制地址栏中的 code#state 粘贴到下方并提交。')
    } catch {
      setOauthHint('网络错误')
    } finally {
      setOauthBusy(false)
    }
  }

  const oauthDoConnect = async () => {
    setOauthHint('')
    setOauthBusy(true)
    try {
      const res = await apiFetch('/v1/claude/oauth/exchange', {
        method: 'POST',
        body: JSON.stringify({ code: oauthPaste }),
      }, accountId)
      const data = await res.json()
      if (!res.ok) {
        setOauthHint(data.error || '换取 token 失败')
        return
      }
      setOauthPaste('')
      setOauthHint('')
      setClaudeConnected(true)
      setOauthStep('form')
      fetchClaudeStatus()
    } catch {
      setOauthHint('网络错误')
    } finally {
      setOauthBusy(false)
    }
  }

  const handleSubmit = async () => {
    let payload = { ...form }
    if (!payload.name.trim()) payload.name = defaultChannelName(payload.type, payload.models)
    payload.name = payload.name.trim().toLowerCase()

    if (modalMode === 'api_key' && !payload.key.trim() && !editingId) {
      alert('API Key 模式需要填写 Key')
      return
    }
    if (modalMode === 'oauth' && payload.type !== 2) {
      alert('订阅（OAuth）目前仅支持 Claude')
      return
    }
    payload.auth_mode = modalMode

    try {
      const duplicate = channels.find(
        (ch) => ch.name.toLowerCase() === payload.name && ch.id !== editingId
      )
      let targetId = editingId
      if (duplicate) {
        const ok = confirm(`名称 ${payload.name} 已存在，是否覆盖？`)
        if (!ok) return
        targetId = duplicate.id
      }

      const url = targetId ? `/v1/admin/providers/${targetId}` : '/v1/admin/providers'
      const method = targetId ? 'PUT' : 'POST'
      const res = await apiFetch(url, { method, body: JSON.stringify(payload) }, accountId)
      if (res.ok) {
        if (payload.is_default === 1 && targetId) {
          await apiFetch(`/v1/admin/providers/${targetId}/set-default`, { method: 'PUT' }, accountId)
        } else if (payload.is_default === 1 && !targetId) {
          const data = await res.json()
          if (data.id) await apiFetch(`/v1/admin/providers/${data.id}/set-default`, { method: 'PUT' }, accountId)
        }
        setShowModal(false)
        setEditingId(null)
        fetchChannels()
      } else {
        const data = await res.json().catch(() => ({}))
        alert(data.error || '保存失败')
      }
    } catch (e) {
      console.error('Failed to save channel:', e)
    }
  }

  const handleDelete = async (id: number) => {
    if (!confirm('确认删除该提供商通道？')) return
    try {
      await apiFetch(`/v1/admin/providers/${id}`, { method: 'DELETE' }, accountId)
      fetchChannels()
    } catch (e) {
      console.error('Failed to delete channel:', e)
    }
  }

  const handleSetDefault = async (id: number) => {
    try {
      await apiFetch(`/v1/admin/providers/${id}/set-default`, { method: 'PUT' }, accountId)
      fetchChannels()
    } catch (e) {
      console.error('Failed to set default channel:', e)
    }
  }

  const parseModelsField = (raw: string): string[] => {
    const trimmed = raw.trim()
    if (!trimmed) return []
    if (trimmed.startsWith('[')) {
      try {
        const parsed = JSON.parse(trimmed)
        if (Array.isArray(parsed)) {
          return parsed.map((m) => String(m).trim()).filter(Boolean)
        }
      } catch {
        // fall through
      }
    }
    return trimmed.split(',').map((m) => m.trim()).filter(Boolean)
  }

  const addModelFromList = (modelId: string) => {
    if (!modelId) return
    setForm((prev) => {
      const existing = parseModelsField(prev.models)
      if (existing.some((m) => m.toLowerCase() === modelId.toLowerCase())) {
        return syncAutoName(prev)
      }
      const nextModels = [...existing, modelId].join(', ')
      return syncAutoName({ ...prev, models: nextModels })
    })
    // Reset so the same option can be chosen again after manual removal.
    setModelPick('')
  }

  const handleFetchModels = async () => {
    if (modalMode === 'api_key' && !editingId && !form.key.trim()) {
      alert('请先填写 API Key 再获取模型列表')
      return
    }
    setFetchingModels(true)
    try {
      const res = editingId
        ? await apiFetch(`/v1/admin/providers/${editingId}/fetch-models`, { method: 'POST' }, accountId)
        : await apiFetch('/v1/admin/providers/fetch-models', {
            method: 'POST',
            body: JSON.stringify({
              type: form.type,
              key: form.key,
              base_url: form.base_url,
              auth_mode: modalMode,
            }),
          }, accountId)
      if (res.ok) {
        const data = await res.json()
        const list: string[] = Array.isArray(data.models) ? data.models.filter(Boolean) : []
        setAvailableModels(list)
        setModelPick('')
        if (list.length === 0) {
          alert('上游未返回可用模型')
        }
      } else {
        const data = await res.json().catch(() => ({}))
        alert(data.error || '获取模型列表失败')
      }
    } catch (e) {
      console.error('Failed to fetch models:', e)
      alert('获取模型列表失败')
    } finally {
      setFetchingModels(false)
    }
  }

  const connectClaude = async () => {
    setClaudeHint('')
    setClaudeBusy(true)
    try {
      const res = await apiFetch('/v1/claude/oauth/authorize?mode=paste', {}, accountId)
      const data = await res.json()
      if (!res.ok) {
        setClaudeHint(data.error || '无法开始授权')
        return
      }
      window.open(data.authorize_url, '_blank', 'noopener,noreferrer')
      setClaudeHint('已在新标签页打开授权。复制 code#state 粘贴到下方并提交。')
    } catch {
      setClaudeHint('网络错误')
    } finally {
      setClaudeBusy(false)
    }
  }

  const submitClaudePaste = async () => {
    setClaudeHint('')
    setClaudeBusy(true)
    try {
      const res = await apiFetch('/v1/claude/oauth/exchange', {
        method: 'POST',
        body: JSON.stringify({ code: claudePaste }),
      }, accountId)
      const data = await res.json()
      if (!res.ok) {
        setClaudeHint(data.error || '换取 token 失败')
        return
      }
      setClaudePaste('')
      setClaudeHint('Claude 订阅已连接')
      fetchClaudeStatus()
    } catch {
      setClaudeHint('网络错误')
    } finally {
      setClaudeBusy(false)
    }
  }

  const canSubmit = Boolean(form.name.trim() || firstModel(form.models)) &&
    (modalMode === 'oauth' || Boolean(form.key) || editingId !== null)

  return (
    <div className="space-y-6">
      {/* ---------- Subscription OAuth providers ---------- */}
      <div className="space-y-3">
        {claudeConfigured && (
          <div className="bg-card border border-border rounded-xl p-4 flex flex-col sm:flex-row sm:items-center gap-3 justify-between">
            <div className="min-w-0">
              <p className="text-[13px] font-medium text-foreground">Claude 订阅 OAuth</p>
              <p className="text-[12px] text-muted-foreground mt-0.5">
                {claudeConnected
                  ? '已连接 — 可新增 Claude 订阅提供商'
                  : '打开授权页 → 粘贴 code#state 以连接订阅'}
              </p>
              {claudeHint && <p className="text-[12px] text-amber-500 mt-1">{claudeHint}</p>}
            </div>
            <div className="flex flex-wrap items-center gap-2 shrink-0">
              {claudeConfigured && !claudeConnected && (
                <button type="button" disabled={claudeBusy} onClick={connectClaude}
                  className="h-8 px-3 bg-primary text-primary-foreground rounded-lg text-[12px] font-medium hover:bg-primary/90 disabled:opacity-50">
                  打开授权页
                </button>
              )}
              {claudeConnected && (
                <span className="inline-flex items-center px-2 py-0.5 rounded-md text-[12px] font-medium text-green-400 bg-green-500/10">Connected</span>
              )}
              <Link to="/settings" className="h-8 px-3 inline-flex items-center border border-border rounded-lg text-[12px] text-muted-foreground hover:text-foreground hover:bg-accent">
                在 Settings 管理
              </Link>
            </div>
            {claudeConfigured && !claudeConnected && (
              <div className="flex gap-2 sm:w-full">
                <input value={claudePaste} onChange={(e) => setClaudePaste(e.target.value)} placeholder="粘贴 code#state"
                  className="flex-1 h-8 px-3 bg-background border border-border rounded-lg text-[12px] focus:outline-none focus:ring-2 focus:ring-ring" />
                <button type="button" disabled={claudeBusy || !claudePaste.trim()} onClick={submitClaudePaste}
                  className="h-8 px-3 border border-primary/30 text-primary rounded-lg text-[12px] hover:bg-primary/10 disabled:opacity-50">
                  提交
                </button>
              </div>
            )}
          </div>
        )}
        <ProviderSection
          title="订阅 OAuth 提供商"
          desc="通过订阅授权（OAuth）接入的供应商，目前支持 Claude。"
          onAdd={() => openAdd('oauth')}
          addLabel="新增订阅 OAuth 提供商"
          emptyHint="暂无订阅 OAuth 提供商，点击右上角新增并完成 Claude 订阅连接。"
          channels={oauthChannels}
          onEdit={openEdit}
          onDelete={handleDelete}
          onSetDefault={handleSetDefault}
        />
      </div>

      {/* ---------- API Key compatible providers (split by protocol) ---------- */}
      <ProviderSection
        title="OpenAI 兼容提供商"
        desc="遵循 OpenAI 协议的供应商，使用 API Key 鉴权，如 OpenAI / Gemini / DeepSeek / Ollama 等。"
        onAdd={() => openAdd('api_key', 1)}
        addLabel="新增 OpenAI 兼容提供商"
        emptyHint="暂无 OpenAI 兼容提供商，点击右上角新增。"
        channels={openaiCompatibleChannels}
        onEdit={openEdit}
        onDelete={handleDelete}
        onSetDefault={handleSetDefault}
      />

      <ProviderSection
        title="Claude 兼容提供商"
        desc="遵循 Anthropic Claude 协议的供应商，使用 API Key 鉴权。"
        onAdd={() => openAdd('api_key', 2)}
        addLabel="新增 Claude 兼容提供商"
        emptyHint="暂无 Claude 兼容提供商，点击右上角新增。"
        channels={claudeCompatibleChannels}
        onEdit={openEdit}
        onDelete={handleDelete}
        onSetDefault={handleSetDefault}
      />

      {/* ---------- Add / Edit Modal ---------- */}
      {showModal && (
        <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50">
          <div className="bg-card border border-border rounded-xl p-6 w-full max-w-md">
            <h3 className="text-base font-semibold text-foreground mb-4">
              {editingId ? '编辑提供商' : (modalMode === 'oauth' ? '新增订阅 OAuth 提供商' : '新增 API Key 提供商')}
            </h3>

            {modalMode === 'oauth' && oauthStep === 'connect' ? (
              <div className="space-y-4">
                <p className="text-[13px] text-muted-foreground">
                  新增订阅 OAuth 提供商需要先连接 Claude 订阅。点击下方按钮打开授权页，登录后复制地址栏中的 <span className="font-mono text-foreground">code#state</span> 并粘贴到下方完成连接。
                </p>
                {oauthHint && (
                  <p className="text-[12px] text-amber-500 bg-amber-500/10 rounded-lg px-3 py-2">{oauthHint}</p>
                )}
                <div className="flex gap-2">
                  <input
                    value={oauthPaste}
                    onChange={(e) => setOauthPaste(e.target.value)}
                    placeholder="粘贴 code#state"
                    className="flex-1 h-9 px-3 bg-background border border-border rounded-lg text-[13px] focus:outline-none focus:ring-2 focus:ring-ring"
                  />
                  <button
                    type="button"
                    disabled={oauthBusy || !oauthPaste.trim()}
                    onClick={oauthDoConnect}
                    className="h-9 px-3 border border-primary/30 text-primary rounded-lg text-[12px] hover:bg-primary/10 disabled:opacity-50"
                  >
                    连接
                  </button>
                </div>
                <button
                  type="button"
                  disabled={oauthBusy}
                  onClick={oauthStartConnect}
                  className="h-9 px-4 bg-primary text-primary-foreground rounded-lg text-[13px] font-medium hover:bg-primary/90 disabled:opacity-50"
                >
                  打开 Claude 授权页
                </button>
              </div>
            ) : (
            <div className="space-y-4">
              <div>
                <label className="block text-[13px] font-medium text-foreground mb-1.5">供应商类型</label>
                <select
                  value={form.type}
                  onChange={(e) => {
                    const type = parseInt(e.target.value)
                    setForm((prev) => syncAutoName({
                      ...prev, type, base_url: defaultBaseURLs[type] || prev.base_url,
                    }))
                  }}
                  className="w-full h-9 px-3 bg-background border border-border rounded-lg text-[13px]"
                >
                  {channelTypeOptions.map((o) => (
                    <option key={o.value} value={o.value}>{o.label}</option>
                  ))}
                </select>
              </div>

              {modalMode === 'api_key' ? (
                <div>
                  <label className="block text-[13px] font-medium text-foreground mb-1.5">
                    API Key {editingId && <span className="font-normal text-muted-foreground">（留空则不修改）</span>}
                  </label>
                  <input
                    type="password"
                    value={form.key}
                    onChange={(e) => setForm({ ...form, key: e.target.value })}
                    placeholder="sk-..."
                    className="w-full h-9 px-3 bg-background border border-border rounded-lg text-[13px]"
                  />
                </div>
              ) : (
                <div className="text-[12px] text-muted-foreground bg-muted/50 rounded-lg p-3">
                  订阅模式将使用已连接的 OAuth 凭据，无需填写 Key。
                  {!claudeConnected && (
                    <p className="text-amber-500 mt-1">尚未连接 Claude 订阅，请先连接后再新增。</p>
                  )}
                </div>
              )}

              <div>
                <label className="block text-[13px] font-medium text-foreground mb-1.5">Base URL</label>
                <input
                  value={form.base_url}
                  onChange={(e) => setForm({ ...form, base_url: e.target.value })}
                  placeholder="https://api.openai.com/v1"
                  className="w-full h-9 px-3 bg-background border border-border rounded-lg text-[13px]"
                />
                <p className="text-[11px] text-muted-foreground mt-1">
                  OpenAI 兼容接口需带版本路径，例如 <code className="text-[10px]">https://你的域名/v1</code>
                  。只填主机名常会被上游 OpenResty 返回 HTML 404。
                </p>
              </div>

              <div>
                <label className="block text-[13px] font-medium text-foreground mb-1.5">Models (逗号分隔，留空表示全部)</label>
                <div className="flex gap-2">
                  <select
                    value={modelPick}
                    onChange={(e) => addModelFromList(e.target.value)}
                    disabled={availableModels.length === 0}
                    className="flex-1 h-9 px-3 bg-background border border-border rounded-lg text-[13px] disabled:opacity-50"
                  >
                    <option value="">
                      {availableModels.length === 0 ? '点击 Fetch Models 获取列表' : '从已获取列表中选择模型'}
                    </option>
                    {availableModels.map((m) => (
                      <option key={m} value={m}>{m}</option>
                    ))}
                  </select>
                  <button type="button" onClick={handleFetchModels} disabled={fetchingModels}
                    className="h-9 px-3 text-[12px] text-primary border border-primary/30 rounded-lg hover:bg-primary/10 disabled:opacity-50 whitespace-nowrap">
                    {fetchingModels ? '...' : 'Fetch Models'}
                  </button>
                </div>
                <input
                  value={form.models}
                  onChange={(e) => setForm((prev) => syncAutoName({ ...prev, models: e.target.value }))}
                  placeholder="已选模型，可手动编辑，如 gpt-4o, gpt-3.5-turbo"
                  className="mt-2 w-full h-9 px-3 bg-background border border-border rounded-lg text-[13px]"
                />
                {availableModels.length > 0 && (
                  <p className="mt-1.5 text-[12px] text-muted-foreground">
                    已获取 {availableModels.length} 个模型，可从上方下拉框选择添加
                  </p>
                )}
              </div>

              <div className="flex items-center gap-2">
                <input type="checkbox" id="is_default" checked={form.is_default === 1}
                  onChange={(e) => setForm({ ...form, is_default: e.target.checked ? 1 : 0 })} className="w-4 h-4 rounded border-border" />
                <label htmlFor="is_default" className="text-[13px] font-medium text-foreground">设为默认提供商</label>
              </div>

              <div className="grid grid-cols-2 gap-4">
                <div>
                  <label className="block text-[13px] font-medium text-foreground mb-1.5">Weight</label>
                  <input type="number" value={form.weight} onChange={(e) => setForm({ ...form, weight: parseInt(e.target.value) })}
                    className="w-full h-9 px-3 bg-background border border-border rounded-lg text-[13px]" />
                </div>
                <div>
                  <label className="block text-[13px] font-medium text-foreground mb-1.5">Priority</label>
                  <input type="number" value={form.priority} onChange={(e) => setForm({ ...form, priority: parseInt(e.target.value) })}
                    className="w-full h-9 px-3 bg-background border border-border rounded-lg text-[13px]" />
                </div>
              </div>

              <div>
                <label className="block text-[13px] font-medium text-foreground mb-1.5">
                  名称 {!nameTouched && <span className="ml-2 font-normal text-muted-foreground">默认 type/model</span>}
                </label>
                <input
                  value={form.name}
                  onChange={(e) => {
                    nameTouchedRef.current = true
                    setNameTouched(true)
                    setForm({ ...form, name: e.target.value.toLowerCase() })
                  }}
                  placeholder={defaultChannelName(form.type, form.models) || 'openai/gpt-4o'}
                  className="w-full h-9 px-3 bg-background border border-border rounded-lg text-[13px]"
                />
              </div>
            </div>
            )}

            <div className="flex justify-end gap-2 mt-6">
              <button onClick={() => { setShowModal(false); setEditingId(null); }}
                className="h-9 px-4 text-[13px] text-muted-foreground hover:text-foreground border border-border rounded-md hover:bg-accent">
                取消
              </button>
              {!(modalMode === 'oauth' && oauthStep === 'connect') && (
              <button onClick={handleSubmit} disabled={!canSubmit}
                className="h-9 px-4 bg-primary text-primary-foreground rounded-lg text-[13px] font-medium hover:bg-primary/90 disabled:opacity-50">
                {editingId ? '更新' : '创建'}
              </button>
              )}
            </div>
          </div>
        </div>
      )}
    </div>
  )
}

function ProviderSection({
  title, desc, onAdd, addLabel, emptyHint, channels, onEdit, onDelete, onSetDefault,
  disabledAdd, disabledAddHint,
}: {
  title: string
  desc: string
  onAdd: () => void
  addLabel: string
  emptyHint: string
  channels: Channel[]
  onEdit: (c: Channel) => void
  onDelete: (id: number) => void
  onSetDefault: (id: number) => void
  disabledAdd?: boolean
  disabledAddHint?: string
}) {
  return (
    <div className="bg-card border border-border rounded-xl">
      <div className="flex items-start justify-between px-5 py-4 border-b border-border">
        <div>
          <p className="text-[14px] font-semibold text-foreground">{title}</p>
          <p className="text-[12px] text-muted-foreground mt-0.5">{desc}</p>
        </div>
        <button
          onClick={onAdd}
          disabled={disabledAdd}
          className="h-9 px-4 bg-primary text-primary-foreground rounded-lg text-[13px] font-medium hover:bg-primary/90 disabled:opacity-50 flex items-center gap-2 shrink-0"
        >
          <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
            <line x1="12" y1="5" x2="12" y2="19" />
            <line x1="5" y1="12" x2="19" y2="12" />
          </svg>
          {addLabel}{disabledAddHint}
        </button>
      </div>

      {channels.length === 0 ? (
        <p className="px-5 py-10 text-center text-[13px] text-muted-foreground">{emptyHint}</p>
      ) : (
        <table className="w-full">
          <thead>
            <tr className="border-b border-border">
              <th className="px-5 py-3 text-left text-[12px] font-medium text-muted-foreground">ID</th>
              <th className="px-5 py-3 text-left text-[12px] font-medium text-muted-foreground">名称</th>
              <th className="px-5 py-3 text-left text-[12px] font-medium text-muted-foreground">类型</th>
              <th className="px-5 py-3 text-left text-[12px] font-medium text-muted-foreground">Base URL</th>
              <th className="px-5 py-3 text-left text-[12px] font-medium text-muted-foreground">状态</th>
              <th className="px-5 py-3 text-left text-[12px] font-medium text-muted-foreground">默认</th>
              <th className="px-5 py-3 text-left text-[12px] font-medium text-muted-foreground">操作</th>
            </tr>
          </thead>
          <tbody>
            {channels.map((channel) => {
              const typeInfo = channelTypes[channel.type] || { name: 'Unknown', color: 'text-gray-400 bg-gray-500/10' }
              return (
                <tr key={channel.id} className="border-b border-border hover:bg-accent/50">
                  <td className="px-5 py-3 text-[13px] text-muted-foreground tabular-nums">{channel.id}</td>
                  <td className="px-5 py-3 text-[13px] text-foreground font-medium">{channel.name}</td>
                  <td className="px-5 py-3">
                    <span className={`inline-flex items-center px-2 py-0.5 rounded-md text-[12px] font-medium ${typeInfo.color}`}>
                      {typeInfo.name}
                    </span>
                  </td>
                  <td className="px-5 py-3 text-[13px] text-muted-foreground max-w-[200px] truncate">{channel.base_url}</td>
                  <td className="px-5 py-3">
                    <span className={`inline-flex items-center gap-1.5 text-[12px] font-medium ${channel.status === 1 ? 'text-success' : 'text-destructive'}`}>
                      <span className={`w-1.5 h-1.5 rounded-full ${channel.status === 1 ? 'bg-success' : 'bg-destructive'}`}></span>
                      {channel.status === 1 ? '启用' : '停用'}
                    </span>
                  </td>
                  <td className="px-5 py-3">
                    {channel.is_default === 1 ? (
                      <span className="inline-flex items-center gap-1 text-[12px] font-medium text-yellow-400">
                        <svg width="14" height="14" viewBox="0 0 24 24" fill="currentColor" stroke="none">
                          <polygon points="12 2 15.09 8.26 22 9.27 17 14.14 18.18 21.02 12 17.77 5.82 21.02 7 14.14 2 9.27 8.91 8.26" />
                        </svg>
                        Default
                      </span>
                    ) : (
                      <button onClick={() => onSetDefault(channel.id)} className="text-[12px] text-muted-foreground hover:text-yellow-400">
                        设为默认
                      </button>
                    )}
                  </td>
                  <td className="px-5 py-3">
                    <div className="flex items-center gap-2">
                      <button onClick={() => onEdit(channel)} className="text-[13px] text-primary hover:text-primary/80 font-medium">编辑</button>
                      <button onClick={() => onDelete(channel.id)} className="text-[13px] text-destructive hover:text-destructive/80 font-medium">删除</button>
                    </div>
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      )}
    </div>
  )
}

// ============ LLM Request Logs ============

interface RequestLogSummary {
  id: string
  provider_id?: number
  provider_name?: string
  model: string
  stream: boolean
  status_code: number
  error?: string
  prompt_tokens: number
  completion_tokens: number
  cached_tokens: number
  latency_ms: number
  created_at: string
}

interface RequestLogDetail extends RequestLogSummary {
  request_body?: string
  response_body?: string
  user_id?: number
}

function formatJSON(raw?: string) {
  if (!raw) return ''
  try {
    return JSON.stringify(JSON.parse(raw), null, 2)
  } catch {
    return raw
  }
}

function RequestLogsPage({ accountId }: { accountId?: number }) {
  const [logs, setLogs] = useState<RequestLogSummary[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [modelFilter, setModelFilter] = useState('')
  const [statusFilter, setStatusFilter] = useState('')
  const [selectedId, setSelectedId] = useState<string | null>(null)
  const [detail, setDetail] = useState<RequestLogDetail | null>(null)
  const [detailLoading, setDetailLoading] = useState(false)

  const fetchLogs = async () => {
    if (!accountId) return
    setLoading(true)
    setError('')
    try {
      const params = new URLSearchParams({ limit: '50', offset: '0' })
      if (modelFilter.trim()) params.set('model', modelFilter.trim())
      if (statusFilter.trim()) params.set('status', statusFilter.trim())
      const res = await apiFetch(`/v1/admin/request-logs?${params}`, {}, accountId)
      const data = await res.json().catch(() => ({}))
      if (!res.ok) {
        setError(data.error || '加载请求日志失败')
        return
      }
      setLogs(data.logs || [])
    } catch (e) {
      console.error(e)
      setError('加载请求日志失败')
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    void fetchLogs()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [accountId])

  const openDetail = async (id: string) => {
    setSelectedId(id)
    setDetail(null)
    setDetailLoading(true)
    try {
      const res = await apiFetch(`/v1/admin/request-logs/${id}`, {}, accountId)
      const data = await res.json().catch(() => ({}))
      if (!res.ok) {
        setError(data.error || '加载详情失败')
        return
      }
      setDetail(data)
    } catch (e) {
      console.error(e)
      setError('加载详情失败')
    } finally {
      setDetailLoading(false)
    }
  }

  return (
    <div className="space-y-4">
      <div className="bg-card border border-border rounded-xl p-4">
        <div className="flex flex-wrap items-end gap-3">
          <div>
            <label className="block text-[12px] text-muted-foreground mb-1">模型</label>
            <input
              value={modelFilter}
              onChange={(e) => setModelFilter(e.target.value)}
              placeholder="例如 gpt-4o"
              className="h-9 w-48 px-3 bg-background border border-border rounded-lg text-[13px]"
            />
          </div>
          <div>
            <label className="block text-[12px] text-muted-foreground mb-1">状态码</label>
            <input
              value={statusFilter}
              onChange={(e) => setStatusFilter(e.target.value)}
              placeholder="200 / 500"
              className="h-9 w-28 px-3 bg-background border border-border rounded-lg text-[13px]"
            />
          </div>
          <button
            type="button"
            onClick={() => void fetchLogs()}
            disabled={loading}
            className="h-9 px-4 bg-primary text-primary-foreground rounded-lg text-[13px] font-medium hover:bg-primary/90 disabled:opacity-50"
          >
            {loading ? '加载中…' : '刷新'}
          </button>
          <p className="text-[12px] text-muted-foreground pb-2">
            展示网关 LLM 调用的请求/响应审计日志（含 Chat Completions 与 Agent）
          </p>
        </div>
        {error && <p className="mt-3 text-[12px] text-destructive">{error}</p>}
      </div>

      <div className="grid grid-cols-1 xl:grid-cols-2 gap-4">
        <div className="bg-card border border-border rounded-xl overflow-hidden">
          <div className="px-4 py-3 border-b border-border">
            <p className="text-[13px] font-medium text-foreground">最近请求</p>
          </div>
          <div className="max-h-[70vh] overflow-auto">
            {logs.length === 0 && !loading ? (
              <p className="px-4 py-10 text-center text-[13px] text-muted-foreground">暂无请求日志</p>
            ) : (
              <table className="w-full">
                <thead className="sticky top-0 bg-card">
                  <tr className="border-b border-border text-left">
                    <th className="px-3 py-2 text-[11px] font-medium text-muted-foreground">时间</th>
                    <th className="px-3 py-2 text-[11px] font-medium text-muted-foreground">模型</th>
                    <th className="px-3 py-2 text-[11px] font-medium text-muted-foreground">提供商</th>
                    <th className="px-3 py-2 text-[11px] font-medium text-muted-foreground">状态</th>
                    <th className="px-3 py-2 text-[11px] font-medium text-muted-foreground">耗时</th>
                    <th className="px-3 py-2 text-[11px] font-medium text-muted-foreground">Tokens</th>
                  </tr>
                </thead>
                <tbody>
                  {logs.map((log) => (
                    <tr
                      key={log.id}
                      onClick={() => void openDetail(log.id)}
                      className={`border-b border-border cursor-pointer hover:bg-accent/50 ${
                        selectedId === log.id ? 'bg-accent/40' : ''
                      }`}
                    >
                      <td className="px-3 py-2 text-[12px] text-muted-foreground whitespace-nowrap">
                        {new Date(log.created_at).toLocaleString()}
                      </td>
                      <td className="px-3 py-2 text-[12px] text-foreground font-mono max-w-[140px] truncate">
                        {log.model || '-'}
                      </td>
                      <td className="px-3 py-2 text-[12px] text-muted-foreground max-w-[120px] truncate">
                        {log.provider_name || '-'}
                      </td>
                      <td className="px-3 py-2 text-[12px]">
                        <span
                          className={
                            log.status_code >= 200 && log.status_code < 300
                              ? 'text-success'
                              : 'text-destructive'
                          }
                        >
                          {log.status_code || '-'}
                        </span>
                        {log.stream ? (
                          <span className="ml-1 text-[10px] text-muted-foreground">stream</span>
                        ) : null}
                      </td>
                      <td className="px-3 py-2 text-[12px] text-muted-foreground tabular-nums">
                        {log.latency_ms}ms
                      </td>
                      <td className="px-3 py-2 text-[12px] text-muted-foreground tabular-nums">
                        {log.prompt_tokens}+{log.completion_tokens}
                        {log.cached_tokens ? ` (c${log.cached_tokens})` : ''}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            )}
          </div>
        </div>

        <div className="bg-card border border-border rounded-xl overflow-hidden min-h-[320px]">
          <div className="px-4 py-3 border-b border-border flex items-center justify-between gap-2">
            <p className="text-[13px] font-medium text-foreground">请求详情</p>
            {detail?.id && (
              <span className="text-[11px] text-muted-foreground font-mono truncate">{detail.id}</span>
            )}
          </div>
          <div className="p-4 max-h-[70vh] overflow-auto space-y-4">
            {!selectedId && (
              <p className="text-[13px] text-muted-foreground">选择左侧一条日志查看请求/响应正文</p>
            )}
            {selectedId && detailLoading && (
              <p className="text-[13px] text-muted-foreground">加载详情…</p>
            )}
            {detail && !detailLoading && (
              <>
                <div className="grid grid-cols-2 gap-2 text-[12px]">
                  <div>
                    <span className="text-muted-foreground">模型</span>
                    <p className="font-mono text-foreground break-all">{detail.model}</p>
                  </div>
                  <div>
                    <span className="text-muted-foreground">提供商</span>
                    <p className="text-foreground break-all">{detail.provider_name || '-'}</p>
                  </div>
                  <div>
                    <span className="text-muted-foreground">状态</span>
                    <p className="text-foreground">{detail.status_code}</p>
                  </div>
                  <div>
                    <span className="text-muted-foreground">耗时</span>
                    <p className="text-foreground">{detail.latency_ms} ms</p>
                  </div>
                </div>
                {detail.error && (
                  <div>
                    <p className="text-[12px] font-medium text-destructive mb-1">错误</p>
                    <pre className="text-[11px] whitespace-pre-wrap break-words bg-destructive/10 border border-destructive/30 rounded-lg p-3">
                      {detail.error}
                    </pre>
                  </div>
                )}
                <div>
                  <p className="text-[12px] font-medium text-foreground mb-1">Request</p>
                  <pre className="text-[11px] whitespace-pre-wrap break-words bg-background border border-border rounded-lg p-3 max-h-64 overflow-auto">
                    {formatJSON(detail.request_body) || '(empty)'}
                  </pre>
                </div>
                <div>
                  <p className="text-[12px] font-medium text-foreground mb-1">Response</p>
                  <pre className="text-[11px] whitespace-pre-wrap break-words bg-background border border-border rounded-lg p-3 max-h-80 overflow-auto">
                    {formatJSON(detail.response_body) || '(empty)'}
                  </pre>
                </div>
              </>
            )}
          </div>
        </div>
      </div>
    </div>
  )
}
