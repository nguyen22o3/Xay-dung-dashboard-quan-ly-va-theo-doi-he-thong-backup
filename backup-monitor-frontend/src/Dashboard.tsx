import "react-grid-layout/css/styles.css"
import "react-resizable/css/styles.css"
import { useEffect, useState } from "react"
import { Responsive, useContainerWidth } from "react-grid-layout"
import type { Layout } from "react-grid-layout"
import { Activity, ArrowUpRight, CalendarClock, Cloud, DatabaseBackup, HardDrive, Server, ShieldCheck } from "lucide-react"
import { Area, AreaChart, Bar, BarChart, CartesianGrid, Cell, ReferenceDot, ReferenceLine, ResponsiveContainer, Tooltip, XAxis, YAxis } from "recharts"
import { useBackupStatus, useCronJobs, useServerStatus } from "./api"
import { tr, localizeMessage } from "./i18n"
import type { Lang } from "./i18n"
import type { TabKey } from "./shared"
import { activityStamp, activityStatus, calendarDays, cronIsOverdue, formatBytes, formatUptime, nextCronRun, readPercentage, statusLabel } from "./shared"
import { dashboardBreakpointForViewport, dashboardBreakpoints, dashboardColumns, dashboardGridStorageKey, readDashboardLayouts } from "./grid"

type DashboardProps = {
  isDark: boolean
  lang: Lang
  onNavigate: (tab: TabKey) => void
}

const backupDayLabel = (date: string) => `${date.slice(8)}/${date.slice(5, 7)}`

function formatDifference(bytes: number | null): string {
  if (bytes === null) return '—'
  if (bytes === 0) return '0 MB'
  return `${bytes < 0 ? '−' : '+'}${formatBytes(Math.abs(bytes))}`
}

function KpiCard({ label, value, detail, error, tone, icon: Icon, data }: {
  label: string
  value: string
  detail: string
  error?: string | null
  tone: 'primary' | 'blue' | 'orange' | 'purple'
  icon: typeof Cloud
  data?: number[]
}) {
  return (
    <div className={`overview-kpi overview-kpi--${tone}`}>
      <div className="overview-kpi-top overview-drag-handle">
        <span>{label}</span>
        <span className="overview-kpi-icon"><Icon size={22} strokeWidth={1.8} /></span>
      </div>
      <strong>{value}</strong>
      {detail && <span className="overview-kpi-detail" title={error || undefined}>{detail}</span>}
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

function PanelNotice({ error, saved, children }: { error?: string | null; saved?: boolean; children: string }) {
  return <p className={`overview-notice${error && !saved ? ' is-error' : ''}`} role={error ? 'alert' : 'status'} title={error || undefined}>{children}</p>
}

export default function Dashboard({ isDark, lang, onNavigate }: DashboardProps) {
  const { width, containerRef, mounted } = useContainerWidth({ measureBeforeMount: true })
  const [breakpoint, setBreakpoint] = useState(() => dashboardBreakpointForViewport(window.innerWidth))
  useEffect(() => {
    const onResize = () => setBreakpoint(dashboardBreakpointForViewport(window.innerWidth))
    window.addEventListener('resize', onResize)
    return () => window.removeEventListener('resize', onResize)
  }, [])
  const [layouts, setLayouts] = useState(() => {
    try { return readDashboardLayouts(localStorage.getItem(dashboardGridStorageKey)) }
    catch { return readDashboardLayouts(null) }
  })
  const onLayoutChange = (_current: Layout, allLayouts: Partial<Record<string, Layout>>) => {
    const next = readDashboardLayouts(JSON.stringify(allLayouts))
    setLayouts(next)
    try { localStorage.setItem(dashboardGridStorageKey, JSON.stringify(next)) }
    catch { /* Dragging/resizing still works if browser storage is unavailable. */ }
  }
  const vi = lang === 'vi'
  const server = useServerStatus(30000)
  const backup = useBackupStatus(60000)
  const cron = useCronJobs(60000)
  const serverData = server.data
  const backupData = backup.data
  const localBackup = serverData?.local_backup
  const localError = server.error || (serverData?.local_backup_error ? localizeMessage(serverData.local_backup_error, lang) : null)
  const driveSaved = Boolean(backupData?.driveStale)
  const days = calendarDays(serverData?.server_time)
  const driveDay = backupData?.driveDay
  const driveDate = Date.parse(driveDay || '')
  const driveDays = Number.isFinite(driveDate) ? calendarDays(undefined, 14, new Date(driveDate)) : days
  const history = new Map((backupData?.history ?? []).map((item) => [item.date, item]))
  const chartData = driveDays.some(day => history.has(day))
    ? driveDays.map((day) => ({ date: day, bytes: history.get(day)?.bytes ?? 0, files: history.get(day)?.files ?? 0 }))
    : []
  const localHistory = new Map(localBackup?.history?.map((item) => [item.date, item]))
  const comparison = days.some(day => history.has(day) || localHistory.has(day))
    ? days.map(date => {
      const drive = history.get(date)?.bytes ?? (Array.isArray(backupData?.history) && !driveSaved && !backup.error ? 0 : null)
      const server = localHistory.get(date)?.bytes ?? (Array.isArray(localBackup?.history) && !localError ? 0 : null)
      return { date, drive, server, difference: drive === null || server === null ? null : drive - server }
    })
    : []
  const latest = comparison.at(-1)
  const comparable = comparison.some(day => day.difference !== null)
  const allEqual = comparable && comparison.every(day => day.difference === 0)
  const maxDifference = Math.max(1, ...comparison.map(day => Math.abs(day.difference ?? 0)))
  const exactBytes = (value: number | null) => value === null ? '—' : `${value.toLocaleString(vi ? 'vi-VN' : 'en-GB')} B`
  const serverLabel = vi ? 'Máy chủ' : 'Server'
  const activity = [
    ...(backupData?.activity ?? []).map(entry => ({ ...entry, source: 'Google Drive' })),
    ...(backupData?.localActivity ?? []).map(entry => ({ ...entry, source: serverLabel })),
  ]
    .sort((a, b) => activityStamp(b).localeCompare(activityStamp(a)))
  const activeJobs = cron.data?.filter(job => job.enabled !== false) ?? []
  const nextCronTime = cron.error || server.error ? null : nextCronRun(activeJobs, serverData?.server_time, lang)
  const overdueJobs = cron.error || server.error ? [] : activeJobs.filter(job => cronIsOverdue(job, serverData?.server_time))
  const sources = [
    { label: serverLabel, requireRun: true, overdue: overdueJobs.filter(job => ['backup-site', 'backup-database', 'backup-panel'].includes(job.id)).length },
    { label: 'Google Drive', requireRun: false, overdue: overdueJobs.filter(job => job.id === 'drive-sync').length },
  ]
  const loading = vi ? 'Đang tải…' : 'Loading…'
  const unavailable = vi ? 'Không tải được dữ liệu' : 'Data unavailable'
  const saved = vi ? 'Dữ liệu đã lưu' : 'Saved data'
  const driveDetail = driveSaved ? '' : backup.error ? unavailable : backup.loading ? loading : ''
  const localDetail = localError ? (localBackup ? saved : unavailable) : server.loading ? loading : ''
  const cronDetail = cron.error ? (cron.data ? saved : unavailable)
    : cron.loading ? loading
      : nextCronTime ? `${tr(lang, 'nextSchedule')}: ${nextCronTime}`
        : activeJobs.length === 0 ? (vi ? 'Không có lịch đang bật' : 'No active schedules')
          : (vi ? 'Chưa xác nhận được lịch kế tiếp' : 'Next run is unconfirmed')
  const gridColor = isDark ? '#273341' : '#e5edf6'
  const mutedColor = isDark ? '#9baabd' : '#61738b'

  return (
    <div className="dashboard-overview">
      <div className="overview-heading">
        <div>
          <h1>{vi ? 'Tổng quan' : 'Overview'}</h1>
          <p>{vi ? 'Theo dõi bản sao lưu trên máy chủ và Google Drive.' : 'Monitor backups across your server and Google Drive.'}</p>
        </div>
        <div className="overview-heading-actions">
          <button className="overview-secondary-action" type="button" onClick={() => onNavigate('available')}>
            {vi ? 'Xem bản sao lưu' : 'View backups'} <ArrowUpRight size={16} />
          </button>
        </div>
      </div>

      <div ref={containerRef}>
        {mounted && width > 0 && <Responsive
          width={width}
          breakpoint={breakpoint}
          className="overview-widget-grid"
          layouts={layouts}
          breakpoints={dashboardBreakpoints}
          cols={dashboardColumns}
          rowHeight={30}
          margin={[18, 18]}
          containerPadding={[0, 0]}
          onLayoutChange={onLayoutChange}
          dragConfig={{ handle: '.overview-drag-handle', cancel: 'button, a, input, select, textarea' }}
          resizeConfig={{ handles: ['s', 'w', 'e', 'n', 'sw', 'nw', 'se', 'ne'] }}
        >
        <div key="drive-files"><KpiCard label={tr(lang, 'backupFilesDrive')} value={backupData?.size?.count?.toLocaleString() ?? '—'} detail={driveDetail} error={backup.error} tone="primary" icon={Cloud} data={chartData.map((item) => item.files)} /></div>
        <div key="drive-size"><KpiCard label={tr(lang, 'backupSizeDrive')} value={backupData?.size?.bytes != null ? formatBytes(backupData.size.bytes) : '—'} detail={driveDetail} error={backup.error} tone="blue" icon={DatabaseBackup} data={chartData.map((item) => item.bytes)} /></div>
        <div key="server-files">
          <KpiCard
            label={tr(lang, 'backupFilesServer')}
            value={localBackup?.count?.toLocaleString() ?? '—'}
            detail={localDetail}
            error={localError}
            tone="orange"
            icon={HardDrive}
            data={localBackup?.history ? days.map((day) => localHistory.get(day)?.files ?? 0) : undefined}
          />
        </div>
        <div key="server-size">
          <KpiCard
            label={tr(lang, 'backupSizeServer')}
            value={localBackup?.bytes != null ? formatBytes(localBackup.bytes) : localBackup?.size || '—'}
            detail={localDetail}
            error={localError}
            tone="orange"
            icon={DatabaseBackup}
            data={localBackup?.history ? days.map((day) => localHistory.get(day)?.bytes ?? 0) : undefined}
          />
        </div>
        <div key="scheduled-jobs"><KpiCard label={tr(lang, 'scheduledJobsTitle')} value={cron.data?.length.toLocaleString() ?? '—'} detail={cronDetail} error={cron.error} tone="purple" icon={CalendarClock} /></div>

        <section key="history" className="overview-panel overview-chart-panel">
          <div className="overview-panel-heading overview-drag-handle">
            <div><h2>{vi ? 'Chênh lệch dung lượng sao lưu' : 'Backup size difference'}</h2><p>{vi ? 'Google Drive − Máy chủ' : 'Google Drive − Server'}</p></div>
            <span className="overview-period">{tr(lang, 'last14Days')}</span>
          </div>
          {backup.error && !driveSaved && <PanelNotice error={backup.error}>{`Google Drive · ${unavailable}`}</PanelNotice>}
          {localError && <PanelNotice error={localError} saved={Boolean(localBackup)}>{`${serverLabel} · ${localBackup ? saved : unavailable}`}</PanelNotice>}
          {latest && <dl className="overview-difference-summary" aria-label={vi ? `Số liệu ngày ${backupDayLabel(latest.date)}` : `Figures for ${backupDayLabel(latest.date)}`}>
            <div><dt>{serverLabel}</dt><dd>{latest.server === null ? '—' : formatBytes(latest.server)}</dd></div>
            <div><dt>Google Drive</dt><dd>{latest.drive === null ? '—' : formatBytes(latest.drive)}</dd></div>
            <div><dt>{vi ? 'Chênh lệch' : 'Difference'} · {backupDayLabel(latest.date)}</dt><dd className={latest.difference !== null && latest.difference < 0 ? 'is-deficit' : undefined}>{formatDifference(latest.difference)}</dd></div>
          </dl>}
          {allEqual ? <div className="overview-empty">{vi ? 'Chênh lệch 0 MB trong 14 ngày' : '0 MB difference over 14 days'}</div>
          : comparable ? (
            <div className="overview-chart">
              <ResponsiveContainer width="100%" height="100%">
                <BarChart data={comparison} margin={{ top: 10, right: 6, left: 0, bottom: 0 }}>
                  <CartesianGrid vertical={false} stroke={gridColor} strokeDasharray="4 5" />
                  <XAxis dataKey="date" tickFormatter={backupDayLabel} tickLine={false} axisLine={false} tick={{ fill: mutedColor, fontSize: 11 }} minTickGap={12} />
                  <YAxis domain={[-maxDifference, maxDifference]} tickFormatter={formatDifference} tickLine={false} axisLine={false} width={74} tickCount={5} allowDecimals={false} tick={{ fill: mutedColor, fontSize: 10 }} />
                  <ReferenceLine y={0} stroke={mutedColor} />
                  <Tooltip cursor={{ fill: gridColor }} filterNull={false} trigger={window.matchMedia('(pointer: coarse)').matches ? 'click' : 'hover'} content={({ active, label }) => {
                    const day = comparison.find(item => item.date === label)
                    if (!active || !day) return null
                    return <div className="overview-difference-tooltip">
                      <strong>{day.date}</strong>
                      <span>{serverLabel} · {exactBytes(day.server)}</span>
                      <span>Google Drive · {exactBytes(day.drive)}</span>
                      <span>{vi ? 'Chênh lệch' : 'Difference'} · {formatDifference(day.difference)}</span>
                    </div>
                  }} />
                  <Bar dataKey="difference" maxBarSize={24} isAnimationActive={false}>
                    {comparison.map(day => <Cell key={day.date} fill={day.difference !== null && day.difference < 0 ? 'var(--app-danger)' : 'var(--app-accent)'} />)}
                  </Bar>
                  {comparison.filter(day => day.difference === null).map(day =>
                    <ReferenceDot key={day.date} x={day.date} y={0} shape={({ cx, cy }) =>
                      <text x={cx} y={cy} dy={-5} textAnchor="middle" fill={mutedColor} fontSize={11}>—</text>} />)}
                </BarChart>
              </ResponsiveContainer>
            </div>
          ) : <div className="overview-empty">{backup.loading || server.loading ? loading : (vi ? 'Chưa đủ dữ liệu để đối chiếu' : 'Not enough data to compare')}</div>}
          {comparable && comparison.some(day => day.difference === null) && <p className="overview-difference-key">{vi ? '— Thiếu dữ liệu' : '— Missing data'}</p>}
        </section>

        <section key="distribution" className="overview-panel overview-backup-panel">
          <div className="overview-panel-heading overview-drag-handle">
            <div><h2>{vi ? 'Sao lưu thành công gần nhất' : 'Latest successful backups'}</h2><p>{vi ? 'Thời điểm ghi nhận theo giờ máy chủ' : 'Recorded in server time'}</p></div>
          </div>
          {backup.activityError && <PanelNotice error={backup.activityError} saved={activity.length > 0}>{activity.length > 0 ? saved : unavailable}</PanelNotice>}
          <div className="overview-backup-list">
            {sources.map(source => {
              const last = activity.find(entry => entry.source === source.label &&
                (!source.requireRun || entry.kind === 'run') && activityStatus(entry.status) === 'success')
              return <div className="overview-backup-row" key={source.label}>
                <div><strong>{source.label}</strong><span>{last ? activityStamp(last) : backup.loading ? loading : backup.activityError ? unavailable : (vi ? 'Chưa ghi nhận thành công' : 'No success recorded')}</span></div>
                {source.overdue > 0 && <button type="button" className="overview-status-pill overview-overdue is-warning" title={vi ? 'Đã qua giờ theo lịch và chưa ghi nhận lần chạy mới' : 'The scheduled time passed without a new recorded run'} onClick={() => onNavigate('jobs')}>{vi ? `${source.overdue} lịch quá giờ` : `Overdue schedules (${source.overdue})`}</button>}
              </div>
            })}
          </div>
        </section>
        <section key="activity" className="overview-panel overview-activity-panel">
          <div className="overview-panel-heading overview-drag-handle"><div><h2>{vi ? 'Hoạt động gần đây' : 'Recent activity'}</h2><p>{vi ? 'Các lần sao lưu mới nhất' : 'Latest backup events'}</p></div><button type="button" className="overview-text-link" onClick={() => onNavigate('activity')}>{vi ? 'Xem tất cả' : 'View all'} <ArrowUpRight size={15} /></button></div>
          {backup.activityError && <PanelNotice error={backup.activityError} saved={activity.length > 0}>{activity.length > 0 ? (vi ? 'Đang hiển thị nhật ký đã lưu' : 'Showing saved activity') : backup.activityError}</PanelNotice>}
          {activity.length > 0 ? <div className="overview-activity-list">{activity.slice(0, 4).map((entry, index) => {
            const successful = activityStatus(entry.status) === 'success'
            return <div className="overview-activity-row" key={`${entry.source}-${entry.date}-${entry.time}-${entry.name}-${index}`}><span className={`overview-activity-symbol ${successful ? 'is-success' : 'is-warning'}`}><Activity size={17} /></span><div><strong>{entry.name || (vi ? 'Sao lưu' : 'Backup')}</strong><span>{entry.source} · {activityStamp(entry)}</span></div><span className={`overview-status-pill ${successful ? 'is-success' : 'is-warning'}`}>{statusLabel(entry.status, lang)}</span></div>
          })}</div> : !backup.activityError && <div className="overview-empty overview-empty--compact">{backup.loading ? loading : (vi ? 'Chưa có hoạt động sao lưu' : 'No backup activity yet')}</div>}
        </section>

        <section key="health" className="overview-panel overview-health-panel">
          <div className="overview-panel-heading overview-drag-handle"><div><h2>{vi ? 'Sức khỏe máy chủ' : 'Server health'}</h2><p>{vi ? 'Thông số hệ thống hiện tại' : 'Current system metrics'}</p></div><Server size={19} /></div>
          {server.error && <PanelNotice error={server.error} saved={Boolean(serverData)}>{serverData ? saved : server.error}</PanelNotice>}
          <div className="overview-health-list">
            {[
              { label: 'CPU', value: readPercentage(serverData?.cpu), color: 'var(--app-accent)' },
              { label: 'RAM', value: readPercentage(serverData?.ram?.usage), color: '#38bdf8' },
              { label: vi ? 'Ổ đĩa' : 'Disk', value: readPercentage(serverData?.disk?.usage), color: '#e8a318' },
            ].map((metric) => <div className="overview-health-row" key={metric.label}><div><span>{metric.label}</span><strong>{metric.value !== null ? `${metric.value.toFixed(1)}%` : '—'}</strong></div><div className="overview-health-track"><span style={{ width: `${metric.value ?? 0}%`, background: metric.color }} /></div></div>)}
          </div>
          <div className="overview-health-footer"><ShieldCheck size={16} /><span>{serverData?.uptime ? `${vi ? 'Hoạt động' : 'Uptime'}: ${formatUptime(serverData.uptime, lang)}` : server.loading ? loading : '—'}</span></div>
        </section>
        </Responsive>}
      </div>
    </div>
  )
}
