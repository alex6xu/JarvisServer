import { createContext, useContext, useEffect, useState, ReactNode } from 'react'

export interface Account {
  id: number
  username: string
  email: string
  role: string
  quota: number
  used_quota: number
  created_at: string
  updated_at: string
}

interface AuthContextValue {
  user: Account | null
  token: string | null
  loading: boolean
  isAdmin: boolean
  login: (username: string, password: string) => Promise<string | null>
  register: (username: string, email: string, password: string) => Promise<string | null>
  logout: () => Promise<void>
  changePassword: (currentPassword: string, newPassword: string) => Promise<string | null>
  refreshMe: () => Promise<void>
}

const TOKEN_KEY = 'codegateway_auth_token'
const ACCOUNT_KEY = 'codegateway_account_id'

const AuthContext = createContext<AuthContextValue | null>(null)

export function getAuthToken(): string | null {
  return localStorage.getItem(TOKEN_KEY)
}

export function authHeaders(accountId?: number | null, opts?: { json?: boolean }): HeadersInit {
  const headers: Record<string, string> = {}
  if (opts?.json !== false) {
    headers['Content-Type'] = 'application/json'
  }
  const token = getAuthToken()
  if (token) {
    headers['Authorization'] = `Bearer ${token}`
  }
  if (accountId) {
    headers['X-Account-ID'] = String(accountId)
  } else {
    const stored = localStorage.getItem(ACCOUNT_KEY)
    if (stored) {
      headers['X-Account-ID'] = stored
    }
  }
  return headers
}

export async function apiFetch(input: string, init: RequestInit = {}, accountId?: number | null): Promise<Response> {
  const isFormData = typeof FormData !== 'undefined' && init.body instanceof FormData
  const headers = {
    ...authHeaders(accountId, { json: !isFormData }),
    ...(init.headers || {}),
  }
  // Let the browser set multipart boundary for FormData
  if (isFormData && headers && typeof headers === 'object') {
    delete (headers as Record<string, string>)['Content-Type']
  }
  return fetch(input, { ...init, headers })
}

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<Account | null>(null)
  const [token, setToken] = useState<string | null>(getAuthToken())
  const [loading, setLoading] = useState(true)

  const applySession = (nextToken: string, account: Account) => {
    localStorage.setItem(TOKEN_KEY, nextToken)
    localStorage.setItem(ACCOUNT_KEY, String(account.id))
    setToken(nextToken)
    setUser(account)
  }

  const clearSession = () => {
    localStorage.removeItem(TOKEN_KEY)
    localStorage.removeItem(ACCOUNT_KEY)
    setToken(null)
    setUser(null)
  }

  const refreshMe = async () => {
    const current = getAuthToken()
    if (!current) {
      setUser(null)
      setLoading(false)
      return
    }
    try {
      const response = await fetch('/v1/auth/me', {
        headers: { Authorization: `Bearer ${current}` },
      })
      if (!response.ok) {
        clearSession()
        return
      }
      const data = await response.json()
      const account = data.account as Account
      setUser(account)
      localStorage.setItem(ACCOUNT_KEY, String(account.id))
      setToken(current)
    } catch (error) {
      console.error('Failed to refresh session:', error)
      clearSession()
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    refreshMe()
  }, [])

  const login = async (username: string, password: string) => {
    try {
      const response = await fetch('/v1/auth/login', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ username, password }),
      })
      const data = await response.json().catch(() => ({}))
      if (!response.ok) {
        return (data.error as string) || '登录失败'
      }
      applySession(data.token, data.account)
      return null
    } catch {
      return '网络错误，请稍后重试'
    }
  }

  const register = async (username: string, email: string, password: string) => {
    try {
      const response = await fetch('/v1/auth/register', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ username, email, password }),
      })
      const data = await response.json().catch(() => ({}))
      if (!response.ok) {
        return (data.error as string) || '注册失败'
      }
      applySession(data.token, data.account)
      return null
    } catch {
      return '网络错误，请稍后重试'
    }
  }

  const logout = async () => {
    const current = getAuthToken()
    if (current) {
      try {
        await fetch('/v1/auth/logout', {
          method: 'POST',
          headers: { Authorization: `Bearer ${current}` },
        })
      } catch {
        // ignore
      }
    }
    clearSession()
  }

  const changePassword = async (currentPassword: string, newPassword: string) => {
    try {
      const response = await apiFetch('/v1/auth/change-password', {
        method: 'POST',
        body: JSON.stringify({
          current_password: currentPassword,
          new_password: newPassword,
        }),
      })
      const data = await response.json().catch(() => ({}))
      if (!response.ok) {
        return (data.error as string) || '修改密码失败'
      }
      if (data.token && data.account) {
        applySession(data.token, data.account)
      }
      return null
    } catch {
      return '网络错误，请稍后重试'
    }
  }

  return (
    <AuthContext.Provider
      value={{
        user,
        token,
        loading,
        isAdmin: user?.role === 'admin',
        login,
        register,
        logout,
        changePassword,
        refreshMe,
      }}
    >
      {children}
    </AuthContext.Provider>
  )
}

export function useAuth() {
  const ctx = useContext(AuthContext)
  if (!ctx) {
    throw new Error('useAuth must be used within AuthProvider')
  }
  return ctx
}
