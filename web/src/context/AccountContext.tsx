import { createContext, useContext, useEffect, useState, ReactNode } from 'react'
import { Account, apiFetch, authHeaders, useAuth } from './AuthContext'

export { apiFetch, authHeaders }
export type { Account }

interface AccountContextValue {
  accounts: Account[]
  currentAccount: Account | null
  loading: boolean
  setCurrentAccountId: (id: number) => void
  refreshAccounts: () => Promise<void>
  createAccount: (data: {
    username: string
    email?: string
    role?: string
    password: string
  }) => Promise<Account | null>
  deleteAccount: (id: number) => Promise<boolean>
}

const STORAGE_KEY = 'codegateway_account_id'

const AccountContext = createContext<AccountContextValue | null>(null)

export function AccountProvider({ children }: { children: ReactNode }) {
  const { user, token, isAdmin, loading: authLoading } = useAuth()
  const [accounts, setAccounts] = useState<Account[]>([])
  const [currentAccount, setCurrentAccount] = useState<Account | null>(null)
  const [loading, setLoading] = useState(true)

  const refreshAccounts = async () => {
    if (!token || !user) {
      setAccounts([])
      setCurrentAccount(null)
      setLoading(false)
      return
    }

    try {
      if (isAdmin) {
        const response = await apiFetch('/v1/admin/accounts')
        if (response.ok) {
          const data = await response.json()
          const list: Account[] = data.accounts || []
          setAccounts(list)

          const storedId = Number(localStorage.getItem(STORAGE_KEY) || '0')
          const selected =
            list.find((a) => a.id === storedId) ||
            list.find((a) => a.id === user.id) ||
            list[0] ||
            null

          if (selected) {
            localStorage.setItem(STORAGE_KEY, String(selected.id))
            setCurrentAccount(selected)
          } else {
            setCurrentAccount(user)
          }
          return
        }
      }

      // Non-admin: only self
      setAccounts([user])
      setCurrentAccount(user)
      localStorage.setItem(STORAGE_KEY, String(user.id))
    } catch (error) {
      console.error('Failed to load accounts:', error)
      setAccounts(user ? [user] : [])
      setCurrentAccount(user)
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => {
    if (authLoading) return
    setLoading(true)
    refreshAccounts()
  }, [authLoading, token, user?.id, isAdmin])

  const setCurrentAccountId = (id: number) => {
    if (!isAdmin) return
    const account = accounts.find((a) => a.id === id)
    if (!account) return
    localStorage.setItem(STORAGE_KEY, String(id))
    setCurrentAccount(account)
  }

  const createAccount = async (data: {
    username: string
    email?: string
    role?: string
    password: string
  }) => {
    try {
      const response = await apiFetch('/v1/admin/accounts', {
        method: 'POST',
        body: JSON.stringify(data),
      })
      if (!response.ok) {
        const err = await response.json().catch(() => ({}))
        throw new Error(err.error || 'failed to create account')
      }
      const result = await response.json()
      await refreshAccounts()
      return result.account as Account
    } catch (error) {
      console.error('Failed to create account:', error)
      return null
    }
  }

  const deleteAccount = async (id: number) => {
    try {
      const response = await apiFetch(`/v1/admin/accounts/${id}`, { method: 'DELETE' })
      if (!response.ok) {
        const err = await response.json().catch(() => ({}))
        throw new Error(err.error || 'failed to delete account')
      }
      if (currentAccount?.id === id) {
        localStorage.setItem(STORAGE_KEY, String(user?.id || ''))
      }
      await refreshAccounts()
      return true
    } catch (error) {
      console.error('Failed to delete account:', error)
      return false
    }
  }

  return (
    <AccountContext.Provider
      value={{
        accounts,
        currentAccount,
        loading: authLoading || loading,
        setCurrentAccountId,
        refreshAccounts,
        createAccount,
        deleteAccount,
      }}
    >
      {children}
    </AccountContext.Provider>
  )
}

export function useAccount() {
  const ctx = useContext(AccountContext)
  if (!ctx) {
    throw new Error('useAccount must be used within AccountProvider')
  }
  return ctx
}
