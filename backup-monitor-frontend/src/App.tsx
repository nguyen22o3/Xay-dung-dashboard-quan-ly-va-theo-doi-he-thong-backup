import { Button } from "./ui"
import { Database, Activity, ArrowUpRight, Archive, CalendarClock, Cloud, LayoutDashboard, LogOut, Menu, Moon, PanelLeftClose, PanelLeftOpen, RefreshCw, Search, Server, Settings, Sun, X } from "lucide-react"
import { makeTheme } from "./shared"
import { useEffect, useState, useRef, lazy, Suspense } from "react"
import { apiErrorMessage, createAdministrator, fetchAuthStatus, login, refreshData, useServerStatus, clearSnapshotsCache } from "./api"
import { tr, localizeMessage } from "./i18n"
import type { Lang } from "./i18n"
import type { ReactNode } from "react"
import type { LucideIcon } from "lucide-react"
import type { TabKey } from "./shared"

interface LoginProps {
  isDark: boolean
  lang: Lang
  onToggleLang: () => void
  onLogin: (token: string) => void
}

export  function Login({ isDark, lang, onToggleLang, onLogin }: LoginProps) {
  const theme = makeTheme(isDark)
  const { bg, cardBg, cardBorder, textPrimary, textSecondary } = theme
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [confirmation, setConfirmation] = useState('')
  const [setupRequired, setSetupRequired] = useState<boolean | null>(null)
  const [checking, setChecking] = useState(true)
  const [statusAttempt, setStatusAttempt] = useState(0)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    let mounted = true
    fetchAuthStatus().then(status => {
      if (mounted) setSetupRequired(status.setupRequired)
    }).catch(statusError => {
      if (mounted) setError(apiErrorMessage(statusError, tr('vi', 'accountStatusError')))
    }).finally(() => {
      if (mounted) setChecking(false)
    })
    return () => { mounted = false }
  }, [statusAttempt])

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault()
    if (setupRequired === null || checking || loading) return
    setError('')
    if (setupRequired && (password !== confirmation || new TextEncoder().encode(password).length < 12 || new TextEncoder().encode(password).length > 72)) {
      setError(tr(lang, password !== confirmation ? 'passwordMismatch' : 'passwordLengthError'))
      return
    }
    setLoading(true)
    try {
      if (setupRequired) {
        await createAdministrator(username, password)
        setSetupRequired(false)
        setConfirmation('')
      }
      const token = await login(username, password)
      localStorage.setItem('auth_token', token)
      onLogin(token)
    } catch (loginError: unknown) {
      setError(apiErrorMessage(loginError, tr(lang, 'loginConnectionError'), lang))
      // Another tab may already have completed setup, or its response was lost.
      if (setupRequired) {
        try { setSetupRequired((await fetchAuthStatus()).setupRequired) } catch { /* Keep the current form; retry remains explicit. */ }
      }
    } finally {
      setLoading(false)
    }
  }

  return (
    <div className="login-page" data-theme={isDark ? 'dark' : 'light'} style={{
      minHeight: '100vh',
      background: bg,
      display: 'flex',
      alignItems: 'center',
      justifyContent: 'center',
    }}>
      <div style={{
        background: cardBg,
        border: `1px solid ${cardBorder}`,
        borderRadius: 16,
        padding: '48px 40px',
        width: '100%',
        maxWidth: 400,
        boxShadow: '0 8px 32px rgba(0,0,0,0.2)',
      }}>
        {/* Logo / Title */}
        <div style={{ textAlign: 'center', marginBottom: 32 }}>
          <div style={{
            width: 56,
            height: 56,
            background: theme.primary,
            color: theme.onPrimary,
            borderRadius: 14,
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            margin: '0 auto 16px',
            fontSize: 24,
          }}>
            <Database size={28} />
          </div>
          <h1 style={{ color: textPrimary, fontSize: 22, fontWeight: 700, margin: 0 }}>
            {tr(lang, setupRequired ? 'setupTitle' : 'loginTitle')}
          </h1>
          <p style={{ color: textSecondary, fontSize: 14, marginTop: 6 }}>
            {tr(lang, checking ? 'checkingAccount' : setupRequired ? 'setupDescription' : 'loginDescription')}
          </p>
          <div role="group" aria-label={lang === 'vi' ? 'Ngôn ngữ' : 'Language'} style={{ display: 'flex', justifyContent: 'center', gap: 8 }}>
            <Button type="button" theme={theme} aria-pressed={lang === 'vi'} onClick={() => { if (lang !== 'vi') onToggleLang() }}>Tiếng Việt</Button>
            <Button type="button" theme={theme} aria-pressed={lang === 'en'} onClick={() => { if (lang !== 'en') onToggleLang() }}>English</Button>
          </div>
        </div>

        <form onSubmit={handleSubmit}>
          {/* Username */}
          <div style={{ marginBottom: 16 }}>
            <label htmlFor="admin-username" style={{ display: 'block', color: textSecondary, fontSize: 13, marginBottom: 6, fontWeight: 500 }}>
              {tr(lang, 'username')}
            </label>
            <input
              id="admin-username"
              type="text"
              autoComplete="username"
              disabled={checking || setupRequired === null || loading}
              pattern={setupRequired ? '[a-zA-Z0-9][a-zA-Z0-9_.\\-]{2,63}' : undefined}
              title={setupRequired ? tr(lang, 'usernameHint') : undefined}
              value={username}
              onChange={e => setUsername(e.target.value)}
              placeholder={tr(lang, 'usernamePlaceholder')}
              required
              style={{
                width: '100%',
                padding: '10px 14px',
                background: bg,
                border: `1px solid ${cardBorder}`,
                borderRadius: 8,
                color: textPrimary,
                fontSize: 14,
                outline: 'none',
                boxSizing: 'border-box',
              }}
            />
          </div>

          {/* Password */}
          <div style={{ marginBottom: 24 }}>
            <label htmlFor="admin-password" style={{ display: 'block', color: textSecondary, fontSize: 13, marginBottom: 6, fontWeight: 500 }}>
              {tr(lang, 'password')}
            </label>
            <input
              id="admin-password"
              type="password"
              autoComplete={setupRequired ? 'new-password' : 'current-password'}
              disabled={checking || setupRequired === null || loading}
              value={password}
              onChange={e => setPassword(e.target.value)}
              placeholder={tr(lang, 'passwordPlaceholder')}
              required
              style={{
                width: '100%',
                padding: '10px 14px',
                background: bg,
                border: `1px solid ${cardBorder}`,
                borderRadius: 8,
                color: textPrimary,
                fontSize: 14,
                outline: 'none',
                boxSizing: 'border-box',
              }}
            />
          </div>

          {setupRequired && <div style={{ marginBottom: 24 }}>
            <label htmlFor="admin-confirmation" style={{ display: 'block', color: textSecondary, fontSize: 13, marginBottom: 6, fontWeight: 500 }}>{tr(lang, 'confirmPassword')}</label>
            <input id="admin-confirmation" type="password" autoComplete="new-password" required
              value={confirmation} onChange={e => setConfirmation(e.target.value)} disabled={loading}
              style={{ width: '100%', padding: '10px 14px', background: bg, border: `1px solid ${cardBorder}`, borderRadius: 8, color: textPrimary, fontSize: 14, boxSizing: 'border-box' }} />
            <p style={{ color: textSecondary, fontSize: 12, lineHeight: 1.6 }}>{tr(lang, 'passwordHint')}</p>
          </div>}

          {/* Error */}
          {error && (
            <div style={{
              background: 'rgba(239,68,68,0.1)',
              border: '1px solid rgba(239,68,68,0.3)',
              borderRadius: 8,
              padding: '10px 14px',
              color: '#ef4444',
              fontSize: 13,
              marginBottom: 16,
            }}>
              <span role="alert">{localizeMessage(error, lang)}</span>
            </div>
          )}

          {!checking && setupRequired === null && <Button theme={theme} type="button" onClick={() => {
            setError('')
            setChecking(true)
            setStatusAttempt(value => value + 1)
          }} style={{ width: '100%', marginBottom: 12 }}>{tr(lang, 'retryConnection')}</Button>}

          {/* Submit */}
          <Button theme={theme} variant="primary"
            type="submit"
            disabled={loading || checking || setupRequired === null}
            style={{ width: '100%' }}
          >
            {tr(lang, loading ? (setupRequired ? 'creatingAccount' : 'signingIn') : setupRequired ? 'createAndSignIn' : 'signIn')}
          </Button>
        </form>
      </div>
    </div>
  )
}

type NavItem = { tab: TabKey; icon: LucideIcon; vi: string; en: string }

const overviewItems: NavItem[] = [
  { tab: 'dashboard', icon: LayoutDashboard, vi: 'Tổng quan', en: 'Overview' },
  { tab: 'server', icon: Server, vi: 'Máy chủ', en: 'Server' },
  { tab: 'home', icon: Cloud, vi: 'Google Drive', en: 'Google Drive' },
]

const managementItems: NavItem[] = [
  { tab: 'available', icon: Archive, vi: 'Bản sao lưu', en: 'Backups' },
  { tab: 'jobs', icon: CalendarClock, vi: 'Cron', en: 'Cron' },
  { tab: 'activity', icon: Activity, vi: 'Nhật ký hoạt động', en: 'Activity log' },
  { tab: 'settings', icon: Settings, vi: 'Cài đặt', en: 'Settings' },
]

export  function Layout({ activeTab, onNavigate, isDark, onToggleDark, lang, children, onLogout }: {
  activeTab: TabKey
  onNavigate: (tab: TabKey) => void
  isDark: boolean
  onToggleDark: () => void
  lang: Lang
  children: ReactNode
  onLogout?: () => void
}) {
  const [collapsed, setCollapsed] = useState(false)
  const [mobileOpen, setMobileOpen] = useState(false)
  const [search, setSearch] = useState('')
  const [isRefreshing, setIsRefreshing] = useState(false)
  const [refreshError, setRefreshError] = useState('')
  const searchRef = useRef<HTMLInputElement>(null)
  const contentRef = useRef<HTMLElement>(null)
  const server = useServerStatus(30000)
  const vi = lang === 'vi'
  const allItems = [...overviewItems, ...managementItems]
  const activeItem = allItems.find((item) => item.tab === activeTab)
  const results = search.trim()
    ? allItems.filter((item) => `${item.vi} ${item.en} ${item.tab === 'jobs' ? 'cronjob cron cấu hình lưu trữ storage configuration' : ''}`.toLowerCase().includes(search.trim().toLowerCase()))
    : []

  useEffect(() => {
    // A new page should open at its heading, not at the previous page's scroll offset.
    contentRef.current?.scrollTo({ top: 0, left: 0, behavior: 'instant' })
  }, [activeTab])

  useEffect(() => {
    const handleShortcut = (event: KeyboardEvent) => {
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'k') {
        event.preventDefault()
        searchRef.current?.focus()
      }
      if (event.key === 'Escape') searchRef.current?.blur()
    }
    window.addEventListener('keydown', handleShortcut)
    return () => window.removeEventListener('keydown', handleShortcut)
  }, [])

  const navigate = (tab: TabKey) => {
    onNavigate(tab)
    setMobileOpen(false)
    setSearch('')
  }

  const refresh = async () => {
    if (isRefreshing) return
    setIsRefreshing(true)
    setRefreshError('')
    try {
      await refreshData()
      window.dispatchEvent(new Event('force-refresh'))
    } catch (error: unknown) {
      setRefreshError(apiErrorMessage(error, vi ? 'Không thể làm mới dữ liệu.' : 'Could not refresh data.'))
    } finally {
      setIsRefreshing(false)
    }
  }

  const renderItems = (items: NavItem[]) => items.map(({ tab, icon: Icon, vi: viLabel, en }) => (
    <button
      className={`app-nav-item ${activeTab === tab ? 'is-active' : ''}`}
      type="button"
      title={vi ? viLabel : en}
      aria-current={activeTab === tab ? 'page' : undefined}
      key={tab}
      onClick={() => navigate(tab)}
    >
      <Icon size={19} strokeWidth={1.8} />
      <span>{vi ? viLabel : en}</span>
      {activeTab === tab && <span className="app-nav-active-dot" />}
    </button>
  ))

  return (
    <div className={`app-shell ${collapsed ? 'sidebar-collapsed' : ''}`} data-theme={isDark ? 'dark' : 'light'}>
      {mobileOpen && <button type="button" className="app-mobile-overlay" aria-label={vi ? 'Đóng menu' : 'Close menu'} onClick={() => setMobileOpen(false)} />}
      <aside className={`app-sidebar ${mobileOpen ? 'mobile-open' : ''}`}>
        <div className="app-brand">
          <span className="app-brand-mark"><Database size={22} strokeWidth={2.2} /></span>
          <button className="app-collapse-button" type="button" aria-label={collapsed ? (vi ? 'Mở rộng thanh bên' : 'Expand sidebar') : (vi ? 'Thu gọn thanh bên' : 'Collapse sidebar')} onClick={() => setCollapsed(!collapsed)}>
            {collapsed ? <PanelLeftOpen size={16} /> : <PanelLeftClose size={16} />}
          </button>
          <button className="app-icon-button app-mobile-close" type="button" aria-label={vi ? 'Đóng menu' : 'Close menu'} onClick={() => setMobileOpen(false)}><X size={20} /></button>
        </div>

        <nav className="app-sidebar-nav" aria-label={vi ? 'Điều hướng chính' : 'Main navigation'}>
          <div className="app-nav-section">{vi ? 'DASHBOARD' : 'DASHBOARD'}</div>
          {renderItems(overviewItems)}
          <div className="app-nav-section app-nav-section--spaced">{vi ? 'QUẢN LÝ' : 'MANAGEMENT'}</div>
          {renderItems(managementItems)}
        </nav>

        <div className="app-sidebar-bottom">
          <div className="app-user-card">
            {onLogout && <button type="button" className="app-icon-button app-logout-button" onClick={onLogout} title={vi ? 'Đăng xuất' : 'Logout'} aria-label={vi ? 'Đăng xuất' : 'Logout'}><LogOut className="app-logout-icon" size={18} aria-hidden="true" /><span>{vi ? 'Đăng xuất' : 'Logout'}</span></button>}
          </div>
        </div>
      </aside>

      <div className="app-main">
        <header className="app-topbar">
          <div className="app-topbar-left">
            <button type="button" className="app-icon-button app-mobile-menu" aria-label={vi ? 'Mở menu' : 'Open menu'} onClick={() => setMobileOpen(true)}><Menu size={20} /></button>
            <div className="app-search">
              <Search size={18} />
              <input
                ref={searchRef}
                value={search}
                onChange={(event) => setSearch(event.target.value)}
                onKeyDown={(event) => { if (event.key === 'Enter' && results[0]) navigate(results[0].tab) }}
                placeholder={vi ? 'Tìm trang trong dashboard...' : 'Search dashboard pages...'}
                aria-label={vi ? 'Tìm trang' : 'Search pages'}
              />
              <kbd>Ctrl K</kbd>
              {search.trim() && <div className="app-search-results">{results.length ? results.map((item) => <button type="button" key={item.tab} onClick={() => navigate(item.tab)}>{vi ? item.vi : item.en}<ArrowUpRight size={15} /></button>) : <span>{vi ? 'Không tìm thấy trang' : 'No pages found'}</span>}</div>}
            </div>
            <span className="app-mobile-title">{vi ? activeItem?.vi : activeItem?.en}</span>
          </div>
          <div className="app-topbar-actions">
            <span className={`app-connection ${server.error ? 'is-offline' : ''}`} title={vi ? 'Trạng thái kết nối lấy thông số máy chủ' : 'Connection used to fetch server metrics'}><i />{server.error ? (vi ? 'Mất kết nối máy chủ' : 'Server disconnected') : !server.data ? (vi ? 'Đang kết nối…' : 'Connecting…') : (vi ? 'Máy chủ đã kết nối' : 'Server connected')}</span>
            <button className="app-topbar-primary" type="button" onClick={() => navigate('available')}><Archive size={17} />{vi ? 'Bản sao lưu' : 'Backups'}</button>
            <button className={`app-icon-button ${isRefreshing ? 'is-spinning' : ''}`} type="button" onClick={refresh} disabled={isRefreshing} title={vi ? 'Làm mới dữ liệu' : 'Refresh data'} aria-label={vi ? 'Làm mới dữ liệu' : 'Refresh data'}><RefreshCw size={19} /></button>
            <button className="app-icon-button" type="button" onClick={onToggleDark} title={isDark ? (vi ? 'Giao diện sáng' : 'Light theme') : (vi ? 'Giao diện tối' : 'Dark theme')} aria-label={isDark ? (vi ? 'Giao diện sáng' : 'Light theme') : (vi ? 'Giao diện tối' : 'Dark theme')}>{isDark ? <Sun size={19} /> : <Moon size={19} />}</button>
          </div>
        </header>
        {refreshError && <div className="app-global-error" role="alert">{localizeMessage(refreshError, lang)}</div>}
        <main className="app-content" ref={contentRef}>{children}</main>
      </div>
    </div>
  )
}

// Load charts, editors and history views only when their page is opened.
const Dashboard = lazy(() => import("./Dashboard"))
const Home = lazy(() => import("./Drive"))
const ServerPage = lazy(() => import("./Server"))
const CronJobs = lazy(() => import("./CronJobs"))
const AppSettings = lazy(() => import("./Settings"))
const AppActivity = lazy(() => import("./Activity"))
const Available = lazy(() => import("./Available"))

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
        return <AppSettings isDark={isDark} onToggleDark={onToggleDark} lang={lang} onToggleLang={onToggleLang} />
      case 'activity':
        return <AppActivity isDark={isDark} lang={lang} />
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
