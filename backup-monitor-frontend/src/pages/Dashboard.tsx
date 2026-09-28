import { Activity, ArrowUpRight, CalendarClock, Cloud, DatabaseBackup, HardDrive, Server, ShieldCheck } from 'lucide-react'
import { Area, AreaChart, CartesianGrid, Cell, Pie, PieChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'
import { useBackupStatus, useServerStatus } from '../api'
import type { Lang } from '../language'
import type { TabKey } from '../types'
import { formatBytes } from '../utils'

type DashboardProps = {
  isDark: boolean
  lang: Lang
  onNavigate: (tab: TabKey) => void
}

const palette = ['#00bd82', '#0ea5a8', '#e8a318']

function percentage(value?: string): number {
  const parsed = Number.parseFloat(value ?? '')
  return Number.isFinite(parsed) ? Math.min(100, Math.max(0, parsed)) : 0
}

function KpiCard({ label, value, detail, tone, icon: Icon, data }: {
  label: string
  value: string
  detail: string
  tone: 'green' | 'blue' | 'amber' | 'purple'
  icon: typeof Cloud
  data?: number[]
}) {
  return (
    <div className={`overview-kpi overview-kpi--${tone}`}>
      <div className="overview-kpi-top">
        <span>{label}</span>
        <span className="overview-kpi-icon"><Icon size={22} strokeWidth={1.8} /></span>
      </div>
      <strong>{value}</strong>
      <span className="overview-kpi-detail">{detail}</span>
      {data && data.length > 1 && (
        <div className="overview-kpi-spark" aria-hidden="true">
          <ResponsiveContainer width="100%" height="100%">
            <AreaChart data={data.map((amount, index) => ({ index, amount }))}>
              <Area type="monotone" dataKey="amount" stroke="currentColor" strokeWidth={2} fill="currentColor" fillOpacity={0.12} isAnimationActive={false} />
            </AreaChart>
          </ResponsiveContainer>
        </div>
      )}
    </div>
  )
}

export default function Dashboard({ isDark, lang, onNavigate }: DashboardProps) {
  const vi = lang === 'vi'
  const server = useServerStatus(30000)
  const backup = useBackupStatus(60000)
  const serverData = server.data
  const backupData = backup.data
  const history = [...(backupData?.history ?? [])].sort((a, b) => a.date.localeCompare(b.date)).slice(-14)
  const chartData = history.map((item) => ({ date: item.date.slice(5), bytes: item.bytes, files: item.files ?? 0 }))
  const today = backupData?.todayBreakdownBytes
  const breakdown = [
    { name: 'Website', value: today?.site ?? 0 },
    { name: 'Database', value: today?.database ?? 0 },
    { name: 'aaPanel', value: today?.panel ?? 0 },
  ]
  const todayTotal = breakdown.reduce((sum, item) => sum + item.value, 0)
  const activity = [...(backupData?.activity ?? []), ...(backupData?.localActivity ?? [])]
    .sort((a, b) => `${b.date} ${b.time}`.localeCompare(`${a.date} ${a.time}`))
    .slice(0, 4)
  const latestDate = [...history].reverse().find((item) => (item.files ?? 0) > 0)?.date
  const gridColor = isDark ? '#28302c' : '#e8eeea'
  const mutedColor = isDark ? '#91a19a' : '#718076'

  return (
    <div className="dashboard-overview">
      <div className="overview-heading">
        <div>
          <h1>Dashboard</h1>
          <p>{vi ? 'Theo dõi tình trạng sao lưu trên máy chủ và Google Drive.' : 'Monitor backups across your server and Google Drive.'}</p>
        </div>
        <button className="overview-secondary-action" type="button" onClick={() => onNavigate('available')}>
          {vi ? 'Xem bản sao lưu' : 'View backups'} <ArrowUpRight size={16} />
        </button>
      </div>

      {(server.error || backup.error) && (
        <div className="overview-error" role="alert">
          {server.error && <span>{vi ? 'Máy chủ' : 'Server'}: {server.error}</span>}
          {backup.error && <span>Google Drive: {backup.error}</span>}
        </div>
      )}

      <div className="overview-kpi-grid">
        <KpiCard label={vi ? 'Tệp trên Google Drive' : 'Files on Google Drive'} value={backupData?.size?.count?.toLocaleString() ?? '—'} detail={vi ? 'Tổng số tệp sao lưu' : 'Total backup files'} tone="green" icon={Cloud} data={chartData.map((item) => item.files)} />
        <KpiCard label={vi ? 'Dung lượng sao lưu' : 'Backup storage'} value={backupData?.size?.bytes != null ? formatBytes(backupData.size.bytes) : '—'} detail="Google Drive / Backup" tone="blue" icon={DatabaseBackup} data={chartData.map((item) => item.bytes)} />
        <KpiCard label={vi ? 'File backup cục bộ' : 'Local backup files'} value={serverData?.local_backup?.count?.toLocaleString() ?? '—'} detail={serverData?.local_backup?.size ? `${vi ? 'Dung lượng' : 'Storage'} ${serverData.local_backup.size}` : (vi ? 'Trên máy chủ' : 'On server')} tone="amber" icon={HardDrive} />
        <KpiCard label={vi ? 'Lịch sao lưu' : 'Scheduled jobs'} value={serverData?.crons?.toLocaleString() ?? '—'} detail={latestDate ? `${vi ? 'Có backup ngày' : 'Latest backup'} ${latestDate}` : (vi ? 'Chưa có lịch sử trên Drive' : 'No Drive history yet')} tone="purple" icon={CalendarClock} />
      </div>

      <div className="overview-main-grid">
        <section className="overview-panel overview-chart-panel">
          <div className="overview-panel-heading">
            <div><h2>{vi ? 'Xu hướng sao lưu' : 'Backup trend'}</h2><p>{vi ? 'Dung lượng bản sao lưu trên Drive theo ngày' : 'Daily backup size on Drive'}</p></div>
            <span className="overview-period">{vi ? '14 ngày gần nhất' : 'Last 14 days'}</span>
          </div>
          {chartData.length > 0 ? (
            <div className="overview-chart">
              <ResponsiveContainer width="100%" height="100%">
                <AreaChart data={chartData} margin={{ top: 16, right: 14, left: 4, bottom: 4 }}>
                  <defs><linearGradient id="backupTrendFill" x1="0" y1="0" x2="0" y2="1"><stop offset="0%" stopColor="#00bd82" stopOpacity={0.24} /><stop offset="100%" stopColor="#00bd82" stopOpacity={0} /></linearGradient></defs>
                  <CartesianGrid vertical={false} stroke={gridColor} strokeDasharray="4 5" />
                  <XAxis dataKey="date" tickLine={false} axisLine={false} tick={{ fill: mutedColor, fontSize: 11 }} dy={12} />
                  <YAxis tickFormatter={(value: number) => `${Math.round(value / (1024 * 1024))} MB`} tickLine={false} axisLine={false} width={62} tick={{ fill: mutedColor, fontSize: 11 }} />
                  <Tooltip formatter={(value) => [formatBytes(Number(value)), vi ? 'Dung lượng' : 'Size']} contentStyle={{ background: isDark ? '#171c19' : '#fff', border: `1px solid ${gridColor}`, borderRadius: 12, color: isDark ? '#f1f5f3' : '#17211d' }} />
                  <Area type="monotone" dataKey="bytes" stroke="#00bd82" strokeWidth={3} fill="url(#backupTrendFill)" activeDot={{ r: 5, fill: '#00bd82' }} isAnimationActive={false} />
                </AreaChart>
              </ResponsiveContainer>
            </div>
          ) : <div className="overview-empty">{backup.loading ? (vi ? 'Đang tải dữ liệu...' : 'Loading data...') : (vi ? 'Chưa có dữ liệu theo ngày' : 'No daily data yet')}</div>}
        </section>

        <section className="overview-panel overview-distribution-panel">
          <div className="overview-panel-heading"><div><h2>{vi ? 'Cơ cấu bản sao lưu' : 'Backup distribution'}</h2><p>{vi ? 'Dung lượng theo loại trong hôm nay' : 'Today by backup type'}</p></div></div>
          {todayTotal > 0 ? (
            <div className="overview-distribution-body">
              <div className="overview-donut">
                <ResponsiveContainer width="100%" height="100%"><PieChart><Pie data={breakdown} dataKey="value" innerRadius="68%" outerRadius="88%" paddingAngle={3} stroke="none" isAnimationActive={false}>{breakdown.map((item, index) => <Cell key={item.name} fill={palette[index]} />)}</Pie></PieChart></ResponsiveContainer>
                <div className="overview-donut-center"><strong>{formatBytes(todayTotal)}</strong><span>{vi ? 'Hôm nay' : 'Today'}</span></div>
              </div>
              <div className="overview-legend">{breakdown.map((item, index) => <div key={item.name}><span className="overview-legend-name"><i style={{ background: palette[index] }} />{item.name}</span><strong>{formatBytes(item.value)}</strong></div>)}</div>
            </div>
          ) : <div className="overview-empty overview-empty--compact">{vi ? 'Hôm nay chưa có bản sao lưu mới' : 'No new backups today'}</div>}
        </section>
      </div>

      <div className="overview-lower-grid">
        <section className="overview-panel overview-activity-panel">
          <div className="overview-panel-heading"><div><h2>{vi ? 'Hoạt động gần đây' : 'Recent activity'}</h2><p>{vi ? 'Các lần sao lưu mới nhất' : 'Latest backup events'}</p></div><button type="button" className="overview-text-link" onClick={() => onNavigate('activity')}>{vi ? 'Xem tất cả' : 'View all'} <ArrowUpRight size={15} /></button></div>
          {activity.length > 0 ? <div className="overview-activity-list">{activity.map((entry, index) => {
            const successful = /success|thành công/i.test(entry.status)
            return <div className="overview-activity-row" key={`${entry.date}-${entry.time}-${entry.name}-${index}`}><span className={`overview-activity-symbol ${successful ? 'is-success' : 'is-warning'}`}><Activity size={17} /></span><div><strong>{entry.name || (vi ? 'Sao lưu' : 'Backup')}</strong><span>{entry.date} · {entry.time}</span></div><span className={`overview-status-pill ${successful ? 'is-success' : 'is-warning'}`}>{successful ? (vi ? 'Thành công' : 'Success') : entry.status}</span></div>
          })}</div> : <div className="overview-empty overview-empty--compact">{vi ? 'Chưa có hoạt động sao lưu' : 'No backup activity yet'}</div>}
        </section>

        <section className="overview-panel overview-health-panel">
          <div className="overview-panel-heading"><div><h2>{vi ? 'Sức khỏe máy chủ' : 'Server health'}</h2><p>{vi ? 'Thông số hệ thống hiện tại' : 'Current system metrics'}</p></div><Server size={19} /></div>
          <div className="overview-health-list">
            {[
              { label: 'CPU', value: percentage(serverData?.cpu), color: '#00bd82' },
              { label: 'RAM', value: percentage(serverData?.ram?.usage), color: '#0ea5a8' },
              { label: vi ? 'Ổ đĩa' : 'Disk', value: percentage(serverData?.disk?.usage), color: '#e8a318' },
            ].map((metric) => <div className="overview-health-row" key={metric.label}><div><span>{metric.label}</span><strong>{serverData ? `${metric.value.toFixed(1)}%` : '—'}</strong></div><div className="overview-health-track"><span style={{ width: `${metric.value}%`, background: metric.color }} /></div></div>)}
          </div>
          <div className="overview-health-footer"><ShieldCheck size={16} /><span>{serverData?.uptime ? `${vi ? 'Hoạt động' : 'Uptime'}: ${serverData.uptime}` : (vi ? 'Đang chờ dữ liệu máy chủ' : 'Waiting for server data')}</span></div>
        </section>
      </div>
    </div>
  )
}
