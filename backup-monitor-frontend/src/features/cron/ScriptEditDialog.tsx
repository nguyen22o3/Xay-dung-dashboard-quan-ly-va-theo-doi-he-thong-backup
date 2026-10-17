import { localizeMessage } from '../../shared/messages'
import { backupTaskLabels } from './backupTasks'
import { useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { RefreshCw, X } from 'lucide-react'
import { apiErrorMessage } from '../../api/dashboard'
import { readTaskScript, saveTaskScript, type ScriptDocument } from '../../api/backupSystem'
import type { Lang } from '../../shared/language'
import type { CronJob } from '../../shared/types'

export default function ScriptEditDialog({ job, lang, running, onClose, onSaved }: { job: CronJob; lang: Lang; running: boolean; onClose: () => void; onSaved: (path: string) => void }) {
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
