import { client } from './dashboard'

type MonitorOptions = { enabled: boolean; backupFailed: boolean; driveFailed: boolean; missedSchedule: boolean; diskThreshold: number; graceMinutes: number }
export type OperationsStatus = { enabled: boolean; workerEnabled?: boolean; storage?: string; message?: string; options?: MonitorOptions; collectedAt?: string | null; collectionStale?: boolean }
type AlertDelivery = { id: number; channel: string; status: string; attempts: number; lastError: string }
export type AlertEvent = { id: number; message: string; observedAt: string; kind?: 'alert' | 'test'; deliveries?: AlertDelivery[] }
export type TaskProgress = { phase: string; bytes: number; totalBytes: number; files: number; totalFiles: number; speed: number; eta: number | null; percent: number | null; observedAt: string; stale: boolean }
export type TaskProcess = { managed: boolean; running: boolean; runId?: string; cancelRequested: boolean; elapsedSeconds: number; progress?: TaskProgress }
type LogPage = { exists: boolean; content: string; olderCursor?: string; Start: number; End: number; Size: number; partialLine: boolean }
export type RestorePlan = { token: string; path: string; size: number; directory: string; status: string; error?: string; files: number; expandedBytes: number; createdAt: string; progress?: TaskProgress }
export async function operationsStatus() { return (await client.get<OperationsStatus>('/api/operations/status')).data }
export async function saveMonitorOptions(options: MonitorOptions) { return (await client.put<MonitorOptions>('/api/operations/options', options)).data }
export async function fetchAlerts() { return (await client.get<{ entries: AlertEvent[] }>('/api/operations/alerts')).data.entries }
export async function sendTestNotification(channel: string) { return (await client.post('/api/notifications/test', { channel })).data }
export async function retryNotification(id: number) { return (await client.post(`/api/notifications/${id}/retry`)).data }
export async function taskProcess(id: string) { return (await client.get<TaskProcess>(`/api/cron-jobs/${encodeURIComponent(id)}/process`)).data }
export async function cancelTask(id: string, runId: string) { return (await client.post<TaskProcess>(`/api/cron-jobs/${encodeURIComponent(id)}/cancel`, { runId, confirm: true })).data }
export async function readLogPage(id: string, cursor = '') { return (await client.get<LogPage>(`/api/cron-jobs/${encodeURIComponent(id)}/log-page`, { params: { cursor } })).data }
export async function previewRestore(path: string) { return (await client.post<RestorePlan>('/api/restore/preview', { path })).data }
export async function startRestore(token: string) { return (await client.post<RestorePlan>('/api/restore/start', { token, confirm: true })).data }
export async function restoreStatus(token: string) { return (await client.get<RestorePlan>(`/api/restore/${encodeURIComponent(token)}`)).data }
