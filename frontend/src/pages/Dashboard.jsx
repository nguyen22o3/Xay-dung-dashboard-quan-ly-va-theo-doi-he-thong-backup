import React from 'react';
import { formatSize } from '../utils/formatters';

export default function Dashboard({ stats }) {
  return (
    <section className="stats-grid">
      <div className="stat-card">
        <div className="stat-icon-wrap">        
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
            <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
            <polyline points="17 8 12 3 7 8" />
            <line x1="12" y1="3" x2="12" y2="15" />
          </svg>
        </div>
        <div className="stat-body">
          <span className="stat-name">Tổng bản backup</span>
          <span className="stat-number">{stats.total}</span>
        </div>
      </div>

      <div className="stat-card">
        <div className="stat-icon-wrap stat-icon--success">
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
            <path d="M22 11.08V12a10 10 0 1 1-5.93-9.14" />
            <polyline points="22 4 12 14.01 9 11.01" />
          </svg>
        </div>
        <div className="stat-body">
          <span className="stat-name">Bản backup thành công</span>
          <span className="stat-number">{stats.success}</span>
        </div>
      </div>

      <div className="stat-card">
        <div className="stat-icon-wrap stat-icon--warning">
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
            <path d="M10.29 3.86L1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z" />
            <line x1="12" y1="9" x2="12" y2="13" />
            <line x1="12" y1="17" x2="12.01" y2="17" />
          </svg>
        </div>
        <div className="stat-body">
          <span className="stat-name">Bản backup thất bại</span>
          <span className="stat-number">{stats.failed}</span>
        </div>
      </div>

      <div className="stat-card">
        <div className="stat-icon-wrap stat-icon--success">
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
            <path d="M22 11.08V12a10 10 0 1 1-5.93-9.14" />
            <polyline points="22 4 12 14.01 9 11.01" />
          </svg>
        </div>
        <div className="stat-body">
          <span className="stat-name">Tỷ lệ thành công</span>
          <span className="stat-number">{stats.successRate}%</span>
        </div>
      </div>

      <div className="stat-card">
        <div className="stat-icon-wrap stat-icon--accent">
          <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round">
            <path d="M4 7V4a2 2 0 0 1 2-2h8.5L20 7.5V20a2 2 0 0 1-2 2H6a2 2 0 0 1-2-2v-3" />
            <polyline points="14 2 14 8 20 8" />
          </svg>
        </div>
        <div className="stat-body">
          <span className="stat-name">Tổng dung lượng</span>
          <span className="stat-number">{formatSize(stats.totalSize)}</span>
        </div>
      </div>
    </section>
  );
}
