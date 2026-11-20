import { localizeMessage } from "./i18n"
import { backupTaskLabels, cronScheduleIsStale, cycleLabels, proposedCronSchedule, backupSourceForTask, buildCronScheduleChange, makeCronScheduleDraft, sourceConfigKeys, cronJobSelector, cronExecutionScript, applyCronImport, CronImportError, cronLogForDisplay, cronLogLineLevel, filterCronLog, applySavedCronSchedule, sortCronJobsByTime, cronImportMaxBytes, exportCronFile, parseCronFile, planCronImport, backupTaskLabels as jobLabels, backupTaskScripts } from "./cronSupport"
import type { CronScheduleDraft, CronImportRow } from "./cronSupport"
import type { Lang } from "./i18n"
import type { CronJob, CronJobLog } from "./shared"
import { formatCronSchedule, statusLabel } from "./shared"
import type { ManagedScriptOption, FolderList, BackupSource, BackupSystemConfig, BackupSystemState, ConfigurationPreview, CronDirectories, FolderRequest, ScriptDocument, TaskProcess } from "./api"
import { Folder, FileCode, RefreshCw, Search, X, ChevronDown, Eye, Pause, Pencil, Play, Trash2 } from "lucide-react"
import { useEffect, useRef, useState, useMemo, useCallback, useLayoutEffect } from "react"
import { createPortal } from "react-dom"
import { listBackupFolders, apiErrorMessage, applyBackupSystem, fetchBackupSystem, fetchManagedScripts, previewBackupSystem, fetchCronJobs, readLogPage, readTaskScript, saveTaskScript, taskProcess, deleteCronJob, fetchCronJobLog, runCronJob, updateCronJobState, useCronJobs, cancelTask } from "./api"
import { FolderPicker, Button, ProgressView } from "./ui"
import type { RefObject } from "react"

type Props = {
  lang: Lang; jobs: CronJob[]; selected?: CronJob; draft: CronScheduleDraft | null
  disabled: boolean; loading: boolean; error: string | null; scriptPath: string
  idPrefix?: string; selectionLocked?: boolean
  scriptChoices?: { options: ManagedScriptOption[]; value: string; loading: boolean; error: string; onOpen: () => void }
  onSelect: (id: string) => void; onDraftChange: (change: Partial<CronScheduleDraft>) => void; onReset: () => void
}

// Controlled fields only: schedule and storage share the parent's preview/apply.
export  function BackupTaskForm({ lang, jobs, selected, draft, disabled, loading, error, scriptPath, scriptChoices, idPrefix = '', selectionLocked = false, onSelect, onDraftChange, onReset }: Props) {
  const vi = lang === 'vi'
  if (!selected || !draft) return loading ? <p className="backup-system-note">{vi ? 'Đang tải tác vụ...' : 'Loading tasks...'}</p> : null
  const stale = cronScheduleIsStale(draft, selected)
  const locked = disabled || !!error || stale || selected.status === 'running' || typeof selected.enabled !== 'boolean'
  const proposed = proposedCronSchedule(draft)
  const showHour = !['hourly', 'hours', 'minutes'].includes(draft.cycle)
  const weekLabels = vi ? ['Chủ nhật', 'Thứ hai', 'Thứ ba', 'Thứ tư', 'Thứ năm', 'Thứ sáu', 'Thứ bảy'] : ['Sunday', 'Monday', 'Tuesday', 'Wednesday', 'Thursday', 'Friday', 'Saturday']
  const choices = selectionLocked ? [selected] : jobs
  return <div className="backup-task-embedded">
    <div className="backup-system-fields">
      <div className="apex-settings-field"><label htmlFor={`${idPrefix}backup-task-type`}>{vi ? 'Loại tác vụ (Task type)' : 'Task type'}</label><select id={`${idPrefix}backup-task-type`} value={selected.id} disabled={disabled || selectionLocked} onChange={event => onSelect(event.target.value)}>{choices.map(job => <option key={job.id} value={job.id}>{backupTaskLabels[job.id]?.[lang] ?? job.name}</option>)}</select></div>
      <div className="apex-settings-field"><label htmlFor={`${idPrefix}backup-task-script`}>{vi ? 'Script thực hiện' : 'Execution script'}</label>{scriptChoices ? <>
        <div className="backup-system-path"><input id={`${idPrefix}backup-task-script`} readOnly value={scriptChoices.value === '__stale__' ? (vi ? 'Script đã thay đổi — chọn lại' : 'Script changed — select again') : scriptChoices.options.find(option => option.selector === scriptChoices.value)?.path ?? scriptPath} spellCheck={false} /><button className="backup-system-secondary" type="button" disabled={disabled} onClick={scriptChoices.onOpen} aria-haspopup="dialog" aria-label={vi ? 'Chọn script trên AlmaLinux' : 'Choose an AlmaLinux script'} title={vi ? 'Chọn script' : 'Choose script'}><Folder size={16} aria-hidden="true" /></button></div>
        {scriptChoices.error && <p className="backup-system-error" role="alert" style={{ gridColumn: '1 / -1', margin: 0 }}>{localizeMessage(scriptChoices.error, lang)}</p>}
      </> : <input id={`${idPrefix}backup-task-script`} readOnly value={scriptPath} spellCheck={false} />}</div>
      <div className="apex-settings-field"><label htmlFor={`${idPrefix}backup-task-cycle`}>{vi ? 'Chu kỳ thực hiện' : 'Recurrence'}</label>
        <div className="backup-task-cycle">
          <select id={`${idPrefix}backup-task-cycle`} disabled={locked} value={draft.cycle} onChange={event => onDraftChange({ cycle: event.target.value as CronScheduleDraft['cycle'] })}>{Object.entries(cycleLabels).filter(([key]) => key !== 'current' || draft.cycle === 'current').map(([key, label]) => <option key={key} value={key}>{label[lang]}</option>)}</select>
          {['days', 'hours', 'minutes'].includes(draft.cycle) && <div className="backup-time-unit"><input type="text" inputMode="numeric" maxLength={2} pattern="[0-9]{1,2}" aria-label={vi ? 'Khoảng cách chu kỳ' : 'Cycle interval'} value={draft.every} disabled={locked} onChange={event => { if (/^\d{0,2}$/.test(event.target.value)) onDraftChange({ every: event.target.value }) }} required /><span>{draft.cycle === 'days' ? (vi ? 'Ngày' : 'Days') : draft.cycle === 'hours' ? (vi ? 'Giờ' : 'Hours') : (vi ? 'Phút' : 'Minutes')}</span></div>}
          {draft.cycle === 'weekly' && <select aria-label={vi ? 'Ngày trong tuần' : 'Weekday'} value={draft.weekday} disabled={locked} onChange={event => onDraftChange({ weekday: event.target.value })}>{weekLabels.map((label, day) => <option key={day} value={day}>{label}</option>)}</select>}
          {draft.cycle === 'monthly' && <div className="backup-time-unit"><input type="text" inputMode="numeric" maxLength={2} pattern="(?:[1-9]|[12][0-9]|3[01])" aria-label={vi ? 'Ngày trong tháng, 1–31' : 'Day of month, 1–31'} value={draft.day} disabled={locked} onChange={event => { if (/^\d{0,2}$/.test(event.target.value)) onDraftChange({ day: event.target.value }) }} required /><span>{vi ? 'Ngày' : 'Day'}</span></div>}
          {draft.cycle !== 'minutes' && <div className="backup-task-clock">
            {showHour && <div className="backup-time-unit"><input type="text" inputMode="numeric" maxLength={2} pattern="(?:[01]?[0-9]|2[0-3])" aria-label={vi ? 'Giờ thực hiện, 00–23' : 'Execution hour, 00–23'} value={draft.hour} disabled={locked} onChange={event => { if (/^\d{0,2}$/.test(event.target.value)) onDraftChange({ hour: event.target.value }) }} required /><span>{vi ? 'Giờ' : 'Hours'}</span></div>}
            <div className="backup-time-unit"><input type="text" inputMode="numeric" maxLength={2} pattern="[0-5]?[0-9]" aria-label={vi ? 'Phút thực hiện, 00–59' : 'Execution minute, 00–59'} value={draft.minute} disabled={locked} onChange={event => { if (/^\d{0,2}$/.test(event.target.value)) onDraftChange({ minute: event.target.value }) }} required /><span>{vi ? 'Phút' : 'Minutes'}</span></div>
          </div>}
          <span className="backup-cycle-preview" role="status">{proposed ? formatCronSchedule(proposed, lang) : (vi ? 'Nhập giá trị chu kỳ hợp lệ' : 'Enter valid schedule values')}</span>
        </div>
      </div>
    </div>
    {(stale || error) && <div className="backup-task-editor-notes">
      <p className="backup-system-error" role="alert">{(error && localizeMessage(error, lang)) || (vi ? 'Lịch hoặc trạng thái trên máy chủ đã thay đổi. Tải lịch hiện tại trước khi xem trước.' : 'The server schedule or state changed. Reload the current schedule before previewing.')}</p>
      <button type="button" className="backup-system-secondary" disabled={disabled} onClick={onReset}>{vi ? 'Tải lịch hiện tại' : 'Reload current schedule'}</button>
    </div>}
  </div>
}

type ManagedScriptPickerProps = {
  vi: boolean; taskName: string; initialPath: string; options: ManagedScriptOption[]; value: string
  loading: boolean; error: string; onReload: () => void
  onClose: () => void; onChoose: (selector: string) => void
}

export  function ManagedScriptPicker({ vi, taskName, initialPath, options, value, loading, error, onReload, onClose, onChoose }: ManagedScriptPickerProps) {
  const [search, setSearch] = useState('')
  // Capture the hash too: refreshing a changed source requires reselection.
  const [selection, setSelection] = useState<ManagedScriptOption | null | undefined>(() => value === '' ? null : options.find(option => option.selector === value))
  const [path, setPath] = useState(() => options.find(option => option.selector === value)?.path.replace(/\/[^/]+$/, '') || initialPath)
  const [browsePath, setBrowsePath] = useState(path), [revision, setRevision] = useState(0)
  const [folderState, setFolderState] = useState<{ path: string; listing: FolderList | null; pending: boolean; error: string }>({ path, listing: null, pending: true, error: '' })
  const dialog = useRef<HTMLDivElement>(null), close = useRef(onClose)
  useEffect(() => { close.current = onClose }, [onClose])
  useEffect(() => {
    let alive = true
    Promise.resolve().then(() => {
      if (!alive) return undefined
      setFolderState({ path: browsePath, listing: null, pending: true, error: '' })
      return listBackupFolders({ location: 'local', purpose: 'scripts', path: browsePath })
    }).then(listing => { if (alive && listing) setFolderState({ path: browsePath, listing, pending: false, error: '' }) })
      .catch((cause: unknown) => { if (alive) setFolderState({ path: browsePath, listing: null, pending: false, error: apiErrorMessage(cause, vi ? 'Không đọc được thư mục trên AlmaLinux.' : 'Could not list the AlmaLinux folder.') }) })
    return () => { alive = false }
  }, [browsePath, revision, vi])
  useEffect(() => {
    const previousFocus = document.activeElement as HTMLElement | null
    const containers = [document.body, document.querySelector<HTMLElement>('.app-content')].filter((element): element is HTMLElement => !!element)
    const overflow = containers.map(element => element.style.overflow)
    containers.forEach(element => { element.style.overflow = 'hidden' })
    dialog.current?.querySelector<HTMLInputElement>('input[type="search"]')?.focus({ preventScroll: true })
    const keydown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') { event.preventDefault(); event.stopPropagation(); close.current(); return }
      if (event.key !== 'Tab') return
      const elements = Array.from(dialog.current?.querySelectorAll<HTMLElement>('button:not(:disabled), input:not(:disabled)') ?? [])
      const first = elements[0], last = elements.at(-1)
      if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus() }
      else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus() }
    }
    document.addEventListener('keydown', keydown)
    return () => {
      document.removeEventListener('keydown', keydown)
      containers.forEach((element, index) => { element.style.overflow = overflow[index] })
      if (previousFocus?.isConnected) previousFocus.focus({ preventScroll: true })
    }
  }, [])
  const query = search.trim().toLowerCase()
  const currentListing = folderState.path === browsePath ? folderState.listing : null
  const folderLoading = folderState.path !== browsePath || folderState.pending
  const filtered = options.filter(option => option.path.replace(/\/[^/]+$/, '') === browsePath && `${option.path} ${option.selector}`.toLowerCase().includes(query))
  const folders = currentListing?.entries.filter(folder => folder.name.toLowerCase().includes(query)) ?? []
  const scriptFolders = [...new Set(options.map(option => option.path.replace(/\/[^/]+$/, '')))]
  const open = (next: string) => { setPath(next); setBrowsePath(next); setRevision(value => value + 1); setSearch('') }
  const valid = selection === null || (!!selection && options.some(option => option.selector === selection.selector && option.hash === selection.hash))
  return createPortal(<div className="backup-folder-overlay backup-management" onMouseDown={event => { if (event.target === event.currentTarget) onClose() }}>
    <div className="backup-folder-dialog managed-script-picker" ref={dialog} role="dialog" aria-modal="true" aria-labelledby="managed-script-picker-title">
      <div className="backup-system-heading"><div><h2 id="managed-script-picker-title">{vi ? 'Chọn script' : 'Choose script'}</h2><p className="managed-script-picker-task">{taskName} · AlmaLinux</p></div><button type="button" className="backup-system-secondary" onClick={onClose} aria-label={vi ? 'Đóng chọn script' : 'Close script picker'}><X size={18} /></button></div>
      <div className="backup-system-path managed-script-picker-path"><input aria-label={vi ? 'Đường dẫn thư mục scripts trên AlmaLinux' : 'AlmaLinux scripts folder path'} value={path} onChange={event => setPath(event.target.value)} onKeyDown={event => { if (event.key === 'Enter') { event.preventDefault(); open(path) } }} /><button type="button" className="backup-system-secondary" disabled={folderLoading} onClick={() => open(path)}>{vi ? 'Mở' : 'Open'}</button><button type="button" className="backup-system-secondary" disabled={folderLoading} onClick={() => open('/')}>{vi ? 'Thư mục gốc /' : 'Server root /'}</button></div>
      {scriptFolders.length > 0 && <div className="managed-script-picker-shortcuts">{scriptFolders.map(folder => <button type="button" className="backup-system-secondary" key={folder} disabled={folderLoading} title={folder} onClick={() => open(folder)}><Folder size={16} aria-hidden="true" />{folder}</button>)}</div>}
      <div className="managed-script-picker-toolbar"><label className="managed-script-picker-search"><Search size={18} aria-hidden="true" /><input type="search" value={search} onChange={event => setSearch(event.target.value)} placeholder={vi ? 'Tìm trong thư mục này…' : 'Search this folder…'} aria-label={vi ? 'Tìm script' : 'Search scripts'} /></label><button type="button" className="backup-system-secondary" disabled={loading || folderLoading} onClick={() => { onReload(); setRevision(value => value + 1) }}><RefreshCw size={16} aria-hidden="true" />{vi ? 'Làm mới' : 'Refresh'}</button></div>
      <div className="backup-folder-list managed-script-picker-list" aria-busy={loading || folderLoading}>
        {currentListing?.parent && <button type="button" disabled={folderLoading} onClick={() => open(currentListing.parent!)}>← {vi ? 'Thư mục cha' : 'Parent folder'}</button>}
        {loading || folderLoading ? <p role="status">{vi ? 'Đang đọc thư mục…' : 'Loading folder…'}</p> : <>
        {folders.map(folder => <button type="button" key={folder.path} onClick={() => open(folder.path)}><Folder size={18} aria-hidden="true" />{folder.name}</button>)}
        {filtered.map(option => {
          const checked = selection?.selector === option.selector && selection.hash === option.hash
          return <label key={option.selector} className={`managed-script-picker-file${checked ? ' is-selected' : ''}`}><input type="radio" name="managed-script-selection" checked={checked} onChange={() => setSelection(option)} /><FileCode size={20} aria-hidden="true" /><span><strong>{option.path.split('/').at(-1)}</strong><small>{option.path}</small></span></label>
        })}
        {!folders.length && !filtered.length && !folderState.error && <p>{!currentListing?.exists ? (vi ? 'Thư mục chưa tồn tại.' : 'Folder does not exist.') : query ? (vi ? 'Không có kết quả khớp tìm kiếm.' : 'No matching items.') : (vi ? 'Không có script được quản lý đúng loại tác vụ trong thư mục này.' : 'No managed scripts matching this task type in this folder.')}</p>}
        </>}
      </div>
      <div className="managed-script-picker-default">
        <label className={`managed-script-picker-file${selection === null ? ' is-selected' : ''}`}><input type="radio" name="managed-script-selection" checked={selection === null} disabled={loading} onChange={() => setSelection(null)} /><FileCode size={20} aria-hidden="true" /><span><strong>{vi ? 'Script mặc định' : 'Default script'}</strong><small>{vi ? 'Tạo script chuẩn nếu chưa có' : 'Create a standard script if missing'}</small></span></label>
      </div>
      {selection && <p className="managed-script-picker-selected">{vi ? 'Đã chọn: ' : 'Selected: '}<code>{selection.path}</code></p>}
      {folderState.error && <p role="alert" className="backup-system-error">{localizeMessage(folderState.error, vi ? 'vi' : 'en')}</p>}
      {error && <p role="alert" className="backup-system-error">{localizeMessage(error, vi ? 'vi' : 'en')}</p>}
      {!valid && !loading && <p role="alert" className="backup-system-error">{vi ? 'Script đã đổi hoặc không còn khả dụng; hãy chọn lại.' : 'The script changed or is no longer available; select again.'}</p>}
      <div className="backup-system-actions"><button type="button" className="backup-system-secondary" onClick={onClose}>{vi ? 'Hủy' : 'Cancel'}</button><button type="button" className="apex-settings-save" disabled={loading || !valid || (!!selection && !!error)} onClick={() => onChoose(selection?.selector ?? '')}>{vi ? 'Chọn script này' : 'Use this script'}</button></div>
    </div>
  </div>, document.querySelector('.app-shell') ?? document.body)
}

type DirectoryKey = 'backupRoot' | 'scriptsDir' | 'logsDir' | 'driveFolder'
type SourceKey = 'siteSource' | 'databaseSource' | 'panelSource'
const labels: Record<DirectoryKey, [string, string]> = {
  backupRoot: ['Thư mục backup trên AlmaLinux', 'AlmaLinux backup directory'], scriptsDir: ['Thư mục scripts', 'Scripts directory'],
  logsDir: ['Thư mục nhật ký (log)', 'Log directory'], driveFolder: ['Thư mục backup trên Google Drive', 'Google Drive backup folder'],
}

export  function BackupSystemSettings({ lang, locked = false, expanded: controlledExpanded, onToggle, onReload, task, onStateChange, onBusyChange, mode = 'add', onClose }: {
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
    {(state ? !state.remotes.length : feedback?.error) && <p className="backup-system-warning">{vi ? 'Nếu chưa có kết nối Google Drive, hãy cấu hình rclone qua SSH rồi tải lại.' : 'If Google Drive is not configured, set up rclone over SSH, then reload.'}</p>}
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
    {picker && <FolderPicker request={picker.request} vi={vi} close={() => setPicker(null)} choose={value => { if (picker.key === 'siteSource' || picker.key === 'databaseSource' || picker.key === 'panelSource') chooseSource(picker.key, value); else edit(picker.key, value); setPicker(null) }} />}
  </section>
}

type BackupTaskEditDialogProps = {
  lang: Lang; job: CronJob; jobs: CronJob[]; loading: boolean; error: string | null
  busy: boolean; locked: boolean; onBusyChange: (busy: boolean) => void
  onClose: () => void; onReload: () => void; onSaved: (schedule?: ConfigurationPreview['schedule']) => void
}

export  function BackupTaskEditDialog({ lang, job, jobs, loading, error, busy, locked, onBusyChange, onClose, onReload, onSaved }: BackupTaskEditDialogProps) {
  const dialog = useRef<HTMLDivElement>(null)
  const closeState = useRef({ busy, onClose })
  useEffect(() => { closeState.current = { busy, onClose } }, [busy, onClose])
  useEffect(() => {
    const restore = document.activeElement as HTMLElement | null
    const containers = [document.body, document.querySelector<HTMLElement>('.app-content')].filter((element): element is HTMLElement => !!element)
    const overflow = containers.map(element => element.style.overflow)
    containers.forEach(element => { element.style.overflow = 'hidden' })
    const selector = 'button:not(:disabled), select:not(:disabled), input:not(:disabled)'
    dialog.current?.querySelector<HTMLElement>(selector)?.focus({ preventScroll: true })
    const keydown = (event: KeyboardEvent) => {
      // The nested folder picker owns focus and Escape while it is open.
      if (document.querySelector('.backup-folder-dialog')) return
      if (event.key === 'Escape' && !closeState.current.busy) closeState.current.onClose()
      if (event.key !== 'Tab') return
      const items = Array.from(dialog.current?.querySelectorAll<HTMLElement>(selector) ?? [])
      const first = items[0], last = items[items.length - 1]
      if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus() }
      else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus() }
    }
    document.addEventListener('keydown', keydown)
    return () => {
      document.removeEventListener('keydown', keydown)
      containers.forEach((element, index) => { element.style.overflow = overflow[index] })
      if (restore?.isConnected) restore.focus({ preventScroll: true })
    }
  }, [])
  return createPortal(<div className="backup-task-edit-overlay backup-management" onMouseDown={event => { if (event.target === event.currentTarget && !busy) onClose() }}>
    <div className="backup-task-edit-dialog" ref={dialog} role="dialog" aria-modal="true" aria-labelledby="edit-backup-storage-title">
      <BackupSystemSettings lang={lang} mode="edit" expanded locked={locked} onClose={onClose} onReload={onReload} onBusyChange={onBusyChange}
        task={{ jobs, selectedId: job.id, revision: 0, loading, error, initialJob: job, onSelect: () => {}, onApplied: onSaved }} />
    </div>
  </div>, document.querySelector('.app-shell') ?? document.body)
}

type CronImportDialogProps = { lang: Lang; filename: string; plan: CronImportRow[]; onClose: () => void; onBusyChange: (busy: boolean) => void; onUpdated: () => void; onSaved: (count: number) => void }

export  function CronImportDialog({ lang, filename, plan, onClose, onBusyChange, onUpdated, onSaved }: CronImportDialogProps) {
  const vi = lang === 'vi'
  const [busy, setBusy] = useState(false), [progress, setProgress] = useState(0)
  const [error, setError] = useState<{ message: string; completed: number; failedId: string; writeAttempted: boolean } | null>(null)
  const dialog = useRef<HTMLDivElement>(null), applying = useRef(false), close = useRef(onClose)
  const changed = plan.filter(row => row.change).length
  useEffect(() => { close.current = onClose }, [onClose])
  useEffect(() => {
    const restore = document.activeElement as HTMLElement | null
    const containers = [document.body, document.querySelector<HTMLElement>('.app-content')].filter((element): element is HTMLElement => !!element)
    const overflow = containers.map(element => element.style.overflow)
    containers.forEach(element => { element.style.overflow = 'hidden' })
    const selector = 'button:not(:disabled)'
    dialog.current?.querySelector<HTMLElement>(selector)?.focus({ preventScroll: true })
    const keydown = (event: KeyboardEvent) => {
      if (event.key === 'Escape' && !applying.current) close.current()
      if (event.key !== 'Tab') return
      const items = Array.from(dialog.current?.querySelectorAll<HTMLElement>(selector) ?? [])
      const first = items[0], last = items[items.length - 1]
      if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus() }
      else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus() }
    }
    document.addEventListener('keydown', keydown)
    return () => {
      document.removeEventListener('keydown', keydown)
      containers.forEach((element, index) => { element.style.overflow = overflow[index] })
      if (restore?.isConnected) restore.focus({ preventScroll: true })
    }
  }, [])
  const apply = async () => {
    if (applying.current || !changed || error) return
    applying.current = true; setBusy(true); onBusyChange(true); setError(null)
    try {
      const saved = await applyCronImport(plan, {
        jobs: fetchCronJobs, settings: fetchBackupSystem,
        preview: (state, change) => previewBackupSystem(state.config, state.version, change),
        apply: (state, token, change) => applyBackupSystem(state.config, state.version, token, change),
      }, completed => setProgress(completed))
      onSaved(saved.length)
    } catch (cause: unknown) {
      const partial = cause instanceof CronImportError ? cause : null
      const message = apiErrorMessage(partial ? partial.cause : cause, vi ? 'Không nhập được lịch Cron.' : 'Could not import Cron schedules.')
      setError({ message, completed: partial?.completedIds.length ?? 0, failedId: partial?.failedId ?? '', writeAttempted: partial?.writeAttempted ?? false })
    } finally {
      applying.current = false; setBusy(false); onBusyChange(false); onUpdated()
    }
  }
  return createPortal(<div className="backup-task-edit-overlay backup-management" onMouseDown={event => { if (event.target === event.currentTarget && !applying.current) onClose() }}>
    <div className="cron-import-dialog" ref={dialog} role="dialog" aria-modal="true" aria-labelledby="cron-import-title" aria-describedby="cron-import-description">
      <div className="backup-storage-heading"><h2 id="cron-import-title">{vi ? 'Import lịch Cron' : 'Import Cron schedules'}</h2><button type="button" className="backup-system-secondary" disabled={busy} onClick={onClose} aria-label={vi ? 'Đóng' : 'Close'}><X size={18} /></button></div>
      <div className="cron-import-body">
        <p className="cron-import-filename">{filename}</p>
        <p id="cron-import-description">{vi ? 'Tệp phiên bản 2 thêm những lịch còn thiếu, giữ nguyên các lịch khác; tệp phiên bản 1 chỉ thay lịch cũ khi xác định được duy nhất. Giữ trạng thái bật/tạm dừng của lịch hiện có; lịch mới sẽ được bật. Không đổi script hoặc nơi lưu, không xóa lịch ngoài tệp, không chạy backup ngay. Áp dụng lần lượt; nếu lỗi sẽ dừng và báo các mục đã lưu.' : 'Version 2 adds missing schedules and preserves other schedules; version 1 replaces a legacy schedule only when unambiguous. Existing enabled/paused states stay unchanged; new schedules are enabled. Scripts and storage are unchanged, unlisted schedules are not deleted, and no backup runs now. Failures stop the import and report saved items.'}</p>
        <div className="cron-import-table-wrap"><table><thead><tr><th>{vi ? 'Tác vụ' : 'Task'}</th><th>{vi ? 'Lịch hiện tại' : 'Current schedule'}</th><th>{vi ? 'Lịch nhập' : 'Imported schedule'}</th><th>{vi ? 'Thay đổi' : 'Change'}</th></tr></thead><tbody>{plan.map(row => <tr key={`${row.id}:${row.schedule}`}><td>{backupTaskLabels[row.id][lang]}</td><td>{row.previous === null ? (vi ? 'Chưa có lịch' : 'Not scheduled') : formatCronSchedule(row.previous, lang)}</td><td>{formatCronSchedule(row.schedule, lang)}</td><td>{row.previous === null ? (vi ? 'Tạo mới · bật' : 'Create · enabled') : row.change ? (vi ? 'Cập nhật' : 'Update') : (vi ? 'Giữ nguyên' : 'Unchanged')}</td></tr>)}</tbody></table></div>
        {!changed && <p role="status">{vi ? 'Các lịch trong tệp đã giống lịch hiện tại, không cần nhập.' : 'Schedules already match; nothing to import.'}</p>}
        {busy && <p role="status">{vi ? `Đang kiểm tra và nhập: ${progress}/${plan.length} tác vụ…` : `Checking and importing: ${progress}/${plan.length} tasks…`}</p>}
        {error && <p className="backup-system-error" role="alert">{[
          localizeMessage(error.message, lang),
          vi ? `Đã xác minh lưu ${error.completed} tác vụ. Dừng nhập; các mục còn lại chưa được áp dụng.` : `${error.completed} saved tasks verified. Import stopped; remaining tasks were not applied.`,
          error.failedId ? `${vi ? 'Tác vụ cần kiểm tra' : 'Check task'}: ${backupTaskLabels[error.failedId]?.[lang] ?? error.failedId}.` : '',
          error.writeAttempted ? (vi ? 'Tác vụ lỗi có thể đã được lưu. Hãy kiểm tra danh sách trước khi nhập lại.' : 'The failed task may have been saved. Check the list before importing again.') : '',
        ].filter(Boolean).join(' ')}</p>}
      </div>
      <div className="backup-system-actions"><button type="button" className="backup-system-secondary" disabled={busy} onClick={onClose}>{vi ? 'Hủy' : 'Cancel'}</button><button type="button" className="apex-settings-save" disabled={busy || !changed || !!error} onClick={() => void apply()}>{busy ? (vi ? 'Đang nhập…' : 'Importing…') : (vi ? 'Nhập cấu hình' : 'Import configuration')}</button></div>
    </div>
  </div>, document.querySelector('.app-shell') ?? document.body)
}

export  function CronLogTools({ content, jobId, selector = jobId, lang }: { content: string; jobId: string; selector?: string; lang: Lang }) {
  const vi = lang === 'vi'
  const [pages, setPages] = useState<string | null>(null)
  const [cursor, setCursor] = useState<string | undefined>()
  const [query, setQuery] = useState('')
  const [level, setLevel] = useState<'all' | 'error' | 'warning'>('all')
  const [highlight, setHighlight] = useState(false)
  const [busy, setBusy] = useState(false)
  const guard = useRef(false)
  const [error, setError] = useState('')
  const [partial, setPartial] = useState(false)
  const raw = pages ?? content
  const display = useMemo(() => cronLogForDisplay(jobId, raw), [jobId, raw])
  const lines = useMemo(() => filterCronLog(display.content, query, level), [display.content, query, level])
  const capped = raw.length >= 1024 * 1024
  const older = async () => {
    if (guard.current) return
    guard.current = true; setBusy(true); setError('')
    try {
      const page = await readLogPage(selector, cursor)
      if (!page.exists) throw new Error(vi ? 'File log không còn tồn tại.' : 'Log file no longer exists.')
      setPages(previous => previous === null || capped ? page.content : page.content + previous)
      setCursor(page.olderCursor); setPartial('partialLine' in page && page.partialLine === true)
    } catch (e) { setError(apiErrorMessage(e, vi ? 'Không đọc được log cũ. Hãy làm mới.' : 'Could not read older log. Refresh and retry.')) }
    finally { guard.current = false; setBusy(false) }
  }
  return <>
    <div className="ops-log-tools">
      <input type="search" aria-label={vi ? 'Tìm trong log đã tải' : 'Search loaded log'} placeholder={vi ? 'Tìm trong log đã tải…' : 'Search loaded log…'} value={query} onChange={e => setQuery(e.target.value)} />
      <select aria-label={vi ? 'Lọc mức log' : 'Log level'} value={level} onChange={e => setLevel(e.target.value as typeof level)}><option value="all">{vi ? 'Tất cả dòng' : 'All lines'}</option><option value="error">ERROR</option><option value="warning">WARNING</option></select>
      <label><input type="checkbox" checked={highlight} onChange={e => setHighlight(e.target.checked)} />{vi ? 'Tô màu lỗi' : 'Highlight errors'}</label>
      <Button type="button" onClick={() => void older()} disabled={busy || (pages !== null && !cursor)}>{busy ? (vi ? 'Đang tải…' : 'Loading…') : pages === null ? (vi ? 'Xem log phân trang' : 'Paged log') : (vi ? 'Trang cũ hơn' : 'Older page')}</Button>
    </div>
    <div className="cron-log-body" role="status">
      {error && <p className="cron-log-error">{localizeMessage(error, lang)}</p>}
      {partial && <p className="cron-log-note">{vi ? 'Đầu trang có thể là một phần dòng; tải trang cũ hơn để xem phần trước.' : 'The first line may be partial; load the previous page for context.'}</p>}
      {capped && <p className="cron-log-note">{vi ? 'Đã tải khoảng 1 MB. Trang tiếp theo thay cửa sổ hiển thị; không xóa dữ liệu lưu trữ.' : 'About 1 MB loaded. The next page replaces the display window, not stored data.'}</p>}
      {(query || level !== 'all') && <p className="cron-log-note">{vi ? 'Chỉ tìm/lọc phần log đã tải; dòng ngữ cảnh có thể không nằm trong kết quả.' : 'Search/filter applies to loaded text only; context lines may be excluded.'}</p>}
      {lines.some(line => line !== '') ? <pre>{lines.map((line, index) => <span key={index} className={highlight ? `ops-log-${cronLogLineLevel(line)}` : undefined}>{line}{index < lines.length - 1 ? '\n' : ''}</span>)}</pre> : <p>{vi ? 'Không có dòng log khớp.' : 'No matching log lines.'}</p>}
    </div>
  </>
}

export  function ScriptEditDialog({ job, lang, running, onClose, onSaved }: { job: CronJob; lang: Lang; running: boolean; onClose: () => void; onSaved: (path: string) => void }) {
  const vi = lang === 'vi'
  const [document, setDocument] = useState<ScriptDocument | null>(null)
  const [content, setContent] = useState(''), [loading, setLoading] = useState(true), [saving, setSaving] = useState(false), [error, setError] = useState('')
  const dialog = useRef<HTMLDivElement>(null), request = useRef(0), savingRef = useRef(false)
  const dirty = !!document && content !== document.content
  const closeRef = useRef(() => {})
  useEffect(() => { closeRef.current = () => {
    if (savingRef.current) return
    if (dirty && !window.confirm(vi ? 'Bỏ nội dung script chưa lưu?' : 'Discard unsaved script changes?')) return
    onClose()
  } }, [dirty, vi, onClose])
  const load = async () => {
    const id = ++request.current
    setLoading(true); setError(''); setDocument(null)
    try { const result = await readTaskScript(job.instanceKey ? `${job.id}~${job.instanceKey}` : job.id); if (id === request.current) { setDocument(result); setContent(result.content) } }
    catch (error: unknown) { if (id === request.current) setError(apiErrorMessage(error, vi ? 'Không đọc được script.' : 'Could not read script.')) }
    finally { if (id === request.current) setLoading(false) }
  }
  useEffect(() => {
    let alive = true
    const id = ++request.current
    readTaskScript(job.instanceKey ? `${job.id}~${job.instanceKey}` : job.id).then(result => { if (alive && id === request.current) { setDocument(result); setContent(result.content) } })
      .catch((error: unknown) => { if (alive && id === request.current) setError(apiErrorMessage(error, vi ? 'Không đọc được script.' : 'Could not read script.')) })
      .finally(() => { if (alive && id === request.current) setLoading(false) })
    return () => { alive = false }
  }, [job.id, job.instanceKey, vi])
  useEffect(() => {
    let alive = true
    const changed = () => {
      if (dirty || savingRef.current) {
        setError(vi ? 'Cấu hình lưu trữ đã thay đổi. Nội dung đang sửa được giữ nguyên; hãy tải lại trước khi lưu.' : 'Storage settings changed. Unsaved edits are preserved; reload before saving.')
        return
      }
      const id = ++request.current
      setLoading(true); setError('')
      void readTaskScript(job.instanceKey ? `${job.id}~${job.instanceKey}` : job.id)
        .then(result => { if (alive && id === request.current) { setDocument(result); setContent(result.content) } })
        .catch((error: unknown) => { if (alive && id === request.current) setError(apiErrorMessage(error, vi ? 'Không đọc được script tại nơi lưu mới.' : 'Could not read the script at its new location.')) })
        .finally(() => { if (alive && id === request.current) setLoading(false) })
    }
    window.addEventListener('backup-storage-changed', changed)
    return () => { alive = false; window.removeEventListener('backup-storage-changed', changed) }
  }, [dirty, job.id, job.instanceKey, vi])
  useEffect(() => {
    const restore = window.document.activeElement as HTMLElement | null
    const containers = [window.document.body, window.document.querySelector<HTMLElement>('.app-content')].filter((item): item is HTMLElement => !!item)
    const overflow = containers.map(item => item.style.overflow)
    containers.forEach(item => { item.style.overflow = 'hidden' })
    dialog.current?.querySelector<HTMLElement>('button:not(:disabled)')?.focus({ preventScroll: true })
    const key = (event: KeyboardEvent) => {
      if (event.key === 'Escape') { event.preventDefault(); closeRef.current() }
      if (event.key !== 'Tab') return
      const items = Array.from(dialog.current?.querySelectorAll<HTMLElement>('button:not(:disabled), textarea:not(:disabled)') ?? [])
      const first = items[0], last = items.at(-1)
      if (event.shiftKey && window.document.activeElement === first) { event.preventDefault(); last?.focus() }
      else if (!event.shiftKey && window.document.activeElement === last) { event.preventDefault(); first?.focus() }
    }
    window.addEventListener('keydown', key)
    return () => { window.removeEventListener('keydown', key); containers.forEach((item, index) => { item.style.overflow = overflow[index] }); if (restore?.isConnected) restore.focus({ preventScroll: true }) }
  }, [])
  const save = async () => {
    if (!document || savingRef.current || loading || running || !dirty) return
    if (new TextEncoder().encode(content).length > 128 * 1024) { setError(vi ? 'Script vượt quá 128 KB.' : 'Script exceeds 128 KB.'); return }
    if (!window.confirm(vi ? 'Lưu nội dung script? Script chạy với quyền root. Hệ thống kiểm tra cú pháp và lưu bản dự phòng; không chạy tác vụ ngay.' : 'Save script? Scripts run as root. Syntax is checked and a recovery copy is saved; the task does not run now.')) return
    savingRef.current = true; setSaving(true); setError('')
    try { const result = await saveTaskScript(document, content); setDocument(result); setContent(result.content); onSaved(result.recoveryDir ?? '') }
    catch (error: unknown) { setError(apiErrorMessage(error, vi ? 'Không lưu được script. Nếu nội dung máy chủ đã đổi, hãy tải lại trước khi sửa tiếp.' : 'Could not save the script. If the server content changed, reload before continuing to edit.')) }
    finally { savingRef.current = false; setSaving(false) }
  }
  return createPortal(<div className="cron-log-overlay backup-management" onMouseDown={event => { if (event.target === event.currentTarget) closeRef.current() }}>
    <div className="cron-log-dialog cron-script-dialog" ref={dialog} role="dialog" aria-modal="true" aria-labelledby="script-editor-title">
      <div className="cron-log-heading"><div><h2 id="script-editor-title">{vi ? 'Sửa script' : 'Edit script'}: {backupTaskLabels[job.id]?.[lang] ?? job.name}</h2><p>{document?.path ?? job.script}</p></div><div className="cron-log-controls">
        <button type="button" disabled={loading || saving} aria-label={vi ? 'Tải lại script' : 'Reload script'} onClick={() => { if (!dirty || window.confirm(vi ? 'Tải lại và bỏ nội dung chưa lưu?' : 'Reload and discard changes?')) void load() }}><RefreshCw size={17} /></button>
        <button type="button" disabled={saving} onClick={() => closeRef.current()} aria-label={vi ? 'Đóng sửa script' : 'Close script editor'}><X size={18} /></button>
      </div></div>
      <div className="cron-script-body">
        {running && <p className="cron-log-error" role="alert">{vi ? 'Tác vụ đang chạy; chờ kết thúc mới được lưu.' : 'Task is running; wait before saving.'}</p>}
        {error && <p className="cron-log-error" role="alert">{localizeMessage(error, lang)}</p>}
        {loading ? <p>{vi ? 'Đang đọc script...' : 'Loading script...'}</p> : document && <textarea aria-label={vi ? 'Nội dung script' : 'Script content'} value={content} onChange={event => setContent(event.target.value)} disabled={saving} spellCheck={false} autoCapitalize="off" autoComplete="off" wrap="off" />}
        {document?.recoveryDir && <p role="status">{vi ? 'Đã lưu. Bản dự phòng: ' : 'Saved. Recovery copy: '}{document.recoveryDir}</p>}
      </div><div className="backup-system-actions cron-script-footer"><button type="button" className="backup-system-secondary" disabled={saving} onClick={() => closeRef.current()}>{vi ? 'Đóng' : 'Close'}</button><button type="button" className="backup-system-primary" disabled={loading || saving || running || !dirty} onClick={() => void save()}>{saving ? (vi ? 'Đang lưu...' : 'Saving...') : (vi ? 'Lưu script' : 'Save script')}</button></div>
    </div>
  </div>, window.document.querySelector('.app-shell') ?? window.document.body)
}

export  function TaskProgress({ id, lang }: {id: string; lang: Lang}) {
  const [state, setState] = useState<TaskProcess | null>(null)
  const [unavailable, setUnavailable] = useState(false)
  useEffect(() => {
    let stopped = false, timer = 0
    const poll = async () => { try {const next = await taskProcess(id); if (!stopped) {setState(next); setUnavailable(false)}} catch {if (!stopped) setUnavailable(true)} finally {if (!stopped) timer = window.setTimeout(() => void poll(), 5000)} }
    void poll(); return () => {stopped = true; window.clearTimeout(timer)}
  }, [id])
  if (unavailable) return <small>{lang === 'vi' ? 'Chưa đọc được tiến độ' : 'Progress unavailable'}</small>
  if (!state?.running) return null
  return <>{state.progress ? <ProgressView progress={state.progress} lang={lang} /> : <small>{lang === 'vi' ? 'Đang chạy · chưa có số liệu để tính %' : 'Running · percentage unavailable'}</small>}<small>{lang === 'vi' ? 'Đã chạy' : 'Elapsed'}: {state.elapsedSeconds}s</small>{id === 'drive-sync' && <small>{lang === 'vi' ? '% và ETA tính cho lệnh tải/ngày hiện tại, không phải toàn tác vụ.' : '% and ETA refer to the current transfer/day, not the entire job.'}</small>}</>
}

const backupIds = ['backup-site', 'backup-database', 'backup-panel']
const categories = [
  { id: 'all', vi: 'Tất cả tác vụ', en: 'All tasks' },
  { id: 'backup', vi: 'Sao lưu', en: 'Backup' },
  { id: 'drive', vi: 'Đồng bộ Drive', en: 'Drive upload' },
  { id: 'maintenance', vi: 'Dọn dẹp & kiểm tra', en: 'Cleanup & checks' },
]
const normalize = (value: string) => value.normalize('NFD').replace(/[\u0300-\u036f]/g, '').replace(/đ/g, 'd').replace(/Đ/g, 'D').toLowerCase()

type CronDeletionResult = { backupPath?: string; jobs?: CronJob[] }

// A request can remove the schedule before its response is lost or rejected.
// Reconcile against a fresh server read instead of leaving a stale error dialog.
async function deleteCronSchedule(job: CronJob): Promise<CronDeletionResult> {
  if (typeof job.enabled !== 'boolean') throw new Error('Không xác minh được trạng thái lịch cron')
  const selector = cronJobSelector(job)
  try {
    const result = await deleteCronJob(selector, job.schedule, job.enabled)
    if (result?.status !== 'deleted') throw new Error('Không xác nhận được kết quả xóa; hãy làm mới danh sách trước khi thử lại')
    return { backupPath: result.backupPath }
  } catch (error: unknown) {
    let jobs: CronJob[]
    try {
      jobs = await fetchCronJobs()
    } catch {
      throw error
    }
    // Fail closed on an invalid list. A remaining instance, even with a new
    // schedule/state, may be an edit conflict rather than a completed deletion.
    if (!Array.isArray(jobs) || jobs.some(current => !current || typeof current.id !== 'string' || typeof current.schedule !== 'string' || cronJobSelector(current) === selector)) throw error
    return { jobs }
  }
}

function useDialogFocus(active: boolean, ref: RefObject<HTMLElement | null>) {
  useEffect(() => {
    if (!active) return
    const restore = document.activeElement as HTMLElement | null
    const containers = [document.body, document.querySelector<HTMLElement>('.app-content')].filter((element): element is HTMLElement => !!element)
    const overflow = containers.map(element => element.style.overflow)
    containers.forEach(element => { element.style.overflow = 'hidden' })
    const selector = 'button:not(:disabled), select:not(:disabled), input:not(:disabled)'
    ref.current?.querySelector<HTMLElement>(selector)?.focus({ preventScroll: true })
    const trap = (event: KeyboardEvent) => {
      if (event.key !== 'Tab') return
      const items = Array.from(ref.current?.querySelectorAll<HTMLElement>(selector) ?? [])
      const first = items[0], last = items[items.length - 1]
      if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus() }
      else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus() }
    }
    window.addEventListener('keydown', trap)
    return () => {
      window.removeEventListener('keydown', trap)
      containers.forEach((element, index) => { element.style.overflow = overflow[index] })
      if (restore?.isConnected) restore.focus({ preventScroll: true })
    }
  }, [active, ref])
}

export default function CronJobs({ lang }: { lang: Lang }) {
  const vi = lang === 'vi'
  const cron = useCronJobs(15000)
  const jobs = cron.data ?? []
  const [runningJob, setRunningJob] = useState<string | null>(null)
  const [deleteJob, setDeleteJob] = useState<CronJob | null>(null)
  const [deletingJob, setDeletingJob] = useState(false)
  const [deleteError, setDeleteError] = useState<string | null>(null)
  const [stateJob, setStateJob] = useState<CronJob | null>(null)
  const [updatingState, setUpdatingState] = useState(false)
  const [stateError, setStateError] = useState<string | null>(null)
  const [feedback, setFeedback] = useState<{ message: string; error: boolean } | null>(null)
  const [logJob, setLogJob] = useState<CronJob | null>(null)
  const [logData, setLogData] = useState<CronJobLog | null>(null)
  const [logLoading, setLogLoading] = useState(false)
  const [logError, setLogError] = useState<string | null>(null)
  const logRequest = useRef(0)
  const [logRevision, setLogRevision] = useState(0)
  const displayedLog = cronLogForDisplay(logJob?.id ?? '', logData?.content ?? '')
  const [selectedJobId, setSelectedJobId] = useState('backup-database')
  const [editorRevision, setEditorRevision] = useState(0)
  const [taskExpanded, setTaskExpanded] = useState(true)
  const [storage, setStorage] = useState<BackupSystemState | null>(null)
  const [storageBusy, setStorageBusy] = useState(false)
  const [storageRevision, setStorageRevision] = useState(0)
  useEffect(() => {
    const changed = (event: Event) => setStorage((event as CustomEvent<BackupSystemState>).detail)
    window.addEventListener('backup-storage-changed', changed)
    return () => window.removeEventListener('backup-storage-changed', changed)
  }, [])
  useEffect(() => {
    let alive = true
    const update = () => {
      void fetchBackupSystem().then(data => { if (alive) setStorage(data) }).catch(() => {})
    }
    update()
    const interval = window.setInterval(update, 30000)
    return () => { alive = false; window.clearInterval(interval) }
  }, [])
  const [editingJob, setEditingJob] = useState<CronJob | null>(null)
  const [editingBusy, setEditingBusy] = useState(false)
  const [scriptJob, setScriptJob] = useState<CronJob | null>(null)
  const [importFile, setImportFile] = useState<{ filename: string; plan: CronImportRow[] } | null>(null)
  const [transferBusy, setTransferBusy] = useState(false)
  const fileInput = useRef<HTMLInputElement>(null), readingFile = useRef(false)
  const [search, setSearch] = useState('')
  const [category, setCategory] = useState('all')
  const cronTable = useRef<HTMLTableElement>(null)
  const logDialog = useRef<HTMLDivElement>(null)
  const deleteDialog = useRef<HTMLDivElement>(null)
  const stateDialog = useRef<HTMLDivElement>(null)
  useDialogFocus(!!logJob, logDialog)
  useDialogFocus(!!deleteJob, deleteDialog)
  useDialogFocus(!!stateJob, stateDialog)
  const mutating = storageBusy || transferBusy || importFile !== null || editingJob !== null || scriptJob !== null || editingBusy || runningJob !== null || deleteJob !== null || stateJob !== null
  const editorJobs: CronJob[] = Object.entries(backupTaskScripts).map(([id, file]) => ({
    id, name: jobLabels[id][lang], script: `${storage?.config.scriptsDir ?? '/root/scripts'}/${file}`, schedule: '', enabled: false, last_run: '', status: 'never', isNew: true,
  }))
  const currentDeleteJob = deleteJob ? jobs.find(job => cronJobSelector(job) === cronJobSelector(deleteJob) && job.schedule === deleteJob.schedule) : null
  const deleteStale = !!deleteJob && (!currentDeleteJob || currentDeleteJob.enabled !== deleteJob.enabled)
  const deleteRunning = currentDeleteJob?.status === 'running'
  const currentStateJob = stateJob ? jobs.find(job => cronJobSelector(job) === cronJobSelector(stateJob) && job.schedule === stateJob.schedule) : null
  const stateStale = !!stateJob && (!currentStateJob || currentStateJob.enabled !== stateJob.enabled)
  const stateRunning = currentStateJob?.status === 'running'
  const filteredJobs = sortCronJobsByTime(jobs.filter(job => {
    const group = backupIds.includes(job.id) ? 'backup' : job.id === 'drive-sync' ? 'drive' : 'maintenance'
    return (category === 'all' || group === category)
      && normalize(`${jobLabels[job.id]?.vi ?? ''} ${jobLabels[job.id]?.en ?? ''} ${job.name} ${job.script}`).includes(normalize(search.trim()))
  }))
  const firstJob = filteredJobs[0]
  const firstJobKey = firstJob ? `${cronJobSelector(firstJob)}:${firstJob.schedule}` : ''
  useLayoutEffect(() => {
    const heading = cronTable.current?.querySelector<HTMLElement>('thead th:last-child')
    const runButton = cronTable.current?.querySelector<HTMLElement>('tbody .cron-run')
    if (!heading || !runButton) return

    const alignHeading = () => {
      const headingStart = heading.getBoundingClientRect().left + parseFloat(getComputedStyle(heading).paddingLeft)
      const buttonStart = runButton.getBoundingClientRect().left
      heading.style.textIndent = `${Math.max(0, buttonStart - headingStart)}px`
    }
    alignHeading()
    const observer = new ResizeObserver(alignHeading)
    observer.observe(heading)
    observer.observe(runButton)
    return () => observer.disconnect()
  }, [firstJobKey, lang])
  const destination = (job: CronJob) => {
    if (['cleanup-panel', 'integrity-check'].includes(job.id)) return '—'
    if (!storage) return vi ? 'Chưa tải cấu hình' : 'Configuration unavailable'
    const options = job.instanceKey ? storage.config.cronLocations?.[job.instanceKey]?.options : undefined
    if (job.id === 'drive-sync') return `${options?.driveRemote ?? storage.config.driveRemote}:${options?.driveFolder ?? storage.config.driveFolder}`
    const folder = ({ 'backup-site': 'site', 'backup-database': 'database', 'backup-panel': 'panel' } as Record<string, string>)[job.id]
    return `${options?.backupRoot ?? storage.config.backupRoot}${folder ? `/${vi ? '{ngày}' : '{date}'}/${folder}` : ''}`
  }

  const handleExport = () => {
    if (mutating || cron.error || cron.loading) return
    try {
      const contents = exportCronFile(jobs)
      const url = URL.createObjectURL(new Blob([contents], { type: 'application/json' }))
      const link = document.createElement('a')
      link.href = url; link.download = `backup-monitor-cron-${new Date().toISOString().replace(/[:.]/g, '-')}.json`
      document.body.appendChild(link); link.click(); link.remove()
      window.setTimeout(() => URL.revokeObjectURL(url), 1000)
    } catch (error: unknown) { setFeedback({ error: true, message: apiErrorMessage(error, vi ? 'Không xuất được lịch Cron.' : 'Could not export Cron schedules.') }) }
  }

  const handleImportFile = async (file?: File) => {
    if (!file || mutating || readingFile.current || cron.error) return
    readingFile.current = true; setTransferBusy(true); setFeedback(null)
    try {
      if (file.size > cronImportMaxBytes) throw new Error(vi ? 'Tệp JSON không được lớn hơn 64 KB.' : 'JSON file must not exceed 64 KB.')
      const entries = parseCronFile(await file.text())
      setImportFile({ filename: file.name, plan: planCronImport(entries, jobs) })
    } catch (error: unknown) { setFeedback({ error: true, message: apiErrorMessage(error, vi ? 'Không đọc được tệp Cron.' : 'Could not read Cron file.') }) }
    finally { readingFile.current = false; setTransferBusy(false); if (fileInput.current) fileInput.current.value = '' }
  }

  const refreshAfterImport = () => {
    setStorageRevision(value => value + 1); cron.reload()
    window.dispatchEvent(new Event('force-refresh'))
  }

  const openScheduleEditor = (job: CronJob) => {
    if (mutating || cron.error || job.status === 'running') return
    setEditingJob({ ...job })
    setEditingBusy(false)
  }
  const handleCancel = async (job: CronJob) => {
    if (mutating) return
    setRunningJob(cronJobSelector(job)); setFeedback(null)
    try {
      const process = await taskProcess(cronJobSelector(job))
      if (!process.managed || !process.running || !process.runId) throw new Error(vi ? 'Chưa xác minh được tiến trình quản lý đang chạy. Với cron cũ, cần bật ghi nhận cron trước; không dừng tiến trình theo tên.' : 'No verified managed process is running. Enable scheduled tracking for legacy cron jobs first; do not stop processes by name.')
      if (!window.confirm(vi ? `Hủy đúng lần chạy ${process.runId} của ${jobLabels[job.id]?.vi ?? job.name}? Có thể để lại tệp đang tạo dở. Không xóa lịch cron hoặc các bản sao lưu hoàn tất.` : `Cancel run ${process.runId} of ${jobLabels[job.id]?.en ?? job.name}? Partial working files may remain. Schedules and completed backups stay unchanged.`)) return
      await cancelTask(cronJobSelector(job), process.runId)
      setFeedback({ error: false, message: vi ? 'Đã yêu cầu hủy. Chờ tiến trình kết thúc rồi làm mới để xác nhận; chưa coi là hủy xong.' : 'Cancellation requested. Wait for the process to exit, then refresh to confirm; cancellation is not yet complete.' })
      cron.reload()
    } catch (e) { setFeedback({ error: true, message: apiErrorMessage(e, vi ? 'Không hủy được.' : 'Could not cancel.') }) }
    finally { setRunningJob(null) }
  }
  const closeEditor = () => {
    if (!editingBusy) setEditingJob(null)
  }

  const loadLog = useCallback(async (job: CronJob) => {
    const request = ++logRequest.current
    setLogRevision(value => value + 1)
    setLogData(null)
    setLogError(null)
    setLogLoading(true)
    try {
      const data = await fetchCronJobLog(cronJobSelector(job))
      if (request === logRequest.current) setLogData(data)
    } catch (error: unknown) {
      if (request === logRequest.current) {
        setLogError(apiErrorMessage(error, vi ? 'Không thể đọc log cronjob.' : 'Could not read the cron job log.'))
      }
    } finally {
      if (request === logRequest.current) setLogLoading(false)
    }
  }, [vi])

  useEffect(() => {
    if (!logJob) return
    const changed = () => { void loadLog(logJob) }
    window.addEventListener('backup-storage-changed', changed)
    return () => window.removeEventListener('backup-storage-changed', changed)
  }, [logJob, loadLog])

  const closeLog = () => {
    logRequest.current += 1
    setLogJob(null)
  }

  useEffect(() => {
    if (!logJob) return
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        logRequest.current += 1
        setLogJob(null)
      }
    }
    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [logJob])

  const handleRun = async (job: CronJob) => {
    const jobId = job.id
    if (!jobId || mutating) return
    const name = jobLabels[jobId]?.[vi ? 'vi' : 'en'] ?? job.name
    const confirmed = window.confirm(vi
      ? `Chạy ngay cronjob "${name}"? Tác vụ này có thể thay đổi hoặc xóa dữ liệu trên máy chủ.`
      : `Run "${name}" now? This job may change or delete server data.`)
    if (!confirmed) return

    setRunningJob(cronJobSelector(job))
    setFeedback(null)
    try {
      const result = await runCronJob(cronJobSelector(job))
      const cleanupCompleted = jobId === 'cleanup-panel' && result.status === 'completed'
      const deletedCount = result.deletedCount ?? 0
      setFeedback({
        message: cleanupCompleted
          ? (deletedCount > 0
              ? (vi ? `Đã dọn dẹp aaPanel: xóa ${deletedCount} file backup cũ.` : `aaPanel cleanup complete: deleted ${deletedCount} old backup files.`)
              : (vi ? 'Đã dọn dẹp aaPanel: không còn file backup cũ cần xóa.' : 'aaPanel cleanup complete: no old backup files to delete.'))
          : (vi
              ? `Đã gửi yêu cầu chạy "${name}". Hãy kiểm tra log sau khi tác vụ kết thúc.`
              : `Requested a run of "${name}". Check its log after the job finishes.`),
        error: false,
      })
      cron.reload()
      window.dispatchEvent(new Event('force-refresh'))
    } catch (error: unknown) {
      setFeedback({ message: apiErrorMessage(error, vi ? 'Không thể chạy cronjob.' : 'Could not run the cron job.'), error: true })
    } finally {
      setRunningJob(null)
    }
  }

  const closeDelete = () => { if (!deletingJob) { setDeleteJob(null); setDeleteError(null) } }
  useEffect(() => {
    if (!deleteJob || deletingJob) return
    const escape = (event: KeyboardEvent) => {
      if (event.key === 'Escape') { setDeleteJob(null); setDeleteError(null) }
    }
    window.addEventListener('keydown', escape)
    return () => window.removeEventListener('keydown', escape)
  }, [deleteJob, deletingJob])

  const handleDelete = async () => {
    if (!deleteJob || deletingJob || deleteStale || deleteRunning || cron.error || typeof deleteJob.enabled !== 'boolean') return
    setDeletingJob(true); setDeleteError(null); setFeedback(null)
    try {
      const result = await deleteCronSchedule(deleteJob)
      const name = jobLabels[deleteJob.id]?.[lang] ?? deleteJob.name
      const refreshedJobs = result.jobs
      if (refreshedJobs) cron.updateData(() => refreshedJobs)
      setFeedback({ error: false, message: vi
        ? (result.jobs ? `Đã xác nhận lịch cron "${name}" không còn trong danh sách. Giữ nguyên script, log và backup.` : `Đã xóa lịch cron "${name}". Giữ nguyên script, log và backup.${result.backupPath ? ` Bản dự phòng crontab: ${result.backupPath}` : ''}`)
        : (result.jobs ? `Confirmed that the schedule for "${name}" is no longer listed. Scripts, logs and backups are unchanged.` : `Deleted the schedule for "${name}". Scripts, logs and backups are unchanged.${result.backupPath ? ` Crontab recovery copy: ${result.backupPath}` : ''}`) })
      setDeleteError(null)
      setDeleteJob(null)
      cron.reload()
      window.dispatchEvent(new Event('force-refresh'))
    } catch (error: unknown) {
      setDeleteError(apiErrorMessage(error, vi ? 'Không thể xóa lịch cron.' : 'Could not delete the cron schedule.'))
      cron.reload()
    } finally { setDeletingJob(false) }
  }

  const closeState = () => { if (!updatingState) { setStateJob(null); setStateError(null) } }
  useEffect(() => {
    if (!stateJob || updatingState) return
    const escape = (event: KeyboardEvent) => {
      if (event.key === 'Escape') { setStateJob(null); setStateError(null) }
    }
    window.addEventListener('keydown', escape)
    return () => window.removeEventListener('keydown', escape)
  }, [stateJob, updatingState])

  const handleState = async () => {
    if (!stateJob || updatingState || stateStale || stateRunning || cron.error || typeof stateJob.enabled !== 'boolean') return
    setUpdatingState(true); setStateError(null); setFeedback(null)
    try {
      const enabled = !stateJob.enabled
      const result = await updateCronJobState(cronJobSelector(stateJob), enabled, stateJob.schedule, stateJob.enabled)
      const name = jobLabels[stateJob.id]?.[lang] ?? stateJob.name
      setFeedback({ error: false, message: vi
        ? `Đã ${enabled ? 'bật' : 'tạm dừng'} lịch cron "${name}". Không chạy script ngay. Bản dự phòng crontab: ${result.backupPath}`
        : `${enabled ? 'Enabled' : 'Paused'} the schedule for "${name}". No script was run. Crontab recovery copy: ${result.backupPath}` })
      setStateJob(null)
      cron.reload()
      window.dispatchEvent(new Event('force-refresh'))
    } catch (error: unknown) {
      setStateError(apiErrorMessage(error, vi ? 'Không thể đổi trạng thái cron.' : 'Could not change the cron state.'))
      cron.reload()
    } finally { setUpdatingState(false) }
  }

  return (
    <div className="cron-page backup-management animate-fade-in">
      <div className="cron-page-heading">
        <div>
          <h1>Cron</h1>
          <p>{vi ? 'Cấu hình nơi lưu trữ và quản lý lịch chạy.' : 'Storage configuration and scheduled tasks.'}</p>
        </div>
      </div>
      <div className="backup-task-editor-anchor"><BackupSystemSettings key={storageRevision} lang={lang} expanded={taskExpanded} onToggle={() => setTaskExpanded(!taskExpanded)} onReload={cron.reload} locked={transferBusy || importFile !== null || editingJob !== null || scriptJob !== null || runningJob !== null || deleteJob !== null || stateJob !== null} onStateChange={setStorage} onBusyChange={setStorageBusy}
        task={{ jobs: editorJobs, selectedId: selectedJobId, revision: editorRevision, loading: cron.loading, error: cron.error, onSelect: id => { setSelectedJobId(id); setEditorRevision(value => value + 1) }, onApplied: saved => { cron.updateData(current => applySavedCronSchedule(current, saved)); setEditorRevision(value => value + 1) } }} /></div>
      {feedback && <div className={`cron-feedback backup-management-feedback ${feedback.error ? 'cron-feedback--error' : ''}`} role={feedback.error ? 'alert' : 'status'}>{localizeMessage(feedback.message, lang)}</div>}
      <section className="cron-panel backup-task-panel" id="backup-tasks" aria-labelledby="backup-tasks-title">
        <div className="cron-panel-heading">
          <div>
            <h2 id="backup-tasks-title">{vi ? 'Danh sách tác vụ' : 'Task list'} <span className="backup-count">{jobs.length}</span></h2>
          </div>
        </div>
        <div className="backup-task-toolbar">
          <div className="cron-transfer-actions">
            <input ref={fileInput} type="file" accept=".json,application/json" hidden onChange={event => { void handleImportFile(event.currentTarget.files?.[0]) }} />
            <button type="button" className="backup-system-secondary" disabled={mutating || !!cron.error || cron.loading || !storage?.ready} onClick={() => fileInput.current?.click()} title={vi ? 'Nhập lịch chạy từ tệp JSON đã xuất trên dashboard' : 'Import schedules from a dashboard JSON export'}>Import</button>
            <button type="button" className="backup-system-secondary cron-export-button" disabled={mutating || !!cron.error || cron.loading || !jobs.length} onClick={handleExport} title={vi ? 'Xuất lịch chạy ra JSON; không chứa mật khẩu, script hay nhật ký' : 'Export schedules as JSON; excludes credentials, scripts and logs'}>Export</button>
          </div>
          <select value={category} onChange={event => setCategory(event.target.value)} aria-label={vi ? 'Lọc loại tác vụ' : 'Filter task category'}>{categories.map(item => <option key={item.id} value={item.id}>{item[vi ? 'vi' : 'en']}</option>)}</select>
          <div className="backup-task-search"><Search size={16} /><input value={search} onChange={event => setSearch(event.target.value)} placeholder={vi ? 'Tìm tên tác vụ hoặc script...' : 'Search task or script...'} aria-label={vi ? 'Tìm tác vụ' : 'Search tasks'} />{search && <button type="button" onClick={() => setSearch('')} aria-label={vi ? 'Xóa tìm kiếm' : 'Clear search'}><X size={14} /></button>}</div>
        </div>
        {cron.error && <div className="cron-message cron-error" role="alert">{vi ? 'Không thể cập nhật danh sách: ' : 'Could not update the list: '}{cron.error}</div>}
        {cron.loading && !cron.data ? (
          <div className="cron-message">{vi ? 'Đang tải cronjob...' : 'Loading cron jobs...'}</div>
        ) : jobs.length === 0 ? (
          <div className="cron-message">{vi ? 'Không tìm thấy lịch chạy script nào.' : 'No scheduled scripts found.'}</div>
        ) : filteredJobs.length === 0 ? <div className="cron-message">{vi ? 'Không có tác vụ phù hợp. Hãy đổi bộ lọc hoặc từ khóa.' : 'No matching tasks. Change the filter or search.'}</div> : (
          <div className="cron-table-wrap">
            <table ref={cronTable} className="cron-table" role="table" aria-labelledby="backup-tasks-title">
              <thead role="rowgroup">
                <tr role="row">
                  <th scope="col" role="columnheader">{vi ? 'Tác vụ' : 'Job'}</th>
                  <th scope="col" role="columnheader">{vi ? 'Lịch tự động' : 'Automatic schedule'}</th>
                  <th scope="col" role="columnheader">{vi ? 'Lịch chạy' : 'Schedule'}</th>
                  <th scope="col" role="columnheader">{vi ? 'Nơi lưu' : 'Destination'}</th>
                  <th scope="col" role="columnheader">{vi ? 'Lần chạy gần nhất' : 'Last run'}</th>
                  <th scope="col" role="columnheader">{vi ? 'Hành động' : 'Action'}</th>
                </tr>
              </thead>
              <tbody role="rowgroup">
                {filteredJobs.map((job) => (
                  <tr key={`${cronJobSelector(job)}:${job.schedule}`} role="row">
                    <td role="cell" data-label={vi ? 'Tác vụ' : 'Job'}>
                      <strong>{jobLabels[job.id]?.[vi ? 'vi' : 'en'] ?? job.name}</strong>
                      {job.status === 'running' && <TaskProgress id={cronJobSelector(job)} lang={lang} />}
                    </td>
                    <td role="cell" data-label={vi ? 'Lịch tự động' : 'Automatic schedule'}><button className={`cron-schedule-state ${job.enabled === true ? 'is-enabled' : job.enabled === false ? 'is-paused' : ''}`} type="button" disabled={mutating || !!cron.error || job.status === 'running' || typeof job.enabled !== 'boolean'} onClick={() => { setStateJob({ ...job }); setStateError(null) }} aria-label={`${job.enabled ? (vi ? 'Tạm dừng cron' : 'Pause cron') : (vi ? 'Bật cron' : 'Enable cron')}: ${jobLabels[job.id]?.[lang] ?? job.name}`}>{job.enabled === true ? <>{vi ? 'Đã bật' : 'Enabled'} <Play size={12} fill="currentColor" /></> : job.enabled === false ? <>{vi ? 'Tạm dừng' : 'Paused'} <Pause size={12} fill="currentColor" /></> : (vi ? 'Chưa xác minh' : 'Unverified')}</button></td>
                    <td role="cell" data-label={vi ? 'Lịch chạy' : 'Schedule'}>
                      <span>{formatCronSchedule(job.schedule, lang)}</span>
                    </td>
                    <td role="cell" data-label={vi ? 'Nơi lưu' : 'Destination'} className="backup-destination"><span title={destination(job)}>{destination(job)}</span></td>
                    <td role="cell" data-label={vi ? 'Lần chạy gần nhất' : 'Last run'}>
                      <strong className="cron-run-result" data-status={job.status}>{job.status === 'never' ? (vi ? 'Chưa ghi nhận' : 'No run recorded') : statusLabel(job.status, lang)}</strong>
                      {(job.last_run || job.tracked_at) && <small>{job.last_run || job.tracked_at}</small>}
                    </td>
                    <td role="cell" data-label={vi ? 'Hành động' : 'Action'}>
                      <div className="cron-actions">
                        <button className="cron-run" type="button" onClick={() => handleRun(job)} disabled={mutating || !job.id || job.status === 'running'} aria-label={`${vi ? 'Chạy ngay' : 'Run now'}: ${jobLabels[job.id]?.[vi ? 'vi' : 'en'] ?? job.name}`}>
                          <Play size={14} fill="currentColor" />
                          {runningJob === cronJobSelector(job)
                            ? (job.id === 'cleanup-panel' ? (vi ? 'Đang dọn...' : 'Cleaning...') : (vi ? 'Đang gửi...' : 'Starting...'))
                            : (vi ? 'Chạy ngay' : 'Run now')}
                        </button>
                        <button className="cron-view-log" type="button" onClick={() => openScheduleEditor(job)} disabled={mutating || job.status === 'running'} aria-label={`${vi ? 'Sửa tác vụ' : 'Edit task'}: ${jobLabels[job.id]?.[vi ? 'vi' : 'en'] ?? job.name}`}>
                          <Pencil size={14} /> {vi ? 'Sửa' : 'Edit'}
                        </button>
                        <button className="cron-view-log" type="button" onClick={() => { setLogJob(job); void loadLog(job) }} aria-label={`${vi ? 'Xem log' : 'View log'}: ${jobLabels[job.id]?.[vi ? 'vi' : 'en'] ?? job.name}`}><Eye size={15} /> Log</button>
                        {job.status === 'running' && <button className="cron-view-log cron-delete" type="button" disabled={mutating} onClick={() => void handleCancel(job)}>{vi ? 'Hủy chạy' : 'Cancel run'}</button>}
                        <button className="cron-view-log" type="button" disabled={mutating || !!cron.error || job.status === 'running'} onClick={() => setScriptJob(job)} aria-label={`${vi ? 'Sửa script' : 'Edit script'}: ${jobLabels[job.id]?.[lang] ?? job.name}`}>{vi ? 'Sửa script' : 'Edit script'}</button>
                        <button className="cron-view-log cron-delete" type="button" onClick={() => { setDeleteJob({ ...job }); setDeleteError(null) }} disabled={mutating || !!cron.error || job.status === 'running' || typeof job.enabled !== 'boolean'} aria-label={`${vi ? 'Xóa cron' : 'Delete cron'}: ${jobLabels[job.id]?.[vi ? 'vi' : 'en'] ?? job.name}`}><Trash2 size={14} />{vi ? 'Xóa' : 'Delete'}</button>
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>

      {editingJob && <BackupTaskEditDialog lang={lang} job={editingJob} jobs={jobs} loading={cron.loading} error={cron.error} busy={editingBusy} locked={storageBusy || runningJob !== null || deleteJob !== null || stateJob !== null} onBusyChange={setEditingBusy} onClose={closeEditor} onReload={cron.reload} onSaved={saved => { cron.updateData(current => applySavedCronSchedule(current, saved)); setEditingJob(null); setEditingBusy(false); setStorageRevision(value => value + 1); setFeedback({ error: false, message: vi ? 'Đã lưu thay đổi tác vụ.' : 'Task changes saved.' }); cron.reload() }} />}

      {importFile && <CronImportDialog lang={lang} filename={importFile.filename} plan={importFile.plan} onClose={() => { if (!transferBusy) setImportFile(null) }} onBusyChange={setTransferBusy} onUpdated={refreshAfterImport} onSaved={count => { setImportFile(null); setFeedback({ error: false, message: vi ? `Đã nhập ${count} lịch Cron. Không chạy backup ngay.` : `Imported ${count} Cron schedules. No backup was started immediately.` }) }} />}

      {scriptJob && <ScriptEditDialog job={scriptJob} lang={lang} running={!!cron.error || jobs.find(job => cronJobSelector(job) === cronJobSelector(scriptJob))?.status === 'running'} onClose={() => setScriptJob(null)} onSaved={path => { setFeedback({ error: false, message: vi ? `Đã lưu script. Không chạy tác vụ ngay. Bản dự phòng: ${path}` : `Script saved. No task was started immediately. Recovery copy: ${path}` }); cron.reload() }} />}

      {stateJob && createPortal(<div className="cron-log-overlay backup-management" role="presentation" onMouseDown={event => { if (event.target === event.currentTarget) closeState() }}>
        <div className="cron-log-dialog cron-delete-dialog" ref={stateDialog} role="dialog" aria-modal="true" aria-labelledby="cron-state-title" aria-describedby="cron-state-description">
          <div className="cron-log-heading"><h2 id="cron-state-title">{stateJob.enabled ? (vi ? 'Tạm dừng lịch cron?' : 'Pause cron schedule?') : (vi ? 'Bật lịch cron?' : 'Enable cron schedule?')}</h2><button className="backup-system-secondary" type="button" disabled={updatingState} onClick={closeState} aria-label={vi ? 'Đóng xác nhận trạng thái' : 'Close state confirmation'}><X size={18} /></button></div>
          <div className="cron-delete-body">
            <strong>{jobLabels[stateJob.id]?.[lang] ?? stateJob.name}</strong>
            <p>{formatCronSchedule(stateJob.schedule, lang)} · {vi ? 'giờ máy chủ' : 'server time'}</p>
            <p id="cron-state-description">{stateJob.enabled
              ? (vi ? 'Ngừng các lần chạy tự động tiếp theo; vẫn có thể dùng “Chạy ngay”. Không xóa script, log hay backup.' : 'Stop future automatic runs; “Run now” remains available. Scripts, logs and backups are kept.')
              : (vi ? 'Khôi phục chạy tự động theo lịch đã lưu. Không chạy script ngay và không chạy bù các lần đã bỏ lỡ.' : 'Resume automatic runs on the saved schedule. This does not run the script now or catch up missed runs.')}</p>
            {stateStale && <p className="cron-log-error" role="alert">{vi ? 'Lịch hoặc trạng thái đã thay đổi. Hãy đóng và làm mới danh sách.' : 'The schedule or state changed. Close and refresh the list.'}</p>}
            {stateRunning && <p className="cron-log-error" role="alert">{vi ? 'Tác vụ đang chạy. Hãy chờ kết thúc rồi đổi trạng thái.' : 'The task is running. Wait for it to finish before changing the state.'}</p>}
            {stateError && <p className="cron-log-error" role="alert">{localizeMessage(stateError, lang)}</p>}
            {cron.error && <p className="cron-log-error" role="alert">{vi ? 'Không thể xác minh lịch hiện tại: ' : 'Could not verify the current schedule: '}{cron.error}</p>}
            <div className="backup-system-actions"><button className="backup-system-secondary" type="button" onClick={closeState} disabled={updatingState}>{vi ? 'Hủy' : 'Cancel'}</button><button className="backup-system-primary" type="button" onClick={() => { void handleState() }} disabled={updatingState || stateStale || stateRunning || !!cron.error}>{updatingState ? (vi ? 'Đang lưu...' : 'Saving...') : stateJob.enabled ? (vi ? 'Tạm dừng cron' : 'Pause cron') : (vi ? 'Bật cron' : 'Enable cron')}</button></div>
          </div>
        </div>
      </div>, document.querySelector('.app-shell') ?? document.body)}

      {deleteJob && createPortal(<div className="cron-log-overlay backup-management" role="presentation" onMouseDown={event => { if (event.target === event.currentTarget) closeDelete() }}>
        <div className="cron-log-dialog cron-delete-dialog" ref={deleteDialog} role="dialog" aria-modal="true" aria-labelledby="cron-delete-title" aria-describedby="cron-delete-description">
          <div className="cron-log-heading"><h2 id="cron-delete-title">{vi ? 'Xóa lịch cron?' : 'Delete cron schedule?'}</h2><button className="backup-system-secondary" type="button" disabled={deletingJob} onClick={closeDelete} aria-label={vi ? 'Đóng xác nhận xóa' : 'Close delete confirmation'}><X size={18} /></button></div>
          <div className="cron-delete-body">
            <strong>{jobLabels[deleteJob.id]?.[lang] ?? deleteJob.name}</strong>
            <p>{formatCronSchedule(deleteJob.schedule, lang)} · {vi ? 'giờ máy chủ' : 'server time'}</p>
            <p id="cron-delete-description">{vi ? 'Chỉ xóa lịch chạy tự động của tác vụ này. Không xóa file script, log hoặc bản backup. Hệ thống lưu bản dự phòng crontab trước khi thay đổi.' : 'Only this automatic schedule is removed. Script files, logs and backups are kept. A recovery copy of the crontab is saved before changing it.'}</p>
            {deleteStale && !deletingJob && !deleteError && <p className="cron-log-error" role="alert">{vi ? 'Lịch đã thay đổi hoặc tác vụ không còn. Hãy đóng và làm mới danh sách.' : 'The schedule changed or the task is gone. Close and refresh the list.'}</p>}
            {deleteRunning && <p className="cron-log-error" role="alert">{vi ? 'Tác vụ đang chạy. Hãy chờ kết thúc rồi xóa lịch.' : 'The task is running. Wait for it to finish before deleting the schedule.'}</p>}
            {deleteError && <p className="cron-log-error" role="alert">{localizeMessage(deleteError, lang)}</p>}
            {cron.error && <p className="cron-log-error" role="alert">{vi ? 'Không thể xác minh lịch hiện tại: ' : 'Could not verify the current schedule: '}{cron.error}</p>}
            <div className="backup-system-actions"><button className="backup-system-secondary" type="button" onClick={closeDelete} disabled={deletingJob}>{vi ? 'Hủy' : 'Cancel'}</button><button className="cron-delete-confirm" type="button" onClick={() => { void handleDelete() }} disabled={deletingJob || deleteStale || deleteRunning || !!cron.error}>{deletingJob ? (vi ? 'Đang xóa...' : 'Deleting...') : (vi ? 'Xóa lịch cron' : 'Delete schedule')}</button></div>
          </div>
        </div>
      </div>, document.querySelector('.app-shell') ?? document.body)}

      {logJob && createPortal(
        <div className="cron-log-overlay backup-management" role="presentation" onMouseDown={(event) => { if (event.target === event.currentTarget) closeLog() }}>
          <div className="cron-log-dialog" ref={logDialog} role="dialog" aria-modal="true" aria-labelledby="cron-log-title">
            <div className="cron-log-heading">
              <div>
                <h2 id="cron-log-title">{vi ? 'Log cronjob' : 'Cron job log'}: {jobLabels[logJob.id]?.[vi ? 'vi' : 'en'] ?? logJob.name}</h2>
                <p>{vi ? '200 dòng cuối, tối đa 64 KB' : 'Last 200 lines, up to 64 KB'}</p>
                {!logLoading && !logError && displayedLog.hiddenAdvisories > 0 && <p>{vi ? `Đã ẩn ${displayedLog.hiddenAdvisories} cảnh báo bộ lọc rclone cũ; log gốc không đổi.` : `Hidden ${displayedLog.hiddenAdvisories} old rclone filter advisories; the original log is unchanged.`}</p>}
              </div>
              <div className="cron-log-controls">
                <button type="button" onClick={() => void loadLog(logJob)} disabled={logLoading} aria-label={vi ? 'Làm mới log' : 'Refresh log'} title={vi ? 'Làm mới log' : 'Refresh log'}><RefreshCw size={17} /></button>
                <button type="button" onClick={closeLog} aria-label={vi ? 'Đóng' : 'Close'} title={vi ? 'Đóng' : 'Close'}><X size={18} /></button>
              </div>
            </div>
            {!logLoading && !logError && logData?.exists && logData.content ? <CronLogTools key={`${cronJobSelector(logJob)}:${logRevision}`} jobId={logJob.id} selector={cronJobSelector(logJob)} content={logData.content} lang={lang} /> : <div className="cron-log-body" role="status">
              {logLoading ? (vi ? 'Đang đọc log...' : 'Loading log...')
                : logError ? <span className="cron-log-error">{localizeMessage(logError, lang)}</span>
                  : !logData?.exists ? (vi ? 'Chưa có file log cho tác vụ này.' : 'No log file for this job yet.')
                    : displayedLog.content ? <pre>{displayedLog.content}</pre>
                      : displayedLog.hiddenAdvisories ? (vi ? 'Không còn dòng log khác sau khi ẩn cảnh báo bộ lọc cũ.' : 'No other log entries after hiding old filter advisories.')
                      : (vi ? 'File log đang trống.' : 'The log file is empty.')}
            </div>}
          </div>
        </div>, document.querySelector('.app-shell') ?? document.body
      )}

    </div>
  )
}
