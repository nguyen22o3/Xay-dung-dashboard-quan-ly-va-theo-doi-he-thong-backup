package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// ===== SERVER, STATUS & SYSTEM ROUTE HANDLERS =====

func registerServerRoutes(auth *gin.RouterGroup) {
	// API 1: Lấy thông số hệ thống máy chủ (CPU, RAM, Disk)
	auth.GET("/server-status", func(c *gin.Context) {
		output, err := readServerStatus()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể lấy thông số máy chủ", "detail": err.Error()})
			return
		}

		c.Data(http.StatusOK, "application/json", output)
	})

	// API 2: Kiểm tra các file Backup trên Google Drive và Dung lượng
	auth.GET("/backup-status", func(c *gin.Context) {
		output, err := readBackupStatus()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể đọc trạng thái sao lưu"})
			return
		}

		c.Data(http.StatusOK, "application/json", output)
	})

	// API 4: Giám sát Uptime các website (động theo thư mục /www/wwwroot)
	auth.GET("/websites-status", func(c *gin.Context) {
		output, err := cachedGet("websites-status", 15*time.Second, func() ([]byte, error) {
			return collectWebsiteStatuses(executeSSHCommandRaw)
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể kiểm tra website", "detail": err.Error()})
			return
		}

		c.Data(http.StatusOK, "application/json", output)
	})

	// API 5: Đọc Log hệ thống Real-time
	auth.GET("/logs", func(c *gin.Context) {
		logType := c.Query("type")
		cmd := ""

		switch logType {
		case "backup":
			// Kết hợp: Log cron + Chi tiết file backup trên Google Drive
			cmd = `
echo "══════════════════════════════════════════════════════"
echo "  LỊCH SỬ CHẠY SCRIPT (Cron Execution Log)"
echo "══════════════════════════════════════════════════════"
tail -n 15 /root/backup-site.log /root/backup-database.log /root/auto_backup.log /root/cleanup-panel-backups.log /root/check_integrity.log /www/server/cron/*.log 2>/dev/null
echo ""
echo "══════════════════════════════════════════════════════"
echo "  CHI TIẾT BẢN SAO LƯU TRÊN GOOGLE DRIVE"
echo "══════════════════════════════════════════════════════"
for DAY in $(/usr/bin/rclone lsf --dirs-only gdrive:Backup --config /root/.config/rclone/rclone.conf --timeout 10s --contimeout 5s 2>/dev/null | sort | tail -n 3); do
  DAYNAME=$(echo "$DAY" | tr -d '/')
  echo ""
  echo "📅 [$DAYNAME]"
  echo "  📁 site/"
  /usr/bin/rclone lsf -R --format "ps" gdrive:Backup/$DAYNAME/site/ --config /root/.config/rclone/rclone.conf --timeout 10s --contimeout 5s 2>/dev/null | awk -F';' '{printf "     %-50s %s\n", $1, $2" bytes"}'
  echo "  📁 database/"
  /usr/bin/rclone lsf -R --format "ps" gdrive:Backup/$DAYNAME/database/ --config /root/.config/rclone/rclone.conf --timeout 10s --contimeout 5s 2>/dev/null | awk -F';' '{printf "     %-50s %s\n", $1, $2" bytes"}'
done
`
		case "system":
			cmd = "tail -n 50 /var/log/messages"
		case "secure":
			cmd = "tail -n 50 /var/log/secure"
		default:
			cmd = "tail -n 50 /var/log/messages"
		}
		if logType == "backup" {
			cmd = strings.Replace(cmd, "tail -n 15 /root/backup-site.log /root/backup-database.log /root/auto_backup.log /root/cleanup-panel-backups.log /root/check_integrity.log", "tail -n 15 "+managedLogArguments(), 1)
		}

		key := "logs-" + logType
		ttl := 10 * time.Second
		if logType == "backup" {
			ttl = 30 * time.Second
		}
		output, err := cachedGet(key, ttl, func() ([]byte, error) {
			out, e := executeSSHCommand(cmd)
			return []byte(out), e
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể đọc log", "detail": err.Error()})
			return
		}

		c.String(http.StatusOK, string(output))
	})

	// API 7: Đọc cấu hình hệ thống (lưu trên máy ảo)
	auth.GET("/config", func(c *gin.Context) {
		cmds := `
CONFIG=/root/backup-monitor/config.json
if [ -f "$CONFIG" ]; then
  cat "$CONFIG"
else
  echo '{"retention_days":7,"notify_enabled":false,"notify_token":"","notify_chat_id":"","email_recipient":""}'
fi
`
		output, err := cachedGet("config", 10*time.Second, func() ([]byte, error) {
			out, e := executeSSHCommand(cmds)
			return []byte(out), e
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể đọc cấu hình", "detail": err.Error()})
			return
		}
		c.Data(http.StatusOK, "application/json", output)
	})

	// API 8: Ghi cấu hình hệ thống (validate JSON rồi base64 truyền qua SSH)
	auth.PUT("/config", func(c *gin.Context) {
		payload, err := c.GetRawData()
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Body không hợp lệ"})
			return
		}
		var cfg map[string]interface{}
		if err := json.Unmarshal(payload, &cfg); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "JSON không hợp lệ"})
			return
		}
		// Chỉ chấp nhận các trường đã biết
		allowed := map[string]bool{
			"retention_days":  true,
			"notify_enabled":  true,
			"notify_token":    true,
			"notify_chat_id":  true,
			"email_recipient": true,
		}
		clean := map[string]interface{}{}
		for k, v := range cfg {
			if allowed[k] {
				clean[k] = v
			}
		}
		normalized, _ := json.Marshal(clean)
		b64 := base64.StdEncoding.EncodeToString(normalized)

		cmds := fmt.Sprintf(`mkdir -p /root/backup-monitor && echo %s | base64 -d > /root/backup-monitor/config.json && echo OK`, b64)
		output, err := executeSSHCommand(cmds)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể ghi cấu hình", "detail": err.Error()})
			return
		}
		invalidate("config")
		c.JSON(http.StatusOK, gin.H{"status": "success", "message": strings.TrimSpace(output), "config": clean})
	})

	auth.POST("/refresh", func(c *gin.Context) {
		refreshReadCaches()
		c.JSON(http.StatusOK, gin.H{"status": "success", "message": "Cache cleared"})
	})

	auth.POST("/clear-log", func(c *gin.Context) {
		var req struct {
			Target  string   `json:"target"`
			Entries []string `json:"entries"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Lỗi tham số"})
			return
		}
		if req.Target != "drive" && req.Target != "server" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Target không hợp lệ"})
			return
		}
		for _, entry := range req.Entries {
			if !activityLogKeyPattern.MatchString(entry) || (req.Target == "drive" && strings.Count(entry, "|") != 1) {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Nhật ký được chọn không hợp lệ"})
				return
			}
		}
		if req.Target == "drive" {
			out, err := executeSSHCommandRaw(driveActivityClearCommand(req.Entries))
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể xóa nhật ký Drive; nếu cron đang chạy, hãy chờ tác vụ kết thúc", "detail": err.Error()})
				return
			}
			invalidate("backup-status")
			if len(req.Entries) > 0 && strings.Contains(out, "LOG_FILES_CHANGED=0\n") {
				c.JSON(http.StatusConflict, gin.H{"error": "Không tìm thấy nhật ký đã chọn. Hãy tải lại danh sách và thử lại."})
				return
			}
			c.JSON(http.StatusOK, gin.H{"message": "Đã xóa nhật ký upload Google Drive trên máy chủ; các tệp backup được giữ nguyên"})
			return
		}
		out, err := executeSSHCommandRaw(serverActivityClearCommand(req.Entries))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể xóa nhật ký máy chủ", "detail": err.Error()})
			return
		}
		invalidate("backup-status", "server-status")
		if len(req.Entries) > 0 && strings.Contains(out, "LOG_FILES_CHANGED=0\n") {
			c.JSON(http.StatusConflict, gin.H{"error": "Không tìm thấy nhật ký đã chọn trên máy chủ. Hãy tải lại danh sách và thử lại."})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "Đã xóa nhật ký trên máy chủ"})
	})
}
