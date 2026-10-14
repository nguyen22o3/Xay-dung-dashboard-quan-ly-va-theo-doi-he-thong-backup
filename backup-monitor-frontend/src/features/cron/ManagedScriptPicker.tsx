import { useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { FileCode, Folder, RefreshCw, Search, X } from 'lucide-react'
import { listBackupFolders } from '../../api/backupSystem'
import type { FolderList, ManagedScriptOption } from '../../api/backupSystem'
import { apiErrorMessage } from '../../api/dashboard'

type Props = {
  vi: boolean; taskName: string; initialPath: string; options: ManagedScriptOption[]; value: string
  loading: boolean; error: string; onReload: () => void
  onClose: () => void; onChoose: (selector: string) => void
}

export default function ManagedScriptPicker({ vi, taskName, initialPath, options, value, loading, error, onReload, onClose, onChoose }: Props) {
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
      {folderState.error && <p role="alert" className="backup-system-error">{folderState.error}</p>}
      {error && <p role="alert" className="backup-system-error">{error}</p>}
      {!valid && !loading && <p role="alert" className="backup-system-error">{vi ? 'Script đã đổi hoặc không còn khả dụng; hãy chọn lại.' : 'The script changed or is no longer available; select again.'}</p>}
      <div className="backup-system-actions"><button type="button" className="backup-system-secondary" onClick={onClose}>{vi ? 'Hủy' : 'Cancel'}</button><button type="button" className="apex-settings-save" disabled={loading || !valid || (!!selection && !!error)} onClick={() => onChoose(selection?.selector ?? '')}>{vi ? 'Chọn script này' : 'Use this script'}</button></div>
    </div>
  </div>, document.querySelector('.app-shell') ?? document.body)
}
