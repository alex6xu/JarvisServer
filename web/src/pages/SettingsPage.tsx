import { FormEvent, useEffect, useState } from 'react'
import { apiFetch, useAccount } from '../context/AccountContext'
import { useAuth } from '../context/AuthContext'

export default function SettingsPage() {
  const { user, changePassword } = useAuth()
  const { currentAccount } = useAccount()
  const [currentPassword, setCurrentPassword] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')
  const [message, setMessage] = useState('')
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)

  const [claudeConfigured, setClaudeConfigured] = useState(false)
  const [claudeConnected, setClaudeConnected] = useState(false)
  const [claudeBusy, setClaudeBusy] = useState(false)
  const [claudeError, setClaudeError] = useState('')
  const [claudePaste, setClaudePaste] = useState('')
  const [claudeMsg, setClaudeMsg] = useState('')

  const [claudeEmail, setClaudeEmail] = useState('')

  const fetchClaudeStatus = async () => {
    try {
      const res = await apiFetch('/v1/claude/oauth/status', {}, currentAccount?.id)
      if (!res.ok) return
      const data = await res.json()
      setClaudeConfigured(!!data.configured)
      setClaudeConnected(!!data.connected)
      setClaudeEmail(data.email || '')
    } catch {
      // ignore
    }
  }

  useEffect(() => {
    fetchClaudeStatus()
    const params = new URLSearchParams(window.location.search)
    const oauth = params.get('claude_oauth')
    if (oauth === 'connected') {
      setClaudeMsg('Claude 订阅已连接')
      window.history.replaceState({}, '', window.location.pathname)
      fetchClaudeStatus()
    } else if (oauth === 'error') {
      setClaudeError(params.get('message') || 'OAuth 失败')
      window.history.replaceState({}, '', window.location.pathname)
    }
  }, [currentAccount?.id])

  const startClaudePaste = async () => {
    setClaudeError('')
    setClaudeMsg('')
    setClaudeBusy(true)
    try {
      const res = await apiFetch('/v1/claude/oauth/authorize?mode=paste', {}, currentAccount?.id)
      const data = await res.json()
      if (!res.ok) {
        setClaudeError(data.error || '无法开始授权')
        return
      }
      window.open(data.authorize_url, '_blank', 'noopener,noreferrer')
      setClaudeMsg('已在新标签页打开授权。完成后复制页面上的 code#state，粘贴到下方并提交。')
    } catch {
      setClaudeError('网络错误')
    } finally {
      setClaudeBusy(false)
    }
  }

  const submitClaudePaste = async () => {
    setClaudeError('')
    setClaudeMsg('')
    setClaudeBusy(true)
    try {
      const res = await apiFetch('/v1/claude/oauth/exchange', {
        method: 'POST',
        body: JSON.stringify({ code: claudePaste }),
      }, currentAccount?.id)
      const data = await res.json()
      if (!res.ok) {
        setClaudeError(data.error || '换取 token 失败')
        return
      }
      setClaudePaste('')
      setClaudeMsg('Claude 订阅已连接')
      fetchClaudeStatus()
    } catch {
      setClaudeError('网络错误')
    } finally {
      setClaudeBusy(false)
    }
  }

  const disconnectClaude = async () => {
    if (!confirm('确定断开 Claude 订阅 OAuth？')) return
    setClaudeBusy(true)
    try {
      await apiFetch('/v1/claude/oauth/disconnect', { method: 'DELETE' }, currentAccount?.id)
      setClaudeConnected(false)
      setClaudeMsg('已断开')
    } finally {
      setClaudeBusy(false)
    }
  }

  const onChangePassword = async (e: FormEvent) => {
    e.preventDefault()
    setMessage('')
    setError('')
    if (newPassword !== confirmPassword) {
      setError('两次输入的新密码不一致')
      return
    }
    if (newPassword.length < 6) {
      setError('新密码至少 6 位')
      return
    }
    setSaving(true)
    const err = await changePassword(currentPassword, newPassword)
    setSaving(false)
    if (err) {
      setError(err)
      return
    }
    setCurrentPassword('')
    setNewPassword('')
    setConfirmPassword('')
    setMessage('密码已更新')
  }

  return (
    <div className="p-6">
      <div className="mb-6">
        <h2 className="text-base font-semibold text-foreground">Settings</h2>
        <p className="text-[13px] text-muted-foreground mt-0.5">账号与实例配置</p>
      </div>

      <div className="space-y-4 max-w-2xl">
        <div className="bg-card border border-border rounded-xl p-5">
          <h3 className="text-sm font-semibold text-foreground mb-4">当前账号</h3>
          <div className="space-y-2 text-[13px]">
            <p>
              <span className="text-muted-foreground">用户名：</span>
              {user?.username}
            </p>
            <p>
              <span className="text-muted-foreground">角色：</span>
              {user?.role}
            </p>
            <p>
              <span className="text-muted-foreground">邮箱：</span>
              {user?.email || '—'}
            </p>
          </div>
        </div>

		<div className="bg-card border border-border rounded-xl p-5">
          <h3 className="text-sm font-semibold text-foreground mb-2">导入 Markdown 对话</h3>
          <p className="text-[13px] text-muted-foreground leading-relaxed">
            在 Sessions 页可导入 <code className="text-[12px]">.md</code> 对话记录。支持
            <code className="mx-1 text-[12px]">## User / ## Assistant</code>、
            <code className="mx-1 text-[12px]">用户/助手</code>、
            <code className="mx-1 text-[12px]">**User**:</code> 等格式，导入后进入会话并可自动打标签。
          </p>
        </div>

        <div className="bg-card border border-border rounded-xl p-5">
          <h3 className="text-sm font-semibold text-foreground mb-2">语音输入</h3>
          <p className="text-[13px] text-muted-foreground leading-relaxed">
            Chat / Code 页支持麦克风口述。默认使用浏览器 Web Speech（推荐 Chrome）。若需完全开源离线识别，可配置
            <code className="mx-1 text-[12px]">asr.base_url</code>
            指向 Whisper 兼容服务（如 speaches / faster-whisper），或设置环境变量
            <code className="mx-1 text-[12px]">ASR_BASE_URL</code>。
          </p>
        </div>

        <div className="bg-card border border-border rounded-xl p-5">
          <h3 className="text-sm font-semibold text-foreground mb-2">Claude 订阅 OAuth</h3>
          <p className="text-[13px] text-muted-foreground leading-relaxed mb-3">
            对齐 OmniRoute / Claude Code：在新标签页登录 Anthropic，把授权页显示的
            <code className="mx-1 text-[12px]">code#state</code>
            粘贴回来。连接后可在 Providers 勾选「使用订阅 OAuth」。
          </p>
          {!claudeConfigured ? (
            <p className="text-[12px] text-amber-500">服务端未启用 claude_oauth（检查 codegateway.yaml）。</p>
          ) : (
            <div className="space-y-3">
              <p className="text-[13px]">
                状态：{claudeConnected ? (
                  <span className="text-green-500 font-medium">
                    已连接{claudeEmail ? `（${claudeEmail}）` : ''}
                  </span>
                ) : (
                  <span className="text-muted-foreground">未连接</span>
                )}
              </p>
              <div className="flex flex-wrap gap-2">
                {!claudeConnected ? (
                  <button
                    type="button"
                    disabled={claudeBusy}
                    onClick={startClaudePaste}
                    className="h-9 px-4 bg-primary text-primary-foreground rounded-lg text-[13px] font-medium hover:bg-primary/90 disabled:opacity-50"
                  >
                    打开授权页
                  </button>
                ) : (
                  <button
                    type="button"
                    disabled={claudeBusy}
                    onClick={disconnectClaude}
                    className="h-9 px-4 text-destructive border border-destructive/30 rounded-lg text-[13px] hover:bg-destructive/10 disabled:opacity-50"
                  >
                    断开连接
                  </button>
                )}
              </div>
              <div>
                <label className="block text-[12px] text-muted-foreground mb-1">粘贴 authorization code（code#state）</label>
                <div className="flex gap-2">
                  <input
                    value={claudePaste}
                    onChange={(e) => setClaudePaste(e.target.value)}
                    placeholder="xxxx#yyyy"
                    className="flex-1 h-9 px-3 bg-background border border-border rounded-lg text-[13px] focus:outline-none focus:ring-2 focus:ring-ring"
                  />
                  <button
                    type="button"
                    disabled={claudeBusy || !claudePaste.trim()}
                    onClick={submitClaudePaste}
                    className="h-9 px-3 border border-primary/30 text-primary rounded-lg text-[12px] hover:bg-primary/10 disabled:opacity-50"
                  >
                    提交
                  </button>
                </div>
              </div>
              {claudeError && <p className="text-[12px] text-red-500">{claudeError}</p>}
              {claudeMsg && <p className="text-[12px] text-green-500">{claudeMsg}</p>}
            </div>
          )}
        </div>

        <div className="bg-card border border-border rounded-xl p-5">
          <h3 className="text-sm font-semibold text-foreground mb-2">GitHub 仓库接入</h3>
          <p className="text-[13px] text-muted-foreground leading-relaxed">
            在 Code 页可授权 GitHub 并导入仓库为云端工作区。服务端需配置 OAuth App：
            <code className="mx-1 text-[12px]">github.client_id</code>/
            <code className="mx-1 text-[12px]">client_secret</code>
            （或环境变量 <code className="text-[12px]">GITHUB_CLIENT_ID</code> /
            <code className="text-[12px]">GITHUB_CLIENT_SECRET</code>），回调地址为
            <code className="mx-1 text-[12px]">/v1/github/callback</code>。
          </p>
        </div>

        <form onSubmit={onChangePassword} className="bg-card border border-border rounded-xl p-5 space-y-4">
          <h3 className="text-sm font-semibold text-foreground">修改密码</h3>
          <div>
            <label className="block text-[13px] font-medium text-foreground mb-1.5">当前密码</label>
            <input
              type="password"
              value={currentPassword}
              onChange={(e) => setCurrentPassword(e.target.value)}
              className="w-full h-9 px-3 bg-background border border-border rounded-lg text-[13px] focus:outline-none focus:ring-2 focus:ring-ring"
              required
            />
          </div>
          <div>
            <label className="block text-[13px] font-medium text-foreground mb-1.5">新密码</label>
            <input
              type="password"
              value={newPassword}
              onChange={(e) => setNewPassword(e.target.value)}
              className="w-full h-9 px-3 bg-background border border-border rounded-lg text-[13px] focus:outline-none focus:ring-2 focus:ring-ring"
              required
              minLength={6}
            />
          </div>
          <div>
            <label className="block text-[13px] font-medium text-foreground mb-1.5">确认新密码</label>
            <input
              type="password"
              value={confirmPassword}
              onChange={(e) => setConfirmPassword(e.target.value)}
              className="w-full h-9 px-3 bg-background border border-border rounded-lg text-[13px] focus:outline-none focus:ring-2 focus:ring-ring"
              required
              minLength={6}
            />
          </div>
          {error && <p className="text-[12px] text-red-500">{error}</p>}
          {message && <p className="text-[12px] text-green-500">{message}</p>}
          <div className="flex justify-end">
            <button
              type="submit"
              disabled={saving}
              className="h-9 px-5 bg-primary text-primary-foreground rounded-lg text-[13px] font-medium hover:bg-primary/90 disabled:opacity-50"
            >
              {saving ? '保存中…' : '更新密码'}
            </button>
          </div>
        </form>
      </div>
    </div>
  )
}
