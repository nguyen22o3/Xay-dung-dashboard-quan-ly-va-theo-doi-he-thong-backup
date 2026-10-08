import { Button, OperationStatus } from '../../shared/ui'
import { Fragment, useCallback, useEffect, useRef, useState } from 'react'
import { apiErrorMessage } from '../../api/dashboard'
import type { Lang } from '../../shared/language'
import { fetchAlerts, operationsStatus, saveMonitorOptions, sendTestNotification, retryNotification, type OperationsStatus, type AlertEvent } from '../../api/operations'

const dateLabel = (value: string, vi: boolean) => new Date(value).toLocaleString(vi ? 'vi-VN' : 'en-GB', { timeZone: 'Asia/Ho_Chi_Minh', hour12: false })
const channelLabel = (channel: string) => ({ telegram: 'Telegram', discord: 'Discord', email: 'Email' })[channel] ?? channel
const deliveryState = (alert: AlertEvent) => {
  if (!alert.deliveries) return 'unknown'
  if (!alert.deliveries.length) return 'not_sent'
  if (alert.deliveries.some(delivery => delivery.status === 'failed')) return 'failed'
  if (alert.deliveries.some(delivery => delivery.status === 'sending')) return 'sending'
  if (alert.deliveries.every(delivery => delivery.status === 'sent')) return 'sent'
  if (alert.deliveries.some(delivery => delivery.status === 'pending')) return 'pending'
  return 'unknown'
}
export default function OperationsPanel({ lang }: { lang: Lang }) {
  const vi = lang === 'vi'
  const [status, setStatus] = useState<OperationsStatus | null>(null)
  const [alerts, setAlerts] = useState<AlertEvent[]>([])
  const [expanded, setExpanded] = useState<number[]>([])
  const [error, setError] = useState('')
  const [message, setMessage] = useState('')
  const [busy, setBusy] = useState(false)
  const guard = useRef(false)
  const generation = useRef(0)
  const reload = useCallback(async () => {
    const current = ++generation.current
    try {
      const next = await operationsStatus()
      if (current !== generation.current) return
      setStatus(previous => previous?.options ? { ...next, options: previous.options } : next)
      if (next.enabled) {
        const events = await fetchAlerts()
        if (current !== generation.current) return
        setAlerts(events)
      }
      setError('')
    } catch (e) { if (current === generation.current) setError(apiErrorMessage(e, vi ? 'Không đọc được giám sát.' : 'Monitoring unavailable.')) }
  }, [vi])
  useEffect(() => { const initial = window.setTimeout(() => void reload(), 0); const timer = window.setInterval(() => void reload(), 30000); const pending = generation; return () => { window.clearTimeout(initial); window.clearInterval(timer); pending.current++ } }, [reload])
  const action = async (work: () => Promise<unknown>, success: string) => {
    if (guard.current) return
    guard.current = true; setBusy(true); setError(''); setMessage('')
    try { await work(); setMessage(success); await reload() }
    catch (e) { setError(apiErrorMessage(e, vi ? 'Không thực hiện được.' : 'Action failed.')) }
    finally { guard.current = false; setBusy(false) }
  }
  const edit = (key: string, value: boolean | number) => setStatus(previous => previous?.options ? { ...previous, options: { ...previous.options, [key]: value } } : previous)
  const opts = status?.options
  return <section className="ops-panel">
    <div className="ops-heading"><h3>{vi ? 'Cảnh báo tự động & lịch sử' : 'Automatic alerts & history'}</h3><Button type="button" onClick={() => void reload()} disabled={busy}>{vi ? 'Làm mới' : 'Refresh'}</Button></div>
    {error && <p role="alert" className="cron-log-error">{error}</p>}{message && <p role="status">{message}</p>}
    {status?.enabled && status.workerEnabled !== false && status.options?.enabled && <p className="ops-note" role={status.collectionStale ? 'alert' : undefined}>{vi ? 'Kiểm tra cảnh báo thành công gần nhất: ' : 'Last successful alert check: '}{status.collectedAt ? dateLabel(status.collectedAt, vi) : '—'}{status.collectionStale && (vi ? ' · Chưa xác nhận dữ liệu mới; kiểm tra backend/kết nối máy chủ.' : ' · Data is stale or not checked yet; check the backend/server connection.')}</p>}
    {!status?.enabled ? <p className="ops-note">{vi ? 'Chưa đọc được trạng thái vận hành. Kiểm tra backend và quyền ghi file JSON.' : 'Operations state unavailable. Check the backend and JSON file permissions.'}</p> : <>
      <p className="ops-note">{vi ? 'Quy tắc và lịch sử gửi được lưu bằng JSON ngoài ứng dụng; không dùng database. Nhật ký tác vụ đọc từ file máy chủ.' : 'Rules and delivery history use a persistent JSON file outside the application. Task logs come from server files.'}</p>
      {status.workerEnabled === false && <p role="alert" className="ops-note">{vi ? 'Worker cảnh báo đang tắt trên backend này. Bật OPERATIONS_WORKER_ENABLED trên một backend duy nhất để tự giám sát và gửi thông báo.' : 'The alert worker is disabled here. Enable OPERATIONS_WORKER_ENABLED on only one backend to monitor and send notifications.'}</p>}
      {opts && <fieldset disabled={busy} className="ops-options"><legend>{vi ? 'Luồng cảnh báo chung' : 'Unified alert rules'}</legend>
        {(['enabled', 'backupFailed', 'driveFailed', 'missedSchedule'] as const).map((key, index) => <label key={key}><input type="checkbox" checked={opts[key]} onChange={e => edit(key, e.target.checked)} />{(vi ? ['Bật cảnh báo tự động', 'Sao lưu thất bại', 'Đồng bộ / kết nối Drive lỗi', 'Bỏ lỡ lịch chạy (cron có theo dõi)'] : ['Enable automatic alerts', 'Backup failures', 'Drive sync / connectivity failure', 'Missed scheduled runs (tracked cron only)'])[index]}</label>)}
        <label>{vi ? 'Ngưỡng sử dụng ổ đĩa (%)' : 'Disk usage threshold (%)'}<input type="number" min={50} max={100} value={opts.diskThreshold} onChange={e => edit('diskThreshold', Number(e.target.value))} /></label>
        <label>{vi ? 'Thời gian cho phép trễ lịch (phút)' : 'Schedule grace period (minutes)'}<input type="number" min={5} max={1440} value={opts.graceMinutes} onChange={e => edit('graceMinutes', Number(e.target.value))} /></label>
        <Button variant="primary" type="button" onClick={() => void action(() => saveMonitorOptions(opts), vi ? 'Đã lưu quy tắc cảnh báo.' : 'Alert rules saved.')}>{vi ? 'Lưu quy tắc' : 'Save rules'}</Button>
      </fieldset>}
      <div className="ops-actions">{['telegram', 'discord', 'email'].map(channel => <Button type="button" key={channel} disabled={busy || status.workerEnabled === false} onClick={() => { if (window.confirm(vi ? `Gửi thông báo thử tới ${channel} đã cấu hình?` : `Send a test notification to the configured ${channel} destination?`)) void action(() => sendTestNotification(channel), vi ? 'Đã xếp hàng gửi thử; xem trạng thái bên dưới.' : 'Test queued; check delivery status below.') }}>{vi ? 'Gửi thử ' : 'Test '}{channel}</Button>)}</div>
      <p className="ops-note">{vi ? 'Dùng kênh đã lưu trong mục Cảnh báo. Thử gửi tối đa 5 lần với thời gian chờ tăng dần; mỗi sự cố có khóa chống gửi trùng. Thông báo đang gửi khi mất kết nối có thể được gửi lại.' : 'Uses saved channels. Up to five delivery attempts with backoff and incident deduplication. Interrupted sends may be retried.'}</p>
      <details open><summary>{vi ? 'Cảnh báo & thông báo' : 'Alerts & notifications'}</summary>
        <div className="ops-scroll"><table aria-label={vi ? 'Cảnh báo & thông báo' : 'Alerts & notifications'}><thead><tr>
          <th>{vi ? 'Thời điểm' : 'Time'}</th><th>{vi ? 'Nội dung' : 'Message'}</th><th>{vi ? 'Kênh gửi' : 'Channels'}</th><th>{vi ? 'Trạng thái gửi' : 'Delivery status'}</th><th>{vi ? 'Thao tác' : 'Actions'}</th>
        </tr></thead><tbody>{alerts.map(row => {
          const deliveries = row.deliveries ?? []
          const open = expanded.includes(row.id)
          return <Fragment key={row.id}>
            <tr data-alert-id={row.id}>
              <td>{dateLabel(row.observedAt, vi)}</td>
              <td>{row.kind === 'test' && <small className="ops-test-label">{vi ? 'Gửi thử' : 'Test notification'}</small>}{row.message}</td>
              <td>{deliveries.length ? deliveries.map(delivery => channelLabel(delivery.channel)).join(', ') : '—'}</td>
              <td><OperationStatus state={deliveryState(row)} lang={lang} />{deliveries.length > 0 && <small>{vi ? `Đã gửi ${deliveries.filter(delivery => delivery.status === 'sent').length}/${deliveries.length} kênh` : `Sent to ${deliveries.filter(delivery => delivery.status === 'sent').length}/${deliveries.length} channels`}</small>}</td>
              <td>{deliveries.length > 0 && <Button type="button" aria-expanded={open} aria-controls={`alert-deliveries-${row.id}`} onClick={() => setExpanded(value => open ? value.filter(id => id !== row.id) : [...value, row.id])}>{open ? (vi ? 'Thu gọn' : 'Collapse') : (vi ? 'Chi tiết' : 'Details')}</Button>}</td>
            </tr>
            {open && deliveries.length > 0 && <tr><td colSpan={5} id={`alert-deliveries-${row.id}`}>
              <div className="ops-delivery-list">{deliveries.map(delivery => <div className="ops-delivery-item" key={delivery.id}>
                <div className="ops-actions"><strong>{channelLabel(delivery.channel)}</strong><OperationStatus state={delivery.status} lang={lang} /><span>{vi ? `${delivery.attempts} lần thử` : `${delivery.attempts} attempts`}</span>
                  {delivery.status === 'failed' && <Button type="button" disabled={busy || status.workerEnabled === false} onClick={() => { if (window.confirm(vi ? `Gửi lại thông báo này qua ${channelLabel(delivery.channel)}?` : `Retry this notification via ${channelLabel(delivery.channel)}?`)) void action(() => retryNotification(delivery.id), vi ? 'Đã xếp hàng gửi lại.' : 'Retry queued.') }}>{vi ? 'Gửi lại' : 'Retry'}</Button>}
                </div>
                {delivery.lastError && <p className="cron-log-error">{delivery.lastError}</p>}
              </div>)}</div>
            </td></tr>}
          </Fragment>
        })}</tbody></table>{!alerts.length && <p>{vi ? 'Chưa có cảnh báo hoặc thông báo.' : 'No alerts or notifications yet.'}</p>}</div>
        {alerts.some(row => !row.deliveries) && <p className="ops-note">{vi ? 'Backend chưa cung cấp chi tiết kênh gửi; cần cập nhật và khởi động lại backend để xem đầy đủ.' : 'The backend does not provide delivery details yet; update and restart it to see all channels.'}</p>}
      </details>
    </>}
  </section>
}
