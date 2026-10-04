import React, { useEffect, useState } from 'react'
import { Responsive } from 'react-grid-layout'
import type { Layout } from 'react-grid-layout'
import 'react-grid-layout/css/styles.css'
import 'react-resizable/css/styles.css'
import {
  AreaChart,
  Area,
  BarChart,
  Bar,
  PieChart,
  Pie,
  Cell,
  XAxis,
  YAxis,
  CartesianGrid,
  Tooltip,
  ResponsiveContainer,
} from 'recharts'
import { ErrorBoundary } from '../ErrorBoundary'
import type { Lang } from '../language'
import { tr } from '../language'
import { makeTheme } from '../theme'
import { useServerStatus, useBackupStatus, useCronJobs } from '../api'
import { activityStamp, activityStatus, calendarDays, categoryLabel, formatBytes, formatDuration, nextCronRun } from '../utils'

import type { CSSProperties } from 'react'

interface ChartDatum {
  date: string
  totalSize: number
  diffSize: number
  success: number
  failed: number
}

function ChartTooltip({
  active,
  payload,
  label,
  unit,
  palette,
}: {
  active?: boolean
  payload?: Array<{ value: number; color?: string; fill?: string }>
  label?: string
  unit?: 'bytes'
  palette: { bg: string; border: string; text: string }
}) {
  if (active && payload && payload.length) {
    return (
      <div
        style={{
          backgroundColor: palette.bg,
          border: `1px solid ${palette.border}`,
          padding: '10px',
          color: palette.text,
          fontSize: '12px',
          zIndex: 1000,
        }}
      >
        <p style={{ margin: '0 0 5px 0', fontWeight: 'bold' }}>{label}</p>
        <p style={{ margin: 0, color: payload[0].color || payload[0].fill }}>
          {unit === 'bytes' ? formatBytes(payload[0].value) : payload[0].value}
        </p>
      </div>
    )
  }
  return null
}

function useCustomContainerWidth() {
  const [width, setWidth] = useState(0)
  const containerRef = React.useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!containerRef.current) return
    const resizeObserver = new ResizeObserver((entries) => {
      if (entries[0] && entries[0].contentRect.width > 0) {
        setWidth(entries[0].contentRect.width)
      }
    })
    resizeObserver.observe(containerRef.current)
    setWidth(containerRef.current.offsetWidth)
    return () => resizeObserver.disconnect()
  }, [])

  return { width, containerRef }
}

export default function Home({ isDark, lang }: { isDark: boolean; lang: Lang }) {
  const theme = makeTheme(isDark)
  const server = useServerStatus()
  const backup = useBackupStatus()
  const cron = useCronJobs(60000)
  
  const { width: containerWidth, containerRef } = useCustomContainerWidth()

  const [layouts, setLayouts] = useState<Partial<Record<string, Layout>>>(() => {
    const saved = localStorage.getItem('driveGridV4')
    if (saved) {
      try {
        const parsed = JSON.parse(saved)
        if (parsed?.lg) return parsed
      } catch {
        /* ignore */
      }
    }
    return {
      lg: [
        { i: 'daily', x: 0, y: 0, w: 12, h: 7, minH: 2, minW: 2 },
        { i: 'last-backup', x: 12, y: 0, w: 12, h: 7, minH: 2, minW: 2 },
        { i: 'growth', x: 0, y: 7, w: 24, h: 10, minH: 2, minW: 2 },
        { i: 'history', x: 0, y: 17, w: 12, h: 10, minH: 2, minW: 2 },
        { i: 'success', x: 12, y: 17, w: 12, h: 10, minH: 2, minW: 2 },
        { i: 'cloud', x: 0, y: 27, w: 24, h: 7, minH: 2, minW: 2 },
      ],
    }
  })

  const onLayoutChange = (_currentLayout: Layout, allLayouts: Partial<Record<string, Layout>>) => {
    setLayouts(allLayouts)
    localStorage.setItem('driveGridV4', JSON.stringify(allLayouts))
  }

  const serverStatus = server.data ?? {}
  const backupStatus = backup.data ?? {}
  const driveUnavailable = Boolean(backupStatus.driveError && !backupStatus.driveStale)
  const driveDataTime = backupStatus.driveDataAt
    ? new Date(backupStatus.driveDataAt).toLocaleString(lang === 'vi' ? 'vi-VN' : 'en-US')
    : null

  // Disk Storage Math
        
  // Drive Storage Math
  const { about } = backupStatus

  const backupFolderSize = backupStatus.size?.bytes ?? 0
  const driveTotal = driveUnavailable ? 0 : (about?.total || 0)
  const rawDrivePercent = driveTotal && about?.used != null ? Math.min(100, Math.max(0, about.used / driveTotal * 100)) : 0
  const driveUsedPercent = driveTotal && about?.used != null ? (rawDrivePercent > 0 && rawDrivePercent < 0.01 ? '< 0.01' : rawDrivePercent.toFixed(1)) : '—'
  const driveFreePercent = driveTotal && about?.free != null ? Math.min(100, Math.max(0, about.free / driveTotal * 100)).toFixed(1) : '—'

  // PIE CHART DATA — phân loại dung lượng theo loại (site/database/panel)
  const activities = backupStatus.activity ?? []
  const breakdownBytes = backupStatus.todayBreakdownBytes || { site: 0, database: 0, panel: 0 }
  const totalPieBytes = breakdownBytes.site + breakdownBytes.database + breakdownBytes.panel
  
  
  const pieData =
    totalPieBytes === 0
      ? [{ name: 'idle', value: 1 }]
      : [
          { name: categoryLabel('site', lang), value: breakdownBytes.site },
          { name: categoryLabel('database', lang), value: breakdownBytes.database },
          { name: categoryLabel('panel', lang), value: breakdownBytes.panel },
        ]
  const PIE_COLORS = ['#4caf50', '#2196f3', '#ff9800']

  // Parse History to get charts data
  let chartData: ChartDatum[] = []
  const history = backupStatus.history
  if (!driveUnavailable && history && Array.isArray(history)) {
    const historyMap = new Map()
    history.forEach((h) => historyMap.set(h.date, { bytes: h.bytes, files: h.files || 0 }))

    const last14Days = calendarDays(serverStatus.server_time)

    chartData = last14Days.map((dateStr, i) => {
      const histData = historyMap.get(dateStr) || { bytes: 0, files: 0 }
        const bytes = histData.bytes
        
      let diff = 0
        if (i > 0) {
          const prevBytes = (historyMap.get(last14Days[i - 1]) || { bytes: 0 }).bytes
          diff = bytes - prevBytes
        }

      let success = 0
      const failed = 0

      const files = histData.files || 0
        if (files > 0) {
          success = files
        }

      return {
        date: dateStr,
        totalSize: bytes,
        diffSize: diff,
        success,
        failed,
      }
    })

      }

  const cardStyle: CSSProperties = {
    backgroundColor: theme.cardBg,
    border: `1px solid ${theme.cardBorder}`,
    boxShadow: isDark ? 'none' : '0 1px 3px rgba(0,0,0,0.1)',
    display: 'flex',
    flexDirection: 'column',
    containerType: 'size' as unknown as 'size',
  }
  const dragHandleStyle: CSSProperties = {
    padding: 'clamp(0px, 2cqh, 8px) 10px',
    cursor: 'grab',
    fontSize: 'clamp(4px, 8cqmin, 12px)',
    fontWeight: 'bold',
    color: theme.titleColor,
    textTransform: 'uppercase',
    display: 'flex',
    alignItems: 'center',
    lineHeight: 1,
  }

  const tickFormatter = (tick: string) => {
    const parts = tick.split('-')
    return parts.length === 3 ? `${parts[2]}/${parts[1]}` : tick
  }

  return (
    <div className="legacy-page" style={{ width: '100%' }}>
      <h2 style={{ margin: '0 0 15px 0', fontSize: '22px', fontWeight: 'normal', color: theme.titleColor }}>
        {tr(lang, 'homeDashboard')}
      </h2>
      {backup.loading && !backup.data && <p role="status" style={{ color: theme.textSecondary }}>{lang === 'vi' ? 'Đang đọc dữ liệu Google Drive…' : 'Loading Google Drive data…'}</p>}
      {backup.error && <p role="alert" style={{ color: theme.errorText }}>{lang === 'vi' ? `Không thể đọc Google Drive: ${backup.error}` : `Could not load Google Drive: ${backup.error}`}</p>}
      {backupStatus.driveStale && <p role="status" style={{ color: theme.textSecondary }}>{lang === 'vi' ? 'Đang hiển thị dữ liệu Drive đã lưu' : 'Showing saved Drive data'}{driveDataTime ? ` (${driveDataTime})` : ''}.</p>}
      {server.error && <p role="alert" style={{ color: theme.errorText }}>{lang === 'vi' ? `Không thể đọc máy chủ: ${server.error}` : `Could not load server: ${server.error}`}</p>}

      <div style={{ margin: '0 -15px' }}>
        <div ref={containerRef} style={{ minHeight: '500px', width: '100%' }}>
          {containerWidth > 0 && (
            <ErrorBoundary>
              <Responsive
                width={containerWidth}
                className="layout"
                layouts={layouts}
                breakpoints={{ lg: 1200, md: 996, sm: 768, xs: 480, xxs: 0 }}
                cols={{ lg: 24, md: 20, sm: 12, xs: 8, xxs: 4 }}
                rowHeight={30}
                onLayoutChange={onLayoutChange}
                dragConfig={{ handle: '.drag-handle' }}
                resizeConfig={{ handles: ['s', 'w', 'e', 'n', 'sw', 'nw', 'se', 'ne'] }}
                margin={[15, 15]}
              >
                {/* DAILY PIE */}
                <div key="daily" style={cardStyle}>
                  <div className="drag-handle" style={dragHandleStyle}>
                    {tr(lang, 'dailyBackups')}
                  </div>
                  <div
                    style={{
                      padding: '0 10px 10px 10px',
                      flex: 1,
                      overflow: 'hidden',
                      minHeight: 0,
                      display: 'flex',
                      alignItems: 'center',
                    }}
                  >
                    {driveUnavailable ? (
                      <div style={{ width: '100%', textAlign: 'center', color: theme.textSecondary, fontSize: '12px' }}>
                        {lang === 'vi' ? 'Không thể tải dữ liệu Google Drive' : 'Google Drive data is unavailable'}
                      </div>
                    ) : (<>
                        <div style={{ flex: 1, position: 'relative', height: '130px' }}>
                          <ResponsiveContainer>
                            <PieChart>
                              <Pie
                                data={pieData}
                                cx="50%"
                                cy="50%"
                                innerRadius="65%"
                                outerRadius="85%"
                                paddingAngle={2}
                                dataKey="value"
                                stroke="none"
                              >
                                {pieData.map((_, index) => (
                                  <Cell key={`cell-${index}`} fill={PIE_COLORS[index % PIE_COLORS.length]} />
                                ))}
                              </Pie>
                              <Tooltip
                                formatter={(value) => formatBytes(value as number)}
                                contentStyle={{
                                  backgroundColor: theme.cardBg,
                                  border: `1px solid ${theme.cardBorder}`,
                                  fontSize: '12px',
                                }}
                              />
                            </PieChart>
                          </ResponsiveContainer>
                        </div>
                        <div style={{ flex: 1.5, fontSize: '12px' }}>
                          {[
                            { label: categoryLabel('site', lang), value: breakdownBytes.site, color: '#4caf50' },
                            { label: categoryLabel('database', lang), value: breakdownBytes.database, color: '#2196f3' },
                            { label: categoryLabel('panel', lang), value: breakdownBytes.panel, color: '#ff9800' },
                          ].map((item) => {
                            const pct = totalPieBytes > 0 ? Math.round((item.value / totalPieBytes) * 100) : 0
                            return (
                              <div key={item.label} style={{ display: 'flex', alignItems: 'center', marginBottom: '12px' }}>
                                <div style={{ width: '8px', height: '8px', borderRadius: '50%', backgroundColor: item.color, marginRight: '8px', flexShrink: 0 }} />
                                <div style={{ flex: 1 }}>{item.label}</div>
                                <div style={{ marginRight: '10px', color: theme.textSecondary }}>{pct}%</div>
                                <strong style={{ color: theme.textPrimary }}>{formatBytes(item.value)}</strong>
                              </div>
                            )
                          })}
                        </div>
                    </>)}
                      
                  </div>
                </div>

                {/* LAST BACKUP */}
                <div key="last-backup" style={cardStyle}>
                  <div className="drag-handle" style={dragHandleStyle}>
                    {tr(lang, 'lastBackup')}
                  </div>
                  <div style={{ padding: '0 10px 10px 10px', flex: 1, overflow: 'hidden', minHeight: 0 }}>
                    {(() => {
                      const lastBackup = activities.length > 0
                        ? [...activities].sort((a, b) => {
                            const da = activityStamp(a)
                            const db = activityStamp(b)
                            return db.localeCompare(da)
                          })[0]
                        : null
                      const nextScheduleDisplay = nextCronRun((cron.data ?? []).filter(job => job.id === 'drive-sync'), serverStatus.server_time, lang) ?? '—'

                      if (!lastBackup) {
                        return (
                          <div style={{ textAlign: 'center', color: theme.textSecondary, padding: '20px', fontSize: '12px' }}>
                            {driveUnavailable ? (lang === 'vi' ? 'Không thể tải lịch sử Google Drive' : 'Google Drive history is unavailable') : tr(lang, 'noBackupYet')}
                          </div>
                        )
                      }

                      const isSuccess = activityStatus(lastBackup.status) === 'success'
                      const durationStr = formatDuration(lastBackup.duration == null || lastBackup.duration === '' ? NaN : Number(lastBackup.duration))

                      return (
                        <div style={{ fontSize: '12px' }}>
                          <div
                            style={{
                              marginBottom: '10px',
                              padding: '8px 0',
                              borderBottom: `1px solid ${theme.gridLine}`,
                            }}
                          >
                            <strong style={{ fontSize: '13px', color: isSuccess ? theme.successText : theme.errorText, display: 'block' }}>
                              {isSuccess ? tr(lang, 'backupSuccessful') : tr(lang, 'backupFailed')}
                            </strong>
                            <span style={{ color: theme.textSecondary, fontSize: '11px' }}>
                              {activityStamp(lastBackup)}
                            </span>
                          </div>
                          <div style={{ lineHeight: '1.9' }}>
                            <div style={{ display: 'flex', justifyContent: 'space-between', gap: '12px', borderBottom: `1px dashed ${theme.gridLine}`, paddingBottom: '1px' }}>
                              <span style={{ color: theme.textSecondary }}>{tr(lang, 'job')}</span>
                              <strong style={{ textAlign: 'right' }}>{tr(lang, 'driveBackupJob')}</strong>
                            </div>
                            <div style={{ display: 'flex', justifyContent: 'space-between', borderBottom: `1px dashed ${theme.gridLine}`, paddingBottom: '1px' }}>
                              <span style={{ color: theme.textSecondary }}>{tr(lang, 'duration')}</span>
                              <strong>{durationStr}</strong>
                            </div>
                            <div style={{ display: 'flex', justifyContent: 'space-between', borderBottom: `1px dashed ${theme.gridLine}`, paddingBottom: '1px' }}>
                              <span style={{ color: theme.textSecondary }}>{tr(lang, 'destination')}</span>
                              <strong>Google Drive</strong>
                            </div>
                            <div style={{ display: 'flex', justifyContent: 'space-between', gap: '12px', paddingTop: '1px' }}>
                              <span style={{ color: theme.textSecondary }}>{tr(lang, 'nextSchedule')}</span>
                              <strong style={{ color: theme.titleColor }}>{nextScheduleDisplay}</strong>
                            </div>
                          </div>
                        </div>
                      )
                    })()}
                  </div>
                </div>

                {/* GROWTH CHART */}
                <div key="growth" style={cardStyle}>
                  <div className="drag-handle" style={dragHandleStyle}>
                    {tr(lang, 'dataGrowth')} ({tr(lang, 'last14Days')})
                  </div>
                  <div style={{ padding: '0 10px 10px 10px', flex: 1, minHeight: 0 }}>
                    {chartData.length > 0 ? (
                      <ResponsiveContainer width="100%" height="100%">
                        <BarChart data={chartData} margin={{ top: 10, right: 10, left: 10, bottom: 0 }}>
                          <CartesianGrid strokeDasharray="3 3" stroke={theme.gridLine} vertical={false} />
                          <XAxis
                            dataKey="date"
                            stroke={theme.textSecondary}
                            fontSize={10}
                            tickLine={false}
                            axisLine={false}
                            padding={{ left: 20, right: 20 }}
                            tickFormatter={tickFormatter}
                            minTickGap={5}
                          />
                          <YAxis
                            stroke={theme.textSecondary}
                            fontSize={10}
                            tickLine={false}
                            axisLine={false}
                            tickFormatter={(tick) => formatBytes(tick)}
                          />
                          <Tooltip content={<ChartTooltip unit="bytes" palette={{ bg: theme.cardBg, border: theme.cardBorder, text: theme.textPrimary }} />} cursor={{ fill: theme.gridLine }} />
                          <Bar dataKey="diffSize" barSize={15}>
                            {chartData.map((entry, index) => (
                              <Cell key={`cell-${index}`} fill={entry.diffSize < 0 ? '#ef5350' : '#29b6f6'} />
                            ))}
                          </Bar>
                        </BarChart>
                      </ResponsiveContainer>
                    ) : (
                      <div style={{ textAlign: 'center', marginTop: '60px', fontSize: '12px', color: theme.textSecondary }}>
                        {tr(lang, 'noData')}
                      </div>
                    )}
                  </div>
                </div>

                {/* HISTORY CHART */}
                <div key="history" style={cardStyle}>
                  <div className="drag-handle" style={dragHandleStyle}>
                    {tr(lang, 'backupsHistory')} ({tr(lang, 'last14Days')})
                  </div>
                  <div style={{ padding: '0 10px 10px 10px', flex: 1, minHeight: 0 }}>
                    {chartData.length > 0 ? (
                      <ResponsiveContainer width="100%" height="100%">
                        <AreaChart data={chartData} margin={{ top: 10, right: 10, left: 10, bottom: 0 }}>
                          <CartesianGrid strokeDasharray="3 3" stroke={theme.gridLine} vertical={false} />
                          <XAxis
                            dataKey="date"
                            stroke={theme.textSecondary}
                            fontSize={10}
                            tickLine={false}
                            axisLine={false}
                            padding={{ left: 20, right: 20 }}
                            tickFormatter={tickFormatter}
                            minTickGap={5}
                          />
                          <YAxis
                            stroke={theme.textSecondary}
                            fontSize={10}
                            tickLine={false}
                            axisLine={false}
                            allowDataOverflow={false}
                            domain={[0, 'auto']}
                            tickFormatter={(tick) => formatBytes(tick)}
                          />
                          <Tooltip content={<ChartTooltip unit="bytes" palette={{ bg: theme.cardBg, border: theme.cardBorder, text: theme.textPrimary }} />} />
                          <defs>
                            <linearGradient id="colorTotal" x1="0" y1="0" x2="0" y2="1">
                              <stop offset="5%" stopColor={theme.successText} stopOpacity={0.8} />
                              <stop offset="95%" stopColor={theme.successText} stopOpacity={0.1} />
                            </linearGradient>
                          </defs>
                          <Area type="monotone" dataKey="totalSize" stroke={theme.successText} strokeWidth={2} fill="url(#colorTotal)" />
                        </AreaChart>
                      </ResponsiveContainer>
                    ) : (
                      <div style={{ textAlign: 'center', marginTop: '60px', fontSize: '12px', color: theme.textSecondary }}>
                        {tr(lang, 'noData')}
                      </div>
                    )}
                  </div>
                </div>

                {/* SUCCESS CHART */}
                <div key="success" style={cardStyle}>
                  <div className="drag-handle" style={dragHandleStyle}>
                    {tr(lang, 'successTrend')} ({tr(lang, 'last14Days')})
                  </div>
                  <div style={{ padding: '0 10px 10px 10px', flex: 1, minHeight: 0 }}>
                    {chartData.length > 0 ? (
                      <ResponsiveContainer width="100%" height="100%">
                        <BarChart data={chartData} margin={{ top: 10, right: 10, left: 10, bottom: 0 }}>
                          <CartesianGrid strokeDasharray="3 3" stroke={theme.gridLine} vertical={false} />
                          <XAxis
                            dataKey="date"
                            stroke={theme.textSecondary}
                            fontSize={10}
                            tickLine={false}
                            axisLine={false}
                            padding={{ left: 20, right: 20 }}
                            tickFormatter={tickFormatter}
                            minTickGap={5}
                          />
                          <YAxis stroke={theme.textSecondary} fontSize={10} tickLine={false} axisLine={false} domain={[0, 'auto']} allowDecimals={false} />
                          <Tooltip
                            contentStyle={{ backgroundColor: theme.cardBg, border: `1px solid ${theme.cardBorder}`, fontSize: '12px' }}
                            cursor={{ fill: theme.gridLine }}
                          />
                          <Bar dataKey="success" stackId="a" fill={theme.successText} barSize={20} name={tr(lang, 'filesCount')} />
                        </BarChart>
                      </ResponsiveContainer>
                    ) : (
                      <div style={{ textAlign: 'center', marginTop: '60px', fontSize: '12px', color: theme.textSecondary }}>
                        {tr(lang, 'noData')}
                      </div>
                    )}
                  </div>
                </div>


                {/* LOCAL BACKUP ON SERVER */}



                {/* CLOUD STORAGE */}
                <div key="cloud" style={cardStyle}>
                  <div className="drag-handle" style={dragHandleStyle}>
                    {tr(lang, 'storageDrive')}
                  </div>
                  <div style={{ padding: '0 10px 10px 10px', flex: 1, overflow: 'hidden', minHeight: 0 }}>
                    <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'baseline', marginBottom: '5px' }}>
                      <span style={{ fontSize: 'clamp(12px, 15cqmin, 18px)', fontWeight: 'bold' }}>{driveUnavailable || !backupStatus.size ? '—' : formatBytes(backupFolderSize)}</span>
                      <span style={{ fontSize: 'clamp(3px, 8cqmin, 10px)', color: theme.textSecondary }}>
                        {lang === 'vi' ? 'Bản sao lưu / Tổng dung lượng tài khoản' : 'Backups / Account capacity'}
                      </span>
                      <span style={{ fontSize: 'clamp(0px, 15cqmin, 18px)', fontWeight: 'bold' }}>{driveTotal ? formatBytes(driveTotal) : '—'}</span>
                    </div>
                    <div style={{ width: '100%', backgroundColor: theme.gridLine, height: '12px', marginBottom: '5px', borderRadius: '2px', overflow: 'hidden' }}>
                      <div style={{ width: `${rawDrivePercent}%`, backgroundColor: '#2196f3', height: '100%' }} />
                    </div>
                    <div
                      style={{
                        display: 'flex',
                        justifyContent: 'space-between',
                        fontSize: 'clamp(3px, 8cqmin, 10px)',
                        color: theme.textSecondary,
                        marginBottom: 'clamp(0px, 5cqh, 20px)',
                      }}
                    >
                      <span>
                        {lang === 'vi' ? 'Tài khoản đã dùng' : 'Account used'} ({driveUsedPercent}%)
                      </span>
                      <span>
                        {tr(lang, 'free')} ({driveFreePercent}%)
                      </span>
                    </div>
                    <div style={{ fontSize: '12px' }}>
                      <div style={{ width: '100%' }}>
                        <div
                          style={{
                            display: 'flex',
                            justifyContent: 'space-between',
                            borderBottom: `1px solid ${theme.gridLine}`,
                            paddingBottom: '3px',
                            marginBottom: 'clamp(0px, 1cqh, 3px)',
                            fontSize: 'clamp(4px, 8cqmin, 13px)',
                            lineHeight: 1.5,
                          }}
                        >
                          <span>{lang === 'vi' ? 'Số ngày có bản sao lưu' : 'Days with backup files'}</span>
                          <strong style={{ color: theme.successText }}>
                            {driveUnavailable || backupStatus.totalFolders == null ? '—' : backupStatus.totalFolders}
                          </strong>
                        </div>
                      </div>
                    </div>
                  </div>
                </div>
              </Responsive>
            </ErrorBoundary>
          )}
        </div>
      </div>
    </div>
  )
}
