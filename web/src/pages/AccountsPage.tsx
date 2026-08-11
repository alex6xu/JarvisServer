import { useState } from 'react'
import { useAccount } from '../context/AccountContext'

export default function AccountsPage() {
  const { accounts, currentAccount, setCurrentAccountId, createAccount, deleteAccount, refreshAccounts } = useAccount()
  const [showAdd, setShowAdd] = useState(false)
  const [username, setUsername] = useState('')
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)

  const handleCreate = async () => {
    if (!username.trim() || !password) return
    if (password.length < 6) {
      setError('密码至少 6 位')
      return
    }
    setSaving(true)
    setError('')
    const account = await createAccount({
      username: username.trim(),
      email: email.trim() || undefined,
      role: 'user',
      password,
    })
    setSaving(false)
    if (!account) {
      setError('创建失败，用户名可能已存在或密码无效')
      return
    }
    setShowAdd(false)
    setUsername('')
    setEmail('')
    setPassword('')
    setCurrentAccountId(account.id)
  }

  const handleDelete = async (id: number, name: string) => {
    if (name === 'admin') {
      alert('默认 admin 账号不可删除')
      return
    }
    if (!confirm(`删除账号「${name}」将同时删除其频道与会话数据，确认？`)) return
    const ok = await deleteAccount(id)
    if (!ok) {
      alert('删除失败')
    }
  }

  return (
    <div className="p-6">
      <div className="flex items-center justify-between mb-6">
        <div>
          <h2 className="text-base font-semibold text-foreground">Accounts</h2>
          <p className="text-[13px] text-muted-foreground mt-0.5">
            每个账号独立保存频道与会话，便于后续用户数据采集
          </p>
        </div>
        <div className="flex gap-2">
          <button
            onClick={() => refreshAccounts()}
            className="h-9 px-3 text-[13px] text-muted-foreground border border-border rounded-lg hover:bg-accent transition-colors"
          >
            Refresh
          </button>
          <button
            onClick={() => { setShowAdd(true); setError('') }}
            className="h-9 px-4 bg-primary text-primary-foreground rounded-lg text-[13px] font-medium hover:bg-primary/90 transition-colors"
          >
            Add Account
          </button>
        </div>
      </div>

      {showAdd && (
        <div className="fixed inset-0 bg-black/50 flex items-center justify-center z-50">
          <div className="bg-card border border-border rounded-xl p-6 w-full max-w-md">
            <h3 className="text-base font-semibold text-foreground mb-4">Create Account</h3>
            <div className="space-y-4">
              <div>
                <label className="block text-[13px] font-medium text-foreground mb-1.5">Username</label>
                <input
                  type="text"
                  value={username}
                  onChange={(e) => setUsername(e.target.value)}
                  placeholder="alice"
                  className="w-full h-9 px-3 bg-background border border-border rounded-lg text-[13px] text-foreground focus:outline-none focus:ring-2 focus:ring-ring"
                />
              </div>
              <div>
                <label className="block text-[13px] font-medium text-foreground mb-1.5">Email (optional)</label>
                <input
                  type="email"
                  value={email}
                  onChange={(e) => setEmail(e.target.value)}
                  placeholder="alice@example.com"
                  className="w-full h-9 px-3 bg-background border border-border rounded-lg text-[13px] text-foreground focus:outline-none focus:ring-2 focus:ring-ring"
                />
              </div>
              <div>
                <label className="block text-[13px] font-medium text-foreground mb-1.5">Password</label>
                <input
                  type="password"
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  placeholder="至少 6 位"
                  className="w-full h-9 px-3 bg-background border border-border rounded-lg text-[13px] text-foreground focus:outline-none focus:ring-2 focus:ring-ring"
                />
              </div>
              {error && <p className="text-[12px] text-destructive">{error}</p>}
            </div>
            <div className="flex justify-end gap-2 mt-6">
              <button
                onClick={() => setShowAdd(false)}
                className="h-9 px-4 text-[13px] text-muted-foreground border border-border rounded-md hover:bg-accent"
              >
                Cancel
              </button>
              <button
                onClick={handleCreate}
                disabled={!username.trim() || !password || saving}
                className="h-9 px-4 bg-primary text-primary-foreground rounded-lg text-[13px] font-medium disabled:opacity-50"
              >
                {saving ? 'Creating...' : 'Create'}
              </button>
            </div>
          </div>
        </div>
      )}

      <div className="bg-card border border-border rounded-xl overflow-hidden">
        <table className="w-full">
          <thead>
            <tr className="border-b border-border">
              <th className="px-4 py-3 text-left text-[12px] font-medium text-muted-foreground">ID</th>
              <th className="px-4 py-3 text-left text-[12px] font-medium text-muted-foreground">Username</th>
              <th className="px-4 py-3 text-left text-[12px] font-medium text-muted-foreground">Email</th>
              <th className="px-4 py-3 text-left text-[12px] font-medium text-muted-foreground">Role</th>
              <th className="px-4 py-3 text-left text-[12px] font-medium text-muted-foreground">Status</th>
              <th className="px-4 py-3 text-left text-[12px] font-medium text-muted-foreground">Actions</th>
            </tr>
          </thead>
          <tbody>
            {accounts.length === 0 ? (
              <tr>
                <td colSpan={6} className="px-4 py-12 text-center text-[13px] text-muted-foreground">
                  No accounts yet
                </td>
              </tr>
            ) : (
              accounts.map((account) => {
                const isActive = currentAccount?.id === account.id
                return (
                  <tr key={account.id} className="border-b border-border hover:bg-accent/50">
                    <td className="px-4 py-3 text-[13px] text-muted-foreground tabular-nums">{account.id}</td>
                    <td className="px-4 py-3 text-[13px] text-foreground font-medium">{account.username}</td>
                    <td className="px-4 py-3 text-[13px] text-muted-foreground">{account.email || '—'}</td>
                    <td className="px-4 py-3 text-[13px] text-muted-foreground">{account.role}</td>
                    <td className="px-4 py-3">
                      {isActive ? (
                        <span className="inline-flex items-center gap-1.5 text-[12px] font-medium text-success">
                          <span className="w-1.5 h-1.5 rounded-full bg-success"></span>
                          Active
                        </span>
                      ) : (
                        <span className="text-[12px] text-muted-foreground">Idle</span>
                      )}
                    </td>
                    <td className="px-4 py-3">
                      <div className="flex items-center gap-2">
                        {!isActive && (
                          <button
                            onClick={() => setCurrentAccountId(account.id)}
                            className="text-[13px] text-primary font-medium"
                          >
                            Switch
                          </button>
                        )}
                        {account.username !== 'admin' && (
                          <button
                            onClick={() => handleDelete(account.id, account.username)}
                            className="text-[13px] text-destructive font-medium"
                          >
                            Delete
                          </button>
                        )}
                      </div>
                    </td>
                  </tr>
                )
              })
            )}
          </tbody>
        </table>
      </div>
    </div>
  )
}
