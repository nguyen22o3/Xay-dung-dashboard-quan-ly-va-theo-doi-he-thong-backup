import { useMemo, useState } from 'react'
import { CloudDownload, Info, RefreshCw } from 'lucide-react'
import type { Lang } from '../language'
import { makeTheme } from '../theme'
import { apiErrorMessage, downloadSnapshot, useSnapshots } from '../api'
import type { SnapshotFile } from '../types'
import { formatBytes } from '../utils'

function snapshotKey(snapshot: SnapshotFile): string {
  return `${snapshot.date}/${snapshot.category}/${snapshot.name}`
}

export default function Available({ isDark, lang }: { isDark: boolean; lang: Lang }) {
  const t = makeTheme(isDark)
  const isVi = lang === 'vi'
  const snapshots = useSnapshots(60000)
  const [downloading, setDownloading] = useState<string | null>(null)
  const [downloadError, setDownloadError] = useState('')

  const files = useMemo(
    () => [...(snapshots.data ?? [])].sort((a, b) => {
      const byDate = b.date.localeCompare(a.date)
      return byDate !== 0 ? byDate : b.modified.localeCompare(a.modified)
    }),
    [snapshots.data],
  )

  const handleDownload = async (snapshot: SnapshotFile) => {
    const key = snapshotKey(snapshot)
    setDownloading(key)
    setDownloadError('')
    try {
      await downloadSnapshot(snapshot)
    } catch (error: unknown) {
      setDownloadError(apiErrorMessage(error, isVi ? 'Không thể tải bản sao lưu.' : 'Could not download the backup.'))
    } finally {
      setDownloading(null)
    }
  }

  return (
    <div className="animate-fade-in" style={{ padding: '20px' }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', gap: '16px', marginBottom: '8px' }}>
        <h2 style={{ margin: 0, fontSize: '20px', fontWeight: '500', color: t.titleColor }}>
          {isVi ? 'Bản sao lưu có sẵn' : 'Available Backups'}
        </h2>
        <button
          type="button"
          onClick={snapshots.reload}
          disabled={snapshots.loading}
          title={isVi ? 'Làm mới danh sách' : 'Refresh list'}
          style={{
            display: 'inline-flex',
            alignItems: 'center',
            gap: '6px',
            padding: '7px 10px',
            border: `1px solid ${t.cardBorder}`,
            borderRadius: '4px',
            background: 'transparent',
            color: t.textSecondary,
            cursor: snapshots.loading ? 'wait' : 'pointer',
          }}
        >
          <RefreshCw size={15} className={snapshots.loading ? 'animate-spin' : undefined} />
          {isVi ? 'Làm mới' : 'Refresh'}
        </button>
      </div>

      <p style={{ color: t.textSecondary, margin: '0 0 16px', fontSize: '14px' }}>
        {isVi
          ? 'Danh sách file sao lưu thực tế trên Google Drive.'
          : 'Actual backup files currently stored on Google Drive.'}
      </p>

      <div style={{ display: 'flex', alignItems: 'center', gap: '8px', marginBottom: '16px', padding: '10px 12px', borderRadius: '4px', background: isDark ? '#202d3a' : '#eef6ff', color: isDark ? '#93c5fd' : '#1d4f91', fontSize: '13px' }}>
        <Info size={16} />
        <span>
          {isVi
            ? 'Hiện tại hệ thống chỉ hỗ trợ tải file. Chức năng khôi phục lên máy chủ chưa được triển khai.'
            : 'Only file download is available. Server-side restore has not been implemented yet.'}
        </span>
      </div>

      {(snapshots.error || downloadError) && (
        <div role="alert" style={{ marginBottom: '14px', padding: '10px 12px', border: '1px solid rgba(239,68,68,0.35)', borderRadius: '4px', color: '#ef4444', fontSize: '13px' }}>
          {downloadError || (isVi ? `Không thể tải danh sách: ${snapshots.error}` : `Could not load backups: ${snapshots.error}`)}
        </div>
      )}

      <div style={{ backgroundColor: isDark ? t.cardBg : 'white', border: `1px solid ${t.cardBorder}`, borderRadius: '4px', overflow: 'hidden' }}>
        <div style={{ overflowX: 'auto' }}>
          <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: '13px' }}>
            <thead>
              <tr style={{ borderBottom: `1px solid ${t.cardBorder}`, backgroundColor: isDark ? 'rgba(255,255,255,0.02)' : '#f9fafb' }}>
                <th style={{ padding: '14px 16px', textAlign: 'left', fontWeight: '600', color: t.titleColor }}>{isVi ? 'Ngày' : 'Date'}</th>
                <th style={{ padding: '14px 16px', textAlign: 'left', fontWeight: '600', color: t.titleColor }}>{isVi ? 'Loại' : 'Category'}</th>
                <th style={{ padding: '14px 16px', textAlign: 'left', fontWeight: '600', color: t.titleColor }}>{isVi ? 'Tên file' : 'File name'}</th>
                <th style={{ padding: '14px 16px', textAlign: 'left', fontWeight: '600', color: t.titleColor }}>{isVi ? 'Kích thước' : 'Size'}</th>
                <th style={{ padding: '14px 16px', textAlign: 'right', fontWeight: '600', color: t.titleColor }}>{isVi ? 'Hành động' : 'Action'}</th>
              </tr>
            </thead>
            <tbody>
              {files.map((snapshot, index) => {
                const key = snapshotKey(snapshot)
                const isDownloading = downloading === key
                return (
                  <tr key={`${key}-${snapshot.modified}`} style={{ borderBottom: index === files.length - 1 ? 'none' : `1px solid ${t.cardBorder}` }}>
                    <td style={{ padding: '14px 16px', color: t.textPrimary, whiteSpace: 'nowrap' }}>{snapshot.date}</td>
                    <td style={{ padding: '14px 16px', color: t.textSecondary, textTransform: 'capitalize' }}>{snapshot.category}</td>
                    <td style={{ padding: '14px 16px', color: t.textPrimary, fontWeight: '500', overflowWrap: 'anywhere' }}>{snapshot.name}</td>
                    <td style={{ padding: '14px 16px', color: t.textSecondary, whiteSpace: 'nowrap' }}>{formatBytes(snapshot.size)}</td>
                    <td style={{ padding: '14px 16px', textAlign: 'right' }}>
                      <button
                        type="button"
                        onClick={() => handleDownload(snapshot)}
                        disabled={downloading !== null}
                        title={isVi ? 'Tải file' : 'Download file'}
                        style={{
                          background: isDownloading ? (isDark ? '#374151' : '#e5e7eb') : '#e6f3ff',
                          border: 'none',
                          color: isDownloading ? t.textSecondary : '#1976d2',
                          borderRadius: '4px',
                          padding: '7px 11px',
                          cursor: downloading !== null ? 'wait' : 'pointer',
                          display: 'inline-flex',
                          alignItems: 'center',
                          gap: '6px',
                          fontWeight: '600',
                          fontSize: '12px',
                        }}
                      >
                        <CloudDownload size={15} />
                        {isDownloading ? (isVi ? 'Đang tải...' : 'Downloading...') : (isVi ? 'Tải xuống' : 'Download')}
                      </button>
                    </td>
                  </tr>
                )
              })}

              {!snapshots.loading && files.length === 0 && !snapshots.error && (
                <tr>
                  <td colSpan={5} style={{ padding: '30px', textAlign: 'center', color: t.textSecondary }}>
                    {isVi ? 'Chưa có file sao lưu nào.' : 'No backup files found.'}
                  </td>
                </tr>
              )}

              {snapshots.loading && files.length === 0 && (
                <tr>
                  <td colSpan={5} style={{ padding: '30px', textAlign: 'center', color: t.textSecondary }}>
                    {isVi ? 'Đang tải dữ liệu...' : 'Loading backups...'}
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
