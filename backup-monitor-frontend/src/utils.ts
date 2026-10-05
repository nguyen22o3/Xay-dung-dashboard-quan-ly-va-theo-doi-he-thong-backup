import type { BackupActivityEntry, CronJob } from './types'

export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes)) return '—'
  if (bytes === 0 || bytes === undefined || bytes === null) return '0 B'
  const isNegative = bytes < 0
  const absBytes = Math.abs(bytes)
  if (absBytes < 1) return (isNegative ? '-' : '') + absBytes.toFixed(2) + ' B'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.min(sizes.length - 1, Math.floor(Math.log(absBytes) / Math.log(k)))
  const formatted = parseFloat((absBytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i]
  return (isNegative ? '-' : '') + formatted
}

export function formatDuration(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0) return '—'
  const mins = Math.floor(seconds / 60)
  const secs = Number((seconds % 60).toFixed(3))
  return mins > 0 ? `${mins}m ${secs}s` : `${secs}s`
}

export function formatUptime(uptime: string, lang: 'vi' | 'en'): string {
  if (lang !== 'vi') return uptime
  const units: Record<string, string> = {
    year: 'năm', month: 'tháng', week: 'tuần', day: 'ngày',
    hour: 'giờ', minute: 'phút', second: 'giây',
  }
  return uptime.replace(/\b(\d+)\s+(years?|months?|weeks?|days?|hours?|minutes?|seconds?)\b/gi,
    (_, count: string, unit: string) => `${count} ${units[unit.toLowerCase().replace(/s$/, '')]}`)
}

export function readPercentage(value?: string): number | null {
  const number = Number.parseFloat(value ?? '')
  return Number.isFinite(number) ? Math.max(0, Math.min(100, number)) : null
}

// Use the server's UTC offset, not the viewer's local timezone.
export function serverClock(serverTime?: string, now = new Date()): Date {
  const offset = serverTime?.match(/([+-])(\d{2}):(\d{2})$/)
  const minutes = offset ? (Number(offset[2]) * 60 + Number(offset[3])) * (offset[1] === '+' ? 1 : -1) : 0
  return new Date(now.getTime() + minutes * 60000)
}

export function calendarDays(serverTime?: string, count = 14, now = new Date()): string[] {
  const today = serverClock(serverTime, now)
  return Array.from({ length: count }, (_, i) => {
    const date = new Date(today)
    date.setUTCDate(today.getUTCDate() - count + 1 + i)
    return date.toISOString().slice(0, 10)
  })
}

function cronFieldMatches(field: string, value: number, min: number, max: number, weekday = false): boolean {
  return field.split(',').some((part) => {
    const [range, stepText] = part.split('/')
    const step = stepText === undefined ? 1 : Number(stepText)
    if (!Number.isInteger(step) || step < 1) return false
    const bounds = range === '*' ? [min, max] : range.split('-').map(Number)
    const start = bounds[0]
    const end = bounds[1] ?? (stepText === undefined ? start : max)
    if (!Number.isInteger(start) || !Number.isInteger(end) || start < min || end > max || start > end) return false
    for (let n = start; n <= end; n += step) if ((weekday && n === 7 ? 0 : n) === value) return true
    return false
  })
}

export function nextCronRun(jobs: Pick<CronJob, 'schedule'>[], serverTime?: string, lang: 'vi' | 'en' = 'vi', now = new Date()): string | null {
  if (!serverTime) return null
  const clock = serverClock(serverTime, now)
  let next: Date | null = null
  for (const job of jobs) {
    const fields = job.schedule.trim().split(/\s+/)
    if (fields.length !== 5 || !/^\d+$/.test(fields[0]) || !/^\d+$/.test(fields[1])) continue
    const minute = Number(fields[0]), hour = Number(fields[1])
    if (minute > 59 || hour > 23) continue
    for (let day = 0; day <= 366; day++) {
      const date = new Date(clock)
      date.setUTCDate(clock.getUTCDate() + day)
      date.setUTCHours(hour, minute, 0, 0)
      if (date <= clock || !cronFieldMatches(fields[3], date.getUTCMonth() + 1, 1, 12)) continue
      const dom = cronFieldMatches(fields[2], date.getUTCDate(), 1, 31)
      const dow = cronFieldMatches(fields[4], date.getUTCDay(), 0, 7, true)
      const matchesDay = fields[2] === '*' || fields[4] === '*' ? dom && dow : dom || dow
      if (!matchesDay) continue
      if (!next || date < next) next = date
      break
    }
  }
  if (!next) return null
  const time = next.toISOString().slice(11, 16)
  const today = clock.toISOString().slice(0, 10)
  const tomorrow = new Date(clock)
  tomorrow.setUTCDate(tomorrow.getUTCDate() + 1)
  const day = next.toISOString().slice(0, 10)
  return `${time}${day === today ? '' : day === tomorrow.toISOString().slice(0, 10) ? (lang === 'vi' ? ' (ngày mai)' : ' (tomorrow)') : ` (${day})`}`
}

export function activityStatus(status: string): 'success' | 'failed' | 'running' | 'unknown' {
  const normalized = status.trim().toLowerCase()
  if (['successful', 'success', 'ok', 'thành công'].includes(normalized)) return 'success'
  if (['failed', 'failure', 'error', 'thất bại'].includes(normalized)) return 'failed'
  if (['running', 'started'].includes(normalized)) return 'running'
  return 'unknown'
}

export function statusLabel(status: string, lang: 'vi' | 'en'): string {
  const labels = { success: ['Thành công', 'Success'], failed: ['Thất bại', 'Failed'], running: ['Đang chạy', 'Running'], unknown: ['Chưa xác nhận', 'Unconfirmed'] }
  return labels[activityStatus(status)][lang === 'vi' ? 0 : 1]
}

export function formatTime24(time: string): string {
  const match = time.trim().match(/^(\d{1,2}):(\d{2})(?::(\d{2}))?\s*(AM|PM)?$/i)
  if (!match) return time
  let hour = Number(match[1])
  if (match[4]) hour = hour % 12 + (match[4].toUpperCase() === 'PM' ? 12 : 0)
  return `${String(hour).padStart(2, '0')}:${match[2]}:${match[3] ?? '00'}`
}

export function activityStamp(entry: BackupActivityEntry): string {
  return `${entry.date} ${formatTime24(entry.time)}`
}

export interface BackupActivitySummary {
  key: string
  name: 'Backup Site' | 'Backup Database' | 'Backup aaPanel'
  date: string
  time: string
  duration: number | null
  status: string
  source: 'run' | 'files' | 'archive'
  details: BackupActivityEntry[]
}

// A table row can contain several file records from one run. Delete their log
// records too, otherwise they reappear as a legacy row after removing the run.
export function localActivityDeletionKeys(row: BackupActivitySummary): string[] {
  if (row.source === 'archive') return []
  const kind = row.name === 'Backup Site' ? 'site' : row.name === 'Backup aaPanel' ? 'panel' : 'database'
  return [...new Set([row, ...row.details].map(entry => `${entry.date}|${formatTime24(entry.time)}|${kind}`))]
}

function localBackupType(entry: BackupActivityEntry): 'site' | 'database' | 'panel' | null {
  const name = (entry.name ?? '').trim().toLowerCase()
  if (/^(?:backup (?:website|site)\b|website backup\b|(?:lần chạy )?sao lưu website\b)/.test(name)) return 'site'
  if (/^(?:backup database\b|database backup\b|(?:lần chạy )?sao lưu cơ sở dữ liệu)/.test(name)) return 'database'
  if (/^(?:backup (?:aapanel|panel)\b|(?:lần chạy )?sao lưu cấu hình aapanel\b)/.test(name)) return 'panel'
  return null
}

function recordedDuration(entry: BackupActivityEntry): number | null {
  if (entry.duration == null || (typeof entry.duration === 'string' && entry.duration.trim() === '')) return null
  const duration = Number(entry.duration)
  return Number.isFinite(duration) && duration >= 0 ? duration : null
}

// Compact server history only. Keep every tracked run, use its authoritative
// result/duration, and suppress the file details it covers. Legacy file records
// are grouped by exact start time, never by minute (two runs may share a minute).
export function summarizeLocalBackupActivity(entries: BackupActivityEntry[]): BackupActivitySummary[] {
  const classified = entries.map(entry => ({ entry, type: localBackupType(entry) }))
    .filter(item => item.type !== null)
  const runs = classified.filter(item => item.entry.kind === 'run').map(item => {
    const duration = recordedDuration(item.entry)
    const start = Date.parse(`${item.entry.date}T${formatTime24(item.entry.time)}Z`)
    return { ...item, duration, start }
  })
  const runCounts = new Map<string, number>()
  const rows: BackupActivitySummary[] = runs.map(({ entry, type, duration }) => {
    const stampKey = `run:${type}:${activityStamp(entry)}`
    const ordinal = runCounts.get(stampKey) ?? 0
    runCounts.set(stampKey, ordinal + 1)
    return {
      key: `${stampKey}:${ordinal}`,
      name: type === 'site' ? 'Backup Site' : type === 'panel' ? 'Backup aaPanel' : 'Backup Database',
      date: entry.date,
      time: formatTime24(entry.time),
      duration,
      status: activityStatus(entry.status),
      source: 'run',
      details: [],
    }
  })
  const legacyGroups = new Map<string, { row: BackupActivitySummary; missingDuration: boolean }>()
  const seenFiles = new Set<string>()
  const severity = { success: 0, unknown: 1, running: 2, failed: 3 }
  for (const { entry, type } of classified) {
    if (entry.kind === 'run') continue
    if (entry.kind === 'archive') {
      // Backfill only days without aaPanel log records. Never merge inventory
      // into a timed run or expose inventory as a deletable log.
      if (type !== 'panel' || classified.some(item => item.type === 'panel' && item.entry.kind !== 'archive' && item.entry.date === entry.date)) continue
      const key = `archive:panel:${entry.date}`
      if (seenFiles.has(key)) continue
      seenFiles.add(key)
      rows.push({ key, name: 'Backup aaPanel', date: entry.date, time: '', duration: null, status: 'unknown', source: 'archive', details: [entry] })
      continue
    }
    const fingerprint = JSON.stringify([type, activityStamp(entry), entry.name, entry.duration, entry.status])
    if (seenFiles.has(fingerprint)) continue
    seenFiles.add(fingerprint)
    const stamp = activityStamp(entry)
    const start = Date.parse(`${entry.date}T${formatTime24(entry.time)}Z`)
    const runIndex = runs.findIndex(run => run.type === type && (
      activityStamp(run.entry) === stamp ||
      (run.duration !== null && Number.isFinite(start) && Number.isFinite(run.start) && start >= run.start && start <= run.start + run.duration * 1000)
    ))
    if (runIndex >= 0) {
      rows[runIndex].details.push(entry)
      continue
    }
    const key = `files:${type}:${stamp}`
    const duration = recordedDuration(entry)
    const status = activityStatus(entry.status)
    const group = legacyGroups.get(key)
    if (!group) {
      legacyGroups.set(key, { row: {
        key, name: type === 'site' ? 'Backup Site' : type === 'panel' ? 'Backup aaPanel' : 'Backup Database',
        date: entry.date, time: formatTime24(entry.time), duration, status, source: 'files', details: [entry],
      }, missingDuration: duration === null })
      continue
    }
    group.missingDuration ||= duration === null
    group.row.details.push(entry)
    if (duration !== null) group.row.duration = (group.row.duration ?? 0) + duration
    if (severity[status] > severity[activityStatus(group.row.status)]) group.row.status = status
  }
  for (const { row, missingDuration } of legacyGroups.values()) {
    if (missingDuration) row.duration = null
    rows.push(row)
  }
  return rows.sort((a, b) => `${b.date} ${b.time}`.localeCompare(`${a.date} ${a.time}`))
}

// Use the same authoritative run totals as the activity page. File details
// covered by a run must never be added again; legacy groups remain a fallback.
export function localBackupDurationByDay(entries: BackupActivityEntry[], days: string[]) {
  const totals = new Map<string, number>()
  const allowedDays = new Set(days)
  for (const row of summarizeLocalBackupActivity(entries)) {
    if (!allowedDays.has(row.date) || row.duration === null) continue
    totals.set(row.date, (totals.get(row.date) ?? 0) + row.duration)
  }
  return days.map(date => ({
    date,
    duration: totals.has(date) ? Number(totals.get(date)!.toFixed(3)) : null,
  }))
}

export function categoryLabel(category: string, lang: 'vi' | 'en'): string {
  return ({ site: ['Website', 'Website'], database: ['Cơ sở dữ liệu', 'Database'], panel: ['aaPanel', 'aaPanel'], root: ['Khác', 'Other'] }[category])?.[lang === 'vi' ? 0 : 1] ?? category
}

export function formatCronSchedule(schedule: string, lang: 'vi' | 'en' = 'vi'): string {
  const parts = schedule.trim().split(/\s+/)
  if (parts.length < 5) return schedule
  const [min, hour] = parts
  const dayOfMonth = parts[2]
  const month = parts[3]
  const dow = parts[4]
  if (/^\*\/[1-9]\d?$/.test(min) && hour === '*' && dayOfMonth === '*' && month === '*' && dow === '*') return lang === 'vi' ? `Mỗi ${Number(min.slice(2))} phút (tính từ phút 0 mỗi giờ)` : `Every ${Number(min.slice(2))} minutes (from minute 0 each hour)`
  if (/^\d{1,2}$/.test(min) && /^\*\/[1-9]\d?$/.test(hour) && dayOfMonth === '*' && month === '*' && dow === '*') return lang === 'vi' ? `Mỗi ${Number(hour.slice(2))} giờ, phút ${min.padStart(2, '0')} (tính từ 00:00 mỗi ngày)` : `Every ${Number(hour.slice(2))} hours at minute ${min.padStart(2, '0')} (from 00:00 daily)`
  if (/^\d{1,2}$/.test(min) && /^\d{1,2}$/.test(hour) && /^\*\/[1-9]\d?$/.test(dayOfMonth) && month === '*' && dow === '*') return lang === 'vi' ? `${hour.padStart(2, '0')}:${min.padStart(2, '0')}, cách ${Number(dayOfMonth.slice(2))} ngày trong tháng (từ ngày 1)` : `${hour.padStart(2, '0')}:${min.padStart(2, '0')}, every ${Number(dayOfMonth.slice(2))} calendar days (from day 1)`
  if (!/^(?:\d{1,2}|\*)$/.test(min) || !/^(?:\d{1,2}|\*)$/.test(hour)) return schedule

  if (min === '*' && hour === '*' && dayOfMonth === '*' && month === '*' && dow === '*') {
    return lang === 'vi' ? 'Mỗi phút' : 'Every minute'
  }
  if (hour === '*' && dayOfMonth === '*' && month === '*' && dow === '*') {
    return lang === 'vi' ? `Mỗi giờ phút ${min}` : `At minute ${min} of every hour`
  }
  if (dayOfMonth === '*' && month === '*' && dow === '*') {
    const clock = `${hour.padStart(2, '0')}:${min.padStart(2, '0')}`
    return lang === 'vi' ? `${clock} hằng ngày` : `${clock} daily`
  }
  if (min === '*' && hour === '*' && dayOfMonth === '*' && month === '*' && dow !== '*') {
    return schedule
  }
  if (dayOfMonth === '*' && month === '*') {
    const days = lang === 'vi' ? ['Chủ nhật', 'Thứ hai', 'Thứ ba', 'Thứ tư', 'Thứ năm', 'Thứ sáu', 'Thứ bảy', 'Chủ nhật'] : ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday', 'Sunday']
    return /^\d$/.test(dow) && days[Number(dow)] ? `${hour.padStart(2, '0')}:${min.padStart(2, '0')} (${days[Number(dow)]})` : schedule
  }
  return lang === 'vi'
    ? `${hour.padStart(2, '0')}:${min.padStart(2, '0')} ngày ${dayOfMonth}/${month}`
    : `${hour.padStart(2, '0')}:${min.padStart(2, '0')} on ${dayOfMonth}/${month}`
}
