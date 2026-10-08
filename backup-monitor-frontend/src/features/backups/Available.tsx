import { Button } from '../../shared/ui'
import { useMemo, useState } from 'react'
import { CloudDownload, Info, RefreshCw } from 'lucide-react'
import type { Lang } from '../../shared/language'
import { makeTheme } from '../../shared/theme'
import { apiErrorMessage, downloadSnapshot, useSnapshots } from '../../api/dashboard'
import type { SnapshotFile } from '../../shared/types'
import { categoryLabel, formatBytes } from '../../shared/utils'
import RestorePanel from './RestorePanel'

function snapshotKey(snapshot: SnapshotFile): string {
  return `${snapshot.date}/${snapshot.category}/${snapshot.name}`
}

export default function Available({ isDark, lang }: { isDark: boolean; lang: Lang }) {
  const t = makeTheme(isDark)
  const isVi = lang === 'vi'
  const snapshots = useSnapshots(60000)
  const [downloading, setDownloading] = useState<string | null>(null)
  const [downloadError, setDownloadError] = useState('')
  const [filterDate, setFilterDate] = useState('')
  const [filterCategory, setFilterCategory] = useState('')
  const [restoreFile, setRestoreFile] = useState<SnapshotFile | null>(null)

  const files = useMemo(
    () => [...(snapshots.data ?? [])].filter(file => (!filterDate || file.date.slice(0, 10) === filterDate) && (!filterCategory || file.category === filterCategory)).sort((a, b) => {
      const byDate = b.date.localeCompare(a.date)
      return byDate !== 0 ? byDate : b.modified.localeCompare(a.modified)
    }),
    [snapshots.data, filterDate, filterCategory],
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
    <div className="animate-fade-in legacy-page available-page" style={{ padding: '20px' }}>
      <div className="ui-page-heading">
        <h2 style={{ margin: 0, fontSize: '20px', fontWeight: '500', color: t.titleColor }}>
          {isVi ? 'Bản sao lưu có sẵn' : 'Available Backups'}
        </h2>
        <button className="legacy-secondary-button"
          type="button"
          onClick={snapshots.reload}
          disabled={snapshots.loading}
          title={isVi ? 'Làm mới danh sách' : 'Refresh list'}
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
      <div className="backup-filters">
        <label>{isVi ? 'Ngày sao lưu' : 'Backup date'}<input type="date" value={filterDate} onChange={event => setFilterDate(event.target.value)} /></label>
        <label>{isVi ? 'Loại sao lưu' : 'Backup type'}<select value={filterCategory} onChange={event => setFilterCategory(event.target.value)}><option value="">{isVi ? 'Tất cả' : 'All types'}</option>{[...new Set((snapshots.data ?? []).map(file => file.category))].sort().map(category => <option key={category} value={category}>{categoryLabel(category, lang)}</option>)}</select></label>
        {(filterDate || filterCategory) && <button type="button" className="legacy-secondary-button" onClick={() => { setFilterDate(''); setFilterCategory('') }}>{isVi ? 'Bỏ lọc' : 'Clear filters'}</button>}
        <span>{files.length} {isVi ? 'tệp' : 'files'} · {formatBytes(files.reduce((sum, file) => sum + file.size, 0))}</span>
      </div>

      <div style={{ display: 'flex', alignItems: 'center', gap: '8px', marginBottom: '16px', padding: '10px 12px', borderRadius: '4px', background: isDark ? '#202d3a' : '#eef6ff', color: isDark ? '#93c5fd' : '#1d4f91', fontSize: '13px' }}>
        <Info size={16} />
        <span>
          {isVi
            ? 'Có thể tải file hoặc khôi phục vào thư mục riêng trên máy chủ để kiểm tra; không ghi đè dữ liệu đang chạy.'
            : 'Download files or restore into a private server directory for inspection; live data is not overwritten.'}
        </span>
      </div>

      {(snapshots.error || downloadError) && (
        <div role="alert" style={{ marginBottom: '14px', padding: '10px 12px', border: '1px solid rgba(239,68,68,0.35)', borderRadius: '4px', color: '#ef4444', fontSize: '13px' }}>
          {downloadError || (isVi ? `Không thể tải danh sách: ${snapshots.error}` : `Could not load backups: ${snapshots.error}`)}
        </div>
      )}
      {restoreFile && <RestorePanel key={restoreFile.path || snapshotKey(restoreFile)} file={restoreFile} lang={lang} close={() => setRestoreFile(null)} />}

      <div className="ui-data-table-wrap" style={{ backgroundColor: t.cardBg, border: `1px solid ${t.cardBorder}` }}>
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
                    <td style={{ padding: '14px 16px', color: t.textSecondary }}>{categoryLabel(snapshot.category, lang)}</td>
                    <td style={{ padding: '14px 16px', color: t.textPrimary, fontWeight: '500', overflowWrap: 'anywhere' }}>{snapshot.name}</td>
                    <td style={{ padding: '14px 16px', color: t.textSecondary, whiteSpace: 'nowrap' }}>{formatBytes(snapshot.size)}</td>
                    <td style={{ padding: '14px 16px', textAlign: 'right' }}><div className="ui-table-actions">
                      <Button variant="soft" size="small"
                        type="button"
                        onClick={() => handleDownload(snapshot)}
                        disabled={downloading !== null}
                        title={isVi ? 'Tải file' : 'Download file'}
                      >
                        <CloudDownload size={15} />
                        {isDownloading ? (isVi ? 'Đang tải...' : 'Downloading...') : (isVi ? 'Tải xuống' : 'Download')}
                      </Button>
                      {['site', 'database', 'panel'].includes(snapshot.category) && <button type="button" className="legacy-secondary-button" disabled={downloading !== null} onClick={() => { if (!restoreFile || window.confirm(isVi ? 'Đổi bản sao lưu đang xem? Thao tác khôi phục đã bắt đầu trên máy chủ vẫn tiếp tục.' : 'Change the selected backup? A server restore already started will keep running.')) setRestoreFile(snapshot) }}>{isVi ? 'Khôi phục riêng' : 'Stage restore'}</button>}</div>
                    </td>
                  </tr>
                )
              })}

              {!snapshots.loading && files.length === 0 && !snapshots.error && (
                <tr>
                  <td colSpan={5} style={{ padding: '30px', textAlign: 'center', color: t.textSecondary }}>
                    {filterDate || filterCategory ? (isVi ? 'Không có bản sao lưu khớp bộ lọc.' : 'No backups match the filters.') : (isVi ? 'Chưa có file sao lưu nào.' : 'No backup files found.')}
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
