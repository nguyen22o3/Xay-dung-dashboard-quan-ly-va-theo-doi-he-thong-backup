package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"backup-dashboard-backend/middleware"

	"github.com/joho/godotenv"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// 1. ĐỊNH NGHĨA CẤU TRÚC BẢNG DỮ LIỆU
// GORM sẽ dựa vào struct này để tự động tạo bảng 'backup_records' trong PostgreSQL
type BackupRecord struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	Source      string    `json:"source"`      // Nguồn backup (vd: Server Web LAMP)
	Destination string    `json:"destination"` // Nơi cất giữ (vd: "Server", "Google Drive", "NAS")
	FileName    string    `json:"file_name"`   // Tên file backup
	FilePath    string    `json:"file_path"`   // Đường dẫn đầy đủ (remote hoặc local) để xóa
	Status      string    `json:"status"`      // Trạng thái: "Success" hoặc "Failed"
	SizeMB      float64   `json:"size_mb"`     // Dung lượng file (Megabyte)
	CreatedAt   time.Time `json:"created_at"`  // Thời gian lưu dữ liệu
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
	DB.AutoMigrate(&BackupRecord{}, &BackupSchedule{})
	fmt.Println("✅ Đã tự động đồng bộ cấu trúc bảng (AutoMigrate)!")
}

func main() {
	// Load file .env (nếu không tìm thấy thì bỏ qua, dùng env của hệ thống)
	if err := godotenv.Load(); err != nil {
		log.Println("⚠️ Không tìm thấy file .env, sử dụng biến môi trường hệ thống")
	}

	// Gọi hàm kết nối DB ngay khi chương trình khởi chạy
	connectDatabase()

	// Khởi động quét Google Drive định kỳ (mô hình push: backend đọc Drive)
	startDriveSyncRunner()

	// Khởi động kiểm tra lịch backup kỳ vọng & cảnh báo quá hạn
	startScheduleChecker()

	// API Kiểm tra trạng thái (không cần auth)
	http.HandleFunc("/api/health", middleware.Logging(middleware.CORS(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"status":  "success",
			"message": "Backend kết nối Database ổn định!",
		})
	})))

	// API Đăng nhập Admin (không cần auth) — cấp JWT token
	http.HandleFunc("/api/login", middleware.Logging(middleware.CORS(handleLogin)))

	// API Lấy danh sách toàn bộ lịch sử Backup (cần JWT)
	http.HandleFunc("/api/backups", middleware.Logging(middleware.CORS(middleware.JWT(func(w http.ResponseWriter, r *http.Request) {
		var backups []BackupRecord
		DB.Order("created_at desc").Find(&backups)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(backups)
	}))))

	// API Quản lý lịch backup kỳ vọng (cần JWT) — GET/POST/PUT/DELETE
	http.HandleFunc("/api/schedules", middleware.Logging(middleware.CORS(middleware.JWT(handleSchedules))))

	// API Tạo bản backup thủ công (cần JWT)
	http.HandleFunc("/api/backup", middleware.Logging(middleware.CORS(middleware.JWT(createBackupHandler))))

	// API Upload file/thư mục từ trình duyệt để tạo backup (cần JWT)
	http.HandleFunc("/api/upload", middleware.Logging(middleware.CORS(middleware.JWT(uploadBackupHandler))))

	// API Liệt kê thư mục trên server (cần JWT) — dùng cho hộp thoại chọn nơi lưu
	http.HandleFunc("/api/folders", middleware.Logging(middleware.CORS(middleware.JWT(folderPickHandler))))

	// API Xóa backup tại đúng nơi cất giữ (cần JWT)
	http.HandleFunc("/api/delete", middleware.Logging(middleware.CORS(middleware.JWT(deleteBackupHandler))))

	// API Reset lại ID (re-index tất cả bản ghi từ 1)
	http.HandleFunc("/api/reset-ids", middleware.Logging(middleware.CORS(middleware.JWT(func(w http.ResponseWriter, r *http.Request) {
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
		if err := tx.Exec("DELETE FROM backup_records").Error; err != nil {
			tx.Rollback()
			http.Error(w, "Lỗi khi reset ID: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// Reset sequence về 1 - dùng ALTER SEQUENCE cho SERIAL, fallback setval
		if err := tx.Exec("ALTER SEQUENCE backup_records_id_seq RESTART WITH 1").Error; err != nil {
			// Fallback cho trường hợp tên sequence khác hoặc Postgres cũ
			if err2 := tx.Exec("SELECT setval(pg_get_serial_sequence('backup_records','id'), 1, false)").Error; err2 != nil {
				tx.Rollback()
				http.Error(w, "Lỗi khi reset sequence: "+err.Error(), http.StatusInternalServerError)
				return
			}
		}

		// Gán lại ID từ 1 và chèn lại
		for i := range records {
			records[i].ID = uint(i + 1)
		}
		if err := tx.CreateInBatches(records, 100).Error; err != nil {
			tx.Rollback()
			http.Error(w, "Lỗi khi reset ID: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// Cập nhật sequence để lần insert tiếp theo không trùng ID
		if err := tx.Exec("SELECT setval('backup_records_id_seq', (SELECT COALESCE(MAX(id), 1) FROM backup_records))").Error; err != nil {
			// Fallback generic
			tx.Exec("SELECT setval(pg_get_serial_sequence('backup_records','id'), (SELECT COALESCE(MAX(id), 1) FROM backup_records))")
		}

		if err := tx.Commit().Error; err != nil {
			http.Error(w, "Lỗi khi reset ID: "+err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"status":  "success",
			"message": "Đã reset ID thành công!",
			"count":   len(records),
		})
	}))))

	// API Xóa toàn bộ bản ghi Backup (cần auth) - xóa cả file thực tế trên Server/Drive/NAS
	http.HandleFunc("/api/clear-all", middleware.Logging(middleware.CORS(middleware.JWT(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Chỉ hỗ trợ phương thức POST", http.StatusMethodNotAllowed)
			return
		}

		// Lấy tất cả bản ghi để xóa file thực tế trước khi xóa DB
		var records []BackupRecord
		DB.Find(&records)
		log.Printf("Clear-all: tìm thấy %d bản ghi, bắt đầu xóa file thực tế...", len(records))

		deletedFiles := 0
		failedFiles := 0
		for _, rec := range records {
			log.Printf("Clear-all: đang xóa %s (%s) -> %s", rec.FileName, rec.Destination, rec.FilePath)
			if rec.FilePath == "" || rec.Destination == "Missed" {
				continue
			}
			var err error
			switch rec.Destination {
			case "Google Drive":
				err = deleteFromDrive(rec.FilePath)
			case "NAS":
				err = deleteFromNas(rec.FilePath)
			case "Server":
				err = os.Remove(rec.FilePath)
				if err != nil && os.IsNotExist(err) {
					err = nil // File đã không tồn tại, bỏ qua
				}
			default:
				continue
			}
			if err != nil {
				lower := strings.ToLower(err.Error())
				if strings.Contains(lower, "not found") || strings.Contains(lower, "no such") || strings.Contains(lower, "doesn't exist") || strings.Contains(lower, "not exist") {
					log.Printf("File không tồn tại khi xóa tất cả, bỏ qua: %s", rec.FilePath)
				} else {
					log.Printf("Lỗi xóa file %s (%s): %v", rec.FilePath, rec.Destination, err)
					failedFiles++
					continue
				}
			} else {
				log.Printf("Clear-all: đã xóa file %s", rec.FilePath)
			}
			deletedFiles++
		}
		log.Printf("Clear-all: xóa file xong: %d thành công, %d lỗi", deletedFiles, failedFiles)

		tx := DB.Begin()

		// Xóa tất cả bản ghi
		if err := tx.Exec("DELETE FROM backup_records").Error; err != nil {
			tx.Rollback()
			http.Error(w, "Lỗi khi xóa toàn bộ dữ liệu: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// Reset sequence về 1 - dùng ALTER SEQUENCE cho SERIAL, fallback setval
		if err := tx.Exec("ALTER SEQUENCE backup_records_id_seq RESTART WITH 1").Error; err != nil {
			if err2 := tx.Exec("SELECT setval(pg_get_serial_sequence('backup_records','id'), 1, false)").Error; err2 != nil {
				tx.Rollback()
				http.Error(w, "Lỗi khi reset sequence: "+err.Error(), http.StatusInternalServerError)
				return
			}
		}

		if err := tx.Commit().Error; err != nil {
			http.Error(w, "Lỗi khi xóa toàn bộ dữ liệu: "+err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"status":  "success",
			"message": "Đã xóa toàn bộ dữ liệu thành công!",
		})
	}))))

	// API Lấy cấu hình thông báo (cần auth)
	http.HandleFunc("/api/config", middleware.Logging(middleware.CORS(middleware.JWT(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		if r.Method == http.MethodGet {
			json.NewEncoder(w).Encode(maskedNotificationConfig(loadNotificationConfig()))
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

	// API Gửi thông báo thử nghiệm (cần auth) - dùng config thực từ .env, bỏ qua body
	http.HandleFunc("/api/test-notify", middleware.Logging(middleware.CORS(middleware.JWT(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Chỉ hỗ trợ phương thức POST", http.StatusMethodNotAllowed)
			return
		}

		sendTestNotifications(loadNotificationConfig())

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
