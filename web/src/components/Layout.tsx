import { Outlet, Link, useLocation, useNavigate } from 'react-router-dom'
import { useAccount } from '../context/AccountContext'
import { useAuth } from '../context/AuthContext'

const baseNavigation = [
  { name: 'Chat', href: '/', icon: '💬' },
  { name: 'Code', href: '/code', icon: '🛠️' },
  { name: 'Dashboard', href: '/dashboard', icon: '📊' },
  { name: 'Providers', href: '/providers', icon: '🔗' },
  { name: 'Sessions', href: '/sessions', icon: '📋' },
  { name: 'Tags', href: '/tags', icon: '🏷️' },
  { name: 'Accounts', href: '/accounts', icon: '👤', adminOnly: true },
  { name: 'Settings', href: '/settings', icon: '⚙️' },
]

export default function Layout() {
  const location = useLocation()
  const navigate = useNavigate()
  const { accounts, currentAccount, setCurrentAccountId, loading } = useAccount()
  const { user, isAdmin, logout } = useAuth()

  const navigation = baseNavigation.filter((item) => !item.adminOnly || isAdmin)
  const initial = (user?.username || currentAccount?.username || 'A').charAt(0).toUpperCase()

  const handleLogout = async () => {
    await logout()
    navigate('/login', { replace: true })
  }

  return (
    <div className="flex h-screen bg-background">
      <aside className="w-60 border-r border-border flex flex-col bg-card">
        <div className="h-14 flex items-center px-5 border-b border-border">
          <div className="flex items-center gap-2.5">
            <div className="w-7 h-7 rounded-lg bg-primary flex items-center justify-center">
              <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="white" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
                <polyline points="16 18 22 12 16 6" />
                <polyline points="8 6 2 12 8 18" />
              </svg>
            </div>
            <div>
              <h1 className="text-sm font-semibold text-foreground">CodeGateway</h1>
              <p className="text-[10px] text-muted-foreground">AI Agent + API Gateway</p>
            </div>
          </div>
        </div>

        <nav className="flex-1 px-3 py-3 space-y-0.5">
          {navigation.map((item) => {
            const isActive =
              item.href === '/'
                ? location.pathname === '/'
                : location.pathname === item.href || location.pathname.startsWith(item.href + '/')
            const codeActive =
              item.href === '/code' && (location.pathname === '/coder' || location.pathname.startsWith('/coder/'))
            const active = isActive || codeActive
            return (
              <Link
                key={item.name}
                to={item.href}
                className={`flex items-center gap-2.5 px-3 py-2 rounded-md text-[13px] font-medium transition-colors ${
                  active
                    ? 'bg-primary text-primary-foreground'
                    : 'text-muted-foreground hover:text-foreground hover:bg-accent'
                }`}
              >
                <span className="text-base">{item.icon}</span>
                {item.name}
              </Link>
            )
          })}
        </nav>

        <div className="p-3 border-t border-border space-y-2">
          {isAdmin && (
            <>
              <label className="px-3 text-[11px] font-medium text-muted-foreground uppercase tracking-wide">
                Impersonate Account
              </label>
              <select
                value={currentAccount?.id || ''}
                onChange={(e) => setCurrentAccountId(Number(e.target.value))}
                disabled={loading || accounts.length === 0}
                className="w-full h-9 px-3 bg-background border border-border rounded-lg text-[13px] text-foreground focus:outline-none focus:ring-2 focus:ring-ring"
              >
                {accounts.map((account) => (
                  <option key={account.id} value={account.id}>
                    {account.username}
                  </option>
                ))}
              </select>
            </>
          )}

          <div className="flex items-center gap-2.5 px-3 py-2 rounded-md">
            <div className="w-7 h-7 rounded-full bg-gradient-to-br from-blue-500 to-cyan-600 flex items-center justify-center">
              <span className="text-white text-xs font-medium">{initial}</span>
            </div>
            <div className="flex-1 min-w-0">
              <p className="text-[13px] font-medium text-foreground truncate">
                {user?.username || 'Loading...'}
              </p>
              <p className="text-[11px] text-muted-foreground truncate">
                {user?.role || user?.email || '—'}
              </p>
            </div>
          </div>

          <button
            onClick={handleLogout}
            className="w-full h-9 px-3 text-[13px] text-muted-foreground border border-border rounded-lg hover:bg-accent hover:text-foreground transition-colors"
          >
            退出登录
          </button>
        </div>
      </aside>

      <main className="flex-1 overflow-auto">
        <Outlet />
      </main>
    </div>
  )
}
