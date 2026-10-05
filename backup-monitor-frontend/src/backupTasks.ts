import type { CronJob } from './types'
import type { CronScheduleChange, ScheduleCycle } from './backupSystemApi'
import type { BackupSource, BackupSystemConfig, BackupSystemState } from './backupSystemApi'

export const sourceConfigKeys: Record<string, 'siteSource' | 'databaseSource' | 'panelSource'> = { 'backup-site': 'siteSource', 'backup-database': 'databaseSource', 'backup-panel': 'panelSource' }
export function backupSourceForTask(config: BackupSystemConfig, state: BackupSystemState, id: string): BackupSource | null {
  const key = sourceConfigKeys[id]
  return key ? (config[key] ?? state.sources?.[id] ?? null) : null
}

export const backupTaskLabels: Record<string, { vi: string; en: string }> = {
  'cleanup-panel': { vi: 'Dọn dẹp thư mục panel', en: 'Clean up panel backups' },
  'backup-site': { vi: 'Sao lưu website', en: 'Backup websites' },
  'backup-database': { vi: 'Sao lưu cơ sở dữ liệu', en: 'Backup databases' },
  'backup-panel': { vi: 'Sao lưu cấu hình aaPanel', en: 'Back up aaPanel configuration' },
  'drive-sync': { vi: 'Đồng bộ Google Drive', en: 'Sync Google Drive' },
  'integrity-check': { vi: 'Kiểm tra số thư mục sao lưu', en: 'Check backup folder count' },
}

export const backupTaskScripts: Record<string, string> = {
  'backup-site': 'backup-site.sh', 'backup-database': 'backup-database.sh', 'backup-panel': 'backup-panel.sh',
  'cleanup-panel': 'cleanup-panel-backups.sh', 'drive-sync': 'auto_backup.sh', 'integrity-check': 'check_integrity.sh',
}

export const cronHours = Array.from({ length: 24 }, (_, hour) => String(hour).padStart(2, '0'))
export const cronMinutes = Array.from({ length: 60 }, (_, minute) => String(minute).padStart(2, '0'))

export function cronClock(schedule: string): { hour: string; minute: string } {
  const [minute, hour] = schedule.trim().split(/\s+/)
  return {
    hour: /^\d{1,2}$/.test(hour ?? '') && Number(hour) < 24 ? hour.padStart(2, '0') : '',
    minute: /^\d{1,2}$/.test(minute ?? '') && Number(minute) < 60 ? minute.padStart(2, '0') : '',
  }
}

export function withCronClock(schedule: string, hour: string, minute: string): string | null {
  if (!/^\d{1,2}$/.test(hour) || !/^\d{1,2}$/.test(minute) || Number(hour) > 23 || Number(minute) > 59) return null
  const fields = schedule.trim().split(/\s+/)
  if (fields.length !== 5) return null
  return `${Number(minute)} ${Number(hour)} ${fields.slice(2).join(' ')}`
}

export type CronScheduleDraft = { id: string; expectedSchedule: string; expectedEnabled?: boolean; hour: string; minute: string; revision: number; cycle: ScheduleCycle['type'] | 'current'; every: string; weekday: string; day: string; create?: boolean }

export const cycleLabels = {
  current: { vi: 'Giữ chu kỳ hiện tại', en: 'Keep current recurrence' },
  daily: { vi: 'Hằng ngày', en: 'Daily' }, days: { vi: 'Mỗi N ngày trong tháng', en: 'Every N calendar days' },
  hourly: { vi: 'Hằng giờ', en: 'Hourly' }, hours: { vi: 'Mỗi N giờ', en: 'Every N hours' },
  minutes: { vi: 'Mỗi N phút', en: 'Every N minutes' }, weekly: { vi: 'Hằng tuần', en: 'Weekly' }, monthly: { vi: 'Hằng tháng', en: 'Monthly' },
}

export function cronCycleDraft(schedule: string): Pick<CronScheduleDraft, 'cycle' | 'every' | 'weekday' | 'day'> {
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

export function scheduleCycle(draft: CronScheduleDraft): ScheduleCycle | null {
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
  return { id: job.id, expectedSchedule: job.schedule, expectedEnabled: job.enabled,
    hour: clock.hour || (job.isNew || ['hourly', 'hours', 'minutes'].includes(cycle.cycle) ? '00' : ''),
    minute: clock.minute || (job.isNew || cycle.cycle === 'minutes' ? '00' : ''), ...cycle, revision, ...(job.isNew ? { create: true } : {}) }
}

export function cronScheduleIsStale(draft: CronScheduleDraft, job: CronJob): boolean {
  return draft.id !== job.id || draft.expectedSchedule !== job.schedule || draft.expectedEnabled !== job.enabled || !!draft.create !== !!job.isNew
}

export function buildCronScheduleChange(draft: CronScheduleDraft): CronScheduleChange | null {
  const proposed = proposedCronSchedule(draft)
  if (!proposed || proposed === draft.expectedSchedule || typeof draft.expectedEnabled !== 'boolean') return null
  const cycle = scheduleCycle(draft)
  const hour = ['hourly', 'hours', 'minutes'].includes(draft.cycle) ? '00' : draft.hour.padStart(2, '0')
  const minute = draft.cycle === 'minutes' ? '00' : draft.minute.padStart(2, '0')
  return { id: draft.id, clock: `${hour}:${minute}`, expectedSchedule: draft.expectedSchedule, expectedEnabled: draft.expectedEnabled,
    ...(cycle && (draft.create || proposed !== withCronClock(draft.expectedSchedule, draft.hour, draft.minute)) ? { cycle } : {}), ...(draft.create ? { create: true } : {}) }
}
