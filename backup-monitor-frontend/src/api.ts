import { useCallback, useEffect, useState } from 'react'
import axios from 'axios'
import type {
  AppConfig,
  BackupStatus,
  CronJob,
  ServerStatus,
  SnapshotFile,
  WebsiteStatus,
} from './types'

export const API_BASE: string = import.meta.env.VITE_API_BASE || 'http://localhost:8080'

export const client = axios.create({ baseURL: API_BASE, timeout: 200000 })

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
    if (error.response?.status === 401) {
      localStorage.removeItem('auth_token')
      window.location.reload()
    }
    return Promise.reject(error)
  }
)

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

export async function runJobNow(script?: string): Promise<void> {
  if (script) {
    await client.post('/api/run-job', { script })
  } else {
    await client.post('/api/run-job')
  }
}

export async function fetchSnapshots(): Promise<SnapshotFile[]> {
  const { data } = await client.get<SnapshotFile[]>('/api/recovery-snapshots')
  return data
}

export async function fetchLocalSnapshots(): Promise<SnapshotFile[]> {
  const { data } = await client.get<SnapshotFile[]>('/api/local-snapshots')
  return data
}

export async function fetchLogs(type: 'backup' | 'system' | 'secure'): Promise<string> {
  const { data } = await client.get<string>('/api/logs', { params: { type } })
  return data
}

export function downloadUrl(path: string): string {
  return `${API_BASE}/api/download-snapshot?path=${encodeURIComponent(path)}`
}

export interface PollState<T> {
  data: T | null
  error: string | null
  loading: boolean
  reload: () => void
}

function usePoll<T>(fetcher: () => Promise<T>, intervalMs: number): PollState<T> {
  const [data, setData] = useState<T | null>(null)
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
    setLoading(true) // Trigger loading state on mount or manual refresh
    const load = async () => {
      try {
        const d = await fetcher()
        if (alive) {
          setData(d)
          setError(null)
        }
      } catch (e) {
        if (alive) {
          const status = (e as { response?: { status?: number } }).response?.status
          setError(status ? `HTTP ${status}` : (e as Error).message || 'Lỗi kết nối')
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
  return usePoll(fetchSnapshots, interval)
}

export function useLocalSnapshots(interval = 30000): PollState<SnapshotFile[]> {
  return usePoll(fetchLocalSnapshots, interval)
}