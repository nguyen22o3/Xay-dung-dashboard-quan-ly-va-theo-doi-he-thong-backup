import React from 'react';

export default function FolderPickerModal({
  setFolderOpen, findResults, setFindResults, findName, setFindName,
  handleCreateField, currentFolderPath, setCurrentFolderPath, loadFolder,
  currentFolders, folderLoading
}) {
  return (
    <div className="modal-overlay" onClick={() => { setFolderOpen(false); setFindResults([]); setFindName('') }}>
      <div className="modal" onClick={e => e.stopPropagation()}>
        <div className="modal-header">
          <h2>Chọn đường dẫn lưu trên Server</h2>
          <button className="modal-close" onClick={() => { setFolderOpen(false); setFindResults([]); setFindName('') }} aria-label="Đóng">×</button>
        </div>
        <div className="modal-body">
          {findResults.length > 0 && (
            <div className="settings-row">
              <label className="settings-label">Tìm thấy "{findName}" tại {findResults.length} vị trí — chọn một:</label>
              <div style={{ border: '1px solid var(--border)', borderRadius: 8, overflow: 'hidden' }}>
                {findResults.map(r => (
                  <div key={r.path} style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: '7px 12px', borderBottom: '1px solid var(--border)' }}>
                    <span style={{ fontSize: 14, fontFamily: 'monospace' }}>{r.path}</span>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                      {!r.write && <span style={{ fontSize: 11, opacity: 0.5 }}>chỉ đọc</span>}
                      <button type="button" className="btn-primary btn-sm" onClick={() => { handleCreateField('dest_path', r.path); setFolderOpen(false); setFindResults([]); setFindName('') }}>Chọn</button>
                    </div>
                  </div>
                ))}
              </div>
            </div>
          )}
          <div className="settings-row">
            <label className="settings-label">Hoặc duyệt thư mục thủ công</label>
            <div style={{ border: '1.5px dashed var(--border)', borderRadius: 12, padding: 16, background: 'var(--bg-subtle)' }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 12, marginBottom: 12 }}>
                <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" style={{ opacity: 0.6 }}>
                  <path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z" />
                </svg>
                <div>
                  <strong>Chọn thư mục lưu trên Server</strong>
                  <div style={{ fontSize: 12, opacity: 0.7 }}>Gõ đường dẫn trực tiếp hoặc bấm vào thư mục bên dưới để di chuyển, sau đó bấm "Chọn"</div>
                </div>
              </div>
              <div className="path-pick" style={{ marginBottom: 10 }}>
                <input className="settings-input path-input" type="text" value={currentFolderPath} onChange={e => setCurrentFolderPath(e.target.value)} onKeyDown={e => { if (e.key === 'Enter') loadFolder(e.target.value) }} placeholder="Nhập đường dẫn và Enter" style={{ flex: 1 }} />
                <button type="button" className="btn-secondary btn-sm path-btn" onClick={() => loadFolder(currentFolderPath)} title="Tải lại">↻</button>
              </div>
              <div className="folder-crumb" style={{ display: 'flex', flexWrap: 'wrap', gap: 4, padding: '6px 10px', background: 'var(--bg-surface)', borderRadius: 8, border: '1px solid var(--border)', fontSize: 13, marginBottom: 10 }}>
                <button type="button" className="crumb-btn" onClick={() => loadFolder('/')}>/</button>
                {currentFolderPath.split('/').filter(Boolean).map((seg, i) => {
                  const upTo = '/' + currentFolderPath.split('/').filter(Boolean).slice(0, i + 1).join('/')
                  return (
                    <span key={i}>
                      <span className="crumb-sep">/</span>
                      <button type="button" className="crumb-btn" onClick={() => loadFolder(upTo)}>{seg}</button>
                    </span>
                  )
                })}
              </div>
              <div style={{ border: '1px solid var(--border)', borderRadius: 8, overflow: 'hidden', maxHeight: 280, overflowY: 'auto' }}>
                {folderLoading ? (
                  <p className="settings-hint" style={{ padding: 20, textAlign: 'center' }}>Đang tải...</p>
                ) : currentFolders.length === 0 ? (
                  <p className="settings-hint" style={{ padding: 20, textAlign: 'center' }}>Thư mục trống</p>
                ) : (
                  currentFolders.map(f => (
                    <div key={f.path} className={`folder-item ${f.write ? '' : 'folder-item--noread'}`} style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: '7px 12px', borderBottom: '1px solid var(--border)' }}>
                      <button type="button" className="folder-name" onClick={() => loadFolder(f.path)} style={{ display: 'flex', alignItems: 'center', gap: 8, flex: 1, textAlign: 'left', background: 'none', border: 'none', cursor: 'pointer', color: 'var(--text)', fontSize: 14 }}>
                        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" style={{ opacity: 0.6, flexShrink: 0 }}>
                          <path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z" />
                        </svg>
                        <span>{f.name}</span>
                      </button>
                      <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                        {!f.write && <span style={{ fontSize: 11, opacity: 0.5 }}>chỉ đọc</span>}
                        <button type="button" className="btn-primary btn-sm" style={{ fontSize: 12, padding: '3px 10px' }} onClick={() => { handleCreateField('dest_path', f.path); setFolderOpen(false); setFindResults([]); setFindName('') }}>Chọn</button>
                      </div>
                    </div>
                  ))
                )}
              </div>
            </div>
          </div>
        </div>
        <div className="modal-footer">
          <button type="button" className="btn-secondary" onClick={() => { setFolderOpen(false); setFindResults([]); setFindName('') }}>Hủy</button>
          <button type="button" className="btn-primary" onClick={() => { handleCreateField('dest_path', currentFolderPath); setFolderOpen(false); setFindResults([]); setFindName('') }}>Chọn thư mục hiện tại</button>
        </div>
      </div>
    </div>
  );
}
