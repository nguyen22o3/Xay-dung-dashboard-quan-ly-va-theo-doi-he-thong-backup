import { useState } from 'react'
import { CalendarClock, Play, RefreshCw } from 'lucide-react'
import { apiErrorMessage, runCronJob, useCronJobs } from '../api'
import type { Lang } from '../language'
import type { CronJob } from '../types'
import { formatCronSchedule } from '../utils'

export default function CronJobs({ lang }: { lang: Lang }) {
  const vi = lang === 'vi'
  const cron = useCronJobs(60000)
  const jobs = (cron.data ?? [])
    .filter((job) => job.name !== 'Gia hạn SSL')
    .map((job) => job.script.split('/').pop() === '900193fc7aeefaddcd40724da9e1a35f'
      ? { ...job, name: vi ? 'Dọn dẹp thư mục panel' : 'Clean up panel backup folder' }
      : job)
  const [runningJob, setRunningJob] = useState<string | null>(null)
  const [feedback, setFeedback] = useState<{ message: string; error: boolean } | null>(null)

  const handleRun = async (job: CronJob) => {
    const jobId = job.script.split('/').pop() || ''
    if (!/^[A-Za-z0-9]{1,64}$/.test(jobId) || runningJob) return
    const confirmed = window.confirm(vi
      ? `Chạy ngay cronjob "${job.name}"? Tác vụ này có thể thay đổi hoặc xóa dữ liệu trên máy chủ.`
      : `Run "${job.name}" now? This job may change or delete server data.`)
    if (!confirmed) return

    setRunningJob(jobId)
    setFeedback(null)
    try {
      await runCronJob(jobId)
      setFeedback({
        message: vi
          ? `Đã gửi yêu cầu chạy "${job.name}". Hãy kiểm tra log sau khi tác vụ kết thúc.`
          : `Started "${job.name}". Check its log after the job finishes.`,
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
          <div className="cron-message">{vi ? 'Không tìm thấy cronjob aaPanel nào.' : 'No aaPanel cron jobs found.'}</div>
        ) : (
          <div className="cron-table-wrap">
            <table className="cron-table">
              <thead>
                <tr>
                  <th>{vi ? 'Tác vụ' : 'Job'}</th>
                  <th>{vi ? 'Lịch chạy' : 'Schedule'}</th>
                  <th title={vi ? 'Lấy từ thời điểm cập nhật log aaPanel' : 'Based on the aaPanel log modification time'}>{vi ? 'Thời gian thực hiện lần cuối' : 'Last execution time'}</th>
                  <th>{vi ? 'Hành động' : 'Action'}</th>
                </tr>
              </thead>
              <tbody>
                {jobs.map((job) => (
                  <tr key={`${job.script}:${job.schedule}`}>
                    <td>
                      <strong>{job.name}</strong>
                    </td>
                    <td>
                      <span>{vi ? formatCronSchedule(job.schedule) : job.schedule}</span>
                      {vi && <small>{job.schedule}</small>}
                    </td>
                    <td>{job.last_run || (vi ? 'Chưa có dữ liệu' : 'No data')}</td>
                    <td>
                      <button className="cron-run" type="button" onClick={() => handleRun(job)} disabled={runningJob !== null || !/^[A-Za-z0-9]{1,64}$/.test(job.script.split('/').pop() || '')}>
                        <Play size={14} fill="currentColor" />
                        {runningJob === job.script.split('/').pop() ? (vi ? 'Đang gửi...' : 'Starting...') : (vi ? 'Chạy ngay' : 'Run now')}
                      </button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </div>
  )
}
