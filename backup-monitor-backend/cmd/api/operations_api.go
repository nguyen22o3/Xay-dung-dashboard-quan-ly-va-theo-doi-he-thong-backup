package main

import (
	"backup-monitor-backend/internal/backupops"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func requireOperations(c *gin.Context) *operationStore {
	if activeOperations == nil {
		c.JSON(503, gin.H{"error": "Không đọc được trạng thái vận hành JSON"})
	}
	return activeOperations
}
func requireOperationsWorker(c *gin.Context) *operationStore {
	p := requireOperations(c)
	if p != nil && !p.worker {
		c.JSON(409, gin.H{"error": "Backend này chưa bật worker cảnh báo; bật OPERATIONS_WORKER_ENABLED trên một backend duy nhất"})
		return nil
	}
	return p
}
func registerOperationRoutes(auth *gin.RouterGroup) {
	auth.GET("/operations/status", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		p := requireOperations(c)
		if p == nil {
			return
		}
		s := p.snapshot()
		c.JSON(200, gin.H{"enabled": true, "storage": "json", "workerEnabled": p.worker, "options": s.Options, "collectedAt": s.CollectedAt, "collectionStale": s.CollectionFailed || s.CollectedAt == nil || s.CollectedAt.Before(s.EnabledAt) || time.Since(*s.CollectedAt) > 3*time.Minute})
	})
	auth.PUT("/operations/options", func(c *gin.Context) {
		p := requireOperations(c)
		if p == nil {
			return
		}
		var opts monitorOptions
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
		if c.ShouldBindJSON(&opts) != nil || opts.validate() != nil {
			c.JSON(400, gin.H{"error": "Cấu hình cảnh báo không hợp lệ"})
			return
		}
		if p.saveOptions(c.Request.Context(), opts) != nil {
			c.JSON(503, gin.H{"error": "Không lưu được cấu hình JSON"})
			return
		}
		c.JSON(200, opts)
	})
	auth.POST("/notifications/test", func(c *gin.Context) {
		db := requireOperationsWorker(c)
		if db == nil {
			return
		}
		var req struct {
			Channel string `json:"channel"`
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
		if c.ShouldBindJSON(&req) != nil {
			c.JSON(400, gin.H{"error": "Dữ liệu không hợp lệ"})
			return
		}
		settings, err := readAlertSettings("alert_settings.json")
		if err != nil {
			c.JSON(503, gin.H{"error": "Không đọc được cấu hình kênh"})
			return
		}
		enabled := false
		for _, channel := range alertChannels(settings) {
			if channel == req.Channel {
				enabled = true
			}
		}
		if !enabled {
			c.JSON(409, gin.H{"error": "Lưu cấu hình và bật kênh trước khi gửi thử"})
			return
		}
		// One persisted test per channel per minute, limiting duplicate clicks.
		key := "test:" + req.Channel + ":" + time.Now().UTC().Format("200601021504")
		if db.enqueue(c.Request.Context(), key, "Thông báo thử từ Backup Monitor. Kết nối kênh cảnh báo hoạt động.", []string{req.Channel}) != nil {
			c.JSON(503, gin.H{"error": "Không tạo được yêu cầu gửi thử"})
			return
		}
		c.JSON(202, gin.H{"status": "queued"})
	})

	auth.POST("/notifications/:id/retry", func(c *gin.Context) {
		p := requireOperationsWorker(c)
		if p == nil {
			return
		}
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil || id < 1 {
			c.JSON(400, gin.H{"error": "ID không hợp lệ"})
			return
		}
		if p.retryDelivery(c.Request.Context(), id) != nil {
			c.JSON(409, gin.H{"error": "Chỉ gửi lại thông báo đã thất bại hoàn toàn; kiểm tra quyền ghi file JSON"})
			return
		}
		c.JSON(202, gin.H{"status": "queued"})
	})
	auth.GET("/operations/alerts", func(c *gin.Context) {
		p := requireOperations(c)
		if p == nil {
			return
		}
		s := p.snapshot()
		entries := []alertEvent{}
		prefix := operationSource() + ":"
		for i := len(s.Alerts) - 1; i >= 0 && len(entries) < 100; i-- {
			if strings.HasPrefix(s.Alerts[i].Key, prefix) {
				a := s.Alerts[i]
				a.Key = ""
				entries = append(entries, a)
			}
		}
		c.JSON(200, gin.H{"entries": entries})
	})
	auth.GET("/cron-jobs/:id/log-page", func(c *gin.Context) {
		task, ok := managedCronTaskByID(c.Param("id"))
		if !ok {
			c.JSON(404, gin.H{"error": "Tác vụ không hợp lệ"})
			return
		}
		raw, err := backupSystemOperation("log-read", map[string]interface{}{"log": backupops.LogRequest{ID: task.ID, Cursor: c.Query("cursor")}})
		if err != nil {
			c.JSON(409, gin.H{"error": "Không đọc được trang log; hãy làm mới hoặc kiểm tra helper v12"})
			return
		}
		c.Data(200, "application/json", raw)
	})
	auth.GET("/cron-jobs/:id/process", func(c *gin.Context) {
		task, ok := managedCronTaskByID(c.Param("id"))
		if !ok {
			c.JSON(404, gin.H{"error": "Tác vụ không hợp lệ"})
			return
		}
		raw, err := backupSystemOperation("task-status", map[string]interface{}{"task": backupops.TaskRequest{ID: task.ID}})
		if err != nil {
			c.JSON(409, gin.H{"error": "Không xác minh được tiến trình; cần helper v12 và wrapper có ghi nhận PID"})
			return
		}
		c.Data(200, "application/json", raw)
	})
	auth.POST("/cron-jobs/:id/cancel", func(c *gin.Context) {
		task, ok := managedCronTaskByID(c.Param("id"))
		if !ok {
			c.JSON(404, gin.H{"error": "Tác vụ không hợp lệ"})
			return
		}
		var req struct {
			RunID   string `json:"runId"`
			Confirm bool   `json:"confirm"`
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
		if c.ShouldBindJSON(&req) != nil || !req.Confirm || !regexpRunID(req.RunID) {
			c.JSON(400, gin.H{"error": "Cần xác nhận đúng lần chạy trước khi hủy"})
			return
		}
		raw, err := backupSystemOperation("task-cancel", map[string]interface{}{"task": backupops.TaskRequest{ID: task.ID, RunID: req.RunID}})
		if err != nil {
			c.JSON(409, gin.H{"error": "Không hủy được hoặc lần chạy đã thay đổi; hãy làm mới"})
			return
		}
		invalidate("cron-jobs")
		c.Data(202, "application/json", raw)
	})

	auth.GET("/operations/restore-check-status", func(c *gin.Context) {
		raw, err := backupSystemOperation("restore-check-status", nil)
		if err != nil {
			c.JSON(503, gin.H{"error": "Không đọc được trạng thái kiểm tra"})
			return
		}
		c.Data(200, "application/json", raw)
	})
	registerRestoreRoutes(auth)
}
func regexpRunID(id string) bool {
	return len(id) <= 128 && strings.Contains(id, "-") && strings.Trim(id, "0123456789-") == ""
}
func registerRestoreRoutes(auth *gin.RouterGroup) {
	auth.POST("/restore/preview", func(c *gin.Context) {
		db := requireOperations(c)
		if db == nil {
			return
		}
		var req struct {
			Path string `json:"path"`
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
		if c.ShouldBindJSON(&req) != nil || !validSnapshotPath(req.Path) {
			c.JSON(400, gin.H{"error": "Tệp backup không hợp lệ"})
			return
		}
		raw, err := backupSystemOperation("restore-preview", map[string]interface{}{"restore": backupops.RestoreRequest{Path: req.Path}})
		if err != nil {
			c.JSON(409, gin.H{"error": "Không kiểm tra được backup; cần helper v12 và tệp trong cấu trúc ngày"})
			return
		}
		var plan backupops.RestorePlan
		if json.Unmarshal(raw, &plan) != nil || plan.Status != "preview" || plan.Path != req.Path || db.saveRestore(c.Request.Context(), plan) != nil {
			c.JSON(503, gin.H{"error": "Không lưu được kế hoạch khôi phục; chưa bắt đầu tải hoặc giải nén"})
			return
		}
		c.Data(200, "application/json", raw)
	})
	auth.POST("/restore/start", func(c *gin.Context) {
		db := requireOperations(c)
		if db == nil {
			return
		}
		var req struct {
			Token   string `json:"token"`
			Confirm bool   `json:"confirm"`
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
		if c.ShouldBindJSON(&req) != nil || !req.Confirm || !validRestoreToken(req.Token) {
			c.JSON(400, gin.H{"error": "Cần kiểm tra và xác nhận bản sao lưu trước"})
			return
		}
		if db.claimRestore(c.Request.Context(), req.Token) != nil {
			c.JSON(409, gin.H{"error": "Không tìm thấy kế hoạch chờ xác nhận của máy chủ này"})
			return
		}
		raw, err := backupSystemOperation("restore-start", map[string]interface{}{"restore": backupops.RestoreRequest{Token: req.Token}})
		if err != nil {
			c.JSON(409, gin.H{"error": "Không bắt đầu được khôi phục hoặc bản sao lưu đã thay đổi"})
			return
		}
		var plan backupops.RestorePlan
		if json.Unmarshal(raw, &plan) != nil || plan.Token != req.Token || plan.Status != "queued" {
			c.JSON(503, gin.H{"error": "Chưa xác nhận được yêu cầu khôi phục"})
			return
		}
		if db.saveRestore(c.Request.Context(), plan) != nil {
			c.Header("X-Restore-History", "unconfirmed")
		}
		c.Data(202, "application/json", raw)
	})
	auth.GET("/restore/:token", func(c *gin.Context) {
		db := requireOperations(c)
		if db == nil {
			return
		}
		if !validRestoreToken(c.Param("token")) {
			c.JSON(400, gin.H{"error": "Mã yêu cầu không hợp lệ"})
			return
		}
		if !db.knowsRestore(c.Param("token")) {
			c.JSON(404, gin.H{"error": "Không có yêu cầu khôi phục của máy chủ này"})
			return
		}
		raw, err := backupSystemOperation("restore-status", map[string]interface{}{"restore": backupops.RestoreRequest{Token: c.Param("token")}})
		if err != nil {
			c.JSON(404, gin.H{"error": "Không đọc được trạng thái khôi phục"})
			return
		}
		var plan backupops.RestorePlan
		if json.Unmarshal(raw, &plan) != nil || plan.Token != c.Param("token") || !validRestorePlan(plan) {
			c.JSON(503, gin.H{"error": "Trạng thái khôi phục không hợp lệ"})
			return
		}
		if db.saveRestore(c.Request.Context(), plan) != nil {
			c.Header("X-Restore-History", "unconfirmed")
		}
		c.Data(200, "application/json", raw)
	})
}
