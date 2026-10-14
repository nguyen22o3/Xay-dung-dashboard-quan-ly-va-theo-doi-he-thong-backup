import { client } from './dashboard'

export type TaskProgress = { phase: string; bytes: number; totalBytes: number; files: number; totalFiles: number; speed: number; eta: number | null; percent: number | null; observedAt: string; stale: boolean }
export type TaskProcess = { managed: boolean; running: boolean; runId?: string; cancelRequested: boolean; elapsedSeconds: number; progress?: TaskProgress }
type LogPage = { exists: boolean; content: string; olderCursor?: string; Start: number; End: number; Size: number; partialLine: boolean }
export type RestorePlan = { token: string; path: string; size: number; directory: string; parent?: string; name?: string; status: string; error?: string; files: number; expandedBytes: number; createdAt: string; progress?: TaskProgress }
export async function taskProcess(id: string) { return (await client.get<TaskProcess>(`/api/cron-jobs/${encodeURIComponent(id)}/process`)).data }
export async function cancelTask(id: string, runId: string) { return (await client.post<TaskProcess>(`/api/cron-jobs/${encodeURIComponent(id)}/cancel`, { runId, confirm: true })).data }
export async function readLogPage(id: string, cursor = '') { return (await client.get<LogPage>(`/api/cron-jobs/${encodeURIComponent(id)}/log-page`, { params: { cursor } })).data }
export async function previewRestore(path: string, parent: string, name: string) { return (await client.post<RestorePlan>('/api/restore/preview', { path, parent, name })).data }
export async function startRestore(token: string) { return (await client.post<RestorePlan>('/api/restore/start', { token, confirm: true })).data }
export async function restoreStatus(token: string) { return (await client.get<RestorePlan>(`/api/restore/${encodeURIComponent(token)}`)).data }
