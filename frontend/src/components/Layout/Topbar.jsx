import React from 'react';
import { formatTime } from '../../utils/formatters';

export default function Topbar({ current, page, stats, setCreateOpen, handleRefresh, refreshing }) {
  return (
    <header className="topbar">
      <div className="topbar-left">
        <h1 className="page-title">{current.title}</h1>
        <p className="page-desc">{current.desc}</p>
      </div>
      <div className="topbar-right">
        {page !== 'settings' && (
          <>
            <div className="topbar-time">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                <circle cx="12" cy="12" r="10" />
                <polyline points="12 6 12 12 16 14" />
              </svg>
              <span>Cập nhật: {stats.lastBackup ? formatTime(stats.lastBackup) : 'Chưa có'}</span>
            </div>
            <button className="btn-create" onClick={() => setCreateOpen(true)} title="Tạo bản backup mới">
              <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                <line x1="12" y1="5" x2="12" y2="19" />
                <line x1="5" y1="12" x2="19" y2="12" />
              </svg>
              <span>Tạo bản backup</span>
            </button>
            <button className="btn-refresh" onClick={handleRefresh} disabled={refreshing} title="Làm mới dữ liệu">
              <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" className={refreshing ? 'spin' : ''}>
                <polyline points="23 4 23 10 17 10" />
                <path d="M20.49 15a9 9 0 1 1-2.12-9.36L23 10" />
              </svg>
              <span>{refreshing ? 'Đang tải...' : 'Làm mới'}</span>
            </button>
          </>
        )}
      </div>
    </header>
  );
}
