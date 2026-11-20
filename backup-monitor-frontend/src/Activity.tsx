import { localizeMessage } from "./i18n"
import { Fragment, useState, useMemo, useRef } from "react"
import { Check, ChevronDown, X, Trash2 } from "lucide-react"
import type { Lang } from "./i18n"
import { makeTheme, activityStamp, activityStatus, formatTime24, localActivityDeletionKeys, statusLabel, summarizeLocalBackupActivity } from "./shared"
import { apiErrorMessage, clearLog, useBackupStatus } from "./api"

const filterStatuses = ['success', 'failed', 'running', 'unknown']
const driveLogKey = (entry: { date: string; time: string }) => `${entry.date}|${entry.time}`

const formatDurationSeconds = (duration: number | null) => {
  if (duration === null) return '—'
  return duration > 0 && duration < 0.01 ? `${duration.toFixed(3)}s` : `${duration.toFixed(2)}s`
}

export default function Activity({ isDark, lang }: { isDark: boolean, lang: Lang }) {
  const t = makeTheme(isDark)
  const isVi = lang === 'vi'
  const { data: driveData, reload, loading, activityError } = useBackupStatus(60000)
  const [isClearing, setIsClearing] = useState(false)
  const clearGuard = useRef(false)
  const [clearError, setClearError] = useState('')
  const [activeTab, setActiveTab] = useState<'server' | 'drive'>(() => {
    const saved = localStorage.getItem('activity-tab')
    return saved === 'server' || saved === 'drive' ? saved : 'drive'
  })
  const [expandedKey, setExpandedKey] = useState<string | null>(null)
  const [selectedKeys, setSelectedKeys] = useState<Set<string>>(new Set())
  const [filterDate, setFilterDate] = useState('')
  const [filterStatus, setFilterStatus] = useState('')
  const filtering = Boolean(filterDate || filterStatus)
  const labels = {
    activity: isVi ? 'Hoạt động sao lưu' : 'Backup activity',
    status: isVi ? 'Trạng thái sao lưu' : 'Backup status',
    time: isVi ? 'Thời gian bắt đầu' : 'Start time',
    duration: isVi ? 'Thời gian thực hiện' : 'Execution time',
    select: isVi ? 'Chọn' : 'Select',
    sharedTime: isVi ? 'Bỏ lọc để chọn các nhật ký cùng thời điểm' : 'Clear filters to select logs at the same time',
  }
  const driveEntries = driveData?.activity ?? []
  const activityLoading = loading
  const hasSavedActivity = activityError && Array.isArray(activeTab === 'drive' ? driveData?.activity : driveData?.localActivity)
  const groupedLocal = useMemo(() => summarizeLocalBackupActivity(driveData?.localActivity ?? []), [driveData?.localActivity])
  const matches = (entry: { date: string; status: string }) =>
    (!filterDate || entry.date.slice(0, 10) === filterDate) && (!filterStatus || activityStatus(entry.status) === filterStatus)
  const driveActivities = driveEntries.filter(matches).sort((a, b) => activityStamp(b).localeCompare(activityStamp(a)))
  const localActivities = groupedLocal.filter(matches)
  // The API deletes by timestamp (and local backup type), which can match a hidden row.
  const hiddenDriveKeys = new Set(driveEntries.filter(entry => !matches(entry)).map(driveLogKey))
  const hiddenLocalKeys = new Set(groupedLocal.filter(entry => !matches(entry)).flatMap(localActivityDeletionKeys))
  const selectableLocalKeys = new Set(localActivities
    .filter(entry => entry.source !== 'archive' && (!hiddenLocalKeys.size || !localActivityDeletionKeys(entry).some(key => hiddenLocalKeys.has(key))))
    .map(entry => entry.key))
  const visibleKeys = activeTab === 'drive'
    ? driveActivities.map(driveLogKey).filter(key => !hiddenDriveKeys.has(key))
    : [...selectableLocalKeys]
  const selected = new Set(visibleKeys.filter(key => selectedKeys.has(key)))
  const allSelected = visibleKeys.length > 0 && visibleKeys.every(key => selected.has(key))
  const emptyMessage = activityLoading ? (isVi ? 'Đang tải lịch sử…' : 'Loading activity…')
    : activityError ? (isVi ? 'Không tải được lịch sử.' : 'Activity unavailable.')
      : filtering ? (isVi ? 'Không có nhật ký khớp bộ lọc.' : 'No logs match the filters.')
        : (isVi ? 'Chưa có hoạt động nào.' : 'No activity yet.')

  const handleClear = async () => {
    if (clearGuard.current || loading || !driveData || activityError || (filtering && !selected.size)) return
    const targetLabel = activeTab === 'drive' ? 'Google Drive' : (isVi ? 'máy chủ' : 'server')
    const entries = activeTab === 'server'
      ? [...new Set(localActivities.filter(row => selected.has(row.key)).flatMap(localActivityDeletionKeys))]
      : [...selected]
    if (selectedKeys.size > 0 && entries.length === 0) {
      setClearError(isVi ? 'Danh sách nhật ký đã thay đổi. Hãy chọn lại dòng cần xóa.' : 'The log list changed. Please select the rows again.')
      return
    }
    if (window.confirm(entries.length > 0
      ? (isVi ? `Xóa ${selected.size} nhật ký đã chọn trên ${targetLabel}? Các tệp backup vẫn được giữ nguyên.` : `Clear ${selected.size} selected ${targetLabel} log entries? Backup files will be kept.`)
      : (isVi ? `Xóa toàn bộ nhật ký trên ${targetLabel}? Các tệp backup vẫn được giữ nguyên.` : `Clear all ${targetLabel} logs? Backup files will be kept.`))) {
      clearGuard.current = true
      setIsClearing(true)
      setClearError('')
      try {
        await clearLog(activeTab, entries)
        setSelectedKeys(new Set())
        reload()
      } catch (error: unknown) {
        setClearError(apiErrorMessage(error, isVi ? 'Không thể xóa nhật ký.' : 'Could not clear logs.'))
      } finally {
        clearGuard.current = false
        setIsClearing(false)
      }
    }
  }

  const toggleSelected = (key: string) => setSelectedKeys((current) => { const next = new Set(current); if (next.has(key)) next.delete(key); else next.add(key); return next })
  const toggleAll = () => setSelectedKeys((current) => { const next = new Set(current); if (allSelected) visibleKeys.forEach((key) => next.delete(key)); else visibleKeys.forEach((key) => next.add(key)); return next })
  const clearSelection = () => {
    setSelectedKeys(new Set())
    setExpandedKey(null)
  }
  const selectTab = (tab: 'server' | 'drive') => {
    setActiveTab(tab)
    localStorage.setItem('activity-tab', tab)
    clearSelection()
  }
  return (
    <div className="animate-fade-in legacy-page" style={{ padding: '20px' }}>
      <h2 style={{ margin: '0 0 20px 0', fontSize: '20px', fontWeight: '500', color: isDark ? t.titleColor : '#1a4175' }}>
        {isVi ? 'Hoạt động sao lưu' : 'Backup activity'}
      </h2>
      {activityError && <p role="alert" style={{ color: t.errorText }}>{isVi ? 'Không thể cập nhật nhật ký: ' : 'Could not update activity: '}{activityError}</p>}
      {hasSavedActivity && <p role="status" style={{ color: t.textSecondary }}>{isVi ? 'Đang hiển thị nhật ký đã lưu.' : 'Showing saved activity.'}</p>}

      {clearError && (
        <div role="alert" style={{ marginBottom: '12px', color: '#ef4444', fontSize: '13px' }}>
          {localizeMessage(clearError, lang)}
        </div>
      )}

      <div className="activity-toolbar">
        <button className={`legacy-tab-button ${activeTab === 'server' ? 'is-active' : ''}`}
          disabled={isClearing}
          onClick={() => selectTab('server')}
        >
          {isVi ? 'Trên máy chủ' : 'On Server'}
        </button>
        <button className={`legacy-tab-button ${activeTab === 'drive' ? 'is-active' : ''}`}
          disabled={isClearing}
          onClick={() => selectTab('drive')}
        >
          {isVi ? 'Trên Google Drive' : 'On Google Drive'}
        </button>
        <div style={{ flex: 1 }} />
        <button className="legacy-danger-button" onClick={handleClear} disabled={isClearing || loading || !driveData || Boolean(activityError) || (filtering && !selected.size)}>
          <Trash2 size={16} />
          {isClearing ? (isVi ? 'Đang xóa...' : 'Clearing...') : selected.size || filtering
            ? (isVi ? `Xóa nhật ký đã chọn (${selected.size})` : `Clear selected logs (${selected.size})`)
            : (isVi ? 'Xóa tất cả nhật ký' : 'Clear all logs')}
        </button>
      </div>

      <div className="backup-filters">
        <label>{isVi ? 'Ngày ghi nhận' : 'Log date'}<input type="date" disabled={isClearing} value={filterDate} onChange={event => { setFilterDate(event.target.value); clearSelection() }} /></label>
        <label>{labels.status}<select disabled={isClearing} value={filterStatus} onChange={event => { setFilterStatus(event.target.value); clearSelection() }}>
          <option value="">{isVi ? 'Tất cả trạng thái' : 'All statuses'}</option>
          {filterStatuses.map(status => <option key={status} value={status}>{statusLabel(status, lang)}</option>)}
        </select></label>
        {filtering && <button type="button" className="legacy-secondary-button" disabled={isClearing} onClick={() => { setFilterDate(''); setFilterStatus(''); clearSelection() }}>{isVi ? 'Bỏ lọc' : 'Clear filters'}</button>}
        <label className="activity-select-all"><input type="checkbox" disabled={isClearing || visibleKeys.length === 0} checked={allSelected} onChange={toggleAll} />{isVi ? 'Chọn các nhật ký đang hiển thị' : 'Select visible logs'}</label>
      </div>

      <div className="ui-data-table-wrap" style={{ backgroundColor: t.cardBg, border: `1px solid ${t.cardBorder}` }}>
        <table className="responsive-data-table" role="table" style={{ width: '100%', borderCollapse: 'collapse', fontSize: '12px' }}>
          <thead>
            <tr style={{ borderBottom: `1px solid ${isDark ? t.cardBorder : '#e0e0e0'}` }}>
              <th style={{ padding: '16px', textAlign: 'left', fontWeight: 'bold', color: t.textPrimary, width: '28%' }}>
                {labels.activity}
              </th>
              <th style={{ padding: '16px', textAlign: 'left', fontWeight: 'bold', color: t.textPrimary, width: '25%' }}>
                {labels.status}
              </th>
              <th style={{ padding: '16px', textAlign: 'left', fontWeight: 'bold', color: t.textPrimary, width: '30%' }}>
                {labels.time}
              </th>
              <th style={{ padding: '16px', textAlign: 'left', fontWeight: 'bold', color: t.textPrimary, width: '15%' }}>
                {labels.duration}
              </th>
              <th scope="col" style={{ padding: '16px', width: '4%', textAlign: 'center' }}>{labels.select}</th>
            </tr>
          </thead>
          <tbody>
            {activeTab === 'drive' ? (
              driveActivities.length > 0 ? (
                driveActivities.map((act, i) => {
                  const isSuccess = activityStatus(act.status) === 'success'
                  const key = driveLogKey(act)
                  const sharesHiddenTimestamp = hiddenDriveKeys.has(key)
                  return (
                    <tr key={act.id ?? i} role="row" style={{ borderBottom: `1px solid ${isDark ? t.cardBorder : '#f5f5f5'}` }}>
                      <td role="cell" className="data-table-name" style={{ padding: '16px', color: t.textSecondary }}>
                        {act.name || 'aaPanel Job'}
                      </td>
                      <td role="cell" data-label={labels.status} style={{ padding: '16px' }}>
                        <span style={{ color: isSuccess ? '#4caf50' : activityStatus(act.status) === 'failed' ? '#f44336' : t.textSecondary, display: 'inline-flex', alignItems: 'center', gap: '4px', fontWeight: 'bold' }}>
                          {isSuccess ? <Check size={14} strokeWidth={3} /> : activityStatus(act.status) === 'failed' ? <X size={14} strokeWidth={3} /> : '—'}
                          {statusLabel(act.status, lang)}
                        </span>
                      </td>
                      <td role="cell" data-label={labels.time} style={{ padding: '16px', color: t.textSecondary }}>
                        {act.date} {formatTime24(act.time)}
                      </td>
                      <td role="cell" data-label={labels.duration} style={{ padding: '16px', color: t.textSecondary }}>
                        {formatDurationSeconds(act.duration == null || act.duration === '' ? null : Number.isFinite(Number(act.duration)) ? Number(act.duration) : null)}
                      </td>
                      <td role="cell" data-label={labels.select} style={{ padding: '16px', textAlign: 'center' }}><input type="checkbox" disabled={isClearing || sharesHiddenTimestamp} checked={selected.has(key)} onChange={() => toggleSelected(key)} aria-label={`${labels.select} ${act.date} ${act.time}`} title={sharesHiddenTimestamp ? labels.sharedTime : undefined} /></td>
                    </tr>
                  )
                })
              ) : (
                <tr>
                  <td colSpan={5} style={{ padding: '30px', textAlign: 'center', color: t.textSecondary }}>
                    {emptyMessage}
                  </td>
                </tr>
              )
            ) : (
              localActivities.length > 0 ? (
                localActivities.map((act, index) => {
                  const isSuccess = activityStatus(act.status) === 'success'
                  const selectable = selectableLocalKeys.has(act.key)
                  const expanded = expandedKey === act.key
                  const detailId = `backup-activity-details-${index}`
                  const toggleDetails = () => setExpandedKey(expanded ? null : act.key)
                  return (
                    <Fragment key={act.key}>
                    <tr className="activity-summary-row" role="row" onClick={toggleDetails} style={{ borderBottom: `1px solid ${isDark ? t.cardBorder : '#f5f5f5'}` }}>
                      <td role="cell" className="data-table-name" style={{ padding: '16px', color: t.textSecondary }}>
                        <button type="button" className="activity-summary-toggle" aria-expanded={expanded} aria-controls={detailId} aria-label={`${expanded ? (isVi ? 'Thu gọn' : 'Collapse') : (isVi ? 'Xem chi tiết' : 'View details')} ${act.name}, ${act.date} ${act.time}`}>
                          <ChevronDown size={16} className={expanded ? 'is-expanded' : ''} aria-hidden="true" />
                          {act.name}
                        </button>
                      </td>
                      <td role="cell" data-label={labels.status} style={{ padding: '16px' }}>
                        <span style={{ color: isSuccess ? '#4caf50' : activityStatus(act.status) === 'failed' ? '#f44336' : t.textSecondary, display: 'inline-flex', alignItems: 'center', gap: '4px', fontWeight: 'bold' }}>
                          {isSuccess ? <Check size={14} strokeWidth={3} /> : activityStatus(act.status) === 'failed' ? <X size={14} strokeWidth={3} /> : '—'}
                          {statusLabel(act.status, lang)}
                        </span>
                      </td>
                      <td role="cell" data-label={labels.time} style={{ padding: '16px', color: t.textSecondary }} title={act.source === 'archive' ? (isVi ? 'Ngày từ tên tệp backup; không có giờ bắt đầu trong nhật ký' : 'Date from the backup filename; no logged start time') : undefined}>
                        {act.date} {formatTime24(act.time)}
                      </td>
                      <td role="cell" data-label={labels.duration} style={{ padding: '16px', color: t.textSecondary }} title={act.source === 'archive'
                        ? (isVi ? 'Tệp backup cũ còn trên máy chủ; không có nhật ký thời lượng' : 'Legacy backup file on the server; no duration log')
                        : act.source === 'run'
                        ? (isVi ? 'Thời lượng của cả lần chạy' : 'Duration of the whole run')
                        : (isVi ? 'Tổng thời lượng các tệp có cùng thời điểm bắt đầu; không phải thời lượng toàn bộ tác vụ' : 'Sum of file durations with the same start time, not the full job duration')}>
                        {formatDurationSeconds(act.duration)}
                      </td>
                      <td role="cell" data-label={labels.select} style={{ padding: '16px', textAlign: 'center' }} onClick={(event) => event.stopPropagation()}>{act.source !== 'archive' ? <input type="checkbox" disabled={isClearing || !selectable} checked={selected.has(act.key)} onChange={() => toggleSelected(act.key)} aria-label={`${labels.select} ${act.name} ${act.date} ${act.time}`} title={!selectable ? labels.sharedTime : undefined} /> : <span title={isVi ? 'Bản ghi từ tệp backup, không phải nhật ký có thể xóa' : 'Backup inventory, not a deletable log'}>—</span>}</td>
                    </tr>
                    {expanded && <tr>
                      <td colSpan={5} className="activity-detail-cell">
                        <div id={detailId} role="region" aria-label={`${isVi ? 'Chi tiết' : 'Details'} ${act.name}`} className="activity-detail-panel">
                          <div className="activity-detail-heading">
                            <strong>{isVi ? 'Chi tiết' : 'Details'} {act.name}</strong>
                          </div>
                          {act.source === 'archive' && <p className="activity-detail-note">{isVi ? 'Tệp backup đã được ghi nhận trên máy chủ. Ngày lấy từ tên tệp; không có log để xác nhận kết quả, giờ bắt đầu hoặc thời lượng. Bản ghi lịch sử không đảm bảo tệp hiện vẫn tồn tại.' : 'A backup file was observed on the server. Its date comes from the filename; no log confirms the result, start time or duration. A history record does not guarantee that the file still exists.'}</p>}
                          {act.details.length > 0 ? <div className="activity-detail-table-wrap"><table className="activity-detail-table responsive-data-table" role="table">
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
                              return <tr role="row" key={`${activityStamp(detail)}:${detail.name}:${detailIndex}`}>
                                <td role="cell" className="data-table-name">{name || '—'}{detail.path && <div style={{ color: t.textSecondary, overflowWrap: 'anywhere' }}>{detail.path}</div>}</td>
                                <td role="cell" data-label={labels.status}><span style={{ color: status === 'success' ? '#4caf50' : status === 'failed' ? '#f44336' : t.textSecondary }}>{statusLabel(detail.status, lang)}</span></td>
                                <td role="cell" data-label={isVi ? 'Thời điểm ghi nhận' : 'Recorded time'}>{activityStamp(detail)}</td>
                                <td role="cell" data-label={labels.duration}>{formatDurationSeconds(Number.isFinite(duration) && duration >= 0 ? duration : null)}</td>
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
                    {emptyMessage}
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
