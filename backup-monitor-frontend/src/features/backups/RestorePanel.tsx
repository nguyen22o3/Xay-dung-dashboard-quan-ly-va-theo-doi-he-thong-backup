import { localizeMessage } from '../../shared/messages'
import { Button, OperationStatus } from '../../shared/ui'
import { ProgressView } from '../../shared/TaskProgress'
import { useEffect, useRef, useState } from 'react'
import { apiErrorMessage } from '../../api/dashboard'
import { previewRestore, startRestore, restoreStatus, type RestorePlan } from '../../api/operations'
import type { Lang } from '../../shared/language'
import type { SnapshotFile } from '../../shared/types'
import { formatBytes } from '../../shared/utils'
import { Folder } from 'lucide-react'
import { FolderPicker } from '../cron/BackupSystemSettings'
import type { FolderRequest } from '../../api/backupSystem'

export default function RestorePanel({ file, lang, close }: { file: SnapshotFile; lang: Lang; close: () => void }) {
  const vi = lang === 'vi'
  const [plan, setPlan] = useState<RestorePlan | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [parent, setParent] = useState('/root')
  const [name, setName] = useState(() => `${file.name.replace(/\.(tar\.gz|sql\.gz|zip)$/, '').replace(/[^A-Za-z0-9_.-]/g, '_').slice(0, 110)}-restored`)
  const [picker, setPicker] = useState<FolderRequest | null>(null)
  const guard = useRef(false)
  const generation = useRef(0)
  const token = plan?.token, status = plan?.status
  const relative = file.path || [file.date, file.category, file.name].join('/')
  useEffect(() => {
    if (!token || !status || !['queued', 'running'].includes(status)) return
    const pending = generation
    const request = ++generation.current
    const update = async () => {
      try { const next = await restoreStatus(token); if (request === generation.current) { setPlan(next); setError('') } }
      catch (e) { if (request === generation.current) setError(apiErrorMessage(e, vi ? 'Không xác nhận được trạng thái; không tự chạy lại.' : 'Status could not be confirmed; do not restart automatically.')) }
    }
    const timer = window.setInterval(() => void update(), 5000)
    return () => { window.clearInterval(timer); pending.current++ }
  }, [token, status, vi])
  const action = async (work: () => Promise<RestorePlan>) => {
    if (guard.current) return
    guard.current = true; setBusy(true); setError('')
    try { setPlan(await work()) }
    catch (e) { setError(apiErrorMessage(e, vi ? 'Không thực hiện được khôi phục.' : 'Could not perform the restore.')); if (plan?.token) { try { setPlan(await restoreStatus(plan.token)) } catch { /* Keep the preview/token for manual verification. */ } } }
    finally { guard.current = false; setBusy(false) }
  }
  const active = !!plan && ['queued', 'running'].includes(plan.status)
  const validName = /^[A-Za-z0-9_-][A-Za-z0-9_.-]*$/.test(name) && name.length <= 128
  const validParent = /^\/[A-Za-z0-9_./-]+$/.test(parent) && parent.length <= 1024 && parent !== '/' && !parent.endsWith('/') && !parent.split('/').slice(1).some(part => !part || part === '.' || part === '..')
  return <section className="restore-panel" aria-label={vi ? 'Tải xuống máy chủ AlmaLinux' : 'Download to AlmaLinux server'}>
    <div className="restore-heading"><h3>{vi ? 'Tải xuống máy chủ AlmaLinux' : 'Download to AlmaLinux server'}</h3><Button type="button" disabled={busy} onClick={close}>{vi ? 'Ẩn' : 'Hide'}</Button></div>
    <p><code>{relative}</code></p>
    {error && <p className="cron-log-error" role="alert">{localizeMessage(error, lang)}</p>}
    {!plan && <>
      <div className="restore-destination-fields">
        <label><span>{vi ? 'Thư mục cha trên AlmaLinux' : 'Parent folder on AlmaLinux'}</span><div className="backup-system-path"><input value={parent} onChange={event => setParent(event.target.value)} disabled={busy} placeholder="/root" /><Button type="button" disabled={busy} onClick={() => setPicker({ location: 'local', purpose: 'backup', path: validParent ? parent : '/root' })} aria-label={vi ? 'Chọn nơi lưu trên AlmaLinux' : 'Choose an AlmaLinux destination'}><Folder size={18} /></Button></div></label>
        <label><span>{vi ? 'Tên thư mục mới chứa dữ liệu giải nén' : 'New folder name for extracted data'}</span><input value={name} onChange={event => setName(event.target.value)} disabled={busy} maxLength={128} placeholder="backup-restored" /></label>
      </div>
      {validParent && validName && <p>{vi ? 'Nơi lưu dữ liệu giải nén: ' : 'Extracted data destination: '}<code>{parent}/{name}</code></p>}
      <Button type="button" disabled={busy || !validParent || !validName} onClick={() => void action(() => previewRestore(relative, parent, name))}>{busy ? (vi ? 'Đang kiểm tra…' : 'Checking…') : (vi ? 'Kiểm tra bản sao lưu và nơi lưu' : 'Check backup and destination')}</Button>
    </>}
    {picker && <FolderPicker request={picker} vi={vi} close={() => setPicker(null)} choose={value => { setParent(value); setPicker(null) }} />}
    {plan && <>
      <p>{vi ? 'Trạng thái: ' : 'Status: '}<OperationStatus state={plan.status} lang={lang} /> · {formatBytes(plan.size)}</p>
      {active && plan.progress && <ProgressView progress={plan.progress} lang={lang} />}
      <p>{vi ? 'Thư mục trên máy chủ: ' : 'Server directory: '}<code>{plan.directory}</code></p>
      <p>{vi ? 'Mã yêu cầu: ' : 'Request ID: '}<code>{plan.token}</code></p>
      {plan.error && <p role="alert" className="cron-log-error">{localizeMessage(plan.error, lang)}</p>}
      {plan.status === 'preview' && <Button type="button" disabled={busy} onClick={() => { generation.current++; setPlan(null); setError('') }}>{vi ? 'Đổi nơi lưu hoặc tên' : 'Change destination or name'}</Button>}
      {plan.status === 'preview' && <Button variant="primary" type="button" disabled={busy} onClick={() => { if (window.confirm(vi ? `Tải và giải nén ${relative} vào ${plan.directory} trên AlmaLinux? Có thể tốn dung lượng và thời gian. Không ghi đè dữ liệu đang chạy.` : `Download and extract ${relative} into ${plan.directory} on AlmaLinux? This may take disk space and time. Live data will not be overwritten.`)) void action(() => startRestore(plan.token)) }}>{vi ? 'Xác nhận tải xuống máy chủ' : 'Confirm server download'}</Button>}
      {active && <p role="status">{vi ? 'Đang xử lý trên máy chủ. Ẩn mục này không hủy thao tác; lưu mã yêu cầu để kiểm tra lại.' : 'Processing on the server. Hiding this panel does not cancel the operation; keep the request ID for later checks.'}</p>}
      {plan.status === 'ready' && <p role="status">{vi ? `Đã chuẩn bị ${plan.files} tệp (${formatBytes(plan.expandedBytes)}). Kiểm tra nội dung trong thư mục riêng trước khi áp dụng thủ công.` : `${plan.files} files prepared (${formatBytes(plan.expandedBytes)}). Inspect the staging directory before applying manually.`}</p>}
      <Button type="button" disabled={busy} onClick={() => void action(() => restoreStatus(plan.token))}>{vi ? 'Kiểm tra trạng thái' : 'Check status'}</Button>
    </>}
  </section>
}
