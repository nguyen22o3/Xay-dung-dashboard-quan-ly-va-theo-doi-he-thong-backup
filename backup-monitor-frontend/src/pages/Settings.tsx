import { useEffect, useState } from 'react'
import { CheckCircle2, ChevronDown, CircleAlert, Cloud, Hash, Languages, Mail, MessageCircle, Moon, Sun } from 'lucide-react'
import type { LucideIcon } from 'lucide-react'
import { apiErrorMessage, fetchAlertSettings, saveAlertSettings } from '../api'
import type { Lang } from '../language'

type Section = 'alerts' | 'monitoring' | 'appearance'
type ChannelId = 'telegram' | 'discord' | 'email'
type Props = { isDark: boolean; onToggleDark: () => void; lang: Lang; onToggleLang: () => void }
type Channel = { id: ChannelId; title: string; vi: string; en: string; icon: LucideIcon }

const channels: Channel[] = [
  { id: 'telegram', title: 'Telegram', vi: 'Nhận thông báo qua Telegram', en: 'Receive alerts through a Telegram', icon: MessageCircle },
  { id: 'discord', title: 'Discord', vi: 'Nhận thông báo qua kênh trên Discord', en: 'Deliver notifications to a channel through a webhook', icon: Hash },
  { id: 'email', title: 'Email', vi: 'Nhận thông báo qua Gmail SMTP', en: 'Send alerts through Gmail SMTP', icon: Mail },
]

const tabs: { id: Section; vi: string; en: string }[] = [
  { id: 'alerts', vi: 'Cảnh báo', en: 'Alerts' },
  { id: 'monitoring', vi: 'Giám sát', en: 'Monitoring' },
  { id: 'appearance', vi: 'Giao diện', en: 'Appearance' },
]

export default function Settings({ isDark, onToggleDark, lang, onToggleLang }: Props) {
  const vi = lang === 'vi'
  const [section, setSection] = useState<Section>(() => {
    const saved = localStorage.getItem('settings-section')
    return saved === 'alerts' || saved === 'monitoring' || saved === 'appearance' ? saved : 'alerts'
  })
  const [expanded, setExpanded] = useState<Record<ChannelId, boolean>>({ telegram: false, discord: false, email: false })
  const [telegramToken, setTelegramToken] = useState('')
  const [telegramChat, setTelegramChat] = useState('')
  const [discordWebhook, setDiscordWebhook] = useState('')
  const [smtpEmail, setSmtpEmail] = useState('')
  const [smtpPassword, setSmtpPassword] = useState('')
  const [targetEmail, setTargetEmail] = useState('')
  const [threshold, setThreshold] = useState<number | ''>(14)
  const [configured, setConfigured] = useState<Record<ChannelId, boolean>>({ telegram: false, discord: false, email: false })
  const [enabled, setEnabled] = useState<Record<ChannelId, boolean>>({ telegram: false, discord: false, email: false })
  const [loading, setLoading] = useState(true)
  const [saving, setSaving] = useState(false)
  const [savingChannel, setSavingChannel] = useState<ChannelId | null>(null)
  const [toggling, setToggling] = useState(false)
  const [message, setMessage] = useState('')
  const [messageType, setMessageType] = useState<'success' | 'error' | null>(null)
  const [lastSavedChannel, setLastSavedChannel] = useState<ChannelId | null>(null)

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
        setEnabled({ telegram: settings.telegramEnabled ?? settings.telegramConfigured, discord: settings.discordEnabled ?? settings.discordConfigured, email: settings.smtpEnabled ?? settings.smtpConfigured })
      })
      .catch((error: unknown) => {
        if (!alive) return
        setMessageType('error')
        setMessage(apiErrorMessage(error, 'Không thể tải cấu hình cảnh báo / Could not load alert settings.'))
      })
      .finally(() => { if (alive) setLoading(false) })
    return () => { alive = false }
  }, [])

  const save = async (nextEnabled = enabled, source: 'form' | 'toggle' = 'form', channel?: ChannelId) => {
    if (source === 'form') setSaving(true)
    else setToggling(true)
    if (source === 'form') setSavingChannel(channel || null)
    setMessage('')
    setMessageType(null)
    setLastSavedChannel(null)
    try {
      const settings = await saveAlertSettings({ telegramToken, telegramChat, discordWebhook, smtpEmail, smtpPassword, targetEmail, threshold: Number(threshold), telegramEnabled: nextEnabled.telegram, discordEnabled: nextEnabled.discord, smtpEnabled: nextEnabled.email })
      setTelegramToken('')
      setDiscordWebhook('')
      setSmtpPassword('')
      setConfigured({ telegram: settings.telegramConfigured, discord: settings.discordConfigured, email: settings.smtpConfigured })
      // Keep the requested state when an older backend omits the new fields.
      setEnabled({ telegram: settings.telegramEnabled ?? nextEnabled.telegram, discord: settings.discordEnabled ?? nextEnabled.discord, email: settings.smtpEnabled ?? nextEnabled.email })
      setMessageType('success')
      setMessage(vi ? 'Đã lưu' : 'Saved')
      setLastSavedChannel(channel || null)
    } catch (error: unknown) {
      setMessageType('error')
      setMessage(apiErrorMessage(error, vi ? 'Không thể lưu cấu hình.' : 'Could not save settings.'))
    } finally {
      if (source === 'form') setSaving(false)
      else setToggling(false)
      if (source === 'form') setSavingChannel(null)
    }
  }

  const saveFooter = <div className="apex-settings-footer">
    {message && <div className={`apex-settings-feedback ${messageType === 'error' ? 'is-error' : ''}`} role={messageType === 'error' ? 'alert' : 'status'}>
      {messageType === 'error' ? <CircleAlert size={16} /> : <CheckCircle2 size={16} />}
      <span>{message}</span>
    </div>}
  </div>

  const toggleExpanded = (id: ChannelId) => setExpanded((current) => ({ ...current, [id]: !current[id] }))
  const selectSection = (next: Section) => {
    setSection(next)
    localStorage.setItem('settings-section', next)
  }

  return <div className="apex-settings-page animate-fade-in">
    <header className="apex-settings-heading"><h1>{vi ? 'Cài đặt' : 'Settings'}</h1><p>{vi ? 'Quản lý cảnh báo, giám sát và giao diện.' : 'Manage alerts, monitoring, and appearance.'}</p></header>
    <div className="apex-settings-tabs" role="tablist" aria-label={vi ? 'Nhóm cài đặt' : 'Settings sections'}>
      {tabs.map((tab) => <button key={tab.id} id={`settings-tab-${tab.id}`} role="tab" type="button" aria-controls={`settings-panel-${tab.id}`} aria-selected={section === tab.id} tabIndex={section === tab.id ? 0 : -1} className={section === tab.id ? 'is-active' : ''} onClick={() => selectSection(tab.id)} onKeyDown={(event) => {
        if (event.key !== 'ArrowLeft' && event.key !== 'ArrowRight') return
        event.preventDefault()
        const index = tabs.findIndex((item) => item.id === section)
        const next = tabs[(index + (event.key === 'ArrowRight' ? 1 : tabs.length - 1)) % tabs.length]
        selectSection(next.id)
        document.getElementById(`settings-tab-${next.id}`)?.focus()
      }}>{vi ? tab.vi : tab.en}</button>)}
    </div>

    {section === 'alerts' && <section className="apex-settings-panel" role="tabpanel" id="settings-panel-alerts" aria-labelledby="settings-tab-alerts">
      <div className="apex-settings-panel-heading"><h2>{vi ? 'Kênh cảnh báo' : 'Alert channels'}</h2><p>{vi ? 'Chọn nơi nhận thông báo khi hệ thống phát hiện sự cố sao lưu.' : 'Choose where to receive backup alerts.'}</p></div>
      <div className="apex-settings-list">
        {channels.map(({ id, title, vi: viSubtitle, en: enSubtitle, icon: Icon }) => <div className={`apex-settings-channel ${expanded[id] ? 'is-expanded' : ''}`} key={id}>
          <button type="button" className="apex-settings-row apex-settings-row-button" aria-expanded={expanded[id]} aria-controls={`settings-channel-${id}`} onClick={() => toggleExpanded(id)}>
            <span className="apex-settings-row-icon"><Icon size={19} /></span>
            <span className="apex-settings-row-copy"><strong>{title}</strong><small>{vi ? viSubtitle : enSubtitle}</small></span>
            <button type="button" className={`apex-settings-switch ${enabled[id] ? 'is-on' : ''}`} role="switch" aria-checked={enabled[id]} aria-label={`${title} ${vi ? 'bật cảnh báo' : 'enable alerts'}`} disabled={!configured[id] || loading || saving || toggling} onClick={(event) => { event.stopPropagation(); const next = { ...enabled, [id]: !enabled[id] }; setEnabled(next); void save(next, 'toggle', id) }}><span className="apex-settings-switch-thumb" /></button>
            <ChevronDown className="apex-settings-chevron" size={17} />
          </button>
          {expanded[id] && <div className="apex-settings-fields" id={`settings-channel-${id}`}>
            {id === 'telegram' && <div className="apex-settings-field-grid">
              <label className="apex-settings-field"><span>Bot Token</span><input type="password" autoComplete="new-password" value={telegramToken} onChange={(event) => setTelegramToken(event.target.value)} /></label>
              <label className="apex-settings-field"><span>Chat ID</span><input type="text" value={telegramChat} onChange={(event) => setTelegramChat(event.target.value)} /></label>
            </div>}
            {id === 'discord' && <div className="apex-settings-field-grid apex-settings-field-grid--single"><label className="apex-settings-field"><span>Webhook URL</span><input type="password" autoComplete="new-password" value={discordWebhook} onChange={(event) => setDiscordWebhook(event.target.value)} /></label></div>}
            {id === 'email' && <div className="apex-settings-field-grid">
              <label className="apex-settings-field"><span>{vi ? 'Email gửi' : 'Sender email'}</span><input type="email" value={smtpEmail} onChange={(event) => setSmtpEmail(event.target.value)} /></label>
              <label className="apex-settings-field"><span>{vi ? 'Mật khẩu ứng dụng' : 'App password'}</span><input type="password" autoComplete="new-password" value={smtpPassword} onChange={(event) => setSmtpPassword(event.target.value)} /></label>
              <label className="apex-settings-field apex-settings-field--wide"><span>{vi ? 'Email nhận cảnh báo' : 'Recipient email'}</span><input type="email" value={targetEmail} onChange={(event) => setTargetEmail(event.target.value)} /></label>
            </div>}
            <div className="apex-settings-channel-actions"><span className={`apex-settings-channel-saved ${lastSavedChannel === id && messageType === 'success' ? '' : 'is-placeholder'}`}>{vi ? 'Đã lưu' : 'Saved'}</span><button type="button" className="apex-settings-save" onClick={() => void save(enabled, 'form', id)} disabled={loading || toggling || (saving && savingChannel === id)}>{saving && savingChannel === id ? (vi ? 'Đang lưu...' : 'Saving...') : (vi ? 'Lưu' : 'Save')}</button></div>
          </div>}
        </div>)}
      </div>
      {messageType === 'error' && saveFooter}
    </section>}

    {section === 'monitoring' && <section className="apex-settings-panel" role="tabpanel" id="settings-panel-monitoring" aria-labelledby="settings-tab-monitoring">
      <div className="apex-settings-list">
        <div className="apex-settings-row"><span className="apex-settings-row-icon"><Cloud size={19} /></span><span className="apex-settings-row-copy"><strong>{vi ? 'Giám sát bản sao lưu Google Drive — đặt số thư mục backup tối thiểu' : 'Google Drive backup monitoring — minimum backup folder count'}</strong><small>{vi ? 'Nếu Drive có ít hơn số này, hệ thống sẽ cảnh báo.' : 'The system warns you when Drive has fewer than this number.'}</small></span><input className="apex-settings-number" type="text" inputMode="numeric" pattern="[0-9]*" value={threshold} aria-label={vi ? 'Số thư mục backup tối thiểu' : 'Minimum backup folder count'} onChange={(event) => setThreshold(event.target.value === '' ? '' : Number(event.target.value.replace(/\D/g, '')))} /></div>
      </div>
      <div className="apex-settings-channel-actions"><span /> <button type="button" className="apex-settings-save" onClick={() => void save()} disabled={loading || saving || toggling}>{saving ? (vi ? 'Đang lưu...' : 'Saving...') : (vi ? 'Lưu' : 'Save')}</button></div>
      {messageType === 'error' && saveFooter}
    </section>}

    {section === 'appearance' && <section className="apex-settings-panel apex-settings-panel--appearance" role="tabpanel" id="settings-panel-appearance" aria-labelledby="settings-tab-appearance">
      <div className="apex-settings-list">
        <div className="apex-settings-row"><span className="apex-settings-row-icon"><Languages size={19} /></span><span className="apex-settings-row-copy"><strong>{vi ? 'Ngôn ngữ' : 'Language'}</strong><small>{vi ? 'Ngôn ngữ hiển thị trên dashboard' : 'Dashboard display language'}</small></span><div className="apex-settings-segmented" role="group" aria-label={vi ? 'Ngôn ngữ' : 'Language'}><button type="button" className={lang === 'vi' ? 'is-selected' : ''} aria-pressed={lang === 'vi'} onClick={() => { if (lang !== 'vi') onToggleLang() }}>Tiếng Việt</button><button type="button" className={lang === 'en' ? 'is-selected' : ''} aria-pressed={lang === 'en'} onClick={() => { if (lang !== 'en') onToggleLang() }}>English</button></div></div>
        <div className="apex-settings-row"><span className="apex-settings-row-icon">{isDark ? <Moon size={19} /> : <Sun size={19} />}</span><span className="apex-settings-row-copy"><strong>{vi ? 'Chủ đề' : 'Theme'}</strong><small>{vi ? 'Chuyển giữa giao diện sáng và tối' : 'Switch between light and dark appearance'}</small></span><div className="apex-settings-segmented" role="group" aria-label={vi ? 'Chủ đề' : 'Theme'}><button type="button" className={!isDark ? 'is-selected' : ''} aria-pressed={!isDark} onClick={() => { if (isDark) onToggleDark() }}><Sun size={14} />{vi ? 'Sáng' : 'Light'}</button><button type="button" className={isDark ? 'is-selected' : ''} aria-pressed={isDark} onClick={() => { if (!isDark) onToggleDark() }}><Moon size={14} />{vi ? 'Tối' : 'Dark'}</button></div></div>
      </div>
    </section>}
  </div>
}
