import type { CronJob } from './types'
import type { CronScheduleChange } from './backupSystemApi'
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
  if (!/^(?:[01]\d|2[0-3]):[0-5]\d$/.test(`${hour}:${minute}`)) return null
  const fields = schedule.trim().split(/\s+/)
  if (fields.length !== 5) return null
  return `${Number(minute)} ${Number(hour)} ${fields.slice(2).join(' ')}`
}

export type CronScheduleDraft = { id: string; expectedSchedule: string; expectedEnabled?: boolean; hour: string; minute: string; revision: number }

export function makeCronScheduleDraft(job: CronJob, revision = 0): CronScheduleDraft {
  return { id: job.id, expectedSchedule: job.schedule, expectedEnabled: job.enabled, ...cronClock(job.schedule), revision }
}

export function cronScheduleIsStale(draft: CronScheduleDraft, job: CronJob): boolean {
  return draft.id !== job.id || draft.expectedSchedule !== job.schedule || draft.expectedEnabled !== job.enabled
}

export function buildCronScheduleChange(draft: CronScheduleDraft): CronScheduleChange | null {
  const proposed = withCronClock(draft.expectedSchedule, draft.hour, draft.minute)
  if (!proposed || proposed === draft.expectedSchedule || typeof draft.expectedEnabled !== 'boolean') return null
  return { id: draft.id, clock: `${draft.hour}:${draft.minute}`, expectedSchedule: draft.expectedSchedule, expectedEnabled: draft.expectedEnabled }
}
