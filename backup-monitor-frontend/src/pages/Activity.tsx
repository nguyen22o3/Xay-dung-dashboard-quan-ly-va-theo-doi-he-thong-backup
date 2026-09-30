import { useState, useMemo } from 'react'
import { Check, X, Trash2 } from 'lucide-react'
import type { Lang } from '../language'
import { makeTheme } from '../theme'
import { apiErrorMessage, clearLog, useBackupStatus } from '../api'

interface GroupedActivity {
  key: string
  name: string
  date: string
  time: string
  duration: number | null
  missingDuration: boolean
  status: string
  count: number
}


const formatTime24 = (timeStr: string) => {
  if (!timeStr) return ''
  const isPM = timeStr.toUpperCase().includes('PM')
  const isAM = timeStr.toUpperCase().includes('AM')
  if (!isPM && !isAM) return timeStr
  
  const [time] = timeStr.split(' ')
  const [h, m, s] = time.split(':')
  let hour = parseInt(h, 10)
  
  if (isPM && hour < 12) hour += 12
  if (isAM && hour === 12) hour = 0
  
  return `${hour.toString().padStart(2, '0')}:${m}:${s}`
}

const formatDurationSeconds = (duration: number | null) => {
  if (duration === null) return '—'
  return duration > 0 && duration < 0.01 ? `${duration.toFixed(3)}s` : `${duration.toFixed(2)}s`
}

export default function Activity({ isDark, lang }: { isDark: boolean, lang: Lang }) {
  const t = makeTheme(isDark)
  const isVi = lang === 'vi'
  const { data: driveData, reload } = useBackupStatus(60000)
  const [isClearing, setIsClearing] = useState(false)
  const [clearError, setClearError] = useState('')
  const [activeTab, setActiveTab] = useState<'server' | 'drive'>('drive')

  const handleClear = async () => {
    if (window.confirm(isVi ? 'Bạn có chắc muốn xóa toàn bộ nhật ký?' : 'Are you sure you want to clear logs?')) {
      setIsClearing(true)
      setClearError('')
      try {
        await clearLog(activeTab)
        reload()
      } catch (error: unknown) {
        setClearError(apiErrorMessage(error, isVi ? 'Không thể xóa nhật ký.' : 'Could not clear logs.'))
      } finally {
        setIsClearing(false)
      }
    }
  }

  const driveActivities = driveData?.activity || []
  const localActivities = useMemo(() => driveData?.localActivity ?? [], [driveData?.localActivity])

  const groupedLocal = useMemo(() => {
    const groups = new Map<string, GroupedActivity>()
    localActivities.forEach((act) => {
       const name = act.name || 'aaPanel Job'
       const isSite = name.includes('Website')
       const isDb = name.includes('Database')
       const type = isSite ? 'Backup Site' : (isDb ? 'Backup Database' : name)
       
       // Use hour and minute for grouping
       const timePrefix = act.time.split(':').slice(0, 2).join(':')
       const groupKey = act.date + ' ' + timePrefix + ' ' + type
       
       let existing = groups.get(groupKey)
       if (!existing) {
         existing = {
           key: groupKey,
           name: type,
           date: act.date,
           time: act.time,
           duration: null,
           missingDuration: false,
           status: 'Successful',
           count: 0
         }
         groups.set(groupKey, existing)
       }
       const duration = act.duration === null || act.duration === '' ? NaN : Number(act.duration)
       if (Number.isFinite(duration) && duration >= 0) {
         existing.duration = (existing.duration ?? 0) + duration
       } else {
         existing.missingDuration = true
       }
       existing.count += 1
       if (act.status !== 'Successful' && act.status !== 'Ok') {
         existing.status = 'Failed'
       }
    })
    return Array.from(groups.values()).sort((a, b) => b.key.localeCompare(a.key))
  }, [localActivities])


  return (
    <div className="animate-fade-in legacy-page" style={{ padding: '20px' }}>
      <h2 style={{ margin: '0 0 20px 0', fontSize: '20px', fontWeight: '500', color: isDark ? t.titleColor : '#1a4175' }}>
        {isVi ? 'Hoạt động sao lưu' : 'Backup activity'}
      </h2>

      {clearError && (
        <div role="alert" style={{ marginBottom: '12px', color: '#ef4444', fontSize: '13px' }}>
          {clearError}
        </div>
      )}

      <div style={{ display: 'flex', gap: '8px', marginBottom: '20px' }}>
        <button className={`legacy-tab-button ${activeTab === 'server' ? 'is-active' : ''}`}
          onClick={() => setActiveTab('server')}
          style={{
            backgroundColor: activeTab === 'server' ? '#3b75af' : (isDark ? '#333' : '#e0e0e0'),
            color: activeTab === 'server' ? 'white' : (isDark ? '#ccc' : '#333'),
            border: 'none',
            padding: '8px 16px',
            fontSize: '13px',
            cursor: 'pointer',
            borderRadius: '2px',
            transition: 'all 0.2s'
          }}
        >
          {isVi ? 'Trên máy chủ' : 'On Server'}
        </button>
        <button className={`legacy-tab-button ${activeTab === 'drive' ? 'is-active' : ''}`}
          onClick={() => setActiveTab('drive')}
          style={{
            backgroundColor: activeTab === 'drive' ? '#3b75af' : (isDark ? '#333' : '#e0e0e0'),
            color: activeTab === 'drive' ? 'white' : (isDark ? '#ccc' : '#333'),
            border: 'none',
            padding: '8px 16px',
            fontSize: '13px',
            cursor: 'pointer',
            borderRadius: '2px',
            transition: 'all 0.2s'
          }}
        >
          {isVi ? 'Trên Google Drive' : 'On Google Drive'}
        </button>
        <div style={{ flex: 1 }} />
        <button className="legacy-danger-button"
          onClick={handleClear}
          disabled={isClearing}
          style={{
            backgroundColor: isDark ? '#b71c1c' : '#f44336',
            color: 'white',
            border: 'none',
            padding: '8px 16px',
            fontSize: '13px',
            cursor: isClearing ? 'not-allowed' : 'pointer',
            borderRadius: '2px',
            transition: 'all 0.2s',
            display: 'flex',
            alignItems: 'center',
            gap: '6px',
            opacity: isClearing ? 0.7 : 1
          }}
        >
          <Trash2 size={16} />
          {isClearing ? (isVi ? 'Đang xóa...' : 'Clearing...') : (isVi ? 'Xóa nhật ký' : 'Clear Logs')}
        </button>
      </div>

      <div style={{ backgroundColor: isDark ? t.cardBg : 'white', border: `1px solid ${isDark ? t.cardBorder : '#e0e0e0'}`, borderRadius: '2px' }}>
        <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: '12px' }}>
          <thead>
            <tr style={{ borderBottom: `1px solid ${isDark ? t.cardBorder : '#e0e0e0'}` }}>
              <th style={{ padding: '16px', textAlign: 'left', fontWeight: 'bold', color: t.textPrimary, width: '30%' }}>
                {isVi ? 'Cron' : 'Job name'}
              </th>
              <th style={{ padding: '16px', textAlign: 'left', fontWeight: 'bold', color: t.textPrimary, width: '25%' }}>
                {isVi ? 'Trạng thái sao lưu' : 'Backup status'}
              </th>
              <th style={{ padding: '16px', textAlign: 'left', fontWeight: 'bold', color: t.textPrimary, width: '30%' }}>
                {isVi ? 'Thời gian bắt đầu' : 'Start time'} ▼
              </th>
              <th style={{ padding: '16px', textAlign: 'left', fontWeight: 'bold', color: t.textPrimary, width: '15%' }}>
                {isVi ? 'Thời gian thực hiện' : 'Duration'}
              </th>
            </tr>
          </thead>
          <tbody>
            {activeTab === 'drive' ? (
              driveActivities.length > 0 ? (
                driveActivities.slice().reverse().map((act, i) => {
                  const isSuccess = act.status?.toLowerCase().includes('success') || act.status?.toLowerCase().includes('ok')
                  return (
                    <tr key={i} style={{ borderBottom: `1px solid ${isDark ? t.cardBorder : '#f5f5f5'}` }}>
                      <td style={{ padding: '16px', color: t.textSecondary }}>
                        {act.name || 'aaPanel Job'}
                      </td>
                      <td style={{ padding: '16px' }}>
                        <span style={{ color: isSuccess ? '#4caf50' : '#f44336', display: 'inline-flex', alignItems: 'center', gap: '4px', fontWeight: 'bold' }}>
                          {isSuccess ? <Check size={14} strokeWidth={3} /> : <X size={14} strokeWidth={3} />} 
                          {isSuccess ? (isVi ? 'Thành công' : 'Successful') : (isVi ? 'Thất bại' : 'Failed')}
                        </span>
                      </td>
                      <td style={{ padding: '16px', color: t.textSecondary }}>
                        {act.date} {formatTime24(act.time)}
                      </td>
                      <td style={{ padding: '16px', color: t.textSecondary }}>
                        {act.duration}s
                      </td>
                    </tr>
                  )
                })
              ) : (
                <tr>
                  <td colSpan={4} style={{ padding: '30px', textAlign: 'center', color: t.textSecondary }}>
                    {isVi ? 'Đang tải dữ liệu hoặc chưa có hoạt động nào...' : 'Loading data or no activity found...'}
                  </td>
                </tr>
              )
            ) : (
              localActivities.length > 0 ? (
                groupedLocal.map((act, i) => {
                  const isSuccess = act.status?.toLowerCase().includes('success') || act.status?.toLowerCase().includes('ok')
                  return (
                    <tr key={i} style={{ borderBottom: `1px solid ${isDark ? t.cardBorder : '#f5f5f5'}` }}>
                      <td style={{ padding: '16px', color: t.textSecondary }}>
                        {act.name || 'aaPanel Job'}
                      </td>
                      <td style={{ padding: '16px' }}>
                        <span style={{ color: isSuccess ? '#4caf50' : '#f44336', display: 'inline-flex', alignItems: 'center', gap: '4px', fontWeight: 'bold' }}>
                          {isSuccess ? <Check size={14} strokeWidth={3} /> : <X size={14} strokeWidth={3} />} 
                          {isSuccess ? (isVi ? 'Thành công' : 'Successful') : (isVi ? 'Thất bại' : 'Failed')}
                        </span>
                      </td>
                      <td style={{ padding: '16px', color: t.textSecondary }}>
                        {act.date} {formatTime24(act.time)}
                      </td>
                      <td style={{ padding: '16px', color: t.textSecondary }}>
                        {act.missingDuration ? '—' : formatDurationSeconds(act.duration)}
                      </td>
                    </tr>
                  )
                })
              ) : (
                <tr>
                  <td colSpan={4} style={{ padding: '30px', textAlign: 'center', color: t.textSecondary }}>
                    {isVi ? 'Đang tải dữ liệu hoặc chưa có hoạt động nào...' : 'Loading data or no activity found...'}
                  </td>
                </tr>
              )
            )}
          </tbody>
        </table>
      </div>
    </div>
  )
}

