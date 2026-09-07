package main

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

// BackupSchedule định nghĩa lịch backup kỳ vọng cho một nguồn (source).
type BackupSchedule struct {
	ID           uint       `gorm:"primaryKey" json:"id"`
	Source       string     `json:"source"`        // Nguồn backup (vd "Server Web LAMP")
	SourceKey    string     `json:"source_key"`    // Prefix tên file để so khớp (vd "web_code_")
	CronExpr     string     `json:"cron_expr"`     // Biểu thức cron chuẩn 5 trường
	Enabled      bool       `json:"enabled"`       // Bật/tắt theo dõi
	GraceMinutes int        `json:"grace_minutes"` // Thời gian ân hạn sau giờ cron (phút)
	PrevDueAt    *time.Time `json:"prev_due_at"`   // Mốc bắt đầu của kỳ kỳ vọng hiện tại
	NextDueAt    *time.Time `json:"next_due_at"`   // Lần cron kỳ vọng đang chờ hoàn thành
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// scheduleHasBackup kiểm tra có backup nào thuộc source/prefix với created_at ≥ mốc thời gian.
// Ưu tiên khớp theo prefix tên file (source_key) vì chính xác hơn tên hiển thị.
func scheduleHasBackup(source, sourceKey string, since time.Time) (bool, error) {
	var count int64
	tx := DB.Model(&BackupRecord{}).
		Where("status = ?", "Success").
		Where("created_at >= ?", since)
	// Bỏ qua các bản ghi "Missed" nội sinh (tên bắt đầu bằng MISSED_).
	tx = tx.Where("file_name NOT LIKE ?", "MISSED_%")
	if sourceKey != "" {
		// Khớp theo prefix tên file nếu có source_key.
		tx = tx.Where("file_name LIKE ?", sourceKey+"%")
	} else {
		tx = tx.Where("source = ?", source)
	}
	if err := tx.Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

// pendingDue trả về lần cron kỳ vọng đang chờ hoàn thành.
func (s BackupSchedule) pendingDue(sched cron.Schedule, now time.Time) time.Time {
	if s.NextDueAt != nil && !s.NextDueAt.IsZero() {
		return *s.NextDueAt
	}
	return sched.Next(now)
}

// checkBackupSchedules quét mọi schedule đang bật, phát hiện backup quá hạn
// và gửi cảnh báo nếu một lần cron đã qua deadline mà chưa thấy file mới.
func checkBackupSchedules() {
	schedules := []BackupSchedule{}
	if err := DB.Where("enabled = ?", true).Find(&schedules).Error; err != nil {
		log.Printf("Lỗi truy vấn lịch backup: %v", err)
		return
	}

	now := time.Now()

	for _, s := range schedules {
		sched, err := cron.ParseStandard(s.CronExpr)
		if err != nil {
			log.Printf("Lỗi parse cron %q cho source %s: %v", s.CronExpr, s.Source, err)
			continue
		}

		// Lần kỳ vọng đang chờ hoàn thành.
		due := s.pendingDue(sched, now)

		// Mốc bắt đầu của kỳ hiện tại: dùng PrevDueAt nếu có, nếu không (lịch vừa tạo)
		// dùng chính due làm mốc so sánh để không báo sai ngay khi tạo.
		periodStart := due
		if s.PrevDueAt != nil && !s.PrevDueAt.IsZero() {
			periodStart = *s.PrevDueAt
		}

		grace := time.Duration(s.GraceMinutes) * time.Minute
		if s.GraceMinutes <= 0 {
			grace = 30 * time.Minute
		}
		deadline := due.Add(grace)

		// Chưa tới deadline của lần kỳ vọng hiện tại -> chưa cần kiểm tra.
		if now.Before(deadline) {
			continue
		}

		// Chỉ xử lý lần kỳ vọng gần đây để tránh "đuổi kịp" hàng loạt lần cũ khi
		// backend bị tắt một thời gian dài (tránh spam cảnh báo).
		if due.Before(now.Add(-2 * time.Hour)) {
			// Lần cũ quá 2h không còn ý nghĩa cảnh báo realtime: đặt lại mốc theo dõi
			// từ lần kỳ vọng kế tiếp để tránh spam cảnh báo sau khi backend bị tắt lâu.
			advanceSchedule(&s, sched, now)
			continue
		}

		has, err := scheduleHasBackup(s.Source, s.SourceKey, periodStart)
		if err != nil {
			log.Printf("Lỗi kiểm tra backup cho %s: %v", s.Source, err)
			continue
		}
		if has {
			// Đã có backup cho kỳ này -> hoàn thành, dịch sang kỳ sau.
			advanceSchedule(&s, sched, due)
			continue
		}

		// Quá hạn và chưa thấy backup cho kỳ này.
		advanceSchedule(&s, sched, due)

		missed := BackupRecord{
			Source:    s.Source,
			FileName:  "MISSED_" + due.Format("20060102_150405"),
			Status:    "Missed",
			SizeMB:    0,
			CreatedAt: now,
		}
		DB.Create(&missed)

		log.Printf("⚠️ Thiếu bản backup cho %s (lịch trình dự kiến %s)", s.Source, due.Format(time.RFC3339))
		go sendNotifications(s.Source, "Missed (Thiếu bản backup dự kiến lúc "+due.Format("02/01/2006 15:04")+")")
	}
}

// advanceSchedule dời mốc kỳ vọng: kỳ hiện tại kết thúc tại `due`, kỳ mới bắt đầu từ
// đúng `due` và lần kỳ vọng tiếp theo là lần cron sau đó. Nếu `from == zero` thì
// khởi tạo cả hai mốc từ bây giờ.
func advanceSchedule(s *BackupSchedule, sched cron.Schedule, due time.Time) {
	if due.IsZero() {
		due = time.Now()
	}
	p := due
	s.PrevDueAt = &p
	n := sched.Next(due)
	s.NextDueAt = &n
	DB.Save(s)
}

// CheckAndAdvanceSchedulesForBackup kiểm tra các schedule đang bật có khớp với backup vừa tạo.
// Nếu khớp (theo source_key hoặc source), đánh dấu kỳ hiện tại đã hoàn thành bằng cách gọi advanceSchedule.
// Trả về true nếu có ít nhất một schedule được cập nhật.
func CheckAndAdvanceSchedulesForBackup(sourceName, fileName string) bool {
	schedules := []BackupSchedule{}
	if err := DB.Where("enabled = ?", true).Find(&schedules).Error; err != nil {
		log.Printf("Lỗi truy vấn lịch backup để kiểm tra: %v", err)
		return false
	}

	now := time.Now()
	updated := false

	for _, s := range schedules {
		sched, err := cron.ParseStandard(s.CronExpr)
		if err != nil {
			continue
		}

		// Kiểm tra khớp: ưu tiên source_key (prefix tên file), fallback source name
		matched := false
		if s.SourceKey != "" && strings.HasPrefix(fileName, s.SourceKey) {
			matched = true
		} else if s.Source == sourceName {
			matched = true
		}

		if !matched {
			continue
		}

		// Kỳ kỳ vọng đang chờ
		due := s.pendingDue(sched, now)

		// Backup vừa tạo có CreatedAt = now, luôn nằm trong kỳ hiện tại
		// Và fileName đã khớp prefix/source_key ở trên

		advanceSchedule(&s, sched, due)
		updated = true
		log.Printf("✅ Backup thủ công thỏa mãn lịch '%s' (source_key=%s), đã cập nhật kỳ kỳ vọng", s.Source, s.SourceKey)
	}

	return updated
}

// startScheduleChecker chạy kiểm tra lịch backup định kỳ trong goroutine.
func startScheduleChecker() {
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			checkBackupSchedules()
		}
	}()
}

// ---------- CRUD handlers ----------

func handleSchedules(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		var schedules []BackupSchedule
		DB.Order("id asc").Find(&schedules)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(schedules)

	case http.MethodPost:
		var s BackupSchedule
		if err := json.NewDecoder(r.Body).Decode(&s); err != nil {
			http.Error(w, "Dữ liệu JSON không hợp lệ", http.StatusBadRequest)
			return
		}
		// Validate cron
		sched, err := cron.ParseStandard(s.CronExpr)
		if err != nil {
			http.Error(w, "Biểu thức cron không hợp lệ: "+err.Error(), http.StatusBadRequest)
			return
		}
		s.CreatedAt = time.Now()
		s.UpdatedAt = time.Now()
		// Khởi tạo: kỳ đầu tiên bắt đầu từ bây giờ, lần kỳ vọng đầu là cron tiếp theo.
		now := time.Now()
		s.PrevDueAt = &now
		due := sched.Next(now)
		s.NextDueAt = &due
		if err := DB.Create(&s).Error; err != nil {
			http.Error(w, "Lỗi khi lưu lịch backup", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(s)

	case http.MethodPut:
		var s BackupSchedule
		if err := json.NewDecoder(r.Body).Decode(&s); err != nil {
			http.Error(w, "Dữ liệu JSON không hợp lệ", http.StatusBadRequest)
			return
		}
		if s.ID == 0 {
			http.Error(w, "Thiếu ID lịch backup", http.StatusBadRequest)
			return
		}
		var existing BackupSchedule
		if err := DB.First(&existing, s.ID).Error; err != nil {
			http.Error(w, "Không tìm thấy lịch backup", http.StatusNotFound)
			return
		}
		if _, err := cron.ParseStandard(s.CronExpr); err != nil {
			http.Error(w, "Biểu thức cron không hợp lệ: "+err.Error(), http.StatusBadRequest)
			return
		}
		s.CreatedAt = existing.CreatedAt
		// Nếu cron thay đổi thì đặt lại mốc kỳ vọng từ bây giờ; giữ nguyên nếu không đổi.
		if s.CronExpr != existing.CronExpr {
			sched, _ := cron.ParseStandard(s.CronExpr)
			now := time.Now()
			s.PrevDueAt = &now
			due := sched.Next(now)
			s.NextDueAt = &due
		} else {
			s.PrevDueAt = existing.PrevDueAt
			s.NextDueAt = existing.NextDueAt
		}
		s.UpdatedAt = time.Now()
		if err := DB.Save(&s).Error; err != nil {
			http.Error(w, "Lỗi khi cập nhật lịch backup", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(s)

	case http.MethodDelete:
		id := r.URL.Query().Get("id")
		if id == "" {
			http.Error(w, "Thiếu ID lịch backup", http.StatusBadRequest)
			return
		}
		DB.Delete(&BackupSchedule{}, id)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"status":  "success",
			"message": "Đã xóa lịch backup",
		})

	default:
		http.Error(w, "Phương thức không được hỗ trợ", http.StatusMethodNotAllowed)
	}
}
