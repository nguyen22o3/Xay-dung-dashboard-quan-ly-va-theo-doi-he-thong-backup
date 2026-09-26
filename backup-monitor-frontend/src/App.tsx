import { useState } from 'react'
import Layout from './components/Layout'
import Home from './pages/Home'
import Settings from './pages/Settings'
import Activity from './pages/Activity'
import Available from './pages/Available'
import ServerPage from './pages/Server'
import Login from './pages/Login'

import type { TabKey } from './types'
import type { Lang } from './language'

export default function App() {
  const [token, setToken] = useState<string | null>(() => localStorage.getItem('auth_token'))
  const [activeTab, setActiveTab] = useState<TabKey>(() => (localStorage.getItem('activeTab') as TabKey) || 'server')
  const [isDark, setIsDark] = useState<boolean>(() => localStorage.getItem('isDarkMode') === 'true')
  const [lang, setLang] = useState<Lang>(() => (localStorage.getItem('language') === 'en' ? 'en' : 'vi'))

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
    localStorage.removeItem('auth_token')
    setToken(null)
  }

  // Chưa đăng nhập → hiển thị trang Login
  if (!token) {
    return <Login isDark={isDark} onLogin={(t) => setToken(t)} />
  }

  return (
    <Layout
      activeTab={activeTab}
      onNavigate={onNavigate}
      isDark={isDark}
      lang={lang}
      onLogout={onLogout}
    >
      <div className="animate-fade-in" style={{ height: '100%' }}>
        <div style={{ display: activeTab === 'home' ? 'block' : 'none', height: '100%' }}>
          <Home isDark={isDark} lang={lang} />
        </div>
        
        <div style={{ display: activeTab === 'settings' ? 'block' : 'none', height: '100%' }}>
          <Settings isDark={isDark} onToggleDark={onToggleDark} lang={lang} onToggleLang={onToggleLang} />
        </div>
        
        <div style={{ display: activeTab === 'activity' ? 'block' : 'none', height: '100%' }}>
          <Activity isDark={isDark} lang={lang} />
        </div>
        
        <div style={{ display: activeTab === 'available' ? 'block' : 'none', height: '100%' }}>
          <Available isDark={isDark} lang={lang} />
        </div>

        <div style={{ display: activeTab === 'server' ? 'block' : 'none', height: '100%' }}>
          <ServerPage isDark={isDark} lang={lang} />
        </div>

        {activeTab !== 'home' && activeTab !== 'server' && activeTab !== 'settings' && activeTab !== 'activity' && activeTab !== 'available' && (
          <div className="animate-fade-in" style={{ padding: '40px', textAlign: 'center', color: isDark ? '#a0a0a0' : '#666' }}>
            <div style={{ fontSize: '48px', marginBottom: '20px' }}>🚧</div>
            <h2 style={{ margin: '0 0 10px 0', color: isDark ? '#e0e0e0' : '#333' }}>Under Construction</h2>
            <p>This module is currently not implemented in this demo.</p>
          </div>
        )}
      </div>
    </Layout>
  )
}
