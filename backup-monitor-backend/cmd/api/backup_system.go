package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"path"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"backup-monitor-backend/internal/backupops"

	"github.com/gin-gonic/gin"
)

type BackupSystemSettings = backupops.Settings

type backupSystemResponse struct {
	Config       BackupSystemSettings         `json:"config"`
	Version      string                       `json:"version"`
	Installation *backupops.InstallationPaths `json:"installation,omitempty"`
}

var activeBackupSystem atomic.Value
var activeInstallation atomic.Value
var backupSystemLoaded atomic.Bool
var backupSystemMutation sync.RWMutex

// Accessed under backupSystemMutation by HTTP handlers.
var lastBackupSystemRead time.Time

func storageReadRoute(route string) bool {
	return route == "/api/server-status" || route == "/api/backup-status" || route == "/api/local-snapshots" || route == "/api/recovery-snapshots" || route == "/api/cron-jobs" || strings.HasPrefix(route, "/api/cron-jobs/")
}

func refreshBackupSystemIfDue(read func() ([]byte, error), now time.Time) error {
	if backupSystemLoaded.Load() && !lastBackupSystemRead.IsZero() && now.Sub(lastBackupSystemRead) < 30*time.Second {
		return nil
	}
	data, err := read()
	if err != nil {
		return err
	}
	return acceptBackupSystemConfig(data)
}

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

func remoteInstallation() backupops.InstallationPaths {
	if current, ok := activeInstallation.Load().(backupops.InstallationPaths); ok {
		return current
	}
	return backupops.LegacyInstallation()
}

func configuredRuntimeCommand(command string) string {
	return backupops.Rewrite(command, backupops.InstallationPairs(backupops.LegacyInstallation(), remoteInstallation()))
}

// These paths belong to the Linux host, even when the API runs on Windows.
// CronSettings is also used by native helper filesystem operations/tests, so
// retain its validation/options but explicitly join remote paths with POSIX path.
func remoteCronSettings(cfg BackupSystemSettings, id, key string) (BackupSystemSettings, error) {
	local, err := backupops.CronSettings(cfg, id, key)
	if err != nil || key == "" {
		return local, err
	}
	loc := cfg.CronLocations[key]
	if loc.FileLayout == "files" {
		local.ScriptsDir, local.LogsDir = loc.ScriptsDir, loc.LogsDir
	} else {
		local.ScriptsDir = path.Join(loc.ScriptsDir, "cron-"+key)
		local.LogsDir = path.Join(loc.LogsDir, "cron-"+key)
	}
	if loc.Options != nil {
		local.DriveHistoryLog = path.Join(local.LogsDir, backupops.CronFileName(local, "aapanel_backup.log"))
	}
	return local, nil
}

func snapshotDownloadCommand(relative string) (string, error) {
	if !validSnapshotPath(relative) {
		return "", fmt.Errorf("Đường dẫn không hợp lệ")
	}
	cfg := backupSystemSettings()
	remotePath := cfg.DriveRemote + ":" + cfg.DriveFolder + "/" + relative
	return "/usr/bin/rclone cat " + activityShellQuote(remotePath) + " --config " + activityShellQuote(remoteInstallation().RcloneConfig), nil
}

// Paths are restricted to shell-safe characters by the remote validator. One
// pass avoids re-replacing a destination nested under a legacy directory.
func configuredBackupCommand(command string) string {
	cfg, defaults := backupSystemSettings(), defaultBackupSystem()
	if reflect.DeepEqual(cfg, defaults) && remoteInstallation().DataDir == "" && !strings.Contains(command, "/tmp/rclone_about") {
		return command
	}
	pairs := [][2]string{{defaults.ScriptsDir, cfg.ScriptsDir}, {defaults.BackupRoot, cfg.BackupRoot},
		{defaults.DriveRemote + ":" + defaults.DriveFolder, cfg.DriveRemote + ":" + cfg.DriveFolder},
		{defaults.DriveRemote + ":", cfg.DriveRemote + ":"}, {defaults.DriveHistoryLog, cfg.DriveHistoryLog}}
	pairs = append(pairs, backupops.InstallationPairs(backupops.LegacyInstallation(), remoteInstallation())...)
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
	for key, loc := range cfg.CronLocations {
		local, err := remoteCronSettings(cfg, loc.ID, key)
		if err != nil {
			continue
		}
		for _, value := range []string{local.ScriptsDir, local.LogsDir} {
			mapping[value] = value
			escaped := strings.ReplaceAll(value, "/", `\/`)
			mapping[escaped] = escaped
		}
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
	// Configuration may contain OAuth tokens or script credentials. Never put
	// payloads in process argv, including on the local-agent transport.
	timeout := 60 * time.Second
	if operation == "preview" || operation == "apply" {
		timeout = 10 * time.Minute
	}
	out, err := executeSSHCommandInputRawTimeout("'"+backupops.BinaryPath+"' config", payload, timeout)
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
	installation := backupops.LegacyInstallation()
	if response.Installation != nil && response.Installation.DataDir != "" {
		if backupops.ValidateInstallationDirectory(response.Installation.DataDir) != nil {
			return fmt.Errorf("vị trí cài đặt máy chủ không hợp lệ")
		}
		installation = backupops.InstallationAt(response.Installation.DataDir)
		if !reflect.DeepEqual(installation, *response.Installation) {
			return fmt.Errorf("đường dẫn cài đặt máy chủ không nhất quán")
		}
	}
	previous := backupSystemSettings()
	changed := !backupSystemLoaded.Load() || !reflect.DeepEqual(previous, response.Config) || !reflect.DeepEqual(remoteInstallation(), installation)
	activeInstallation.Store(installation)
	activeBackupSystem.Store(response.Config)
	backupSystemLoaded.Store(true)
	lastBackupSystemRead = time.Now()
	if changed {
		invalidate("config", "cron-jobs", "server-status", "backup-status", "local-snapshots", "recovery-snapshots")
		if previous.DriveRemote != response.Config.DriveRemote || previous.DriveFolder != response.Config.DriveFolder {
			clearDriveStatusSnapshot()
		}
	}
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
	auth.GET("/backup-system/drive/status", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		data, err := backupSystemOperation("drive-status", nil)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		c.Data(http.StatusOK, "application/json", data)
	})
	auth.GET("/backup-system", func(c *gin.Context) {
		backupSystemMutation.Lock()
		defer backupSystemMutation.Unlock()
		data, err := backupSystemOperation("get", nil)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		c.Header("Cache-Control", "no-store")
		if err := acceptBackupSystemConfig(data); err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "Cấu hình máy chủ không hợp lệ"})
			return
		}
		c.Data(http.StatusOK, "application/json", data)
	})
	for _, route := range []struct{ URL, Operation string }{
		{"/backup-system/directories/list", "list"}, {"/backup-system/directories/create", "create"},
		{"/backup-system/preview", "preview"}, {"/backup-system/apply", "apply"},
		{"/backup-system/scripts/read", "script-read"}, {"/backup-system/scripts/save", "script-save"},
		{"/backup-system/scripts/options", "script-options"},
		{"/backup-system/drive/connect", "drive-connect"},
	} {
		operation := route.Operation
		auth.POST(route.URL, func(c *gin.Context) {
			c.Header("Cache-Control", "no-store")
			limit := int64(131072)
			if operation == "script-save" {
				limit = 1024 * 1024
			}
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
			var fields map[string]interface{}
			if err := c.ShouldBindJSON(&fields); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Dữ liệu cấu hình không hợp lệ"})
				return
			}
			if operation == "apply" || operation == "script-save" || operation == "drive-connect" {
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
			if operation == "drive-connect" {
				invalidate("backup-status", "config", "recovery-snapshots")
				clearDriveStatusSnapshot()
			}
			c.Data(http.StatusOK, "application/json", data)
		})
	}
}
