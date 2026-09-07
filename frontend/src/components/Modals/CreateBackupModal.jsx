import React from 'react';

export default function CreateBackupModal({
  closeCreateModal, createBackup,
  createForm, handleCreateField,
  handleSourcePickClick,
  uploadFiles, setUploadFiles,
  toggleDestination, handleDestPickClick,
  creating
}) {
  return (
    <div className="modal-overlay" onClick={closeCreateModal}>
      <div className="modal" onClick={e => e.stopPropagation()}>
        <div className="modal-header">
          <h2>Tạo bản backup</h2>
          <button className="modal-close" onClick={closeCreateModal} aria-label="Đóng">×</button>
        </div>
        <form onSubmit={createBackup}>
          <div className="modal-body">
            <div className="settings-row">
              <label className="settings-label">Nguồn dữ liệu</label>
              <div className="path-pick">
                <input className="settings-input path-input" type="text" value={createForm.source_path} onChange={e => handleCreateField('source_path', e.target.value)} />
                <button type="button" className="btn-secondary btn-sm path-btn" onClick={() => handleSourcePickClick('dir')} title="Chọn thư mục trên server">
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round"><path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z" /></svg>
                </button>
                <button type="button" className="btn-secondary btn-sm path-btn" onClick={() => handleSourcePickClick('file')} title="Chọn file trên server" style={{ marginLeft: 4 }}>
                  <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round"><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"></path><polyline points="14 2 14 8 20 8"></polyline></svg>
                </button>
              </div>
              {uploadFiles.length > 0 && (
                <div className="upload-list">
                  {uploadFiles.length === 1
                    ? `Đã chọn từ máy: ${uploadFiles[0].name}`
                    : `Đã chọn từ máy ${uploadFiles.length} file/folder`}
                  <ul>
                    {uploadFiles.slice(0, 5).map((f, i) => (
                      <li key={i}>{f.webkitRelativePath || f.name}</li>
                    ))}
                    {uploadFiles.length > 5 && <li>... và {uploadFiles.length - 5} mục khác</li>}
                  </ul>
                  <button type="button" className="btn-secondary btn-sm" style={{ marginTop: 6 }} onClick={() => { setUploadFiles([]) }}>Bỏ chọn</button>
                </div>
              )}
            </div>

            <div className="settings-row">
              <label className="settings-label">Tên bản backup (tùy chọn)</label>
              <input className="settings-input" type="text" placeholder="VD: Server Web LAMP (chỉ để hiển thị tên)" value={createForm.source_name} onChange={e => handleCreateField('source_name', e.target.value)} />
            </div>

            <div className="settings-row">
              <label className="settings-label">Tên file nén (tùy chọn)</label>
              <input className="settings-input" type="text" placeholder="VD: data_web (Tên file sẽ là: data_web_2024...) " value={createForm.custom_file_name} onChange={e => handleCreateField('custom_file_name', e.target.value)} />
            </div>

            <div className="settings-row">
              <label className="settings-label">Nơi lưu trữ</label>
              <div className="dest-check-row">
                <label className={`dest-check ${(createForm.destinations || []).includes('server') ? 'dest-check--active' : ''}`}>
                  <input type="checkbox" checked={(createForm.destinations || []).includes('server')} onChange={() => toggleDestination('server')} />
                  <span className="dest-check-box"><span className="dot dot--server" /></span>
                  <span>Server</span>
                </label>
                <label className={`dest-check ${(createForm.destinations || []).includes('drive') ? 'dest-check--active' : ''}`}>
                  <input type="checkbox" checked={(createForm.destinations || []).includes('drive')} onChange={() => toggleDestination('drive')} />
                  <span className="dest-check-box"><span className="dot dot--drive" /></span>
                  <span>Google Drive</span>
                </label>
                <label className={`dest-check ${(createForm.destinations || []).includes('nas') ? 'dest-check--active' : ''}`}>
                  <input type="checkbox" checked={(createForm.destinations || []).includes('nas')} onChange={() => toggleDestination('nas')} />
                  <span className="dest-check-box"><span className="dot dot--nas" /></span>
                  <span>NAS</span>
                </label>
              </div>
            </div>

            {(createForm.destinations || []).includes('server') && (
              <div className="settings-row">
                <label className="settings-label">Nơi lưu trữ trên Server</label>
                <div className="path-pick">
                  <input className="settings-input path-input" type="text" value={createForm.dest_path} onChange={e => handleCreateField('dest_path', e.target.value)} required />
                  <button type="button" className="btn-secondary btn-sm path-btn" onClick={handleDestPickClick} title="Chọn thư mục trên server">
                    <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round"><path d="M22 19a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h5l2 3h9a2 2 0 0 1 2 2z" /></svg>
                  </button>
                </div>
              </div>
            )}
          </div>
          <div className="modal-footer">
            <button type="button" className="btn-secondary" onClick={closeCreateModal}>Hủy</button>
            <button type="submit" className="btn-primary" disabled={creating}>
              {creating ? 'Đang tạo...' : 'Tạo backup'}
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}
