package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"path"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"backup-monitor-backend/internal/backupops"

	"github.com/gin-gonic/gin"
)

type BackupSystemSettings = backupops.Settings

type backupSystemResponse struct {
	Config  BackupSystemSettings `json:"config"`
	Version string               `json:"version"`
}

var activeBackupSystem atomic.Value
var backupSystemLoaded atomic.Bool
var backupSystemMutation sync.RWMutex

func defaultBackupSystem() BackupSystemSettings {
	return backupops.DefaultSettings()
}

func backupSystemSettings() BackupSystemSettings {
	if config, ok := activeBackupSystem.Load().(BackupSystemSettings); ok {
		return config
	}
	return defaultBackupSystem()
}

func configuredScript(name string) string { return path.Join(backupSystemSettings().ScriptsDir, name) }
func configuredLog(name string) string    { return path.Join(backupSystemSettings().LogsDir, name) }

func snapshotDownloadCommand(relative string) (string, error) {
	if !validSnapshotPath(relative) {
		return "", fmt.Errorf("Đường dẫn không hợp lệ")
	}
	cfg := backupSystemSettings()
	remotePath := cfg.DriveRemote + ":" + cfg.DriveFolder + "/" + relative
	return "/usr/bin/rclone cat " + activityShellQuote(remotePath) + " --config /root/.config/rclone/rclone.conf", nil
}

// Paths are restricted to shell-safe characters by the remote validator. One
// pass avoids re-replacing a destination nested under a legacy directory.
func configuredBackupCommand(command string) string {
	cfg, defaults := backupSystemSettings(), defaultBackupSystem()
	if cfg == defaults && !strings.Contains(command, "/tmp/rclone_about") {
		return command
	}
	pairs := [][2]string{{defaults.ScriptsDir, cfg.ScriptsDir}, {defaults.BackupRoot, cfg.BackupRoot},
		{defaults.DriveRemote + ":" + defaults.DriveFolder, cfg.DriveRemote + ":" + cfg.DriveFolder},
		{defaults.DriveRemote + ":", cfg.DriveRemote + ":"}, {defaults.DriveHistoryLog, cfg.DriveHistoryLog}}
	if strings.Contains(command, "/tmp/rclone_about") {
		// Account quota is shared by folders on one remote, never by different remotes.
		// A new cache namespace also prevents reusing the old default-account cache.
		cachePrefix := fmt.Sprintf("/tmp/backup-monitor-rclone-about-%x", sha256.Sum256([]byte(cfg.DriveRemote)))
		pairs = append(pairs, [2]string{"/tmp/rclone_about", cachePrefix})
	}
	for _, name := range backupops.LogNames {
		if cfg.LogsDir != defaults.LogsDir {
			pairs = append(pairs, [2]string{"/root/" + name, path.Join(cfg.LogsDir, name)})
		}
	}
	mapping := map[string]string{}
	for _, pair := range pairs {
		if pair[0] == pair[1] {
			continue
		}
		mapping[pair[0]] = pair[1]
		if escaped := strings.ReplaceAll(pair[0], "/", `\/`); escaped != pair[0] {
			mapping[escaped] = strings.ReplaceAll(regexp.QuoteMeta(pair[1]), "/", `\/`)
		}
	}
	// Protect already-configured prefixes, including paths nested below an old
	// prefix. Commands can pass through both a generator and the SSH executor.
	for _, value := range mapping {
		mapping[value] = value
	}
	keys := make([]string, 0, len(mapping))
	for key := range mapping {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	if len(keys) == 0 {
		return command
	}
	for i, key := range keys {
		keys[i] = regexp.QuoteMeta(key)
	}
	return regexp.MustCompile(strings.Join(keys, "|")).ReplaceAllStringFunc(command, func(value string) string { return mapping[value] })
}

func backupSystemOperation(operation string, fields map[string]interface{}) ([]byte, error) {
	if fields == nil {
		fields = map[string]interface{}{}
	}
	fields["operation"] = operation
	payload, err := json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	command := fmt.Sprintf("printf '%%s' '%s' | base64 -d | '%s' config", encodeForShell(string(payload)), backupops.BinaryPath)
	var out string
	if operation == "script-read" || operation == "script-save" {
		// Script text can contain credentials: use SSH stdin, never argv.
		out, err = executeSSHCommandInputRaw("'"+backupops.BinaryPath+"' config", payload)
	} else {
		out, err = executeSSHCommandRaw(command)
	}
	if err != nil {
		return nil, fmt.Errorf("Không thể hoàn tất thao tác cấu hình trên máy chủ; hãy kiểm tra đường dẫn hoặc thử lại")
	}
	var result map[string]interface{}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		return nil, fmt.Errorf("Phản hồi cấu hình không hợp lệ")
	}
	if message, ok := result["error"].(string); ok {
		return nil, fmt.Errorf("%s", message)
	}
	return []byte(out), nil
}

func acceptBackupSystemConfig(data []byte) error {
	var response backupSystemResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return err
	}
	if response.Config.BackupRoot == "" || response.Config.ScriptsDir == "" || response.Version == "" {
		return fmt.Errorf("cấu hình máy chủ không hợp lệ")
	}
	activeBackupSystem.Store(response.Config)
	backupSystemLoaded.Store(true)
	return nil
}

func initializeBackupSystem() {
	data, err := backupSystemOperation("get", nil)
	if err == nil {
		err = acceptBackupSystemConfig(data)
	}
	if err != nil {
		log.Print("Backup system configuration unavailable; remote mutations are disabled until configuration loads")
	} else if current := backupSystemSettings(); current.DriveRemote != defaultBackupSystem().DriveRemote || current.DriveFolder != defaultBackupSystem().DriveFolder {
		// The cache predates configurable destinations; never display the old
		// destination's counters as a fallback for a different Drive folder.
		clearDriveStatusSnapshot()
	}
}

func registerBackupSystemRoutes(auth *gin.RouterGroup) {
	auth.GET("/backup-system", func(c *gin.Context) {
		data, err := backupSystemOperation("get", nil)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		c.Header("Cache-Control", "no-store")
		if !backupSystemLoaded.Load() {
			_ = acceptBackupSystemConfig(data)
		}
		c.Data(http.StatusOK, "application/json", data)
	})
	for _, route := range []struct{ URL, Operation string }{
		{"/backup-system/directories/list", "list"}, {"/backup-system/directories/create", "create"},
		{"/backup-system/preview", "preview"}, {"/backup-system/apply", "apply"},
		{"/backup-system/scripts/read", "script-read"}, {"/backup-system/scripts/save", "script-save"},
	} {
		operation := route.Operation
		auth.POST(route.URL, func(c *gin.Context) {
			c.Header("Cache-Control", "no-store")
			limit := int64(32768)
			if operation == "script-save" {
				limit = 1024 * 1024
			}
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
			var fields map[string]interface{}
			if err := c.ShouldBindJSON(&fields); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Dữ liệu cấu hình không hợp lệ"})
				return
			}
			if operation == "apply" || operation == "script-save" {
				backupSystemMutation.Lock()
				defer backupSystemMutation.Unlock()
			}
			data, err := backupSystemOperation(operation, fields)
			if err != nil {
				c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
				return
			}
			if operation == "apply" {
				previous := backupSystemSettings()
				if err := acceptBackupSystemConfig(data); err != nil {
					c.JSON(http.StatusBadGateway, gin.H{"error": "Đã gửi cấu hình nhưng chưa xác nhận được trạng thái; hãy tải lại"})
					return
				}
				invalidate("config", "cron-jobs", "server-status", "backup-status", "local-snapshots", "recovery-snapshots")
				current := backupSystemSettings()
				if previous.DriveRemote != current.DriveRemote || previous.DriveFolder != current.DriveFolder {
					clearDriveStatusSnapshot()
				}
			}
			if operation == "script-save" {
				invalidate("cron-jobs")
			}
			c.Data(http.StatusOK, "application/json", data)
		})
	}
}
