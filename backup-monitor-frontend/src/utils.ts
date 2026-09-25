export function formatBytes(bytes: number): string {
  if (!bytes || bytes === 0) return '0 B'
  if (bytes < 1) return bytes.toFixed(2) + ' B'
  const k = 1024
  const sizes = ['B', 'KB', 'MB', 'GB', 'TB']
  const i = Math.floor(Math.log(bytes) / Math.log(k))
  return parseFloat((bytes / Math.pow(k, i)).toFixed(2)) + ' ' + sizes[i]
}

export function formatDuration(seconds: number): string {
  const mins = Math.floor(seconds / 60)
  const secs = seconds % 60
  return mins > 0 ? `${mins}m ${secs}s` : `${secs}s`
}

export function formatCronSchedule(schedule: string): string {
  const parts = schedule.trim().split(/\s+/)
  if (parts.length < 5) return schedule
  const [min, hour] = parts
  const dayOfMonth = parts[2]
  const month = parts[3]
  const dow = parts[4]

  if (min === '*' && hour === '*' && dayOfMonth === '*' && month === '*' && dow === '*') {
    return 'Mỗi phút'
  }
  if (hour === '*' && dayOfMonth === '*' && month === '*' && dow === '*') {
    return `Mỗi giờ phút ${min}`
  }
  if (dayOfMonth === '*' && month === '*' && dow === '*') {
    return `${hour.padStart(2, '0')}:${min.padStart(2, '0')} hằng ngày`
  }
  if (min === '*' && hour === '*' && dayOfMonth === '*' && month === '*' && dow !== '*') {
    return `Mỗi giờ vào ${dow}`
  }
  if (dayOfMonth === '*' && month === '*') {
    return `${hour.padStart(2, '0')}:${min.padStart(2, '0')} (${dow})`
  }
  return `${hour.padStart(2, '0')}:${min.padStart(2, '0')} dd/${month}`
}