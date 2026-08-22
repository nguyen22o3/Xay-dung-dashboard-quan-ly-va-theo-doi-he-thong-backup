import { useState, useEffect } from 'react'

function App() {
  const [backups, setBackups] = useState([])
  const [loading, setLoading] = useState(true)

  // Tự động gọi API lấy dữ liệu khi mở web
  useEffect(() => {
    fetch('http://localhost:8080/api/backups')
      .then(response => response.json())
      .then(data => {
        // Nếu API trả về null (chưa có data), set thành mảng rỗng
        setBackups(data || [])
        setLoading(false)
      })
      .catch(error => {
        console.error("Lỗi khi tải dữ liệu:", error)
        setLoading(false)
      })
  }, [])

  return (
    <div style={{ padding: '30px', fontFamily: 'Arial, sans-serif', maxWidth: '1000px', margin: '0 auto' }}>
      <h2 style={{ color: '#1e293b', borderBottom: '2px solid #e2e8f0', paddingBottom: '10px' }}>
        📊 Dashboard Quản Lý Lịch Sử Backup
      </h2>

      {loading ? (
        <p>Đang tải dữ liệu từ máy chủ...</p>
      ) : (
        <table style={{ width: '100%', borderCollapse: 'collapse', marginTop: '20px', boxShadow: '0 4px 6px -1px rgba(0, 0, 0, 0.1)' }}>
          <thead>
            <tr style={{ backgroundColor: '#f8fafc', textAlign: 'left' }}>
              <th style={{ padding: '12px', border: '1px solid #cbd5e1' }}>ID</th>
              <th style={{ padding: '12px', border: '1px solid #cbd5e1' }}>Nguồn lưu trữ</th>
              <th style={{ padding: '12px', border: '1px solid #cbd5e1' }}>Tên File</th>
              <th style={{ padding: '12px', border: '1px solid #cbd5e1' }}>Dung lượng</th>
              <th style={{ padding: '12px', border: '1px solid #cbd5e1' }}>Trạng thái</th>
              <th style={{ padding: '12px', border: '1px solid #cbd5e1' }}>Thời gian</th>
            </tr>
          </thead>
          <tbody>
            {backups.map((item) => (
              <tr key={item.id} style={{ backgroundColor: '#ffffff' }}>
                <td style={{ padding: '12px', border: '1px solid #cbd5e1' }}>#{item.id}</td>
                <td style={{ padding: '12px', border: '1px solid #cbd5e1', fontWeight: 'bold' }}>{item.source}</td>
                <td style={{ padding: '12px', border: '1px solid #cbd5e1', color: '#0369a1' }}>{item.file_name}</td>
                <td style={{ padding: '12px', border: '1px solid #cbd5e1' }}>{item.size_mb} MB</td>
                <td style={{ padding: '12px', border: '1px solid #cbd5e1' }}>
                  {/* Tự động đổi màu nhãn tùy theo trạng thái */}
                  <span style={{
                    padding: '4px 8px',
                    borderRadius: '4px',
                    color: '#fff',
                    fontSize: '14px',
                    backgroundColor: item.status === 'Success' ? '#16a34a' : '#dc2626'
                  }}>
                    {item.status}
                  </span>
                </td>
                <td style={{ padding: '12px', border: '1px solid #cbd5e1' }}>
                  {new Date(item.created_at).toLocaleString('vi-VN')}
                </td>
              </tr>
            ))}
            
            {/* Hiển thị nếu mảng rỗng */}
            {backups.length === 0 && (
              <tr>
                <td colSpan="6" style={{ padding: '20px', textAlign: 'center', color: '#64748b' }}>
                  Chưa có dữ liệu backup nào được ghi nhận.
                </td>
              </tr>
            )}
          </tbody>
        </table>
      )}
    </div>
  )
}

export default App