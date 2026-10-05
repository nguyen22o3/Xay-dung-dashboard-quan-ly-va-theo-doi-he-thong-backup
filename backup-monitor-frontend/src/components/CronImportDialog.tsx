import { useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { X } from 'lucide-react'
import { apiErrorMessage, fetchCronJobs } from '../api'
import { applyBackupSystem, fetchBackupSystem, previewBackupSystem } from '../backupSystemApi'
import { backupTaskLabels } from '../backupTasks'
import { applyCronImport, CronImportError } from '../cronTransfer'
import type { CronImportRow } from '../cronTransfer'
import type { Lang } from '../language'
import { formatCronSchedule } from '../utils'

type Props = { lang: Lang; filename: string; plan: CronImportRow[]; onClose: () => void; onBusyChange: (busy: boolean) => void; onUpdated: () => void; onSaved: (count: number) => void }

export default function CronImportDialog({ lang, filename, plan, onClose, onBusyChange, onUpdated, onSaved }: Props) {
  const vi = lang === 'vi'
  const [busy, setBusy] = useState(false), [error, setError] = useState(''), [progress, setProgress] = useState(0)
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
    applying.current = true; setBusy(true); onBusyChange(true); setError('')
    try {
      const saved = await applyCronImport(plan, {
        jobs: fetchCronJobs, settings: fetchBackupSystem,
        preview: (state, change) => previewBackupSystem(state.config, state.version, change),
        apply: (state, token, change) => applyBackupSystem(state.config, state.version, token, change),
      }, completed => setProgress(completed))
      onSaved(saved.length)
    } catch (cause: unknown) {
      const message = apiErrorMessage(cause, vi ? 'Không nhập được lịch Cron.' : 'Could not import Cron schedules.')
      const partial = cause instanceof CronImportError ? cause : null
      setError([
        message,
        vi ? `Đã xác minh lưu ${partial?.completedIds.length ?? 0} tác vụ. Dừng nhập; các mục còn lại chưa được áp dụng.` : `${partial?.completedIds.length ?? 0} saved tasks verified. Import stopped; remaining tasks were not applied.`,
        partial?.failedId ? `${vi ? 'Tác vụ cần kiểm tra' : 'Check task'}: ${backupTaskLabels[partial.failedId]?.[lang] ?? partial.failedId}.` : '',
        partial?.writeAttempted ? (vi ? 'Tác vụ lỗi có thể đã được lưu. Hãy kiểm tra danh sách trước khi nhập lại.' : 'The failed task may have been saved. Check the list before importing again.') : '',
      ].filter(Boolean).join(' '))
    } finally {
      applying.current = false; setBusy(false); onBusyChange(false); onUpdated()
    }
  }
  return createPortal(<div className="backup-task-edit-overlay backup-management" onMouseDown={event => { if (event.target === event.currentTarget && !applying.current) onClose() }}>
    <div className="cron-import-dialog" ref={dialog} role="dialog" aria-modal="true" aria-labelledby="cron-import-title" aria-describedby="cron-import-description">
      <div className="backup-storage-heading"><h2 id="cron-import-title">{vi ? 'Import lịch Cron' : 'Import Cron schedules'}</h2><button type="button" className="backup-system-secondary" disabled={busy} onClick={onClose} aria-label={vi ? 'Đóng' : 'Close'}><X size={18} /></button></div>
      <div className="cron-import-body">
        <p className="cron-import-filename">{filename}</p>
        <p id="cron-import-description">{vi ? 'Chỉ nhập lịch chạy. Giữ trạng thái bật/tạm dừng của tác vụ hiện có; tác vụ mới sẽ được bật. Không đổi script hoặc nơi lưu, không xóa tác vụ ngoài tệp, không chạy backup ngay. Áp dụng lần lượt; nếu lỗi sẽ dừng và báo các mục đã lưu.' : 'Import schedules only. Existing enabled/paused states stay unchanged; new tasks are enabled. Scripts and storage are unchanged, unlisted tasks are not deleted, and no backup runs now. Tasks are applied one by one; failures stop the import and report saved items.'}</p>
        <div className="cron-import-table-wrap"><table><thead><tr><th>{vi ? 'Tác vụ' : 'Task'}</th><th>{vi ? 'Lịch hiện tại' : 'Current schedule'}</th><th>{vi ? 'Lịch nhập' : 'Imported schedule'}</th><th>{vi ? 'Thay đổi' : 'Change'}</th></tr></thead><tbody>{plan.map(row => <tr key={row.id}><td>{backupTaskLabels[row.id][lang]}</td><td>{row.previous === null ? (vi ? 'Chưa có lịch' : 'Not scheduled') : formatCronSchedule(row.previous, lang)}</td><td>{formatCronSchedule(row.schedule, lang)}</td><td>{row.previous === null ? (vi ? 'Tạo mới · bật' : 'Create · enabled') : row.change ? (vi ? 'Cập nhật' : 'Update') : (vi ? 'Giữ nguyên' : 'Unchanged')}</td></tr>)}</tbody></table></div>
        {!changed && <p role="status">{vi ? 'Các lịch trong tệp đã giống lịch hiện tại, không cần nhập.' : 'Schedules already match; nothing to import.'}</p>}
        {busy && <p role="status">{vi ? `Đang kiểm tra và nhập: ${progress}/${plan.length} tác vụ…` : `Checking and importing: ${progress}/${plan.length} tasks…`}</p>}
        {error && <p className="backup-system-error" role="alert">{error}</p>}
      </div>
      <div className="backup-system-actions"><button type="button" className="backup-system-secondary" disabled={busy} onClick={onClose}>{vi ? 'Hủy' : 'Cancel'}</button><button type="button" className="apex-settings-save" disabled={busy || !changed || !!error} onClick={() => void apply()}>{busy ? (vi ? 'Đang nhập…' : 'Importing…') : (vi ? 'Nhập cấu hình' : 'Import configuration')}</button></div>
    </div>
  </div>, document.querySelector('.app-shell') ?? document.body)
}
