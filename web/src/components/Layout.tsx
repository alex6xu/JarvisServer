import { Outlet, useLocation } from 'react-router-dom'
import { useAccount } from '../context/AccountContext'
import { useAppearance } from '../context/AppearanceContext'
import WorkbenchSidebar from './WorkbenchSidebar'
import { navigationGroups, isNavigationActive } from '../lib/navigation'

export default function Layout() {
  const { pathname } = useLocation()
  const { currentAccount } = useAccount()
  const { homeLayout, loading } = useAppearance()
  const page = navigationGroups.flatMap((group) => group.items).find((item) => isNavigationActive(pathname, item.href))

  if (loading) return <div role="status" className="min-h-screen bg-background flex items-center justify-center text-muted-foreground text-sm">加载中...</div>

  return (
    <div className={`control-shell workbench-shell ${homeLayout === 'workbench' ? 'workbench-theme' : 'control-dark'}`}>
      <a className="skip-link" href="#main-content">跳到主要内容</a>
      <WorkbenchSidebar />
      <div className="control-workspace">
        <header className="control-topbar">
          <div className="control-breadcrumb"><span>JarvisServer</span><span aria-hidden="true">/</span><strong>{page?.label || '工作区'}</strong></div>
          <span className="control-account-context" title="当前操作账号">{currentAccount?.username || '未选择账号'}</span>
        </header>
        <main id="main-content" tabIndex={-1} className="workbench-main control-content min-w-0 flex-1 overflow-auto">
          <Outlet />
        </main>
      </div>
    </div>
  )
}
