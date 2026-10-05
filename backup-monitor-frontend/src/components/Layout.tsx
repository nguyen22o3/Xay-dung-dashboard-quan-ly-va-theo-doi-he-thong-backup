import { type ReactNode, useEffect, useRef, useState } from 'react'
import { Activity, ArrowUpRight, Archive, CalendarClock, Cloud, Database, LayoutDashboard, LogOut, Menu, Moon, PanelLeftClose, PanelLeftOpen, RefreshCw, Search, Server, Settings, Sun, X } from 'lucide-react'
import type { LucideIcon } from 'lucide-react'
import { apiErrorMessage, refreshData, useServerStatus } from '../api'
import type { Lang } from '../language'
import type { TabKey } from '../types'

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

export default function Layout({ activeTab, onNavigate, isDark, onToggleDark, lang, children, onLogout }: {
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
  const server = useServerStatus(30000)
  const vi = lang === 'vi'
  const allItems = [...overviewItems, ...managementItems]
  const activeItem = allItems.find((item) => item.tab === activeTab)
  const results = search.trim()
    ? allItems.filter((item) => `${item.vi} ${item.en} ${item.tab === 'jobs' ? 'cronjob cron cấu hình lưu trữ storage configuration' : ''}`.toLowerCase().includes(search.trim().toLowerCase()))
    : []

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
          <button className="app-collapse-button" type="button" aria-label={collapsed ? 'Expand sidebar' : 'Collapse sidebar'} onClick={() => setCollapsed(!collapsed)}>
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
        {refreshError && <div className="app-global-error" role="alert">{refreshError}</div>}
        <main className="app-content">{children}</main>
      </div>
    </div>
  )
}
