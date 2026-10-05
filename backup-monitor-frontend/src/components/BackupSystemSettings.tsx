import { useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { ChevronDown, Folder, FolderPlus, RefreshCw, X } from 'lucide-react'
import { apiErrorMessage, clearSnapshotsCache } from '../api'
import { applyBackupSystem, createBackupFolder, fetchBackupSystem, listBackupFolders, previewBackupSystem } from '../backupSystemApi'
import type { BackupSource, BackupSystemConfig, BackupSystemState, ConfigurationPreview, FolderList, FolderRequest } from '../backupSystemApi'
import type { Lang } from '../language'
import type { CronJob } from '../types'
import BackupTaskForm from './BackupTaskForm'
import { backupSourceForTask, buildCronScheduleChange, cronScheduleIsStale, makeCronScheduleDraft, sourceConfigKeys, proposedCronSchedule } from '../backupTasks'
import type { CronScheduleDraft } from '../backupTasks'
import { formatCronSchedule } from '../utils'

type DirectoryKey = 'backupRoot' | 'scriptsDir' | 'logsDir' | 'driveFolder'
type SourceKey = 'siteSource' | 'databaseSource' | 'panelSource'
const labels: Record<DirectoryKey, [string, string]> = {
  backupRoot: ['Thư mục backup trên AlmaLinux', 'AlmaLinux backup directory'], scriptsDir: ['Thư mục scripts', 'Scripts directory'],
  logsDir: ['Thư mục nhật ký (log)', 'Log directory'], driveFolder: ['Thư mục backup trên Google Drive', 'Google Drive backup folder'],
}

function FolderPicker({ request, vi, close, choose }: { request: FolderRequest; vi: boolean; close: () => void; choose: (path: string) => void }) {
  const sourceOnly = request.purpose.startsWith('source-')
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
    setBusy(true); setError('')
    try { await createBackupFolder({ ...request, path: value }); await load(value); setName('') }
    catch (error: unknown) { setError(apiErrorMessage(error, vi ? 'Không tạo được thư mục.' : 'Could not create folder.')); setBusy(false) }
  }
  const selectable = listing?.exists && listing.path === path && path !== '/' && (request.location !== 'drive' || !!path)
    && !(request.purpose === 'scripts' && ['/root', '/opt/backup-monitor'].includes(path))
  // Outside the animated, scrollable page; the shell keeps the active theme variables.
  return createPortal(<div className="backup-folder-overlay backup-management"><div className="backup-folder-dialog" ref={dialog} role="dialog" aria-modal="true" aria-labelledby="folder-picker-title">
    <div className="backup-system-heading"><h2 id="folder-picker-title">{sourceOnly ? (vi ? 'Chọn thư mục dữ liệu cần sao lưu' : 'Choose source data folder') : (vi ? 'Chọn hoặc tạo thư mục' : 'Choose or create a folder')} · {request.location === 'local' ? 'AlmaLinux' : 'Google Drive'}</h2><button type="button" className="backup-system-secondary" onClick={close} aria-label={vi ? 'Đóng' : 'Close'}><X size={18} /></button></div>
    <p className="backup-system-note">{sourceOnly ? (vi ? 'Chọn thư mục dữ liệu đã tồn tại trên máy chủ. Chọn ở đây chưa đổi script; cần xác nhận áp dụng ở cuối form.' : 'Choose an existing server data folder. Selecting it does not update scripts until you confirm and apply the form.') : (vi ? 'Tên thư mục dùng chữ không dấu, số, -, _, .; không có khoảng trắng. Tạo thư mục chưa thay đổi nơi lưu trữ đang dùng.' : 'Use ASCII letters, numbers, -, _, and .; no spaces. Creating a folder does not change active storage.')}</p>
    <div className="backup-system-path"><input aria-label={vi ? 'Đường dẫn thư mục' : 'Folder path'} value={path} onChange={e => setPath(e.target.value)} disabled={busy} /><button type="button" className="backup-system-secondary" disabled={busy} onClick={() => void load(path)}>{vi ? 'Mở' : 'Open'}</button>{request.location === 'local' && <button type="button" className="backup-system-secondary" disabled={busy} onClick={() => void load('/')}>{vi ? 'Thư mục gốc /' : 'Server root /'}</button>}</div>
    <div className="backup-folder-list" aria-busy={busy}>{busy ? <p>{vi ? 'Đang đọc thư mục...' : 'Loading folders...'}</p> : <>
      {listing?.parent !== null && listing?.parent !== undefined && listing.parent !== path && <button type="button" onClick={() => void load(listing.parent!)}>← {vi ? 'Thư mục cha' : 'Parent folder'}</button>}
      {listing?.entries.map(entry => <button type="button" key={entry.path} onClick={() => void load(entry.path)}><Folder size={16} />{entry.name}</button>)}
      {listing?.exists && !listing.entries.length && <p>{vi ? 'Chưa có thư mục con.' : 'No subfolders.'}</p>}
      {listing && !listing.exists && <p>{vi ? 'Thư mục chưa tồn tại.' : 'Folder does not exist.'}</p>}
    </>}</div>
    {error && <p role="alert" className="backup-system-error">{error}</p>}
    {!sourceOnly && <div className="backup-system-path"><input aria-label={vi ? 'Tên thư mục mới' : 'New folder name'} placeholder={vi ? 'Tên thư mục con mới' : 'New subfolder name'} value={name} onChange={e => setName(e.target.value)} disabled={busy} /><button type="button" className="backup-system-secondary" disabled={busy || !/^[A-Za-z0-9_-][A-Za-z0-9_.-]*$/.test(name)} onClick={() => void create(`${path.replace(/\/$/, '')}/${name}`.replace(/^\//, request.location === 'drive' ? '' : '/'))}><FolderPlus size={16} />{vi ? 'Tạo thư mục con' : 'Create subfolder'}</button></div>}
    <div className="backup-system-actions">{!sourceOnly && <button type="button" className="backup-system-secondary" disabled={busy || !path || path === '/'} onClick={() => void create(path)}>{vi ? 'Tạo đường dẫn này nếu chưa có' : 'Create this path if missing'}</button>}<button type="button" className="apex-settings-save" disabled={busy || !selectable} onClick={() => choose(path)}>{vi ? 'Chọn thư mục này' : 'Use this folder'}</button></div>
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
  const [feedback, setFeedback] = useState<{ text: string; error: boolean } | null>(null)
  const [picker, setPicker] = useState<{ key: DirectoryKey | SourceKey; request: FolderRequest } | null>(null)
  const [localExpanded, setExpanded] = useState(true)
  const expanded = controlledExpanded ?? localExpanded
  const disabled = busy || locked
  const selected = task?.initialJob ? task.jobs.find(job => job.id === task.initialJob?.id) : task?.jobs.find(job => job.id === task.selectedId) ?? task?.jobs.find(job => job.id === 'backup-database') ?? task?.jobs[0]
  const sourceId = selected?.id ?? 'backup-site'
  const sourceKey = sourceConfigKeys[sourceId]
  const source = draft && state ? backupSourceForTask(draft, state, sourceId) : null
  const taskDraft = selected ? (scheduleDraft?.id === selected.id && scheduleDraft.revision === task?.revision ? scheduleDraft : makeCronScheduleDraft(task?.initialJob ?? selected, task?.revision)) : null
  const schedule = taskDraft ? buildCronScheduleChange(taskDraft) : null
  const scheduleBlocked = !!task && (!selected || !!task.error || (task.loading && !task.jobs.length) || !!(selected && taskDraft && (
    cronScheduleIsStale(taskDraft, selected) || !proposedCronSchedule(taskDraft) || selected.status === 'running' || typeof selected.enabled !== 'boolean'
  )))
  const formKey = JSON.stringify([draft, state?.version, taskDraft, schedule])
  const preview = previewFormKey === formKey ? previewResponse : null
  useEffect(() => { onStateChange?.(state) }, [state, onStateChange])
  useEffect(() => { onBusyChange?.(busy) }, [busy, onBusyChange])
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
    try { const result = await fetchBackupSystem(); setState(result); setDraft(result.config); setScheduleDraft(selected ? makeCronScheduleDraft(selected, task?.revision) : null); onReload?.() }
    catch (error: unknown) { setFeedback({ text: apiErrorMessage(error, vi ? 'Không tải được cấu hình.' : 'Could not load settings.'), error: true }) }
    finally { setBusy(false) }
  }
  const edit = (key: DirectoryKey | 'driveRemote', value: string) => { if (draft) setDraft({ ...draft, [key]: value }); setPreview(null); setFeedback(null) }
  const editSource = (key: SourceKey, value: BackupSource) => { if (draft) setDraft({ ...draft, [key]: value }); setPreview(null); setFeedback(null) }
  const chooseSource = (key: SourceKey, path: string) => editSource(key, { path, scope: (key === 'databaseSource' && (path === '/www/server/data' || (state?.sources?.['backup-database']?.path === path && state.sources['backup-database'].scope === 'children'))) || (key === 'siteSource' && path === '/www/wwwroot') ? 'children' : 'folder' })
  const apply = async () => {
    if (!draft || !state || disabled || !state.ready || scheduleBlocked || applying.current) return
    applying.current = true
    setBusy(true); setFeedback(null); setPreview(null)
    try {
      const checked = await previewBackupSystem(draft, state.version, schedule)
      if (schedule && taskDraft && (checked.schedule?.id !== schedule.id || checked.schedule?.proposed !== proposedCronSchedule(taskDraft) || checked.schedule?.previous !== schedule.expectedSchedule)) {
        throw new Error(vi ? 'Backend chưa xác nhận đúng chu kỳ đã chọn. Hãy chạy lại backend bản mới rồi tải lại cấu hình.' : 'The backend did not confirm the selected recurrence. Restart the updated backend and reload settings.')
      }
      setPreview(checked); setPreviewFormKey(formKey)
      if (!checked.changed) {
        setFeedback({ error: false, text: vi ? 'Không có thay đổi để áp dụng.' : 'No changes to apply.' })
        return
      }
      const summary = [
        vi ? `Áp dụng thay đổi? ${checked.filesToMove} tệp backup / ${checked.daysToMove} thư mục ngày sẽ chuyển; ${checked.scriptsToUpdate} script cập nhật; ${checked.logsToMove} log sao chép.` : `Apply changes? Move ${checked.filesToMove} backup files / ${checked.daysToMove} dated folders; update ${checked.scriptsToUpdate} scripts; copy ${checked.logsToMove} logs.`,
        ...(checked.schedule ? [`${vi ? 'Lịch chạy' : 'Schedule'}: ${checked.schedule.previous ? formatCronSchedule(checked.schedule.previous, lang) : (vi ? 'Chưa có lịch' : 'Not scheduled')} → ${formatCronSchedule(checked.schedule.proposed, lang)}${checked.schedule.enabled ? '' : (vi ? ' · giữ trạng thái tạm dừng' : ' · stays paused')}`] : []),
        ...(checked.sources?.map(change => `${vi ? 'Nguồn dữ liệu' : 'Source data'}: ${change.previous.path} → ${change.proposed.path}`) ?? []),
        ...(checked.driveChanged ? [vi ? `Dashboard sẽ đọc thư mục Drive mới: ${draft.driveFolder}. Backup cũ vẫn ở thư mục cũ.` : `Dashboard will read the new Drive folder: ${draft.driveFolder}. Existing backups stay in the old folder.`] : []),
        vi ? 'Không di chuyển/xóa dữ liệu nguồn; không chuyển/xóa backup cũ trên Drive; không chạy backup ngay.' : 'Source data is not moved/deleted; existing Drive backups are not moved/deleted; no backup runs now.',
      ].join('\n')
      if (!window.confirm(summary)) return
      const result = await applyBackupSystem(draft, state.version, checked.token, schedule)
      setState(result); setDraft(result.config); setPreview(null); setScheduleDraft(null); onReload?.()
      clearSnapshotsCache(); window.dispatchEvent(new Event('force-refresh'))
      setFeedback({ error: false, text: [vi ? 'Đã lưu cấu hình và lịch chạy đã chọn. Không chạy backup ngay; chính sách 14 ngày không thay đổi.' : 'Selected configuration and schedule saved. No backup ran; the 14-day retention policy is unchanged.', ...(result.warnings || [])].join(' ') })
      task?.onApplied?.(checked.schedule)
    } catch (error: unknown) { setPreview(null); setFeedback({ text: apiErrorMessage(error, vi ? 'Không áp dụng được cấu hình. Hãy tải lại rồi thử lại.' : 'Could not apply. Reload and try again.'), error: true }) }
    finally { applying.current = false; setBusy(false) }
  }
  const open = (key: DirectoryKey) => {
    if (draft) setPicker({ key, request: { location: key === 'driveFolder' ? 'drive' : 'local', purpose: key === 'scriptsDir' ? 'scripts' : key === 'logsDir' ? 'logs' : 'backup', path: draft[key], remote: draft.driveRemote } })
  }
  return <section className="apex-settings-panel backup-system-panel" id={`${idPrefix}backup-storage`} aria-labelledby={`${idPrefix}backup-storage-title`}>
    <div className="backup-storage-heading"><h2 id={`${idPrefix}backup-storage-title`}>{mode === 'edit' ? (vi ? 'Sửa tác vụ' : 'Edit task') : <button type="button" aria-expanded={expanded} aria-controls={`${idPrefix}backup-storage-fields`} onClick={() => onToggle ? onToggle() : setExpanded(!expanded)}>{vi ? 'Thêm tác vụ' : 'Add task'}<ChevronDown size={17} className={expanded ? 'is-expanded' : ''} /></button>}</h2><div className="backup-edit-heading-actions"><button type="button" className="backup-system-secondary" disabled={disabled} onClick={() => void reload()} aria-label={vi ? 'Tải lại cấu hình' : 'Reload configuration'}><RefreshCw size={16} /></button>{mode === 'edit' && <button type="button" className="backup-system-secondary" disabled={disabled} onClick={onClose} aria-label={vi ? 'Đóng chỉnh sửa' : 'Close editor'}><X size={18} /></button>}</div></div>
    <div id={`${idPrefix}backup-storage-fields`} className="backup-storage-body" hidden={!expanded}>
    {feedback && <p className={feedback.error ? 'backup-system-error' : 'backup-system-success'} role={feedback.error ? 'alert' : 'status'}>{feedback.text}</p>}
    {!draft || !state ? <p className="backup-system-note">{feedback?.error ? (vi ? 'Bấm tải lại để thử lại.' : 'Reload to retry.') : (vi ? 'Đang tải cấu hình từ AlmaLinux...' : 'Loading configuration from AlmaLinux...')}</p> : <>
      <form className="backup-unified-form" onSubmit={event => { event.preventDefault(); void apply() }}>
      {task && <BackupTaskForm lang={lang} jobs={task.jobs} selected={selected} draft={taskDraft} disabled={disabled} loading={task.loading} error={task.error} scriptsDir={draft.scriptsDir} idPrefix={idPrefix} selectionLocked={mode === 'edit'}
        onSelect={id => { setScheduleDraft(null); setPreview(null); setFeedback(null); task.onSelect(id) }}
        onDraftChange={change => { if (taskDraft) setScheduleDraft({ ...taskDraft, ...change }); setPreview(null); setFeedback(null) }}
        onReset={() => { setScheduleDraft(selected ? makeCronScheduleDraft(selected, task?.revision) : null); setPreview(null); onReload?.() }} />}
      <div className="backup-system-fields">
        <div className="apex-settings-field"><label htmlFor={`${idPrefix}backup-system-source`}>{vi ? 'Thư mục dữ liệu cần sao lưu' : 'Source data folder'}</label><div className="backup-system-path"><input id={`${idPrefix}backup-system-source`} value={source?.path ?? (sourceKey ? '' : draft.backupRoot)} disabled={disabled || !source} readOnly={!source} placeholder={sourceKey ? (vi ? 'Tải lại để lấy nguồn từ máy chủ' : 'Reload to get source settings') : undefined} onChange={e => sourceKey && chooseSource(sourceKey, e.target.value)} spellCheck={false} /><button type="button" className="backup-system-secondary" disabled={disabled || !source} onClick={() => source && sourceKey && setPicker({ key: sourceKey, request: { location: 'local', purpose: `source-${sourceId.replace('backup-', '')}` as FolderRequest['purpose'], path: source.path } })} aria-label={vi ? 'Chọn thư mục dữ liệu cần sao lưu' : 'Choose source data folder'} title={vi ? 'Chọn thư mục' : 'Choose folder'}><Folder size={16} aria-hidden="true" /></button></div></div>
        {(['backupRoot', 'driveFolder', 'scriptsDir', 'logsDir'] as DirectoryKey[]).map(key => <div className="apex-settings-field" key={key}><label htmlFor={`${idPrefix}backup-system-${key}`}>{labels[key][vi ? 0 : 1]}</label><div className="backup-system-path"><input id={`${idPrefix}backup-system-${key}`} value={draft[key]} disabled={disabled} onChange={e => edit(key, e.target.value)} spellCheck={false} /><button type="button" className="backup-system-secondary" disabled={disabled || (key === 'driveFolder' && !state.remotes.length)} onClick={() => open(key)} aria-label={`${vi ? 'Chọn' : 'Choose'} ${labels[key][vi ? 0 : 1]}`} title={`${vi ? 'Chọn / Tạo' : 'Browse / Create'} ${labels[key][vi ? 0 : 1]}`}><Folder size={16} aria-hidden="true" /></button></div></div>)}
      </div>
      {!state.ready && <p role="alert" className="backup-system-error">{vi ? 'Thiếu helper lưu trữ theo ngày; cần cài helper trước khi đổi đường dẫn.' : 'The daily-layout helper is missing; install it before changing paths.'}</p>}
      {preview && <div className="backup-system-preview" aria-label={vi ? 'Xem trước cấu hình' : 'Configuration preview'}><h3>{vi ? 'Ảnh hưởng khi áp dụng' : 'Changes to apply'}</h3><p>{vi ? `${preview.filesToMove} tệp backup / ${preview.daysToMove} thư mục ngày (${(preview.bytesToMove / 1048576).toFixed(2)} MB); ${preview.scriptsToUpdate} script cập nhật; ${preview.logsToMove} log sao chép.` : `${preview.filesToMove} backup files / ${preview.daysToMove} dated folders (${(preview.bytesToMove / 1048576).toFixed(2)} MB); ${preview.scriptsToUpdate} scripts updated; ${preview.logsToMove} logs copied.`}</p>{preview.driveChanged && <p className="backup-system-warning">{vi ? 'Thư mục Drive mới dùng cho lần đẩy tiếp theo. Backup cũ vẫn ở thư mục cũ; không bị xóa hoặc chuyển. Dashboard sẽ đọc thư mục mới.' : 'The new Drive folder is used for future uploads. Old backups stay in the old folder, without deletion or migration. The dashboard reads the new folder.'}</p>}{!preview.changed && <p>{vi ? 'Không có thay đổi để áp dụng.' : 'No changes to apply.'}</p>}</div>}
      {preview?.schedule && <p className="backup-system-note">{vi ? 'Lịch tác vụ đã chọn: ' : 'Selected task schedule: '}{preview.schedule.previous ? formatCronSchedule(preview.schedule.previous, lang) : (vi ? 'Chưa có lịch' : 'Not scheduled')} → {formatCronSchedule(preview.schedule.proposed, lang)}{preview.schedule.enabled ? '' : (vi ? ' · giữ trạng thái tạm dừng' : ' · stays paused')}</p>}
      {preview?.sources?.map(change => <p className="backup-system-note" key={change.id}>{vi ? 'Nguồn dữ liệu: ' : 'Source data: '}{change.previous.path} → {change.proposed.path} · {change.proposed.scope === 'children' ? (vi ? 'từng thư mục con' : 'per child folder') : (vi ? 'thư mục đã chọn' : 'selected folder')}. {vi ? 'Cập nhật script cho lần chạy tiếp theo; không di chuyển dữ liệu nguồn.' : 'Updates the script for the next run; source data is not moved.'}</p>)}
      <div className="backup-system-actions">{mode === 'edit' && <button type="button" className="backup-system-secondary" disabled={disabled} onClick={onClose}>{vi ? 'Hủy' : 'Cancel'}</button>}<button type="submit" className="apex-settings-save" disabled={disabled || !state.ready || scheduleBlocked}>{busy ? (vi ? 'Đang xử lý...' : 'Working...') : mode === 'edit' ? (vi ? 'Lưu thay đổi' : 'Save changes') : (vi ? 'Thêm tác vụ' : 'Add task')}</button></div>
      {state.recoveryDir && <p className="backup-system-note">{vi ? 'Dự phòng cấu hình cũ: ' : 'Previous settings saved in: '}{state.recoveryDir}</p>}
      </form>
    </>}
    </div>
    {picker && <FolderPicker request={picker.request} vi={vi} close={() => setPicker(null)} choose={value => { if (picker.key === 'siteSource' || picker.key === 'databaseSource' || picker.key === 'panelSource') chooseSource(picker.key, value); else edit(picker.key, value); setPicker(null) }} />}
  </section>
}
