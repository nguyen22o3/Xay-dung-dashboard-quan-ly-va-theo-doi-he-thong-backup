import { localizeMessage } from '../../shared/messages'
import { useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { ChevronDown, Folder, FolderPlus, RefreshCw, X } from 'lucide-react'
import { apiErrorMessage } from '../../api/dashboard'
import { applyBackupSystem, createBackupFolder, fetchBackupSystem, fetchManagedScripts, listBackupFolders, previewBackupSystem } from '../../api/backupSystem'
import type { BackupSource, BackupSystemConfig, BackupSystemState, ConfigurationPreview, CronDirectories, FolderList, FolderRequest, ManagedScriptOption } from '../../api/backupSystem'
import type { Lang } from '../../shared/language'
import type { CronJob } from '../../shared/types'
import BackupTaskForm from './BackupTaskForm'
import ManagedScriptPicker from './ManagedScriptPicker'
import { backupSourceForTask, backupTaskLabels, buildCronScheduleChange, cronScheduleIsStale, makeCronScheduleDraft, sourceConfigKeys, proposedCronSchedule, cronJobSelector, cronExecutionScript } from './backupTasks'
import type { CronScheduleDraft } from './backupTasks'
import { formatCronSchedule } from '../../shared/utils'

type DirectoryKey = 'backupRoot' | 'scriptsDir' | 'logsDir' | 'driveFolder'
type SourceKey = 'siteSource' | 'databaseSource' | 'panelSource'
const labels: Record<DirectoryKey, [string, string]> = {
  backupRoot: ['Thư mục backup trên AlmaLinux', 'AlmaLinux backup directory'], scriptsDir: ['Thư mục scripts', 'Scripts directory'],
  logsDir: ['Thư mục nhật ký (log)', 'Log directory'], driveFolder: ['Thư mục backup trên Google Drive', 'Google Drive backup folder'],
}

export function FolderPicker({ request, vi, close, choose, existingOnly = false, allowCreate = true }: { request: FolderRequest; vi: boolean; close: () => void; choose: (path: string) => void; existingOnly?: boolean; allowCreate?: boolean }) {
	const sourceOnly = request.purpose.startsWith('source-') || existingOnly
  const canCreate = !sourceOnly && allowCreate
  const [path, setPath] = useState(request.path), [name, setName] = useState('')
  const [listing, setListing] = useState<FolderList | null>(null)
  const [busy, setBusy] = useState(true), [error, setError] = useState('')
  const dialog = useRef<HTMLDivElement>(null), sequence = useRef(0), closeRef = useRef(close)
  useEffect(() => { closeRef.current = close }, [close])
  const load = async (value: string) => {
    const id = ++sequence.current
    setBusy(true); setError(''); setPath(value); setListing(null)
    try { const result = await listBackupFolders({ ...request, path: value }); if (sequence.current === id) setListing(result) }
    catch (error: unknown) { if (sequence.current === id) setError(apiErrorMessage(error, vi ? 'Không đọc được thư mục.' : 'Could not list folders.')) }
    finally { if (sequence.current === id) setBusy(false) }
  }
  useEffect(() => {
    const restore = document.activeElement as HTMLElement | null
    const scrollContainers = [document.body, document.querySelector<HTMLElement>('.app-content')].filter((element): element is HTMLElement => !!element)
    const previousOverflow = scrollContainers.map(element => element.style.overflow)
    scrollContainers.forEach(element => { element.style.overflow = 'hidden' })
    dialog.current?.querySelector<HTMLElement>('button')?.focus({ preventScroll: true })
    const keydown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') closeRef.current()
      if (event.key !== 'Tab') return
      const items = Array.from(dialog.current?.querySelectorAll<HTMLElement>('button:not(:disabled), input:not(:disabled)') ?? [])
      const first = items[0], last = items[items.length - 1]
      if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus() }
      else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus() }
    }
    document.addEventListener('keydown', keydown)
    return () => {
      document.removeEventListener('keydown', keydown)
      scrollContainers.forEach((element, index) => { element.style.overflow = previousOverflow[index] })
      restore?.focus({ preventScroll: true })
    }
  }, [])
  useEffect(() => {
    let alive = true
    listBackupFolders(request).then(result => { if (alive) setListing(result) })
      .catch((error: unknown) => { if (alive) setError(apiErrorMessage(error, vi ? 'Không đọc được thư mục.' : 'Could not list folders.')) })
      .finally(() => { if (alive) setBusy(false) })
    return () => { alive = false }
  }, [request, vi])
  const create = async (value: string) => {
    if (!canCreate) return
    setBusy(true); setError('')
    try { await createBackupFolder({ ...request, path: value }); await load(value); setName('') }
    catch (error: unknown) { setError(apiErrorMessage(error, vi ? 'Không tạo được thư mục.' : 'Could not create folder.')); setBusy(false) }
  }
  const selectable = listing?.exists && listing.path === path && path !== '/' && (request.location !== 'drive' || !!path)
    && !(request.purpose === 'scripts' && ['/root', '/opt/backup-monitor'].includes(path))
  // Outside the animated, scrollable page; the shell keeps the active theme variables.
  return createPortal(<div className="backup-folder-overlay backup-management"><div className="backup-folder-dialog" ref={dialog} role="dialog" aria-modal="true" aria-labelledby="folder-picker-title">
    <div className="backup-system-heading"><h2 id="folder-picker-title">{existingOnly ? (vi ? 'Chọn thư mục backup cần giám sát' : 'Choose a backup folder to monitor') : sourceOnly ? (vi ? 'Chọn thư mục dữ liệu cần sao lưu' : 'Choose source data folder') : canCreate ? (vi ? 'Chọn hoặc tạo thư mục' : 'Choose or create a folder') : (vi ? 'Chọn thư mục' : 'Choose a folder')} · {request.location === 'local' ? 'AlmaLinux' : 'Google Drive'}</h2><button type="button" className="backup-system-secondary" onClick={close} aria-label={vi ? 'Đóng' : 'Close'}><X size={18} /></button></div>
    <p className="backup-system-note">{existingOnly ? (vi ? 'Chọn thư mục backup đã tồn tại. Script giám sát sẽ cập nhật khi bạn lưu cài đặt.' : 'Choose an existing backup folder. The monitoring script updates when you save settings.') : sourceOnly ? (vi ? 'Chọn thư mục dữ liệu đã tồn tại trên máy chủ. Chọn ở đây chưa đổi script; cần xác nhận áp dụng ở cuối form.' : 'Choose an existing server data folder. Selecting it does not update scripts until you confirm and apply the form.') : canCreate ? (vi ? 'Tên thư mục dùng chữ không dấu, số, -, _, .; không có khoảng trắng. Tạo thư mục chưa thay đổi nơi lưu trữ đang dùng.' : 'Use ASCII letters, numbers, -, _, and .; no spaces. Creating a folder does not change active storage.') : (vi ? 'Chọn thư mục đã tồn tại. Thay đổi được áp dụng khi bạn lưu tác vụ.' : 'Choose an existing folder. Changes apply when you save the task.')}</p>
    <div className="backup-system-path"><input aria-label={vi ? 'Đường dẫn thư mục' : 'Folder path'} value={path} onChange={e => setPath(e.target.value)} disabled={busy} /><button type="button" className="backup-system-secondary" disabled={busy} onClick={() => void load(path)}>{vi ? 'Mở' : 'Open'}</button>{request.location === 'local' && <button type="button" className="backup-system-secondary" disabled={busy} onClick={() => void load('/')}>{vi ? 'Thư mục gốc /' : 'Server root /'}</button>}</div>
    <div className="backup-folder-list" aria-busy={busy}>{busy ? <p>{vi ? 'Đang đọc thư mục...' : 'Loading folders...'}</p> : <>
      {listing?.parent !== null && listing?.parent !== undefined && listing.parent !== path && <button type="button" onClick={() => void load(listing.parent!)}>← {vi ? 'Thư mục cha' : 'Parent folder'}</button>}
      {listing?.entries.map(entry => <button type="button" key={entry.path} onClick={() => void load(entry.path)}><Folder size={16} />{entry.name}</button>)}
      {listing?.exists && !listing.entries.length && <p>{vi ? 'Chưa có thư mục con.' : 'No subfolders.'}</p>}
      {listing && !listing.exists && <p>{vi ? 'Thư mục chưa tồn tại.' : 'Folder does not exist.'}</p>}
    </>}</div>
    {error && <p role="alert" className="backup-system-error">{localizeMessage(error, vi ? 'vi' : 'en')}</p>}
    {canCreate && <div className="backup-system-path"><input aria-label={vi ? 'Tên thư mục mới' : 'New folder name'} placeholder={vi ? 'Tên thư mục con mới' : 'New subfolder name'} value={name} onChange={e => setName(e.target.value)} disabled={busy} /><button type="button" className="backup-system-secondary" disabled={busy || !/^[A-Za-z0-9_-][A-Za-z0-9_.-]*$/.test(name)} onClick={() => void create(`${path.replace(/\/$/, '')}/${name}`.replace(/^\//, request.location === 'drive' ? '' : '/'))}><FolderPlus size={16} />{vi ? 'Tạo thư mục con' : 'Create subfolder'}</button></div>}
    <div className="backup-system-actions">{canCreate && <button type="button" className="backup-system-secondary" disabled={busy || !path || path === '/'} onClick={() => void create(path)}>{vi ? 'Tạo đường dẫn này nếu chưa có' : 'Create this path if missing'}</button>}<button type="button" className="apex-settings-save" disabled={busy || !selectable} onClick={() => choose(path)}>{vi ? 'Chọn thư mục này' : 'Use this folder'}</button></div>
  </div></div>, document.querySelector('.app-shell') ?? document.body)
}

export default function BackupSystemSettings({ lang, locked = false, expanded: controlledExpanded, onToggle, onReload, task, onStateChange, onBusyChange, mode = 'add', onClose }: {
  lang: Lang; locked?: boolean; expanded?: boolean; onToggle?: () => void; onReload?: () => void
  mode?: 'add' | 'edit'; onClose?: () => void
  task?: { jobs: CronJob[]; selectedId: string; revision: number; loading: boolean; error: string | null; onSelect: (id: string) => void; onApplied?: (schedule?: ConfigurationPreview['schedule']) => void; initialJob?: CronJob }
  onStateChange?: (state: BackupSystemState | null) => void; onBusyChange?: (busy: boolean) => void
}) {
  const vi = lang === 'vi'
  const idPrefix = mode === 'edit' ? 'edit-' : ''
  const [state, setState] = useState<BackupSystemState | null>(null), [draft, setDraft] = useState<BackupSystemConfig | null>(null)
  const [previewResponse, setPreview] = useState<ConfigurationPreview | null>(null), [busy, setBusy] = useState(false)
  const [previewFormKey, setPreviewFormKey] = useState('')
  const applying = useRef(false)
  const [scheduleDraft, setScheduleDraft] = useState<CronScheduleDraft | null>(null)
  const [directoryDraft, setDirectoryDraft] = useState<(CronDirectories & { key: string }) | null>(null)
  const [sourceScript, setSourceScript] = useState<(ManagedScriptOption & { taskID: string }) | null>(null)
  const [scriptOptions, setScriptOptions] = useState<{ taskID: string; version: string; options: ManagedScriptOption[] } | null>(null)
  const [scriptsLoading, setScriptsLoading] = useState(false), [scriptsError, setScriptsError] = useState('')
  const [scriptsRevision, setScriptsRevision] = useState(0)
  const [scriptPickerOpen, setScriptPickerOpen] = useState(false)
  const [feedback, setFeedback] = useState<{ text: string | string[]; error: boolean } | null>(null)
  const [picker, setPicker] = useState<{ key: DirectoryKey | SourceKey; request: FolderRequest } | null>(null)
  const [localExpanded, setExpanded] = useState(true)
  const expanded = controlledExpanded ?? localExpanded
  const disabled = busy || locked
  const initialJob = task?.initialJob
  const selected = initialJob ? task?.jobs.find(job => cronJobSelector(job) === cronJobSelector(initialJob) && job.schedule === initialJob.schedule) : task?.jobs.find(job => job.id === task.selectedId) ?? task?.jobs.find(job => job.id === 'backup-database') ?? task?.jobs[0]
  const directoryKey = selected ? `${cronJobSelector(selected)}:${selected.schedule}:${task?.revision}` : ''
  const location = selected?.instanceKey ? draft?.cronLocations?.[selected.instanceKey] : null
  const directories = directoryDraft?.key === directoryKey ? directoryDraft : { scriptsDir: location?.scriptsDir ?? draft?.scriptsDir ?? '', logsDir: location?.logsDir ?? draft?.logsDir ?? '', options: {
    backupRoot: location?.options?.backupRoot ?? draft?.backupRoot ?? '', driveRemote: location?.options?.driveRemote ?? draft?.driveRemote ?? '', driveFolder: location?.options?.driveFolder ?? draft?.driveFolder ?? '',
    source: location?.options?.source ?? (draft && state && selected ? backupSourceForTask(draft, state, selected.id) ?? undefined : undefined),
  } }
  const fieldValue = (key: DirectoryKey | 'driveRemote') => task ? (key === 'scriptsDir' || key === 'logsDir' ? directories[key] : directories.options?.[key] ?? '') : draft?.[key] ?? ''
  const sourceId = selected?.id ?? 'backup-site'
  const sourceKey = sourceConfigKeys[sourceId]
  const source = task ? directories.options?.source ?? null : draft && state ? backupSourceForTask(draft, state, sourceId) : null
  const taskDraft = selected ? (scheduleDraft?.id === selected.id && scheduleDraft.instanceKey === selected.instanceKey && scheduleDraft.expectedSchedule === selected.schedule && scheduleDraft.revision === task?.revision ? scheduleDraft : makeCronScheduleDraft(task?.initialJob ?? selected, task?.revision)) : null
  const scheduleChange = taskDraft ? buildCronScheduleChange(taskDraft, true) : null
  const backupTask = !!selected && ['backup-site', 'backup-database', 'backup-panel'].includes(selected.id)
  const moveBackups = !!task && mode === 'edit' && backupTask && fieldValue('backupRoot') !== (location?.options?.backupRoot ?? draft?.backupRoot ?? '')
  const canSelectScript = mode === 'add' && !!task && !!state?.supportsManagedScriptSelection
  const optionsReady = scriptOptions?.taskID === sourceId && scriptOptions.version === state?.version
  const chosenScript = sourceScript?.taskID === sourceId ? sourceScript : null
  const scriptChoiceValid = !chosenScript || (canSelectScript && optionsReady && scriptOptions.options.some(option => option.selector === chosenScript.selector && option.hash === chosenScript.hash))
  const schedule = scheduleChange ? { ...scheduleChange, ...(chosenScript && canSelectScript ? { sourceScript: { selector: chosenScript.selector, hash: chosenScript.hash } } : {}), ...(moveBackups ? { moveBackups: true } : {}), directories: { scriptsDir: directories.scriptsDir, logsDir: directories.logsDir, options: directories.options } } : null
  const scheduleBlocked = !!task && (!scriptChoiceValid || (!!chosenScript && (scriptsLoading || !!scriptsError)) || !state?.supportsCronIsolation || (moveBackups && !state?.supportsCronBackupMigration) || !selected || !!task.error || (task.loading && !task.jobs.length) || !!(selected && taskDraft && (
    cronScheduleIsStale(taskDraft, selected) || !proposedCronSchedule(taskDraft) || selected.status === 'running' || typeof selected.enabled !== 'boolean'
  )))
  const formKey = JSON.stringify([draft, state?.version, taskDraft, schedule])
  const preview = previewFormKey === formKey ? previewResponse : null
  const scriptPath = preview?.cronFiles?.script ?? (selected ? cronExecutionScript(selected, directories.scriptsDir,
    location?.fileLayout === 'files' || (!!state?.supportsFlatCronFiles && directoryDraft?.key === directoryKey)) : '')
  useEffect(() => { onStateChange?.(state) }, [state, onStateChange])
  useEffect(() => { onBusyChange?.(busy) }, [busy, onBusyChange])
  useEffect(() => {
    if (!canSelectScript || !state) return
    let alive = true
    Promise.resolve().then(() => {
      if (!alive) return undefined
      setScriptsLoading(true); setScriptsError('')
      return fetchManagedScripts(sourceId)
    }).then(result => {
      if (!alive || !result) return
      if (result.version !== state.version) throw new Error(vi ? 'Cấu hình đã thay đổi; tải lại cấu hình trước khi chọn script.' : 'Configuration changed; reload settings before selecting a script.')
      setScriptOptions({ taskID: sourceId, version: result.version, options: result.scripts })
    }).catch((error: unknown) => { if (alive) setScriptsError(apiErrorMessage(error, vi ? 'Không tải được danh sách script.' : 'Could not list scripts.')) })
      .finally(() => { if (alive) setScriptsLoading(false) })
    return () => { alive = false }
  }, [canSelectScript, sourceId, state, scriptsRevision, vi])
  useEffect(() => {
    // Schedule edits and manual runs can invalidate the server's preview fingerprint.
    const invalidatePreview = () => setPreview(null)
    window.addEventListener('force-refresh', invalidatePreview)
    return () => window.removeEventListener('force-refresh', invalidatePreview)
  }, [])
  useEffect(() => {
    let alive = true
    fetchBackupSystem().then(result => { if (alive) { setState(result); setDraft(result.config) } })
      .catch((error: unknown) => { if (alive) setFeedback({ text: apiErrorMessage(error, vi ? 'Không tải được cấu hình.' : 'Could not load settings.'), error: true }) })
    return () => { alive = false }
  }, [vi])
  const reload = async () => {
    setBusy(true); setFeedback(null); setPreview(null)
    try { const result = await fetchBackupSystem(); setState(result); setDraft(result.config); setDirectoryDraft(null); setScheduleDraft(selected ? makeCronScheduleDraft(selected, task?.revision) : null); onReload?.() }
    catch (error: unknown) { setFeedback({ text: apiErrorMessage(error, vi ? 'Không tải được cấu hình.' : 'Could not load settings.'), error: true }) }
    finally { setBusy(false) }
  }
  const edit = (key: DirectoryKey | 'driveRemote', value: string) => {
    if (task) setDirectoryDraft({ ...directories, key: directoryKey, ...(key === 'scriptsDir' || key === 'logsDir' ? { [key]: value } : { options: { ...directories.options!, [key]: value } }) })
    else if (draft) setDraft({ ...draft, [key]: value })
    setPreview(null); setFeedback(null)
  }
  const editSource = (key: SourceKey, value: BackupSource) => {
    if (task) setDirectoryDraft({ ...directories, key: directoryKey, options: { ...directories.options!, source: value } })
    else if (draft) setDraft({ ...draft, [key]: value })
    setPreview(null); setFeedback(null)
  }
  const chooseSource = (key: SourceKey, path: string) => editSource(key, { path, scope: source?.path === path ? source.scope : (key === 'databaseSource' && (path === '/www/server/data' || (state?.sources?.['backup-database']?.path === path && state.sources['backup-database'].scope === 'children'))) || (key === 'siteSource' && path === '/www/wwwroot') ? 'children' : 'folder' })
  const apply = async () => {
    if (!draft || !state || disabled || !state.ready || scheduleBlocked || applying.current) return
    applying.current = true
    setBusy(true); setFeedback(null); setPreview(null)
    try {
      if (mode === 'add' && task && !state.supportsMultipleSchedules) throw new Error(vi ? 'Cần cập nhật backup-manager trên AlmaLinux lên bản hỗ trợ nhiều lịch (v14). Khởi động lại backend Windows không cập nhật công cụ trên máy chủ.' : 'Update the AlmaLinux backup-manager to a multiple-schedule build (v14). Restarting the Windows backend does not update the server helper.')
      const checked = await previewBackupSystem(draft, state.version, schedule)
      if (chosenScript && (checked.sourceScript?.selector !== chosenScript.selector || checked.sourceScript.hash !== chosenScript.hash || checked.sourceScript.path !== chosenScript.path)) throw new Error(vi ? 'Máy chủ chưa xác nhận script đã chọn; cần helper v25 và tải lại danh sách.' : 'Server did not confirm the selected script; update to helper v25 and reload the list.')
      if (moveBackups && !checked.backupMigration) throw new Error(vi ? 'Máy chủ chưa xác nhận chuyển backup cũ; cần cập nhật backup-manager lên v19.' : 'The server did not confirm backup migration; update backup-manager to v19.')
      if (schedule && taskDraft && (checked.schedule?.id !== schedule.id || checked.schedule?.proposed !== proposedCronSchedule(taskDraft) || checked.schedule?.previous !== schedule.expectedSchedule)) {
        throw new Error(vi ? 'Backend chưa xác nhận đúng chu kỳ đã chọn. Hãy chạy lại backend bản mới rồi tải lại cấu hình.' : 'The backend did not confirm the selected recurrence. Restart the updated backend and reload settings.')
      }
      setPreview(checked); setPreviewFormKey(formKey)
      if (!checked.changed) {
        setFeedback({ error: false, text: vi ? 'Không có thay đổi để áp dụng.' : 'No changes to apply.' })
        return
      }
      const summary = [
        ...(checked.sourceScript ? [vi ? `Script nguồn: ${checked.sourceScript.path}. Tạo bản riêng cho cron mới; không sửa/xóa script nguồn.` : `Source script: ${checked.sourceScript.path}. Create a private copy for the new cron; the source is not changed or deleted.`] : []),
        ...(checked.cronFiles ? [`Script: ${checked.cronFiles.script}`, `Runner: ${checked.cronFiles.runner}`, `Log: ${checked.cronFiles.log}`, ...(checked.cronFiles.flattening ? [vi ? 'Chuyển file của cron này ra thư mục đã chọn. Chỉ xóa bản cũ đã kiểm tra và thư mục cron cũ nếu trống; giữ bản dự phòng scripts.' : 'Flatten this cron’s files into the selected folders. Remove only verified originals and empty old cron folders; script recovery copies are retained.'] : [])] : []),
        ...(checked.cronDirectories ? [`${vi ? 'Chỉ cron đang chọn — scripts' : 'Selected cron only — scripts'}: ${checked.cronDirectories.scriptsDir}`, `${vi ? 'Log' : 'Logs'}: ${checked.cronDirectories.logsDir}`] : []),
        ...(checked.cronDirectories?.options ? [`${vi ? 'Nơi lưu backup' : 'Backup storage'}: ${checked.cronDirectories.options.backupRoot}`, `Drive: ${checked.cronDirectories.options.driveRemote}:${checked.cronDirectories.options.driveFolder}`] : []),
        vi ? `Áp dụng thay đổi? Chuyển ${checked.filesToMove} tệp backup thuộc ${checked.daysToMove} ngày (${(checked.bytesToMove / 1048576).toFixed(2)} MB); ${checked.scriptsToUpdate} script cập nhật; ${checked.logsToMove} log sao chép.` : `Apply changes? Move ${checked.filesToMove} backup files across ${checked.daysToMove} days (${(checked.bytesToMove / 1048576).toFixed(2)} MB); update ${checked.scriptsToUpdate} scripts; copy ${checked.logsToMove} logs.`,
        ...((checked.scriptsToCreate ?? []).map(path => `${vi ? 'Tạo script mới' : 'Create new script'}: ${path}`)),
        ...(checked.schedule ? [`${vi ? 'Lịch chạy' : 'Schedule'}: ${checked.schedule.previous ? formatCronSchedule(checked.schedule.previous, lang) : (vi ? 'Chưa có lịch' : 'Not scheduled')} → ${formatCronSchedule(checked.schedule.proposed, lang)}${checked.schedule.enabled ? '' : (vi ? ' · giữ trạng thái tạm dừng' : ' · stays paused')}`] : []),
        ...(checked.sources?.map(change => `${vi ? 'Nguồn dữ liệu' : 'Source data'}: ${change.previous.path} → ${change.proposed.path}`) ?? []),
        ...(checked.driveChanged ? [vi ? `Dashboard sẽ đọc thư mục Drive mới: ${draft.driveFolder}. Backup cũ vẫn ở thư mục cũ.` : `Dashboard will read the new Drive folder: ${draft.driveFolder}. Existing backups stay in the old folder.`] : []),
        checked.backupMigration
          ? (vi ? 'Chuyển backup cũ của cron này sang nơi mới, kiểm tra checksum rồi mới xóa bản gốc. Không chuyển dữ liệu nguồn hoặc backup trên Drive; không đổi cron khác.' : 'Move this cron’s existing backups, verify checksums, then remove originals. Source data, Drive archives and other cron settings are unchanged.')
          : (vi ? 'Chỉ thay đổi cron đang chọn. Không di chuyển/xóa dữ liệu nguồn hoặc backup cũ; không chạy backup ngay.' : 'Only this cron changes. Source data and existing backups are not moved/deleted; no backup runs now.'),
      ].join('\n')
      if (!window.confirm(summary)) return
      const result = await applyBackupSystem(draft, state.version, checked.token, schedule)
      setState(result); setDraft(result.config); setDirectoryDraft(null); setSourceScript(null); setPreview(null); setScheduleDraft(null); onReload?.()
      setFeedback({ error: false, text: [vi ? 'Đã lưu cấu hình và lịch chạy đã chọn. Không chạy backup ngay; chính sách 14 ngày không thay đổi.' : 'Selected configuration and schedule saved. No backup was started immediately; the 14-day retention policy is unchanged.', ...(checked.backupMigration ? [vi ? `${checked.filesToMove} tệp backup đã được kiểm tra tại nơi lưu mới.` : `${checked.filesToMove} backup files verified at the new location.`] : []), ...(result.warnings || [])] })
      task?.onApplied?.(checked.schedule)
    } catch (error: unknown) { setPreview(null); setFeedback({ text: apiErrorMessage(error, vi ? 'Không áp dụng được cấu hình. Hãy tải lại rồi thử lại.' : 'Could not apply settings. Reload and try again.'), error: true }) }
    finally { applying.current = false; setBusy(false) }
  }
  const open = (key: DirectoryKey) => {
    if (draft) setPicker({ key, request: { location: key === 'driveFolder' ? 'drive' : 'local', purpose: key === 'scriptsDir' ? 'scripts' : key === 'logsDir' ? 'logs' : 'backup', path: fieldValue(key), remote: fieldValue('driveRemote') } })
  }
  return <section className="apex-settings-panel backup-system-panel" id={`${idPrefix}backup-storage`} aria-labelledby={`${idPrefix}backup-storage-title`}>
    <div className="backup-storage-heading"><h2 id={`${idPrefix}backup-storage-title`}>{mode === 'edit' ? (vi ? 'Sửa tác vụ' : 'Edit task') : <button type="button" aria-expanded={expanded} aria-controls={`${idPrefix}backup-storage-fields`} onClick={() => onToggle ? onToggle() : setExpanded(!expanded)}>{vi ? 'Thêm tác vụ' : 'Add task'}<ChevronDown size={17} className={expanded ? 'is-expanded' : ''} /></button>}</h2><div className="backup-edit-heading-actions"><button type="button" className="backup-system-secondary" disabled={disabled} onClick={() => void reload()} aria-label={vi ? 'Tải lại cấu hình' : 'Reload configuration'}><RefreshCw size={16} /></button>{mode === 'edit' && <button type="button" className="backup-system-secondary" disabled={disabled} onClick={onClose} aria-label={vi ? 'Đóng chỉnh sửa' : 'Close editor'}><X size={18} /></button>}</div></div>
    <div id={`${idPrefix}backup-storage-fields`} className="backup-storage-body" hidden={!expanded}>
    {feedback && <p className={feedback.error ? 'backup-system-error' : 'backup-system-success'} role={feedback.error ? 'alert' : 'status'}>{(Array.isArray(feedback.text) ? feedback.text : [feedback.text]).map(text => localizeMessage(text, lang)).join(' ')}</p>}
    {!draft || !state ? <p className="backup-system-note">{feedback?.error ? (vi ? 'Bấm tải lại để thử lại.' : 'Reload to retry.') : (vi ? 'Đang tải cấu hình từ AlmaLinux...' : 'Loading configuration from AlmaLinux...')}</p> : <>
      <form className="backup-unified-form" onSubmit={event => { event.preventDefault(); void apply() }}>
      {task && <BackupTaskForm lang={lang} jobs={task.jobs} selected={selected} draft={taskDraft} disabled={disabled} loading={task.loading} error={task.error} scriptPath={scriptPath} idPrefix={idPrefix} selectionLocked={mode === 'edit'}
        scriptChoices={canSelectScript ? { options: optionsReady ? scriptOptions.options : [], value: chosenScript ? (scriptChoiceValid ? chosenScript.selector : '__stale__') : '', loading: scriptsLoading || (!optionsReady && !scriptsError), error: !scriptChoiceValid ? (vi ? 'Script nguồn đã đổi hoặc không còn trong danh sách; hãy chọn lại.' : 'Source script changed or is no longer available; select again.') : scriptsError,
          onOpen: () => setScriptPickerOpen(true) } : undefined}
        onSelect={id => { setScriptPickerOpen(false); setSourceScript(null); setScheduleDraft(null); setPreview(null); setFeedback(null); task.onSelect(id) }}
        onDraftChange={change => { if (taskDraft) setScheduleDraft({ ...taskDraft, ...change }); setPreview(null); setFeedback(null) }}
        onReset={() => { setScheduleDraft(selected ? makeCronScheduleDraft(selected, task?.revision) : null); setPreview(null); onReload?.() }} />}
      <div className="backup-system-fields">
        <div className="apex-settings-field"><label htmlFor={`${idPrefix}backup-system-source`}>{vi ? 'Thư mục dữ liệu cần sao lưu' : 'Source data folder'}</label><div className="backup-system-path"><input id={`${idPrefix}backup-system-source`} value={source?.path ?? (sourceKey ? '' : fieldValue('backupRoot'))} disabled={disabled || !source} readOnly={!source} placeholder={sourceKey ? (vi ? 'Tải lại để lấy nguồn từ máy chủ' : 'Reload to get source settings') : undefined} onChange={e => sourceKey && chooseSource(sourceKey, e.target.value)} spellCheck={false} /><button type="button" className="backup-system-secondary" disabled={disabled || !source} onClick={() => source && sourceKey && setPicker({ key: sourceKey, request: { location: 'local', purpose: `source-${sourceId.replace('backup-', '')}` as FolderRequest['purpose'], path: source.path } })} aria-label={vi ? 'Chọn thư mục dữ liệu cần sao lưu' : 'Choose source data folder'} title={vi ? 'Chọn thư mục' : 'Choose folder'}><Folder size={16} aria-hidden="true" /></button></div></div>
        {(['backupRoot', 'driveFolder', 'scriptsDir', 'logsDir'] as DirectoryKey[]).map(key => <div className="apex-settings-field" key={key}><label htmlFor={`${idPrefix}backup-system-${key}`}>{labels[key][vi ? 0 : 1]}</label><div className="backup-system-path"><input id={`${idPrefix}backup-system-${key}`} value={fieldValue(key)} disabled={disabled} onChange={e => edit(key, e.target.value)} spellCheck={false} /><button type="button" className="backup-system-secondary" disabled={disabled || (key === 'driveFolder' && !state.remotes.length)} onClick={() => open(key)} aria-label={`${vi ? 'Chọn' : 'Choose'} ${labels[key][vi ? 0 : 1]}`} title={`${vi ? 'Chọn' : 'Browse'} ${labels[key][vi ? 0 : 1]}`}><Folder size={16} aria-hidden="true" /></button></div></div>)}
      </div>
      {task && !state.supportsCronIsolation && <p role="alert" className="backup-system-error">{vi ? 'Cần cập nhật backup-manager trên máy chủ lên v18 để sửa cấu hình riêng từng cron.' : 'Update the server backup-manager to v18 for isolated cron settings.'}</p>}
      {mode === 'add' && task && !state.supportsManagedScriptSelection && <p className="backup-system-note">{vi ? 'Cập nhật helper v25 trên AlmaLinux để chọn script thực hiện.' : 'Update the AlmaLinux helper to v25 to choose an execution script.'}</p>}
      {moveBackups && !state.supportsCronBackupMigration && <p role="alert" className="backup-system-error">{vi ? 'Cần cập nhật backup-manager trên AlmaLinux lên v19 để chuyển backup cũ khi đổi nơi lưu.' : 'Update the AlmaLinux backup-manager to v19 to migrate existing backups.'}</p>}
      {!state.ready && <p role="alert" className="backup-system-error">{vi ? 'Thiếu helper lưu trữ theo ngày; cần cài helper trước khi đổi đường dẫn.' : 'The daily-layout helper is missing; install it before changing paths.'}</p>}
      {!state.remotes.length && <p className="backup-system-warning">{vi ? 'Kết nối Google Drive ở phía trên trước khi thêm tác vụ.' : 'Connect Google Drive above before adding tasks.'}</p>}
      {mode === 'add' && task && !state.supportsMultipleSchedules && <p role="alert" className="backup-system-error">{vi ? 'backup-manager trên máy chủ chưa hỗ trợ nhiều lịch cùng loại. Cần cập nhật công cụ này lên bản v14 trước khi thêm lịch.' : 'The server backup-manager does not support multiple schedules yet. Update this helper to v14 before adding schedules.'}</p>}
      {preview && <div className="backup-system-preview" aria-label={vi ? 'Xem trước cấu hình' : 'Configuration preview'}><h3>{vi ? 'Ảnh hưởng khi áp dụng' : 'Changes to apply'}</h3><p>{vi ? `${preview.filesToMove} tệp backup / ${preview.daysToMove} thư mục ngày (${(preview.bytesToMove / 1048576).toFixed(2)} MB); ${preview.scriptsToUpdate} script cập nhật; ${preview.logsToMove} log sao chép.` : `${preview.filesToMove} backup files / ${preview.daysToMove} dated folders (${(preview.bytesToMove / 1048576).toFixed(2)} MB); ${preview.scriptsToUpdate} scripts updated; ${preview.logsToMove} logs copied.`}</p>{preview.driveChanged && <p className="backup-system-warning">{vi ? 'Thư mục Drive mới dùng cho lần đẩy tiếp theo. Backup cũ vẫn ở thư mục cũ; không bị xóa hoặc chuyển. Dashboard sẽ đọc thư mục mới.' : 'The new Drive folder is used for future uploads. Old backups stay in the old folder, without deletion or migration. The dashboard reads the new folder.'}</p>}{!preview.changed && <p>{vi ? 'Không có thay đổi để áp dụng.' : 'No changes to apply.'}</p>}</div>}
      {preview?.schedule && <p className="backup-system-note">{vi ? 'Lịch tác vụ đã chọn: ' : 'Selected task schedule: '}{preview.schedule.previous ? formatCronSchedule(preview.schedule.previous, lang) : (vi ? 'Chưa có lịch' : 'Not scheduled')} → {formatCronSchedule(preview.schedule.proposed, lang)}{preview.schedule.enabled ? '' : (vi ? ' · giữ trạng thái tạm dừng' : ' · stays paused')}</p>}
      {preview?.cronDirectories && <p className="backup-system-note">{vi ? 'Đường dẫn riêng sẽ sử dụng' : 'Private paths to use'}: scripts — {preview.cronDirectories.scriptsDir}; log — {preview.cronDirectories.logsDir}.</p>}
      {preview?.cronFiles && <p className="backup-system-note">Script: {preview.cronFiles.script}<br />Log: {preview.cronFiles.log}</p>}
      {preview?.scriptsToCreate?.map(path => <p className="backup-system-note" key={path}>{vi ? 'Script sẽ tạo: ' : 'Script to create: '}{path}</p>)}
      {preview?.sources?.map(change => <p className="backup-system-note" key={change.id}>{vi ? 'Nguồn dữ liệu: ' : 'Source data: '}{change.previous.path} → {change.proposed.path} · {change.proposed.scope === 'children' ? (vi ? 'từng thư mục con' : 'per child folder') : (vi ? 'thư mục đã chọn' : 'selected folder')}. {vi ? 'Cập nhật script cho lần chạy tiếp theo; không di chuyển dữ liệu nguồn.' : 'Updates the script for the next run; source data is not moved.'}</p>)}
      <div className="backup-system-actions">{mode === 'edit' && <button type="button" className="backup-system-secondary" disabled={disabled} onClick={onClose}>{vi ? 'Hủy' : 'Cancel'}</button>}<button type="submit" className="apex-settings-save" disabled={disabled || !state.ready || scheduleBlocked}>{busy ? (vi ? 'Đang xử lý...' : 'Working...') : mode === 'edit' ? (vi ? 'Lưu thay đổi' : 'Save changes') : (vi ? 'Thêm tác vụ' : 'Add task')}</button></div>
      {state.recoveryDir && <p className="backup-system-note">{vi ? 'Dự phòng cấu hình cũ: ' : 'Previous settings saved in: '}{state.recoveryDir}</p>}
      </form>
    </>}
    </div>
    {scriptPickerOpen && canSelectScript && selected && <ManagedScriptPicker key={sourceId} vi={vi} taskName={backupTaskLabels[selected.id]?.[lang] ?? selected.name} initialPath={directories.scriptsDir || '/root/scripts'} options={optionsReady ? scriptOptions.options : []} value={chosenScript ? (scriptChoiceValid ? chosenScript.selector : '__stale__') : ''} loading={scriptsLoading || (!optionsReady && !scriptsError)} error={scriptsError} onReload={() => setScriptsRevision(value => value + 1)} onClose={() => setScriptPickerOpen(false)} onChoose={selector => {
      const option = optionsReady ? scriptOptions.options.find(item => item.selector === selector) : null
      if (selector && !option) return
      setSourceScript(option ? { ...option, taskID: sourceId } : null); setPreview(null); setFeedback(null); setScriptPickerOpen(false)
    }} />}
    {picker && <FolderPicker request={picker.request} vi={vi} allowCreate={false} close={() => setPicker(null)} choose={value => { if (picker.key === 'siteSource' || picker.key === 'databaseSource' || picker.key === 'panelSource') chooseSource(picker.key, value); else edit(picker.key, value); setPicker(null) }} />}
  </section>
}
