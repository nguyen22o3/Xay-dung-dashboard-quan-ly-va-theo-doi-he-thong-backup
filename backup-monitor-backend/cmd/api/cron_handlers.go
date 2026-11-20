package main

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// ===== CRON JOB COMMAND DISPATCHERS =====

var runJobSSHCommand = executeSSHCommand

// Only allowlisted scripts still scheduled in root's crontab may be started.
// The client never supplies a command or path.
func commandForCronJob(jobID string) (string, bool) {
	if task, ok := managedCronTaskByID(jobID); ok {
		return commandForManagedCronJob(task), true
	}
	return "", false
}

func commandForJob(jobID string) (string, bool) {
	switch jobID {
	case "drive-sync":
		return commandForCronJob("drive-sync")
	case "site-backup":
		return commandForCronJob("backup-site")
	case "database-backup":
		return commandForCronJob("backup-database")
	default:
		return "", false
	}
}

// ===== CRON JOB ROUTE HANDLERS =====

func registerCronRoutes(auth *gin.RouterGroup) {
	// Kích hoạt một loại backup cố định bằng script được quản lý.
	auth.POST("/run-job", func(c *gin.Context) {
		var req struct {
			JobID string `json:"jobId"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Dữ liệu không hợp lệ"})
			return
		}
		command, ok := commandForJob(req.JobID)
		if !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": "jobId không hợp lệ"})
			return
		}
		output, err := runJobSSHCommand(command)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể khởi chạy backup"})
			return
		}
		if strings.TrimSpace(output) != "STARTED" {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Không xác nhận được backup đã khởi chạy"})
			return
		}
		invalidate("cron-jobs")
		c.JSON(http.StatusAccepted, gin.H{"status": "started"})
	})

	// Chạy thủ công đúng script được quản lý còn hiện diện trong crontab.
	var cronRunMu sync.Mutex
	lastCronRun := make(map[string]time.Time)
	auth.POST("/run-cron-job", func(c *gin.Context) {
		var req struct {
			JobID string `json:"jobId"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Dữ liệu không hợp lệ"})
			return
		}
		command, ok := commandForCronJob(req.JobID)
		if !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Mã cronjob không hợp lệ"})
			return
		}
		cronRunMu.Lock()
		if time.Since(lastCronRun[req.JobID]) < time.Minute {
			cronRunMu.Unlock()
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "Cronjob vừa được khởi chạy; hãy đợi ít nhất 1 phút"})
			return
		}
		lastCronRun[req.JobID] = time.Now()
		cronRunMu.Unlock()
		releaseReservation := func() {
			cronRunMu.Lock()
			delete(lastCronRun, req.JobID)
			cronRunMu.Unlock()
		}
		output, err := runJobSSHCommand(command)
		if err != nil {
			releaseReservation()
			if strings.Contains(err.Error(), "CRON_ALREADY_RUNNING") {
				c.JSON(http.StatusConflict, gin.H{"error": "Tác vụ đang chạy; hãy đợi hoàn tất và xem log"})
				return
			}
			if strings.Contains(err.Error(), "CRON_NOT_SCHEDULED") {
				c.JSON(http.StatusConflict, gin.H{"error": "Cronjob không còn trong lịch chạy"})
				return
			}
			if strings.Contains(err.Error(), "CRON_SCRIPT_UNAVAILABLE") {
				c.JSON(http.StatusConflict, gin.H{"error": "Script cronjob không còn khả dụng"})
				return
			}
			if strings.Split(req.JobID, "~")[0] == "cleanup-panel" {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Dọn dẹp aaPanel chưa hoàn tất; hãy kiểm tra log cronjob"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể khởi chạy cronjob"})
			return
		}
		if strings.Split(req.JobID, "~")[0] == "cleanup-panel" {
			deleted, ok := parsePanelCleanupResult(output)
			if !ok {
				releaseReservation()
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Không xác nhận được kết quả dọn dẹp aaPanel"})
				return
			}
			invalidate("cron-jobs", "server-status", "local-snapshots", "backup-activity")
			c.JSON(http.StatusOK, gin.H{"status": "completed", "deletedCount": deleted})
			return
		}
		if strings.TrimSpace(output) != "STARTED" {
			releaseReservation()
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Không xác nhận được cronjob đã khởi chạy"})
			return
		}
		// Triggering is not completion: keep the expensive Drive/snapshot caches
		// available until their normal TTL expires, so dashboards stay responsive.
		invalidate("cron-jobs")
		c.JSON(http.StatusAccepted, gin.H{"status": "started"})
	})

	// Liệt kê các script được quản lý trong root crontab.
	auth.POST("/cron-tracking", func(c *gin.Context) {
		output, err := executeSSHCommand(buildCronTrackingInstallCommand())
		if err != nil {
			switch {
			case strings.Contains(err.Error(), "CRON_NOT_SCHEDULED"):
				c.JSON(http.StatusConflict, gin.H{"error": "Không tìm thấy cron sao lưu website, cơ sở dữ liệu hoặc cấu hình aaPanel để bật ghi nhận"})
			case strings.Contains(err.Error(), "CRON_DUPLICATE"):
				c.JSON(http.StatusConflict, gin.H{"error": "Có lịch sao lưu trùng; cần kiểm tra crontab trước khi bật ghi nhận"})
			case strings.Contains(err.Error(), "CRON_WRAPPER_CONFLICT"):
				c.JSON(http.StatusConflict, gin.H{"error": "File bộ ghi nhận đã tồn tại nhưng không thuộc dashboard; chưa ghi đè"})
			case strings.Contains(err.Error(), "CRON_SCRIPT_UNAVAILABLE"):
				c.JSON(http.StatusConflict, gin.H{"error": "Script sao lưu không còn khả dụng; chưa đổi crontab"})
			default:
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể bật ghi nhận cron tự động; lịch cũ được lưu dự phòng nếu đã cập nhật"})
			}
			return
		}
		if strings.TrimSpace(output) != "TRACKING_ENABLED" {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Không xác nhận được việc bật ghi nhận cron tự động"})
			return
		}
		invalidate("cron-jobs", "server-status", "backup-status")
		c.JSON(http.StatusOK, gin.H{"status": "enabled"})
	})

	auth.GET("/cron-jobs", func(c *gin.Context) {
		output, err := cachedGetFreshOrError("cron-jobs", 3*time.Second, func() ([]byte, error) {
			return fetchManagedCronJobs()
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể đọc tiến trình định kỳ", "detail": err.Error()})
			return
		}
		c.Data(http.StatusOK, "application/json", output)
	})

	auth.GET("/cron-jobs/:id/log", func(c *gin.Context) {
		task, ok := managedCronTaskByID(c.Param("id"))
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "Cronjob không hợp lệ"})
			return
		}
		c.Header("Cache-Control", "no-store")
		result, err := fetchManagedCronJobLog(task)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể đọc log cronjob"})
			return
		}
		c.JSON(http.StatusOK, result)
	})

	auth.PUT("/cron-jobs/:id/schedule", func(c *gin.Context) {
		task, ok := managedCronTaskByID(c.Param("id"))
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "Cronjob không hợp lệ"})
			return
		}
		var req struct {
			Time             string `json:"time"`
			ExpectedSchedule string `json:"expectedSchedule"`
			ExpectedEnabled  *bool  `json:"expectedEnabled"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.ExpectedEnabled == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Dữ liệu không hợp lệ"})
			return
		}
		command, valid := commandForCronScheduleUpdate(task, req.Time, req.ExpectedSchedule, *req.ExpectedEnabled)
		if !valid {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Giờ chạy hoặc lịch hiện tại không hợp lệ"})
			return
		}
		output, err := executeSSHCommand(command)
		if err != nil {
			switch {
			case strings.Contains(err.Error(), "CRON_NOT_SCHEDULED"):
				c.JSON(http.StatusConflict, gin.H{"error": "Cronjob không còn trong lịch chạy"})
			case strings.Contains(err.Error(), "CRON_DUPLICATE"):
				c.JSON(http.StatusConflict, gin.H{"error": "Có dòng cron cùng loại và cùng giờ/chu kỳ; hãy chọn lịch khác hoặc xử lý dòng trùng"})
			case strings.Contains(err.Error(), "CRON_SCHEDULE_CHANGED"):
				c.JSON(http.StatusConflict, gin.H{"error": "Lịch chạy đã thay đổi; hãy làm mới danh sách và thử lại"})
			default:
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể lưu lịch chạy cronjob"})
			}
			return
		}
		if !strings.HasPrefix(strings.TrimSpace(output), "UPDATED|"+remoteInstallation().ConfigDir+"/cron-recovery/schedule-"+task.ID+".") {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Không xác nhận được lịch chạy đã lưu"})
			return
		}
		invalidate("cron-jobs", "server-status")
		c.JSON(http.StatusOK, gin.H{"schedule": fmt.Sprintf("%s %s %s", req.Time[3:], req.Time[:2], strings.Join(strings.Fields(req.ExpectedSchedule)[2:], " "))})
	})

	auth.DELETE("/cron-jobs/:id", func(c *gin.Context) {
		task, ok := managedCronTaskByID(c.Param("id"))
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "Cronjob không hợp lệ"})
			return
		}
		var req struct {
			ExpectedSchedule string `json:"expectedSchedule"`
			ExpectedEnabled  *bool  `json:"expectedEnabled"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.ExpectedEnabled == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Dữ liệu không hợp lệ"})
			return
		}
		command, valid := commandForCronDeletion(task, req.ExpectedSchedule, *req.ExpectedEnabled)
		if !valid {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Lịch hiện tại không hợp lệ"})
			return
		}
		output, err := executeSSHCommand(command)
		// Refresh even after an uncertain SSH outcome: installation may have
		// completed before the connection failed.
		invalidate("cron-jobs", "server-status", "backup-status")
		if err != nil {
			switch {
			case strings.Contains(err.Error(), "CRON_NOT_SCHEDULED"):
				c.JSON(http.StatusConflict, gin.H{"error": "Cronjob không còn trong lịch chạy; hãy làm mới danh sách"})
			case strings.Contains(err.Error(), "CRON_DUPLICATE"):
				c.JSON(http.StatusConflict, gin.H{"error": "Có dòng cron cùng loại và cùng giờ/chu kỳ; hãy chọn lịch khác hoặc xử lý dòng trùng"})
			case strings.Contains(err.Error(), "CRON_SCHEDULE_CHANGED"):
				c.JSON(http.StatusConflict, gin.H{"error": "Lịch chạy đã thay đổi; hãy đóng xác nhận, làm mới danh sách và thử lại"})
			case strings.Contains(err.Error(), "CRON_ALREADY_RUNNING"):
				c.JSON(http.StatusConflict, gin.H{"error": "Tác vụ đang chạy; hãy chờ kết thúc rồi xóa lịch"})
			default:
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể xóa lịch cron; không xóa script, log hay backup"})
			}
			return
		}
		backupPath, ok := strings.CutPrefix(strings.TrimSpace(output), "DELETED|")
		if !ok || !strings.HasPrefix(backupPath, remoteInstallation().ConfigDir+"/cron-recovery/delete-"+task.ID+".") || !strings.HasSuffix(backupPath, "/crontab.txt") {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Không xác nhận được kết quả xóa; hãy làm mới danh sách trước khi thử lại"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "deleted", "backupPath": backupPath})
	})

	auth.PUT("/cron-jobs/:id/state", func(c *gin.Context) {
		task, ok := managedCronTaskByID(c.Param("id"))
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "Cronjob không hợp lệ"})
			return
		}
		var req struct {
			Enabled          *bool  `json:"enabled"`
			ExpectedSchedule string `json:"expectedSchedule"`
			ExpectedEnabled  *bool  `json:"expectedEnabled"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.Enabled == nil || req.ExpectedEnabled == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Dữ liệu không hợp lệ"})
			return
		}
		command, valid := commandForCronStateUpdate(task, req.ExpectedSchedule, *req.ExpectedEnabled, *req.Enabled)
		if !valid {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Lịch hiện tại không hợp lệ"})
			return
		}
		output, err := executeSSHCommand(command)
		invalidate("cron-jobs", "server-status", "backup-status")
		if err != nil {
			switch {
			case strings.Contains(err.Error(), "CRON_NOT_SCHEDULED"):
				c.JSON(http.StatusConflict, gin.H{"error": "Cronjob không còn trong danh sách; hãy làm mới"})
			case strings.Contains(err.Error(), "CRON_DUPLICATE"):
				c.JSON(http.StatusConflict, gin.H{"error": "Có dòng cron cùng loại và cùng giờ/chu kỳ; hãy chọn lịch khác hoặc xử lý dòng trùng"})
			case strings.Contains(err.Error(), "CRON_SCHEDULE_CHANGED"):
				c.JSON(http.StatusConflict, gin.H{"error": "Lịch hoặc trạng thái đã thay đổi; hãy đóng xác nhận và làm mới danh sách"})
			case strings.Contains(err.Error(), "CRON_ALREADY_RUNNING"):
				c.JSON(http.StatusConflict, gin.H{"error": "Tác vụ đang chạy; hãy chờ kết thúc rồi đổi trạng thái"})
			default:
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể cập nhật trạng thái cron"})
			}
			return
		}
		operation := "pause"
		if *req.Enabled {
			operation = "enable"
		}
		backupPath, ok := strings.CutPrefix(strings.TrimSpace(output), "UPDATED|")
		if !ok || !strings.HasPrefix(backupPath, remoteInstallation().ConfigDir+"/cron-recovery/"+operation+"-"+task.ID+".") || !strings.HasSuffix(backupPath, "/crontab.txt") {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Không xác nhận được trạng thái đã lưu; hãy làm mới danh sách"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"enabled": *req.Enabled, "backupPath": backupPath})
	})
}
