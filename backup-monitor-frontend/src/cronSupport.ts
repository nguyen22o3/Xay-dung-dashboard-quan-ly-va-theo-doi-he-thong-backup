import type { CronJob } from "./shared"
import type { CronScheduleChange, ScheduleCycle, BackupSource, BackupSystemConfig, BackupSystemState, ConfigurationPreview } from "./api"

export function cronJobSelector(job: Pick<CronJob, 'id' | 'instanceKey'>): string { return job.instanceKey ? `${job.id}~${job.instanceKey}` : job.id }

export const sourceConfigKeys: Record<string, 'siteSource' | 'databaseSource' | 'panelSource'> = { 'backup-site': 'siteSource', 'backup-database': 'databaseSource', 'backup-panel': 'panelSource' }
export function backupSourceForTask(config: BackupSystemConfig, state: BackupSystemState, id: string): BackupSource | null {
  const key = sourceConfigKeys[id]
  return key ? (config[key] ?? state.sources?.[id] ?? null) : null
}

export const backupTaskLabels: Record<string, { vi: string; en: string }> = {
  'cleanup-panel': { vi: 'Dọn dẹp bản sao lưu aaPanel', en: 'Clean up aaPanel backups' },
  'backup-site': { vi: 'Sao lưu website', en: 'Back up websites' },
  'backup-database': { vi: 'Sao lưu cơ sở dữ liệu', en: 'Back up databases' },
  'backup-panel': { vi: 'Sao lưu cấu hình aaPanel', en: 'Back up aaPanel configuration' },
  'drive-sync': { vi: 'Đồng bộ Google Drive', en: 'Sync Google Drive' },
  'integrity-check': { vi: 'Kiểm tra số thư mục sao lưu', en: 'Check backup folder count' },
}

export const backupTaskScripts: Record<string, string> = {
  'backup-site': 'backup-site.sh', 'backup-database': 'backup-database.sh', 'backup-panel': 'backup-panel.sh',
  'cleanup-panel': 'cleanup-panel-backups.sh', 'drive-sync': 'auto_backup.sh', 'integrity-check': 'check_integrity.sh',
}

// The folder picker edits the chosen parent, not selected.script (which can
// still contain a previous/global path until the next cron poll finishes).
export function cronExecutionScript(job: CronJob, scriptsDir: string, flatFiles = false): string {
  const parent = scriptsDir.replace(/\/+$/, '')
  if (!parent) return job.script
  const file = backupTaskScripts[job.id] ?? job.script.split('/').at(-1) ?? ''
  if (!job.instanceKey) return `${parent}/${file}`
  if (!flatFiles) return `${parent}/cron-${job.instanceKey}/${file}`
  const extension = file.lastIndexOf('.')
  const name = extension > 0 ? `${file.slice(0, extension)}-${job.instanceKey}${file.slice(extension)}` : `${file}-${job.instanceKey}`
  return `${parent}/${name}`
}

function cronClock(schedule: string): { hour: string; minute: string } {
  const [minute, hour] = schedule.trim().split(/\s+/)
  return {
    hour: /^\d{1,2}$/.test(hour ?? '') && Number(hour) < 24 ? hour.padStart(2, '0') : '',
    minute: /^\d{1,2}$/.test(minute ?? '') && Number(minute) < 60 ? minute.padStart(2, '0') : '',
  }
}

function withCronClock(schedule: string, hour: string, minute: string): string | null {
  if (!/^\d{1,2}$/.test(hour) || !/^\d{1,2}$/.test(minute) || Number(hour) > 23 || Number(minute) > 59) return null
  const fields = schedule.trim().split(/\s+/)
  if (fields.length !== 5) return null
  return `${Number(minute)} ${Number(hour)} ${fields.slice(2).join(' ')}`
}

export type CronScheduleDraft = { instanceKey?: string; id: string; expectedSchedule: string; expectedEnabled?: boolean; hour: string; minute: string; revision: number; cycle: ScheduleCycle['type'] | 'current'; every: string; weekday: string; day: string; create?: boolean }

export const cycleLabels = {
  current: { vi: 'Giữ chu kỳ hiện tại', en: 'Keep current recurrence' },
  daily: { vi: 'Hằng ngày', en: 'Daily' }, days: { vi: 'Mỗi N ngày trong tháng', en: 'Every N days within each month' },
  hourly: { vi: 'Hằng giờ', en: 'Hourly' }, hours: { vi: 'Mỗi N giờ', en: 'Every N hours' },
  minutes: { vi: 'Mỗi N phút', en: 'Every N minutes' }, weekly: { vi: 'Hằng tuần', en: 'Weekly' }, monthly: { vi: 'Hằng tháng', en: 'Monthly' },
}

function cronCycleDraft(schedule: string): Pick<CronScheduleDraft, 'cycle' | 'every' | 'weekday' | 'day'> {
  const [minute, hour, day, month, weekday] = schedule.trim().split(/\s+/)
  const defaults = { cycle: 'current' as CronScheduleDraft['cycle'], every: '2', weekday: '1', day: '1' }
  if (month !== '*') return defaults
  if (day === '*' && weekday === '*') {
    if (/^\*\/\d+$/.test(minute) && hour === '*') return { ...defaults, cycle: 'minutes', every: minute.slice(2) }
    if (/^\d+$/.test(minute) && /^\*\/\d+$/.test(hour)) return { ...defaults, cycle: 'hours', every: hour.slice(2) }
    if (/^\d+$/.test(minute) && hour === '*') return { ...defaults, cycle: 'hourly' }
    if (/^\d+$/.test(minute) && /^\d+$/.test(hour)) return { ...defaults, cycle: 'daily' }
  }
  if (/^\d+$/.test(minute) && /^\d+$/.test(hour)) {
    if (day === '*' && /^[0-7]$/.test(weekday)) return { ...defaults, cycle: 'weekly', weekday: String(Number(weekday) % 7) }
    if (weekday === '*' && /^\d+$/.test(day)) return { ...defaults, cycle: 'monthly', day }
    if (weekday === '*' && /^\*\/\d+$/.test(day)) return { ...defaults, cycle: 'days', every: day.slice(2) }
  }
  return defaults
}

function scheduleCycle(draft: CronScheduleDraft): ScheduleCycle | null {
  const integer = (value: string, min: number, max: number) => /^\d{1,2}$/.test(value) && Number(value) >= min && Number(value) <= max
  switch (draft.cycle) {
    case 'daily': case 'hourly': return { type: draft.cycle }
    case 'days': case 'hours': case 'minutes': {
      const bounds = { days: [2, 31], hours: [2, 23], minutes: [1, 59] }[draft.cycle]
      return integer(draft.every, bounds[0], bounds[1]) ? { type: draft.cycle, every: Number(draft.every) } : null
    }
    case 'weekly': return integer(draft.weekday, 0, 6) ? { type: 'weekly', weekday: Number(draft.weekday) } : null
    case 'monthly': return integer(draft.day, 1, 31) ? { type: 'monthly', day: Number(draft.day) } : null
    default: return null
  }
}

export function proposedCronSchedule(draft: CronScheduleDraft): string | null {
  const clock = withCronClock('0 0 * * *', ['hourly', 'hours', 'minutes'].includes(draft.cycle) ? '00' : draft.hour, draft.cycle === 'minutes' ? '00' : draft.minute)
  if (!clock) return null
  if (draft.cycle === 'current') return withCronClock(draft.expectedSchedule, draft.hour, draft.minute)
  const cycle = scheduleCycle(draft)
  if (!cycle) return null
  const [minute, hour] = clock.split(' ')
  switch (cycle.type) {
    case 'daily': return `${minute} ${hour} * * *`
    case 'hourly': return `${minute} * * * *`
    case 'hours': return `${minute} */${cycle.every} * * *`
    case 'minutes': return `*/${cycle.every} * * * *`
    case 'days': return `${minute} ${hour} */${cycle.every} * *`
    case 'weekly': return `${minute} ${hour} * * ${cycle.weekday}`
    case 'monthly': return `${minute} ${hour} ${cycle.day} * *`
  }
}

export function makeCronScheduleDraft(job: CronJob, revision = 0): CronScheduleDraft {
  const clock = cronClock(job.schedule)
  const cycle = cronCycleDraft(job.isNew ? '0 0 * * *' : job.schedule)
  return { id: job.id, instanceKey: job.instanceKey, expectedSchedule: job.schedule, expectedEnabled: job.enabled,
    hour: clock.hour || (job.isNew || ['hourly', 'hours', 'minutes'].includes(cycle.cycle) ? '00' : ''),
    minute: clock.minute || (job.isNew || cycle.cycle === 'minutes' ? '00' : ''), ...cycle, revision, ...(job.isNew ? { create: true } : {}) }
}

export function cronScheduleIsStale(draft: CronScheduleDraft, job: CronJob): boolean {
  return draft.id !== job.id || draft.instanceKey !== job.instanceKey || draft.expectedSchedule !== job.schedule || draft.expectedEnabled !== job.enabled || !!draft.create !== !!job.isNew
}

export function buildCronScheduleChange(draft: CronScheduleDraft, force = false): CronScheduleChange | null {
  const proposed = proposedCronSchedule(draft)
  if (!proposed || (!force && proposed === draft.expectedSchedule) || typeof draft.expectedEnabled !== 'boolean') return null
  const cycle = scheduleCycle(draft)
  const hour = ['hourly', 'hours', 'minutes'].includes(draft.cycle) ? '00' : draft.hour.padStart(2, '0')
  const minute = draft.cycle === 'minutes' ? '00' : draft.minute.padStart(2, '0')
  return { id: draft.id, instanceKey: draft.instanceKey, clock: `${hour}:${minute}`, expectedSchedule: draft.expectedSchedule, expectedEnabled: draft.expectedEnabled,
    ...(cycle && (draft.create || proposed !== withCronClock(draft.expectedSchedule, draft.hour, draft.minute)) ? { cycle } : {}), ...(draft.create ? { create: true } : {}) }
}

// Hide only this obsolete rclone advisory in the Drive viewer. Never alter
// stored logs, machine-readable history, or other errors (including auth/I/O).
const advisory = /^\d{4}\/\d{2}\/\d{2} \d{2}:\d{2}:\d{2}(?:\.\d+)? ERROR\s*:\s*Using --filter is recommended instead of both --include and --exclude as the order they are( parsed(?: in)? is indeterminate)?\s*$/
const advisoryContinuation = /^parsed(?: in)? is indeterminate$/

export function cronLogForDisplay(jobId: string, content: string): { content: string; hiddenAdvisories: number } {
  // New rclone JSON stats are stored unchanged. Show their human message in
  // the existing raw-text viewer, including real error messages and timestamps.
  content = content.split('\n').map(line => {
    if (!line.startsWith('{')) return line
    try {const record: unknown = JSON.parse(line)
      if (record && typeof record === 'object' && 'msg' in record && 'time' in record && 'level' in record && typeof record.msg === 'string' && typeof record.time === 'string' && typeof record.level === 'string') return `[${record.time}] ${record.level.toUpperCase()}: ${record.msg.trimEnd()}`
    } catch { /* Retain non-JSON and partial lines exactly. */ }
    return line
  }).join('\n')
  if (jobId !== 'drive-sync') return { content, hiddenAdvisories: 0 }
  const lines = content.split('\n'), kept: string[] = []
  let hiddenAdvisories = 0
  for (let i = 0; i < lines.length; i++) {
    const match = advisory.exec(lines[i])
    if (match && (match[1] || advisoryContinuation.test(lines[i + 1]?.trim() ?? ''))) {
      hiddenAdvisories++
      if (!match[1]) i++
    } else kept.push(lines[i])
  }
  return { content: kept.join('\n'), hiddenAdvisories }
}

export function cronLogLineLevel(line: string): 'info' | 'error' | 'warning' {
  if (/\b(?:ERROR|CRITICAL|FATAL|FAILED|invalid_grant)\b|MONITOR_RUN\|[^|]+\|[^|]+\|[^|]+\|failed\||Không thể|Không đọc được/i.test(line)) return 'error'
  if (/\bWARN(?:ING)?\b|DRY_RUN|Would delete:/i.test(line)) return 'warning'
  return 'info'
}
export function filterCronLog(content: string, query: string, level: 'all' | 'error' | 'warning') {
  const needle = query.toLocaleLowerCase().trim()
  return content.split('\n').filter(line => (!needle || line.toLocaleLowerCase().includes(needle)) && (level === 'all' || cronLogLineLevel(line) === level))
}

export const cronImportMaxBytes = 64 * 1024
type CronFileJob = { id: string; schedule: string; append?: boolean }
export type CronImportRow = CronFileJob & { previous: string | null; enabled: boolean; change: CronScheduleChange | null }
type CronFile = { format: 'backup-monitor-cron'; version: 2; exportedAt: string; jobs: CronFileJob[] }
type TransferApi = {
  jobs: () => Promise<CronJob[]>
  settings: () => Promise<BackupSystemState>
  preview: (state: BackupSystemState, change: CronScheduleChange) => Promise<ConfigurationPreview>
  apply: (state: BackupSystemState, token: string, change: CronScheduleChange) => Promise<unknown>
}

function firstClockValue(field: string, max: number): number | null {
  let first = Infinity
  for (const part of field.split(',')) {
    const match = /^(\*|\d+(?:-\d+)?)(?:\/(\d+))?$/.exec(part)
    if (!match) return null
    const step = match[2] === undefined ? 1 : Number(match[2])
    if (step < 1 || step > max + 1) return null
    const bounds = match[1] === '*' ? [0, max] : match[1].split('-').map(Number)
    const start = bounds[0], end = bounds[1] ?? start
    if (start > end || end > max || start < 0) return null
    first = Math.min(first, start)
  }
  return Number.isFinite(first) ? first : null
}

// Sort by the earliest clock time in a day, not by the last log or next date.
function cronStartMinute(schedule: string): number {
  const fields = schedule.trim().split(/\s+/)
  if (fields.length !== 5) return Infinity
  const minute = firstClockValue(fields[0], 59), hour = firstClockValue(fields[1], 23)
  return minute === null || hour === null ? Infinity : hour * 60 + minute
}

export function sortCronJobsByTime<T extends Pick<CronJob, 'id' | 'schedule'>>(jobs: readonly T[]): T[] {
  return [...jobs].sort((a, b) => cronStartMinute(a.schedule) - cronStartMinute(b.schedule) || a.id.localeCompare(b.id))
}

// Only use a schedule confirmed by a successful server apply, never a draft.
export function applySavedCronSchedule(jobs: CronJob[] | null, saved: ConfigurationPreview['schedule']): CronJob[] | null {
  if (!jobs || !saved) return jobs
  // Creation is refreshed from the server (there is no previous row to update).
  if (!saved.previous) return jobs
  return jobs.map(job => job.id === saved.id && job.schedule === saved.previous ? { ...job, schedule: saved.proposed, enabled: saved.enabled } : job)
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
  if (!record(value) || value.format !== 'backup-monitor-cron' || (value.version !== 1 && value.version !== 2) || !onlyKeys(value, ['format', 'version', 'exportedAt', 'jobs']) || typeof value.exportedAt !== 'string' || value.exportedAt.length > 40 || !Number.isFinite(Date.parse(value.exportedAt)) || !Array.isArray(value.jobs) || !value.jobs.length || value.jobs.length > (value.version === 1 ? Object.keys(backupTaskScripts).length : 256)) {
    throw new Error('Hãy dùng tệp JSON xuất từ dashboard này; không hỗ trợ tệp aaPanel hoặc định dạng khác.')
  }
  const seen = new Set<string>()
  return value.jobs.map(job => {
    if (!record(job) || !onlyKeys(job, ['id', 'schedule']) || typeof job.id !== 'string' || !known(job.id) || typeof job.schedule !== 'string') throw new Error('Tệp có tác vụ lạ hoặc trường dữ liệu không được phép.')
    const schedule = canonicalSchedule(job.schedule)
    const key = value.version === 1 ? job.id : `${job.id}:${schedule}`
    if (seen.has(key)) throw new Error('Tệp có lịch chạy trùng lặp.')
    seen.add(key)
    return { id: job.id, schedule, ...(value.version === 2 ? { append: true } : {}) }
  })
}

export function exportCronFile(jobs: CronJob[], now = new Date()): string {
  const managed = jobs.filter(job => known(job.id) && !job.isNew)
  if (!managed.length) throw new Error('Chưa có lịch chạy để xuất.')
  const value: CronFile = { format: 'backup-monitor-cron', version: 2, exportedAt: now.toISOString(), jobs: managed.map(job => ({ id: job.id, schedule: job.schedule })) }
  // Export only restorable schedules, never shell commands, paths or credentials.
  value.jobs = parseCronFile(JSON.stringify(value)).map(({ id, schedule }) => ({ id, schedule }))
  return JSON.stringify(value, null, 2)
}

export function planCronImport(entries: CronFileJob[], jobs: CronJob[]): CronImportRow[] {
  return entries.map(entry => {
    if (!known(entry.id)) throw new Error('Không thể nhập tác vụ không được dashboard quản lý.')
    const schedule = canonicalSchedule(entry.schedule)
    const matches = jobs.filter(job => job.id === entry.id && !job.isNew)
    const exact = matches.filter(job => job.schedule === schedule)
    if (exact.length > 1 || (!entry.append && !exact.length && matches.length > 1)) throw new Error('Không xác định được lịch cần thay thế trong tệp phiên bản cũ. Dùng tệp xuất mới hoặc sửa từng lịch.')
    // V2 restores a set of schedules additively. V1 keeps its legacy single-
    // schedule replacement behavior only when the target is unambiguous.
    const current = exact[0] ?? (entry.append ? undefined : matches[0])
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
  const matches = jobs.filter(job => job.id === row.id && !job.isNew && job.schedule === (row.previous ?? row.schedule))
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
  cause: unknown
  completedIds: string[]
  failedId: string
  writeAttempted: boolean
  constructor(cause: unknown, completedIds: string[], failedId: string, writeAttempted: boolean) {
    super(cause instanceof Error ? cause.message : 'Không nhập được lịch Cron.')
    this.name = 'CronImportError'; this.cause = cause; this.completedIds = [...completedIds]; this.failedId = failedId; this.writeAttempted = writeAttempted
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
        const saved = (await api.jobs()).filter(job => job.id === row.id && job.schedule === row.schedule)
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
