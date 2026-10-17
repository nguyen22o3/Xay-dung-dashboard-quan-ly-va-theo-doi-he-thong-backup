package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const localInventoryTTL = 2 * time.Second
const driveInventoryTTL = 10 * time.Second

var driveDateFolderPattern = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)

// One listing supplies both the dashboard counters and the recovery file picker.
// Cache failures as values as well, so opening both views cannot bypass Drive's
// retry/quota backoff. Listing remains the last confirmed listing on failure.
type driveInventory struct {
	Summary    json.RawMessage `json:"summary"`
	Listing    string          `json:"listing"`
	HasListing bool            `json:"hasListing"`
}

func (d driveInventory) driveError() string {
	var status struct {
		Error string `json:"driveError"`
	}
	_ = json.Unmarshal(d.Summary, &status)
	return status.Error
}

const driveInventoryReadCommand = `
TODAY=$(date +"%Y-%m-%d")
LSF_OUT=$(timeout -k 5s 40s /usr/bin/rclone lsf -R --format "pst" gdrive:Backup --config /root/.config/rclone/rclone.conf --log-level ERROR --fast-list --timeout 15s --contimeout 5s 2>&1)
LSF_STATUS=$?
ABOUT=""
ABOUT_CACHE=/tmp/rclone_about.json
if [ -f "$ABOUT_CACHE" ] && [ ! -L "$ABOUT_CACHE" ]; then
  CACHE_TIME=$(stat -c '%Y' "$ABOUT_CACHE" 2>/dev/null || printf '0')
  CACHE_AGE=$(( $(date +%s) - CACHE_TIME ))
  if [ "$CACHE_AGE" -ge 0 ] && [ "$CACHE_AGE" -lt 86400 ]; then
    ABOUT=$(cat "$ABOUT_CACHE")
  fi
fi
if [ "$LSF_STATUS" -eq 0 ] && [ -z "$ABOUT" ]; then
  ABOUT=$(timeout -k 5s 10s /usr/bin/rclone about gdrive: --config /root/.config/rclone/rclone.conf --log-level ERROR --fast-list --json 2>/dev/null)
  if [ -n "$ABOUT" ]; then
    ABOUT_TMP=$(mktemp /tmp/rclone_about.XXXXXXXX)
    printf '%s\n' "$ABOUT" > "$ABOUT_TMP"
    mv -f -- "$ABOUT_TMP" "$ABOUT_CACHE"
  fi
fi
if [ -z "$ABOUT" ] && [ -f "$ABOUT_CACHE" ] && [ ! -L "$ABOUT_CACHE" ]; then
  ABOUT=$(cat "$ABOUT_CACHE")
fi
if [ -z "$ABOUT" ]; then ABOUT="{}"; fi
printf '%s\n%s\n__DRIVE_ABOUT__\n%s\n__DRIVE_LISTING__\n%s' "$TODAY" "$LSF_STATUS" "$ABOUT" "$LSF_OUT"
`

type driveHistoryDay struct {
	Date  string `json:"date"`
	Bytes int64  `json:"bytes"`
	Files int64  `json:"files"`
}

// Aggregate the exact same path/size fields that the previous awk reader used.
// The third lsf field is retained untouched for recovery-snapshots.
func parseDriveInventory(output string, fetchedAt time.Time) (driveInventory, error) {
	meta, listing, ok := strings.Cut(output, "\n__DRIVE_LISTING__\n")
	if !ok {
		return driveInventory{}, fmt.Errorf("Thiếu danh sách Google Drive")
	}
	header, about, ok := strings.Cut(meta, "\n__DRIVE_ABOUT__\n")
	fields := strings.Split(header, "\n")
	if !ok || len(fields) != 2 || !json.Valid([]byte(about)) {
		return driveInventory{}, fmt.Errorf("Thông số Google Drive không hợp lệ")
	}
	exitCode, err := strconv.Atoi(fields[1])
	if err != nil {
		return driveInventory{}, err
	}
	driveError := ""
	if exitCode != 0 {
		lower := strings.ToLower(listing)
		if strings.Contains(lower, "ratelimitexceeded") || strings.Contains(lower, "quota") && strings.Contains(lower, "exceeded") || strings.Contains(lower, "rate") && strings.Contains(lower, "limit") && strings.Contains(lower, "exceeded") {
			driveError = "Google Drive API đang giới hạn truy vấn; cần kiểm tra quota và cấu hình client ID riêng cho rclone"
		} else {
			driveError = fmt.Sprintf("rclone mã lỗi %d; kiểm tra DNS, mạng và cấu hình rclone trên máy chủ", exitCode)
		}
		listing = ""
	}
	days := map[string]*driveHistoryDay{}
	todayDirs := map[string]bool{}
	todayCounts := map[string]int64{"site": 0, "database": 0, "panel": 0}
	todayBytes := map[string]int64{"site": 0, "database": 0, "panel": 0}
	var totalBytes, totalFiles int64
	for _, line := range strings.Split(listing, "\n") {
		parts := strings.SplitN(line, ";", 3)
		if len(parts) < 2 {
			continue
		}
		size, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil || size < 0 {
			continue
		}
		totalBytes += size
		totalFiles++
		path := strings.Split(parts[0], "/")
		day := path[0]
		if !driveDateFolderPattern.MatchString(day) {
			continue
		}
		if days[day] == nil {
			days[day] = &driveHistoryDay{Date: day}
		}
		days[day].Bytes += size
		days[day].Files++
		if day == fields[0] && len(path) >= 2 && path[1] != "" {
			todayDirs[path[1]] = true
			if _, known := todayCounts[path[1]]; known {
				todayCounts[path[1]]++
				todayBytes[path[1]] += size
			}
		}
	}
	history := make([]driveHistoryDay, 0, len(days))
	for _, day := range days {
		history = append(history, *day)
	}
	sort.Slice(history, func(i, j int) bool { return history[i].Date < history[j].Date })
	dirs := make([]string, 0, len(todayDirs))
	for dir := range todayDirs {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	dirsValue := strings.Join(dirs, ",")
	if len(dirs) > 0 {
		dirsValue += ","
	}
	status := map[string]interface{}{
		"about": json.RawMessage(about), "size": map[string]int64{"bytes": totalBytes, "count": totalFiles},
		"dirs": dirsValue, "totalFolders": len(days), "history": history,
		"driveError": driveError, "todayBreakdown": todayCounts, "todayBreakdownBytes": todayBytes,
	}
	if driveError != "" {
		status["size"] = nil
	} else {
		status["driveDataAt"] = fetchedAt.UTC().Format(time.RFC3339)
		status["driveStale"] = false
	}
	summary, err := json.Marshal(status)
	return driveInventory{Summary: summary, Listing: listing, HasListing: exitCode == 0}, err
}

func markDriveInventoryStale(data []byte) []byte {
	var inventory driveInventory
	if json.Unmarshal(data, &inventory) != nil {
		return nil
	}
	if stale := markDriveStatusStale(inventory.Summary); len(stale) > 0 {
		inventory.Summary = stale
	}
	marked, _ := json.Marshal(inventory)
	return marked
}

func readDriveInventory() (driveInventory, error) {
	// Capture the resolved destination before the background refresh is launched.
	command := configuredBackupCommand(driveInventoryReadCommand)
	data, err := cachedGetWithPolicy("drive-inventory", driveInventoryTTL, func() ([]byte, error) {
		output, err := executeSSHCommandRaw(command)
		if err != nil {
			return nil, err
		}
		inventory, err := parseDriveInventory(output, time.Now())
		if err != nil {
			return nil, err
		}
		if !inventory.HasListing {
			cacheMu.Lock()
			previous := cache["drive-inventory"].value
			cacheMu.Unlock()
			var good driveInventory
			if json.Unmarshal(previous, &good) == nil && good.HasListing {
				inventory.Listing, inventory.HasListing = good.Listing, true
			}
		}
		return json.Marshal(inventory)
	}, func(data []byte) time.Duration {
		var inventory driveInventory
		if json.Unmarshal(data, &inventory) != nil {
			return driveInventoryTTL
		}
		return backupStatusTTL(inventory.Summary)
	}, markDriveInventoryStale)
	var inventory driveInventory
	if err == nil {
		err = json.Unmarshal(data, &inventory)
	}
	return inventory, err
}

const localActivityReadCommand = `
ACTIVITY=$(__DRIVE_ACTIVITY_COMMAND__)
LOCAL_LOGS=$(awk '
    /start backup\[/ {
        match($0, /\[(.*)\]/, arr)
        dt = arr[1]
        split(dt, d, " ")
        date = d[1]
        time = d[2]
    }
    /Backup site: / {
        match($0, /site: (.*)/, arr)
        name = "Backup Website: " arr[1]
    }
    /Backup .*database: / {
        match($0, /database: (.*)/, arr)
        name = "Backup Database: " arr[1]
    }
    /Compression completed, took / {
        match($0, /took ([0-9.]*) seconds/, arr)
        duration = arr[1]
        status = "Successful"
        printf "{\"name\":\"%s\",\"date\":\"%s\",\"time\":\"%s\",\"duration\":\"%s\",\"status\":\"%s\"},", name, date, time, duration, status
    }
    /Database backup completed, taking / {
        match($0, /taking ([0-9.]*) seconds/, arr)
        duration = arr[1]
        status = "Successful"
        printf "{\"name\":\"%s\",\"date\":\"%s\",\"time\":\"%s\",\"duration\":\"%s\",\"status\":\"%s\"},", name, date, time, duration, status
    }
    ' /www/server/cron/*.log 2>/dev/null | sed 's/,$//')
CUSTOM_LOCAL_LOGS=$(__CUSTOM_LOCAL_ACTIVITY_COMMAND__)
RUN_LOGS=$(__BACKUP_RUN_ACTIVITY_COMMAND__)
PANEL_LOGS=$(__PANEL_ACTIVITY_COMMAND__)
PANEL_ARCHIVES=$(__PANEL_ARCHIVE_ACTIVITY_COMMAND__)
LOCAL_ACTIVITY="["
SEP=""
for ENTRIES in "$LOCAL_LOGS" "$CUSTOM_LOCAL_LOGS" "$RUN_LOGS" "$PANEL_LOGS" "$PANEL_ARCHIVES"; do
  if [ -n "$ENTRIES" ]; then LOCAL_ACTIVITY="$LOCAL_ACTIVITY$SEP$ENTRIES"; SEP=","; fi
done
LOCAL_ACTIVITY="$LOCAL_ACTIVITY]"
echo "{\"activity\": $ACTIVITY, \"localActivity\": $LOCAL_ACTIVITY}"
`

func readBackupActivity() ([]byte, error) {
	command := resolvedBackupStatusCommand(localActivityReadCommand)
	return cachedGetFresh("backup-activity", localInventoryTTL, func() ([]byte, error) {
		output, err := executeSSHCommandRaw(command)
		if err == nil && !json.Valid([]byte(output)) {
			err = fmt.Errorf("Nhật ký sao lưu không hợp lệ")
		}
		return []byte(output), err
	})
}

func readBackupStatus() ([]byte, error) {
	type result struct {
		data []byte
		err  error
	}
	activityResult := make(chan result, 1)
	go func() {
		data, err := readBackupActivity()
		activityResult <- result{data, err}
	}()
	var summary []byte
	cacheMu.Lock()
	_, hasInventory := cache["drive-inventory"]
	cacheMu.Unlock()
	good := currentGoodDriveStatus()
	if !hasInventory && len(good) > 0 {
		// Keep the persistent last-good summary immediately available at startup.
		go func() { _, _ = readDriveInventory() }()
		summary = markDriveStatusStale(good)
	} else {
		inventory, err := readDriveInventory()
		if err != nil {
			return nil, err
		}
		summary = inventory.Summary
		if inventory.driveError() != "" {
			var err error
			summary, _, err = mergeDriveStatus(summary, good, time.Now())
			if err != nil {
				return nil, err
			}
		} else if !bytes.Equal(summary, good) {
			var status struct {
				Stale bool `json:"driveStale"`
			}
			if json.Unmarshal(summary, &status) == nil && !status.Stale {
				rememberGoodDriveStatus(summary)
			}
		}
	}
	activity := <-activityResult
	if activity.err != nil {
		return nil, activity.err
	}
	var status, logs map[string]json.RawMessage
	if err := json.Unmarshal(summary, &status); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(activity.data, &logs); err != nil {
		return nil, err
	}
	status["activity"], status["localActivity"] = logs["activity"], logs["localActivity"]
	return json.Marshal(status)
}

func readLocalInventory() ([]byte, error) {
	command := configuredLocalInventoryCommand()
	return cachedGetFresh("local-snapshots", localInventoryTTL, func() ([]byte, error) {
		output, err := executeSSHCommandRaw(command)
		return []byte(output), err
	})
}

func readServerStatus() ([]byte, error) {
	command := serverStatusReadCommand()
	return cachedGetFresh("server-status", localInventoryTTL, func() ([]byte, error) {
		type result struct {
			output string
			err    error
		}
		metrics := make(chan result, 1)
		go func() {
			output, err := executeSSHCommandRaw(command)
			metrics <- result{output, err}
		}()
		inventory, err := readLocalInventory()
		if err != nil {
			return nil, err
		}
		status := <-metrics
		if status.err != nil {
			return nil, status.err
		}
		return parseServerStatus(status.output + "\n__LOCAL_INVENTORY__\n" + string(inventory))
	})
}

func refreshReadCaches() {
	flightMu.Lock()
	cacheMu.Lock()
	for key, entry := range cache {
		entry.expiresAt = time.Time{}
		cache[key] = entry
	}
	// Detach prior producers so they cannot make pre-refresh values fresh again.
	inFlight = map[string]*flightCall{}
	cacheMu.Unlock()
	flightMu.Unlock()
}
