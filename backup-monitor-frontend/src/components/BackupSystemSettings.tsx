import { useEffect, useRef, useState } from 'react'
import { ChevronDown, Folder, FolderPlus, RefreshCw, Save, X } from 'lucide-react'
import { apiErrorMessage, clearSnapshotsCache } from '../api'
import { applyBackupSystem, createBackupFolder, fetchBackupSystem, listBackupFolders, previewBackupSystem } from '../backupSystemApi'
import type { BackupSource, BackupSystemConfig, BackupSystemState, ConfigurationPreview, FolderList, FolderRequest } from '../backupSystemApi'
import type { Lang } from '../language'
import type { CronJob } from '../types'
import BackupTaskForm from './BackupTaskForm'
import { backupSourceForTask, buildCronScheduleChange, cronScheduleIsStale, makeCronScheduleDraft, sourceConfigKeys, withCronClock } from '../backupTasks'
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
    dialog.current?.querySelector<HTMLElement>('button')?.focus()
    const keydown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') closeRef.current()
      if (event.key !== 'Tab') return
      const items = Array.from(dialog.current?.querySelectorAll<HTMLElement>('button:not(:disabled), input:not(:disabled)') ?? [])
      const first = items[0], last = items[items.length - 1]
      if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last?.focus() }
      else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first?.focus() }
    }
    document.addEventListener('keydown', keydown)
    return () => { document.removeEventListener('keydown', keydown); restore?.focus() }
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
  return <div className="backup-folder-overlay"><div className="backup-folder-dialog" ref={dialog} role="dialog" aria-modal="true" aria-labelledby="folder-picker-title">
    <div className="backup-system-heading"><h2 id="folder-picker-title">{sourceOnly ? (vi ? 'Chọn thư mục dữ liệu cần sao lưu' : 'Choose source data folder') : (vi ? 'Chọn hoặc tạo thư mục' : 'Choose or create a folder')} · {request.location === 'local' ? 'AlmaLinux' : 'Google Drive'}</h2><button type="button" className="backup-system-secondary" onClick={close} aria-label={vi ? 'Đóng' : 'Close'}><X size={18} /></button></div>
    <p className="backup-system-note">{sourceOnly ? (vi ? 'Chọn thư mục dữ liệu đã tồn tại trên máy chủ. Chọn ở đây chưa đổi script; cần xem trước và xác nhận ở cuối form.' : 'Choose an existing server data folder. Selecting it does not update scripts until you preview and confirm the form.') : (vi ? 'Tên thư mục dùng chữ không dấu, số, -, _, .; không có khoảng trắng. Tạo thư mục chưa thay đổi nơi lưu trữ đang dùng.' : 'Use ASCII letters, numbers, -, _, and .; no spaces. Creating a folder does not change active storage.')}</p>
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
  </div></div>
}

export default function BackupSystemSettings({ lang, locked = false, expanded: controlledExpanded, onToggle, onReload, task, onStateChange, onBusyChange }: {
  lang: Lang; locked?: boolean; expanded?: boolean; onToggle?: () => void; onReload?: () => void
  task?: { jobs: CronJob[]; selectedId: string; revision: number; loading: boolean; error: string | null; onSelect: (id: string) => void }
  onStateChange?: (state: BackupSystemState | null) => void; onBusyChange?: (busy: boolean) => void
}) {
  const vi = lang === 'vi'
  const [state, setState] = useState<BackupSystemState | null>(null), [draft, setDraft] = useState<BackupSystemConfig | null>(null)
  const [previewResponse, setPreview] = useState<ConfigurationPreview | null>(null), [busy, setBusy] = useState(false)
  const [previewFormKey, setPreviewFormKey] = useState('')
  const [scheduleDraft, setScheduleDraft] = useState<CronScheduleDraft | null>(null)
  const [feedback, setFeedback] = useState<{ text: string; error: boolean } | null>(null)
  const [picker, setPicker] = useState<{ key: DirectoryKey | SourceKey; request: FolderRequest } | null>(null)
  const [localExpanded, setExpanded] = useState(true)
  const expanded = controlledExpanded ?? localExpanded
  const disabled = busy || locked
  const selected = task?.jobs.find(job => job.id === task.selectedId) ?? task?.jobs.find(job => job.id === 'backup-database') ?? task?.jobs[0]
  const sourceId = selected?.id ?? 'backup-site'
  const sourceKey = sourceConfigKeys[sourceId]
  const source = draft && state ? backupSourceForTask(draft, state, sourceId) : null
  const taskDraft = selected ? (scheduleDraft?.id === selected.id && scheduleDraft.revision === task?.revision ? scheduleDraft : makeCronScheduleDraft(selected, task?.revision)) : null
  const schedule = taskDraft ? buildCronScheduleChange(taskDraft) : null
  const scheduleBlocked = !!task && (!!task.error || (task.loading && !task.jobs.length) || !!(selected && taskDraft && (
    cronScheduleIsStale(taskDraft, selected) || !withCronClock(taskDraft.expectedSchedule, taskDraft.hour, taskDraft.minute) || selected.status === 'running' || typeof selected.enabled !== 'boolean'
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
    try { const result = await fetchBackupSystem(); setState(result); setDraft(result.config); setScheduleDraft(null); onReload?.() }
    catch (error: unknown) { setFeedback({ text: apiErrorMessage(error, vi ? 'Không tải được cấu hình.' : 'Could not load settings.'), error: true }) }
    finally { setBusy(false) }
  }
  const edit = (key: DirectoryKey | 'driveRemote', value: string) => { if (draft) setDraft({ ...draft, [key]: value }); setPreview(null); setFeedback(null) }
  const editSource = (key: SourceKey, value: BackupSource) => { if (draft) setDraft({ ...draft, [key]: value }); setPreview(null); setFeedback(null) }
  const chooseSource = (key: SourceKey, path: string) => editSource(key, { path, scope: (key === 'databaseSource' && (path === '/www/server/data' || (state?.sources?.['backup-database']?.path === path && state.sources['backup-database'].scope === 'children'))) || (key === 'siteSource' && path === '/www/wwwroot') ? 'children' : 'folder' })
  const inspect = async () => {
    if (!draft || !state || disabled || scheduleBlocked) return
    setBusy(true); setFeedback(null); setPreview(null)
    try { const result = await previewBackupSystem(draft, state.version, schedule); setPreview(result); setPreviewFormKey(formKey) }
    catch (error: unknown) { setFeedback({ text: apiErrorMessage(error, vi ? 'Không xem trước được thay đổi.' : 'Could not preview changes.'), error: true }) }
    finally { setBusy(false) }
  }
  const apply = async () => {
    if (!preview || !draft || !state || disabled || scheduleBlocked) return
    if (!window.confirm(vi ? 'Áp dụng nguồn dữ liệu, lịch chạy và nơi lưu đã xem trước? Script sẽ đọc nguồn mới trong lần backup tiếp theo; không di chuyển/xóa dữ liệu nguồn. Giữ trạng thái bật/tạm dừng. Chỉ chuyển backup theo ngày nếu đổi nơi lưu trên máy chủ; không chuyển/xóa backup cũ trên Drive, không chạy backup ngay.' : 'Apply the previewed source, schedule and storage? Scripts use the new source on the next run without moving/deleting source data. Enabled/paused state stays unchanged. Local dated backups move only if storage changes; old Drive backups stay unchanged and no backup runs now.')) return
    setBusy(true); setFeedback(null)
    try {
      const result = await applyBackupSystem(draft, state.version, preview.token, schedule)
      setState(result); setDraft(result.config); setPreview(null); setScheduleDraft(null); onReload?.()
      clearSnapshotsCache(); window.dispatchEvent(new Event('force-refresh'))
      setFeedback({ error: false, text: [vi ? 'Đã lưu cấu hình và lịch chạy đã chọn. Không chạy backup ngay; chính sách 14 ngày không thay đổi.' : 'Selected configuration and schedule saved. No backup ran; the 14-day retention policy is unchanged.', ...(result.warnings || [])].join(' ') })
    } catch (error: unknown) { setPreview(null); setFeedback({ text: apiErrorMessage(error, vi ? 'Không áp dụng được cấu hình. Hãy tải lại và xem trước lại.' : 'Could not apply. Reload and preview again.'), error: true }) }
    finally { setBusy(false) }
  }
  const open = (key: DirectoryKey) => {
    if (draft) setPicker({ key, request: { location: key === 'driveFolder' ? 'drive' : 'local', purpose: key === 'scriptsDir' ? 'scripts' : key === 'logsDir' ? 'logs' : 'backup', path: draft[key], remote: draft.driveRemote } })
  }
  return <section className="apex-settings-panel backup-system-panel" id="backup-storage" aria-labelledby="backup-storage-title">
    <div className="backup-storage-heading"><h2 id="backup-storage-title"><button type="button" aria-expanded={expanded} aria-controls="backup-storage-fields" onClick={() => onToggle ? onToggle() : setExpanded(!expanded)}>{vi ? 'Cấu hình hệ thống sao lưu' : 'Backup system configuration'}<ChevronDown size={17} className={expanded ? 'is-expanded' : ''} /></button></h2><button type="button" className="backup-system-secondary" disabled={disabled} onClick={() => void reload()} aria-label={vi ? 'Tải lại cấu hình' : 'Reload configuration'}><RefreshCw size={16} /></button></div>
    <div id="backup-storage-fields" className="backup-storage-body" hidden={!expanded}>
    <p className="backup-system-note">{vi ? 'Loại tác vụ chọn script thực thi. Nguồn dữ liệu và nơi lưu được chọn độc lập bằng cách duyệt toàn bộ cây thư mục máy chủ từ /. Điền xong, xem trước và xác nhận một lần ở cuối; không chạy script ngay.' : 'Task type selects the execution script. Browse the entire server directory tree from / to choose source and storage independently. Preview and confirm once at the end; the script does not run immediately.'}</p>
    {feedback && <p className={feedback.error ? 'backup-system-error' : 'backup-system-success'} role={feedback.error ? 'alert' : 'status'}>{feedback.text}</p>}
    {!draft || !state ? <p className="backup-system-note">{feedback?.error ? (vi ? 'Bấm tải lại để thử lại.' : 'Reload to retry.') : (vi ? 'Đang tải cấu hình từ AlmaLinux...' : 'Loading configuration from AlmaLinux...')}</p> : <>
      <form className="backup-unified-form" onSubmit={event => { event.preventDefault(); void inspect() }}>
      {task && <BackupTaskForm lang={lang} jobs={task.jobs} selected={selected} draft={taskDraft} disabled={disabled} loading={task.loading} error={task.error} scriptsDir={draft.scriptsDir}
        onSelect={id => { setScheduleDraft(null); setPreview(null); setFeedback(null); task.onSelect(id) }}
        onClockChange={(hour, minute) => { if (taskDraft) setScheduleDraft({ ...taskDraft, hour, minute }); setPreview(null); setFeedback(null) }}
        onReset={() => { setScheduleDraft(null); setPreview(null); onReload?.() }} />}
      <div className="backup-system-fields">
        <div className="apex-settings-field"><label htmlFor="backup-system-source">{vi ? 'Thư mục dữ liệu cần sao lưu' : 'Source data folder'}</label><div className="backup-system-path"><input id="backup-system-source" value={source?.path ?? (sourceKey ? '' : draft.backupRoot)} disabled={disabled || !source} readOnly={!source} placeholder={sourceKey ? (vi ? 'Tải lại để lấy nguồn từ máy chủ' : 'Reload to get source settings') : undefined} onChange={e => sourceKey && chooseSource(sourceKey, e.target.value)} spellCheck={false} /><button type="button" className="backup-system-secondary" disabled={disabled || !source} onClick={() => source && sourceKey && setPicker({ key: sourceKey, request: { location: 'local', purpose: `source-${sourceId.replace('backup-', '')}` as FolderRequest['purpose'], path: source.path } })}><Folder size={16} />{vi ? 'Chọn thư mục' : 'Choose folder'}</button></div><small>{sourceId === 'backup-database' ? (vi ? 'Chọn data hoặc thư mục con của database. Script xuất SQL bằng mysqldump rồi nén .sql.gz; không nén trực tiếp dữ liệu MySQL đang chạy.' : 'Choose data or a database subfolder. The script uses mysqldump and compresses .sql.gz; it never archives a live MySQL datadir.') : sourceId === 'backup-panel' ? (vi ? 'Chọn thư mục gốc aaPanel có data, config, vhost. Script giữ cơ chế snapshot SQLite và tạo ZIP.' : 'Choose an aaPanel root containing data, config and vhost. SQLite snapshots and ZIP creation are preserved.') : sourceId === 'backup-site' ? (vi ? 'Script sẽ tìm và nén dữ liệu từ nguồn đã lưu. Không di chuyển/xóa dữ liệu nguồn; không chọn nơi chứa backup.' : 'The script reads and compresses the saved source. Source data is not moved/deleted; do not select backup storage.') : (vi ? 'Tác vụ này dùng nơi lưu backup bên dưới, không tạo bản nén từ dữ liệu nguồn mới.' : 'This task uses backup storage below, not a new source-data archive.')}</small></div>
        {source && sourceId !== 'backup-panel' && <div className="apex-settings-field"><label htmlFor="backup-system-source-scope">{vi ? 'Phạm vi sao lưu' : 'Backup scope'}</label><select id="backup-system-source-scope" value={source.scope} disabled={disabled} onChange={e => sourceKey && editSource(sourceKey, { ...source, scope: e.target.value as BackupSource['scope'] })}><option value="folder">{sourceId === 'backup-database' ? (vi ? 'Chỉ database của thư mục đã chọn' : 'Only the selected database') : (vi ? 'Nén toàn bộ thư mục đã chọn' : 'Archive the chosen folder')}</option><option value="children">{sourceId === 'backup-database' ? (vi ? 'Các database con (bỏ database hệ thống)' : 'Child databases (exclude system databases)') : (vi ? 'Mỗi thư mục con là một bản backup riêng' : 'A separate backup per child folder')}</option></select><small>{vi ? 'Duyệt nguồn từ /; đường dẫn được kiểm tra khi xem trước và lưu.' : 'Browse source folders from /; paths are validated on preview and save.'}</small></div>}
        {(['backupRoot', 'driveFolder', 'scriptsDir', 'logsDir'] as DirectoryKey[]).map(key => <div className="apex-settings-field" key={key}><label htmlFor={`backup-system-${key}`}>{labels[key][vi ? 0 : 1]}</label><div className="backup-system-path"><input id={`backup-system-${key}`} value={draft[key]} disabled={disabled} onChange={e => edit(key, e.target.value)} spellCheck={false} /><button type="button" className="backup-system-secondary" disabled={disabled || (key === 'driveFolder' && !state.remotes.length)} onClick={() => open(key)} aria-label={`${vi ? 'Chọn' : 'Choose'} ${labels[key][vi ? 0 : 1]}`}><Folder size={16} />{vi ? 'Chọn / Tạo' : 'Browse / Create'}</button></div><small>{key === 'driveFolder' ? (vi ? 'Đường dẫn tương đối, ví dụ Backup. Không chọn toàn bộ Drive.' : 'Relative path, e.g. Backup. The whole Drive cannot be selected.') : (vi ? 'Duyệt toàn bộ máy chủ từ /. Nguồn và nơi lưu backup phải tách biệt; script/log phải thuộc root và không cho người khác ghi.' : 'Browse the whole server from /. Keep sources separate from backup storage; script/log directories must be root-owned and not writable by others.')}</small></div>)}
      </div>
      <div className="backup-system-policy">{vi ? `Giữ ${state.localRetentionDays} ngày có backup trên máy chủ, ${state.driveRetentionDays} ngày trên Drive. Mỗi website, cơ sở dữ liệu và aaPanel: 1 bản cron + 2 bản thủ công/ngày.` : `Keep ${state.localRetentionDays} backup dates locally and ${state.driveRetentionDays} on Drive. Per website, database and aaPanel: 1 scheduled + 2 manual versions/day.`}</div>
      <p className="backup-system-note">{vi ? 'Đường dẫn dùng chữ không dấu, không có khoảng trắng. Bản đầu tiên chỉ chuyển backup trên cùng filesystem. aaPanel vẫn tạo bản panel trong thư mục gốc trước khi script gom vào thư mục theo ngày.' : 'Use ASCII paths without spaces. This version moves backups only within one filesystem. aaPanel keeps its staging folder before scripts collect archives into dated folders.'}</p>
      {!state.ready && <p role="alert" className="backup-system-error">{vi ? 'Thiếu helper lưu trữ theo ngày; cần cài helper trước khi đổi đường dẫn.' : 'The daily-layout helper is missing; install it before changing paths.'}</p>}
      {preview && <div className="backup-system-preview" aria-label={vi ? 'Xem trước cấu hình' : 'Configuration preview'}><h3>{vi ? 'Ảnh hưởng khi áp dụng' : 'Changes to apply'}</h3><p>{vi ? `${preview.filesToMove} tệp backup / ${preview.daysToMove} thư mục ngày (${(preview.bytesToMove / 1048576).toFixed(2)} MB); ${preview.scriptsToUpdate} script cập nhật; ${preview.logsToMove} log sao chép.` : `${preview.filesToMove} backup files / ${preview.daysToMove} dated folders (${(preview.bytesToMove / 1048576).toFixed(2)} MB); ${preview.scriptsToUpdate} scripts updated; ${preview.logsToMove} logs copied.`}</p>{preview.driveChanged && <p className="backup-system-warning">{vi ? 'Thư mục Drive mới dùng cho lần đẩy tiếp theo. Backup cũ vẫn ở thư mục cũ; không bị xóa hoặc chuyển. Dashboard sẽ đọc thư mục mới.' : 'The new Drive folder is used for future uploads. Old backups stay in the old folder, without deletion or migration. The dashboard reads the new folder.'}</p>}{!preview.changed && <p>{vi ? 'Không có thay đổi để áp dụng.' : 'No changes to apply.'}</p>}</div>}
      {preview?.schedule && <p className="backup-system-note">{vi ? 'Lịch tác vụ đã chọn: ' : 'Selected task schedule: '}{formatCronSchedule(preview.schedule.previous, lang)} → {formatCronSchedule(preview.schedule.proposed, lang)}{preview.schedule.enabled ? '' : (vi ? ' · giữ trạng thái tạm dừng' : ' · stays paused')}</p>}
      {preview?.sources?.map(change => <p className="backup-system-note" key={change.id}>{vi ? 'Nguồn dữ liệu: ' : 'Source data: '}{change.previous.path} → {change.proposed.path} · {change.proposed.scope === 'children' ? (vi ? 'từng thư mục con' : 'per child folder') : (vi ? 'thư mục đã chọn' : 'selected folder')}. {vi ? 'Cập nhật script cho lần chạy tiếp theo; không di chuyển dữ liệu nguồn.' : 'Updates the script for the next run; source data is not moved.'}</p>)}
      <div className="backup-system-actions"><button type="submit" className="backup-system-secondary" disabled={disabled || !state.ready || scheduleBlocked}>{busy ? (vi ? 'Đang xử lý...' : 'Working...') : (vi ? 'Xem trước thay đổi' : 'Preview changes')}</button><button type="button" className="apex-settings-save" disabled={disabled || scheduleBlocked || !preview?.changed} onClick={() => void apply()}><Save size={16} />{vi ? 'Xác nhận áp dụng' : 'Confirm and apply'}</button></div>
      {state.recoveryDir && <p className="backup-system-note">{vi ? 'Dự phòng cấu hình cũ: ' : 'Previous settings saved in: '}{state.recoveryDir}</p>}
      </form>
    </>}
    </div>
    {picker && <FolderPicker request={picker.request} vi={vi} close={() => setPicker(null)} choose={value => { if (picker.key === 'siteSource' || picker.key === 'databaseSource' || picker.key === 'panelSource') chooseSource(picker.key, value); else edit(picker.key, value); setPicker(null) }} />}
  </section>
}
