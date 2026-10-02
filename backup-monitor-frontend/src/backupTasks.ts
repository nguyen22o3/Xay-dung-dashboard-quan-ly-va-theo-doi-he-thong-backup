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
