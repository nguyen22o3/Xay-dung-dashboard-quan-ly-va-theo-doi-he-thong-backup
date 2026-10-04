import { useEffect, useRef, useState, type RefObject } from 'react'
import { Eye, Pause, Pencil, Play, RefreshCw, Search, Trash2, X } from 'lucide-react'
import { apiErrorMessage, deleteCronJob, enableCronTracking, fetchCronJobLog, runCronJob, updateCronJobState, useCronJobs } from '../api'
import type { Lang } from '../language'
import type { CronJob, CronJobLog } from '../types'
import { formatCronSchedule } from '../utils'
import BackupSystemSettings from '../components/BackupSystemSettings'
import { backupTaskLabels as jobLabels } from '../backupTasks'
import type { BackupSystemState } from '../backupSystemApi'

const backupIds = ['backup-site', 'backup-database', 'backup-panel']
const categories = [
  { id: 'all', vi: 'Tất cả tác vụ', en: 'All tasks' },
  { id: 'backup', vi: 'Sao lưu', en: 'Backup' },
  { id: 'drive', vi: 'Đồng bộ Drive', en: 'Drive upload' },
  { id: 'maintenance', vi: 'Dọn dẹp & kiểm tra', en: 'Cleanup & checks' },
]
const normalize = (value: string) => value.normalize('NFD').replace(/[\u0300-\u036f]/g, '').replace(/đ/g, 'd').replace(/Đ/g, 'D').toLowerCase()
const scheduledTime = (job: CronJob) => {
  const [minute, hour] = job.schedule.trim().split(/\s+/)
  return /^\d+$/.test(minute) && /^\d+$/.test(hour) ? Number(hour) * 60 + Number(minute) : 1440
}

function useDialogFocus(active: boolean, ref: RefObject<HTMLElement | null>) {
  useEffect(() => {
    if (!active) return
    const restore = document.activeElement as HTMLElement | null
    const selector = 'button:not(:disabled), select:not(:disabled), input:not(:disabled)'
    ref.current?.querySelector<HTMLElement>(selector)?.focus()
    const trap = (event: KeyboardEvent) => {
      if (event.key !== 'Tab') return
      const items = Array.from(ref.current?.querySelectorAll<HTMLElement>(selector) ?? [])
      const first = items[0], last = items[items.length - 1]
      if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus() }
      else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus() }
    }
    window.addEventListener('keydown', trap)
    return () => { window.removeEventListener('keydown', trap); if (restore?.isConnected) restore.focus() }
  }, [active, ref])
}

export default function CronJobs({ lang }: { lang: Lang }) {
  const vi = lang === 'vi'
  const cron = useCronJobs(15000)
  const jobs = cron.data ?? []
  const [runningJob, setRunningJob] = useState<string | null>(null)
  const [deleteJob, setDeleteJob] = useState<CronJob | null>(null)
  const [deletingJob, setDeletingJob] = useState(false)
  const [deleteError, setDeleteError] = useState<string | null>(null)
  const [stateJob, setStateJob] = useState<CronJob | null>(null)
  const [updatingState, setUpdatingState] = useState(false)
  const [stateError, setStateError] = useState<string | null>(null)
  const [feedback, setFeedback] = useState<{ message: string; error: boolean } | null>(null)
  const [logJob, setLogJob] = useState<CronJob | null>(null)
  const [logData, setLogData] = useState<CronJobLog | null>(null)
  const [logLoading, setLogLoading] = useState(false)
  const [logError, setLogError] = useState<string | null>(null)
  const logRequest = useRef(0)
  const [selectedJobId, setSelectedJobId] = useState('backup-database')
  const [editorRevision, setEditorRevision] = useState(0)
  const [taskExpanded, setTaskExpanded] = useState(true)
  const [enablingTracking, setEnablingTracking] = useState(false)
  const [storage, setStorage] = useState<BackupSystemState | null>(null)
  const [storageBusy, setStorageBusy] = useState(false)
  const [search, setSearch] = useState('')
  const [category, setCategory] = useState('all')
  const logDialog = useRef<HTMLDivElement>(null)
  const deleteDialog = useRef<HTMLDivElement>(null)
  const stateDialog = useRef<HTMLDivElement>(null)
  const taskEditor = useRef<HTMLDivElement>(null)
  useDialogFocus(!!logJob, logDialog)
  useDialogFocus(!!deleteJob, deleteDialog)
  useDialogFocus(!!stateJob, stateDialog)
  const mutating = storageBusy || enablingTracking || runningJob !== null || deleteJob !== null || stateJob !== null
  const currentDeleteJob = deleteJob ? jobs.find(job => job.id === deleteJob.id) : null
  const deleteStale = !!deleteJob && (!currentDeleteJob || currentDeleteJob.schedule !== deleteJob.schedule || currentDeleteJob.enabled !== deleteJob.enabled)
  const deleteRunning = currentDeleteJob?.status === 'running'
  const currentStateJob = stateJob ? jobs.find(job => job.id === stateJob.id) : null
  const stateStale = !!stateJob && (!currentStateJob || currentStateJob.schedule !== stateJob.schedule || currentStateJob.enabled !== stateJob.enabled)
  const stateRunning = currentStateJob?.status === 'running'
  const backupJobs = jobs.filter(job => backupIds.includes(job.id))
  const trackingEnabled = backupJobs.length > 0 && backupJobs.every(job => job.schedule_tracked)
  const filteredJobs = jobs.filter(job => {
    const group = backupIds.includes(job.id) ? 'backup' : job.id === 'drive-sync' ? 'drive' : 'maintenance'
    return (category === 'all' || group === category)
      && normalize(`${jobLabels[job.id]?.vi ?? ''} ${jobLabels[job.id]?.en ?? ''} ${job.name} ${job.script}`).includes(normalize(search.trim()))
  }).sort((a, b) => scheduledTime(a) - scheduledTime(b) || a.id.localeCompare(b.id))
  const destination = (job: CronJob) => {
    if (!storage) return vi ? 'Chưa tải cấu hình' : 'Configuration unavailable'
    if (['drive-sync', 'integrity-check'].includes(job.id)) return `${storage.config.driveRemote}:${storage.config.driveFolder}`
    const folder = ({ 'backup-site': 'site', 'backup-database': 'database', 'backup-panel': 'panel' } as Record<string, string>)[job.id]
    return `${storage.config.backupRoot}${folder ? `/${vi ? '{ngày}' : '{date}'}/${folder}` : ''}`
  }

  const handleEnableTracking = async () => {
    if (mutating || trackingEnabled) return
    if (!window.confirm(vi
      ? 'Bật ghi nhận kết quả cho cron sao lưu website, cơ sở dữ liệu và cấu hình aaPanel? Giữ nguyên lịch chạy, script và tham số; lưu dự phòng crontab trước khi cập nhật. Không chạy sao lưu ngay.'
      : 'Enable result tracking for scheduled website, database and aaPanel configuration backups? Schedules, scripts and arguments stay unchanged; the crontab is backed up before updating. This does not run a backup now.')) return
    setEnablingTracking(true)
    setFeedback(null)
    try {
      await enableCronTracking()
      setFeedback({ error: false, message: vi
        ? 'Đã bật ghi nhận cron tự động. Kết quả sẽ xuất hiện sau lần sao lưu tiếp theo; không tạo lại kết quả từ log cũ.'
        : 'Scheduled backup tracking is enabled. Results appear after the next backup; old log results are not reconstructed.' })
      window.dispatchEvent(new Event('force-refresh'))
    } catch (error: unknown) {
      setFeedback({ error: true, message: apiErrorMessage(error, vi ? 'Không thể bật ghi nhận cron tự động.' : 'Could not enable scheduled backup tracking.') })
    } finally {
      setEnablingTracking(false)
    }
  }

  const openScheduleEditor = (job: CronJob) => {
    setSelectedJobId(job.id)
    setEditorRevision(value => value + 1)
    setTaskExpanded(true)
    requestAnimationFrame(() => {
      taskEditor.current?.scrollIntoView({ behavior: 'smooth', block: 'start' })
      taskEditor.current?.querySelector<HTMLSelectElement>('#backup-task-type')?.focus({ preventScroll: true })
    })
  }

  const loadLog = async (job: CronJob) => {
    const request = ++logRequest.current
    setLogData(null)
    setLogError(null)
    setLogLoading(true)
    try {
      const data = await fetchCronJobLog(job.id)
      if (request === logRequest.current) setLogData(data)
    } catch (error: unknown) {
      if (request === logRequest.current) {
        setLogError(apiErrorMessage(error, vi ? 'Không thể đọc log cronjob.' : 'Could not read the cron job log.'))
      }
    } finally {
      if (request === logRequest.current) setLogLoading(false)
    }
  }

  const closeLog = () => {
    logRequest.current += 1
    setLogJob(null)
  }

  useEffect(() => {
    if (!logJob) return
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        logRequest.current += 1
        setLogJob(null)
      }
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [logJob])

  const handleRun = async (job: CronJob) => {
    const jobId = job.id
    if (!jobId || mutating) return
    const name = jobLabels[jobId]?.[vi ? 'vi' : 'en'] ?? job.name
    const confirmed = window.confirm(vi
      ? `Chạy ngay cronjob "${name}"? Tác vụ này có thể thay đổi hoặc xóa dữ liệu trên máy chủ.`
      : `Run "${name}" now? This job may change or delete server data.`)
    if (!confirmed) return

    setRunningJob(jobId)
    setFeedback(null)
    try {
      const result = await runCronJob(jobId)
      const cleanupCompleted = jobId === 'cleanup-panel' && result.status === 'completed'
      const deletedCount = result.deletedCount ?? 0
      setFeedback({
        message: cleanupCompleted
          ? (deletedCount > 0
              ? (vi ? `Đã dọn dẹp aaPanel: xóa ${deletedCount} file backup cũ.` : `aaPanel cleanup complete: deleted ${deletedCount} old backup files.`)
              : (vi ? 'Đã dọn dẹp aaPanel: không còn file backup cũ cần xóa.' : 'aaPanel cleanup complete: no old backup files to delete.'))
          : (vi
              ? `Đã gửi yêu cầu chạy "${name}". Hãy kiểm tra log sau khi tác vụ kết thúc.`
              : `Started "${name}". Check its log after the job finishes.`),
        error: false,
      })
      cron.reload()
      window.dispatchEvent(new Event('force-refresh'))
    } catch (error: unknown) {
      setFeedback({ message: apiErrorMessage(error, vi ? 'Không thể chạy cronjob.' : 'Could not run the cron job.'), error: true })
    } finally {
      setRunningJob(null)
    }
  }

  const closeDelete = () => { if (!deletingJob) { setDeleteJob(null); setDeleteError(null) } }
  useEffect(() => {
    if (!deleteJob || deletingJob) return
    const escape = (event: KeyboardEvent) => {
      if (event.key === 'Escape') { setDeleteJob(null); setDeleteError(null) }
    }
    window.addEventListener('keydown', escape)
    return () => window.removeEventListener('keydown', escape)
  }, [deleteJob, deletingJob])

  const handleDelete = async () => {
    if (!deleteJob || deletingJob || deleteStale || deleteRunning || cron.error || typeof deleteJob.enabled !== 'boolean') return
    setDeletingJob(true); setDeleteError(null); setFeedback(null)
    try {
      const result = await deleteCronJob(deleteJob.id, deleteJob.schedule, deleteJob.enabled)
      const name = jobLabels[deleteJob.id]?.[lang] ?? deleteJob.name
      setFeedback({ error: false, message: vi
        ? `Đã xóa lịch cron "${name}". Giữ nguyên script, log và backup. Bản dự phòng crontab: ${result.backupPath}`
        : `Deleted the schedule for "${name}". Scripts, logs and backups are unchanged. Crontab recovery copy: ${result.backupPath}` })
      setDeleteJob(null)
      cron.reload()
      window.dispatchEvent(new Event('force-refresh'))
    } catch (error: unknown) {
      setDeleteError(apiErrorMessage(error, vi ? 'Không thể xóa lịch cron.' : 'Could not delete the cron schedule.'))
      cron.reload()
    } finally { setDeletingJob(false) }
  }

  const closeState = () => { if (!updatingState) { setStateJob(null); setStateError(null) } }
  useEffect(() => {
    if (!stateJob || updatingState) return
    const escape = (event: KeyboardEvent) => {
      if (event.key === 'Escape') { setStateJob(null); setStateError(null) }
    }
    window.addEventListener('keydown', escape)
    return () => window.removeEventListener('keydown', escape)
  }, [stateJob, updatingState])

  const handleState = async () => {
    if (!stateJob || updatingState || stateStale || stateRunning || cron.error || typeof stateJob.enabled !== 'boolean') return
    setUpdatingState(true); setStateError(null); setFeedback(null)
    try {
      const enabled = !stateJob.enabled
      const result = await updateCronJobState(stateJob.id, enabled, stateJob.schedule, stateJob.enabled)
      const name = jobLabels[stateJob.id]?.[lang] ?? stateJob.name
      setFeedback({ error: false, message: vi
        ? `Đã ${enabled ? 'bật' : 'tạm dừng'} lịch cron "${name}". Không chạy script ngay. Bản dự phòng crontab: ${result.backupPath}`
        : `${enabled ? 'Enabled' : 'Paused'} the schedule for "${name}". No script was run. Crontab recovery copy: ${result.backupPath}` })
      setStateJob(null)
      cron.reload()
      window.dispatchEvent(new Event('force-refresh'))
    } catch (error: unknown) {
      setStateError(apiErrorMessage(error, vi ? 'Không thể đổi trạng thái cron.' : 'Could not change the cron state.'))
      cron.reload()
    } finally { setUpdatingState(false) }
  }

  return (
    <div className="cron-page backup-management animate-fade-in">
      <div className="cron-page-heading">
        <div>
          <h1>{vi ? 'Quản lý sao lưu' : 'Backup management'}</h1>
          <p>{vi ? 'Cấu hình nơi lưu trữ và quản lý lịch chạy trên cùng một trang.' : 'Storage configuration and scheduled tasks in one place.'}</p>
        </div>
      </div>
      <div ref={taskEditor} className="backup-task-editor-anchor"><BackupSystemSettings lang={lang} expanded={taskExpanded} onToggle={() => setTaskExpanded(!taskExpanded)} onReload={cron.reload} locked={enablingTracking || runningJob !== null || deleteJob !== null || stateJob !== null} onStateChange={setStorage} onBusyChange={setStorageBusy}
        task={{ jobs, selectedId: selectedJobId, revision: editorRevision, loading: cron.loading, error: cron.error, onSelect: setSelectedJobId }} /></div>
      {feedback && <div className={`cron-feedback backup-management-feedback ${feedback.error ? 'cron-feedback--error' : ''}`} role={feedback.error ? 'alert' : 'status'}>{feedback.message}</div>}
      <section className="cron-panel backup-task-panel" id="backup-tasks" aria-labelledby="backup-tasks-title">
        <div className="cron-panel-heading">
          <div>
            <h2 id="backup-tasks-title">{vi ? 'Danh sách tác vụ' : 'Task list'} <span className="backup-count">{jobs.length}</span></h2>
          </div>
          <button className="backup-system-secondary" type="button" onClick={cron.reload} disabled={cron.loading}><RefreshCw size={15} />{vi ? 'Làm mới' : 'Refresh'}</button>
        </div>
        <div className="backup-task-toolbar">
          <select value={category} onChange={event => setCategory(event.target.value)} aria-label={vi ? 'Lọc loại tác vụ' : 'Filter task category'}>{categories.map(item => <option key={item.id} value={item.id}>{item[vi ? 'vi' : 'en']}</option>)}</select>
          <div className="backup-task-search"><Search size={16} /><input value={search} onChange={event => setSearch(event.target.value)} placeholder={vi ? 'Tìm tên tác vụ hoặc script...' : 'Search task or script...'} aria-label={vi ? 'Tìm tác vụ' : 'Search tasks'} />{search && <button type="button" onClick={() => setSearch('')} aria-label={vi ? 'Xóa tìm kiếm' : 'Clear search'}><X size={14} /></button>}</div>
        </div>
        {backupJobs.length > 0 && !trackingEnabled && <div className="cron-tracking-bar">
          <span>{trackingEnabled
            ? (vi ? 'Đã bật ghi nhận kết quả cron sao lưu tự động.' : 'Scheduled backup result tracking is enabled.')
            : (vi ? 'Cron sao lưu tự động chưa được ghi nhận đầy đủ.' : 'Scheduled backup runs are not fully tracked yet.')}</span>
          {!trackingEnabled && <button className="cron-run" type="button" onClick={() => void handleEnableTracking()} disabled={mutating || !!cron.error}>
            {enablingTracking ? (vi ? 'Đang bật...' : 'Enabling...') : (vi ? 'Bật ghi nhận cron tự động' : 'Enable scheduled backup tracking')}
          </button>}
        </div>}

        {cron.error && <div className="cron-message cron-error" role="alert">{vi ? 'Không thể cập nhật danh sách: ' : 'Could not update the list: '}{cron.error}</div>}
        {cron.loading && !cron.data ? (
          <div className="cron-message">{vi ? 'Đang tải cronjob...' : 'Loading cron jobs...'}</div>
        ) : jobs.length === 0 ? (
          <div className="cron-message">{vi ? 'Không tìm thấy lịch chạy script nào.' : 'No scheduled scripts found.'}</div>
        ) : filteredJobs.length === 0 ? <div className="cron-message">{vi ? 'Không có tác vụ phù hợp. Hãy đổi bộ lọc hoặc từ khóa.' : 'No matching tasks. Change the filter or search.'}</div> : (
          <div className="cron-table-wrap">
            <table className="cron-table">
              <thead>
                <tr>
                  <th>{vi ? 'Tác vụ' : 'Job'}</th>
                  <th>{vi ? 'Trạng thái' : 'Status'}</th>
                  <th>{vi ? 'Lịch chạy' : 'Schedule'}</th>
                  <th>{vi ? 'Nơi lưu / Phạm vi' : 'Destination / Scope'}</th>
                  <th>{vi ? 'Thời điểm ghi nhận' : 'Recorded time'}</th>
                  <th>{vi ? 'Hành động' : 'Action'}</th>
                </tr>
              </thead>
              <tbody>
                {filteredJobs.map((job) => (
                  <tr key={`${job.id}:${job.schedule}`}>
                    <td>
                      <strong>{jobLabels[job.id]?.[vi ? 'vi' : 'en'] ?? job.name}</strong>
                      <small className="backup-script-name" title={job.script}>{job.script.split('/').pop()}</small>
                    </td>
                    <td><button className={`cron-schedule-state ${job.enabled === true ? 'is-enabled' : job.enabled === false ? 'is-paused' : ''}`} type="button" disabled={mutating || !!cron.error || job.status === 'running' || typeof job.enabled !== 'boolean'} onClick={() => { setStateJob({ ...job }); setStateError(null) }} aria-label={`${job.enabled ? (vi ? 'Tạm dừng cron' : 'Pause cron') : (vi ? 'Bật cron' : 'Enable cron')}: ${jobLabels[job.id]?.[lang] ?? job.name}`} title={vi ? 'Trạng thái lịch tự động; bấm để bật/tạm dừng, không phải kết quả backup' : 'Automatic schedule state; click to enable/pause, not the backup result'}>{job.enabled === true ? <>{vi ? 'Đã bật' : 'Enabled'} <Play size={12} fill="currentColor" /></> : job.enabled === false ? <>{vi ? 'Tạm dừng' : 'Stopped'} <Pause size={12} fill="currentColor" /></> : (vi ? 'Chưa xác minh' : 'Unverified')}</button></td>
                    <td>
                      <span>{formatCronSchedule(job.schedule, lang)}</span>
                    </td>
                    <td className="backup-destination"><span title={destination(job)}>{destination(job)}</span>{job.id === 'integrity-check' && <small className="cron-time-source">{vi ? 'Chỉ đếm thư mục' : 'Folder count only'}</small>}</td>
                    <td><span>{job.log_updated_at || job.last_run || (vi ? 'Chưa có dữ liệu' : 'No data')}</span><small className="cron-time-source">{job.log_updated_at ? (vi ? 'Cập nhật log' : 'Log updated') : ''}{job.tracked_at ? `${vi ? ' · Bắt đầu: ' : ' · Started: '}${job.tracked_at}` : ''}</small></td>
                    <td>
                      <div className="cron-actions">
                        <button className="cron-run" type="button" onClick={() => handleRun(job)} disabled={mutating || !job.id || job.status === 'running'} aria-label={`${vi ? 'Chạy ngay' : 'Run now'}: ${jobLabels[job.id]?.[vi ? 'vi' : 'en'] ?? job.name}`}>
                          <Play size={14} fill="currentColor" />
                          {runningJob === job.id
                            ? (job.id === 'cleanup-panel' ? (vi ? 'Đang dọn...' : 'Cleaning...') : (vi ? 'Đang gửi...' : 'Starting...'))
                            : (vi ? 'Chạy ngay' : 'Run now')}
                        </button>
                        <button className="cron-view-log" type="button" onClick={() => openScheduleEditor(job)} disabled={mutating || job.status === 'running'} aria-label={`${vi ? 'Sửa tác vụ' : 'Edit task'}: ${jobLabels[job.id]?.[vi ? 'vi' : 'en'] ?? job.name}`}>
                          <Pencil size={14} /> {vi ? 'Sửa' : 'Edit'}
                        </button>
                        <button className="cron-view-log" type="button" onClick={() => { setLogJob(job); void loadLog(job) }} aria-label={`${vi ? 'Xem log' : 'View log'}: ${jobLabels[job.id]?.[vi ? 'vi' : 'en'] ?? job.name}`}><Eye size={15} /> Log</button>
                        <button className="cron-view-log cron-delete" type="button" onClick={() => { setDeleteJob({ ...job }); setDeleteError(null) }} disabled={mutating || !!cron.error || job.status === 'running' || typeof job.enabled !== 'boolean'} aria-label={`${vi ? 'Xóa cron' : 'Delete cron'}: ${jobLabels[job.id]?.[vi ? 'vi' : 'en'] ?? job.name}`}><Trash2 size={14} />{vi ? 'Xóa' : 'Delete'}</button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>

      {stateJob && <div className="cron-log-overlay" role="presentation" onMouseDown={event => { if (event.target === event.currentTarget) closeState() }}>
        <div className="cron-log-dialog cron-delete-dialog" ref={stateDialog} role="dialog" aria-modal="true" aria-labelledby="cron-state-title" aria-describedby="cron-state-description">
          <div className="cron-log-heading"><h2 id="cron-state-title">{stateJob.enabled ? (vi ? 'Tạm dừng lịch cron?' : 'Pause cron schedule?') : (vi ? 'Bật lịch cron?' : 'Enable cron schedule?')}</h2><button className="backup-system-secondary" type="button" disabled={updatingState} onClick={closeState} aria-label={vi ? 'Đóng xác nhận trạng thái' : 'Close state confirmation'}><X size={18} /></button></div>
          <div className="cron-delete-body">
            <strong>{jobLabels[stateJob.id]?.[lang] ?? stateJob.name}</strong>
            <p>{formatCronSchedule(stateJob.schedule, lang)} · {vi ? 'giờ máy chủ' : 'server time'}</p>
            <p id="cron-state-description">{stateJob.enabled
              ? (vi ? 'Ngừng các lần chạy tự động tiếp theo; vẫn có thể dùng “Chạy ngay”. Không xóa script, log hay backup.' : 'Stop future automatic runs; “Run now” remains available. Scripts, logs and backups are kept.')
              : (vi ? 'Khôi phục chạy tự động theo lịch đã lưu. Không chạy script ngay và không chạy bù các lần đã bỏ lỡ.' : 'Resume automatic runs on the saved schedule. This does not run the script now or catch up missed runs.')}</p>
            {stateStale && <p className="cron-log-error" role="alert">{vi ? 'Lịch hoặc trạng thái đã thay đổi. Hãy đóng và làm mới danh sách.' : 'The schedule or state changed. Close and refresh the list.'}</p>}
            {stateRunning && <p className="cron-log-error" role="alert">{vi ? 'Tác vụ đang chạy. Hãy chờ kết thúc rồi đổi trạng thái.' : 'The task is running. Wait for it to finish before changing the state.'}</p>}
            {stateError && <p className="cron-log-error" role="alert">{stateError}</p>}
            {cron.error && <p className="cron-log-error" role="alert">{vi ? 'Không thể xác minh lịch hiện tại: ' : 'Could not verify the current schedule: '}{cron.error}</p>}
            <div className="backup-system-actions"><button className="backup-system-secondary" type="button" onClick={closeState} disabled={updatingState}>{vi ? 'Hủy' : 'Cancel'}</button><button className="backup-system-primary" type="button" onClick={() => { void handleState() }} disabled={updatingState || stateStale || stateRunning || !!cron.error}>{updatingState ? (vi ? 'Đang lưu...' : 'Saving...') : stateJob.enabled ? (vi ? 'Tạm dừng cron' : 'Pause cron') : (vi ? 'Bật cron' : 'Enable cron')}</button></div>
          </div>
        </div>
      </div>}

      {deleteJob && <div className="cron-log-overlay" role="presentation" onMouseDown={event => { if (event.target === event.currentTarget) closeDelete() }}>
        <div className="cron-log-dialog cron-delete-dialog" ref={deleteDialog} role="dialog" aria-modal="true" aria-labelledby="cron-delete-title" aria-describedby="cron-delete-description">
          <div className="cron-log-heading"><h2 id="cron-delete-title">{vi ? 'Xóa lịch cron?' : 'Delete cron schedule?'}</h2><button className="backup-system-secondary" type="button" disabled={deletingJob} onClick={closeDelete} aria-label={vi ? 'Đóng xác nhận xóa' : 'Close delete confirmation'}><X size={18} /></button></div>
          <div className="cron-delete-body">
            <strong>{jobLabels[deleteJob.id]?.[lang] ?? deleteJob.name}</strong>
            <p>{formatCronSchedule(deleteJob.schedule, lang)} · {vi ? 'giờ máy chủ' : 'server time'}</p>
            <p id="cron-delete-description">{vi ? 'Chỉ xóa lịch chạy tự động của tác vụ này. Không xóa file script, log hoặc bản backup. Hệ thống lưu bản dự phòng crontab trước khi thay đổi.' : 'Only this automatic schedule is removed. Script files, logs and backups are kept. A recovery copy of the crontab is saved before changing it.'}</p>
            {deleteStale && <p className="cron-log-error" role="alert">{vi ? 'Lịch đã thay đổi hoặc tác vụ không còn. Hãy đóng và làm mới danh sách.' : 'The schedule changed or the task is gone. Close and refresh the list.'}</p>}
            {deleteRunning && <p className="cron-log-error" role="alert">{vi ? 'Tác vụ đang chạy. Hãy chờ kết thúc rồi xóa lịch.' : 'The task is running. Wait for it to finish before deleting the schedule.'}</p>}
            {deleteError && <p className="cron-log-error" role="alert">{deleteError}</p>}
            {cron.error && <p className="cron-log-error" role="alert">{vi ? 'Không thể xác minh lịch hiện tại: ' : 'Could not verify the current schedule: '}{cron.error}</p>}
            <div className="backup-system-actions"><button className="backup-system-secondary" type="button" onClick={closeDelete} disabled={deletingJob}>{vi ? 'Hủy' : 'Cancel'}</button><button className="cron-delete-confirm" type="button" onClick={() => { void handleDelete() }} disabled={deletingJob || deleteStale || deleteRunning || !!cron.error}>{deletingJob ? (vi ? 'Đang xóa...' : 'Deleting...') : (vi ? 'Xóa lịch cron' : 'Delete schedule')}</button></div>
          </div>
        </div>
      </div>}

      {logJob && (
        <div className="cron-log-overlay" role="presentation" onMouseDown={(event) => { if (event.target === event.currentTarget) closeLog() }}>
          <div className="cron-log-dialog" ref={logDialog} role="dialog" aria-modal="true" aria-labelledby="cron-log-title">
            <div className="cron-log-heading">
              <div>
                <h2 id="cron-log-title">{vi ? 'Log cronjob' : 'Cron job log'}: {jobLabels[logJob.id]?.[vi ? 'vi' : 'en'] ?? logJob.name}</h2>
                <p>{vi ? '200 dòng cuối, tối đa 64 KB' : 'Last 200 lines, up to 64 KB'}</p>
              </div>
              <div className="cron-log-controls">
                <button type="button" onClick={() => void loadLog(logJob)} disabled={logLoading} aria-label={vi ? 'Làm mới log' : 'Refresh log'} title={vi ? 'Làm mới log' : 'Refresh log'}><RefreshCw size={17} /></button>
                <button type="button" onClick={closeLog} aria-label={vi ? 'Đóng' : 'Close'} title={vi ? 'Đóng' : 'Close'}><X size={18} /></button>
              </div>
            </div>
            <div className="cron-log-body" role="status">
              {logLoading ? (vi ? 'Đang đọc log...' : 'Loading log...')
                : logError ? <span className="cron-log-error">{logError}</span>
                  : !logData?.exists ? (vi ? 'Chưa có file log cho tác vụ này.' : 'No log file for this job yet.')
                    : logData.content ? <pre>{logData.content}</pre>
                      : (vi ? 'File log đang trống.' : 'The log file is empty.')}
            </div>
          </div>
        </div>
      )}

    </div>
  )
}
