import { useState, useEffect } from 'react'
import { CloudDownload, RotateCcw, CheckCircle } from 'lucide-react'
import type { Lang } from '../language'
import { makeTheme } from '../theme'
import { useBackupStatus } from '../api'
import { formatBytes } from '../utils'

export default function Available({ isDark, lang }: { isDark: boolean, lang: Lang }) {
  const t = makeTheme(isDark)
  const isVi = lang === 'vi'
  
  // Lấy data thật từ API (Lịch sử backup trên Google Drive)
  const { data } = useBackupStatus(60000)
  const history = [...(data?.history || [])].sort((a, b) => b.date.localeCompare(a.date))

  const [restoringDate, setRestoringDate] = useState<string | null>(null)
  const [progress, setProgress] = useState(0)
  const [showSuccess, setShowSuccess] = useState(false)

  // Giả lập tiến trình khôi phục
  useEffect(() => {
    if (restoringDate) {
      setProgress(0)
      setShowSuccess(false)
      const interval = setInterval(() => {
        setProgress(p => {
          if (p >= 100) {
            clearInterval(interval)
            setTimeout(() => {
              setRestoringDate(null)
              setShowSuccess(true)
              setTimeout(() => setShowSuccess(false), 3000)
            }, 500)
            return 100
          }
          return Math.min(100, p + Math.floor(Math.random() * 10) + 5)
        })
      }, 200)
      return () => clearInterval(interval)
    }
  }, [restoringDate])

  return (
    <div className="animate-fade-in" style={{ padding: '20px' }}>
      
      {showSuccess && (
        <div style={{
          position: 'fixed',
          top: '20px',
          right: '20px',
          backgroundColor: '#4caf50',
          color: 'white',
          padding: '12px 24px',
          borderRadius: '4px',
          boxShadow: '0 4px 12px rgba(0,0,0,0.15)',
          display: 'flex',
          alignItems: 'center',
          gap: '12px',
          zIndex: 1000,
          animation: 'slide-in 0.3s ease-out'
        }}>
          <CheckCircle size={20} />
          <span style={{ fontWeight: '500' }}>
            {isVi ? 'Khôi phục dữ liệu thành công!' : 'Data restored successfully!'}
          </span>
        </div>
      )}

      <h2 style={{ margin: '0 0 20px 0', fontSize: '20px', fontWeight: '500', color: t.titleColor }}>
        {isVi ? 'Bản sao lưu có sẵn' : 'Available Backups'}
      </h2>
      <p style={{ color: t.textSecondary, marginBottom: '24px', fontSize: '14px' }}>
        {isVi ? 'Danh sách các bản sao lưu đang được lưu trữ theo ngày trên Google Drive.' : 'List of backups securely stored by date on Google Drive.'}
      </p>

      <div style={{ backgroundColor: isDark ? t.cardBg : 'white', border: `1px solid ${t.cardBorder}`, borderRadius: '4px', overflow: 'hidden' }}>
        <div style={{ overflowX: 'auto' }}>
          <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: '13px' }}>
            <thead>
              <tr style={{ borderBottom: `1px solid ${t.cardBorder}`, backgroundColor: isDark ? 'rgba(255,255,255,0.02)' : '#f9fafb' }}>
                <th style={{ padding: '16px', textAlign: 'left', fontWeight: '600', color: t.titleColor }}>
                  {isVi ? 'Thư mục Ngày' : 'Date Folder'}
                </th>
                <th style={{ padding: '16px', textAlign: 'left', fontWeight: '600', color: t.titleColor }}>
                  {isVi ? 'Kích thước thực tế' : 'Real Size'}
                </th>
                <th style={{ padding: '16px', textAlign: 'right', fontWeight: '600', color: t.titleColor }}>
                  {isVi ? 'Hành động' : 'Actions'}
                </th>
              </tr>
            </thead>
            <tbody>
              {history.length > 0 ? history.map((item, i) => (
                <tr key={item.date} style={{ borderBottom: i === history.length - 1 ? 'none' : `1px solid ${t.cardBorder}` }}>
                  <td style={{ padding: '16px', color: t.textPrimary, fontWeight: '500' }}>
                    {item.date}
                  </td>
                  <td style={{ padding: '16px', color: t.textSecondary }}>
                    {formatBytes(item.bytes)}
                  </td>
                  <td style={{ padding: '16px', textAlign: 'right' }}>
                    {restoringDate === item.date ? (
                      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'flex-end', gap: '12px' }}>
                        <div style={{ fontSize: '12px', color: '#2196f3', fontWeight: '600', minWidth: '40px', textAlign: 'right' }}>
                          {progress}%
                        </div>
                        <div style={{ width: '100px', height: '6px', backgroundColor: isDark ? '#333' : '#e0e0e0', borderRadius: '3px', overflow: 'hidden' }}>
                          <div style={{ width: `${progress}%`, height: '100%', backgroundColor: '#2196f3', transition: 'width 0.2s ease' }} />
                        </div>
                      </div>
                    ) : (
                      <div style={{ display: 'flex', gap: '8px', justifyContent: 'flex-end' }}>
                        <button
                          title={isVi ? 'Tải xuống' : 'Download'}
                          style={{
                            background: 'transparent',
                            border: `1px solid ${t.cardBorder}`,
                            color: t.textSecondary,
                            borderRadius: '4px',
                            padding: '6px 10px',
                            cursor: 'pointer',
                            display: 'flex',
                            alignItems: 'center',
                            transition: 'all 0.2s'
                          }}
                          onMouseOver={(e) => { e.currentTarget.style.color = '#2196f3'; e.currentTarget.style.borderColor = '#2196f3' }}
                          onMouseOut={(e) => { e.currentTarget.style.color = t.textSecondary; e.currentTarget.style.borderColor = t.cardBorder }}
                        >
                          <CloudDownload size={16} />
                        </button>
                        <button
                          onClick={() => setRestoringDate(item.date)}
                          disabled={restoringDate !== null}
                          title={isVi ? 'Khôi phục ngay' : 'Restore Now'}
                          style={{
                            background: restoringDate !== null ? (isDark ? '#333' : '#ccc') : '#e6f3ff',
                            border: 'none',
                            color: restoringDate !== null ? t.textSecondary : '#1976d2',
                            borderRadius: '4px',
                            padding: '6px 12px',
                            cursor: restoringDate !== null ? 'not-allowed' : 'pointer',
                            display: 'flex',
                            alignItems: 'center',
                            gap: '6px',
                            fontWeight: '600',
                            fontSize: '12px',
                            transition: 'all 0.2s'
                          }}
                        >
                          <RotateCcw size={14} className={restoringDate === item.date ? "animate-spin" : ""} />
                          {isVi ? 'Khôi phục' : 'Restore'}
                        </button>
                      </div>
                    )}
                  </td>
                </tr>
              )) : (
                <tr>
                  <td colSpan={4} style={{ padding: '30px', textAlign: 'center', color: t.textSecondary }}>
                    {isVi ? 'Đang tải dữ liệu...' : 'Loading data...'}
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>
    </div>
  )
}
