import React from 'react';
import { formatSize, formatTime } from '../utils/formatters';

export default function Backups({
  search, setSearch, setPageNum,
  filterStatus, setFilterStatus,
  filterSource, setFilterSource,
  filterDestination, setFilterDestination,
  sources, destinations,
  backups, paginated, filtered,
  safePage, totalPages,
  handleDelete
}) {
  return (
    <section className="content-card">
      <div className="card-toolbar">
        <div className="search-box">
          <svg className="search-icon" width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
            <circle cx="11" cy="11" r="8" />
            <line x1="21" y1="21" x2="16.65" y2="16.65" />
          </svg>
          <input
            type="text"
            className="search-input"
            placeholder="Tìm theo tên bản backup hoặc tên file..."
            value={search}
            onChange={e => { setSearch(e.target.value); setPageNum(1) }}
          />
        </div>
        <div className="filter-row">
          <select className="filter-select" value={filterStatus} onChange={e => { setFilterStatus(e.target.value); setPageNum(1) }}>
            <option value="all">Tất cả trạng thái</option>
            <option value="Success">Thành công</option>
            <option value="Failed">Thất bại</option>
            <option value="Processing">Đang chạy</option>
          </select>
          <select className="filter-select" value={filterSource} onChange={e => { setFilterSource(e.target.value); setPageNum(1) }}>
            {sources.map(s => (
              <option key={s} value={s}>{s === 'all' ? 'Tất cả nguồn' : s}</option>
            ))}
          </select>
          <select className="filter-select" value={filterDestination} onChange={e => { setFilterDestination(e.target.value); setPageNum(1) }}>
            {destinations.map(d => (
              <option key={d} value={d}>{d === 'all' ? 'Tất cả nơi lưu trữ' : d}</option>
            ))}
          </select>
        </div>
      </div>

      <div className="table-wrap">
        <table className="data-table">
          <thead>
            <tr>
              <th style={{ width: 64 }}>ID</th>
              <th>Tên bản backup</th>
              <th>Nơi lưu trữ</th>
              <th>Tên file</th>
              <th>Dung lượng</th>
              <th>Trạng thái</th>
              <th>Thời gian</th>
              <th style={{ width: 90, textAlign: 'center' }}>Thao tác</th>
            </tr>
          </thead>
          <tbody>
            {paginated.map(item => (
              <tr key={item.id}>
                <td className="cell-id"># {item.displayId}</td>
                <td className="cell-source">{item.source}</td>
                <td>
                  {item.status === 'Missed' ? (
                    <span className="dest-tag dest-tag--missed">--</span>
                  ) : (
                    ['Server', 'Google Drive', 'NAS'].map(dest => {
                      const hasDest = item.dests && item.dests[dest]
                      if (hasDest) {
                        return (
                          <div key={dest} style={{ marginBottom: 4 }}>
                            <span className={`dest-tag dest-tag--${dest.toLowerCase().replace(/\s+/g, '-')}`}>
                              {dest}
                            </span>
                          </div>
                        )
                      }
                      return (
                        <div key={dest} style={{ marginBottom: 4 }}>
                          <span className="dest-tag" style={{ background: 'transparent', border: '1px dashed #ccc', color: '#999' }}>
                            {dest}: Không có
                          </span>
                        </div>
                      )
                    })
                  )}
                </td>
                <td className="cell-filename" title={item.status === 'Missed' ? 'Không có file' : item.file_name}>
                  {item.status === 'Missed' ? '--' : item.file_name}
                </td>
                <td className="cell-mono">{formatSize(item.size_mb)}</td>
                <td>
                  <span className={`tag tag--${item.status === 'Success' ? 'success' : item.status === 'Missed' ? 'warning' : item.status === 'Processing' ? 'info' : 'danger'}`}>
                    {item.status === 'Success' ? 'Thành công' : item.status === 'Missed' ? 'Thiếu file' : item.status === 'Processing' ? 'Đang chạy' : 'Thất bại'}
                  </span>
                </td>
                <td className="cell-time">{formatTime(item.created_at)}</td>
                <td style={{ textAlign: 'center' }}>
                  <button className="btn-icon btn-icon--danger" onClick={() => handleDelete(item)} title="Xóa bản backup">
                    <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                      <polyline points="3 6 5 6 21 6" />
                      <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2" />
                    </svg>
                  </button>
                </td>
              </tr>
            ))}
            {filtered.length === 0 && (
              <tr>
                <td colSpan="8">
                  <div className="empty">
                    <svg width="40" height="40" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1" strokeLinecap="round" strokeLinejoin="round" style={{ opacity: 0.3 }}>
                      <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
                      <polyline points="17 8 12 3 7 8" />
                      <line x1="12" y1="3" x2="12" y2="15" />
                    </svg>
                    <p>{backups.length === 0 ? 'Chưa có bản backup nào' : 'Không tìm thấy kết quả phù hợp'}</p>
                  </div>
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>

      <div className="card-footer">
        <div className="pagination">
          <button
            className="pagination-btn"
            onClick={() => setPageNum(safePage - 1)}
            disabled={safePage <= 1}
            aria-label="Trang trước"
          >‹</button>
          <span className="pagination-info">{safePage} / {totalPages}</span>
          <button
            className="pagination-btn"
            onClick={() => setPageNum(safePage + 1)}
            disabled={safePage >= totalPages}
            aria-label="Trang sau"
          >›</button>
        </div>
      </div>
    </section>
  );
}
