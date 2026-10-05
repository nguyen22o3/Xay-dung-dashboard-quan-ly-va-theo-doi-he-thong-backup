import type { CronJob } from './types'
import type { ConfigurationPreview } from './backupSystemApi'

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
export function cronStartMinute(schedule: string): number {
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
  return jobs.map(job => job.id === saved.id ? { ...job, schedule: saved.proposed, enabled: saved.enabled } : job)
}
