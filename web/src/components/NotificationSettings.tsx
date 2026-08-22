import { useEffect, useState } from 'react'
import {
  AlertCircle,
  Bell,
  CheckCircle2,
  Loader2,
  MessageCircle,
  Save,
  Send,
  Trash2,
} from 'lucide-react'
import { apiFetch } from '../context/AccountContext'

type ChannelKind = 'wechat' | 'telegram' | 'dingtalk'
type NotificationEvent = 'run_done' | 'run_failed'

interface NotificationChannel {
  kind: ChannelKind
  name: string
  enabled: boolean
  events: NotificationEvent[]
  configured: boolean
  target_hint: string
  last_error?: string
  last_test_at?: string
  updated_at: string
}

interface ChannelConfig {
  bridge_url?: string
  target?: string
  access_token?: string
  bot_token?: string
  chat_id?: string
  webhook_url?: string
  secret?: string
}

interface ChannelDraft {
  enabled: boolean
  runDone: boolean
  runFailed: boolean
  config: ChannelConfig
}

interface ChannelDefinition {
  kind: ChannelKind
  name: string
  description: string
  fields: Array<{
    key: keyof ChannelConfig
    label: string
    placeholder: string
    secret?: boolean
  }>
}

const CHANNELS: ChannelDefinition[] = [
  {
    kind: 'wechat',
    name: '微信',
    description: 'OpenClaw Bridge',
    fields: [
      { key: 'bridge_url', label: 'Bridge URL', placeholder: 'https://openclaw.example.com/notify' },
      { key: 'target', label: '接收目标', placeholder: '微信联系人或群聊标识' },
      { key: 'access_token', label: 'Bridge Token', placeholder: '可选', secret: true },
    ],
  },
  {
    kind: 'telegram',
    name: 'Telegram',
    description: 'Bot API',
    fields: [
      { key: 'bot_token', label: 'Bot Token', placeholder: '123456:ABC...', secret: true },
      { key: 'chat_id', label: 'Chat ID', placeholder: '-1001234567890' },
    ],
  },
  {
    kind: 'dingtalk',
    name: '钉钉',
    description: '自定义机器人',
    fields: [
      {
        key: 'webhook_url',
        label: 'Webhook',
        placeholder: 'https://oapi.dingtalk.com/robot/send?access_token=...',
        secret: true,
      },
      { key: 'secret', label: '加签 Secret', placeholder: 'SEC...', secret: true },
    ],
  },
]

function newDraft(): ChannelDraft {
  return { enabled: true, runDone: true, runFailed: true, config: {} }
}

function initialDrafts(): Record<ChannelKind, ChannelDraft> {
  return {
    wechat: newDraft(),
    telegram: newDraft(),
    dingtalk: newDraft(),
  }
}

async function responseError(response: Response, fallback: string): Promise<string> {
  const body = await response.json().catch(() => ({}))
  return typeof body.error === 'string' && body.error ? body.error : fallback
}

function formatTestTime(value?: string): string {
  if (!value) return ''
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return ''
  return date.toLocaleString('zh-CN', { hour12: false })
}

export default function NotificationSettings({ accountId }: { accountId?: number }) {
  const [channels, setChannels] = useState<Partial<Record<ChannelKind, NotificationChannel>>>({})
  const [drafts, setDrafts] = useState<Record<ChannelKind, ChannelDraft>>(initialDrafts)
  const [loading, setLoading] = useState(true)
  const [busyKind, setBusyKind] = useState<ChannelKind | null>(null)
  const [errors, setErrors] = useState<Partial<Record<ChannelKind | 'list', string>>>({})
  const [messages, setMessages] = useState<Partial<Record<ChannelKind, string>>>({})

  useEffect(() => {
    let active = true
    setChannels({})
    setDrafts(initialDrafts())
    setErrors({})
    setMessages({})

    if (!accountId) {
      setLoading(false)
      return () => {
        active = false
      }
    }

    setLoading(true)
    apiFetch('/v1/notifications/channels', {}, accountId)
      .then(async (response) => {
        if (!response.ok) throw new Error(await responseError(response, '无法读取通知订阅'))
        const body = await response.json()
        if (!active) return
        const nextChannels: Partial<Record<ChannelKind, NotificationChannel>> = {}
        const nextDrafts = initialDrafts()
        for (const channel of (body.channels || []) as NotificationChannel[]) {
          if (!CHANNELS.some((item) => item.kind === channel.kind)) continue
          nextChannels[channel.kind] = channel
          nextDrafts[channel.kind] = {
            enabled: channel.enabled,
            runDone: channel.events.includes('run_done'),
            runFailed: channel.events.includes('run_failed'),
            config: {},
          }
        }
        setChannels(nextChannels)
        setDrafts(nextDrafts)
      })
      .catch((error: unknown) => {
        if (active) setErrors({ list: error instanceof Error ? error.message : '无法读取通知订阅' })
      })
      .finally(() => {
        if (active) setLoading(false)
      })

    return () => {
      active = false
    }
  }, [accountId])

  const updateDraft = (kind: ChannelKind, update: (draft: ChannelDraft) => ChannelDraft) => {
    setDrafts((current) => ({ ...current, [kind]: update(current[kind]) }))
    setErrors((current) => ({ ...current, [kind]: '' }))
    setMessages((current) => ({ ...current, [kind]: '' }))
  }

  const saveChannel = async (definition: ChannelDefinition) => {
    const { kind } = definition
    const draft = drafts[kind]
    const events: NotificationEvent[] = []
    if (draft.runDone) events.push('run_done')
    if (draft.runFailed) events.push('run_failed')
    if (events.length === 0) {
      setErrors((current) => ({ ...current, [kind]: '至少选择一种通知事件' }))
      return
    }

    setBusyKind(kind)
    setErrors((current) => ({ ...current, [kind]: '' }))
    setMessages((current) => ({ ...current, [kind]: '' }))
    try {
      const response = await apiFetch(
        `/v1/notifications/channels/${kind}`,
        {
          method: 'PUT',
          body: JSON.stringify({
            name: definition.name,
            enabled: draft.enabled,
            events,
            config: Object.fromEntries(
              Object.entries(draft.config).map(([key, value]) => [key, typeof value === 'string' ? value.trim() : value]),
            ),
          }),
        },
        accountId,
      )
      if (!response.ok) throw new Error(await responseError(response, '保存失败'))
      const body = await response.json()
      setChannels((current) => ({ ...current, [kind]: body.channel as NotificationChannel }))
      updateDraft(kind, (current) => ({ ...current, config: {} }))
      setMessages((current) => ({ ...current, [kind]: '配置已保存' }))
    } catch (error) {
      setErrors((current) => ({ ...current, [kind]: error instanceof Error ? error.message : '保存失败' }))
    } finally {
      setBusyKind(null)
    }
  }

  const testChannel = async (kind: ChannelKind) => {
    setBusyKind(kind)
    setErrors((current) => ({ ...current, [kind]: '' }))
    setMessages((current) => ({ ...current, [kind]: '' }))
    try {
      const response = await apiFetch(`/v1/notifications/channels/${kind}/test`, { method: 'POST' }, accountId)
      if (!response.ok) throw new Error(await responseError(response, '测试通知发送失败'))
      setMessages((current) => ({ ...current, [kind]: '测试通知已发送' }))
      setChannels((current) => {
        const channel = current[kind]
        return channel
          ? { ...current, [kind]: { ...channel, last_error: '', last_test_at: new Date().toISOString() } }
          : current
      })
    } catch (error) {
      setErrors((current) => ({
        ...current,
        [kind]: error instanceof Error ? error.message : '测试通知发送失败',
      }))
    } finally {
      setBusyKind(null)
    }
  }

  const deleteChannel = async (kind: ChannelKind, name: string) => {
    if (!window.confirm(`确定删除${name}通知配置？`)) return
    setBusyKind(kind)
    setErrors((current) => ({ ...current, [kind]: '' }))
    setMessages((current) => ({ ...current, [kind]: '' }))
    try {
      const response = await apiFetch(`/v1/notifications/channels/${kind}`, { method: 'DELETE' }, accountId)
      if (!response.ok) throw new Error(await responseError(response, '删除失败'))
      setChannels((current) => {
        const next = { ...current }
        delete next[kind]
        return next
      })
      setDrafts((current) => ({ ...current, [kind]: newDraft() }))
    } catch (error) {
      setErrors((current) => ({ ...current, [kind]: error instanceof Error ? error.message : '删除失败' }))
    } finally {
      setBusyKind(null)
    }
  }

  return (
    <section className="bg-card border border-border rounded-xl overflow-hidden">
      <div className="p-5">
        <div className="flex items-center gap-2">
          <Bell className="h-4 w-4 text-primary" />
          <h3 className="text-sm font-semibold text-foreground">通知订阅</h3>
        </div>
        {errors.list && <p className="mt-3 text-[12px] text-red-500">{errors.list}</p>}
      </div>

      {CHANNELS.map((definition) => {
        const { kind } = definition
        const channel = channels[kind]
        const draft = drafts[kind]
        const busy = busyKind === kind
        return (
          <div key={kind} className="border-t border-border p-5 space-y-4">
            <div className="flex flex-wrap items-center justify-between gap-3">
              <div className="flex min-w-0 items-center gap-3">
                <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-muted text-muted-foreground">
                  <MessageCircle className="h-4 w-4" />
                </div>
                <div className="min-w-0">
                  <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
                    <h4 className="text-[13px] font-semibold text-foreground">{definition.name}</h4>
                    <span className="text-[11px] text-muted-foreground">{definition.description}</span>
                  </div>
                  <p className="mt-0.5 text-[11px] text-muted-foreground">
                    {channel ? `已配置${channel.target_hint ? ` · ${channel.target_hint}` : ''}` : '未配置'}
                  </p>
                </div>
              </div>
              <div className="flex items-center gap-2 text-[12px] text-foreground">
                <span>启用</span>
                <button
                  type="button"
                  role="switch"
                  aria-checked={draft.enabled}
                  aria-label={`${definition.name}通知`}
                  onClick={() => updateDraft(kind, (current) => ({ ...current, enabled: !current.enabled }))}
                  className={`relative h-5 w-9 rounded-full transition-colors ${draft.enabled ? 'bg-primary' : 'bg-muted-foreground/30'}`}
                >
                  <span
                    className={`absolute top-0.5 h-4 w-4 rounded-full bg-white shadow-sm transition-transform ${
                      draft.enabled ? 'translate-x-[18px]' : 'translate-x-0.5'
                    }`}
                  />
                </button>
              </div>
            </div>

            <div className="grid gap-3 sm:grid-cols-2">
              {definition.fields.map((field, index) => (
                <label
                  key={field.key}
                  className={definition.fields.length > 2 && index === 0 ? 'sm:col-span-2' : ''}
                >
                  <span className="mb-1.5 block text-[12px] text-muted-foreground">{field.label}</span>
                  <input
                    type={field.secret ? 'password' : 'text'}
                    autoComplete="off"
                    value={draft.config[field.key] || ''}
                    onChange={(event) =>
                      updateDraft(kind, (current) => ({
                        ...current,
                        config: { ...current.config, [field.key]: event.target.value },
                      }))
                    }
                    placeholder={channel ? '已保存，留空保持不变' : field.placeholder}
                    className="h-9 w-full min-w-0 rounded-lg border border-border bg-background px-3 text-[13px] focus:outline-none focus:ring-2 focus:ring-ring"
                  />
                </label>
              ))}
            </div>

            <div className="flex flex-wrap items-center gap-x-5 gap-y-2">
              <span className="text-[12px] text-muted-foreground">订阅事件</span>
              <label className="flex cursor-pointer items-center gap-2 text-[12px] text-foreground">
                <input
                  type="checkbox"
                  checked={draft.runDone}
                  onChange={(event) => updateDraft(kind, (current) => ({ ...current, runDone: event.target.checked }))}
                  className="h-4 w-4 rounded border-border accent-primary"
                />
                任务成功
              </label>
              <label className="flex cursor-pointer items-center gap-2 text-[12px] text-foreground">
                <input
                  type="checkbox"
                  checked={draft.runFailed}
                  onChange={(event) => updateDraft(kind, (current) => ({ ...current, runFailed: event.target.checked }))}
                  className="h-4 w-4 rounded border-border accent-primary"
                />
                任务失败
              </label>
            </div>

            {channel?.last_error && (
              <div className="flex items-start gap-2 text-[12px] text-red-500">
                <AlertCircle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
                <span className="break-all">最近发送失败：{channel.last_error}</span>
              </div>
            )}
            {channel?.last_test_at && !channel.last_error && (
              <p className="text-[11px] text-muted-foreground">最近测试：{formatTestTime(channel.last_test_at)}</p>
            )}
            {errors[kind] && (
              <div className="flex items-start gap-2 text-[12px] text-red-500">
                <AlertCircle className="mt-0.5 h-3.5 w-3.5 shrink-0" />
                <span className="break-all">{errors[kind]}</span>
              </div>
            )}
            {messages[kind] && (
              <div className="flex items-center gap-2 text-[12px] text-green-500">
                <CheckCircle2 className="h-3.5 w-3.5" />
                <span>{messages[kind]}</span>
              </div>
            )}

            <div className="flex flex-wrap items-center gap-2">
              <button
                type="button"
                disabled={loading || busy || !accountId}
                onClick={() => void saveChannel(definition)}
                className="inline-flex h-9 items-center gap-2 rounded-lg bg-primary px-3 text-[12px] font-medium text-primary-foreground hover:bg-primary/90 disabled:opacity-50"
              >
                {busy ? <Loader2 className="h-3.5 w-3.5 animate-spin" /> : <Save className="h-3.5 w-3.5" />}
                保存
              </button>
              <button
                type="button"
                disabled={!channel || busy}
                onClick={() => void testChannel(kind)}
                className="inline-flex h-9 items-center gap-2 rounded-lg border border-border px-3 text-[12px] text-foreground hover:bg-muted disabled:opacity-50"
              >
                <Send className="h-3.5 w-3.5" />
                测试发送
              </button>
              {channel && (
                <button
                  type="button"
                  disabled={busy}
                  onClick={() => void deleteChannel(kind, definition.name)}
                  className="inline-flex h-9 items-center gap-2 rounded-lg px-3 text-[12px] text-destructive hover:bg-destructive/10 disabled:opacity-50"
                >
                  <Trash2 className="h-3.5 w-3.5" />
                  删除
                </button>
              )}
            </div>
          </div>
        )
      })}
    </section>
  )
}
