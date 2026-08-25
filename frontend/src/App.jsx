import { useState, useEffect } from 'react'

function App() {
  const [backups, setBackups] = useState([])
  const [loading, setLoading] = useState(true)

  // 1. Tự động gọi API lấy dữ liệu khi mở web
  useEffect(() => {
    fetch('http://localhost:8080/api/backups')
      .then(response => response.json())
      .then(data => {
        setBackups(data || [])
        setLoading(false)
      })
      .catch(error => {
        console.error("Lỗi khi tải dữ liệu:", error)
        setLoading(false)
      })
  }, [])

  // Hàm gọi API xóa bản ghi
  const handleDelete = async (id) => {
    if (!window.confirm("Bạn có chắc chắn muốn xóa bản ghi này khỏi lịch sử và máy chủ?")) {
      return;
    }

    try {
      // Lưu ý: Đảm bảo đường dẫn API khớp với Backend Go của bạn
      const response = await fetch(`http://localhost:8080/api/delete?id=${id}`, {
        method: 'DELETE',
      });
      
      if (response.ok) {
        // Lọc bỏ bản ghi vừa xóa khỏi giao diện ngay lập tức
        setBackups(backups.filter(record => record.id !== id));
        alert("Đã xóa thành công!");
      } else {
        alert("Xóa thất bại, vui lòng kiểm tra máy chủ.");
      }
    } catch (error) {
      console.error("Lỗi khi xóa dữ liệu:", error);
    }
  };
  // ==================================================

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
              {/* VỊ TRÍ THÊM 2: Cột tiêu đề Hành động */}
              <th style={{ padding: '12px', border: '1px solid #cbd5e1', textAlign: 'center' }}>Hành động</th>
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
                
                {/* VỊ TRÍ THÊM 3: Nút Xóa cho từng dòng */}
                <td style={{ padding: '12px', border: '1px solid #cbd5e1', textAlign: 'center' }}>
                  <button 
                    onClick={() => handleDelete(item.id)}
                    style={{ 
                      backgroundColor: '#ef4444', 
                      color: 'white', 
                      border: 'none', 
                      padding: '6px 12px', 
                      borderRadius: '4px', 
                      cursor: 'pointer',
                      fontWeight: 'bold'
                    }}
                    onMouseOver={(e) => e.target.style.backgroundColor = '#dc2626'}
                    onMouseOut={(e) => e.target.style.backgroundColor = '#ef4444'}
                  >
                    Xóa
                  </button>
                </td>
              </tr>
            ))}
            
            {/* Hiển thị nếu mảng rỗng */}
            {backups.length === 0 && (
              <tr>
                {/* VỊ TRÍ THÊM 4: Sửa colSpan từ 6 thành 7 vì đã thêm 1 cột */}
                <td colSpan="7" style={{ padding: '20px', textAlign: 'center', color: '#64748b' }}>
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