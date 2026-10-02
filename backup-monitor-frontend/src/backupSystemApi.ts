import { client } from './api'
export type BackupSystemConfig = { backupRoot: string; scriptsDir: string; logsDir: string; driveRemote: string; driveFolder: string; driveHistoryLog: string }
export type BackupSystemState = {
  config: BackupSystemConfig; version: string; remotes: string[]; ready: boolean
  localRetentionDays: number; driveRetentionDays: number; allowedRoots: Record<string, string[]>
  applied?: boolean; recoveryDir?: string; warnings?: string[]
}
export type ConfigurationPreview = {
  token: string; changed: boolean; filesToMove: number; bytesToMove: number; daysToMove: number
  scriptsToUpdate: number; logsToMove: number; driveChanged: boolean; config: BackupSystemConfig; version: string
}
export type FolderRequest = { location: 'local' | 'drive'; purpose: 'backup' | 'scripts' | 'logs'; path: string; remote?: string }
export type FolderList = { path: string; exists: boolean; parent: string | null; entries: { name: string; path: string }[] }
export async function fetchBackupSystem(): Promise<BackupSystemState> { return (await client.get<BackupSystemState>('/api/backup-system')).data }
export async function listBackupFolders(request: FolderRequest): Promise<FolderList> { return (await client.post<FolderList>('/api/backup-system/directories/list', request)).data }
export async function createBackupFolder(request: FolderRequest): Promise<void> { await client.post('/api/backup-system/directories/create', request) }
export async function previewBackupSystem(config: BackupSystemConfig, expectedVersion: string): Promise<ConfigurationPreview> { return (await client.post<ConfigurationPreview>('/api/backup-system/preview', { config, expectedVersion })).data }
export async function applyBackupSystem(config: BackupSystemConfig, expectedVersion: string, previewToken: string): Promise<BackupSystemState> { return (await client.post<BackupSystemState>('/api/backup-system/apply', { config, expectedVersion, previewToken })).data }
