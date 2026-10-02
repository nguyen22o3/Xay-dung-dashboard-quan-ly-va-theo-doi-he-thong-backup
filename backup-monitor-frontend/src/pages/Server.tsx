import React, { useEffect, useState, useRef, useMemo } from 'react'
import { Responsive } from 'react-grid-layout'
import type { Layout } from 'react-grid-layout'
import 'react-grid-layout/css/styles.css'
import 'react-resizable/css/styles.css'
import { PieChart, Pie, Cell, Tooltip as RechartsTooltip, ResponsiveContainer, Legend, LineChart, Line, XAxis, YAxis, CartesianGrid, BarChart, Bar } from 'recharts'

import { useServerStatus, useBackupStatus, useWebsitesStatus, useCronJobs, useLocalSnapshots } from '../api'
import { activityStatus, calendarDays, categoryLabel, formatBytes, formatCronSchedule, nextCronRun, readPercentage } from '../utils'
import { makeTheme } from '../theme'
import { CheckCircle, AlertTriangle, Clock, Calendar } from 'lucide-react'
import { tr, type Lang } from '../language'

function useCustomContainerWidth() {
  const [width, setWidth] = useState(0)
  const containerRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!containerRef.current) return
    const ro = new ResizeObserver((entries) => {
      for (const entry of entries) {
        setWidth(entry.contentRect.width)
      }
    })
    ro.observe(containerRef.current)
    return () => ro.disconnect()
  }, [])

  return { width, containerRef }
}

export default function ServerPage({ isDark, lang }: { isDark: boolean; lang: Lang }) {
  const theme = { ...makeTheme(isDark), isDark }
  const server = useServerStatus(15000)
  const backup = useBackupStatus(60000)
  const cron = useCronJobs(60000)
  const websites = useWebsitesStatus(30000)
  
    const localSnapshots = useLocalSnapshots(30000)

  
  const last14Days = useMemo(() => {
    return calendarDays(server.data?.server_time)
  }, [server.data?.server_time])

  const categoryData = useMemo(() => {
    if (!localSnapshots.data) return []
    const map = new Map<string, number>()
    
    // Khởi tạo sẵn 2 mục với giá trị 0 để biểu đồ luôn hiện cả 2
    const siteLabel = categoryLabel('site', lang)
    const dbLabel = categoryLabel('database', lang)
    map.set(siteLabel, 0)
    map.set(dbLabel, 0)

    for (const snap of localSnapshots.data) {
      const date = snap.date.split(' ')[0]
      if (!last14Days.includes(date)) continue

      const cat = categoryLabel(snap.category, lang)
      
      map.set(cat, (map.get(cat) || 0) + snap.size)
    }
    
    return Array.from(map.entries()).map(([name, value]) => ({ name, value }))
  }, [localSnapshots.data, lang, last14Days])

    const growthData = useMemo(() => {
    if (!localSnapshots.data) return []
    const map = new Map<string, number>()
    for (const snap of localSnapshots.data) {
      const date = snap.date.split(' ')[0]
      if (!last14Days.includes(date)) continue
      map.set(date, (map.get(date) || 0) + snap.size)
    }
    
    

    const arr = last14Days.map(dateStr => ({
      date: dateStr,
      size: map.get(dateStr) || 0
    }))
    
    return arr
  }, [localSnapshots.data, last14Days])

  const chartColors = ['#10b981', '#3b82f6', '#f59e0b', '#ec4899', '#8b5cf6']
  
  const s = server.data ?? {}
  const websitesStatus = websites.data ?? []
  const backupJobs = (cron.data ?? []).filter(job => job.id === 'backup-site' || job.id === 'backup-database')
  const scheduledBackupJobs = (cron.data ?? []).filter(job => ['backup-site', 'backup-database', 'backup-panel'].includes(job.id))
  const trackingEnabled = backupJobs.length > 0 && backupJobs.every(job => job.schedule_tracked)
  
  const successFailureData = useMemo(() => {
    const la = backup.data?.localActivity
    if (!la || la.length === 0) return []
    let success = 0
    let failed = 0
    la.forEach((a) => {
      if (a.kind === 'run' && last14Days.includes(a.date)) {
        if (activityStatus(a.status) === 'success') success++
        else if (activityStatus(a.status) === 'failed') failed++
      }
    })
    if (success === 0 && failed === 0) return []
    return [
      { name: lang === 'vi' ? 'Thành công' : 'Success', value: success },
      { name: lang === 'vi' ? 'Thất bại' : 'Failed', value: failed }
    ]
  }, [backup.data, last14Days, lang])

  const durationData = useMemo(() => {
    if (!backup.data?.localActivity) return []
    const map = new Map<string, number>()
    backup.data.localActivity.forEach((a) => {
      if (a.kind === 'run') return
      if (last14Days.includes(a.date)) {
        const duration = a.duration === null || a.duration === '' ? NaN : Number(a.duration)
        if (Number.isFinite(duration) && duration >= 0) {
          map.set(a.date, (map.get(a.date) || 0) + duration)
        }
      }
    })
    return last14Days.map((dateStr: string) => ({
      date: dateStr,
      duration: map.has(dateStr) ? parseFloat(map.get(dateStr)!.toFixed(3)) : null
    }))
  }, [backup.data, last14Days])

  const diskUsage = readPercentage(s.disk?.usage)
  const diskPercent = diskUsage ?? 0
  const cpuPercent = s.cpu ? parseFloat(s.cpu) : 0
  const ramPercent = s.ram && s.ram.usage ? parseFloat(s.ram.usage) : 0

  const { width: containerWidth, containerRef } = useCustomContainerWidth()

  const [layouts, setLayouts] = useState<Partial<Record<string, Layout>>>(() => {
    const saved = localStorage.getItem('serverGridV19')
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
        { i: 'overview-status', x: 0, y: 0, w: 24, h: 4, minH: 3, minW: 6 },
        { i: 'cpu', x: 0, y: 4, w: 6, h: 6, minH: 4, minW: 3 },
        { i: 'ram', x: 6, y: 4, w: 6, h: 6, minH: 4, minW: 3 },
        { i: 'disk', x: 12, y: 4, w: 12, h: 6, minH: 4, minW: 3 },
        { i: 'growth-trend', x: 0, y: 10, w: 12, h: 8, minH: 4, minW: 3 },
        { i: 'backup-chart', x: 12, y: 10, w: 12, h: 8, minH: 4, minW: 3 },
        { i: 'websites', x: 0, y: 18, w: 8, h: 8, minH: 4, minW: 3 },
        { i: 'local-snapshots', x: 8, y: 18, w: 8, h: 8, minH: 4, minW: 3 },
        { i: 'cron-jobs', x: 16, y: 18, w: 8, h: 8, minH: 4, minW: 3 },
      ],
      md: [
        { i: 'overview-status', x: 0, y: 0, w: 20, h: 4, minH: 3, minW: 6 },
        { i: 'cpu', x: 0, y: 4, w: 5, h: 6, minH: 4, minW: 3 },
        { i: 'ram', x: 5, y: 4, w: 5, h: 6, minH: 4, minW: 3 },
        { i: 'disk', x: 10, y: 4, w: 10, h: 6, minH: 4, minW: 3 },
        { i: 'growth-trend', x: 0, y: 10, w: 10, h: 8, minH: 4, minW: 3 },
        { i: 'backup-chart', x: 10, y: 10, w: 10, h: 8, minH: 4, minW: 3 },
        { i: 'websites', x: 0, y: 18, w: 10, h: 8, minH: 4, minW: 3 },
        { i: 'local-snapshots', x: 10, y: 18, w: 10, h: 8, minH: 4, minW: 3 },
        { i: 'cron-jobs', x: 0, y: 26, w: 20, h: 8, minH: 4, minW: 3 },
      ],
      sm: [
        { i: 'overview-status', x: 0, y: 0, w: 12, h: 5, minH: 3, minW: 6 },
        { i: 'cpu', x: 0, y: 5, w: 6, h: 6, minH: 4, minW: 3 },
        { i: 'ram', x: 6, y: 5, w: 6, h: 6, minH: 4, minW: 3 },
        { i: 'disk', x: 0, y: 11, w: 12, h: 6, minH: 4, minW: 3 },
        { i: 'growth-trend', x: 0, y: 17, w: 12, h: 8, minH: 4, minW: 3 },
        { i: 'backup-chart', x: 0, y: 25, w: 12, h: 8, minH: 4, minW: 3 },
        { i: 'websites', x: 0, y: 33, w: 12, h: 8, minH: 4, minW: 3 },
        { i: 'local-snapshots', x: 0, y: 41, w: 12, h: 8, minH: 4, minW: 3 },
        { i: 'cron-jobs', x: 0, y: 49, w: 12, h: 8, minH: 4, minW: 3 },
      ]
    }
  })

  const onLayoutChange = (_current: Layout, allLayouts: Partial<Record<string, Layout>>) => {
    setLayouts(allLayouts)
    localStorage.setItem('serverGridV19', JSON.stringify(allLayouts))
  }

    const cardStyle: React.CSSProperties = {
    backgroundColor: theme.cardBg,
    border: `1px solid ${theme.cardBorder}`,
    boxShadow: isDark ? 'none' : '0 1px 3px rgba(0,0,0,0.1)',
    display: 'flex',
    flexDirection: 'column',
    containerType: 'size' as unknown as 'size',
  }

    const dragHandleStyle: React.CSSProperties = {
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

  return (
    <div className="legacy-page" style={{ width: '100%' }}>
      <h2 style={{ margin: '0 0 15px 0', fontSize: '22px', fontWeight: 'normal', color: theme.titleColor }}>
        {lang === 'vi' ? 'Máy chủ' : 'Server'}
      </h2>
      {server.loading && !server.data && <p role="status" style={{ color: theme.textSecondary }}>{lang === 'vi' ? 'Đang đọc thông số máy chủ…' : 'Loading server status…'}</p>}
      {server.error && <p role="alert" style={{ color: theme.errorText }}>{lang === 'vi' ? `Không thể đọc máy chủ: ${server.error}` : `Could not load server: ${server.error}`}</p>}
      {backup.error && <p role="alert" style={{ color: theme.errorText }}>{lang === 'vi' ? `Không thể đọc lịch sử sao lưu từ Google Drive: ${backup.error}` : `Could not load Google Drive backup history: ${backup.error}`}</p>}
      {localSnapshots.error && <p role="alert" style={{ color: theme.errorText }}>{lang === 'vi' ? `Không thể đọc bản sao lưu trên máy chủ: ${localSnapshots.error}` : `Could not load server backups: ${localSnapshots.error}`}</p>}
      {websites.error && <p role="alert" style={{ color: theme.errorText }}>{lang === 'vi' ? 'Không thể cập nhật trạng thái website: ' : 'Could not update website status: '}{websites.error}</p>}
      <div style={{ margin: '0 -15px' }}>
        <div ref={containerRef} style={{ minHeight: '100vh', width: '100%' }}>
          {containerWidth > 0 && (
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
          {/* OVERVIEW STATUS */}
          <div key="overview-status" style={{ ...cardStyle, display: 'flex', flexDirection: 'column' }}>
            <div className="drag-handle" style={dragHandleStyle}>
              {lang === 'vi' ? 'Trạng thái hoạt động tổng quan' : 'Overview Status'}
            </div>
            <div style={{ flex: 1, display: 'flex', gap: '15px', padding: '10px 15px', overflowX: 'auto' }}>
              


              {/* Alerts */}
              <div style={{ flex: 1, minWidth: '180px', display: 'flex', alignItems: 'center', gap: '15px', background: theme.gridLine, padding: '15px', borderRadius: '8px' }}>
                <AlertTriangle size={32} color={diskPercent >= 85 ? theme.errorText : theme.successText} />
                <div>
                  <div style={{ fontSize: '12px', color: theme.textSecondary }}>{lang === 'vi' ? 'Tình trạng ổ đĩa' : 'Disk status'}</div>
                  <div style={{ fontSize: '18px', fontWeight: 'bold', color: diskPercent >= 85 ? theme.errorText : theme.successText }}>
                    {diskUsage === null || server.error ? (lang === 'vi' ? 'Chưa xác nhận' : 'Unconfirmed') : diskPercent >= 85 ? (lang === 'vi' ? 'Sắp hết dung lượng' : 'Low disk space') : (lang === 'vi' ? 'Bình thường' : 'Normal')}
                  </div>
                </div>
              </div>

              {/* Last Backup Time */}
              <div style={{ flex: 1, minWidth: '180px', display: 'flex', alignItems: 'center', gap: '15px', background: theme.gridLine, padding: '15px', borderRadius: '8px' }}>
                <Clock size={32} color={theme.titleColor} />
                <div>
                  <div style={{ fontSize: '12px', color: theme.textSecondary }}>{lang === 'vi' ? 'Sao lưu gần nhất' : 'Last Backup'}</div>
                  <div style={{ fontSize: '14px', fontWeight: 'bold', color: theme.titleColor }}>
                    {localSnapshots.data?.[0]?.date || '—'}
                  </div>
                </div>
              </div>

              {/* Next Schedule */}
              <div style={{ flex: 1, minWidth: '180px', display: 'flex', alignItems: 'center', gap: '15px', background: theme.gridLine, padding: '15px', borderRadius: '8px' }}>
                <Calendar size={32} color={theme.titleColor} />
                <div>
                  <div style={{ fontSize: '12px', color: theme.textSecondary }}>{tr(lang, 'nextSchedule')}</div>
                  <div style={{ fontSize: '14px', fontWeight: 'bold', color: theme.titleColor }}>
                    {nextCronRun(scheduledBackupJobs, s.server_time, lang) ?? '—'}
                  </div>
                </div>
              </div>

            </div>
          </div>

          
          {/* CPU CAPACITY */}
          <div key="cpu" style={cardStyle}>
            <div className="drag-handle" style={dragHandleStyle}>
              {lang === 'vi' ? 'Sử dụng CPU' : 'CPU Usage'}
            </div>
            <div style={{ padding: '0 15px 15px 15px', flex: 1, display: 'flex', flexDirection: 'column', justifyContent: 'center' }}>
              <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: '10px' }}>
                <span style={{ fontSize: '24px', fontWeight: 'bold', color: theme.titleColor }}>{s.cpu ? `${cpuPercent.toFixed(1)}%` : '—'}</span>
              </div>
              <div style={{ width: '100%', background: theme.gridLine, height: '24px', borderRadius: '12px', overflow: 'hidden', marginBottom: '10px' }}>
                <div style={{ width: `${cpuPercent}%`, background: cpuPercent >= 85 ? theme.errorText : theme.successText, height: '100%', transition: 'width 0.4s' }} />
              </div>
              <div style={{ display: 'flex', justifyContent: 'space-between', color: theme.textSecondary, fontSize: '12px' }}>
                <span>{lang === 'vi' ? 'Đang dùng' : 'Used'}</span>
                <span>{lang === 'vi' ? 'Nhàn rỗi' : 'Idle'} {s.cpu ? `${(100 - cpuPercent).toFixed(1)}%` : '—'}</span>
              </div>
            </div>
          </div>

          {/* RAM CAPACITY */}
          <div key="ram" style={cardStyle}>
            <div className="drag-handle" style={dragHandleStyle}>
              {lang === 'vi' ? 'Sử dụng RAM' : 'RAM Usage'}
            </div>
            <div style={{ padding: '0 15px 15px 15px', flex: 1, display: 'flex', flexDirection: 'column', justifyContent: 'center' }}>
              {s.ram ? (
                <>
                  <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: '10px' }}>
                    <span style={{ fontSize: '24px', fontWeight: 'bold', color: theme.titleColor }}>{s.ram.used}</span>
                    <span style={{ fontSize: '24px', fontWeight: 'bold', color: theme.titleColor }}>{s.ram.total}</span>
                  </div>
                  <div style={{ width: '100%', background: theme.gridLine, height: '24px', borderRadius: '12px', overflow: 'hidden', marginBottom: '10px' }}>
                    <div style={{ width: `${ramPercent}%`, background: ramPercent >= 85 ? theme.errorText : theme.successText, height: '100%', transition: 'width 0.4s' }} />
                  </div>
                  <div style={{ display: 'flex', justifyContent: 'space-between', color: theme.textSecondary, fontSize: '12px' }}>
                    <span>{lang === 'vi' ? 'Đã dùng' : 'Used'} ({ramPercent.toFixed(1)}%)</span>
                    <span>{lang === 'vi' ? 'Còn trống' : 'Free'} {(100 - ramPercent).toFixed(1)}%</span>
                  </div>
                </>
              ) : null}
            </div>
          </div>

          {/* STORAGE CAPACITY (DISK) */}
          <div key="disk" style={cardStyle}>
            <div className="drag-handle" style={dragHandleStyle}>
              {lang === 'vi' ? 'Tổng dung lượng ổ đĩa' : 'Storage Capacity'}
            </div>
            <div style={{ padding: '0 15px 15px 15px', flex: 1, display: 'flex', flexDirection: 'column', justifyContent: 'center' }}>
              {s.disk ? (
                <>
                  <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: '10px' }}>
                    <span style={{ fontSize: '24px', fontWeight: 'bold', color: theme.titleColor }}>{s.disk.used}</span>
                    <span style={{ fontSize: '24px', fontWeight: 'bold', color: theme.titleColor }}>{s.disk.total}</span>
                  </div>
                  <div style={{ width: '100%', background: theme.gridLine, height: '24px', borderRadius: '12px', overflow: 'hidden', marginBottom: '10px' }}>
                    <div style={{ width: `${diskPercent}%`, background: diskPercent >= 85 ? theme.errorText : theme.successText, height: '100%', transition: 'width 0.4s' }} />
                  </div>
                  <div style={{ display: 'flex', justifyContent: 'space-between', color: theme.textSecondary, fontSize: '12px' }}>
                    <span>{lang === 'vi' ? 'Đã dùng' : 'Used'} ({diskPercent.toFixed(1)}%)</span>
                    <span>{lang === 'vi' ? 'Còn trống' : 'Free'} {(100 - diskPercent).toFixed(1)}%</span>
                  </div>
                </>
              ) : null}
            </div>
          </div>

          {/* DATA GROWTH TREND */}
          <div key="growth-trend" style={cardStyle}>
            <div className="drag-handle" style={dragHandleStyle}>
              {lang === 'vi' ? 'Dung lượng bản sao lưu trên máy chủ theo ngày' : 'Backup size on the server by date'} ({tr(lang, 'last14Days')})
            </div>
            <div style={{ padding: '10px', flex: 1, minHeight: 0 }}>
              {growthData.length > 0 ? (
                <ResponsiveContainer width="100%" height="100%">
                  <LineChart data={growthData} margin={{ top: 10, right: 10, left: 0, bottom: 0 }}>
                    <CartesianGrid strokeDasharray="3 3" stroke={theme.gridLine} />
                    <XAxis dataKey="date" stroke={theme.textSecondary} fontSize={10} tickFormatter={(v) => v.substring(5)} />
                    <YAxis stroke={theme.textSecondary} fontSize={10} tickFormatter={(v) => (v / (1024*1024)).toFixed(0) + 'M'} width={45} />
                    <RechartsTooltip 
                      formatter={(value) => formatBytes(Number(value))}
                      contentStyle={{ backgroundColor: theme.cardBg, borderColor: theme.gridLine, color: theme.titleColor, borderRadius: '8px' }}
                    />
                    <Line type="monotone" dataKey="size" name={lang === 'vi' ? 'Dung lượng' : 'Size'} stroke="#3b82f6" strokeWidth={3} dot={{ r: 4 }} activeDot={{ r: 6 }} />
                  </LineChart>
                </ResponsiveContainer>
              ) : (
                <div style={{ textAlign: 'center', color: theme.textSecondary, padding: '20px', fontSize: '12px' }}>
                  {lang === 'vi' ? 'Đang tải dữ liệu...' : 'Loading chart...'}
                </div>
              )}
            </div>
          </div>

          {/* PIE CHART (STORAGE BY CATEGORY) */}
          <div key="backup-chart" style={cardStyle}>
            <div className="drag-handle" style={dragHandleStyle}>
              {lang === 'vi' ? 'Dung lượng bản sao lưu trên máy chủ theo loại' : 'Backup size on the server by type'} ({tr(lang, 'last14Days')})
            </div>
            <div style={{ padding: '10px', flex: 1, minHeight: 0 }}>
              {categoryData.some(item => item.value > 0) ? (
                <ResponsiveContainer width="100%" height="100%">
                  <PieChart>
                    <Pie
                      data={categoryData}
                      dataKey="value"
                      nameKey="name"
                      cx="50%"
                      cy="50%"
                      innerRadius={50}
                      outerRadius={70}
                      paddingAngle={5}
                    >
                      {categoryData.map((_entry, index) => (
                        <Cell key={`cell-${index}`} fill={chartColors[index % chartColors.length]} />
                      ))}
                    </Pie>
                    <RechartsTooltip 
                      formatter={(value) => formatBytes(Number(value))}
                      contentStyle={{ backgroundColor: theme.cardBg, borderColor: theme.gridLine, color: theme.titleColor, borderRadius: '8px' }}
                      itemStyle={{ color: theme.titleColor }}
                    />
                    <Legend verticalAlign="bottom" height={24} wrapperStyle={{ fontSize: '11px', color: theme.textSecondary }} />
                  </PieChart>
                </ResponsiveContainer>
              ) : (
                <div style={{ textAlign: 'center', color: theme.textSecondary, padding: '20px', fontSize: '12px' }}>
                  {localSnapshots.loading ? (lang === 'vi' ? 'Đang tải dữ liệu...' : 'Loading chart...') : localSnapshots.error ? (lang === 'vi' ? 'Không tải được dữ liệu.' : 'Data unavailable.') : (lang === 'vi' ? 'Chưa có bản sao lưu trong 14 ngày.' : 'No backups in the last 14 days.')}
                </div>
              )}
            </div>
          </div>

          {/* PROTECTED ENTITIES - WEBSITES */}
          <div key="websites" style={cardStyle}>
            <div className="drag-handle" style={dragHandleStyle}>
              {lang === 'vi' ? 'Trạng thái Website' : 'Website Status'}
            </div>
            <div style={{ padding: '0 10px 10px 10px', flex: 1, overflow: 'hidden', minHeight: 0 }}>
              {websitesStatus.length > 0 ? (
                <div style={{ overflowY: 'auto', height: '100%' }}>
                  <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: '11px' }}>
                    <thead>
                      <tr style={{ borderBottom: `1px solid ${theme.gridLine}` }}>
                        <th style={{ padding: '6px 0', textAlign: 'left', color: theme.textSecondary, fontWeight: 'normal' }}>{lang === 'vi' ? 'Tên miền' : 'Domain'}</th>
                        <th style={{ padding: '6px 0', textAlign: 'center', color: theme.textSecondary, fontWeight: 'normal' }}>{lang === 'vi' ? 'Trạng thái' : 'Status'}</th>
                      </tr>
                    </thead>
                    <tbody>
                      {websitesStatus.map((site, i) => {
                        const isOnline = site.status === 'ONLINE'
                        return (
                          <tr key={i} style={{ borderBottom: `1px solid ${theme.gridLine}` }}>
                            <td style={{ padding: '8px 0', display: 'flex', alignItems: 'center', gap: '8px', color: theme.titleColor }}>
                              <span style={{ width: '8px', height: '8px', borderRadius: '50%', backgroundColor: isOnline ? theme.successText : theme.errorText, display: 'inline-block', flexShrink: 0 }} />
                              {site.name}
                            </td>
                            <td style={{ padding: '8px 0', textAlign: 'center' }}>
                              <span style={{ padding: '2px 6px', borderRadius: '10px', fontSize: '9px', fontWeight: 'bold', backgroundColor: isOnline ? (isDark ? '#1b4d3e' : '#e8f5e9') : (isDark ? '#4a1111' : '#ffebee'), color: isOnline ? theme.successText : theme.errorText }}>
                                {site.status}
                              </span>
                            </td>
                          </tr>
                        )
                      })}
                    </tbody>
                  </table>
                </div>
              ) : (
                <div style={{ textAlign: 'center', color: theme.textSecondary, padding: '20px', fontSize: '12px' }}>
                  {lang === 'vi' ? 'Đang tải...' : 'Loading...'}
                </div>
              )}
            </div>
          </div>

          {/* PROTECTED ENTITIES - RECENT BACKUPS */}
          <div key="local-snapshots" style={cardStyle}>
            <div className="drag-handle" style={dragHandleStyle}>
              {lang === 'vi' ? 'Tệp sao lưu trên máy chủ' : 'Backup files on the server'} ({tr(lang, 'last14Days')})
            </div>
            <div style={{ padding: '0 10px 10px 10px', flex: 1, overflow: 'hidden', minHeight: 0 }}>
              {localSnapshots.data && localSnapshots.data.length > 0 ? (
                <div style={{ overflowY: 'auto', height: '100%' }}>
                  <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: '11px' }}>
                    <thead>
                      <tr style={{ borderBottom: `1px solid ${theme.gridLine}` }}>
                        <th style={{ padding: '6px 0', textAlign: 'left', color: theme.textSecondary, fontWeight: 'normal' }}>{tr(lang, 'fileName')}</th>
                        <th style={{ padding: '6px 0', textAlign: 'center', color: theme.textSecondary, fontWeight: 'normal' }}>{lang === 'vi' ? 'Dung lượng' : 'Size'}</th>
                      </tr>
                    </thead>
                    <tbody>
                      {localSnapshots.data.filter(s => last14Days.includes(s.date.split(' ')[0])).map((snap, i) => (
                        <tr key={i} style={{ borderBottom: `1px solid ${theme.gridLine}` }}>
                          <td style={{ padding: '8px 0', color: theme.titleColor, whiteSpace: 'nowrap',  textOverflow: 'ellipsis', maxWidth: '120px' }} title={snap.name}>
                            {snap.name}
                          </td>
                          <td style={{ padding: '8px 0', textAlign: 'center', color: theme.titleColor }}>{formatBytes(snap.size)}</td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              ) : (
                <div style={{ textAlign: 'center', color: theme.textSecondary, padding: '20px', fontSize: '12px' }}>
                  {lang === 'vi' ? 'Không có bản sao lưu nào.' : 'No backups found.'}
                </div>
              )}
            </div>
          </div>

          {/* CRON JOBS */}
          <div key="cron-jobs" style={cardStyle}>
                          <div className="drag-handle" style={dragHandleStyle}>
                <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', width: '100%' }}>
                  <span>{tr(lang, 'scheduledJobsTitle')}</span>
                  <div style={{ padding: '2px 8px', background: 'rgba(255,255,255,0.05)', border: `1px solid ${theme.gridLine}`, borderRadius: '12px', fontSize: '10px', display: 'flex', alignItems: 'center', gap: '6px', color: theme.successText, textTransform: 'none' }}>
                    <CheckCircle size={12} />
                    <span style={{ color: theme.titleColor, fontWeight: 'bold' }}>{cron.data ? `${scheduledBackupJobs.length} ${tr(lang, 'jobCountUnit')}` : '—'}</span>
                  </div>
                </div>
              </div>
            <div style={{ padding: '0 10px 10px 10px', flex: 1, overflowY: 'auto' }}>
              <table style={{ width: '100%', fontSize: '11px', borderCollapse: 'collapse' }}>
                <thead>
                  <tr style={{ borderBottom: `1px solid ${theme.gridLine}`, color: theme.textSecondary, textAlign: 'left' }}>
                    <th style={{ padding: '6px 4px', fontWeight: 'bold' }}>{tr(lang, 'jobName')}</th>
                    <th style={{ padding: '6px 4px', fontWeight: 'bold' }}>{tr(lang, 'schedule')}</th>
                  </tr>
                </thead>
                <tbody>
                  {scheduledBackupJobs.length > 0 ? (
                    scheduledBackupJobs.map((cJob) => (
                      <tr key={`${cJob.id}:${cJob.schedule}`} style={{ borderBottom: `1px solid ${theme.gridLine}` }}>
                        <td style={{ padding: '6px 4px', whiteSpace: 'nowrap',  textOverflow: 'ellipsis', maxWidth: '120px' }} title={cJob.name}>
                          {tr(lang, cJob.id === 'backup-site' ? 'backupSiteJob' : cJob.id === 'backup-panel' ? 'backupPanelJob' : 'backupDatabaseJob')}
                        </td>
                        <td style={{ padding: '6px 4px', color: theme.textSecondary }}>{formatCronSchedule(cJob.schedule, lang)}</td>
                      </tr>
                    ))
                  ) : (
                    <tr>
                      <td colSpan={2} style={{ textAlign: 'center', padding: '16px', color: theme.textSecondary }}>
                        {lang === 'vi' ? 'Không có lịch chạy nào.' : 'No local cron jobs found.'}
                      </td>
                    </tr>
                  )}
                </tbody>
              </table>
            </div>
          </div>

          
            {/* SUCCESS / FAILURE DONUT CHART */}
            <div key="success-failure" style={cardStyle}>
              <div className="drag-handle" style={dragHandleStyle}>
                {lang === 'vi' ? 'Kết quả sao lưu trên máy chủ' : 'Server backup run results'} ({tr(lang, 'last14Days')})
              </div>
              <div style={{ padding: '10px', flex: 1, minHeight: 0 }}>
                {successFailureData.length > 0 ? (
                  <ResponsiveContainer width="100%" height="100%">
                    <PieChart>
                      <Pie
                        data={successFailureData}
                        dataKey="value"
                        nameKey="name"
                        cx="50%"
                        cy="50%"
                        innerRadius="50%"
                        outerRadius="80%"
                        paddingAngle={2}
                        stroke="none"
                      >
                        {successFailureData.map((entry, index) => (
                          <Cell key={`cell-${index}`} fill={entry.name === 'Thành công' || entry.name === 'Success' ? theme.successText : theme.errorText} />
                        ))}
                      </Pie>
                      <RechartsTooltip 
                        contentStyle={{ backgroundColor: theme.cardBg, borderColor: theme.gridLine, color: theme.titleColor, borderRadius: '8px' }}
                        itemStyle={{ color: theme.titleColor }}
                      />
                      <Legend verticalAlign="bottom" height={24} wrapperStyle={{ fontSize: '11px', color: theme.textSecondary }} />
                    </PieChart>
                  </ResponsiveContainer>
                ) : (
                  <div style={{ textAlign: 'center', color: theme.textSecondary, padding: '20px', fontSize: '12px' }}>
                    {trackingEnabled
                      ? (lang === 'vi' ? 'Chưa có lần sao lưu hoàn tất được ghi nhận. Kết quả sẽ xuất hiện sau lần chạy tiếp theo.' : 'No completed backup runs recorded yet. Results appear after the next run.')
                      : (lang === 'vi' ? 'Chưa có kết quả lần chạy. Vào Cronjob → Bật ghi nhận cron tự động để theo dõi các lần sao lưu tiếp theo.' : 'No run results yet. Go to Cron Jobs → Enable scheduled backup tracking to track future backups.')}
                    <p>{lang === 'vi' ? 'Log cũ chỉ ghi tệp thành công, không đủ để thống kê kết quả từng lần chạy.' : 'Old logs only record completed files, not the result of each run.'}</p>
                  </div>
                )}
              </div>
            </div>

            {/* DURATION BAR CHART */}
            <div key="duration-chart" style={cardStyle}>
              <div className="drag-handle" style={dragHandleStyle}>
                {lang === 'vi' ? 'Tổng thời lượng xử lý tệp đã ghi nhận mỗi ngày (giây)' : 'Total recorded file processing duration per day (seconds)'}
              </div>
              <div style={{ padding: '10px', flex: 1, minHeight: 0 }}>
                {durationData.length > 0 && durationData.some(d => d.duration !== null) ? (
                  <ResponsiveContainer width="100%" height="100%">
                    <BarChart data={durationData} margin={{ top: 10, right: 10, left: -20, bottom: 0 }}>
                      <CartesianGrid strokeDasharray="3 3" stroke={theme.gridLine} vertical={false} />
                      <XAxis dataKey="date" stroke={theme.textSecondary} fontSize={10} tickFormatter={(v) => v.substring(5)} tickLine={false} axisLine={false} />
                      <YAxis stroke={theme.textSecondary} fontSize={10} tickLine={false} axisLine={false} />
                      <RechartsTooltip 
                        contentStyle={{ backgroundColor: theme.cardBg, borderColor: theme.gridLine, color: theme.titleColor, borderRadius: '8px' }}
                        cursor={{ fill: theme.gridLine }}
                      />
                      <Bar dataKey="duration" name={tr(lang, 'durationSeconds')} fill="#8b5cf6" barSize={15} radius={[4, 4, 0, 0]} />
                    </BarChart>
                  </ResponsiveContainer>
                ) : (
                  <div style={{ textAlign: 'center', color: theme.textSecondary, padding: '20px', fontSize: '12px' }}>
                    {tr(lang, 'noData')}
                  </div>
                )}
              </div>
            </div>
            
          </Responsive>
        )}
        </div>
      </div>
    </div>
  )
}
