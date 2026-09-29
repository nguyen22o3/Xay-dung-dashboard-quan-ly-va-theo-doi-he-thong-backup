import { useEffect, useRef, useState } from 'react'
import { CalendarClock, Eye, Pencil, Play, RefreshCw, X } from 'lucide-react'
import { apiErrorMessage, fetchCronJobLog, runCronJob, updateCronJobSchedule, useCronJobs } from '../api'
import type { Lang } from '../language'
import type { CronJob, CronJobLog } from '../types'
import { formatCronSchedule } from '../utils'

const jobLabels: Record<string, { vi: string; en: string }> = {
  'cleanup-panel': { vi: 'Dọn dẹp thư mục panel', en: 'Clean up panel backups' },
  'backup-site': { vi: 'Sao lưu website', en: 'Backup websites' },
  'backup-database': { vi: 'Sao lưu database', en: 'Backup databases' },
  'drive-sync': { vi: 'Đồng bộ Google Drive', en: 'Sync Google Drive' },
  'integrity-check': { vi: 'Kiểm tra toàn vẹn backup', en: 'Check backup integrity' },
}

export default function CronJobs({ lang }: { lang: Lang }) {
  const vi = lang === 'vi'
  const cron = useCronJobs(60000)
  const jobs = cron.data ?? []
  const [runningJob, setRunningJob] = useState<string | null>(null)
  const [feedback, setFeedback] = useState<{ message: string; error: boolean } | null>(null)
  const [logJob, setLogJob] = useState<CronJob | null>(null)
  const [logData, setLogData] = useState<CronJobLog | null>(null)
  const [logLoading, setLogLoading] = useState(false)
  const [logError, setLogError] = useState<string | null>(null)
  const logRequest = useRef(0)
  const [editingJob, setEditingJob] = useState<CronJob | null>(null)
  const [editTime, setEditTime] = useState('')
  const [scheduleError, setScheduleError] = useState<string | null>(null)
  const [savingSchedule, setSavingSchedule] = useState(false)

  const openScheduleEditor = (job: CronJob) => {
    const [minute, hour] = job.schedule.split(' ')
    setEditTime(/^\d{1,2}$/.test(hour) && /^\d{1,2}$/.test(minute)
      ? `${hour.padStart(2, '0')}:${minute.padStart(2, '0')}` : '')
    setScheduleError(null)
    setEditingJob(job)
  }

  const saveSchedule = async () => {
    if (!editingJob || savingSchedule) return
    if (!/^(?:[01]\d|2[0-3]):[0-5]\d$/.test(editTime)) {
      setScheduleError(vi ? 'Hãy chọn giờ hợp lệ (00:00–23:59).' : 'Choose a valid time (00:00–23:59).')
      return
    }
    setSavingSchedule(true)
    setScheduleError(null)
    try {
      await updateCronJobSchedule(editingJob.id, editTime, editingJob.schedule)
      setFeedback({ message: vi
        ? `Đã đổi giờ chạy "${jobLabels[editingJob.id]?.vi ?? editingJob.name}" thành ${editTime} (giờ máy chủ).`
        : `Updated "${jobLabels[editingJob.id]?.en ?? editingJob.name}" to ${editTime} (server time).`, error: false })
      setEditingJob(null)
      cron.reload()
    } catch (error: unknown) {
      setScheduleError(apiErrorMessage(error, vi ? 'Không thể lưu giờ chạy.' : 'Could not save the schedule.'))
    } finally {
      setSavingSchedule(false)
    }
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

  useEffect(() => {
    if (!editingJob || savingSchedule) return
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setEditingJob(null)
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [editingJob, savingSchedule])

  const handleRun = async (job: CronJob) => {
    const jobId = job.id
    if (!jobId || runningJob) return
    const name = jobLabels[jobId]?.[vi ? 'vi' : 'en'] ?? job.name
    const confirmed = window.confirm(vi
      ? `Chạy ngay cronjob "${name}"? Tác vụ này có thể thay đổi hoặc xóa dữ liệu trên máy chủ.`
      : `Run "${name}" now? This job may change or delete server data.`)
    if (!confirmed) return

    setRunningJob(jobId)
    setFeedback(null)
    try {
      await runCronJob(jobId)
      setFeedback({
        message: vi
          ? `Đã gửi yêu cầu chạy "${name}". Hãy kiểm tra log sau khi tác vụ kết thúc.`
          : `Started "${name}". Check its log after the job finishes.`,
        error: false,
      })
      cron.reload()
    } catch (error: unknown) {
      setFeedback({ message: apiErrorMessage(error, vi ? 'Không thể chạy cronjob.' : 'Could not run the cron job.'), error: true })
    } finally {
      setRunningJob(null)
    }
  }

  return (
    <div className="cron-page animate-fade-in">
      <div className="cron-page-heading">
        <div>
          <h1>Cronjob</h1>
        </div>
        <button className="cron-refresh" type="button" onClick={cron.reload} aria-label={vi ? 'Làm mới cronjob' : 'Refresh cron jobs'}>
          <RefreshCw size={17} /> {vi ? 'Làm mới' : 'Refresh'}
        </button>
      </div>

      <section className="cron-panel" aria-label={vi ? 'Danh sách cronjob' : 'Cron job list'}>
        <div className="cron-panel-heading">
          <div className="cron-panel-icon"><CalendarClock size={20} /></div>
          <div>
            <h2>{vi ? 'Tác vụ định kỳ' : 'Scheduled jobs'}</h2>
            <p>{vi ? `${jobs.length} tác vụ trong danh sách` : `${jobs.length} jobs in the list`}</p>
          </div>
        </div>

        {cron.error && <div className="cron-message cron-error" role="alert">{vi ? 'Không thể cập nhật danh sách: ' : 'Could not update the list: '}{cron.error}</div>}
        {feedback && <div className={`cron-feedback ${feedback.error ? 'cron-feedback--error' : ''}`} role="status">{feedback.message}</div>}
        {cron.loading && !cron.data ? (
          <div className="cron-message">{vi ? 'Đang tải cronjob...' : 'Loading cron jobs...'}</div>
        ) : jobs.length === 0 ? (
          <div className="cron-message">{vi ? 'Không tìm thấy lịch chạy script nào.' : 'No scheduled scripts found.'}</div>
        ) : (
          <div className="cron-table-wrap">
            <table className="cron-table">
              <thead>
                <tr>
                  <th>{vi ? 'Tác vụ' : 'Job'}</th>
                  <th>{vi ? 'Lịch chạy' : 'Schedule'}</th>
                  <th title={vi ? 'Dựa trên thời điểm cập nhật log; tác vụ không ghi log sẽ không có dữ liệu' : 'Based on log modification time; jobs without a log have no timestamp'}>{vi ? 'Thời gian thực hiện lần cuối' : 'Last execution time'}</th>
                  <th>{vi ? 'Hành động' : 'Action'}</th>
                </tr>
              </thead>
              <tbody>
                {jobs.map((job) => (
                  <tr key={`${job.id}:${job.schedule}`}>
                    <td>
                      <strong>{jobLabels[job.id]?.[vi ? 'vi' : 'en'] ?? job.name}</strong>
                    </td>
                    <td>
                      <span>{vi ? formatCronSchedule(job.schedule) : job.schedule}</span>
                      {vi && <small>{job.schedule}</small>}
                    </td>
                    <td>{job.last_run || (vi ? 'Chưa có dữ liệu' : 'No data')}</td>
                    <td>
                      <div className="cron-actions">
                        <button className="cron-run" type="button" onClick={() => handleRun(job)} disabled={runningJob !== null || !job.id}>
                          <Play size={14} fill="currentColor" />
                          {runningJob === job.id ? (vi ? 'Đang gửi...' : 'Starting...') : (vi ? 'Chạy ngay' : 'Run now')}
                        </button>
                        <button className="cron-view-log" type="button" onClick={() => { setLogJob(job); void loadLog(job) }}>
                          <Eye size={15} /> {vi ? 'Xem log' : 'View log'}
                        </button>
                        <button className="cron-view-log" type="button" onClick={() => openScheduleEditor(job)}>
                          <Pencil size={14} /> {vi ? 'Đổi giờ' : 'Edit time'}
                        </button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>

      {logJob && (
        <div className="cron-log-overlay" role="presentation" onMouseDown={(event) => { if (event.target === event.currentTarget) closeLog() }}>
          <div className="cron-log-dialog" role="dialog" aria-modal="true" aria-labelledby="cron-log-title">
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

      {editingJob && (
        <div className="cron-log-overlay" role="presentation" onMouseDown={(event) => { if (event.target === event.currentTarget && !savingSchedule) setEditingJob(null) }}>
          <form className="cron-log-dialog cron-schedule-dialog" role="dialog" aria-modal="true" aria-labelledby="cron-schedule-title" onSubmit={(event) => { event.preventDefault(); void saveSchedule() }}>
            <div className="cron-log-heading">
              <div>
                <h2 id="cron-schedule-title">{vi ? 'Đổi giờ chạy' : 'Edit run time'}: {jobLabels[editingJob.id]?.[vi ? 'vi' : 'en'] ?? editingJob.name}</h2>
                <p>{vi ? 'Giữ nguyên ngày chạy; chỉ đổi giờ và phút theo giờ máy chủ.' : 'Keep the same recurrence; change only hour and minute in server time.'}</p>
              </div>
              <div className="cron-log-controls">
                <button type="button" onClick={() => setEditingJob(null)} disabled={savingSchedule} aria-label={vi ? 'Đóng' : 'Close'}><X size={18} /></button>
              </div>
            </div>
            <div className="cron-schedule-body">
              <label htmlFor="cron-schedule-time">{vi ? 'Giờ chạy (theo giờ máy chủ)' : 'Run time (server time)'}</label>
              <input id="cron-schedule-time" type="time" required value={editTime} onChange={(event) => setEditTime(event.target.value)} disabled={savingSchedule} />
              <small>{vi ? `Lịch hiện tại: ${editingJob.schedule}` : `Current schedule: ${editingJob.schedule}`}</small>
              <small>{vi ? 'Các cron khác không tự đổi giờ; hãy tránh để tác vụ phụ thuộc chạy trước backup.' : 'Other jobs keep their schedules; avoid running dependent jobs before backups finish.'}</small>
              {scheduleError && <p className="cron-log-error" role="alert">{scheduleError}</p>}
            </div>
            <div className="cron-schedule-footer">
              <button type="button" className="cron-view-log" onClick={() => setEditingJob(null)} disabled={savingSchedule}>{vi ? 'Hủy' : 'Cancel'}</button>
              <button type="submit" className="cron-run" disabled={savingSchedule || !editTime}>{savingSchedule ? (vi ? 'Đang lưu...' : 'Saving...') : (vi ? 'Lưu giờ chạy' : 'Save time')}</button>
            </div>
          </form>
        </div>
      )}
    </div>
  )
}
