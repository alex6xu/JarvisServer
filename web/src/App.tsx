import { lazy, Suspense } from 'react'
import { BrowserRouter as Router, Routes, Route, Navigate } from 'react-router-dom'
import { AuthProvider, useAuth } from './context/AuthContext'
import { AccountProvider } from './context/AccountContext'
import Layout from './components/Layout'
import ChatPage from './pages/ChatPage'
import CoderPage from './pages/CoderPage'
import DashboardPage from './pages/DashboardPage'
import StockPage from './pages/StockPage'
import ChannelsPage from './pages/ChannelsPage'
import SessionsPage from './pages/SessionsPage'
import TagsPage from './pages/TagsPage'
import SettingsPage from './pages/SettingsPage'
import AccountsPage from './pages/AccountsPage'
import LoginPage from './pages/LoginPage'
import RegisterPage from './pages/RegisterPage'

const CryptoDetailPage = lazy(() => import('./pages/CryptoDetailPage'))

function ProtectedApp() {
  const { user, loading, isAdmin } = useAuth()

  if (loading) {
    return (
      <div className="min-h-screen flex items-center justify-center bg-background text-[13px] text-muted-foreground">
        加载中…
      </div>
    )
  }

  if (!user) {
    return <Navigate to="/login" replace />
  }

  return (
    <AccountProvider>
      <Routes>
        <Route path="/" element={<Layout />}>
          <Route index element={<ChatPage />} />
          <Route path="code" element={<CoderPage />} />
          <Route path="coder" element={<CoderPage />} />
          <Route path="dashboard" element={<DashboardPage />} />
          <Route path="stock" element={<StockPage />} />
          <Route path="stock/crypto/:asset" element={(
            <Suspense fallback={<div className="flex min-h-full items-center justify-center text-[11px] text-muted-foreground">加载行情详情...</div>}>
              <CryptoDetailPage />
            </Suspense>
          )} />
          <Route path="providers" element={<ChannelsPage />} />
          <Route path="channels" element={<Navigate to="/providers" replace />} />
          <Route path="sessions" element={<SessionsPage />} />
          <Route path="tags" element={<TagsPage />} />
          <Route path="agent-tasks" element={<Navigate to="/code" replace />} />
          <Route
            path="accounts"
            element={isAdmin ? <AccountsPage /> : <Navigate to="/" replace />}
          />
          <Route path="settings" element={<SettingsPage />} />
        </Route>
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
    </AccountProvider>
  )
}

function App() {
  return (
    <AuthProvider>
      <Router>
        <Routes>
          <Route path="/login" element={<LoginPage />} />
          <Route path="/register" element={<RegisterPage />} />
          <Route path="/*" element={<ProtectedApp />} />
        </Routes>
      </Router>
    </AuthProvider>
  )
}

export default App
