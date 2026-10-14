package main

import (
	"backup-monitor-backend/internal/backupops"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"net/http"
	"strings"
)

func requireRestoreState(c *gin.Context) *restoreStore {
	if activeRestores == nil {
		c.JSON(503, gin.H{"error": "Không đọc được trạng thái khôi phục JSON"})
	}
	return activeRestores
}
func registerOperationRoutes(auth *gin.RouterGroup) {
	auth.GET("/cron-jobs/:id/log-page", func(c *gin.Context) {
		task, ok := managedCronTaskByID(c.Param("id"))
		if !ok {
			c.JSON(404, gin.H{"error": "Tác vụ không hợp lệ"})
			return
		}
		raw, err := backupSystemOperation("log-read", map[string]interface{}{"log": backupops.LogRequest{ID: task.selector(), Cursor: c.Query("cursor")}})
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
		raw, err := backupSystemOperation("task-status", map[string]interface{}{"task": backupops.TaskRequest{ID: task.selector()}})
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
		raw, err := backupSystemOperation("task-cancel", map[string]interface{}{"task": backupops.TaskRequest{ID: task.selector(), RunID: req.RunID}})
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
		db := requireRestoreState(c)
		if db == nil {
			return
		}
		var req struct {
			Path   string `json:"path"`
			Parent string `json:"parent"`
			Name   string `json:"name"`
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
		if c.ShouldBindJSON(&req) != nil || !validSnapshotPath(req.Path) {
			c.JSON(400, gin.H{"error": "Tệp backup không hợp lệ"})
			return
		}
		var destination string
		if req.Parent != "" || req.Name != "" {
			var err error
			destination, err = backupops.RestoreDestination(req.Parent, req.Name)
			if err != nil {
				c.JSON(400, gin.H{"error": err.Error()})
				return
			}
		}
		raw, err := backupSystemOperation("restore-preview", map[string]interface{}{"restore": backupops.RestoreRequest{Path: req.Path, Parent: req.Parent, Name: req.Name}})
		if err != nil {
			c.JSON(409, gin.H{"error": "Không kiểm tra được bản sao lưu/nơi lưu (cần helper v24 trên AlmaLinux): " + err.Error()})
			return
		}
		var plan backupops.RestorePlan
		if json.Unmarshal(raw, &plan) == nil && destination != "" && (plan.Directory != destination || plan.Parent != req.Parent || plan.Name != req.Name) {
			c.JSON(409, gin.H{"error": "Helper chưa hỗ trợ nơi lưu/tên đã chọn; cập nhật helper v24 trên AlmaLinux. Chưa bắt đầu tải."})
			return
		}
		if json.Unmarshal(raw, &plan) != nil || plan.Status != "preview" || plan.Path != req.Path || db.saveRestore(c.Request.Context(), plan) != nil {
			c.JSON(503, gin.H{"error": "Không lưu được kế hoạch khôi phục; chưa bắt đầu tải hoặc giải nén"})
			return
		}
		c.Data(200, "application/json", raw)
	})
	auth.POST("/restore/start", func(c *gin.Context) {
		db := requireRestoreState(c)
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
			c.JSON(409, gin.H{"error": "Không bắt đầu được tải/giải nén; hãy kiểm tra lại: " + err.Error()})
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
		db := requireRestoreState(c)
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
