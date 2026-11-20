import { useMemo, useState } from 'react'
import { RefreshCw } from 'lucide-react'
import type { Lang } from './language'
import { makeTheme } from './theme'
import { useSnapshots } from './dashboardApi'
import type { SnapshotFile } from './types'
import { categoryLabel, formatBytes } from './utils'
import RestorePanel from './RestorePanel'

const pageSize = 20

function snapshotKey(snapshot: SnapshotFile): string {
  return `${snapshot.date}/${snapshot.category}/${snapshot.name}`
}

export default function Available({ isDark, lang }: { isDark: boolean; lang: Lang }) {
  const t = makeTheme(isDark)
  const isVi = lang === 'vi'
  const snapshots = useSnapshots(60000)
  const [filterDate, setFilterDate] = useState('')
  const [filterCategory, setFilterCategory] = useState('')
  const [search, setSearch] = useState('')
  const [page, setPage] = useState(1)
  const [restoreFile, setRestoreFile] = useState<SnapshotFile | null>(null)
  const labels = {
    date: isVi ? 'Ngày' : 'Date',
    category: isVi ? 'Loại' : 'Category',
    name: isVi ? 'Tên tệp' : 'File name',
    size: isVi ? 'Dung lượng' : 'Size',
    action: isVi ? 'Hành động' : 'Action',
  }
  const query = search.trim().toLocaleLowerCase()
  const filtering = Boolean(query || filterDate || filterCategory)

  const files = useMemo(
    () => (snapshots.data ?? []).filter(file =>
      (!filterDate || file.date.slice(0, 10) === filterDate) &&
      (!filterCategory || file.category === filterCategory) &&
      (!query || file.name.toLocaleLowerCase().includes(query)),
    ).sort((a, b) => {
      const byDate = b.date.localeCompare(a.date)
      return byDate !== 0 ? byDate : b.modified.localeCompare(a.modified)
    }),
    [snapshots.data, filterDate, filterCategory, query],
  )
  const pageCount = Math.max(1, Math.ceil(files.length / pageSize))
  const currentPage = Math.min(page, pageCount)
  const pageFiles = files.slice((currentPage - 1) * pageSize, currentPage * pageSize)

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
        <label>{isVi ? 'Tìm tên tệp' : 'Find a file'}<input type="search" value={search} onChange={event => { setSearch(event.target.value); setPage(1) }} /></label>
        <label>{isVi ? 'Ngày sao lưu' : 'Backup date'}<input type="date" value={filterDate} onChange={event => { setFilterDate(event.target.value); setPage(1) }} /></label>
        <label>{isVi ? 'Loại sao lưu' : 'Backup type'}<select value={filterCategory} onChange={event => { setFilterCategory(event.target.value); setPage(1) }}><option value="">{isVi ? 'Tất cả' : 'All types'}</option>{[...new Set((snapshots.data ?? []).map(file => file.category))].sort().map(category => <option key={category} value={category}>{categoryLabel(category, lang)}</option>)}</select></label>
        {filtering && <button type="button" className="legacy-secondary-button" onClick={() => { setSearch(''); setFilterDate(''); setFilterCategory(''); setPage(1) }}>{isVi ? 'Bỏ lọc' : 'Clear filters'}</button>}
        <span>{files.length} {isVi ? 'tệp' : 'files'} · {formatBytes(files.reduce((sum, file) => sum + file.size, 0))}</span>
      </div>

      {snapshots.error && (
        <div role="alert" style={{ marginBottom: '14px', padding: '10px 12px', border: '1px solid rgba(239,68,68,0.35)', borderRadius: '4px', color: '#ef4444', fontSize: '13px' }}>
          {isVi ? `Không thể tải danh sách: ${snapshots.error}` : `Could not load backups: ${snapshots.error}`}
        </div>
      )}
      {restoreFile && <RestorePanel key={restoreFile.path || snapshotKey(restoreFile)} file={restoreFile} lang={lang} close={() => setRestoreFile(null)} />}

      {pageCount > 1 && <nav className="backup-pagination" aria-label={isVi ? 'Phân trang bản sao lưu' : 'Backup pagination'}>
        <button type="button" className="legacy-secondary-button" disabled={currentPage === 1} onClick={() => setPage(currentPage - 1)}>{isVi ? 'Trang trước' : 'Previous'}</button>
        <span aria-live="polite">{isVi ? 'Trang' : 'Page'} {currentPage} / {pageCount}</span>
        <button type="button" className="legacy-secondary-button" disabled={currentPage === pageCount} onClick={() => setPage(currentPage + 1)}>{isVi ? 'Trang sau' : 'Next'}</button>
      </nav>}
      <div className="ui-data-table-wrap" style={{ backgroundColor: t.cardBg, border: `1px solid ${t.cardBorder}` }}>
        <div style={{ overflowX: 'auto' }}>
          <table className="responsive-data-table" role="table" style={{ width: '100%', borderCollapse: 'collapse', fontSize: '13px' }}>
            <thead>
              <tr style={{ borderBottom: `1px solid ${t.cardBorder}`, backgroundColor: isDark ? 'rgba(255,255,255,0.02)' : '#f9fafb' }}>
                <th scope="col">{labels.date}</th>
                <th scope="col">{labels.category}</th>
                <th scope="col">{labels.name}</th>
                <th scope="col" style={{ width: '14%' }}>{labels.size}</th>
                <th scope="col" style={{ width: '1%', whiteSpace: 'nowrap' }}>{labels.action}</th>
              </tr>
            </thead>
            <tbody>
              {pageFiles.map((snapshot, index) => {
                const key = snapshotKey(snapshot)
                return (
                  <tr role="row" key={`${key}-${snapshot.modified}`} style={{ borderBottom: index === pageFiles.length - 1 ? 'none' : `1px solid ${t.cardBorder}` }}>
                    <td role="cell" data-label={labels.date} style={{ padding: '14px 16px', color: t.textPrimary, whiteSpace: 'nowrap' }}>{snapshot.date}</td>
                    <td role="cell" data-label={labels.category} style={{ padding: '14px 16px', color: t.textSecondary }}>{categoryLabel(snapshot.category, lang)}</td>
                    <td role="cell" className="data-table-name" style={{ padding: '14px 16px', color: t.textPrimary, fontWeight: '500', overflowWrap: 'anywhere' }}>{snapshot.name}</td>
                    <td role="cell" data-label={labels.size} style={{ padding: '14px 16px', color: t.textSecondary, whiteSpace: 'nowrap' }}>{formatBytes(snapshot.size)}</td>
                    <td role="cell" className="data-table-action" style={{ padding: '14px 16px' }}>
                      <div className="ui-table-actions">
                        {['site', 'database', 'panel'].includes(snapshot.category) && (
                          <button
                            type="button"
                            className="legacy-secondary-button"
                            title={isVi ? 'Tải file nén về thư mục trên AlmaLinux, không giải nén và không ghi đè dữ liệu đang chạy' : 'Download archive to a folder on AlmaLinux without extracting or overwriting live data'}
                            onClick={() => {
                              if (!restoreFile || window.confirm(isVi ? 'Đổi bản sao lưu đang xem? Thao tác đã bắt đầu trên máy chủ vẫn tiếp tục.' : 'Change the selected backup? An operation already started on the server will keep running.')) {
                                setRestoreFile(snapshot)
                              }
                            }}
                          >
                            {isVi ? 'Tải xuống máy chủ AlmaLinux' : 'Download to AlmaLinux server'}
                          </button>
                        )}
                      </div>
                    </td>
                  </tr>
                )
              })}

              {!snapshots.loading && files.length === 0 && !snapshots.error && (
                <tr>
                  <td colSpan={5} style={{ padding: '30px', textAlign: 'center', color: t.textSecondary }}>
                    {filtering ? (isVi ? 'Không có bản sao lưu khớp bộ lọc.' : 'No backups match the filters.') : (isVi ? 'Chưa có file sao lưu nào.' : 'No backup files found.')}
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
