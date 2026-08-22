package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// 1. ĐỊNH NGHĨA CẤU TRÚC BẢNG DỮ LIỆU
// GORM sẽ dựa vào struct này để tự động tạo bảng 'backup_records' trong PostgreSQL
type BackupRecord struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Source    string    `json:"source"`    // Nguồn backup (vd: Server, NAS, Google Drive)
	FileName  string    `json:"file_name"` // Tên file backup
	Status    string    `json:"status"`    // Trạng thái: "Success" hoặc "Failed"
	SizeMB    float64   `json:"size_mb"`   // Dung lượng file (Megabyte)
	CreatedAt time.Time `json:"created_at"` // Thời gian lưu dữ liệu
}

// Biến toàn cục để lưu trữ kết nối Database
var DB *gorm.DB

// 2. HÀM KẾT NỐI DATABASE
func connectDatabase() {
	// Chuỗi cấu hình kết nối (thay đổi theo thông tin bạn đã tạo ở Bước 5)
	dsn := "host=localhost user=db_admin password=2212427 dbname=backup_monitor port=5432 sslmode=disable"
	
	var err error
	DB, err = gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatal("❌ Lỗi kết nối Database:", err)
	}

	fmt.Println("✅ Kết nối PostgreSQL thành công!")

	// Tự động tạo bảng dựa trên struct BackupRecord (nếu bảng chưa tồn tại)
	DB.AutoMigrate(&BackupRecord{})
	fmt.Println("✅ Đã tự động đồng bộ cấu trúc bảng (AutoMigrate)!")
}

func main() {
	// Gọi hàm kết nối DB ngay khi chương trình khởi chạy
	connectDatabase()

	// API Kiểm tra trạng thái
	http.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		w.Header().Set("Access-Control-Allow-Origin", "*")

		json.NewEncoder(w).Encode(map[string]string{
			"status":  "success",
			"message": "Backend kết nối Database ổn định!",
		})
	})

	// API Nhận dữ liệu từ các máy chủ Backup gửi về
	http.HandleFunc("/api/backup", func(w http.ResponseWriter, r *http.Request) {
		// Cho phép gọi từ Frontend khác port
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		// Bỏ qua request OPTIONS (trình duyệt tự gửi trước khi gọi POST)
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		// Chỉ nhận phương thức POST
		if r.Method != http.MethodPost {
			http.Error(w, "Chỉ hỗ trợ phương thức POST", http.StatusMethodNotAllowed)
			return
		}

		// Đọc dữ liệu JSON gửi lên
		var newData BackupRecord
		err := json.NewDecoder(r.Body).Decode(&newData)
		if err != nil {
			http.Error(w, "Dữ liệu JSON không hợp lệ", http.StatusBadRequest)
			return
		}

		// Thiết lập thời gian hiện tại
		newData.CreatedAt = time.Now()

		// Lưu thẳng vào PostgreSQL thông qua GORM
		result := DB.Create(&newData)
		if result.Error != nil {
			http.Error(w, "Lỗi khi lưu vào Database", http.StatusInternalServerError)
			return
		}

		// Trả về thông báo thành công
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"status":  "success",
			"message": "Đã lưu bản ghi backup thành công!",
		})
	})

	// API Lấy danh sách toàn bộ lịch sử Backup (Dành cho giao diện React)
	http.HandleFunc("/api/backups", func(w http.ResponseWriter, r *http.Request) {
		// Cho phép gọi từ Frontend
		w.Header().Set("Access-Control-Allow-Origin", "*")

		var backups []BackupRecord
		// Lấy toàn bộ dữ liệu từ Database, sắp xếp từ mới nhất đến cũ nhất
		DB.Order("created_at desc").Find(&backups)

		// Trả dữ liệu về cho React dưới dạng JSON
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(backups)
	})

	// Khởi chạy Server
	port := ":8080"
	fmt.Println("🚀 Backend Go đang chạy tại địa chỉ: http://localhost" + port)
	
	err := http.ListenAndServe(port, nil)
	if err != nil {
		fmt.Println("Lỗi khi khởi chạy server:", err)
	}
}