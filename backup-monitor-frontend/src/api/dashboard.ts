import { useCallback, useSyncExternalStore } from 'react'
import axios from 'axios'
import type { Lang } from '../shared/language'
import { localizeMessage, savedLanguage } from '../shared/messages'
import type {
  AlertSettings,
  AlertSettingsUpdate,
  BackupStatus,
  CronJob,
  CronJobLog,
  LoginResponse,
  ServerStatus,
  SnapshotFile,
  WebsiteStatus,
} from '../shared/types'

// An explicitly empty value selects a same-origin reverse proxy.
// Unconfigured Vite development keeps its existing local backend default.
const API_BASE: string = import.meta.env.VITE_API_BASE === ''
  ? window.location.origin
  : (import.meta.env.VITE_API_BASE || 'http://localhost:8080')

export const client = axios.create({ baseURL: API_BASE, timeout: 200000 })

type SessionData<T> = { token: string; data: T }
let serverStatusCache: SessionData<ServerStatus> | null = null
let backupStatusCache: SessionData<BackupStatus> | null = null
let dashboardCacheEpoch = 0
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

export function apiErrorMessage(error: unknown, fallback: string, lang: Lang = savedLanguage()): string {
  if (axios.isAxiosError<{ error?: string }>(error)) {
    return localizeMessage(error.response?.data?.error || error.message || fallback, lang)
  }
  return localizeMessage(error instanceof Error ? error.message : fallback, lang)
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
    const requestHeaders = error.config?.headers
    const authorization = requestHeaders?.get?.('Authorization') ?? requestHeaders?.Authorization
    if (error.response?.status === 401 && token && authorization === `Bearer ${token}`) {
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

export async function fetchAuthStatus(): Promise<{ setupRequired: boolean }> {
  const { data } = await client.get<{ setupRequired: boolean }>('/api/auth/status')
  if (typeof data.setupRequired !== 'boolean') throw new Error('Máy chủ trả trạng thái tài khoản không hợp lệ')
  return data
}

export async function createAdministrator(username: string, password: string): Promise<void> {
  await client.post('/api/auth/setup', { username, password })
}

async function fetchServerStatus(signal?: AbortSignal): Promise<ServerStatus> {
	const epoch = dashboardCacheEpoch
  const token = localStorage.getItem('auth_token')
  const { data } = await client.get<ServerStatus>('/api/server-status', { signal })
  if (epoch === dashboardCacheEpoch && token && token === localStorage.getItem('auth_token')) serverStatusCache = { token, data }
  return data
}

async function fetchBackupStatus(signal?: AbortSignal): Promise<BackupStatus> {
	const epoch = dashboardCacheEpoch
  const token = localStorage.getItem('auth_token')
  const { data } = await client.get<BackupStatus>('/api/backup-status', { signal })
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
  if (epoch === dashboardCacheEpoch && token && token === localStorage.getItem('auth_token')) {
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

async function fetchWebsitesStatus(signal?: AbortSignal): Promise<WebsiteStatus[]> {
  const { data } = await client.get<WebsiteStatus[]>('/api/websites-status', { signal })
  return data
}

export async function fetchCronJobs(signal?: AbortSignal): Promise<CronJob[]> {
  const { data } = await client.get<CronJob[]>('/api/cron-jobs', { signal })
  return data
}

export async function enableCronTracking(): Promise<void> {
  await client.post('/api/cron-tracking')
  invalidatePollResources('server', 'cron')
}

type CronJobRunResult = { status: 'started' | 'completed'; deletedCount?: number }

export async function runCronJob(jobId: string): Promise<CronJobRunResult> {
  const { data } = await client.post<CronJobRunResult>('/api/run-cron-job', { jobId })
  invalidatePollResources('server', 'cron')
  return data
}

export async function fetchCronJobLog(jobId: string): Promise<CronJobLog> {
  const { data } = await client.get<CronJobLog>(`/api/cron-jobs/${encodeURIComponent(jobId)}/log`)
  return data
}

export async function deleteCronJob(jobId: string, expectedSchedule: string, expectedEnabled: boolean): Promise<{ status: 'deleted'; backupPath: string }> {
  const { data } = await client.delete<{ status: 'deleted'; backupPath: string }>(`/api/cron-jobs/${encodeURIComponent(jobId)}`, { data: { expectedSchedule, expectedEnabled } })
  invalidatePollResources('server', 'cron')
  return data
}

export async function updateCronJobState(jobId: string, enabled: boolean, expectedSchedule: string, expectedEnabled: boolean): Promise<{ enabled: boolean; backupPath: string }> {
  const { data } = await client.put<{ enabled: boolean; backupPath: string }>(`/api/cron-jobs/${encodeURIComponent(jobId)}/state`, { enabled, expectedSchedule, expectedEnabled })
  invalidatePollResources('server', 'cron')
  return data
}

let snapshotCache: { token: string; data: SnapshotFile[] } | null = null

function cachedSnapshots(): SnapshotFile[] | null {
  const token = localStorage.getItem('auth_token')
  return token && snapshotCache?.token === token ? snapshotCache.data : null
}

async function fetchSnapshots(signal?: AbortSignal): Promise<SnapshotFile[]> {
	const epoch = dashboardCacheEpoch
  const token = localStorage.getItem('auth_token')
  const { data } = await client.get<SnapshotFile[]>('/api/recovery-snapshots', { signal })
  if (epoch === dashboardCacheEpoch && token && token === localStorage.getItem('auth_token')) {
    snapshotCache = { token, data }
  }
  return data
}

export function clearSnapshotsCache(): void {
  dashboardCacheEpoch += 1
  snapshotCache = null
  serverStatusCache = null
  backupStatusCache = null
  healthyBackupCache = null
  try {
    sessionStorage.removeItem(HEALTHY_BACKUP_KEY)
  } catch {
    // Storage may be disabled by the browser.
  }
  Object.values(pollResources).forEach(resource => resource.reset())
}

async function fetchLocalSnapshots(signal?: AbortSignal): Promise<SnapshotFile[]> {
  const { data } = await client.get<SnapshotFile[]>('/api/local-snapshots', { signal })
  return data
}

export async function clearLog(target: 'server' | 'drive', entries: string[] = []): Promise<void> {
  await client.post('/api/clear-log', { target, entries })
  invalidatePollResources('backup')
}

export async function refreshData(): Promise<void> {
  await client.post('/api/refresh')
  invalidatePollResources()
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

interface PollState<T> {
  data: T | null
  error: string | null
  loading: boolean
  reload: () => void
  updateData: (updater: (current: T | null) => T | null) => void
}

type PollSnapshot<T> = Pick<PollState<T>, 'data' | 'error' | 'loading'>
type PollResource<T> = {
  getSnapshot: () => PollSnapshot<T>
  subscribe: (listener: () => void, interval: number) => () => void
  reload: () => void
  updateData: PollState<T>['updateData']
  reset: () => void
  pause: () => void
}

function pageHidden(): boolean {
  return typeof document !== 'undefined' && document.visibilityState === 'hidden'
}

// Layout and the active page share one request, cache and timer per resource.
// Schedule after completion so a slow SSH/Drive read cannot overlap its next poll.
function createPollResource<T>(fetcher: (signal?: AbortSignal) => Promise<T>, maximumInterval: number, initialData: () => T | null = () => null): PollResource<T> {
  const subscribers = new Map<() => void, number>()
  let token: string | null | undefined
  let state: PollSnapshot<T> = { data: null, error: null, loading: true }
  let timer: ReturnType<typeof setTimeout> | undefined
  let revision = 0
  let inFlight: { controller: AbortController; revision: number } | null = null
  let refreshQueued = false

  const clearTimer = () => {
    if (timer !== undefined) clearTimeout(timer)
    timer = undefined
  }
  const publish = (next: PollSnapshot<T>) => {
    state = next
    subscribers.forEach((_interval, listener) => listener())
  }
  const interval = () => {
    const active = [...subscribers.values()].filter(value => value > 0)
    return active.length > 0 ? Math.min(maximumInterval, ...active) : 0
  }
  const synchronizeSession = () => {
    const currentToken = localStorage.getItem('auth_token')
    if (currentToken === token) return
    token = currentToken
    revision += 1
    refreshQueued = false
    inFlight?.controller.abort()
    clearTimer()
    state = { data: initialData(), error: null, loading: Boolean(token) }
  }
  const schedule = (delay: number) => {
    clearTimer()
    if (subscribers.size > 0 && !pageHidden() && token) timer = setTimeout(() => void load(), delay)
  }
  const load = async () => {
    timer = undefined
    synchronizeSession()
    if (subscribers.size === 0 || pageHidden() || !token) return
    if (inFlight) {
      refreshQueued = true
      return
    }
    const request = { controller: new AbortController(), revision }
    const requestToken = token
    inFlight = request
    publish({ ...state, loading: true })
    const current = () => request.revision === revision && requestToken === localStorage.getItem('auth_token')
    try {
      const data = await fetcher(request.controller.signal)
      if (current()) publish({ data, error: null, loading: false })
    } catch (error) {
      if (current() && !request.controller.signal.aborted) publish({ ...state, error: apiErrorMessage(error, 'Lỗi kết nối'), loading: false })
    } finally {
      if (inFlight === request) {
        inFlight = null
        if (refreshQueued) {
          refreshQueued = false
          schedule(0)
        } else {
          const delay = interval()
          if (delay > 0) schedule(delay)
        }
      }
    }
  }
  const reload = () => {
    synchronizeSession()
    revision += 1
    clearTimer()
    if (inFlight) {
      refreshQueued = true
      inFlight.controller.abort()
    } else schedule(0)
  }
  return {
    getSnapshot: () => { synchronizeSession(); return state },
    subscribe: (listener, requestedInterval) => {
      synchronizeSession()
      const wasInactive = subscribers.size === 0
      subscribers.set(listener, requestedInterval)
      installPollListeners()
      if (wasInactive) {
        if (inFlight) refreshQueued = true
        else schedule(0)
      }
      return () => {
        subscribers.delete(listener)
        if (subscribers.size === 0) {
          clearTimer()
          refreshQueued = false
          revision += 1
          inFlight?.controller.abort()
        }
      }
    },
    reload,
    updateData: updater => {
      synchronizeSession()
      revision += 1
      inFlight?.controller.abort()
      publish({ ...state, data: updater(state.data), loading: false })
    },
    reset: () => {
      synchronizeSession()
      publish({ data: initialData(), error: null, loading: Boolean(token) })
      reload()
    },
    pause: clearTimer,
  }
}

const pollResources = {
  server: createPollResource(fetchServerStatus, 3000, () => cachedSessionData(serverStatusCache)),
  backup: createPollResource(fetchBackupStatus, 5000, cachedBackupStatus),
  websites: createPollResource(fetchWebsitesStatus, 15000),
  cron: createPollResource(fetchCronJobs, 3000),
  snapshots: createPollResource(fetchSnapshots, 5000, cachedSnapshots),
  localSnapshots: createPollResource(fetchLocalSnapshots, 3000),
}

function invalidatePollResources(...keys: (keyof typeof pollResources)[]): void {
  dashboardCacheEpoch += 1
  const resources = keys.length > 0 ? keys.map(key => pollResources[key]) : Object.values(pollResources)
  resources.forEach(resource => resource.reload())
}

let pollListenersInstalled = false
function installPollListeners(): void {
  if (pollListenersInstalled || typeof window === 'undefined') return
  pollListenersInstalled = true
  let lastResume = -Infinity
  const resume = () => {
    if (pageHidden() || Date.now() - lastResume < 1000) return
    lastResume = Date.now()
    invalidatePollResources()
  }
  window.addEventListener('force-refresh', () => invalidatePollResources())
  window.addEventListener('focus', resume)
  if (typeof document !== 'undefined') document.addEventListener('visibilitychange', () => {
    if (pageHidden()) {
      lastResume = -Infinity
      Object.values(pollResources).forEach(resource => resource.pause())
    }
    else resume()
  })
}

function usePoll<T>(resource: PollResource<T>, intervalMs: number): PollState<T> {
  const subscribe = useCallback((listener: () => void) => resource.subscribe(listener, intervalMs), [resource, intervalMs])
  const state = useSyncExternalStore(subscribe, resource.getSnapshot)
  return { ...state, error: state.error ? localizeMessage(state.error, savedLanguage()) : null, reload: resource.reload, updateData: resource.updateData }
}

export function useServerStatus(interval = 30000): PollState<ServerStatus> {
  return usePoll(pollResources.server, interval)
}

export function useBackupStatus(interval = 30000): PollState<BackupStatus> & { fetchError: string | null } {
  const state = usePoll(pollResources.backup, interval)
  return {
    ...state,
    fetchError: state.error,
    data: state.error && state.data ? { ...state.data, driveStale: true } : state.data,
    error: state.error ?? (state.data?.driveError ? localizeMessage(state.data.driveError, savedLanguage()) : null),
  }
}

export function useWebsitesStatus(interval = 30000): PollState<WebsiteStatus[]> {
  return usePoll(pollResources.websites, interval)
}

export function useCronJobs(interval = 20000): PollState<CronJob[]> {
  return usePoll(pollResources.cron, interval)
}

export function useSnapshots(interval = 30000): PollState<SnapshotFile[]> {
  return usePoll(pollResources.snapshots, interval)
}

export function useLocalSnapshots(interval = 30000): PollState<SnapshotFile[]> {
  return usePoll(pollResources.localSnapshots, interval)
}
