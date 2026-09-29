export interface DiskInfo {
  total: string
  used: string
  free: string
  usage: string
}

export interface RamInfo {
  total: string
  used: string
  free: string
  usage: string
}

export interface LocalBackupInfo {
  size: string
  count: number
  latest_date: string
  latest_name: string
}

export interface ServerStatus {
  disk?: DiskInfo
  ram?: RamInfo
  cpu?: string
  uptime?: string
  websites?: number
  databases?: number
  crons?: number
  cron_times?: string
  drive_crons?: number
  drive_cron_times?: string
  local_crons?: number
  local_cron_times?: string
  local_backup?: LocalBackupInfo
}

export interface BackupActivityEntry {
  name?: string
  date: string
  time: string
  duration: number | string
  status: string
}

export interface BackupStatus {
  driveError?: string
  driveStale?: boolean
  driveDataAt?: string
  about?: { total?: number; used?: number; free?: number }
  size?: { count?: number; bytes?: number }
  dirs?: string
  totalFolders?: number
  history?: { date: string; bytes: number; files?: number }[]
  activity?: BackupActivityEntry[]
  localActivity?: BackupActivityEntry[]
  todayBreakdownBytes?: {
    site: number
    database: number
    panel: number
  }
}

export interface WebsiteStatus {
  name: string
  status: string
  code: string
  time: string
}

export type CronJobStatus = 'success' | 'failed' | 'never' | 'unknown'

export interface CronJob {
  id: string
  name: string
  script: string
  schedule: string
  last_run: string
  status: CronJobStatus
}

export interface CronJobLog {
  content: string
  exists: boolean
}

export interface AppConfig {
  retention_days: number
  notify_enabled: boolean
  notify_token: string
  notify_chat_id: string
  email_recipient: string
}

export interface SnapshotFile {
  path?: string
  date: string
  category: string
  name: string
  size: number
  modified: string
}

export type BackupJobId = 'drive-sync' | 'site-backup' | 'database-backup'

export interface LoginResponse {
  token: string
}

export interface AlertSettings {
  telegramChat: string
  smtpEmail: string
  targetEmail: string
  threshold: number
  telegramConfigured: boolean
  discordConfigured: boolean
  smtpConfigured: boolean
}

export interface AlertSettingsUpdate {
  telegramToken: string
  telegramChat: string
  discordWebhook: string
  smtpEmail: string
  smtpPassword: string
  targetEmail: string
  threshold: number
}

export type TabKey = 'dashboard' | 'home' | 'server' | 'settings' | 'jobs' | 'activity' | 'available' | 'options' | 'logs' | 'registration' | 'crypto'
