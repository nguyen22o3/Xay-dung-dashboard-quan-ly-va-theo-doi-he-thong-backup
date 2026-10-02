import { useEffect, useState } from 'react'
import { Check, CheckCircle2, ChevronDown, CircleAlert, Cloud, Hash, Languages, Mail, MessageCircle, Moon, Save, ShieldCheck, Sun } from 'lucide-react'
import type { LucideIcon } from 'lucide-react'
import { apiErrorMessage, fetchAlertSettings, saveAlertSettings } from '../api'
import type { Lang } from '../language'

type Section = 'alerts' | 'monitoring' | 'appearance'
type ChannelId = 'telegram' | 'discord' | 'email'
type Props = { isDark: boolean; onToggleDark: () => void; lang: Lang; onToggleLang: () => void }
type Channel = { id: ChannelId; title: string; vi: string; en: string; icon: LucideIcon }

const channels: Channel[] = [
  { id: 'telegram', title: 'Telegram', vi: 'Nhận cảnh báo qua Telegram Bot và Chat ID', en: 'Receive alerts through a Telegram Bot and Chat ID', icon: MessageCircle },
  { id: 'discord', title: 'Discord', vi: 'Gửi thông báo tới một kênh qua webhook', en: 'Deliver notifications to a channel through a webhook', icon: Hash },
  { id: 'email', title: 'Email', vi: 'Gửi cảnh báo qua Gmail SMTP', en: 'Send alerts through Gmail SMTP', icon: Mail },
]

const tabs: { id: Section; vi: string; en: string }[] = [
  { id: 'alerts', vi: 'Cảnh báo', en: 'Alerts' },
  { id: 'monitoring', vi: 'Giám sát', en: 'Monitoring' },
  { id: 'appearance', vi: 'Giao diện', en: 'Appearance' },
]

export default function Settings({ isDark, onToggleDark, lang, onToggleLang }: Props) {
  const vi = lang === 'vi'
  const [section, setSection] = useState<Section>('alerts')
  const [expanded, setExpanded] = useState<ChannelId | null>(null)
  const [telegramToken, setTelegramToken] = useState('')
  const [telegramChat, setTelegramChat] = useState('')
  const [discordWebhook, setDiscordWebhook] = useState('')
  const [smtpEmail, setSmtpEmail] = useState('')
  const [smtpPassword, setSmtpPassword] = useState('')
  const [targetEmail, setTargetEmail] = useState('')
  const [threshold, setThreshold] = useState(14)
  const [configured, setConfigured] = useState<Record<ChannelId, boolean>>({ telegram: false, discord: false, email: false })
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [message, setMessage] = useState('')
  const [messageType, setMessageType] = useState<'success' | 'error' | null>(null)

  useEffect(() => {
    let alive = true
    fetchAlertSettings()
      .then((settings) => {
        if (!alive) return
        setTelegramChat(settings.telegramChat || '')
        setSmtpEmail(settings.smtpEmail || '')
        setTargetEmail(settings.targetEmail || '')
        setThreshold(settings.threshold || 14)
        setConfigured({ telegram: settings.telegramConfigured, discord: settings.discordConfigured, email: settings.smtpConfigured })
      })
      .catch((error: unknown) => {
        if (!alive) return
        setMessageType('error')
        setMessage(apiErrorMessage(error, 'Không thể tải cấu hình cảnh báo / Could not load alert settings.'))
      })
      .finally(() => { if (alive) setLoading(false) })
    return () => { alive = false }
  }, [])

  const save = async () => {
    setSaving(true)
    setMessage('')
    setMessageType(null)
    try {
      const settings = await saveAlertSettings({ telegramToken, telegramChat, discordWebhook, smtpEmail, smtpPassword, targetEmail, threshold: Number(threshold) })
      setTelegramToken('')
      setDiscordWebhook('')
      setSmtpPassword('')
      setConfigured({ telegram: settings.telegramConfigured, discord: settings.discordConfigured, email: settings.smtpConfigured })
      setMessageType('success')
      setMessage(vi ? 'Đã lưu cấu hình thành công.' : 'Settings saved successfully.')
    } catch (error: unknown) {
      setMessageType('error')
      setMessage(apiErrorMessage(error, vi ? 'Không thể lưu cấu hình.' : 'Could not save settings.'))
    } finally {
      setSaving(false)
    }
  }

  const secretPlaceholder = (isConfigured: boolean, example: string) => isConfigured
    ? (vi ? 'Đã cấu hình — để trống để giữ nguyên' : 'Configured — leave blank to keep it')
    : example

  const saveFooter = <div className="apex-settings-footer">
    {message && <div className={`apex-settings-feedback ${messageType === 'error' ? 'is-error' : ''}`} role={messageType === 'error' ? 'alert' : 'status'}>
      {messageType === 'error' ? <CircleAlert size={16} /> : <CheckCircle2 size={16} />}
      <span>{message}</span>
    </div>}
    <button className="apex-settings-save" type="button" onClick={save} disabled={loading || saving}><Save size={16} />{saving ? (vi ? 'Đang lưu...' : 'Saving...') : (vi ? 'Lưu thay đổi' : 'Save changes')}</button>
  </div>

  return <div className="apex-settings-page animate-fade-in">
    <header className="apex-settings-heading"><h1>{vi ? 'Cài đặt' : 'Settings'}</h1><p>{vi ? 'Quản lý cảnh báo, giám sát và tùy chọn giao diện.' : 'Manage alerts, monitoring, and appearance preferences.'}</p></header>
    <div className="apex-settings-tabs" role="tablist" aria-label={vi ? 'Nhóm cài đặt' : 'Settings sections'}>
      {tabs.map((tab) => <button key={tab.id} id={`settings-tab-${tab.id}`} role="tab" type="button" aria-controls={`settings-panel-${tab.id}`} aria-selected={section === tab.id} tabIndex={section === tab.id ? 0 : -1} className={section === tab.id ? 'is-active' : ''} onClick={() => setSection(tab.id)} onKeyDown={(event) => {
        if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') return
        event.preventDefault()
        const index = tabs.findIndex((item) => item.id === section)
        const next = tabs[(index + (event.key === 'ArrowRight' ? 1 : tabs.length - 1)) % tabs.length]
        setSection(next.id)
        document.getElementById(`settings-tab-${next.id}`)?.focus()
      }}>{vi ? tab.vi : tab.en}</button>)}
    </div>

    {section === 'alerts' && <section className="apex-settings-panel" role="tabpanel" id="settings-panel-alerts" aria-labelledby="settings-tab-alerts">
      <div className="apex-settings-panel-heading"><h2>{vi ? 'Kênh cảnh báo' : 'Alert channels'}</h2><p>{vi ? 'Chọn nơi nhận thông báo khi hệ thống phát hiện sự cố sao lưu. “Đã cấu hình” xác nhận thông tin đã lưu, chưa xác nhận gửi được thông báo.' : 'Choose where to receive backup alerts. “Configured” means settings are saved; delivery has not been verified.'}</p></div>
      <div className="apex-settings-list">
        {channels.map(({ id, title, vi: viSubtitle, en: enSubtitle, icon: Icon }) => <div className={`apex-settings-channel ${expanded === id ? 'is-expanded' : ''}`} key={id}>
          <button type="button" className="apex-settings-row apex-settings-row-button" aria-expanded={expanded === id} aria-controls={`settings-channel-${id}`} onClick={() => setExpanded(expanded === id ? null : id)}>
            <span className="apex-settings-row-icon"><Icon size={19} /></span>
            <span className="apex-settings-row-copy"><strong>{title}</strong><small>{vi ? viSubtitle : enSubtitle}</small></span>
            <span className={`apex-settings-status ${configured[id] ? 'is-configured' : ''}`}>{configured[id] && <Check size={12} />}{configured[id] ? (vi ? 'Đã cấu hình' : 'Configured') : (vi ? 'Chưa cấu hình' : 'Not configured')}</span>
            <ChevronDown className="apex-settings-chevron" size={17} />
          </button>
          {expanded === id && <div className="apex-settings-fields" id={`settings-channel-${id}`}>
            {id === 'telegram' && <div className="apex-settings-field-grid">
              <label className="apex-settings-field"><span>Bot Token</span><input type="password" autoComplete="new-password" value={telegramToken} onChange={(event) => setTelegramToken(event.target.value)} placeholder={secretPlaceholder(configured.telegram, '123456789:ABCdef...')} /><small>{vi ? 'Lấy từ BotFather; để trống để giữ token hiện tại.' : 'Get this from BotFather; leave blank to keep the current token.'}</small></label>
              <label className="apex-settings-field"><span>Chat ID</span><input type="text" value={telegramChat} onChange={(event) => setTelegramChat(event.target.value)} placeholder="123456789" /><small>{vi ? 'ID người dùng, nhóm hoặc kênh nhận cảnh báo.' : 'User, group, or channel receiving alerts.'}</small></label>
            </div>}
            {id === 'discord' && <div className="apex-settings-field-grid apex-settings-field-grid--single"><label className="apex-settings-field"><span>Webhook URL</span><input type="password" autoComplete="new-password" value={discordWebhook} onChange={(event) => setDiscordWebhook(event.target.value)} placeholder={secretPlaceholder(configured.discord, 'https://discord.com/api/webhooks/...')} /><small>{vi ? 'Webhook của kênh Discord muốn nhận thông báo.' : 'Webhook for the Discord channel receiving notifications.'}</small></label></div>}
            {id === 'email' && <div className="apex-settings-field-grid">
              <label className="apex-settings-field"><span>{vi ? 'Email gửi' : 'Sender email'}</span><input type="email" value={smtpEmail} onChange={(event) => setSmtpEmail(event.target.value)} placeholder="sender@gmail.com" /></label>
              <label className="apex-settings-field"><span>{vi ? 'Mật khẩu ứng dụng' : 'App password'}</span><input type="password" autoComplete="new-password" value={smtpPassword} onChange={(event) => setSmtpPassword(event.target.value)} placeholder={secretPlaceholder(configured.email, 'xxxx xxxx xxxx xxxx')} /></label>
              <label className="apex-settings-field apex-settings-field--wide"><span>{vi ? 'Email nhận cảnh báo' : 'Recipient email'}</span><input type="email" value={targetEmail} onChange={(event) => setTargetEmail(event.target.value)} placeholder="admin@example.com" /></label>
            </div>}
          </div>}
        </div>)}
      </div>
      {saveFooter}
    </section>}

    {section === 'monitoring' && <section className="apex-settings-panel" role="tabpanel" id="settings-panel-monitoring" aria-labelledby="settings-tab-monitoring">
      <div className="apex-settings-panel-heading"><h2>{vi ? 'Quy tắc giám sát' : 'Monitoring rule'}</h2><p>{vi ? 'Điều chỉnh ngưỡng để phát hiện thiếu bản sao lưu trên Google Drive.' : 'Set the threshold used to detect missing Drive backups.'}</p></div>
      <div className="apex-settings-list">
        <div className="apex-settings-row"><span className="apex-settings-row-icon"><Cloud size={19} /></span><span className="apex-settings-row-copy"><strong>{vi ? 'Số thư mục backup tối thiểu' : 'Minimum backup folders'}</strong><small>{vi ? 'Cảnh báo nếu số thư mục trong gdrive:Backup ít hơn ngưỡng này.' : 'Alert when folders in gdrive:Backup fall below this threshold.'}</small></span><input className="apex-settings-number" type="number" min={1} max={365} value={threshold} aria-label={vi ? 'Số thư mục backup tối thiểu' : 'Minimum backup folders'} onChange={(event) => setThreshold(Number(event.target.value))} /></div>
        <div className="apex-settings-row"><span className="apex-settings-row-icon"><ShieldCheck size={19} /></span><span className="apex-settings-row-copy"><strong>{vi ? 'Phạm vi giám sát đã cấu hình' : 'Configured monitoring scope'}</strong><small>{vi ? 'Theo dõi xóa tệp trên máy chủ và đếm thư mục Drive theo lịch. Chưa kiểm tra checksum hoặc khả năng khôi phục tệp.' : 'Watch server file deletion and count Drive folders on a schedule. Checksums and restorability are not verified.'}</small></span><span className="apex-settings-source">Server + Google Drive</span></div>
      </div>
      {saveFooter}
    </section>}

    {section === 'appearance' && <section className="apex-settings-panel apex-settings-panel--appearance" role="tabpanel" id="settings-panel-appearance" aria-labelledby="settings-tab-appearance">
      <div className="apex-settings-list">
        <div className="apex-settings-row"><span className="apex-settings-row-icon"><Languages size={19} /></span><span className="apex-settings-row-copy"><strong>{vi ? 'Ngôn ngữ' : 'Language'}</strong><small>{vi ? 'Ngôn ngữ hiển thị trên dashboard' : 'Dashboard display language'}</small></span><div className="apex-settings-segmented" role="group" aria-label={vi ? 'Ngôn ngữ' : 'Language'}><button type="button" className={lang === 'vi' ? 'is-selected' : ''} aria-pressed={lang === 'vi'} onClick={() => { if (lang !== 'vi') onToggleLang() }}>Tiếng Việt</button><button type="button" className={lang === 'en' ? 'is-selected' : ''} aria-pressed={lang === 'en'} onClick={() => { if (lang !== 'en') onToggleLang() }}>English</button></div></div>
        <div className="apex-settings-row"><span className="apex-settings-row-icon">{isDark ? <Moon size={19} /> : <Sun size={19} />}</span><span className="apex-settings-row-copy"><strong>{vi ? 'Chủ đề' : 'Theme'}</strong><small>{vi ? 'Chuyển giữa giao diện sáng và tối' : 'Switch between light and dark appearance'}</small></span><div className="apex-settings-segmented" role="group" aria-label={vi ? 'Chủ đề' : 'Theme'}><button type="button" className={!isDark ? 'is-selected' : ''} aria-pressed={!isDark} onClick={() => { if (isDark) onToggleDark() }}><Sun size={14} />{vi ? 'Sáng' : 'Light'}</button><button type="button" className={isDark ? 'is-selected' : ''} aria-pressed={isDark} onClick={() => { if (!isDark) onToggleDark() }}><Moon size={14} />{vi ? 'Tối' : 'Dark'}</button></div></div>
      </div>
    </section>}
  </div>
}
