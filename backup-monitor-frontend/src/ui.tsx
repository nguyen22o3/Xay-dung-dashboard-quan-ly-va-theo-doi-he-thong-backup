import React from "react"
import type { CSSProperties, ReactNode } from "react"
import type { Theme } from "./shared"
import type { Lang } from "./i18n"
import { useEffect, useRef, useState } from "react"
import { createPortal } from "react-dom"
import { Folder, X } from "lucide-react"
import { apiErrorMessage, listBackupFolders } from "./api"
import type { FolderList, FolderRequest, TaskProgress as Progress } from "./api"
import { localizeMessage } from "./i18n"
import { formatBytes } from "./shared"

// All actions share the same semantic styles; forms opt into type="submit".
export function Button({
  theme,
  variant = 'secondary',
  size = 'default',
  iconOnly = false,
  className = '',
  style,
  type = 'button',
  children,
  ...rest
}: React.ButtonHTMLAttributes<HTMLButtonElement> & {
  theme?: Theme
  variant?: 'primary' | 'secondary' | 'success' | 'danger' | 'soft' | 'danger-outline'
  size?: 'default' | 'small'
  iconOnly?: boolean
  children: ReactNode
}) {
  const colors = theme ? {
    '--app-card': theme.cardBg,
    '--app-border': theme.cardBorder,
    '--app-text': theme.textPrimary,
    '--app-muted': theme.textSecondary,
    '--app-accent': theme.accentText,
    '--app-primary': theme.primary,
    '--app-danger': theme.errorText,
    '--app-on-accent': theme.onPrimary,
  } as CSSProperties : undefined
  return (
    <button
      {...rest}
      type={type}
      className={`ui-button ${iconOnly ? 'ui-button--icon' : ''} ${className}`.trim()}
      data-variant={variant}
      data-size={size}
      style={{ ...colors, ...style }}
    >
      {children}
    </button>
  )
}

export function OperationStatus({ state, lang }: { state: string; lang: Lang }) {
  const labels: Record<string, [string, string, string]> = {
    success: ['Thành công', 'Success', 'success'],
    ready: ['Đã chuẩn bị', 'Ready', 'success'],
    failed: ['Thất bại', 'Failed', 'failed'],
    cancelled: ['Đã hủy', 'Cancelled', 'warning'],
    running: ['Đang chạy', 'Running', 'warning'],
    queued: ['Đang chờ', 'Queued', 'neutral'],
    preview: ['Xem trước', 'Preview', 'neutral'],
    unknown: ['Chưa xác nhận', 'Unconfirmed', 'neutral'],
  }
  const label = labels[state]
  return <span className="ui-status" data-tone={label?.[2] ?? 'neutral'}>{label ? label[lang === 'vi' ? 0 : 1] : state}</span>
}

interface Props {
  children?: ReactNode;
  lang: Lang;
}

interface State {
  hasError: boolean;
  error: Error | null;
  errorInfo: React.ErrorInfo | null;
}

export class ErrorBoundary extends React.Component<Props, State> {
  constructor(props: Props) {
    super(props);
    this.state = { hasError: false, error: null, errorInfo: null };
  }

  static getDerivedStateFromError(_: Error): State {
    return { hasError: true, error: _, errorInfo: null };
  }

  componentDidCatch(error: Error, errorInfo: React.ErrorInfo) {
    this.setState({ error, errorInfo });
    console.error("ErrorBoundary caught an error", error, errorInfo);
  }

  render() {
    if (this.state.hasError) {
      return (
        <div style={{ padding: '20px', background: '#ffebee', color: '#c62828' }}>
          <h2>{this.props.lang === 'vi' ? 'Đã xảy ra lỗi hiển thị.' : 'Something went wrong while displaying this page.'}</h2>
          <details style={{ whiteSpace: 'pre-wrap' }}>
            {this.state.error && this.state.error.toString()}
            <br />
            {this.state.errorInfo && this.state.errorInfo.componentStack}
          </details>
        </div>
      );
    }
    return this.props.children;
  }
}

export function FolderPicker({ request, vi, close, choose, existingOnly = false }: { request: FolderRequest; vi: boolean; close: () => void; choose: (path: string) => void; existingOnly?: boolean }) {
	const sourceOnly = request.purpose.startsWith('source-') || existingOnly
  const [path, setPath] = useState(request.path)
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
  const selectable = listing?.exists && listing.path === path && path !== '/' && (request.location !== 'drive' || !!path)
    && !(request.purpose === 'scripts' && ['/root', '/opt/backup-monitor'].includes(path))
  // Outside the animated, scrollable page; the shell keeps the active theme variables.
  return createPortal(<div className="backup-folder-overlay backup-management"><div className="backup-folder-dialog" ref={dialog} role="dialog" aria-modal="true" aria-labelledby="folder-picker-title">
    <div className="backup-system-heading"><h2 id="folder-picker-title">{existingOnly ? (vi ? 'Chọn thư mục backup cần giám sát' : 'Choose a backup folder to monitor') : sourceOnly ? (vi ? 'Chọn thư mục dữ liệu cần sao lưu' : 'Choose source data folder') : (vi ? 'Chọn thư mục' : 'Choose a folder')} · {request.location === 'local' ? 'AlmaLinux' : 'Google Drive'}</h2><button type="button" className="backup-system-secondary" onClick={close} aria-label={vi ? 'Đóng' : 'Close'}><X size={18} /></button></div>
    <p className="backup-system-note">{existingOnly ? (vi ? 'Chọn thư mục backup đã tồn tại. Script giám sát sẽ cập nhật khi bạn lưu cài đặt.' : 'Choose an existing backup folder. The monitoring script updates when you save settings.') : sourceOnly ? (vi ? 'Chọn thư mục dữ liệu đã tồn tại trên máy chủ. Chọn ở đây chưa đổi script; cần xác nhận áp dụng ở cuối form.' : 'Choose an existing server data folder. Selecting it does not update scripts until you confirm and apply the form.') : (vi ? 'Chọn thư mục đã tồn tại.' : 'Choose an existing folder.')}</p>
    <div className="backup-system-path"><input aria-label={vi ? 'Đường dẫn thư mục' : 'Folder path'} value={path} onChange={e => setPath(e.target.value)} disabled={busy} /><button type="button" className="backup-system-secondary" disabled={busy} onClick={() => void load(path)}>{vi ? 'Mở' : 'Open'}</button>{request.location === 'local' && <button type="button" className="backup-system-secondary" disabled={busy} onClick={() => void load('/')}>{vi ? 'Thư mục gốc /' : 'Server root /'}</button>}</div>
    <div className="backup-folder-list" aria-busy={busy}>{busy ? <p>{vi ? 'Đang đọc thư mục...' : 'Loading folders...'}</p> : <>
      {listing?.parent !== null && listing?.parent !== undefined && listing.parent !== path && <button type="button" onClick={() => void load(listing.parent!)}>← {vi ? 'Thư mục cha' : 'Parent folder'}</button>}
      {listing?.entries.map(entry => <button type="button" key={entry.path} onClick={() => void load(entry.path)}><Folder size={16} />{entry.name}</button>)}
      {listing?.exists && !listing.entries.length && <p>{vi ? 'Chưa có thư mục con.' : 'No subfolders.'}</p>}
      {listing && !listing.exists && <p>{vi ? 'Thư mục chưa tồn tại.' : 'Folder does not exist.'}</p>}
    </>}</div>
    {error && <p role="alert" className="backup-system-error">{localizeMessage(error, vi ? 'vi' : 'en')}</p>}
    <div className="backup-system-actions"><button type="button" className="apex-settings-save" disabled={busy || !selectable} onClick={() => choose(path)}>{vi ? 'Chọn thư mục này' : 'Use this folder'}</button></div>
  </div></div>, document.querySelector('.app-shell') ?? document.body)
}

export function ProgressView({ progress, lang }: {progress: Progress; lang: Lang}) {
  const vi = lang === 'vi'
  return <div className="task-progress">
    <span>{progress.phase.startsWith('day:') ? `${vi ? 'Ngày' : 'Day'} ${progress.phase.slice(4)}` : ({download: vi ? 'Tải backup' : 'Downloading', extract: vi ? 'Giải nén' : 'Extracting', transfer: vi ? 'Truyền dữ liệu' : 'Transfer', cleanup: vi ? 'Dọn dẹp' : 'Cleanup'}[progress.phase] ?? progress.phase)}{progress.percent != null ? ` · ${progress.percent.toFixed(1)}%` : ''}</span>
    {progress.percent != null && <progress max={100} value={progress.percent} aria-label={vi ? 'Tiến độ giai đoạn hiện tại' : 'Current phase progress'} />}
    <small>{formatBytes(progress.bytes)}{progress.totalBytes > 0 ? ` / ${formatBytes(progress.totalBytes)}` : ''} · {progress.files}{progress.totalFiles > 0 ? ` / ${progress.totalFiles}` : ''} {vi ? 'tệp' : 'files'}{progress.speed > 0 ? ` · ${formatBytes(progress.speed)}/s` : ''}{progress.eta != null ? ` · ETA ${Math.ceil(progress.eta)}s` : ''}</small>
    {progress.stale && <small>{vi ? 'Số liệu chưa cập nhật; tác vụ vẫn đang chạy.' : 'Stats are stale; the process is still running.'}</small>}
  </div>
}
