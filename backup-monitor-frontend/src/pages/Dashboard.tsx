import { Activity, ArrowUpRight, CalendarClock, Cloud, DatabaseBackup, HardDrive, Server, ShieldCheck } from 'lucide-react'
import { Area, AreaChart, CartesianGrid, Cell, Pie, PieChart, ResponsiveContainer, Tooltip, XAxis, YAxis } from 'recharts'
import { useBackupStatus, useCronJobs, useServerStatus } from '../api'
import { tr, type Lang } from '../language'
import type { TabKey } from '../types'
import { activityStamp, activityStatus, calendarDays, categoryLabel, formatBytes, nextCronRun, readPercentage, statusLabel } from '../utils'

type DashboardProps = {
  isDark: boolean
  lang: Lang
  onNavigate: (tab: TabKey) => void
}

const palette = ['#00bd82', '#0ea5a8', '#e8a318']

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
  const cron = useCronJobs(60000)
  const serverData = server.data
  const backupData = backup.data
  const history = new Map((backupData?.history ?? []).map((item) => [item.date, item]))
  const chartData = backupData?.history ? calendarDays(serverData?.server_time).map((day) => ({ date: day, bytes: history.get(day)?.bytes ?? 0, files: history.get(day)?.files ?? 0 })) : []
  const today = backupData?.todayBreakdownBytes
  const breakdown = [
    { name: categoryLabel('site', lang), value: today?.site ?? 0 },
    { name: categoryLabel('database', lang), value: today?.database ?? 0 },
    { name: categoryLabel('panel', lang), value: today?.panel ?? 0 },
  ]
  const todayTotal = breakdown.reduce((sum, item) => sum + item.value, 0)
  const activity = [...(backupData?.activity ?? []), ...(backupData?.localActivity ?? [])]
    .sort((a, b) => activityStamp(b).localeCompare(activityStamp(a)))
    .slice(0, 4)
  const nextCronTime = nextCronRun(cron.data ?? [], serverData?.server_time, lang)
  const gridColor = isDark ? '#28302c' : '#e8eeea'
  const mutedColor = isDark ? '#91a19a' : '#718076'

  return (
    <div className="dashboard-overview">
      <div className="overview-heading">
        <div>
          <h1>{vi ? 'Tổng quan' : 'Overview'}</h1>
          <p>{vi ? 'Theo dõi bản sao lưu trên máy chủ và Google Drive.' : 'Monitor backups across your server and Google Drive.'}</p>
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
      {backupData?.driveStale && <p role="status" className="overview-kpi-detail">{vi ? 'Số liệu Drive đã lưu; cập nhật lần cuối: ' : 'Saved Drive data; last updated: '}{backupData.driveDataAt ? new Date(backupData.driveDataAt).toLocaleString(vi ? 'vi-VN' : 'en-GB', { hour12: false }) : '—'}</p>}

      <div className="overview-kpi-grid">
        <KpiCard label={tr(lang, 'backupFilesDrive')} value={backupData?.size?.count?.toLocaleString() ?? '—'} detail="" tone="green" icon={Cloud} data={chartData.map((item) => item.files)} />
        <KpiCard label={tr(lang, 'backupSizeDrive')} value={backupData?.size?.bytes != null ? formatBytes(backupData.size.bytes) : '—'} detail="" tone="blue" icon={DatabaseBackup} data={chartData.map((item) => item.bytes)} />
        <KpiCard label={tr(lang, 'backupFilesServer')} value={serverData?.local_backup?.count?.toLocaleString() ?? '—'} detail={serverData?.local_backup?.size ? `${vi ? 'Dung lượng' : 'Storage'} ${serverData.local_backup.size}` : (vi ? 'Trên máy chủ' : 'On server')} tone="amber" icon={HardDrive} />
        <KpiCard label={tr(lang, 'scheduledJobsTitle')} value={cron.data?.length.toLocaleString() ?? '—'} detail={nextCronTime ? `${tr(lang, 'nextSchedule')}: ${nextCronTime}` : (vi ? 'Chưa xác nhận được lịch kế tiếp' : 'Next run is unconfirmed')} tone="purple" icon={CalendarClock} />
      </div>

      <div className="overview-main-grid">
        <section className="overview-panel overview-chart-panel">
          <div className="overview-panel-heading">
            <div><h2>{tr(lang, 'backupsHistory')}</h2><p>{vi ? 'Dung lượng theo ngày của bản sao lưu còn lưu trên Google Drive' : 'Daily size of backups retained on Google Drive'}</p></div>
            <span className="overview-period">{tr(lang, 'last14Days')}</span>
          </div>
          {chartData.length > 0 ? (
            <div className="overview-chart">
              <ResponsiveContainer width="100%" height="100%">
                <AreaChart data={chartData} margin={{ top: 16, right: 14, left: 4, bottom: 4 }}>
                  <defs><linearGradient id="backupTrendFill" x1="0" y1="0" x2="0" y2="1"><stop offset="0%" stopColor="#00bd82" stopOpacity={0.24} /><stop offset="100%" stopColor="#00bd82" stopOpacity={0} /></linearGradient></defs>
                  <CartesianGrid vertical={false} stroke={gridColor} strokeDasharray="4 5" />
                  <XAxis dataKey="date" tickFormatter={(value: string) => value.slice(5)} tickLine={false} axisLine={false} tick={{ fill: mutedColor, fontSize: 11 }} dy={12} />
                  <YAxis tickFormatter={formatBytes} tickLine={false} axisLine={false} width={70} tick={{ fill: mutedColor, fontSize: 11 }} />
                  <Tooltip formatter={(value) => [formatBytes(Number(value)), vi ? 'Dung lượng' : 'Size']} contentStyle={{ background: isDark ? '#171c19' : '#fff', border: `1px solid ${gridColor}`, borderRadius: 12, color: isDark ? '#f1f5f3' : '#17211d' }} />
                  <Area type="monotone" dataKey="bytes" stroke="#00bd82" strokeWidth={3} fill="url(#backupTrendFill)" activeDot={{ r: 5, fill: '#00bd82' }} isAnimationActive={false} />
                </AreaChart>
              </ResponsiveContainer>
            </div>
          ) : <div className="overview-empty">{backup.loading ? (vi ? 'Đang tải dữ liệu...' : 'Loading data...') : (vi ? 'Chưa có dữ liệu theo ngày' : 'No daily data yet')}</div>}
        </section>

        <section className="overview-panel overview-distribution-panel">
          <div className="overview-panel-heading"><div><h2>{tr(lang, 'dailyBackups')}</h2><p>{vi ? 'Bản sao lưu trên Google Drive' : 'Backups on Google Drive'}</p></div></div>
          {todayTotal > 0 ? (
            <div className="overview-distribution-body">
              <div className="overview-donut">
                <ResponsiveContainer width="100%" height="100%"><PieChart><Pie data={breakdown} dataKey="value" innerRadius="68%" outerRadius="88%" paddingAngle={3} stroke="none" isAnimationActive={false}>{breakdown.map((item, index) => <Cell key={item.name} fill={palette[index]} />)}</Pie></PieChart></ResponsiveContainer>
                <div className="overview-donut-center"><strong>{formatBytes(todayTotal)}</strong><span>{vi ? 'Hôm nay' : 'Today'}</span></div>
              </div>
              <div className="overview-legend">{breakdown.map((item, index) => <div key={item.name}><span className="overview-legend-name"><i style={{ background: palette[index] }} />{item.name}</span><strong>{formatBytes(item.value)}</strong></div>)}</div>
            </div>
          ) : <div className="overview-empty overview-empty--compact">{!backupData ? (backup.loading ? (vi ? 'Đang tải dữ liệu…' : 'Loading…') : (vi ? 'Không tải được dữ liệu' : 'Data unavailable')) : (vi ? 'Chưa có bản sao lưu trên Drive cho ngày máy chủ hiện tại' : 'No Drive backups for the current server date')}</div>}
        </section>
      </div>

      <div className="overview-lower-grid">
        <section className="overview-panel overview-activity-panel">
          <div className="overview-panel-heading"><div><h2>{vi ? 'Hoạt động gần đây' : 'Recent activity'}</h2><p>{vi ? 'Các lần sao lưu mới nhất' : 'Latest backup events'}</p></div><button type="button" className="overview-text-link" onClick={() => onNavigate('activity')}>{vi ? 'Xem tất cả' : 'View all'} <ArrowUpRight size={15} /></button></div>
          {activity.length > 0 ? <div className="overview-activity-list">{activity.map((entry, index) => {
            const successful = activityStatus(entry.status) === 'success'
            return <div className="overview-activity-row" key={`${entry.date}-${entry.time}-${entry.name}-${index}`}><span className={`overview-activity-symbol ${successful ? 'is-success' : 'is-warning'}`}><Activity size={17} /></span><div><strong>{entry.name || (vi ? 'Sao lưu' : 'Backup')}</strong><span>{activityStamp(entry)}</span></div><span className={`overview-status-pill ${successful ? 'is-success' : 'is-warning'}`}>{statusLabel(entry.status, lang)}</span></div>
          })}</div> : <div className="overview-empty overview-empty--compact">{vi ? 'Chưa có hoạt động sao lưu' : 'No backup activity yet'}</div>}
        </section>

        <section className="overview-panel overview-health-panel">
          <div className="overview-panel-heading"><div><h2>{vi ? 'Sức khỏe máy chủ' : 'Server health'}</h2><p>{vi ? 'Thông số hệ thống hiện tại' : 'Current system metrics'}</p></div><Server size={19} /></div>
          <div className="overview-health-list">
            {[
              { label: 'CPU', value: readPercentage(serverData?.cpu), color: '#00bd82' },
              { label: 'RAM', value: readPercentage(serverData?.ram?.usage), color: '#0ea5a8' },
              { label: vi ? 'Ổ đĩa' : 'Disk', value: readPercentage(serverData?.disk?.usage), color: '#e8a318' },
            ].map((metric) => <div className="overview-health-row" key={metric.label}><div><span>{metric.label}</span><strong>{metric.value !== null ? `${metric.value.toFixed(1)}%` : '—'}</strong></div><div className="overview-health-track"><span style={{ width: `${metric.value ?? 0}%`, background: metric.color }} /></div></div>)}
          </div>
          <div className="overview-health-footer"><ShieldCheck size={16} /><span>{serverData?.uptime ? `${vi ? 'Hoạt động' : 'Uptime'}: ${serverData.uptime}` : (vi ? 'Đang chờ dữ liệu máy chủ' : 'Waiting for server data')}</span></div>
        </section>
      </div>
    </div>
  )
}
