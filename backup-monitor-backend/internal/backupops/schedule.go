package backupops

import (
	"errors"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type ScheduleChange struct {
	ID               string `json:"id"`
	Clock            string `json:"clock"`
	ExpectedSchedule string `json:"expectedSchedule"`
	ExpectedEnabled  *bool  `json:"expectedEnabled"`
}

type SchedulePreview struct {
	ID       string `json:"id"`
	Previous string `json:"previous"`
	Proposed string `json:"proposed"`
	Enabled  bool   `json:"enabled"`
	Changed  bool   `json:"changed"`
}

const pausedSchedulePrefix = "# backup-monitor-paused "

var scheduleClockPattern = regexp.MustCompile(`^([01][0-9]|2[0-3]):[0-5][0-9]$`)
var scheduleTokens = regexp.MustCompile(`[^ \t]+`)

// Update only a known direct/wrapped task and preserve its arguments, recurrence
// and pause marker. This runs inside the storage transaction, never on its own.
func proposeSchedule(cron []byte, scriptsDir string, change *ScheduleChange) ([]byte, *SchedulePreview, error) {
	if change == nil {
		return cron, nil, nil
	}
	files := map[string]string{
		"backup-site": "backup-site.sh", "backup-database": "backup-database.sh", "backup-panel": "backup-panel.sh",
		"cleanup-panel": "cleanup-panel-backups.sh", "drive-sync": "auto_backup.sh", "integrity-check": "check_integrity.sh",
	}
	file, known := files[change.ID]
	expected := strings.Fields(change.ExpectedSchedule)
	if !known || !scheduleClockPattern.MatchString(change.Clock) || change.ExpectedEnabled == nil ||
		len(change.ExpectedSchedule) > 100 || len(expected) != 5 || strings.Join(expected, " ") != change.ExpectedSchedule {
		return nil, nil, errors.New("Yêu cầu đổi lịch cron không hợp lệ")
	}
	hour, _ := strconv.Atoi(change.Clock[:2])
	minute, _ := strconv.Atoi(change.Clock[3:])
	script := filepath.Join(scriptsDir, file)
	wrapper := filepath.Join(scriptsDir, "backup-monitor-run.sh")
	lines := strings.Split(string(cron), "\n")
	matches := 0
	var preview *SchedulePreview
	for i, original := range lines {
		paused := strings.HasPrefix(original, pausedSchedulePrefix)
		body := original
		if paused {
			body = strings.TrimPrefix(body, pausedSchedulePrefix)
		}
		fields := strings.Fields(body)
		if len(fields) < 7 || strings.HasPrefix(fields[0], "#") || fields[5] != "/bin/bash" {
			continue
		}
		wrapped := len(fields) >= 8 && fields[6] == wrapper && fields[7] == change.ID &&
			(change.ID == "backup-site" || change.ID == "backup-database" || change.ID == "backup-panel")
		if fields[6] != script && !wrapped {
			continue
		}
		matches++
		actual := strings.Join(fields[:5], " ")
		if actual != change.ExpectedSchedule || !paused != *change.ExpectedEnabled {
			return nil, nil, errors.New("Lịch hoặc trạng thái cron đã thay đổi; hãy tải lại")
		}
		proposed := strconv.Itoa(minute) + " " + strconv.Itoa(hour) + " " + strings.Join(fields[2:5], " ")
		preview = &SchedulePreview{change.ID, actual, proposed, !paused, proposed != actual}
		if preview.Changed {
			tokens := scheduleTokens.FindAllStringIndex(body, -1)
			updated := body[:tokens[0][0]] + strconv.Itoa(minute) + body[tokens[0][1]:tokens[1][0]] + strconv.Itoa(hour) + body[tokens[1][1]:]
			if paused {
				updated = pausedSchedulePrefix + updated
			}
			lines[i] = updated
		}
	}
	if matches == 0 {
		return nil, nil, errors.New("Không tìm thấy lịch cron được quản lý; hãy tải lại")
	}
	if matches != 1 {
		return nil, nil, errors.New("Có nhiều lịch cho cùng tác vụ; hãy xử lý lịch trùng trước")
	}
	return []byte(strings.Join(lines, "\n")), preview, nil
}
