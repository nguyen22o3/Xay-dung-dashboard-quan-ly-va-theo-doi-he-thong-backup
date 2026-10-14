import { client, clearSnapshotsCache } from './dashboard'
export type DriveConnectionStatus = { remotes: string[]; rcloneInstalled: boolean }
export type DriveConnectionInput = { remote: string; clientId: string; clientSecret: string; token: string }
export async function fetchDriveConnection(): Promise<DriveConnectionStatus> { return (await client.get<DriveConnectionStatus>('/api/backup-system/drive/status')).data }
export async function connectDrive(drive: DriveConnectionInput): Promise<DriveConnectionStatus> { return (await client.post<DriveConnectionStatus>('/api/backup-system/drive/connect', { drive })).data }
export type BackupSource = { path: string; scope: 'folder' | 'children' }
export type CronOptions = { backupRoot: string; driveRemote: string; driveFolder: string; source?: BackupSource }
export type CronDirectories = { scriptsDir: string; logsDir: string; options?: CronOptions }
export type BackupSystemConfig = { cronLocations?: Record<string, CronDirectories & { id: string; fileLayout?: 'files' }>; backupRoot: string; scriptsDir: string; logsDir: string; driveRemote: string; driveFolder: string; driveHistoryLog: string; siteSource?: BackupSource; databaseSource?: BackupSource; panelSource?: BackupSource }
export type ScheduleCycle = { type: 'daily' | 'days' | 'hourly' | 'hours' | 'minutes' | 'weekly' | 'monthly'; every?: number; weekday?: number; day?: number }
export type CronScheduleChange = { sourceScript?: { selector: string; hash: string }; moveBackups?: boolean; instanceKey?: string; directories?: CronDirectories; id: string; clock: string; expectedSchedule: string; expectedEnabled: boolean; cycle?: ScheduleCycle; create?: boolean }
export type ManagedScriptOption = { selector: string; path: string; hash: string }
export async function fetchManagedScripts(id: string): Promise<{ version: string; scripts: ManagedScriptOption[] }> { return (await client.post('/api/backup-system/scripts/options', { script: { id } })).data }
export type BackupSystemState = {
  supportsManagedScriptSelection?: boolean
  supportsFlatCronFiles?: boolean
  supportsCronBackupMigration?: boolean
  supportsCronIsolation?: boolean
  supportsCronDirectories?: boolean
  supportsMultipleSchedules?: boolean
  config: BackupSystemConfig; version: string; remotes: string[]; ready: boolean
  localRetentionDays: number; driveRetentionDays: number; allowedRoots: Record<string, string[]>
  applied?: boolean; recoveryDir?: string; warnings?: string[]
  sources?: Record<string, BackupSource>
}
export type ConfigurationPreview = {
  sourceScript?: ManagedScriptOption
  cronFiles?: { script: string; runner: string; log: string; flattening: boolean }
  backupMigration?: boolean
  cronDirectories?: CronDirectories
  token: string; changed: boolean; filesToMove: number; bytesToMove: number; daysToMove: number
  scriptsToUpdate: number; logsToMove: number; driveChanged: boolean; config: BackupSystemConfig; version: string
  scriptsToCreate?: string[]
  schedule?: { id: string; previous: string; proposed: string; enabled: boolean; changed: boolean }
  sources?: { id: string; previous: BackupSource; proposed: BackupSource }[]
}
export type FolderRequest = { location: 'local' | 'drive'; purpose: 'backup' | 'scripts' | 'logs' | 'source-site' | 'source-database' | 'source-panel'; path: string; remote?: string }
export type FolderList = { path: string; exists: boolean; parent: string | null; entries: { name: string; path: string }[] }
let storageEpoch = 0
let storageRead = 0
let lastStorageState: BackupSystemState | null = null
function announceStorage(state: BackupSystemState): void {
  storageEpoch += 1
  lastStorageState = state
  clearSnapshotsCache()
  window.dispatchEvent(new CustomEvent<BackupSystemState>('backup-storage-changed', { detail: state }))
  window.dispatchEvent(new Event('force-refresh'))
}
export async function fetchBackupSystem(): Promise<BackupSystemState> {
  const epoch = storageEpoch, request = ++storageRead
  const { data } = await client.get<BackupSystemState>('/api/backup-system')
  if ((epoch !== storageEpoch || request !== storageRead) && lastStorageState) return lastStorageState
  if (lastStorageState && lastStorageState.version !== data.version) announceStorage(data)
  else lastStorageState = data
  return data
}
export async function listBackupFolders(request: FolderRequest): Promise<FolderList> { return (await client.post<FolderList>('/api/backup-system/directories/list', request)).data }
export async function createBackupFolder(request: FolderRequest): Promise<void> { await client.post('/api/backup-system/directories/create', request) }
export async function previewBackupSystem(config: BackupSystemConfig, expectedVersion: string, schedule?: CronScheduleChange | null): Promise<ConfigurationPreview> { return (await client.post<ConfigurationPreview>('/api/backup-system/preview', { config, expectedVersion, ...(schedule ? { schedule } : {}) }, { timeout: 660000 })).data }
export async function applyBackupSystem(config: BackupSystemConfig, expectedVersion: string, previewToken: string, schedule?: CronScheduleChange | null): Promise<BackupSystemState> {
  const { data } = await client.post<BackupSystemState>('/api/backup-system/apply', { config, expectedVersion, previewToken, ...(schedule ? { schedule } : {}) }, { timeout: 660000 })
  announceStorage(data)
  return data
}
export type ScriptDocument = { id: string; path: string; content: string; hash: string; version: string; recoveryDir?: string }
export async function readTaskScript(id: string): Promise<ScriptDocument> { return (await client.post<ScriptDocument>('/api/backup-system/scripts/read', { script: { id } })).data }
export async function saveTaskScript(document: ScriptDocument, content: string): Promise<ScriptDocument> { return (await client.post<ScriptDocument>('/api/backup-system/scripts/save', { expectedVersion: document.version, script: { id: document.id, expectedHash: document.hash, content } })).data }
