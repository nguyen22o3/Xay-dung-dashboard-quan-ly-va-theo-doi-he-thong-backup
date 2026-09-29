package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type managedCronTask struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Script string `json:"script"`
	Log    string `json:"-"`
}

var managedCronTasks = []managedCronTask{
	{ID: "cleanup-panel", Name: "Dọn dẹp thư mục panel", Script: "/root/scripts/cleanup-panel-backups.sh", Log: "/root/cleanup-panel-backups.log"},
	{ID: "backup-site", Name: "Sao lưu website", Script: "/root/scripts/backup-site.sh", Log: "/root/backup-site.log"},
	{ID: "backup-database", Name: "Sao lưu database", Script: "/root/scripts/backup-database.sh", Log: "/root/backup-database.log"},
	{ID: "drive-sync", Name: "Đồng bộ Google Drive", Script: "/root/scripts/auto_backup.sh", Log: "/root/auto_backup.log"},
	{ID: "integrity-check", Name: "Kiểm tra toàn vẹn backup", Script: "/root/scripts/check_integrity.sh", Log: "/root/check_integrity.log"},
}

var cronClockRe = regexp.MustCompile(`^(?:[01][0-9]|2[0-3]):[0-5][0-9]$`)

func commandForCronScheduleUpdate(task managedCronTask, clock, expectedSchedule string) (string, bool) {
	if !cronClockRe.MatchString(clock) || len(expectedSchedule) > 100 {
		return "", false
	}
	fields := strings.Fields(expectedSchedule)
	if len(fields) != 5 || strings.Join(fields, " ") != expectedSchedule {
		return "", false
	}
	hour, _ := strconv.Atoi(clock[:2])
	minute, _ := strconv.Atoi(clock[3:])
	expectedBase64 := base64.StdEncoding.EncodeToString([]byte(expectedSchedule))
	// Only the allowlisted script path and validated numbers enter shell syntax.
	// The previous schedule is base64-encoded and compared inside awk so a stale
	// browser cannot silently overwrite an intervening crontab edit.
	return fmt.Sprintf(`set -eu
SCRIPT=%q
HOUR=%d
MINUTE=%d
EXPECTED=$(printf '%%s' '%s' | base64 -d)
exec 9>/root/.backup-monitor-crontab.lock
flock -x 9
TMP_DIR=$(mktemp -d /tmp/backup-monitor-cron.XXXXXXXX)
trap 'rm -f -- "$TMP_DIR/current" "$TMP_DIR/updated"; rmdir -- "$TMP_DIR"' EXIT
if ! crontab -l > "$TMP_DIR/current" 2>/dev/null; then
  printf 'CRON_NOT_SCHEDULED\n' >&2
  exit 1
fi
if ! awk -v script="$SCRIPT" -v hour="$HOUR" -v minute="$MINUTE" -v expected="$EXPECTED" '
  NF >= 7 && $1 !~ /^#/ && $6 == "/bin/bash" && $7 == script {
    matches++
    actual=$1 " " $2 " " $3 " " $4 " " $5
    if (actual != expected) stale=1
    match($0, /^[[:space:]]*[^[:space:]]+[[:space:]]+[^[:space:]]+[[:space:]]+[^[:space:]]+[[:space:]]+[^[:space:]]+[[:space:]]+[^[:space:]]+[[:space:]]+/)
    print minute " " hour " " $3 " " $4 " " $5 " " substr($0, RLENGTH + 1)
    next
  }
  { print }
  END {
    if (matches == 0) { print "CRON_NOT_SCHEDULED" > "/dev/stderr"; exit 1 }
    if (matches > 1) { print "CRON_DUPLICATE" > "/dev/stderr"; exit 1 }
    if (stale) { print "CRON_SCHEDULE_CHANGED" > "/dev/stderr"; exit 1 }
  }
' "$TMP_DIR/current" > "$TMP_DIR/updated"; then
  exit 1
fi
crontab "$TMP_DIR/updated"
printf 'UPDATED\n'
`, task.Script, hour, minute, expectedBase64), true
}

func managedCronTaskByID(id string) (managedCronTask, bool) {
	for _, task := range managedCronTasks {
		if task.ID == id {
			return task, true
		}
	}
	return managedCronTask{}, false
}

func commandForManagedCronJob(task managedCronTask) string {
	// task comes exclusively from managedCronTasks, never from user input.
	return fmt.Sprintf(`set -eu
SCRIPT=%q
LOG=%q
if ! crontab -l 2>/dev/null | awk -v script="$SCRIPT" '
  NF >= 7 && $1 !~ /^#/ && $6 == "/bin/bash" && $7 == script { found=1 }
  END { exit !found }
'; then
  printf 'CRON_NOT_SCHEDULED\n' >&2
  exit 1
fi
if [ ! -f "$SCRIPT" ] || [ -L "$SCRIPT" ]; then
  printf 'CRON_SCRIPT_UNAVAILABLE\n' >&2
  exit 1
fi
nohup /bin/bash "$SCRIPT" >> "$LOG" 2>&1 </dev/null &
printf 'STARTED\n'
`, task.Script, task.Log)
}

type managedCronJobResponse struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Script   string `json:"script"`
	Schedule string `json:"schedule"`
	LastRun  string `json:"last_run"`
	Status   string `json:"status"`
}

type managedCronJobLogResponse struct {
	Content string `json:"content"`
	Exists  bool   `json:"exists"`
}

func fetchManagedCronJobLog(task managedCronTask) (managedCronJobLogResponse, error) {
	// The path comes only from managedCronTasks, never from a request parameter.
	command := fmt.Sprintf(`LOG=%q
if [ -f "$LOG" ] && [ ! -L "$LOG" ]; then
  printf 'EXISTS\n'
  tail -n 200 -- "$LOG" | tail -c 65536
else
  printf 'MISSING\n'
fi`, task.Log)
	output, err := executeSSHCommand(command)
	if err != nil {
		return managedCronJobLogResponse{}, err
	}
	state, content, ok := strings.Cut(output, "\n")
	if !ok {
		return managedCronJobLogResponse{}, fmt.Errorf("invalid cron log response")
	}
	switch state {
	case "EXISTS":
		return managedCronJobLogResponse{Content: content, Exists: true}, nil
	case "MISSING":
		return managedCronJobLogResponse{Exists: false}, nil
	default:
		return managedCronJobLogResponse{}, fmt.Errorf("invalid cron log response")
	}
}

func parseManagedCronJobs(output string) []managedCronJobResponse {
	jobs := make([]managedCronJobResponse, 0, len(managedCronTasks))
	logs := make(map[string]string)
	lines := strings.Split(output, "\n")
	section := ""
	for _, line := range lines {
		line = strings.TrimSpace(line)
		switch line {
		case "__CRONTAB__", "__LOGS__":
			section = line
			continue
		}
		if section == "__LOGS__" {
			path, stamp, ok := strings.Cut(line, "|")
			if ok {
				logs[path] = stamp
			}
			continue
		}
		if section != "__CRONTAB__" || line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 7 || fields[5] != "/bin/bash" {
			continue
		}
		for _, task := range managedCronTasks {
			if fields[6] == task.Script {
				jobs = append(jobs, managedCronJobResponse{
					ID: task.ID, Name: task.Name, Script: task.Script,
					Schedule: strings.Join(fields[:5], " "), Status: "unknown",
				})
				break
			}
		}
	}
	for i := range jobs {
		task, _ := managedCronTaskByID(jobs[i].ID)
		jobs[i].LastRun = logs[task.Log]
	}
	return jobs
}

func fetchManagedCronJobs() ([]byte, error) {
	output, err := executeSSHCommand(`printf '__CRONTAB__\n'
crontab -l 2>/dev/null || true
printf '__LOGS__\n'
for file in /root/cleanup-panel-backups.log /root/backup-site.log /root/backup-database.log /root/auto_backup.log /root/check_integrity.log; do
  if [ -f "$file" ] && [ ! -L "$file" ]; then
    printf '%s|' "$file"
    date -r "$file" '+%Y-%m-%d %H:%M:%S'
  fi
done`)
	if err != nil {
		return nil, err
	}
	return json.Marshal(parseManagedCronJobs(output))
}
