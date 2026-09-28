import { useState, useEffect } from 'react'
import { Sun, Moon, Languages, Bell, Save,  } from 'lucide-react'
import type { Lang } from '../language'
import { makeTheme } from '../theme'
import { tr } from '../language'
import { client } from '../api'

export default function Settings({
  isDark,
  onToggleDark,
  lang,
  onToggleLang
}: {
  isDark: boolean
  onToggleDark: () => void
  lang: Lang
  onToggleLang: () => void
}) {
  const t = makeTheme(isDark)
  const language = lang

  const [activeTab, setActiveTab] = useState<'telegram' | 'discord' | 'email'>('telegram')

  const [telegramToken, setTelegramToken] = useState('')
  const [telegramChat, setTelegramChat] = useState('')
  
  const [discordWebhook, setDiscordWebhook] = useState('')

  const [smtpEmail, setSmtpEmail] = useState('')
  const [smtpPassword, setSmtpPassword] = useState('')
  const [targetEmail, setTargetEmail] = useState('')

  const [threshold, setThreshold] = useState(14)
  const [saving, setSaving] = useState(false)
  const [message, setMessage] = useState('')

  useEffect(() => {
    client.get('/api/alert-settings').then((res: any) => {
      if (res.data) {
        setTelegramToken(res.data.telegramToken || '')
        setTelegramChat(res.data.telegramChat || '')
        setDiscordWebhook(res.data.discordWebhook || '')
        setSmtpEmail(res.data.smtpEmail || '')
        setSmtpPassword(res.data.smtpPassword || '')
        setTargetEmail(res.data.targetEmail || '')
        setThreshold(res.data.threshold || 14)
      }
    }).catch((err: any) => console.error(err))
  }, [])

  const handleSaveAlerts = async () => {
    setSaving(true)
    setMessage('')
    try {
      await client.post('/api/alert-settings', {
        telegramToken,
        telegramChat,
        discordWebhook,
        smtpEmail,
        smtpPassword,
        targetEmail,
        threshold: Number(threshold)
      })
      setMessage(lang === 'vi' ? 'Đã lưu cấu hình và khởi tạo Cảnh báo thành công!' : 'Saved and initialized alert successfully!')
    } catch (error) {
      setMessage(lang === 'vi' ? 'Có lỗi xảy ra khi lưu' : 'Failed to save settings')
    }
    setSaving(false)
  }

  const inputStyle = {
    width: '100%',
    boxSizing: 'border-box' as const,
    padding: '8px 12px',
    borderRadius: '4px',
    border: `1px solid ${t.gridLine}`,
    background: t.cardBg,
    color: t.titleColor,
    fontSize: '14px',
    marginBottom: '15px'
  }

  const labelStyle = {
    display: 'block',
    fontSize: '13px',
    color: t.textSecondary,
    marginBottom: '5px'
  }



  return (
    <div className="animate-fade-in" style={{ 
      padding: '40px 20px', 
      height: '100%', 
      display: 'flex', 
      flexDirection: 'column', 
      alignItems: 'center', 
      justifyContent: 'flex-start',
      overflowY: 'auto'
    }}>
      
      <div style={{ width: '100%', maxWidth: '600px' }}>
        <h2 style={{ margin: '0 0 20px 0', fontSize: '22px', fontWeight: 'normal', color: t.titleColor }}>
          {tr(lang, 'settings')}
        </h2>

        <div style={{ display: 'grid', gap: '20px', paddingBottom: '40px' }}>

          {/* ALERTS SECTION */}
          <div style={{ backgroundColor: t.cardBg, border: `1px solid ${t.cardBorder}`, borderRadius: '4px', padding: '20px', boxShadow: isDark ? 'none' : '0 1px 3px rgba(0,0,0,0.1)' }}>
            <h3 style={{ margin: '0 0 20px 0', fontSize: '18px', color: t.titleColor, display: 'flex', alignItems: 'center', gap: '8px' }}>
              <Bell size={20} /> {lang === 'vi' ? 'Cảnh báo tính toàn vẹn dữ liệu' : 'Data Integrity Alerts'}
            </h3>
            
            {/* Dropdown chọn nền tảng */}
            <div style={{ marginBottom: '20px' }}>
              <label style={labelStyle}>{lang === 'vi' ? 'Nền tảng nhận thông báo' : 'Notification Platform'}</label>
              <select 
                value={activeTab} 
                onChange={e => setActiveTab(e.target.value as any)}
                style={{
                  ...inputStyle,
                  cursor: 'pointer',
                  appearance: 'auto',
                  border: `1px solid ${t.gridLine}`,
                  outlineColor: '#4caf50' // Green outline on focus similar to aaPanel
                }}
              >
                <option value="telegram">Telegram Bot</option>
                <option value="discord">Discord Webhook</option>
                <option value="email">Gmail / Email (SMTP)</option>
              </select>
            </div>

            <div>
              {/* Telegram Form */}
              {activeTab === 'telegram' && (
                <div className="animate-fade-in">
                  <p style={{ color: '#0088cc', fontSize: '13px', marginTop: 0, marginBottom: '15px' }}>
                    {lang === 'vi' ? 'Thiết lập nhận thông báo qua Telegram Bot.' : 'Set up notifications via Telegram Bot.'}
                  </p>
                  <label style={labelStyle}>Bot Token</label>
                  <input 
                    type="text" 
                    style={inputStyle}
                    placeholder="123456789:ABCdefGHIjklMNOpqr..." 
                    value={telegramToken}
                    onChange={e => setTelegramToken(e.target.value)}
                  />
                  <label style={labelStyle}>Chat ID</label>
                  <input 
                    type="text" 
                    style={{...inputStyle, marginBottom: 0}}
                    placeholder="123456789" 
                    value={telegramChat}
                    onChange={e => setTelegramChat(e.target.value)}
                  />
                </div>
              )}

              {/* Discord Form */}
              {activeTab === 'discord' && (
                <div className="animate-fade-in">
                  <p style={{ color: '#5865F2', fontSize: '13px', marginTop: 0, marginBottom: '15px' }}>
                    {lang === 'vi' ? 'Thiết lập nhận thông báo qua Discord Webhook.' : 'Set up notifications via Discord Webhook.'}
                  </p>
                  <label style={labelStyle}>Webhook URL</label>
                  <input 
                    type="text" 
                    style={{...inputStyle, marginBottom: 0}}
                    placeholder="https://discord.com/api/webhooks/..." 
                    value={discordWebhook}
                    onChange={e => setDiscordWebhook(e.target.value)}
                  />
                </div>
              )}

              {/* Email / Gmail Form */}
              {activeTab === 'email' && (
                <div className="animate-fade-in">
                  <p style={{ color: '#ea4335', fontSize: '13px', marginTop: 0, marginBottom: '15px' }}>
                    {lang === 'vi' ? 'Thiết lập nhận thông báo qua Email sử dụng SMTP.' : 'Set up notifications via Email using SMTP.'}
                  </p>
                  <label style={labelStyle}>Email gửi (Người gửi)</label>
                  <input 
                    type="email" 
                    style={inputStyle}
                    placeholder="sender@gmail.com" 
                    value={smtpEmail}
                    onChange={e => setSmtpEmail(e.target.value)}
                  />
                  <label style={labelStyle}>Mật khẩu ứng dụng (App Password)</label>
                  <input 
                    type="password" 
                    style={inputStyle}
                    placeholder="xxxx xxxx xxxx xxxx" 
                    value={smtpPassword}
                    onChange={e => setSmtpPassword(e.target.value)}
                  />
                  <label style={labelStyle}>Email nhận cảnh báo</label>
                  <input 
                    type="email" 
                    style={{...inputStyle, marginBottom: 0}}
                    placeholder="admin@example.com" 
                    value={targetEmail}
                    onChange={e => setTargetEmail(e.target.value)}
                  />
                </div>
              )}

              {/* Threshold (Global) */}
              <div style={{ marginTop: '20px', paddingTop: '20px', borderTop: `1px dashed ${t.gridLine}` }}>
                <label style={{...labelStyle, fontWeight: 'bold'}}>{lang === 'vi' ? 'Ngưỡng ngày kiểm tra (Mặc định: 14)' : 'Check Threshold (Default: 14)'}</label>
                <input 
                  type="number" 
                  style={inputStyle}
                  value={threshold}
                  onChange={e => setThreshold(Number(e.target.value))}
                />
              </div>

              <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginTop: '10px' }}>
                <div style={{ color: message.includes('lỗi') || message.includes('Failed') ? '#ef5350' : '#4caf50', fontSize: '13px', maxWidth: '60%' }}>
                  {message}
                </div>
                <button
                  onClick={handleSaveAlerts}
                  disabled={saving}
                  style={{
                    background: '#4caf50',
                    border: 'none',
                    color: 'white',
                    borderRadius: '4px',
                    padding: '8px 24px',
                    cursor: saving ? 'not-allowed' : 'pointer',
                    fontSize: '13px',
                    fontWeight: 'bold',
                    display: 'flex',
                    alignItems: 'center',
                    gap: '8px',
                    opacity: saving ? 0.7 : 1,
                    transition: 'background 0.2s'
                  }}
                >
                  <Save size={16} /> {saving ? (lang === 'vi' ? 'ĐANG LƯU...' : 'SAVING...') : (lang === 'vi' ? 'LƯU CẤU HÌNH' : 'SAVE')}
                </button>
              </div>
            </div>
          </div>

          <div style={{ backgroundColor: t.cardBg, border: `1px solid ${t.cardBorder}`, borderRadius: '4px', padding: '20px', boxShadow: isDark ? 'none' : '0 1px 3px rgba(0,0,0,0.1)' }}>
            <h3 style={{ margin: '0 0 15px 0', fontSize: '16px', color: t.titleColor, display: 'flex', alignItems: 'center', gap: '8px' }}>
              <Languages size={18} /> {language === 'vi' ? 'Ngôn ngữ' : 'Language'}
            </h3>
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
              <div style={{ color: t.textSecondary, fontSize: '13px' }}>
                {language === 'vi' ? 'Chuyển đổi giao diện sang Tiếng Việt / Tiếng Anh' : 'Switch interface to Vietnamese / English'}
              </div>
              <button
                onClick={onToggleLang}
                style={{
                  background: '#2196f3',
                  border: 'none',
                  color: 'white',
                  borderRadius: '4px',
                  padding: '8px 16px',
                  cursor: 'pointer',
                  fontSize: '13px',
                  fontWeight: 'bold',
                  display: 'flex',
                  alignItems: 'center',
                  gap: '8px',
                  transition: 'background 0.2s'
                }}
              >
                <Languages size={16} /> {language === 'vi' ? 'TIẾNG VIỆT' : 'ENGLISH'}
              </button>
            </div>
          </div>

          <div style={{ backgroundColor: t.cardBg, border: `1px solid ${t.cardBorder}`, borderRadius: '4px', padding: '20px', boxShadow: isDark ? 'none' : '0 1px 3px rgba(0,0,0,0.1)' }}>
            <h3 style={{ margin: '0 0 15px 0', fontSize: '16px', color: t.titleColor, display: 'flex', alignItems: 'center', gap: '8px' }}>
              {isDark ? <Moon size={18} /> : <Sun size={18} />} {language === 'vi' ? 'Giao diện' : 'Appearance'}
            </h3>
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
              <div style={{ color: t.textSecondary, fontSize: '13px' }}>
                {language === 'vi' ? 'Chuyển đổi giữa chế độ Sáng / Tối' : 'Switch between Light / Dark mode'}
              </div>
              <button
                onClick={onToggleDark}
                style={{
                  background: isDark ? '#333' : '#e0e0e0',
                  border: 'none',
                  color: isDark ? 'white' : '#333',
                  borderRadius: '4px',
                  padding: '8px 16px',
                  cursor: 'pointer',
                  fontSize: '13px',
                  fontWeight: 'bold',
                  display: 'flex',
                  alignItems: 'center',
                  gap: '8px',
                  transition: 'background 0.2s'
                }}
              >
                {isDark ? <Moon size={16} /> : <Sun size={16} />} {isDark ? (language === 'vi' ? 'TỐI' : 'DARK') : (language === 'vi' ? 'SÁNG' : 'LIGHT')}
              </button>
            </div>
          </div>
        </div>
      </div>
    </div>
  )
}
