import { useCallback, useEffect, useState } from 'react'
import axios from 'axios'
import type {
  AlertSettings,
  AlertSettingsUpdate,
  AppConfig,
  BackupJobId,
  BackupStatus,
  CronJob,
  LoginResponse,
  ServerStatus,
  SnapshotFile,
  WebsiteStatus,
} from './types'

export const API_BASE: string = import.meta.env.VITE_API_BASE || 'http://localhost:8080'

export const client = axios.create({ baseURL: API_BASE, timeout: 200000 })

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
  const { data } = await client.get<ServerStatus>('/api/server-status')
  return data
}

export async function fetchBackupStatus(): Promise<BackupStatus> {
  const { data } = await client.get<BackupStatus>('/api/backup-status')
  return data
}

export async function fetchWebsitesStatus(): Promise<WebsiteStatus[]> {
  const { data } = await client.get<WebsiteStatus[]>('/api/websites-status')
  return data
}

export async function fetchCronJobs(): Promise<CronJob[]> {
  const { data } = await client.get<CronJob[]>('/api/cron-jobs')
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

export function prefetchSnapshots(): void {
  if (!cachedSnapshots()) void fetchSnapshots().catch(() => {})
}

export function clearSnapshotsCache(): void {
  snapshotCache = null
  snapshotInFlight = null
}

export async function fetchLocalSnapshots(): Promise<SnapshotFile[]> {
  const { data } = await client.get<SnapshotFile[]>('/api/local-snapshots')
  return data
}

export async function fetchLogs(type: 'backup' | 'system' | 'secure'): Promise<string> {
  const { data } = await client.get<string>('/api/logs', { params: { type } })
  return data
}

export async function clearLog(target: 'server' | 'drive'): Promise<void> {
  await client.post('/api/clear-log', { target })
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

  return { data, error, loading, reload }
}

export function useServerStatus(interval = 30000): PollState<ServerStatus> {
  return usePoll(fetchServerStatus, interval)
}

export function useBackupStatus(interval = 30000): PollState<BackupStatus> {
  return usePoll(fetchBackupStatus, interval)
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
