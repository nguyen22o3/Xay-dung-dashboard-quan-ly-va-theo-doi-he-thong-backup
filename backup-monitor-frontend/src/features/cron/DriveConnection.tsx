import { useEffect, useState } from 'react'
import type { FormEvent } from 'react'
import { Cloud, ChevronDown } from 'lucide-react'
import { connectDrive, fetchDriveConnection } from '../../api/backupSystem'
import type { DriveConnectionStatus } from '../../api/backupSystem'
import { apiErrorMessage } from '../../api/dashboard'
import type { Lang } from '../../shared/language'

export default function DriveConnection({ lang, locked, onConnected, onBusyChange }: { lang: Lang; locked: boolean; onConnected: () => void; onBusyChange: (busy: boolean) => void }) {
  const vi = lang === 'vi'
  const [status, setStatus] = useState<DriveConnectionStatus | null>(null)
  const [expanded, setExpanded] = useState(false)
  const [remote, setRemote] = useState('gdrive'), [clientId, setClientId] = useState('')
  const [clientSecret, setClientSecret] = useState(''), [token, setToken] = useState('')
  const [busy, setBusy] = useState(false), [error, setError] = useState(''), [saved, setSaved] = useState(false)
  const [lanConsent, setLanConsent] = useState(false)
  const secure = window.location.protocol === 'https:' || ['localhost', '127.0.0.1', '[::1]'].includes(window.location.hostname)
  useEffect(() => { onBusyChange(busy); return () => onBusyChange(false) }, [busy, onBusyChange])
  useEffect(() => {
    let alive = true
    fetchDriveConnection().then(result => { if (alive) { setStatus(result); setExpanded(!result.remotes.length) } })
      .catch((err: unknown) => { if (alive) setError(apiErrorMessage(err, vi ? 'Cần cập nhật công cụ trên máy chủ.' : 'Update the host helper.')) })
    return () => { alive = false }
  }, [vi])
  const save = async (event: FormEvent) => {
    event.preventDefault()
    if (busy || locked || (!secure && !lanConsent)) return
    setBusy(true); setError(''); setSaved(false)
    try {
      setStatus(await connectDrive({ remote, clientId, clientSecret, token }))
      setToken(''); setClientSecret(''); setSaved(true); onConnected()
    } catch (err: unknown) { setError(apiErrorMessage(err, vi ? 'Không kết nối được Google Drive.' : 'Could not connect Google Drive.')) }
    finally { setBusy(false) }
  }
  const fields = [
    { id: 'remote', label: vi ? 'Tên kết nối' : 'Remote name', value: remote, update: setRemote, type: 'text' },
    { id: 'client', label: 'Client ID', value: clientId, update: setClientId, type: 'text' },
    { id: 'secret', label: 'Client secret', value: clientSecret, update: setClientSecret, type: 'password' },
  ]
  return <section className="apex-settings-panel backup-system-panel drive-connection-panel">
    <div className="backup-storage-heading"><h2><button type="button" aria-expanded={expanded} aria-controls="drive-connection-fields" onClick={() => setExpanded(!expanded)}><Cloud size={18} />{vi ? 'Kết nối Google Drive' : 'Connect Google Drive'}<ChevronDown size={17} className={expanded ? 'is-expanded' : ''} /></button></h2><span className="backup-system-note">{status?.remotes.join(', ')}</span></div>
    <div id="drive-connection-fields" className="backup-storage-body" hidden={!expanded}>
      <p className="backup-system-note">{vi ? 'Kết nối tài khoản của bạn; không ghi đè kết nối cũ, không tạo lịch hoặc chạy backup.' : 'Connect your account; existing connections are preserved. No schedules or backups are started.'}</p>
      <p className="backup-system-note">{vi ? 'Tạo OAuth client dạng Desktop theo ' : 'Create a Desktop OAuth client using '}<a href="https://rclone.org/drive/#making-your-own-client-id" target="_blank" rel="noopener noreferrer">{vi ? 'hướng dẫn rclone' : 'the rclone guide'}</a>{vi ? ', rồi chạy trên máy có trình duyệt:' : ', then run on a computer with a browser:'}</p>
      <pre className="drive-authorize-command">rclone authorize drive "CLIENT_ID" "CLIENT_SECRET"</pre>
      <p className="backup-system-note">{vi ? 'Đăng nhập Google, cấp quyền, dán nguyên JSON token rclone trả về. Không chia sẻ token.' : 'Sign in to Google, authorize access, then paste the complete rclone token JSON. Do not share it.'}</p>
      {error && <p className="backup-system-error" role="alert">{error}</p>}
      {saved && <p className="backup-system-success" role="status">{vi ? 'Đã lưu. Chọn kết nối và thư mục Drive trong phần Thêm tác vụ.' : 'Saved. Select the remote and Drive folder when adding a task.'}</p>}
      {status && !status.rcloneInstalled && <p className="backup-system-error">{vi ? 'Máy chủ chưa có /usr/bin/rclone. Chạy bộ cài local trước.' : 'Install /usr/bin/rclone on the host using the local installer.'}</p>}
      <form className="backup-unified-form" onSubmit={event => void save(event)}>
        <div className="backup-system-fields">
          {fields.map(field => <div className="apex-settings-field" key={field.id}><label htmlFor={`drive-connect-${field.id}`}>{field.label}</label><input id={`drive-connect-${field.id}`} type={field.type} value={field.value} onChange={event => field.update(event.target.value.trim())} autoComplete={field.type === 'password' ? 'new-password' : 'off'} spellCheck={false} required disabled={busy || locked} maxLength={field.id === 'remote' ? 64 : 256} /></div>)}
          <div className="apex-settings-field"><label htmlFor="drive-connect-token">OAuth token JSON</label><textarea id="drive-connect-token" value={token} onChange={event => setToken(event.target.value)} rows={4} maxLength={16384} autoComplete="off" spellCheck={false} required disabled={busy || locked} /></div>
        </div>
        {!secure && <label className="backup-system-warning drive-lan-consent"><input type="checkbox" checked={lanConsent} onChange={event => setLanConsent(event.target.checked)} disabled={busy || locked} />{vi ? 'Địa chỉ chưa dùng HTTPS. Tôi dùng LAN tin cậy và hiểu thông tin kết nối có thể bị đọc trên mạng. Dùng HTTPS cho triển khai thực tế.' : 'This is not HTTPS. I use a trusted LAN and understand credentials may be read on the network. Use HTTPS for production.'}</label>}
        <div className="backup-system-actions"><button type="submit" className="apex-settings-save" disabled={busy || locked || !status?.rcloneInstalled || (!secure && !lanConsent)}>{busy ? (vi ? 'Đang kiểm tra...' : 'Checking...') : (vi ? 'Kiểm tra và lưu kết nối' : 'Verify and save connection')}</button></div>
      </form>
    </div>
  </section>
}
