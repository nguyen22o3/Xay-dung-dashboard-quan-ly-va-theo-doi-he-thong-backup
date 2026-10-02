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
