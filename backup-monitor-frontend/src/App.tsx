import { useEffect, useState } from 'react'
import Layout from './components/Layout'
import Home from './pages/Home'
import Settings from './pages/Settings'
import Activity from './pages/Activity'
import Available from './pages/Available'
import ServerPage from './pages/Server'
import Login from './pages/Login'
import Dashboard from './pages/Dashboard'
import { clearSnapshotsCache, prefetchSnapshots } from './api'

import type { TabKey } from './types'
import type { Lang } from './language'

export default function App() {
  const [token, setToken] = useState<string | null>(() => localStorage.getItem('auth_token'))
  const [activeTab, setActiveTab] = useState<TabKey>(() => localStorage.getItem('dashboardLayoutVersion') === 'apex-v1'
    ? (localStorage.getItem('activeTab') as TabKey) || 'dashboard'
    : 'dashboard')
  const [isDark, setIsDark] = useState<boolean>(() => localStorage.getItem('isDarkMode') !== 'false')
  const [lang, setLang] = useState<Lang>(() => (localStorage.getItem('language') === 'en' ? 'en' : 'vi'))

  useEffect(() => {
    localStorage.setItem('dashboardLayoutVersion', 'apex-v1')
    localStorage.setItem('activeTab', activeTab)
  }, [activeTab])

  useEffect(() => {
    if (!token) return
    const timer = window.setTimeout(prefetchSnapshots, 1500)
    return () => window.clearTimeout(timer)
  }, [token])

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
    return <Login isDark={isDark} onLogin={(t) => setToken(t)} />
  }

  const activePage = (() => {
    switch (activeTab) {
      case 'dashboard':
        return <Dashboard isDark={isDark} lang={lang} onNavigate={onNavigate} />
      case 'home':
        return <Home isDark={isDark} lang={lang} />
      case 'server':
        return <ServerPage isDark={isDark} lang={lang} />
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
            <h2 style={{ margin: '0 0 10px 0', color: isDark ? '#e0e0e0' : '#333' }}>Under Construction</h2>
            <p>This module is currently not implemented in this demo.</p>
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
        {activePage}
      </div>
    </Layout>
  )
}
