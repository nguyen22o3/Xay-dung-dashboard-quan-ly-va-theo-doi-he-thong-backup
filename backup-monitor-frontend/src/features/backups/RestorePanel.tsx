import { Button, OperationStatus } from '../../shared/ui'
import { ProgressView } from '../../shared/TaskProgress'
import { useEffect, useRef, useState } from 'react'
import { apiErrorMessage } from '../../api/dashboard'
import { previewRestore, startRestore, restoreStatus, type RestorePlan } from '../../api/operations'
import type { Lang } from '../../shared/language'
import type { SnapshotFile } from '../../shared/types'
import { formatBytes } from '../../shared/utils'

export default function RestorePanel({ file, lang, close }: { file: SnapshotFile; lang: Lang; close: () => void }) {
  const vi = lang === 'vi'
  const [plan, setPlan] = useState<RestorePlan | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
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
      catch (e) { if (request === generation.current) setError(apiErrorMessage(e, vi ? 'Không xác nhận được trạng thái; không tự chạy lại.' : 'Status unavailable; do not restart blindly.')) }
    }
    const timer = window.setInterval(() => void update(), 5000)
    return () => { window.clearInterval(timer); pending.current++ }
  }, [token, status, vi])
  const action = async (work: () => Promise<RestorePlan>) => {
    if (guard.current) return
    guard.current = true; setBusy(true); setError('')
    try { setPlan(await work()) }
    catch (e) { setError(apiErrorMessage(e, vi ? 'Không thực hiện được khôi phục.' : 'Restore failed.')); if (plan?.token) { try { setPlan(await restoreStatus(plan.token)) } catch { /* Keep the preview/token for manual verification. */ } } }
    finally { guard.current = false; setBusy(false) }
  }
  const active = !!plan && ['queued', 'running'].includes(plan.status)
  return <section className="ops-panel" aria-label={vi ? 'Khôi phục bản sao lưu' : 'Restore backup'}>
    <div className="ops-heading"><h3>{vi ? 'Khôi phục vào thư mục riêng' : 'Restore into a separate directory'}</h3><Button type="button" disabled={busy} onClick={close}>{vi ? 'Ẩn' : 'Hide'}</Button></div>
    <p><code>{relative}</code></p>
    <p className="ops-note">{vi ? 'Không ghi đè website/aaPanel và không nhập SQL vào database đang chạy. Tệp ZIP/TAR được giải nén an toàn; database.sql chỉ được chuẩn bị để kiểm tra. Thư mục khôi phục được giữ lại, không tự xóa.' : 'Does not overwrite live websites/aaPanel or import SQL into a running database. ZIP/TAR archives are safely extracted; database.sql is prepared for inspection only. Staging directories are preserved.'}</p>
    {error && <p className="cron-log-error" role="alert">{error}</p>}
    {!plan && <Button type="button" disabled={busy} onClick={() => void action(() => previewRestore(relative))}>{busy ? (vi ? 'Đang kiểm tra…' : 'Checking…') : (vi ? 'Kiểm tra bản sao lưu' : 'Check backup')}</Button>}
    {plan && <>
      <p>{vi ? 'Trạng thái: ' : 'Status: '}<OperationStatus state={plan.status} lang={lang} /> · {formatBytes(plan.size)}</p>
      {active && plan.progress && <ProgressView progress={plan.progress} lang={lang} />}
      <p>{vi ? 'Thư mục trên máy chủ: ' : 'Server directory: '}<code>{plan.directory}</code></p>
      <p>{vi ? 'Mã yêu cầu: ' : 'Request ID: '}<code>{plan.token}</code></p>
      {plan.error && <p role="alert" className="cron-log-error">{plan.error}</p>}
      {plan.status === 'preview' && <Button variant="primary" type="button" disabled={busy} onClick={() => { if (window.confirm(vi ? `Tải và khôi phục ${relative} vào ${plan.directory}? Có thể tốn dung lượng và thời gian. Không ghi đè dữ liệu đang chạy.` : 'Download and extract this backup into its private staging directory? Live data will not be overwritten.')) void action(() => startRestore(plan.token)) }}>{vi ? 'Xác nhận khôi phục' : 'Confirm restore'}</Button>}
      {active && <p role="status">{vi ? 'Đang xử lý trên máy chủ. Ẩn mục này không hủy thao tác; lưu mã yêu cầu để kiểm tra lại.' : 'Processing on the server. Hiding this panel does not cancel the operation; keep the request ID for later checks.'}</p>}
      {plan.status === 'ready' && <p role="status">{vi ? `Đã chuẩn bị ${plan.files} tệp (${formatBytes(plan.expandedBytes)}). Kiểm tra nội dung trong thư mục riêng trước khi áp dụng thủ công.` : `${plan.files} files prepared (${formatBytes(plan.expandedBytes)}). Inspect the staging directory before applying manually.`}</p>}
      <Button type="button" disabled={busy} onClick={() => void action(() => restoreStatus(plan.token))}>{vi ? 'Kiểm tra trạng thái' : 'Check status'}</Button>
    </>}
  </section>
}
