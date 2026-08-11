import { useState, useEffect } from 'react'
import { apiFetch, useAccount } from '../context/AccountContext'

interface Stats {
  totalSessions: number
  totalMessages: number
  totalTokens: number
  totalCost: number
  activeProviders: number
}

export default function DashboardPage() {
  const { currentAccount } = useAccount()
  const [stats, setStats] = useState<Stats>({
    totalSessions: 0,
    totalMessages: 0,
    totalTokens: 0,
    totalCost: 0,
    activeProviders: 0,
  })

  useEffect(() => {
    if (currentAccount) {
      fetchStats()
    }
  }, [currentAccount?.id])

  const fetchStats = async () => {
    try {
      const response = await apiFetch('/v1/admin/stats', {}, currentAccount?.id)
      if (response.ok) {
        const data = await response.json()
        setStats(data)
      }
    } catch (error) {
      console.error('Failed to fetch stats:', error)
    }
  }

  const statCards = [
    { name: 'Total Sessions', value: stats.totalSessions, icon: '📋', color: 'bg-blue-500/20 text-blue-400' },
    { name: 'Total Messages', value: stats.totalMessages, icon: '💬', color: 'bg-purple-500/20 text-purple-400' },
    { name: 'Total Tokens', value: stats.totalTokens.toLocaleString(), icon: '🔤', color: 'bg-amber-500/20 text-amber-400' },
    { name: 'Total Cost', value: `$${stats.totalCost.toFixed(2)}`, icon: '💰', color: 'bg-green-500/20 text-green-400' },
    { name: 'Active Providers', value: stats.activeProviders, icon: '🔗', color: 'bg-cyan-500/20 text-cyan-400' },
  ]

  return (
    <div className="p-6">
      <div className="mb-6">
        <h2 className="text-base font-semibold text-foreground">Dashboard</h2>
        <p className="text-[13px] text-muted-foreground mt-0.5">
          Overview for {currentAccount?.username || 'current account'}
        </p>
      </div>

      {/* Stats Grid */}
      <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 xl:grid-cols-5 gap-3 mb-6">
        {statCards.map((stat) => (
          <div
            key={stat.name}
            className="bg-card border border-border rounded-xl p-4 hover:border-border/80 transition-colors"
          >
            <div className="flex items-center justify-between mb-3">
              <span className="text-[12px] font-medium text-muted-foreground">{stat.name}</span>
              <div className={`w-8 h-8 rounded-lg ${stat.color} flex items-center justify-center`}>
                <span className="text-base">{stat.icon}</span>
              </div>
            </div>
            <p className="text-xl font-semibold text-foreground tabular-nums">{stat.value}</p>
          </div>
        ))}
      </div>

      {/* Charts placeholder */}
      <div className="grid grid-cols-1 lg:grid-cols-2 gap-4">
        <div className="bg-card border border-border rounded-xl p-5">
          <h3 className="text-sm font-semibold text-foreground mb-4">Usage Over Time</h3>
          <div className="h-56 flex items-center justify-center text-muted-foreground text-[13px] border border-dashed border-border rounded-lg">
            Chart placeholder
          </div>
        </div>

        <div className="bg-card border border-border rounded-xl p-5">
          <h3 className="text-sm font-semibold text-foreground mb-4">Model Distribution</h3>
          <div className="h-56 flex items-center justify-center text-muted-foreground text-[13px] border border-dashed border-border rounded-lg">
            Chart placeholder
          </div>
        </div>
      </div>
    </div>
  )
}
