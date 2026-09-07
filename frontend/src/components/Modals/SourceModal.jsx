import React from 'react';

export default function SourceModal({
  setSourceModalOpen, handleDrop, handleFileChange,
  uploadFiles, setUploadFiles
}) {
  return (
    <div className="modal-overlay" onClick={() => setSourceModalOpen(false)}>
      <div className="modal" onClick={e => e.stopPropagation()}>
        <div className="modal-header">
          <h2>Chọn nguồn dữ liệu</h2>
          <button className="modal-close" onClick={() => setSourceModalOpen(false)} aria-label="Đóng">×</button>
        </div>
        <div className="modal-body">
          <div className="settings-row">
            <label className="settings-label">Chọn nguồn từ máy</label>
            <div className="file-picker file-picker--unified" onDragOver={e => e.preventDefault()} onDrop={handleDrop} style={{ border: '1.5px dashed var(--border)', borderRadius: 12, padding: 16, background: 'var(--bg-subtle)' }}>
              <input type="file" id="upload-files" className="file-input" multiple onChange={handleFileChange} style={{ display: 'none' }} />
              <input type="file" id="upload-dir" className="file-input" multiple webkitdirectory="" onChange={handleFileChange} style={{ display: 'none' }} />
              <div className="file-btn file-btn--unified" style={{ display: 'flex', alignItems: 'center', gap: 12, marginBottom: 12 }}>
                <svg width="22" height="22" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" style={{ opacity: 0.6 }}>
                  <path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4" />
                  <polyline points="17 8 12 3 7 8" />
                  <line x1="12" y1="3" x2="12" y2="15" />
                </svg>
                <div>
                  <strong>Chọn file / thư mục</strong>
                  <div style={{ fontSize: 12, opacity: 0.7 }}>Hỗ trợ chọn nhiều file và cả thư mục (giữ cấu trúc) - có thể chọn nhiều lần, kéo thả vào đây</div>
                </div>
              </div>
              <div style={{ display: 'flex', gap: 8 }}>
                <label htmlFor="upload-files" className="btn-secondary btn-sm" style={{ cursor: 'pointer' }}>Chọn file</label>
                <label htmlFor="upload-dir" className="btn-secondary btn-sm" style={{ cursor: 'pointer' }}>Chọn thư mục</label>
                <span style={{ fontSize: 12, opacity: 0.6, alignSelf: 'center' }}>hoặc kéo thả file/thư mục vào khung</span>
              </div>
            </div>
            {uploadFiles.length > 0 && (
              <div className="upload-list">
                {uploadFiles.length === 1
                  ? `Đã chọn: ${uploadFiles[0].name}`
                  : `Đã chọn ${uploadFiles.length} file/folder`}
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
        </div>
        <div className="modal-footer">
          <button type="button" className="btn-secondary" onClick={() => setSourceModalOpen(false)}>Đóng</button>
          <button type="button" className="btn-primary" onClick={() => setSourceModalOpen(false)}>Xong</button>
        </div>
      </div>
    </div>
  );
}
