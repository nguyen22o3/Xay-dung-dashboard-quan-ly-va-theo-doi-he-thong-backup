import { backupTaskScripts, buildCronScheduleChange, makeCronScheduleDraft, proposedCronSchedule } from './backupTasks.ts'
import type { CronJob } from './types'
import type { BackupSystemState, ConfigurationPreview, CronScheduleChange } from './backupSystemApi'

export const cronImportMaxBytes = 64 * 1024
export type CronFileJob = { id: string; schedule: string }
export type CronImportRow = CronFileJob & { previous: string | null; enabled: boolean; change: CronScheduleChange | null }
type CronFile = { format: 'backup-monitor-cron'; version: 1; exportedAt: string; jobs: CronFileJob[] }
type TransferApi = {
  jobs: () => Promise<CronJob[]>
  settings: () => Promise<BackupSystemState>
  preview: (state: BackupSystemState, change: CronScheduleChange) => Promise<ConfigurationPreview>
  apply: (state: BackupSystemState, token: string, change: CronScheduleChange) => Promise<unknown>
}

const known = (id: string) => Object.hasOwn(backupTaskScripts, id)
const record = (value: unknown): value is Record<string, unknown> => !!value && typeof value === 'object' && !Array.isArray(value)
const onlyKeys = (value: Record<string, unknown>, keys: string[]) => Object.keys(value).every(key => keys.includes(key))

function canonicalSchedule(schedule: string): string {
  if (schedule.length > 100 || !/^[0-9*/ \t]+$/.test(schedule) || schedule.trim().split(/\s+/).length !== 5) throw new Error('Lịch chạy trong tệp không hợp lệ.')
  const draft = makeCronScheduleDraft({ schedule, id: '', enabled: false } as CronJob)
  const normalized = proposedCronSchedule(draft)
  if (draft.cycle === 'current' || !normalized) throw new Error('Tệp có chu kỳ chưa được dashboard hỗ trợ.')
  return normalized
}

export function parseCronFile(text: string): CronFileJob[] {
  if (new TextEncoder().encode(text).length > cronImportMaxBytes) throw new Error('Tệp JSON không được lớn hơn 64 KB.')
  let value: unknown
  try { value = JSON.parse(text) } catch { throw new Error('Tệp không phải JSON hợp lệ.') }
  if (!record(value) || value.format !== 'backup-monitor-cron' || value.version !== 1 || !onlyKeys(value, ['format', 'version', 'exportedAt', 'jobs']) || typeof value.exportedAt !== 'string' || value.exportedAt.length > 40 || !Number.isFinite(Date.parse(value.exportedAt)) || !Array.isArray(value.jobs) || !value.jobs.length || value.jobs.length > Object.keys(backupTaskScripts).length) {
    throw new Error('Hãy dùng tệp JSON xuất từ dashboard này; không hỗ trợ tệp aaPanel hoặc định dạng khác.')
  }
  const seen = new Set<string>()
  return value.jobs.map(job => {
    if (!record(job) || !onlyKeys(job, ['id', 'schedule']) || typeof job.id !== 'string' || !known(job.id) || typeof job.schedule !== 'string' || seen.has(job.id)) throw new Error('Tệp có tác vụ lạ, trùng lặp hoặc trường dữ liệu không được phép.')
    seen.add(job.id)
    return { id: job.id, schedule: canonicalSchedule(job.schedule) }
  })
}

export function exportCronFile(jobs: CronJob[], now = new Date()): string {
  const managed = jobs.filter(job => known(job.id) && !job.isNew)
  if (!managed.length) throw new Error('Chưa có lịch chạy để xuất.')
  const value: CronFile = { format: 'backup-monitor-cron', version: 1, exportedAt: now.toISOString(), jobs: managed.map(job => ({ id: job.id, schedule: job.schedule })) }
  // Export only restorable schedules, never shell commands, paths or credentials.
  value.jobs = parseCronFile(JSON.stringify(value))
  return JSON.stringify(value, null, 2)
}

export function planCronImport(entries: CronFileJob[], jobs: CronJob[]): CronImportRow[] {
  return entries.map(entry => {
    if (!known(entry.id)) throw new Error('Không thể nhập tác vụ không được dashboard quản lý.')
    const schedule = canonicalSchedule(entry.schedule)
    const matches = jobs.filter(job => job.id === entry.id && !job.isNew)
    if (matches.length > 1) throw new Error('Máy chủ có lịch trùng lặp. Hãy kiểm tra trước khi nhập.')
    const current = matches[0]
    if (current && typeof current.enabled !== 'boolean') throw new Error('Chưa xác minh được trạng thái tác vụ. Hãy tải lại.')
    const base = current ?? ({ id: entry.id, schedule: '', enabled: false, isNew: true } as CronJob)
    const desired = makeCronScheduleDraft({ ...base, schedule, isNew: false })
    const draft = { ...makeCronScheduleDraft(base), cycle: desired.cycle, hour: desired.hour, minute: desired.minute, every: desired.every, weekday: desired.weekday, day: desired.day }
    const change = buildCronScheduleChange(draft)
    if (change && current?.status === 'running') throw new Error('Một tác vụ đang chạy. Hãy chờ kết thúc rồi nhập lại.')
    return { id: entry.id, schedule, previous: current?.schedule ?? null, enabled: current?.enabled ?? true, change }
  })
}

function assertUnchanged(row: CronImportRow, jobs: CronJob[]): void {
  const matches = jobs.filter(job => job.id === row.id && !job.isNew)
  const current = matches[0]
  if (matches.length > 1 || (row.previous === null ? !!current : !current || current.schedule !== row.previous || current.enabled !== row.enabled) || (row.change && current?.status === 'running')) throw new Error('Lịch hoặc trạng thái trên máy chủ đã thay đổi. Hãy đóng và nhập lại tệp.')
}

const stable = (value: unknown): string => {
  if (Array.isArray(value)) return `[${value.map(stable).join(',')}]`
  if (record(value)) return `{${Object.keys(value).sort().map(key => `${JSON.stringify(key)}:${stable(value[key])}`).join(',')}}`
  return JSON.stringify(value) ?? 'null'
}

function assertPreview(row: CronImportRow, state: BackupSystemState, checked: ConfigurationPreview): void {
  if (!checked.token || checked.version !== state.version || checked.schedule?.id !== row.id || checked.schedule.previous !== (row.previous ?? '') || checked.schedule.proposed !== row.schedule || checked.schedule.enabled !== row.enabled || !checked.changed) throw new Error('Máy chủ chưa xác nhận đúng lịch cần nhập. Không áp dụng thay đổi.')
  if (checked.filesToMove || checked.daysToMove || checked.logsToMove || checked.scriptsToUpdate || checked.driveChanged || checked.sources?.length || stable(checked.config) !== stable(state.config)) throw new Error('Import chỉ được đổi lịch chạy, không được đổi script, nguồn dữ liệu hoặc nơi lưu backup.')
}

export class CronImportError extends Error {
  completedIds: string[]
  failedId: string
  writeAttempted: boolean
  constructor(cause: unknown, completedIds: string[], failedId: string, writeAttempted: boolean) {
    super(cause instanceof Error ? cause.message : 'Không nhập được lịch Cron.')
    this.name = 'CronImportError'; this.completedIds = [...completedIds]; this.failedId = failedId; this.writeAttempted = writeAttempted
  }
}

// Each task uses the existing server transaction and a fresh preview token.
// Preflight every task before writing; on failure, stop and report partial progress.
export async function applyCronImport(plan: CronImportRow[], api: TransferApi, progress?: (completed: number, total: number) => void): Promise<string[]> {
  const completed: string[] = []
  let failedId = '', writeAttempted = false
  try {
    const initialJobs = await api.jobs()
    plan.forEach(row => assertUnchanged(row, initialJobs))
    const initial = await api.settings()
    if (!initial.ready) throw new Error('Máy chủ chưa sẵn sàng nhận lịch Cron.')
    const baseline = stable(initial.config)
    for (const row of plan) {
      failedId = row.id
      if (row.change) assertPreview(row, initial, await api.preview(initial, row.change))
    }
    let visited = 0
    for (const row of plan) {
      failedId = row.id; writeAttempted = false
      assertUnchanged(row, await api.jobs())
      if (row.change) {
        const fresh = await api.settings()
        if (!fresh.ready || stable(fresh.config) !== baseline) throw new Error('Cấu hình nơi lưu đã thay đổi trong lúc nhập. Hãy kiểm tra rồi nhập lại.')
        const checked = await api.preview(fresh, row.change)
        assertPreview(row, fresh, checked)
        writeAttempted = true
        await api.apply(fresh, checked.token, row.change)
        const saved = (await api.jobs()).filter(job => job.id === row.id)
        if (saved.length !== 1 || saved[0].schedule !== row.schedule || saved[0].enabled !== row.enabled) throw new Error('Chưa xác minh được kết quả lưu trên máy chủ. Hãy kiểm tra danh sách trước khi thử lại.')
        completed.push(row.id)
      }
      progress?.(++visited, plan.length)
    }
    return completed
  } catch (error) {
    throw new CronImportError(error, completed, failedId, writeAttempted)
  }
}
