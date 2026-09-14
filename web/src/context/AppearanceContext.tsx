import { createContext, useCallback, useContext, useEffect, useRef, useState, type ReactNode } from 'react'
import { apiFetch } from './AuthContext'

export type HomeLayout = 'classic' | 'workbench'

const AppearanceContext = createContext<{
  homeLayout: HomeLayout
  loading: boolean
  error: string
  refresh: () => Promise<void>
  save: (layout: HomeLayout) => Promise<void>
} | null>(null)

export function AppearanceProvider({ children }: { children: ReactNode }) {
  const [homeLayout, setHomeLayout] = useState<HomeLayout>('classic')
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const generation = useRef(0)

  const refresh = useCallback(async () => {
    const current = ++generation.current
    try {
      const response = await apiFetch('/v1/ui/settings')
      if (!response.ok) throw new Error('主页设置加载失败')
      const data = await response.json()
      if (current !== generation.current) return
      setHomeLayout(data.home_layout === 'workbench' ? 'workbench' : 'classic')
      setError('')
    } catch (err) {
      if (current === generation.current) setError(err instanceof Error ? err.message : '主页设置加载失败')
    } finally {
      if (current === generation.current) setLoading(false)
    }
  }, [])

  useEffect(() => {
    void refresh()
    window.addEventListener('focus', refresh)
    return () => { ++generation.current; window.removeEventListener('focus', refresh) }
  }, [refresh])

  const save = async (layout: HomeLayout) => {
    ++generation.current
    const response = await apiFetch('/v1/admin/ui/settings', {
      method: 'PUT', body: JSON.stringify({ home_layout: layout }),
    })
    if (!response.ok) throw new Error('主页设置保存失败，请重试')
    const data = await response.json()
    setHomeLayout(data.home_layout === 'workbench' ? 'workbench' : 'classic')
    setError('')
  }

  return <AppearanceContext.Provider value={{ homeLayout, loading, error, refresh, save }}>{children}</AppearanceContext.Provider>
}

export function useAppearance() {
  const context = useContext(AppearanceContext)
  if (!context) throw new Error('useAppearance must be used within AppearanceProvider')
  return context
}
