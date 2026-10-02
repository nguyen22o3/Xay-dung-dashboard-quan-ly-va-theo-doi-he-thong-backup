import { Fragment, useState, useMemo } from 'react'
import { Check, ChevronDown, X, Trash2 } from 'lucide-react'
import type { Lang } from '../language'
import { makeTheme } from '../theme'
import { apiErrorMessage, clearLog, useBackupStatus } from '../api'
import { activityStamp, activityStatus, formatTime24, statusLabel, summarizeLocalBackupActivity } from '../utils'
const formatDurationSeconds = (duration: number | null) => {
  if (duration === null) return '—'
  return duration > 0 && duration < 0.01 ? `${duration.toFixed(3)}s` : `${duration.toFixed(2)}s`
}

export default function Activity({ isDark, lang }: { isDark: boolean, lang: Lang }) {
  const t = makeTheme(isDark)
  const isVi = lang === 'vi'
  const { data: driveData, reload, loading, error } = useBackupStatus(60000)
  const [isClearing, setIsClearing] = useState(false)
  const [clearError, setClearError] = useState('')
  const [activeTab, setActiveTab] = useState<'server' | 'drive'>('drive')
  const [expandedKey, setExpandedKey] = useState<string | null>(null)

  const handleClear = async () => {
    if (activeTab !== 'drive') return
    if (window.confirm(isVi ? 'Xóa nhật ký các lần đẩy backup lên Drive? Các tệp backup vẫn được giữ nguyên.' : 'Clear Drive upload history? Backup files will be kept.')) {
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

  const driveActivities = [...(driveData?.activity ?? [])].sort((a, b) => activityStamp(b).localeCompare(activityStamp(a)))
  const groupedLocal = useMemo(() => summarizeLocalBackupActivity(driveData?.localActivity ?? []), [driveData?.localActivity])
  return (
    <div className="animate-fade-in legacy-page" style={{ padding: '20px' }}>
      <h2 style={{ margin: '0 0 20px 0', fontSize: '20px', fontWeight: '500', color: isDark ? t.titleColor : '#1a4175' }}>
        {isVi ? 'Hoạt động sao lưu' : 'Backup activity'}
      </h2>
      {error && <p role="alert" style={{ color: t.errorText }}>{isVi ? 'Không thể cập nhật nhật ký: ' : 'Could not update activity: '}{error}</p>}
      {driveData?.driveStale && <p role="status" style={{ color: t.textSecondary }}>{isVi ? 'Dữ liệu đã lưu; thời điểm cập nhật: ' : 'Saved data; last updated: '}{driveData.driveDataAt ? new Date(driveData.driveDataAt).toLocaleString(isVi ? 'vi-VN' : 'en-GB', { hour12: false }) : '—'}</p>}

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
        {activeTab === 'drive' && <button className="legacy-danger-button"
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
        </button>}
      </div>

      <div style={{ backgroundColor: isDark ? t.cardBg : 'white', border: `1px solid ${isDark ? t.cardBorder : '#e0e0e0'}`, borderRadius: '2px' }}>
        <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: '12px' }}>
          <thead>
            <tr style={{ borderBottom: `1px solid ${isDark ? t.cardBorder : '#e0e0e0'}` }}>
              <th style={{ padding: '16px', textAlign: 'left', fontWeight: 'bold', color: t.textPrimary, width: '30%' }}>
                {isVi ? 'Hoạt động sao lưu' : 'Backup activity'}
              </th>
              <th style={{ padding: '16px', textAlign: 'left', fontWeight: 'bold', color: t.textPrimary, width: '25%' }}>
                {isVi ? 'Trạng thái sao lưu' : 'Backup status'}
              </th>
              <th style={{ padding: '16px', textAlign: 'left', fontWeight: 'bold', color: t.textPrimary, width: '30%' }}>
                {isVi ? 'Thời gian bắt đầu' : 'Start time'} ▼
              </th>
              <th style={{ padding: '16px', textAlign: 'left', fontWeight: 'bold', color: t.textPrimary, width: '15%' }}>
                {isVi ? 'Thời lượng ghi nhận' : 'Recorded duration'}
              </th>
            </tr>
          </thead>
          <tbody>
            {activeTab === 'drive' ? (
              driveActivities.length > 0 ? (
                driveActivities.map((act, i) => {
                  const isSuccess = activityStatus(act.status) === 'success'
                  return (
                    <tr key={i} style={{ borderBottom: `1px solid ${isDark ? t.cardBorder : '#f5f5f5'}` }}>
                      <td style={{ padding: '16px', color: t.textSecondary }}>
                        {act.name || 'aaPanel Job'}
                      </td>
                      <td style={{ padding: '16px' }}>
                        <span style={{ color: isSuccess ? '#4caf50' : activityStatus(act.status) === 'failed' ? '#f44336' : t.textSecondary, display: 'inline-flex', alignItems: 'center', gap: '4px', fontWeight: 'bold' }}>
                          {isSuccess ? <Check size={14} strokeWidth={3} /> : activityStatus(act.status) === 'failed' ? <X size={14} strokeWidth={3} /> : '—'}
                          {statusLabel(act.status, lang)}
                        </span>
                      </td>
                      <td style={{ padding: '16px', color: t.textSecondary }}>
                        {act.date} {formatTime24(act.time)}
                      </td>
                      <td style={{ padding: '16px', color: t.textSecondary }}>
                        {formatDurationSeconds(act.duration == null || act.duration === '' ? null : Number.isFinite(Number(act.duration)) ? Number(act.duration) : null)}
                      </td>
                    </tr>
                  )
                })
              ) : (
                <tr>
                  <td colSpan={4} style={{ padding: '30px', textAlign: 'center', color: t.textSecondary }}>
                    {loading ? (isVi ? 'Đang tải nhật ký…' : 'Loading activity…') : error ? (isVi ? 'Không tải được nhật ký.' : 'Activity unavailable.') : (isVi ? 'Chưa có hoạt động nào.' : 'No activity yet.')}
                  </td>
                </tr>
              )
            ) : (
              groupedLocal.length > 0 ? (
                groupedLocal.map((act, index) => {
                  const isSuccess = activityStatus(act.status) === 'success'
                  const expanded = expandedKey === act.key
                  const detailId = `backup-activity-details-${index}`
                  const toggleDetails = () => setExpandedKey(expanded ? null : act.key)
                  return (
                    <Fragment key={act.key}>
                    <tr className="activity-summary-row" onClick={toggleDetails} style={{ borderBottom: `1px solid ${isDark ? t.cardBorder : '#f5f5f5'}` }}>
                      <td style={{ padding: '16px', color: t.textSecondary }}>
                        <button type="button" className="activity-summary-toggle" aria-expanded={expanded} aria-controls={detailId} aria-label={`${expanded ? (isVi ? 'Thu gọn' : 'Collapse') : (isVi ? 'Xem chi tiết' : 'View details')} ${act.name}, ${act.date} ${act.time}`}>
                          <ChevronDown size={16} className={expanded ? 'is-expanded' : ''} aria-hidden="true" />
                          {act.name}
                        </button>
                      </td>
                      <td style={{ padding: '16px' }}>
                        <span style={{ color: isSuccess ? '#4caf50' : activityStatus(act.status) === 'failed' ? '#f44336' : t.textSecondary, display: 'inline-flex', alignItems: 'center', gap: '4px', fontWeight: 'bold' }}>
                          {isSuccess ? <Check size={14} strokeWidth={3} /> : activityStatus(act.status) === 'failed' ? <X size={14} strokeWidth={3} /> : '—'}
                          {statusLabel(act.status, lang)}
                        </span>
                      </td>
                      <td style={{ padding: '16px', color: t.textSecondary }}>
                        {act.date} {formatTime24(act.time)}
                      </td>
                      <td style={{ padding: '16px', color: t.textSecondary }} title={act.source === 'run'
                        ? (isVi ? 'Thời lượng của cả lần chạy' : 'Duration of the whole run')
                        : (isVi ? 'Tổng thời lượng các tệp có cùng thời điểm bắt đầu; không phải thời lượng toàn bộ tác vụ' : 'Sum of file durations with the same start time, not the full job duration')}>
                        {formatDurationSeconds(act.duration)}
                      </td>
                    </tr>
                    {expanded && <tr>
                      <td colSpan={4} className="activity-detail-cell">
                        <div id={detailId} role="region" aria-label={`${isVi ? 'Chi tiết' : 'Details'} ${act.name}`} className="activity-detail-panel">
                          <div className="activity-detail-heading">
                            <strong>{isVi ? 'Chi tiết' : 'Details'} {act.name}</strong>
                            <span>{act.date} {act.time} · {act.details.length} {isVi ? 'bản ghi tệp' : 'file records'}</span>
                          </div>
                          {act.source === 'files' && <p className="activity-detail-note">{isVi ? 'Log cũ: tổng hợp các tệp cùng thời điểm bắt đầu, chưa có bản ghi kết quả của cả tác vụ.' : 'Legacy logs: grouped file records with the same start time; a whole-run result is unavailable.'}</p>}
                          {act.details.length > 0 ? <div className="activity-detail-table-wrap"><table className="activity-detail-table">
                            <thead><tr>
                              <th>{isVi ? 'Website / Cơ sở dữ liệu' : 'Website / Database'}</th>
                              <th>{isVi ? 'Trạng thái' : 'Status'}</th>
                              <th>{isVi ? 'Thời điểm ghi nhận' : 'Recorded time'}</th>
                              <th>{isVi ? 'Thời lượng tệp' : 'File duration'}</th>
                            </tr></thead>
                            <tbody>{act.details.map((detail, detailIndex) => {
                              const status = activityStatus(detail.status)
                              const duration = detail.duration == null || String(detail.duration).trim() === '' ? NaN : Number(detail.duration)
                              const name = (detail.name ?? '').replace(/^Backup (?:Website|Site):\s*/i, 'Website: ').replace(/^Backup Database:\s*/i, isVi ? 'Cơ sở dữ liệu: ' : 'Database: ')
                              return <tr key={`${activityStamp(detail)}:${detail.name}:${detailIndex}`}>
                                <td>{name || '—'}</td>
                                <td><span style={{ color: status === 'success' ? '#4caf50' : status === 'failed' ? '#f44336' : t.textSecondary }}>{statusLabel(detail.status, lang)}</span></td>
                                <td>{activityStamp(detail)}</td>
                                <td>{formatDurationSeconds(Number.isFinite(duration) && duration >= 0 ? duration : null)}</td>
                              </tr>
                            })}</tbody>
                          </table></div> : <p className="activity-detail-note">{isVi ? 'Chưa có bản ghi chi tiết tệp cho lần chạy này. Bạn có thể xem log tác vụ trong mục Cronjob.' : 'No file details were recorded for this run. View its log in Cron Jobs.'}</p>}
                        </div>
                      </td>
                    </tr>}
                    </Fragment>
                  )
                })
              ) : (
                <tr>
                  <td colSpan={4} style={{ padding: '30px', textAlign: 'center', color: t.textSecondary }}>
                    {loading ? (isVi ? 'Đang tải nhật ký…' : 'Loading activity…') : error ? (isVi ? 'Không tải được nhật ký.' : 'Activity unavailable.') : (isVi ? 'Chưa có hoạt động nào.' : 'No activity yet.')}
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

