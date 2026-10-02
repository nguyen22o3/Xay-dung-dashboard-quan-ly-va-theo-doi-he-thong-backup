import { useEffect, useRef, useState, type ReactNode } from 'react'
import { ChevronDown, Folder, FolderPlus, RefreshCw, Save, X } from 'lucide-react'
import { apiErrorMessage, clearSnapshotsCache } from '../api'
import { applyBackupSystem, createBackupFolder, fetchBackupSystem, listBackupFolders, previewBackupSystem } from '../backupSystemApi'
import type { BackupSystemConfig, BackupSystemState, ConfigurationPreview, FolderList, FolderRequest } from '../backupSystemApi'
import type { Lang } from '../language'

type DirectoryKey = 'backupRoot' | 'scriptsDir' | 'logsDir' | 'driveFolder'
const labels: Record<DirectoryKey, [string, string]> = {
  backupRoot: ['Thư mục backup trên AlmaLinux', 'AlmaLinux backup directory'], scriptsDir: ['Thư mục scripts', 'Scripts directory'],
  logsDir: ['Thư mục nhật ký (log)', 'Log directory'], driveFolder: ['Thư mục backup trên Google Drive', 'Google Drive backup folder'],
}

function FolderPicker({ request, vi, close, choose }: { request: FolderRequest; vi: boolean; close: () => void; choose: (path: string) => void }) {
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
    <div className="backup-system-heading"><h2 id="folder-picker-title">{vi ? 'Chọn hoặc tạo thư mục' : 'Choose or create a folder'} · {request.location === 'local' ? 'AlmaLinux' : 'Google Drive'}</h2><button type="button" className="backup-system-secondary" onClick={close} aria-label={vi ? 'Đóng' : 'Close'}><X size={18} /></button></div>
    <p className="backup-system-note">{vi ? 'Tên thư mục dùng chữ không dấu, số, -, _, .; không có khoảng trắng. Tạo thư mục chưa thay đổi nơi lưu trữ đang dùng.' : 'Use ASCII letters, numbers, -, _, and .; no spaces. Creating a folder does not change active storage.'}</p>
    <div className="backup-system-path"><input aria-label={vi ? 'Đường dẫn thư mục' : 'Folder path'} value={path} onChange={e => setPath(e.target.value)} disabled={busy} /><button type="button" className="backup-system-secondary" disabled={busy} onClick={() => void load(path)}>{vi ? 'Mở' : 'Open'}</button></div>
    <div className="backup-folder-list" aria-busy={busy}>{busy ? <p>{vi ? 'Đang đọc thư mục...' : 'Loading folders...'}</p> : <>
      {listing?.parent !== null && listing?.parent !== undefined && listing.parent !== path && <button type="button" onClick={() => void load(listing.parent!)}>← {vi ? 'Thư mục cha' : 'Parent folder'}</button>}
      {listing?.entries.map(entry => <button type="button" key={entry.path} onClick={() => void load(entry.path)}><Folder size={16} />{entry.name}</button>)}
      {listing?.exists && !listing.entries.length && <p>{vi ? 'Chưa có thư mục con.' : 'No subfolders.'}</p>}
      {listing && !listing.exists && <p>{vi ? 'Thư mục chưa tồn tại.' : 'Folder does not exist.'}</p>}
    </>}</div>
    {error && <p role="alert" className="backup-system-error">{error}</p>}
    <div className="backup-system-path"><input aria-label={vi ? 'Tên thư mục mới' : 'New folder name'} placeholder={vi ? 'Tên thư mục con mới' : 'New subfolder name'} value={name} onChange={e => setName(e.target.value)} disabled={busy} /><button type="button" className="backup-system-secondary" disabled={busy || !/^[A-Za-z0-9_-][A-Za-z0-9_.-]*$/.test(name)} onClick={() => void create(`${path.replace(/\/$/, '')}/${name}`.replace(/^\//, request.location === 'drive' ? '' : '/'))}><FolderPlus size={16} />{vi ? 'Tạo thư mục con' : 'Create subfolder'}</button></div>
    <div className="backup-system-actions"><button type="button" className="backup-system-secondary" disabled={busy || !path || path === '/'} onClick={() => void create(path)}>{vi ? 'Tạo đường dẫn này nếu chưa có' : 'Create this path if missing'}</button><button type="button" className="apex-settings-save" disabled={busy || !selectable} onClick={() => choose(path)}>{vi ? 'Chọn thư mục này' : 'Use this folder'}</button></div>
  </div></div>
}

export default function BackupSystemSettings({ lang, locked = false, expanded: controlledExpanded, onToggle, onReload, children, onStateChange, onBusyChange }: {
  lang: Lang; locked?: boolean; expanded?: boolean; onToggle?: () => void; onReload?: () => void; children?: ReactNode
  onStateChange?: (state: BackupSystemState | null) => void; onBusyChange?: (busy: boolean) => void
}) {
  const vi = lang === 'vi'
  const [state, setState] = useState<BackupSystemState | null>(null), [draft, setDraft] = useState<BackupSystemConfig | null>(null)
  const [preview, setPreview] = useState<ConfigurationPreview | null>(null), [busy, setBusy] = useState(false)
  const [feedback, setFeedback] = useState<{ text: string; error: boolean } | null>(null)
  const [picker, setPicker] = useState<{ key: DirectoryKey; request: FolderRequest } | null>(null)
  const [localExpanded, setExpanded] = useState(true)
  const expanded = controlledExpanded ?? localExpanded
  const disabled = busy || locked
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
    try { const result = await fetchBackupSystem(); setState(result); setDraft(result.config); onReload?.() }
    catch (error: unknown) { setFeedback({ text: apiErrorMessage(error, vi ? 'Không tải được cấu hình.' : 'Could not load settings.'), error: true }) }
    finally { setBusy(false) }
  }
  const edit = (key: keyof BackupSystemConfig, value: string) => { if (draft) setDraft({ ...draft, [key]: value }); setPreview(null); setFeedback(null) }
  const inspect = async () => {
    if (!draft || !state || disabled) return
    setBusy(true); setFeedback(null); setPreview(null)
    try { setPreview(await previewBackupSystem(draft, state.version)) }
    catch (error: unknown) { setFeedback({ text: apiErrorMessage(error, vi ? 'Không xem trước được thay đổi.' : 'Could not preview changes.'), error: true }) }
    finally { setBusy(false) }
  }
  const apply = async () => {
    if (!preview || !draft || !state || disabled) return
    if (!window.confirm(vi ? 'Áp dụng cấu hình đã xem trước? Backup theo ngày sẽ được chuyển nếu đổi thư mục máy chủ. Không chuyển/xóa backup cũ trên Drive; không chạy backup ngay.' : 'Apply these changes? Local dated backups will move if the server folder changes. Old Drive backups are not moved/deleted; no backup runs now.')) return
    setBusy(true); setFeedback(null)
    try {
      const result = await applyBackupSystem(draft, state.version, preview.token)
      setState(result); setDraft(result.config); setPreview(null)
      clearSnapshotsCache(); window.dispatchEvent(new Event('force-refresh'))
      setFeedback({ error: false, text: [vi ? 'Đã áp dụng cấu hình. Chính sách giữ 14 ngày không thay đổi.' : 'Settings applied. The 14-day retention policy is unchanged.', ...(result.warnings || [])].join(' ') })
    } catch (error: unknown) { setPreview(null); setFeedback({ text: apiErrorMessage(error, vi ? 'Không áp dụng được cấu hình. Hãy tải lại và xem trước lại.' : 'Could not apply. Reload and preview again.'), error: true }) }
    finally { setBusy(false) }
  }
  const open = (key: DirectoryKey) => {
    if (draft) setPicker({ key, request: { location: key === 'driveFolder' ? 'drive' : 'local', purpose: key === 'scriptsDir' ? 'scripts' : key === 'logsDir' ? 'logs' : 'backup', path: draft[key], remote: draft.driveRemote } })
  }
  return <section className="apex-settings-panel backup-system-panel" id="backup-storage" aria-labelledby="backup-storage-title">
    <div className="backup-storage-heading"><h2 id="backup-storage-title"><button type="button" aria-expanded={expanded} aria-controls="backup-storage-fields" onClick={() => onToggle ? onToggle() : setExpanded(!expanded)}>{vi ? 'Cấu hình hệ thống sao lưu' : 'Backup system configuration'}<ChevronDown size={17} className={expanded ? 'is-expanded' : ''} /></button></h2><button type="button" className="backup-system-secondary" disabled={disabled} onClick={() => void reload()} aria-label={vi ? 'Tải lại cấu hình' : 'Reload configuration'}><RefreshCw size={16} /></button></div>
    <div id="backup-storage-fields" className="backup-storage-body" hidden={!expanded}>
    {children}
    {children && <h3 className="backup-storage-subheading">{vi ? 'Đường dẫn lưu trữ' : 'Storage paths'}</h3>}
    <p className="backup-system-note">{vi ? 'Chọn nơi lưu backup, script và log. Đường dẫn chỉ thay đổi sau khi xem trước và xác nhận áp dụng.' : 'Choose backup, script and log locations. Paths change only after preview and confirmation.'}</p>
    {feedback && <p className={feedback.error ? 'backup-system-error' : 'backup-system-success'} role={feedback.error ? 'alert' : 'status'}>{feedback.text}</p>}
    {!draft || !state ? <p className="backup-system-note">{feedback?.error ? (vi ? 'Bấm tải lại để thử lại.' : 'Reload to retry.') : (vi ? 'Đang tải cấu hình từ AlmaLinux...' : 'Loading configuration from AlmaLinux...')}</p> : <>
      <div className="backup-system-policy">{vi ? `Giữ ${state.localRetentionDays} ngày có backup trên máy chủ, ${state.driveRetentionDays} ngày trên Drive. Mỗi website, cơ sở dữ liệu và aaPanel: 1 bản cron + 2 bản thủ công/ngày.` : `Keep ${state.localRetentionDays} backup dates locally and ${state.driveRetentionDays} on Drive. Per website, database and aaPanel: 1 scheduled + 2 manual versions/day.`}</div>
      <div className="backup-system-fields">
        <div className="apex-settings-field"><label htmlFor="backup-system-remote">{vi ? 'Kết nối Google Drive' : 'Google Drive connection'}</label><select id="backup-system-remote" value={draft.driveRemote} disabled={disabled || !state.remotes.length} onChange={e => edit('driveRemote', e.target.value)}>{state.remotes.map(remote => <option key={remote}>{remote}</option>)}</select><small>{vi ? 'Kết nối rclone đã cấu hình trên máy chủ; không nhập khóa hoặc token tại đây.' : 'An existing server rclone connection; credentials are not entered here.'}</small></div>
        {(['backupRoot', 'driveFolder', 'scriptsDir', 'logsDir'] as DirectoryKey[]).map(key => <div className="apex-settings-field" key={key}><label htmlFor={`backup-system-${key}`}>{labels[key][vi ? 0 : 1]}</label><div className="backup-system-path"><input id={`backup-system-${key}`} value={draft[key]} disabled={disabled} onChange={e => edit(key, e.target.value)} spellCheck={false} /><button type="button" className="backup-system-secondary" disabled={disabled || (key === 'driveFolder' && !state.remotes.length)} onClick={() => open(key)} aria-label={`${vi ? 'Chọn' : 'Choose'} ${labels[key][vi ? 0 : 1]}`}><Folder size={16} />{vi ? 'Chọn / Tạo' : 'Browse / Create'}</button></div><small>{key === 'driveFolder' ? (vi ? 'Đường dẫn tương đối, ví dụ Backup. Không chọn toàn bộ Drive.' : 'Relative path, e.g. Backup. The whole Drive cannot be selected.') : `${vi ? 'Vùng được phép' : 'Allowed roots'}: ${state.allowedRoots[key === 'backupRoot' ? 'backup' : key === 'scriptsDir' ? 'scripts' : 'logs']?.join(', ')}`}</small></div>)}
      </div>
      <p className="backup-system-note">{vi ? 'Đường dẫn dùng chữ không dấu, không có khoảng trắng. Bản đầu tiên chỉ chuyển backup trên cùng filesystem. aaPanel vẫn tạo bản panel trong thư mục gốc trước khi script gom vào thư mục theo ngày.' : 'Use ASCII paths without spaces. This version moves backups only within one filesystem. aaPanel keeps its staging folder before scripts collect archives into dated folders.'}</p>
      {!state.ready && <p role="alert" className="backup-system-error">{vi ? 'Thiếu helper lưu trữ theo ngày; cần cài helper trước khi đổi đường dẫn.' : 'The daily-layout helper is missing; install it before changing paths.'}</p>}
      {preview && <div className="backup-system-preview" aria-label={vi ? 'Xem trước cấu hình' : 'Configuration preview'}><h3>{vi ? 'Ảnh hưởng khi áp dụng' : 'Changes to apply'}</h3><p>{vi ? `${preview.filesToMove} tệp backup / ${preview.daysToMove} thư mục ngày (${(preview.bytesToMove / 1048576).toFixed(2)} MB); ${preview.scriptsToUpdate} script cập nhật; ${preview.logsToMove} log sao chép.` : `${preview.filesToMove} backup files / ${preview.daysToMove} dated folders (${(preview.bytesToMove / 1048576).toFixed(2)} MB); ${preview.scriptsToUpdate} scripts updated; ${preview.logsToMove} logs copied.`}</p>{preview.driveChanged && <p className="backup-system-warning">{vi ? 'Thư mục Drive mới dùng cho lần đẩy tiếp theo. Backup cũ vẫn ở thư mục cũ; không bị xóa hoặc chuyển. Dashboard sẽ đọc thư mục mới.' : 'The new Drive folder is used for future uploads. Old backups stay in the old folder, without deletion or migration. The dashboard reads the new folder.'}</p>}{!preview.changed && <p>{vi ? 'Không có thay đổi đường dẫn để áp dụng.' : 'No path changes to apply.'}</p>}</div>}
      <div className="backup-system-actions"><button type="button" className="backup-system-secondary" disabled={disabled || !state.ready} onClick={() => void inspect()}>{busy ? (vi ? 'Đang xử lý...' : 'Working...') : (vi ? 'Xem trước thay đổi' : 'Preview changes')}</button><button type="button" className="apex-settings-save" disabled={disabled || !preview?.changed} onClick={() => void apply()}><Save size={16} />{vi ? 'Xác nhận áp dụng' : 'Confirm and apply'}</button></div>
      {state.recoveryDir && <p className="backup-system-note">{vi ? 'Dự phòng cấu hình cũ: ' : 'Previous settings saved in: '}{state.recoveryDir}</p>}
    </>}
    </div>
    {picker && <FolderPicker request={picker.request} vi={vi} close={() => setPicker(null)} choose={value => { edit(picker.key, value); setPicker(null) }} />}
  </section>
}
