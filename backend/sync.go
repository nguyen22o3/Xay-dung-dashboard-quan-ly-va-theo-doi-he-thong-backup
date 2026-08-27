package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// drivelFileItem matches one entry of `rclone lsjson`.
type drivelFileItem struct {
	Name  string `json:"Name"`
	Path  string `json:"Path"`
	IsDir bool   `json:"IsDir"`
	Size  int64  `json:"Size"`
}

// sourceByPrefix maps known file-name prefixes to their display source name.
var sourceByPrefix = map[string]string{
	"web_code_":      "Server Web LAMP",
	"system_config_": "System Config (/etc)",
}

// inferSource tries to determine the backup source from a file name prefix.
func inferSource(fileName string) string {
	lower := strings.ToLower(fileName)
	for prefix, source := range sourceByPrefix {
		if strings.HasPrefix(lower, prefix) {
			return source
		}
	}
	return "Google Drive"
}

// driveRemote returns the configured drive remote (or default).
func driveRemote() string {
	if r := os.Getenv("DRIVE_REMOTE"); r != "" {
		return r
	}
	return "gdrive:Backup_Dashboard"
}

// listDriveFiles runs `rclone lsjson` on the backup folder and returns non-dir files.
func listDriveFiles() ([]drivelFileItem, error) {
	cmd := exec.Command("rclone", "lsjson", "--recursive", driveRemote())
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("rclone lsjson thất bại: %v (%s)", err, strings.TrimSpace(string(out)))
	}

	var items []drivelFileItem
	if err := json.Unmarshal(out, &items); err != nil {
		return nil, fmt.Errorf("không parse được output rclone: %v", err)
	}

	// Chỉ giữ file backup (đuôi .tar.gz)
	var files []drivelFileItem
	for _, it := range items {
		if !it.IsDir && strings.HasSuffix(strings.ToLower(it.Name), ".tar.gz") {
			files = append(files, it)
		}
	}
	return files, nil
}

// syncDriveToDB writes new backups found on Drive into the database.
// Trả về số record mới đã thêm.
func syncDriveToDB() (int, error) {
	files, err := listDriveFiles()
	if err != nil {
		return 0, err
	}

	// Tập hợp tên file đã tồn tại trong DB.
	var existing []string
	DB.Model(&BackupRecord{}).Pluck("file_name", &existing)
	known := make(map[string]bool, len(existing))
	for _, n := range existing {
		known[n] = true
	}

	// Sắp xếp theo tên (mặc định tên có timestamp nên tăng dần theo thời gian).
	sort.Slice(files, func(i, j int) bool {
		return files[i].Name < files[j].Name
	})

	added := 0
	for _, f := range files {
		if known[f.Name] {
			continue
		}

		filePath := strings.TrimRight(driveRemote(), "/") + "/" + f.Name
		rec := BackupRecord{
			Source:      inferSource(f.Name),
			Destination: "Google Drive",
			FileName:    f.Name,
			FilePath:    filePath,
			Status:      "Success",
			SizeMB:      float64(f.Size) / (1024 * 1024),
			CreatedAt:   time.Now(),
		}
		if err := DB.Create(&rec).Error; err != nil {
			log.Printf("Lỗi khi lưu %s: %v", f.Name, err)
			continue
		}
		added++
		log.Printf("Phát hiện backup mới từ Drive: %s (%s, %.1f MB)", f.Name, rec.Source, rec.SizeMB)
	}

	return added, nil
}

// startDriveSyncRunner chạy quét Drive định kỳ trong một goroutine.
func startDriveSyncRunner() {
	interval := 30 * time.Second
	if v := os.Getenv("DRIVE_SYNC_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			interval = d
		}
	}

	go func() {
		for {
			added, err := syncDriveToDB()
			if err != nil {
				log.Printf("Lỗi đồng bộ Google Drive: %v", err)
			} else if added > 0 {
				fmt.Printf("Đã thêm %d backup mới từ Google Drive.\n", added)
			}
			time.Sleep(interval)
		}
	}()
}
