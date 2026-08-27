package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"backup-dashboard-backend/middleware"

	"github.com/joho/godotenv"
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
	dsn := fmt.Sprintf("host=%s user=%s password=%s dbname=%s port=%s sslmode=disable",
		os.Getenv("DB_HOST"),
		os.Getenv("DB_USER"),
		os.Getenv("DB_PASSWORD"),
		os.Getenv("DB_NAME"),
		os.Getenv("DB_PORT"),
	)
	
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
	// Load file .env (nếu không tìm thấy thì bỏ qua, dùng env của hệ thống)
	if err := godotenv.Load(); err != nil {
		log.Println("⚠️ Không tìm thấy file .env, sử dụng biến môi trường hệ thống")
	}

	// Gọi hàm kết nối DB ngay khi chương trình khởi chạy
	connectDatabase()

	// API Kiểm tra trạng thái (không cần auth)
	http.HandleFunc("/api/health", middleware.Logging(middleware.CORS(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"status":  "success",
			"message": "Backend kết nối Database ổn định!",
		})
	})))

	// API Nhận dữ liệu từ các máy chủ Backup gửi về (cần auth)
	http.HandleFunc("/api/backup", middleware.Logging(middleware.CORS(middleware.APIKey(func(w http.ResponseWriter, r *http.Request) {
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

		go sendNotifications(newData.FileName, newData.Status)
	}))))

	// API Lấy danh sách toàn bộ lịch sử Backup (cần auth)
	http.HandleFunc("/api/backups", middleware.Logging(middleware.CORS(middleware.APIKey(func(w http.ResponseWriter, r *http.Request) {
		var backups []BackupRecord
		DB.Order("created_at desc").Find(&backups)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(backups)
	}))))

	// API Xóa một bản ghi Backup (cần auth)
	http.HandleFunc("/api/delete", middleware.Logging(middleware.CORS(middleware.APIKey(func(w http.ResponseWriter, r *http.Request) {
		// Lấy ID từ đường dẫn URL (ví dụ: /api/delete?id=5)
		id := r.URL.Query().Get("id")
		if id == "" {
			http.Error(w, "Thiếu ID bản ghi", http.StatusBadRequest)
			return
		}

		DB.Delete(&BackupRecord{}, id)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"status":  "success",
			"message": "Đã xóa bản ghi thành công!",
		})
	}))))

	// API Reset lại ID (re-index tất cả bản ghi từ 1)
	http.HandleFunc("/api/reset-ids", middleware.Logging(middleware.CORS(middleware.APIKey(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Chỉ hỗ trợ phương thức POST", http.StatusMethodNotAllowed)
			return
		}

		// Lấy tất cả bản ghi, sắp xếp theo thời gian tạo
		var records []BackupRecord
		DB.Order("created_at asc").Find(&records)

		if len(records) == 0 {
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(map[string]string{
				"status":  "success",
				"message": "Không có bản ghi nào để reset!",
			})
			return
		}

		// Bắt đầu transaction
		tx := DB.Begin()

		// Xóa tất cả bản ghi cũ
		tx.Exec("DELETE FROM backup_records")

		// Reset sequence về 1
		tx.Exec("ALTER SEQUENCE backup_records_id_seq RESTART WITH 1")

		// Gán lại ID từ 1 và chèn lại
		for i := range records {
			records[i].ID = uint(i + 1)
		}
		tx.CreateInBatches(records, 100)

		if tx.Error != nil {
			tx.Rollback()
			http.Error(w, "Lỗi khi reset ID", http.StatusInternalServerError)
			return
		}

		tx.Commit()

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "success",
			"message": "Đã reset ID thành công!",
			"count":   len(records),
		})
	}))))

	// API Xóa toàn bộ bản ghi Backup (cần auth)
	http.HandleFunc("/api/clear-all", middleware.Logging(middleware.CORS(middleware.APIKey(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Chỉ hỗ trợ phương thức POST", http.StatusMethodNotAllowed)
			return
		}

		tx := DB.Begin()

		// Xóa tất cả bản ghi
		tx.Exec("DELETE FROM backup_records")

		// Reset sequence về 1
		tx.Exec("ALTER SEQUENCE backup_records_id_seq RESTART WITH 1")

		if tx.Error != nil {
			tx.Rollback()
			http.Error(w, "Lỗi khi xóa toàn bộ dữ liệu", http.StatusInternalServerError)
			return
		}

		tx.Commit()

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"status":  "success",
			"message": "Đã xóa toàn bộ dữ liệu thành công!",
		})
	}))))

	// API Lấy cấu hình thông báo (cần auth)
	http.HandleFunc("/api/config", middleware.Logging(middleware.CORS(middleware.APIKey(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method == http.MethodGet {
			json.NewEncoder(w).Encode(loadNotificationConfig())
			return
		}

		if r.Method == http.MethodPost {
			var cfg NotificationConfig
			if err := json.NewDecoder(r.Body).Decode(&cfg); err != nil {
				http.Error(w, "Dữ liệu JSON không hợp lệ", http.StatusBadRequest)
				return
			}
			if err := saveNotificationConfig(cfg); err != nil {
				http.Error(w, "Lỗi khi lưu cấu hình", http.StatusInternalServerError)
				return
			}
			json.NewEncoder(w).Encode(map[string]string{
				"status":  "success",
				"message": "Đã lưu cấu hình thành công!",
			})
			return
		}

		http.Error(w, "Phương thức không được hỗ trợ", http.StatusMethodNotAllowed)
	}))))

	// API Gửi thông báo thử nghiệm (cần auth) - nhận config từ body để thử ngay không cần lưu
	http.HandleFunc("/api/test-notify", middleware.Logging(middleware.CORS(middleware.APIKey(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Chỉ hỗ trợ phương thức POST", http.StatusMethodNotAllowed)
			return
		}

		var cfg NotificationConfig
		json.NewDecoder(r.Body).Decode(&cfg)
		sendTestNotifications(cfg)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"status":  "success",
			"message": "Đã gửi thông báo thử nghiệm!",
		})
	}))))

	// API Khởi chạy Server
	port := os.Getenv("SERVER_PORT")
	if port == "" {
		port = "8080"
	}
	fmt.Println("🚀 Backend Go đang chạy tại địa chỉ: http://localhost:" + port)
	
	err := http.ListenAndServe(":"+port, nil)
	if err != nil {
		fmt.Println("Lỗi khi khởi chạy server:", err)
	}
}