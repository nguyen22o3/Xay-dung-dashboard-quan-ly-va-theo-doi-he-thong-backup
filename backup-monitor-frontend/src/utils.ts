export function formatBytes(bytes: number): string {
  if (bytes === 0 || bytes === undefined || bytes === null) return '0 B'
  const isNegative = bytes < 0
  const absBytes = Math.abs(bytes)
  if (absBytes < 1) return (isNegative ? '-' : '') + absBytes.toFixed(2) + ' B'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(absBytes) / Math.log(k))
  const formatted = parseFloat((absBytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i]
  return (isNegative ? '-' : '') + formatted
}

export function formatDuration(seconds: number): string {
  const mins = Math.floor(seconds / 60)
  const secs = seconds % 60
  return mins > 0 ? `${mins}m ${secs}s` : `${secs}s`
}

export function formatCronSchedule(schedule: string, lang: 'vi' | 'en' = 'vi'): string {
  const parts = schedule.trim().split(/\s+/)
  if (parts.length < 5) return schedule
  const [min, hour] = parts
  const dayOfMonth = parts[2]
  const month = parts[3]
  const dow = parts[4]

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
    return lang === 'vi' ? `Mỗi giờ vào ${dow}` : `Every hour on ${dow}`
  }
  if (dayOfMonth === '*' && month === '*') {
    return `${hour.padStart(2, '0')}:${min.padStart(2, '0')} (${dow})`
  }
  return lang === 'vi'
    ? `${hour.padStart(2, '0')}:${min.padStart(2, '0')} ngày ${dayOfMonth}/${month}`
    : `${hour.padStart(2, '0')}:${min.padStart(2, '0')} on ${dayOfMonth}/${month}`
}
