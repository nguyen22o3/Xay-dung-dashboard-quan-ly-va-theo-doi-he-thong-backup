import { client } from './api'
export type BackupSource = { path: string; scope: 'folder' | 'children' }
export type BackupSystemConfig = { backupRoot: string; scriptsDir: string; logsDir: string; driveRemote: string; driveFolder: string; driveHistoryLog: string; siteSource?: BackupSource; databaseSource?: BackupSource; panelSource?: BackupSource }
export type CronScheduleChange = { id: string; clock: string; expectedSchedule: string; expectedEnabled: boolean }
export type BackupSystemState = {
  config: BackupSystemConfig; version: string; remotes: string[]; ready: boolean
  localRetentionDays: number; driveRetentionDays: number; allowedRoots: Record<string, string[]>
  applied?: boolean; recoveryDir?: string; warnings?: string[]
  sources?: Record<string, BackupSource>
}
export type ConfigurationPreview = {
  token: string; changed: boolean; filesToMove: number; bytesToMove: number; daysToMove: number
  scriptsToUpdate: number; logsToMove: number; driveChanged: boolean; config: BackupSystemConfig; version: string
  schedule?: { id: string; previous: string; proposed: string; enabled: boolean; changed: boolean }
  sources?: { id: string; previous: BackupSource; proposed: BackupSource }[]
}
export type FolderRequest = { location: 'local' | 'drive'; purpose: 'backup' | 'scripts' | 'logs' | 'source-site' | 'source-database' | 'source-panel'; path: string; remote?: string }
export type FolderList = { path: string; exists: boolean; parent: string | null; entries: { name: string; path: string }[] }
export async function fetchBackupSystem(): Promise<BackupSystemState> { return (await client.get<BackupSystemState>('/api/backup-system')).data }
export async function listBackupFolders(request: FolderRequest): Promise<FolderList> { return (await client.post<FolderList>('/api/backup-system/directories/list', request)).data }
export async function createBackupFolder(request: FolderRequest): Promise<void> { await client.post('/api/backup-system/directories/create', request) }
export async function previewBackupSystem(config: BackupSystemConfig, expectedVersion: string, schedule?: CronScheduleChange | null): Promise<ConfigurationPreview> { return (await client.post<ConfigurationPreview>('/api/backup-system/preview', { config, expectedVersion, ...(schedule ? { schedule } : {}) })).data }
export async function applyBackupSystem(config: BackupSystemConfig, expectedVersion: string, previewToken: string, schedule?: CronScheduleChange | null): Promise<BackupSystemState> { return (await client.post<BackupSystemState>('/api/backup-system/apply', { config, expectedVersion, previewToken, ...(schedule ? { schedule } : {}) })).data }
