import { deleteCronJob, fetchCronJobs } from '../../api/dashboard'
import type { CronJob } from '../../shared/types'
import { cronJobSelector } from './backupTasks'

export type CronDeletionResult = { backupPath?: string; jobs?: CronJob[] }

// A request can remove the schedule before its response is lost or rejected.
// Reconcile against a fresh server read instead of leaving a stale error dialog.
export async function deleteCronSchedule(job: CronJob): Promise<CronDeletionResult> {
  if (typeof job.enabled !== 'boolean') throw new Error('Không xác minh được trạng thái lịch cron')
  const selector = cronJobSelector(job)
  try {
    const result = await deleteCronJob(selector, job.schedule, job.enabled)
    if (result?.status !== 'deleted') throw new Error('Không xác nhận được kết quả xóa; hãy làm mới danh sách trước khi thử lại')
    return { backupPath: result.backupPath }
  } catch (error: unknown) {
    let jobs: CronJob[]
    try {
      jobs = await fetchCronJobs()
    } catch {
      throw error
    }
    // Fail closed on an invalid list. A remaining instance, even with a new
    // schedule/state, may be an edit conflict rather than a completed deletion.
    if (!Array.isArray(jobs) || jobs.some(current => !current || typeof current.id !== 'string' || typeof current.schedule !== 'string' || cronJobSelector(current) === selector)) throw error
    return { jobs }
  }
}
