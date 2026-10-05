import { useCallback, useEffect, useState } from 'react'
import axios from 'axios'
import type {
  AlertSettings,
  AlertSettingsUpdate,
  AppConfig,
  BackupJobId,
  BackupStatus,
  CronJob,
  CronJobLog,
  LoginResponse,
  ServerStatus,
  SnapshotFile,
  WebsiteStatus,
} from './types'

export const API_BASE: string = import.meta.env.VITE_API_BASE || 'http://localhost:8080'

export const client = axios.create({ baseURL: API_BASE, timeout: 200000 })

type SessionData<T> = { token: string; data: T }
let serverStatusCache: SessionData<ServerStatus> | null = null
let backupStatusCache: SessionData<BackupStatus> | null = null
type HealthyBackup = SessionData<BackupStatus> & { fetchedAt: string }
const HEALTHY_BACKUP_KEY = 'backup_status_last_healthy_v1'
let healthyBackupCache: HealthyBackup | null = null

function lastHealthyBackup(): HealthyBackup | null {
  const token = localStorage.getItem('auth_token')
  if (!token) return null
  if (healthyBackupCache?.token === token) return healthyBackupCache
  try {
    const saved = sessionStorage.getItem(HEALTHY_BACKUP_KEY)
    if (!saved) return null
    const parsed = JSON.parse(saved) as HealthyBackup
    if (parsed.token !== token || !parsed.fetchedAt || !parsed.data?.size || !Array.isArray(parsed.data.history)) return null
    healthyBackupCache = parsed
    return parsed
  } catch {
    return null
  }
}

function cachedBackupStatus(): BackupStatus | null {
  const cached = cachedSessionData(backupStatusCache)
  if (cached) return cached
  const lastGood = lastHealthyBackup()
  return lastGood ? { ...lastGood.data, driveStale: true, driveDataAt: lastGood.fetchedAt } : null
}

function cachedSessionData<T>(entry: SessionData<T> | null): T | null {
  const token = localStorage.getItem('auth_token')
  return token && entry?.token === token ? entry.data : null
}

export function apiErrorMessage(error: unknown, fallback: string): string {
  if (axios.isAxiosError<{ error?: string }>(error)) {
    return error.response?.data?.error || error.message || fallback
  }
  return error instanceof Error ? error.message : fallback
}

// Tự động đính kèm token vào mọi request
client.interceptors.request.use((config) => {
  const token = localStorage.getItem('auth_token')
  if (token) {
    config.headers.Authorization = `Bearer ${token}`
  }
  return config
})

// Nếu token hết hạn hoặc không hợp lệ, tự động đăng xuất
client.interceptors.response.use(
  (res) => res,
  (error) => {
    const token = localStorage.getItem('auth_token')
    if (error.response?.status === 401 && token) {
      clearSnapshotsCache()
      localStorage.removeItem('auth_token')
      window.location.reload()
    }
    return Promise.reject(error)
  }
)

export async function login(username: string, password: string): Promise<string> {
  const { data } = await client.post<LoginResponse>('/api/login', { username, password })
  return data.token
}

export async function fetchServerStatus(): Promise<ServerStatus> {
  const token = localStorage.getItem('auth_token')
  const { data } = await client.get<ServerStatus>('/api/server-status')
  if (token && token === localStorage.getItem('auth_token')) serverStatusCache = { token, data }
  return data
}

export async function fetchBackupStatus(): Promise<BackupStatus> {
  const token = localStorage.getItem('auth_token')
  const { data } = await client.get<BackupStatus>('/api/backup-status')
  const lastGood = lastHealthyBackup()
  const fetchedAt = new Date().toISOString()
  const backendDataTime = Date.parse(data.driveDataAt || '')
  const useBrowserSnapshot = Boolean(data.driveError && lastGood &&
    (!data.driveStale || !Number.isFinite(backendDataTime) || Date.parse(lastGood.fetchedAt) > backendDataTime))
  const result: BackupStatus = useBrowserSnapshot && lastGood
    ? {
        ...data,
        about: lastGood.data.about,
        size: lastGood.data.size,
        dirs: lastGood.data.dirs,
        totalFolders: lastGood.data.totalFolders,
        history: lastGood.data.history,
        todayBreakdownBytes: lastGood.data.todayBreakdownBytes,
        driveStale: true,
        driveDataAt: lastGood.fetchedAt,
      }
    : data.driveStale
      ? { ...data, driveStale: true }
      : { ...data, driveStale: false, driveDataAt: data.driveError ? undefined : (data.driveDataAt || fetchedAt) }
  if (token && token === localStorage.getItem('auth_token')) {
    backupStatusCache = { token, data: result }
    if (!data.driveError && !data.driveStale && data.size && Array.isArray(data.history)) {
      healthyBackupCache = { token, data: result, fetchedAt: result.driveDataAt || fetchedAt }
      try {
        sessionStorage.setItem(HEALTHY_BACKUP_KEY, JSON.stringify(healthyBackupCache))
      } catch {
        // The in-memory snapshot still works when session storage is unavailable.
      }
    }
  }
  return result
}

export async function fetchWebsitesStatus(): Promise<WebsiteStatus[]> {
  const { data } = await client.get<WebsiteStatus[]>('/api/websites-status')
  return data
}

export async function fetchCronJobs(): Promise<CronJob[]> {
  const { data } = await client.get<CronJob[]>('/api/cron-jobs')
  return data
}

export async function enableCronTracking(): Promise<void> {
  await client.post('/api/cron-tracking')
}

export type CronJobRunResult = { status: 'started' | 'completed'; deletedCount?: number }

export async function runCronJob(jobId: string): Promise<CronJobRunResult> {
  const { data } = await client.post<CronJobRunResult>('/api/run-cron-job', { jobId })
  return data
}

export async function fetchCronJobLog(jobId: string): Promise<CronJobLog> {
  const { data } = await client.get<CronJobLog>(`/api/cron-jobs/${encodeURIComponent(jobId)}/log`)
  return data
}

export async function updateCronJobSchedule(jobId: string, time: string, expectedSchedule: string, expectedEnabled: boolean): Promise<void> {
  await client.put(`/api/cron-jobs/${encodeURIComponent(jobId)}/schedule`, { time, expectedSchedule, expectedEnabled })
}

export async function deleteCronJob(jobId: string, expectedSchedule: string, expectedEnabled: boolean): Promise<{ status: 'deleted'; backupPath: string }> {
  const { data } = await client.delete<{ status: 'deleted'; backupPath: string }>(`/api/cron-jobs/${encodeURIComponent(jobId)}`, { data: { expectedSchedule, expectedEnabled } })
  return data
}

export async function updateCronJobState(jobId: string, enabled: boolean, expectedSchedule: string, expectedEnabled: boolean): Promise<{ enabled: boolean; backupPath: string }> {
  const { data } = await client.put<{ enabled: boolean; backupPath: string }>(`/api/cron-jobs/${encodeURIComponent(jobId)}/state`, { enabled, expectedSchedule, expectedEnabled })
  return data
}

export async function fetchConfig(): Promise<AppConfig> {
  const { data } = await client.get<AppConfig>('/api/config')
  return data
}

export async function saveConfig(config: AppConfig): Promise<AppConfig> {
  const { data } = await client.put<AppConfig>('/api/config', config)
  return data
}

export async function runJobNow(jobId: BackupJobId): Promise<void> {
  await client.post('/api/run-job', { jobId })
}

let snapshotCache: { token: string; data: SnapshotFile[] } | null = null
let snapshotInFlight: { token: string; promise: Promise<SnapshotFile[]> } | null = null

function cachedSnapshots(): SnapshotFile[] | null {
  const token = localStorage.getItem('auth_token')
  return token && snapshotCache?.token === token ? snapshotCache.data : null
}

export async function fetchSnapshots(): Promise<SnapshotFile[]> {
  const token = localStorage.getItem('auth_token')
  if (token && snapshotInFlight?.token === token) {
    return snapshotInFlight.promise
  }
  const request = client.get<SnapshotFile[]>('/api/recovery-snapshots').then(({ data }) => {
    if (token && token === localStorage.getItem('auth_token')) {
      snapshotCache = { token, data }
    }
    return data
  })
  if (token) snapshotInFlight = { token, promise: request }
  try {
    return await request
  } finally {
    if (snapshotInFlight?.promise === request) snapshotInFlight = null
  }
}

export function clearSnapshotsCache(): void {
  snapshotCache = null
  snapshotInFlight = null
  serverStatusCache = null
  backupStatusCache = null
  healthyBackupCache = null
  try {
    sessionStorage.removeItem(HEALTHY_BACKUP_KEY)
  } catch {
    // Storage may be disabled by the browser.
  }
}

export async function fetchLocalSnapshots(): Promise<SnapshotFile[]> {
  const { data } = await client.get<SnapshotFile[]>('/api/local-snapshots')
  return data
}

export async function fetchLogs(type: 'backup' | 'system' | 'secure'): Promise<string> {
  const { data } = await client.get<string>('/api/logs', { params: { type } })
  return data
}

export async function clearLog(target: 'server' | 'drive', entries: string[] = []): Promise<void> {
  await client.post('/api/clear-log', { target, entries })
}

export async function refreshData(): Promise<void> {
  await client.post('/api/refresh')
}

export async function fetchAlertSettings(): Promise<AlertSettings> {
  const { data } = await client.get<AlertSettings>('/api/alert-settings')
  return data
}

export async function saveAlertSettings(settings: AlertSettingsUpdate): Promise<AlertSettings> {
  const { data } = await client.post<AlertSettings>('/api/alert-settings', settings)
  return data
}

export async function downloadSnapshot(snapshot: SnapshotFile): Promise<void> {
  const path = snapshot.path || (snapshot.category === 'root'
    ? [snapshot.date, snapshot.name].join('/')
    : [snapshot.date, snapshot.category, snapshot.name].join('/'))
  const token = localStorage.getItem('auth_token')
  const createDirectUrl = async (): Promise<URL> => {
    const { data } = await client.post<{ ticket: string }>('/api/download-ticket', { path })
    const url = new URL('/api/download-snapshot', API_BASE)
    url.searchParams.set('ticket', data.ticket)
    return url
  }

  // Ask for the destination before downloading, then stream directly to disk
  // when the browser supports the File System Access API. This avoids buffering
  // large backups in memory and makes the save dialog appear immediately.
  const picker = (window as Window & {
    showSaveFilePicker?: (options?: {
      suggestedName?: string
    }) => Promise<{ createWritable: () => Promise<WritableStream<Uint8Array>> }>
  }).showSaveFilePicker
  if (picker && typeof window.fetch === 'function') {
    const handle = await picker({ suggestedName: snapshot.name })
    const directUrl = await createDirectUrl()
    const response = await fetch(directUrl, { headers: token ? { Authorization: `Bearer ${token}` } : undefined })
    if (!response.ok || !response.body) throw new Error(`HTTP ${response.status}`)
    const writable = await handle.createWritable()
    await response.body.pipeTo(writable)
    return
  }

  const directUrl = await createDirectUrl()
  const anchor = document.createElement('a')
  anchor.href = directUrl.toString()
  anchor.download = snapshot.name
  document.body.appendChild(anchor)
  anchor.click()
  anchor.remove()
}

export interface PollState<T> {
  data: T | null
  error: string | null
  loading: boolean
  reload: () => void
  updateData: (updater: (current: T | null) => T | null) => void
}

function usePoll<T>(fetcher: () => Promise<T>, intervalMs: number, initialData: () => T | null = () => null): PollState<T> {
  const [data, setData] = useState<T | null>(initialData)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(true)
  const [tick, setTick] = useState(0)

  const reload = useCallback(() => setTick((t) => t + 1), [])

  useEffect(() => {
    const handleForceReload = () => setTick((t) => t + 1)
    window.addEventListener('force-refresh', handleForceReload)
    return () => window.removeEventListener('force-refresh', handleForceReload)
  }, [])

  useEffect(() => {
    let alive = true
    const load = async () => {
      if (alive) setLoading(true)
      try {
        const d = await fetcher()
        if (alive) {
          setData(d)
          setError(null)
        }
      } catch (e) {
        if (alive) {
          setError(apiErrorMessage(e, 'Lỗi kết nối'))
        }
      } finally {
        if (alive) setLoading(false)
      }
    }
    load()
    if (intervalMs > 0) {
      const id = setInterval(load, intervalMs)
      return () => {
        alive = false
        clearInterval(id)
      }
    }
    return () => {
      alive = false
    }
  }, [fetcher, intervalMs, tick])

  return { data, error, loading, reload, updateData: setData }
}

export function useServerStatus(interval = 30000): PollState<ServerStatus> {
  return usePoll(fetchServerStatus, interval, () => cachedSessionData(serverStatusCache))
}

export function useBackupStatus(interval = 30000): PollState<BackupStatus> {
  const state = usePoll(fetchBackupStatus, interval, cachedBackupStatus)
  return {
    ...state,
    data: state.error && state.data ? { ...state.data, driveStale: true } : state.data,
    error: state.error ?? state.data?.driveError ?? null,
  }
}

export function useWebsitesStatus(interval = 30000): PollState<WebsiteStatus[]> {
  return usePoll(fetchWebsitesStatus, interval)
}

export function useCronJobs(interval = 20000): PollState<CronJob[]> {
  return usePoll(fetchCronJobs, interval)
}

export function useConfig(interval = 15000): PollState<AppConfig> {
  return usePoll(fetchConfig, interval)
}

export function useSnapshots(interval = 30000): PollState<SnapshotFile[]> {
  return usePoll(fetchSnapshots, interval, cachedSnapshots)
}

export function useLocalSnapshots(interval = 30000): PollState<SnapshotFile[]> {
  return usePoll(fetchLocalSnapshots, interval)
}
