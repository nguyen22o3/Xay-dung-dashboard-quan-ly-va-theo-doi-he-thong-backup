package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
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
	{ID: "integrity-check", Name: "Kiểm tra số thư mục sao lưu", Script: "/root/scripts/check_integrity.sh", Log: "/root/check_integrity.log"},
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
TASK_ID=%q
WRAPPER=%q
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
if ! awk -v script="$SCRIPT" -v id="$TASK_ID" -v wrapper="$WRAPPER" -v hour="$HOUR" -v minute="$MINUTE" -v expected="$EXPECTED" '
  NF >= 7 && $1 !~ /^#/ && $6 == "/bin/bash" && ($7 == script || ($7 == wrapper && $8 == id)) {
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
`, task.Script, task.ID, scheduledBackupRunnerPath, hour, minute, expectedBase64), true
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
	runner := trackedRunnerForTask(task)
	// task comes exclusively from managedCronTasks, never from user input.
	return fmt.Sprintf(`set -eu
SCRIPT=%q
LOG=%q
TASK_ID=%q
WRAPPER=%q
if ! crontab -l 2>/dev/null | awk -v script="$SCRIPT" -v id="$TASK_ID" -v wrapper="$WRAPPER" '
  NF >= 7 && $1 !~ /^#/ && $6 == "/bin/bash" && ($7 == script || ($7 == wrapper && $8 == id)) { found=1 }
  END { exit !found }
'; then
  printf 'CRON_NOT_SCHEDULED\n' >&2
  exit 1
fi
if [ ! -f "$SCRIPT" ] || [ -L "$SCRIPT" ]; then
  printf 'CRON_SCRIPT_UNAVAILABLE\n' >&2
  exit 1
fi
exec 8>/root/.backup-monitor-job-%s.lock
if ! flock -n 8; then
  printf 'CRON_ALREADY_RUNNING\n' >&2
  exit 1
fi
if [ "$SCRIPT" = "/root/scripts/cleanup-panel-backups.sh" ]; then
  START_SECONDS=$(date +%%s)
  START_DATE=$(date +%%F)
  START_TIME=$(date +%%T)
  record_cleanup() {
    printf 'MONITOR_RUN|cleanup-panel|%%s|%%s|%%s|%%s\n' "$START_DATE" "$START_TIME" "$1" "$(( $(date +%%s) - START_SECONDS ))" >> "$LOG"
  }
  record_cleanup running
  if ! RUN_OUTPUT=$(/bin/bash "$SCRIPT" --delete 2>&1); then
    [ -z "$RUN_OUTPUT" ] || echo "$RUN_OUTPUT" >> "$LOG"
    record_cleanup failed
    echo 'CRON_CLEANUP_FAILED' >&2
    exit 1
  fi
  if ! VERIFY_OUTPUT=$(/bin/bash "$SCRIPT" --dry-run 2>&1); then
    [ -z "$VERIFY_OUTPUT" ] || echo "$VERIFY_OUTPUT" >> "$LOG"
    record_cleanup failed
    echo 'CRON_CLEANUP_VERIFY_FAILED' >&2
    exit 1
  fi
  if echo "$VERIFY_OUTPUT" | grep -q '^Would delete: '; then
    [ -z "$RUN_OUTPUT" ] || echo "$RUN_OUTPUT" >> "$LOG"
    echo "$VERIFY_OUTPUT" >> "$LOG"
    record_cleanup failed
    echo 'CRON_CLEANUP_INCOMPLETE' >&2
    exit 1
  fi
  DELETED=$(echo "$RUN_OUTPUT" | awk '/^Deleted: / { count++ } END { print count+0 }')
  [ -z "$RUN_OUTPUT" ] || echo "$RUN_OUTPUT" >> "$LOG"
  echo "[$(date '+%%F %%T')] Cleaned panel backups: $DELETED deleted" >> "$LOG"
  record_cleanup success
  echo "CLEANED:$DELETED"
  exit 0
fi
RUNNER=$(printf '%%s' '%s' | base64 -d)
nohup /bin/bash -c "$RUNNER" >> "$LOG" 2>&1 </dev/null &
printf 'STARTED\n'
`, task.Script, task.Log, task.ID, scheduledBackupRunnerPath, task.ID, base64.StdEncoding.EncodeToString([]byte(runner)))
}

func parsePanelCleanupResult(output string) (int, bool) {
	value, ok := strings.CutPrefix(strings.TrimSpace(output), "CLEANED:")
	if !ok {
		return 0, false
	}
	deleted, err := strconv.Atoi(value)
	return deleted, err == nil && deleted >= 0
}

type managedCronJobResponse struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Script          string `json:"script"`
	Schedule        string `json:"schedule"`
	LastRun         string `json:"last_run"`
	LogUpdatedAt    string `json:"log_updated_at"`
	TrackedAt       string `json:"tracked_at,omitempty"`
	Status          string `json:"status"`
	ScheduleTracked bool   `json:"schedule_tracked"`
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
	runs := make(map[string][]string)
	lines := strings.Split(output, "\n")
	section := ""
	for _, line := range lines {
		line = strings.TrimSpace(line)
		switch line {
		case "__CRONTAB__", "__LOGS__", "__RUNS__":
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
		if section == "__RUNS__" {
			fields := strings.Split(line, "|")
			if (len(fields) == 7 || len(fields) == 8) && fields[0] == "MONITOR_RUN" {
				runs[fields[1]] = fields
			}
			continue
		}
		if section != "__CRONTAB__" || line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if task, tracked, ok := managedTaskForCronFields(fields); ok {
			jobs = append(jobs, managedCronJobResponse{
				ID: task.ID, Name: task.Name, Script: task.Script,
				Schedule: strings.Join(fields[:5], " "), Status: "unknown", ScheduleTracked: tracked,
			})
		}
	}
	for i := range jobs {
		task, _ := managedCronTaskByID(jobs[i].ID)
		jobs[i].LogUpdatedAt = logs[task.Log]
		if jobs[i].LogUpdatedAt == "" {
			jobs[i].Status = "never"
		}
		if run, ok := runs[task.ID]; ok {
			active := run[len(run)-1] == "active"
			if run[2] != "" && run[3] != "" {
				jobs[i].LastRun = run[2] + " " + run[3]
				jobs[i].TrackedAt = jobs[i].LastRun
			}
			jobs[i].Status = run[4]
			if active {
				jobs[i].Status = "running"
			}
			if run[4] == "running" && !active {
				jobs[i].Status = "unknown"
			}
			// A later untracked execution must not reuse an old result.
			if !active && jobs[i].LogUpdatedAt > jobs[i].LastRun {
				if run[4] == "success" || run[4] == "failed" {
					duration, _ := strconv.Atoi(run[5])
					started, err := time.Parse("2006-01-02 15:04:05", jobs[i].LastRun)
					if err == nil && jobs[i].LogUpdatedAt > started.Add(time.Duration(duration+2)*time.Second).Format("2006-01-02 15:04:05") {
						jobs[i].Status = "unknown"
					}
				}
			}
		}
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
done
printf '__RUNS__\n'
for pair in 'cleanup-panel:/root/cleanup-panel-backups.log' 'backup-site:/root/backup-site.log' 'backup-database:/root/backup-database.log' 'drive-sync:/root/auto_backup.log' 'integrity-check:/root/check_integrity.log'; do
  id=${pair%%:*}
  file=${pair#*:}
  [ -f "$file" ] && [ ! -L "$file" ] || continue
  marker=$(tail -n 200 "$file" | awk -F'|' -v id="$id" '$1 == "MONITOR_RUN" && $2 == id {last=$0} END {print last}')
  active=inactive
  lock=/root/.backup-monitor-job-$id.lock
  if [ -f "$lock" ] && ! flock -n "$lock" -c true; then active=active; fi
  if [ -z "$marker" ] && [ "$active" = active ]; then marker="MONITOR_RUN|$id|||running|0"; fi
  [ -z "$marker" ] || printf '%s|%s\n' "$marker" "$active"
done`)
	if err != nil {
		return nil, err
	}
	return json.Marshal(parseManagedCronJobs(output))
}
