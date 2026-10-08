interface ResourceUsage {
  total: string
  used: string
  free: string
  usage: string
}

interface LocalBackupInfo {
  size: string
  count: number
  latest_date: string
  latest_name: string
}

export interface ServerStatus {
  server_time?: string
  disk?: ResourceUsage
  ram?: ResourceUsage
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
  id?: string
  runId?: string
  path?: string
  evidence?: 'log' | 'archive'
  kind?: 'file' | 'run' | 'archive'
  name?: string
  date: string
  time: string
  duration: number | string | null
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
  reason?: string
}

type CronJobStatus = 'running' | 'success' | 'failed' | 'never' | 'unknown'

export interface CronJob {
  isNew?: boolean
  id: string
  name: string
  script: string
  schedule: string
  enabled?: boolean
  last_run: string
  log_updated_at?: string
  tracked_at?: string
  schedule_tracked?: boolean
  task_controlled?: boolean
  status: CronJobStatus
}

export interface CronJobLog {
  content: string
  exists: boolean
}

export interface SnapshotFile {
  path?: string
  date: string
  category: string
  name: string
  size: number
  modified: string
}

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
  telegramEnabled?: boolean
  discordEnabled?: boolean
  smtpEnabled?: boolean
}

export interface AlertSettingsUpdate {
  telegramToken: string
  telegramChat: string
  discordWebhook: string
  smtpEmail: string
  smtpPassword: string
  targetEmail: string
  threshold: number
  telegramEnabled: boolean
  discordEnabled: boolean
  smtpEnabled: boolean
}

export type TabKey = 'dashboard' | 'home' | 'server' | 'settings' | 'jobs' | 'activity' | 'available' | 'options' | 'logs' | 'registration' | 'crypto'
