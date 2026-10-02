package main

import (
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var (
	driveGoodMu     sync.Mutex
	driveGoodStatus []byte
)

type driveUnavailableError struct{ message string }

func (e driveUnavailableError) Error() string { return e.message }

func driveStatusCachePath() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "backup-monitor", "drive-status.json")
}

func readGoodDriveStatus(path string) []byte {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var status struct {
		DriveError string          `json:"driveError"`
		Size       json.RawMessage `json:"size"`
		History    json.RawMessage `json:"history"`
	}
	if json.Unmarshal(data, &status) != nil || status.DriveError != "" || len(status.Size) == 0 || string(status.Size) == "null" || len(status.History) == 0 || string(status.History) == "null" {
		return nil
	}
	return data
}

func markDriveStatusStale(good []byte) []byte {
	var status map[string]json.RawMessage
	if json.Unmarshal(good, &status) != nil {
		return nil
	}
	if len(status["size"]) == 0 || string(status["size"]) == "null" || len(status["history"]) == 0 || string(status["history"]) == "null" {
		return nil
	}
	status["driveStale"] = json.RawMessage("true")
	data, err := json.Marshal(status)
	if err != nil {
		return nil
	}
	return data
}

// The Drive listing is separate from the local backup logs. A failed Drive
// refresh must not replace the last confirmed Drive counts and history with 0.
func mergeDriveStatus(raw, good []byte, fetchedAt time.Time) (response, newGood []byte, err error) {
	var status map[string]json.RawMessage
	if err := json.Unmarshal(raw, &status); err != nil {
		return nil, nil, err
	}
	var driveError string
	if field := status["driveError"]; len(field) > 0 && json.Unmarshal(field, &driveError) != nil {
		return nil, nil, errors.New("invalid Drive error response")
	}
	if driveError != "" {
		if len(good) > 0 {
			var previous map[string]json.RawMessage
			if json.Unmarshal(good, &previous) == nil {
				for _, field := range []string{"about", "size", "dirs", "totalFolders", "history", "todayBreakdown", "todayBreakdownBytes", "driveDataAt"} {
					if value, ok := previous[field]; ok {
						status[field] = value
					}
				}
				status["driveStale"] = json.RawMessage("true")
				response, err = json.Marshal(status)
				return response, nil, err
			}
		}
		return nil, nil, driveUnavailableError{message: driveError}
	}
	if len(status["size"]) == 0 || string(status["size"]) == "null" || len(status["history"]) == 0 || string(status["history"]) == "null" {
		return nil, nil, errors.New("Drive listing has no summary")
	}
	stamp, _ := json.Marshal(fetchedAt.UTC().Format(time.RFC3339))
	status["driveDataAt"] = stamp
	status["driveStale"] = json.RawMessage("false")
	response, err = json.Marshal(status)
	return response, response, err
}

func backupStatusTTL(data []byte) time.Duration {
	var status struct {
		DriveError string `json:"driveError"`
	}
	if json.Unmarshal(data, &status) == nil && status.DriveError != "" {
		if strings.Contains(status.DriveError, "giới hạn truy vấn") {
			return 5 * time.Minute
		}
		return 30 * time.Second
	}
	return 2 * time.Minute
}

func currentGoodDriveStatus() []byte {
	driveGoodMu.Lock()
	defer driveGoodMu.Unlock()
	return driveGoodStatus
}

func rememberGoodDriveStatus(data []byte) {
	driveGoodMu.Lock()
	driveGoodStatus = data
	driveGoodMu.Unlock()
	path := driveStatusCachePath()
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		log.Printf("cannot create Drive status cache directory: %v", err)
		return
	}
	if err := writeFileAtomic0600(path, data); err != nil {
		log.Printf("cannot save Drive status cache: %v", err)
	}
}

func warmDriveStatusCache() {
	good := readGoodDriveStatus(driveStatusCachePath())
	if len(good) == 0 {
		return
	}
	driveGoodMu.Lock()
	driveGoodStatus = good
	driveGoodMu.Unlock()
	stale := markDriveStatusStale(good)
	if len(stale) == 0 {
		return
	}
	cacheMu.Lock()
	if _, exists := cache["backup-status"]; !exists {
		cache["backup-status"] = cacheEntry{value: stale, expiresAt: time.Now().Add(-time.Second)}
	}
	cacheMu.Unlock()
}

func clearDriveStatusSnapshot() {
	driveGoodMu.Lock()
	driveGoodStatus = nil
	driveGoodMu.Unlock()
	if cachePath := driveStatusCachePath(); cachePath != "" {
		// Only the app-owned cached summary is removed, never backup data.
		_ = os.Remove(cachePath)
	}
}
