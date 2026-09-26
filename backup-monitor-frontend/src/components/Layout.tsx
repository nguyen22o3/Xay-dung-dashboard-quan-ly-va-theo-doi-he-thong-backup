import type { ReactNode } from 'react'
import { Database, Cloud, Server, Activity, Settings, LogOut } from 'lucide-react'
import { useServerStatus } from '../api'
import type { Lang } from '../language'
import { tr } from '../language'
import type { TabKey } from '../types'
import { makeTheme } from '../theme'

const SidebarItem = ({
  icon: Icon,
  text,
  tabName,
  activeTab,
  onNavigate,
}: {
  icon: any
  text: string
  tabName: TabKey
  activeTab: TabKey
  onNavigate: (tab: TabKey) => void
}) => {
  const active = activeTab === tabName
  return (
    <div
      onClick={() => onNavigate(tabName)}
      style={{
        padding: '10px 20px',
        cursor: 'pointer',
        display: 'flex',
        alignItems: 'center',
        gap: '12px',
        backgroundColor: active ? '#ffffff' : 'transparent',
        color: active ? '#333333' : '#dddddd',
        fontWeight: active ? 'bold' : 'normal',
        fontSize: '13px',
        borderLeft: active ? '4px solid #1a4175' : '4px solid transparent',
        transition: 'all 0.15s',
        userSelect: 'none',
      }}
    >
      <Icon size={16} color={active ? '#1a4175' : '#dddddd'} /> {text}
    </div>
  )
}

const SidebarSection = ({ title }: { title: string }) => (
  <div style={{ padding: '15px 20px 5px 20px', fontSize: '11px', fontWeight: 'bold', color: '#aaaaaa', textTransform: 'uppercase' }}>
    {title}
  </div>
)

export default function Layout({
  activeTab,
  onNavigate,
  isDark,
  lang,
  children,
  onLogout,
}: {
  activeTab: TabKey
  onNavigate: (tab: TabKey) => void
  isDark: boolean
  lang: Lang
  children: ReactNode
  onLogout?: () => void
}) {
  const theme = makeTheme(isDark)
  const server = useServerStatus(30000)
  const uptime = server.data?.uptime

  return (
    <div
      style={{
        display: 'flex',
        flexDirection: 'column',
        height: '100vh',
        overflow: 'hidden',
        fontFamily: '"Segoe UI", Roboto, Helvetica, Arial, sans-serif',
      }}
    >
      {/* TOP HEADER */}
      <div
        style={{
          height: '45px',
          backgroundColor: theme.headerBg,
          display: 'flex',
          alignItems: 'center',
          padding: '0 20px',
          color: 'white',
          justifyContent: 'space-between',
          zIndex: 10,
          flexShrink: 0,
        }}
      >
        <div style={{ fontSize: '18px', fontWeight: 'bold', display: 'flex', alignItems: 'center', gap: '8px' }}>
          <Database size={20} /> {tr(lang, 'appTitle')}
        </div>
        <div style={{ display: 'flex', alignItems: 'center', gap: '15px' }}>
          {/* UPTIME CORNER BADGE - ONLY SHOW ON SERVER TAB */}
          {activeTab === 'server' && (
            <div style={{ display: 'flex', alignItems: 'center', gap: '8px', fontSize: '12px' }}>
              <span style={{ color: '#aaaaaa', textTransform: 'uppercase', fontSize: '10px', fontWeight: 'bold' }}>
                {lang === 'vi' ? 'Hoạt động:' : 'Uptime:'}
              </span>
              <span style={{ color: 'white', fontWeight: 'bold' }}>
                {uptime
                  ? lang === 'vi'
                    ? uptime.replace(/weeks?/g, 'tuần').replace(/days?/g, 'ngày').replace(/hours?/g, 'giờ').replace(/minutes?/g, 'phút')
                    : uptime
                  : '—'}
              </span>
              <span className="animate-pulse" style={{ width: '8px', height: '8px', borderRadius: '50%', backgroundColor: '#4caf50', boxShadow: '0 0 6px #4caf50', marginLeft: '4px' }} />
            </div>
          )}
        </div>
      </div>

      <div style={{ display: 'flex', flex: 1, overflow: 'hidden' }}>
        {/* LEFT SIDEBAR */}
        <div style={{ width: '220px', backgroundColor: theme.sidebarBg, display: 'flex', flexDirection: 'column', flexShrink: 0 }}>
          <div style={{ flex: 1, overflowY: 'auto' }}>
            <SidebarSection title={tr(lang, 'dashboardSection')} />
            <SidebarItem icon={Server} text={lang === 'vi' ? 'Máy chủ' : 'Server'} tabName="server" activeTab={activeTab} onNavigate={onNavigate} />
            <SidebarItem icon={Cloud} text={tr(lang, 'home')} tabName="home" activeTab={activeTab} onNavigate={onNavigate} />
            
            <SidebarSection title={tr(lang, 'manage')} />
            <SidebarItem icon={Settings} text={tr(lang, 'settings')} tabName="settings" activeTab={activeTab} onNavigate={onNavigate} />

            <SidebarSection title={tr(lang, 'backup')} />
            <SidebarItem icon={Activity} text={tr(lang, 'activity')} tabName="activity" activeTab={activeTab} onNavigate={onNavigate} />

            <SidebarSection title={tr(lang, 'recover')} />
            <SidebarItem icon={Database} text={tr(lang, 'available')} tabName="available" activeTab={activeTab} onNavigate={onNavigate} />
          </div>

          {onLogout && (
            <div style={{ padding: '12px 16px', borderTop: '1px solid rgba(255, 255, 255, 0.08)' }}>
              <div
                onClick={onLogout}
                style={{
                  display: 'flex',
                  alignItems: 'center',
                  gap: '10px',
                  color: '#ff6b6b',
                  cursor: 'pointer',
                  fontSize: '13px',
                  fontWeight: 500,
                  padding: '9px 14px',
                  borderRadius: '6px',
                  backgroundColor: 'rgba(255, 107, 107, 0.08)',
                  transition: 'all 0.15s',
                  userSelect: 'none',
                }}
                onMouseEnter={(e) => {
                  e.currentTarget.style.backgroundColor = 'rgba(255, 107, 107, 0.16)'
                }}
                onMouseLeave={(e) => {
                  e.currentTarget.style.backgroundColor = 'rgba(255, 107, 107, 0.08)'
                }}
              >
                <LogOut size={16} />
                <span>{lang === 'vi' ? 'Đăng xuất' : 'Logout'}</span>
              </div>
            </div>
          )}
        </div>

        {/* MAIN CONTENT */}
        <div style={{ flex: 1, backgroundColor: theme.bg, overflowY: 'auto', padding: '20px', color: theme.textPrimary }}>
          {children}
        </div>
      </div>
    </div>
  )
}