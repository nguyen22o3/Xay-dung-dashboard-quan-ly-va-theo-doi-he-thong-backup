import { lazy, Suspense, useEffect, useState } from 'react'
import Layout from './Layout'
import Login from '../features/auth/Login'
import { clearSnapshotsCache } from '../api/dashboard'

import type { TabKey } from '../shared/types'
import type { Lang } from '../shared/language'

// Load charts, editors and history views only when their page is opened.
const Dashboard = lazy(() => import('../features/dashboard/Dashboard'))
const Home = lazy(() => import('../features/drive/Drive'))
const ServerPage = lazy(() => import('../features/server/Server'))
const CronJobs = lazy(() => import('../features/cron/CronJobs'))
const Settings = lazy(() => import('../features/settings/Settings'))
const Activity = lazy(() => import('../features/backups/Activity'))
const Available = lazy(() => import('../features/backups/Available'))

export default function App() {
  const [token, setToken] = useState<string | null>(() => localStorage.getItem('auth_token'))
  const [activeTab, setActiveTab] = useState<TabKey>(() => localStorage.getItem('dashboardLayoutVersion') === 'apex-v1'
    ? (localStorage.getItem('activeTab') as TabKey) || 'dashboard'
    : 'dashboard')
  const [isDark, setIsDark] = useState<boolean>(() => localStorage.getItem('isDarkMode') !== 'false')
  const [lang, setLang] = useState<Lang>(() => (localStorage.getItem('language') === 'en' ? 'en' : 'vi'))

  useEffect(() => { document.documentElement.lang = lang }, [lang])

  useEffect(() => {
    localStorage.setItem('dashboardLayoutVersion', 'apex-v1')
    localStorage.setItem('activeTab', activeTab)
  }, [activeTab])

  const onNavigate = (tab: TabKey) => {
    setActiveTab(tab)
    localStorage.setItem('activeTab', tab)
  }
  
  const onToggleDark = () => {
    const next = !isDark
    setIsDark(next)
    localStorage.setItem('isDarkMode', next.toString())
  }
  
  const onToggleLang = () => {
    const next: Lang = lang === 'vi' ? 'en' : 'vi'
    setLang(next)
    localStorage.setItem('language', next)
  }

  const onLogout = () => {
    clearSnapshotsCache()
    localStorage.removeItem('auth_token')
    setToken(null)
  }

  // Chưa đăng nhập → hiển thị trang Login
  if (!token) {
    return <Login isDark={isDark} lang={lang} onToggleLang={onToggleLang} onLogin={(t) => setToken(t)} />
  }

  const activePage = (() => {
    switch (activeTab) {
      case 'dashboard':
        return <Dashboard isDark={isDark} lang={lang} onNavigate={onNavigate} />
      case 'home':
        return <Home isDark={isDark} lang={lang} />
      case 'server':
        return <ServerPage isDark={isDark} lang={lang} />
      case 'jobs':
        return <CronJobs lang={lang} />
      case 'settings':
        return <Settings isDark={isDark} onToggleDark={onToggleDark} lang={lang} onToggleLang={onToggleLang} />
      case 'activity':
        return <Activity isDark={isDark} lang={lang} />
      case 'available':
        return <Available isDark={isDark} lang={lang} />
      default:
        return (
          <div className="animate-fade-in" style={{ padding: '40px', textAlign: 'center', color: isDark ? '#a0a0a0' : '#666' }}>
            <div style={{ fontSize: '48px', marginBottom: '20px' }}>🚧</div>
            <h2 style={{ margin: '0 0 10px 0', color: isDark ? '#e0e0e0' : '#333' }}>{lang === 'vi' ? 'Đang phát triển' : 'Under construction'}</h2>
            <p>{lang === 'vi' ? 'Chức năng này chưa được triển khai trong bản demo.' : 'This module is not yet implemented in this demo.'}</p>
          </div>
        )
    }
  })()

  return (
    <Layout
      activeTab={activeTab}
      onNavigate={onNavigate}
      isDark={isDark}
      onToggleDark={onToggleDark}
      lang={lang}
      onLogout={onLogout}
    >
      <div className="animate-fade-in" style={{ height: '100%' }}>
        <Suspense fallback={<div role="status" style={{ padding: '24px', color: 'var(--app-muted)' }}>{lang === 'vi' ? 'Đang tải trang…' : 'Loading page…'}</div>}>
          {activePage}
        </Suspense>
      </div>
    </Layout>
  )
}
