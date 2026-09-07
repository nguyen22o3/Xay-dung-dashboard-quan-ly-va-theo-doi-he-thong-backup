import React from 'react';
import { formatSize } from '../utils/formatters';

export default function Settings({
  config, setConfigField, saveConfig, testNotify,
  darkMode, toggleDarkMode,
  schedules, scheduleForm, handleScheduleForm, saveSchedule, editSchedule, deleteSchedule, setScheduleForm,
  backendOnline, stats, handleClearAll
}) {
  return (
    <div className="settings-page">
      <section className="settings-section">
        <div className="settings-header">
          <h2>Cấu hình thông báo</h2>
          <p>Chọn nền tảng nhận thông báo khi backup thành công hoặc thất bại</p>
        </div>
        <form className="settings-card" onSubmit={saveConfig}>
          {/* Discord */}
          <div className="settings-platform">
            <div className="settings-row settings-row--between">
              <div className="settings-brand">
                <div className="settings-brand-icon settings-brand-icon--discord">
                  <svg width="18" height="18" viewBox="0 0 24 24" fill="currentColor">
                    <path d="M20.317 4.37a19.79 19.79 0 0 0-4.885-1.515.074.074 0 0 0-.079.037c-.21.375-.444.864-.608 1.25a18.27 18.27 0 0 0-5.487 0 12.64 12.64 0 0 0-.617-1.25.077.077 0 0 0-.079-.037A19.736 19.736 0 0 0 3.677 4.37a.07.07 0 0 0-.032.027C.533 9.046-.32 13.58.099 18.057a.082.082 0 0 0 .031.057 19.9 19.9 0 0 0 5.993 3.03.078.078 0 0 0 .084-.028c.462-.63.874-1.295 1.226-1.994a.076.076 0 0 0-.041-.106 13.107 13.107 0 0 1-1.872-.892.077.077 0 0 1-.008-.128 10.2 10.2 0 0 0 .372-.292.074.074 0 0 1 .077-.01c3.928 1.793 8.18 1.793 12.062 0a.074.074 0 0 1 .078.01c.12.098.246.198.373.292a.077.077 0 0 1-.006.127 12.299 12.299 0 0 1-1.873.892.077.077 0 0 0-.041.107c.36.698.772 1.362 1.225 1.993a.076.076 0 0 0 .084.028 19.839 19.839 0 0 0 6.002-3.03.077.077 0 0 0 .032-.054c.5-5.177-.838-9.674-3.549-13.66a.061.061 0 0 0-.031-.03zM8.02 15.33c-1.183 0-2.157-1.085-2.157-2.419 0-1.333.956-2.419 2.157-2.419 1.21 0 2.176 1.096 2.157 2.42 0 1.333-.956 2.418-2.157 2.418zm7.975 0c-1.183 0-2.157-1.085-2.157-2.419 0-1.333.955-2.419 2.157-2.419 1.21 0 2.176 1.096 2.157 2.42 0 1.333-.946 2.418-2.157 2.418z" />
                  </svg>
                </div>
                <div>
                  <div className="settings-label">Discord</div>
                  <p className="settings-hint" style={{ margin: '2px 0 0' }}>Gửi qua webhook kênh Discord</p>
                </div>
              </div>
              <button type="button" className={`switch ${config.discord_enabled ? 'switch--on' : ''}`} role="switch" aria-checked={config.discord_enabled} onClick={() => setConfigField('discord_enabled', !config.discord_enabled)}>
                <span className="switch-thumb" />
              </button>
            </div>
            {config.discord_enabled && (
              <div className="settings-row">
                <label className="settings-label">Webhook URL</label>
                <input className="settings-input" type="password" placeholder="https://discord.com/api/webhooks/..." value={config.discord_webhook_url === '••••••••' ? '' : config.discord_webhook_url} onChange={e => setConfigField('discord_webhook_url', e.target.value)} />
              </div>
            )}
          </div>

          <div className="settings-divider" />

          {/* Gmail */}
          <div className="settings-platform">
            <div className="settings-row settings-row--between">
              <div className="settings-brand">
                <div className="settings-brand-icon settings-brand-icon--gmail">
                  <svg width="18" height="18" viewBox="0 0 24 24" fill="currentColor">
                    <path d="M20 4H4c-1.1 0-1.99.9-1.99 2L2 18c0 1.1.9 2 2 2h16c1.1 0 2-.9 2-2V6c0-1.1-.9-2-2-2zm0 4l-8 5-8-5V6l8 5 8-5v2z" />
                  </svg>
                </div>
                <div>
                  <div className="settings-label">Gmail</div>
                  <p className="settings-hint" style={{ margin: '2px 0 0' }}>Gửi qua SMTP Gmail</p>
                </div>
              </div>
              <button type="button" className={`switch ${config.gmail_enabled ? 'switch--on' : ''}`} role="switch" aria-checked={config.gmail_enabled} onClick={() => setConfigField('gmail_enabled', !config.gmail_enabled)}>
                <span className="switch-thumb" />
              </button>
            </div>
            {config.gmail_enabled && (
              <>
                <div className="settings-row">
                  <label className="settings-label">Email gửi (Gmail)</label>
                  <input className="settings-input" type="text" placeholder="you@gmail.com" value={config.gmail_email} onChange={e => setConfigField('gmail_email', e.target.value)} />
                </div>
                <div className="settings-row">
                  <label className="settings-label">Mật khẩu ứng dụng (App Password)</label>
                  <input className="settings-input" type="password" placeholder={config.gmail_app_password === '••••••••' ? '•••••••• (đã lưu, nhập để thay đổi)' : '16 ký tự app password'} value={config.gmail_app_password === '••••••••' ? '' : config.gmail_app_password} onChange={e => setConfigField('gmail_app_password', e.target.value)} />
                  <p className="settings-hint">Bật 2FA và tạo App Password trong tài khoản Google</p>
                </div>
                <div className="settings-row">
                  <label className="settings-label">Email nhận thông báo</label>
                  <input className="settings-input" type="text" placeholder="recipient@example.com" value={config.gmail_to} onChange={e => setConfigField('gmail_to', e.target.value)} />
                </div>
                <div className="settings-row settings-row--split">
                  <div className="settings-row">
                    <label className="settings-label">SMTP Host</label>
                    <input className="settings-input" type="text" value={config.gmail_smtp_host} onChange={e => setConfigField('gmail_smtp_host', e.target.value)} />
                  </div>
                  <div className="settings-row">
                    <label className="settings-label">SMTP Port</label>
                    <input className="settings-input" type="text" value={config.gmail_smtp_port} onChange={e => setConfigField('gmail_smtp_port', e.target.value)} />
                  </div>
                </div>
              </>
            )}
          </div>

          <div className="settings-divider" />

          {/* Telegram */}
          <div className="settings-platform">
            <div className="settings-row settings-row--between">
              <div className="settings-brand">
                <div className="settings-brand-icon settings-brand-icon--telegram">
                  <svg width="18" height="18" viewBox="0 0 24 24" fill="currentColor">
                    <path d="M11.944 0A12 12 0 0 0 0 12a12 12 0 0 0 12 12 12 12 0 0 0 12-12A12 12 0 0 0 12 0a12 12 0 0 0-.056 0zm4.962 7.224c.1-.002.321.023.465.14a.506.506 0 0 1 .171.325c.016.093.036.306.02.472-.18 1.898-.962 6.502-1.36 8.627-.168.9-.499 1.201-.82 1.23-.696.065-1.225-.46-1.9-.902-1.056-.693-1.653-1.124-2.678-1.8-1.185-.78-.417-1.21.258-1.91.177-.184 3.247-2.977 3.307-3.23.007-.032.014-.15-.056-.212s-.174-.041-.249-.024c-.106.024-1.793 1.14-5.061 3.345-.48.33-.913.49-1.302.48-.428-.008-1.252-.241-1.865-.44-.752-.245-1.349-.374-1.297-.789.027-.216.325-.437.893-.663 3.498-1.524 5.83-2.529 6.998-3.014 3.332-1.386 4.025-1.627 4.476-1.635z" />
                  </svg>
                </div>
                <div>
                  <div className="settings-label">Telegram</div>
                  <p className="settings-hint" style={{ margin: '2px 0 0' }}>Gửi qua Telegram Bot</p>
                </div>
              </div>
              <button type="button" className={`switch ${config.telegram_enabled ? 'switch--on' : ''}`} role="switch" aria-checked={config.telegram_enabled} onClick={() => setConfigField('telegram_enabled', !config.telegram_enabled)}>
                <span className="switch-thumb" />
              </button>
            </div>
            {config.telegram_enabled && (
              <>
                <div className="settings-row">
                  <label className="settings-label">Bot Token</label>
                  <input className="settings-input" type="password" placeholder={config.telegram_bot_token === '••••••••' ? '•••••••• (đã lưu, nhập để thay đổi)' : '123456:ABC-DEF... từ @BotFather'} value={config.telegram_bot_token === '••••••••' ? '' : config.telegram_bot_token} onChange={e => setConfigField('telegram_bot_token', e.target.value)} />
                </div>
                <div className="settings-row">
                  <label className="settings-label">Chat / Channel ID</label>
                  <input className="settings-input" type="text" placeholder="Ví dụ: -1001234567890 hoặc 123456789" value={config.telegram_chat_id} onChange={e => setConfigField('telegram_chat_id', e.target.value)} />
                </div>
              </>
            )}
          </div>

          <div className="settings-divider" />

          <div className="settings-actions">
            <button className="btn-primary" type="submit">Lưu cấu hình</button>
            <button className="btn-secondary" type="button" onClick={testNotify}>Gửi thông báo thử</button>
          </div>
        </form>
      </section>

      <section className="settings-section">
        <div className="settings-header">
          <h2>Cài đặt giao diện</h2>
          <p>Tùy chỉnh hiển thị dashboard</p>
        </div>
        <div className="settings-card">
          <div className="settings-row settings-row--between">
            <div>
              <div className="settings-label">Chế độ tối</div>
            </div>
            <button className={`switch ${darkMode ? 'switch--on' : ''}`} onClick={toggleDarkMode} role="switch" aria-checked={darkMode}>
              <span className="switch-thumb" />
            </button>
          </div>
        </div>
      </section>


      <section className="settings-section">
        <div className="settings-header">
          <h2>Cảnh báo thiếu bản Backup</h2>
          <p>Cấu hình lịch trình để tự động cảnh báo nếu không nhận được bản backup như dự kiến</p>
        </div>
        <div className="settings-card">
          <form className="schedule-form" onSubmit={saveSchedule}>
            <div className="settings-row">
              <label className="settings-label">Tên bản backup</label>
              <input className="settings-input" type="text" value={scheduleForm.source} onChange={e => handleScheduleForm('source', e.target.value)} required />
            </div>
            <div className="settings-row settings-row--split">
              <div className="settings-row">
                <label className="settings-label">Biểu thức Cron</label>
                <input className="settings-input" type="text" placeholder="0 2 * * *" value={scheduleForm.cron_expr} onChange={e => handleScheduleForm('cron_expr', e.target.value)} required />
                <p className="settings-hint">Định dạng 5 trường: phút giờ ngày tháng tuần. VD: <code>0 2 * * *</code> = 2h sáng hằng ngày</p>
              </div>
              <div className="settings-row">
                <label className="settings-label">Độ trễ (phút)</label>
                <input className="settings-input" type="number" min="0" value={scheduleForm.grace_minutes} onChange={e => handleScheduleForm('grace_minutes', Number(e.target.value))} />
              </div>
            </div>
            <div className="settings-row settings-row--between">
              <div>
                <div className="settings-label">Bật theo dõi</div>
                <p className="settings-hint" style={{ margin: '4px 0 0' }}>Bật để backend cảnh báo khi quá hạn</p>
              </div>
              <button type="button" className={`switch ${scheduleForm.enabled ? 'switch--on' : ''}`} role="switch" aria-checked={scheduleForm.enabled} onClick={() => handleScheduleForm('enabled', !scheduleForm.enabled)}>
                <span className="switch-thumb" />
              </button>
            </div>
            <div className="settings-actions">
              <button className="btn-primary" type="submit">{scheduleForm.id ? 'Cập nhật lịch' : 'Thêm lịch'}</button>
              {scheduleForm.id && (
                <button className="btn-secondary" type="button" onClick={() => setScheduleForm({ id: null, source: '', source_key: '', cron_expr: '0 2 * * *', enabled: true, grace_minutes: 30 })}>Hủy</button>
              )}
            </div>
          </form>

          {schedules && schedules.length > 0 ? (
            <div className="schedule-list">
              <table className="schedule-table">
                <thead>
                  <tr>
                    <th>Tên bản backup</th>
                    <th>Cron</th>
                    <th>Độ trễ</th>
                    <th>Trạng thái</th>
                    <th></th>
                  </tr>
                </thead>
                <tbody>
                  {schedules.map(s => (
                    <tr key={s.id}>
                      <td>{s.source}</td>
                      <td><code>{s.cron_expr}</code></td>
                      <td>{s.grace_minutes} phút</td>
                      <td>
                        <span className={`tag ${s.enabled ? 'tag--success' : ''}`}>
                          {s.enabled ? 'Bật' : 'Tắt'}
                        </span>
                      </td>
                      <td>
                        <div className="table-actions">
                          <button className="btn-secondary btn-sm" type="button" onClick={() => editSchedule(s)}>Sửa</button>
                          <button className="btn-danger btn-sm" type="button" onClick={() => deleteSchedule(s.id)}>Xóa</button>
                        </div>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          ) : (
            <p className="settings-hint" style={{ padding: '8px 2px 0' }}>Chưa có lịch backup nào. Thêm lịch để bắt đầu theo dõi.</p>
          )}
        </div>
      </section>

      <section className="settings-section">
        <div className="settings-header">
          <h2>Thông tin hệ thống</h2>
          <p>Trạng thái hoạt động của hệ thống</p>
        </div>
        <div className="settings-card">
          <div className="settings-row settings-row--between">
            <div>
              <div className="settings-label">Trạng thái backend</div>
              <p className="settings-hint" style={{ margin: '4px 0 0' }}>Kết nối máy chủ</p>
            </div>
            <span className={`tag ${backendOnline === false ? 'tag--danger' : 'tag--success'}`}>
              {backendOnline === null
                ? 'Đang kiểm tra...'
                : backendOnline
                  ? 'Hoạt động'
                  : 'Ngoại tuyến'}
            </span>
          </div>
          <div className="settings-divider" />
          <div className="settings-row settings-row--between">
            <div>
              <div className="settings-label">Số bản backup</div>
              <p className="settings-hint" style={{ margin: '4px 0 0' }}>Tổng số bản ghi trong hệ thống</p>
            </div>
            <span className="cell-mono">{stats.total}</span>
          </div>
          <div className="settings-divider" />
          <div className="settings-row settings-row--between">
            <div>
              <div className="settings-label">Tổng dung lượng</div>
              <p className="settings-hint" style={{ margin: '4px 0 0' }}>Dung lượng đã backup</p>
            </div>
            <span className="cell-mono">{formatSize(stats.totalSize)}</span>
          </div>
          <div className="settings-divider" />
          <div className="settings-row settings-row--between">
            <div>
              <div className="settings-label">Tỷ lệ thành công</div>
              <p className="settings-hint" style={{ margin: '4px 0 0' }}>Phần trăm backup thành công</p>
            </div>
            <span className="cell-mono">{stats.successRate}%</span>
          </div>
        </div>
      </section>

      <section className="settings-section">
        <div className="settings-header">
          <h2>Xóa dữ liệu</h2>
          <p>Quản lý toàn bộ dữ liệu backup</p>
        </div>
        <div className="settings-card settings-card--danger">
          <div className="settings-danger-content">
            <div>
              <div className="settings-label">Xóa toàn bộ bản backup</div>
              <p className="settings-hint" style={{ margin: '4px 0 0' }}>Xóa tất cả bản ghi và reset ID về 1. Không thể hoàn tác.</p>
            </div>
            <button className="btn-danger" onClick={handleClearAll}>
              <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                <polyline points="3 6 5 6 21 6" />
                <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2" />
              </svg>
              Xóa tất cả
            </button>
          </div>
        </div>
      </section>
    </div>
  );
}
