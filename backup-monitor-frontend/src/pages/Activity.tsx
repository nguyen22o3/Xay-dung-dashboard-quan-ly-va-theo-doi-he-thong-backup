import { Fragment, useState, useMemo } from 'react'
import { Check, ChevronDown, X, Trash2 } from 'lucide-react'
import type { Lang } from '../language'
import { makeTheme } from '../theme'
import { apiErrorMessage, clearLog, useBackupStatus } from '../api'
import { activityStamp, activityStatus, formatTime24, localActivityDeletionKeys, statusLabel, summarizeLocalBackupActivity } from '../utils'
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
  const [activeTab, setActiveTab] = useState<'server' | 'drive'>(() => {
    const saved = localStorage.getItem('activity-tab')
    return saved === 'server' || saved === 'drive' ? saved : 'drive'
  })
  const [expandedKey, setExpandedKey] = useState<string | null>(null)
  const [selectedKeys, setSelectedKeys] = useState<Set<string>>(new Set())
  const driveActivities = [...(driveData?.activity ?? [])].sort((a, b) => activityStamp(b).localeCompare(activityStamp(a)))
  const groupedLocal = useMemo(() => summarizeLocalBackupActivity(driveData?.localActivity ?? []), [driveData?.localActivity])

  const handleClear = async () => {
    const targetLabel = activeTab === 'drive' ? 'Google Drive' : (isVi ? 'máy chủ' : 'server')
    const entries = activeTab === 'server'
      ? [...new Set(groupedLocal.filter(row => selectedKeys.has(row.key)).flatMap(localActivityDeletionKeys))]
      : [...selectedKeys]
    if (selectedKeys.size > 0 && entries.length === 0) {
      setClearError(isVi ? 'Danh sách nhật ký đã thay đổi. Hãy chọn lại dòng cần xóa.' : 'The log list changed. Please select the rows again.')
      return
    }
    if (window.confirm(entries.length > 0
      ? (isVi ? `Xóa ${selectedKeys.size} nhật ký đã chọn trên ${targetLabel}? Các tệp backup vẫn được giữ nguyên.` : `Clear ${selectedKeys.size} selected ${targetLabel} log entries? Backup files will be kept.`)
      : (isVi ? `Xóa toàn bộ nhật ký trên ${targetLabel}? Các tệp backup vẫn được giữ nguyên.` : `Clear all ${targetLabel} logs? Backup files will be kept.`))) {
      setIsClearing(true)
      setClearError('')
      try {
        await clearLog(activeTab, entries)
        setSelectedKeys(new Set())
        reload()
      } catch (error: unknown) {
        setClearError(apiErrorMessage(error, isVi ? 'Không thể xóa nhật ký.' : 'Could not clear logs.'))
      } finally {
        setIsClearing(false)
      }
    }
  }

  const visibleKeys = activeTab === 'drive' ? driveActivities.map(entry => `${entry.date}|${entry.time}`) : groupedLocal.filter(entry => entry.source !== 'archive').map(entry => entry.key)
  const allSelected = visibleKeys.length > 0 && visibleKeys.every((key) => selectedKeys.has(key))
  const toggleSelected = (key: string) => setSelectedKeys((current) => { const next = new Set(current); if (next.has(key)) next.delete(key); else next.add(key); return next })
  const toggleAll = () => setSelectedKeys((current) => { const next = new Set(current); if (allSelected) visibleKeys.forEach((key) => next.delete(key)); else visibleKeys.forEach((key) => next.add(key)); return next })
  const selectTab = (tab: 'server' | 'drive') => {
    setActiveTab(tab)
    localStorage.setItem('activity-tab', tab)
    setSelectedKeys(new Set())
  }
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
          onClick={() => selectTab('server')}
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
          onClick={() => selectTab('drive')}
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
        <button className="legacy-danger-button" onClick={handleClear} disabled={isClearing} style={{ backgroundColor: isDark ? '#b71c1c' : '#f44336', color: 'white', border: 'none', padding: '8px 16px', fontSize: '13px', cursor: isClearing ? 'not-allowed' : 'pointer', borderRadius: '2px', transition: 'all 0.2s', display: 'flex', alignItems: 'center', gap: '6px', opacity: isClearing ? 0.7 : 1 }}>
          <Trash2 size={16} />
          {isClearing ? (isVi ? 'Đang xóa...' : 'Clearing...') : (selectedKeys.size > 0 ? (isVi ? `Xóa nhật ký (${selectedKeys.size})` : `Clear Logs (${selectedKeys.size})`) : (isVi ? 'Xóa tất cả nhật ký' : 'Clear all logs'))}
        </button>
      </div>

      <div style={{ backgroundColor: isDark ? t.cardBg : 'white', border: `1px solid ${isDark ? t.cardBorder : '#e0e0e0'}`, borderRadius: '2px' }}>
        <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: '12px' }}>
          <thead>
            <tr style={{ borderBottom: `1px solid ${isDark ? t.cardBorder : '#e0e0e0'}` }}>
              <th style={{ padding: '16px', textAlign: 'left', fontWeight: 'bold', color: t.textPrimary, width: '28%' }}>
                {isVi ? 'Hoạt động sao lưu' : 'Backup activity'}
              </th>
              <th style={{ padding: '16px', textAlign: 'left', fontWeight: 'bold', color: t.textPrimary, width: '25%' }}>
                {isVi ? 'Trạng thái sao lưu' : 'Backup status'}
              </th>
              <th style={{ padding: '16px', textAlign: 'left', fontWeight: 'bold', color: t.textPrimary, width: '30%' }}>
                {isVi ? 'Thời gian bắt đầu' : 'Start time'}
              </th>
              <th style={{ padding: '16px', textAlign: 'left', fontWeight: 'bold', color: t.textPrimary, width: '15%' }}>
                {isVi ? 'Thời gian thực hiện' : 'Execution time'}
              </th>
              <th style={{ padding: '16px', width: '4%', textAlign: 'center' }}><input type="checkbox" checked={allSelected} onChange={toggleAll} aria-label={isVi ? 'Chọn tất cả nhật ký' : 'Select all logs'} /></th>
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
                      <td style={{ padding: '16px', textAlign: 'center' }}><input type="checkbox" checked={selectedKeys.has(`${act.date}|${act.time}`)} onChange={() => toggleSelected(`${act.date}|${act.time}`)} aria-label={`${isVi ? 'Chọn' : 'Select'} ${act.date} ${act.time}`} /></td>
                    </tr>
                  )
                })
              ) : (
                <tr>
                  <td colSpan={5} style={{ padding: '30px', textAlign: 'center', color: t.textSecondary }}>
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
                      <td style={{ padding: '16px', color: t.textSecondary }} title={act.source === 'archive' ? (isVi ? 'Ngày từ tên tệp backup; không có giờ bắt đầu trong nhật ký' : 'Date from the backup filename; no logged start time') : undefined}>
                        {act.date} {formatTime24(act.time)}
                      </td>
                      <td style={{ padding: '16px', color: t.textSecondary }} title={act.source === 'archive'
                        ? (isVi ? 'Tệp backup cũ còn trên máy chủ; không có nhật ký thời lượng' : 'Legacy backup file on the server; no duration log')
                        : act.source === 'run'
                        ? (isVi ? 'Thời lượng của cả lần chạy' : 'Duration of the whole run')
                        : (isVi ? 'Tổng thời lượng các tệp có cùng thời điểm bắt đầu; không phải thời lượng toàn bộ tác vụ' : 'Sum of file durations with the same start time, not the full job duration')}>
                        {formatDurationSeconds(act.duration)}
                      </td>
                      <td style={{ padding: '16px', textAlign: 'center' }} onClick={(event) => event.stopPropagation()}>{act.source !== 'archive' ? <input type="checkbox" checked={selectedKeys.has(act.key)} onChange={() => toggleSelected(act.key)} aria-label={`${isVi ? 'Chọn' : 'Select'} ${act.name} ${act.date} ${act.time}`} /> : <span title={isVi ? 'Bản ghi từ tệp backup, không phải nhật ký có thể xóa' : 'Backup inventory, not a deletable log'}>—</span>}</td>
                    </tr>
                    {expanded && <tr>
                      <td colSpan={5} className="activity-detail-cell">
                        <div id={detailId} role="region" aria-label={`${isVi ? 'Chi tiết' : 'Details'} ${act.name}`} className="activity-detail-panel">
                          <div className="activity-detail-heading">
                            <strong>{isVi ? 'Chi tiết' : 'Details'} {act.name}</strong>
                          </div>
                          {act.source === 'archive' && <p className="activity-detail-note">{isVi ? 'Bản backup cũ còn trên máy chủ. Ngày lấy từ tên tệp; không có log để xác nhận kết quả, giờ bắt đầu hoặc thời lượng.' : 'Legacy backup file still on the server. Its date comes from the filename; no log confirms the result, start time or duration.'}</p>}
                          {act.details.length > 0 ? <div className="activity-detail-table-wrap"><table className="activity-detail-table">
                            <thead><tr>
                              <th>{act.name === 'Backup Site' ? 'Website' : act.name === 'Backup aaPanel' ? 'aaPanel' : (isVi ? 'Cơ sở dữ liệu' : 'Database')}</th>
                              <th>{isVi ? 'Trạng thái' : 'Status'}</th>
                              <th>{isVi ? 'Thời điểm ghi nhận' : 'Recorded time'}</th>
                              <th>{isVi ? 'Thời gian thực thi' : 'Execution time'}</th>
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
                  <td colSpan={5} style={{ padding: '30px', textAlign: 'center', color: t.textSecondary }}>
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

