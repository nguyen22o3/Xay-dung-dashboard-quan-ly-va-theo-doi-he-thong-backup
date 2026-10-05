package backupops

import (
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

type ScheduleChange struct {
	ID               string         `json:"id"`
	Clock            string         `json:"clock"`
	ExpectedSchedule string         `json:"expectedSchedule"`
	ExpectedEnabled  *bool          `json:"expectedEnabled"`
	Cycle            *ScheduleCycle `json:"cycle,omitempty"`
	Create           bool           `json:"create,omitempty"`
}

type ScheduleCycle struct {
	Type    string `json:"type"`
	Every   int    `json:"every,omitempty"`
	Weekday int    `json:"weekday,omitempty"`
	Day     int    `json:"day,omitempty"`
}

func cycleSchedule(change *ScheduleChange) (string, error) {
	if !scheduleClockPattern.MatchString(change.Clock) {
		return "", errors.New("Giờ thực hiện không hợp lệ")
	}
	hour, _ := strconv.Atoi(change.Clock[:2])
	minute, _ := strconv.Atoi(change.Clock[3:])
	h, m := strconv.Itoa(hour), strconv.Itoa(minute)
	if change.Cycle == nil {
		return "", nil
	}
	c := change.Cycle
	switch c.Type {
	case "daily":
		return m + " " + h + " * * *", nil
	case "hourly":
		return m + " * * * *", nil
	case "days":
		if c.Every >= 2 && c.Every <= 31 {
			return m + " " + h + " */" + strconv.Itoa(c.Every) + " * *", nil
		}
	case "hours":
		if c.Every >= 2 && c.Every <= 23 {
			return m + " */" + strconv.Itoa(c.Every) + " * * *", nil
		}
	case "minutes":
		if c.Every >= 1 && c.Every <= 59 {
			return "*/" + strconv.Itoa(c.Every) + " * * * *", nil
		}
	case "weekly":
		if c.Weekday >= 0 && c.Weekday <= 6 {
			return m + " " + h + " * * " + strconv.Itoa(c.Weekday), nil
		}
	case "monthly":
		if c.Day >= 1 && c.Day <= 31 {
			return m + " " + h + " " + strconv.Itoa(c.Day) + " * *", nil
		}
	}
	return "", errors.New("Chu kỳ thực hiện không hợp lệ hoặc chưa được hỗ trợ")
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
func proposeSchedule(cron []byte, scriptsDir string, change *ScheduleChange, logsDirs ...string) ([]byte, *SchedulePreview, error) {
	if change == nil {
		return cron, nil, nil
	}
	files := map[string]string{
		"backup-site": "backup-site.sh", "backup-database": "backup-database.sh", "backup-panel": "backup-panel.sh",
		"cleanup-panel": "cleanup-panel-backups.sh", "drive-sync": "auto_backup.sh", "integrity-check": "check_integrity.sh",
	}
	file, known := files[change.ID]
	expected := strings.Fields(change.ExpectedSchedule)
	creating := change.Create && change.ExpectedSchedule == "" && change.ExpectedEnabled != nil && !*change.ExpectedEnabled && change.Cycle != nil
	if !known || !scheduleClockPattern.MatchString(change.Clock) || change.ExpectedEnabled == nil ||
		(!creating && (change.Create || len(change.ExpectedSchedule) > 100 || len(expected) != 5 || strings.Join(expected, " ") != change.ExpectedSchedule)) {
		return nil, nil, errors.New("Yêu cầu đổi lịch cron không hợp lệ")
	}
	cycle, err := cycleSchedule(change)
	if err != nil {
		return nil, nil, err
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
		if creating {
			return nil, nil, errors.New("Tác vụ đã có lịch; hãy tải lại và dùng chức năng sửa")
		}
		actual := strings.Join(fields[:5], " ")
		if actual != change.ExpectedSchedule || !paused != *change.ExpectedEnabled {
			return nil, nil, errors.New("Lịch hoặc trạng thái cron đã thay đổi; hãy tải lại")
		}
		proposed := strconv.Itoa(minute) + " " + strconv.Itoa(hour) + " " + strings.Join(fields[2:5], " ")
		if cycle != "" {
			proposed = cycle
		}
		preview = &SchedulePreview{change.ID, actual, proposed, !paused, proposed != actual}
		if preview.Changed {
			tokens := scheduleTokens.FindAllStringIndex(body, -1)
			updated := body[:tokens[0][0]] + strconv.Itoa(minute) + body[tokens[0][1]:tokens[1][0]] + strconv.Itoa(hour) + body[tokens[1][1]:]
			if cycle != "" {
				updated = body[:tokens[0][0]] + cycle + body[tokens[4][1]:]
			}
			if paused {
				updated = pausedSchedulePrefix + updated
			}
			lines[i] = updated
		}
	}
	if matches == 0 {
		if creating {
			if len(logsDirs) != 1 || SafeParents(script) != nil {
				return nil, nil, errors.New("Không xác nhận được script hoặc thư mục log")
			}
			info, err := os.Stat(script)
			if err != nil || !info.Mode().IsRegular() {
				return nil, nil, errors.New("Script tác vụ chưa tồn tại")
			}
			logNames := map[string]string{"backup-site": "backup-site.log", "backup-database": "backup-database.log", "backup-panel": "backup-panel.log", "cleanup-panel": "cleanup-panel-backups.log", "drive-sync": "auto_backup.log", "integrity-check": "check_integrity.log"}
			command := "/bin/bash " + script
			if (change.ID == "backup-site" || change.ID == "backup-database" || change.ID == "backup-panel") && exists(wrapper) && SafeParents(wrapper) == nil {
				command = "/bin/bash " + wrapper + " " + change.ID
			}
			if change.ID == "cleanup-panel" {
				command += " --delete"
			}
			command += " >> " + filepath.Join(logsDirs[0], logNames[change.ID]) + " 2>&1"
			return []byte(strings.TrimRight(string(cron), "\n") + "\n" + cycle + " " + command + "\n"), &SchedulePreview{change.ID, "", cycle, true, true}, nil
		}
		return nil, nil, errors.New("Không tìm thấy lịch cron được quản lý; hãy tải lại")
	}
	if matches != 1 {
		return nil, nil, errors.New("Có nhiều lịch cho cùng tác vụ; hãy xử lý lịch trùng trước")
	}
	return []byte(strings.Join(lines, "\n")), preview, nil
}
