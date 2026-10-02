package main

import (
	"encoding/base64"
	"fmt"
	"strings"
)

const scheduledBackupRunnerPath = "/root/scripts/backup-monitor-run.sh"
const backupRunHistoryPath = "/root/backup-monitor-runs.log"

// Manual and scheduled runs share a lifecycle and the same per-job flock.
// The separate structured history survives verbose/truncated script output.
const trackedRunShell = `umask 077
if [ ! -f "$SCRIPT" ] || [ -L "$SCRIPT" ]; then
  printf 'CRON_SCRIPT_UNAVAILABLE\n' >&2
  exit 1
fi
if [ -n "$RUN_HISTORY" ]; then
  if [ -L "$RUN_HISTORY" ] || { [ -e "$RUN_HISTORY" ] && [ ! -f "$RUN_HISTORY" ]; }; then
    printf 'CRON_HISTORY_UNAVAILABLE\n' >&2
    exit 1
  fi
  : >> "$RUN_HISTORY" || exit 1
fi
START_SECONDS=$(date +%s)
START_DATE=$(date +%F)
START_TIME=$(date +%T)
RUN_ID="$(date +%s%N)-$$"
finish_run() {
  code=$?
  trap - EXIT
  status=success
  [ "$code" -eq 0 ] || status=failed
  marker=$(printf 'MONITOR_RUN|%s|%s|%s|%s|%s|%s' "$TASK_ID" "$START_DATE" "$START_TIME" "$status" "$(( $(date +%s) - START_SECONDS ))" "$RUN_ID")
  printf '%s\n' "$marker"
  if [ -n "$RUN_HISTORY" ]; then
    printf '%s\n' "$marker" >> "$RUN_HISTORY" || printf 'CRON_HISTORY_WRITE_FAILED\n' >&2
  fi
  exit "$code"
}
trap finish_run EXIT
printf 'MONITOR_RUN|%s|%s|%s|running|0|%s\n' "$TASK_ID" "$START_DATE" "$START_TIME" "$RUN_ID"
/bin/bash "$SCRIPT" "$@" &
child=$!
trap 'kill -TERM "$child" 2>/dev/null || true; wait "$child" 2>/dev/null || true; exit 143' TERM HUP
trap 'kill -INT "$child" 2>/dev/null || true; wait "$child" 2>/dev/null || true; exit 130' INT
wait "$child"
result=$?
trap - TERM HUP INT
exit "$result"
`

func trackedRunnerForTask(task managedCronTask) string {
	history := ""
	if task.ID == "backup-site" || task.ID == "backup-database" {
		history = backupRunHistoryPath
	}
	return fmt.Sprintf("SCRIPT=%q\nTASK_ID=%q\nRUN_HISTORY=%q\n", task.Script, task.ID, history) + trackedRunShell
}

func buildScheduledBackupRunner() string {
	var script strings.Builder
	script.WriteString("#!/bin/bash\n# backup-monitor scheduled tracking v1\nset -u\numask 077\nTASK_ID=${1:-}\ncase \"$TASK_ID\" in\n")
	for _, id := range []string{"backup-site", "backup-database"} {
		task, _ := managedCronTaskByID(id)
		fmt.Fprintf(&script, "  %s) SCRIPT=%q; LOG=%q ;;\n", task.ID, task.Script, task.Log)
	}
	script.WriteString("  *) printf 'CRON_INVALID_JOB\\n' >&2; exit 1 ;;\nesac\nshift\n")
	fmt.Fprintf(&script, "RUN_HISTORY=%q\n", backupRunHistoryPath)
	script.WriteString(`if [ -L "$LOG" ] || { [ -e "$LOG" ] && [ ! -f "$LOG" ]; }; then
  printf 'CRON_LOG_UNAVAILABLE\n' >&2
  exit 1
fi
exec >> "$LOG" 2>&1
exec 8>"/root/.backup-monitor-job-$TASK_ID.lock"
if ! flock -n 8; then
  printf '[%s] CRON_ALREADY_RUNNING: %s; skipped duplicate start\n' "$(date '+%F %T')" "$TASK_ID"
  exit 75
fi
`)
	script.WriteString(trackedRunShell)
	return script.String()
}

// Only two allowlisted command forms are managed. Scripts, arguments, times,
// comments, environment variables and unrelated cron entries stay intact.
const trackingCronAwk = `
  NF >= 7 && $1 !~ /^#/ && $6 == "/bin/bash" {
    id=""
    if ($7 == "/root/scripts/backup-site.sh") id="backup-site"
    else if ($7 == "/root/scripts/backup-database.sh") id="backup-database"
    else if ($7 == wrapper && ($8 == "backup-site" || $8 == "backup-database")) id=$8
    if (id != "") {
      counts[id]++
      total++
      if ($7 != wrapper) {
        match($0, /^[[:space:]]*[^[:space:]]+[[:space:]]+[^[:space:]]+[[:space:]]+[^[:space:]]+[[:space:]]+[^[:space:]]+[[:space:]]+[^[:space:]]+[[:space:]]+/)
        prefix=substr($0, 1, RLENGTH)
        command=substr($0, RLENGTH+1)
        match(command, /^\/bin\/bash[[:space:]]+[^[:space:]]+/)
        print prefix "/bin/bash " wrapper " " id substr(command, RLENGTH+1)
        next
      }
    }
  }
  { print }
  END {
    if (!total) { print "CRON_NOT_SCHEDULED" > "/dev/stderr"; exit 1 }
    if (counts["backup-site"] > 1 || counts["backup-database"] > 1) { print "CRON_DUPLICATE" > "/dev/stderr"; exit 1 }
  }
`

func buildCronTrackingInstallCommand() string {
	return fmt.Sprintf(`set -euo pipefail
umask 077
exec 9>/root/.backup-monitor-crontab.lock
flock -x 9
SCRIPTS_DIR=/root/scripts
WRAPPER=%q
HISTORY=%q
[ ! -L "$SCRIPTS_DIR" ] || { printf 'CRON_UNSAFE_DIRECTORY\n' >&2; exit 1; }
install -d -m 700 "$SCRIPTS_DIR"
[ "$(stat -c '%%u' "$SCRIPTS_DIR")" = 0 ] || { printf 'CRON_UNSAFE_DIRECTORY\n' >&2; exit 1; }
if [ -e "$WRAPPER" ] || [ -L "$WRAPPER" ]; then
  [ -f "$WRAPPER" ] && [ ! -L "$WRAPPER" ] && grep -qx '# backup-monitor scheduled tracking v1' "$WRAPPER" || { printf 'CRON_WRAPPER_CONFLICT\n' >&2; exit 1; }
fi
if [ -L "$HISTORY" ] || { [ -e "$HISTORY" ] && [ ! -f "$HISTORY" ]; }; then
  printf 'CRON_HISTORY_UNAVAILABLE\n' >&2
  exit 1
fi
TMP_DIR=$(mktemp -d "$SCRIPTS_DIR/.tracking.XXXXXXXX")
trap 'rm -f -- "$TMP_DIR/current" "$TMP_DIR/updated" "$TMP_DIR/runner"; rmdir -- "$TMP_DIR"' EXIT
crontab -l > "$TMP_DIR/current" 2>/dev/null || { printf 'CRON_NOT_SCHEDULED\n' >&2; exit 1; }
awk -v wrapper="$WRAPPER" '%s' "$TMP_DIR/current" > "$TMP_DIR/updated"
IDS=$(awk -v wrapper="$WRAPPER" 'NF >= 8 && $1 !~ /^#/ && $6 == "/bin/bash" && $7 == wrapper {print $8}' "$TMP_DIR/updated")
for id in $IDS; do
  case "$id" in
    backup-site) script=/root/scripts/backup-site.sh ;;
    backup-database) script=/root/scripts/backup-database.sh ;;
    *) continue ;;
  esac
  [ -f "$script" ] && [ ! -L "$script" ] || { printf 'CRON_SCRIPT_UNAVAILABLE\n' >&2; exit 1; }
done
printf '%%s' '%s' | base64 -d > "$TMP_DIR/runner"
/bin/bash -n "$TMP_DIR/runner"
chmod 700 "$TMP_DIR/runner"
BACKUP="$SCRIPTS_DIR/.crontab-before-tracking-$(date +%%Y%%m%%dT%%H%%M%%S)-$$.txt"
cp -- "$TMP_DIR/current" "$BACKUP"
: >> "$HISTORY"
mv -f -- "$TMP_DIR/runner" "$WRAPPER"
crontab "$TMP_DIR/updated"
printf 'TRACKING_ENABLED\n'
`, scheduledBackupRunnerPath, backupRunHistoryPath, trackingCronAwk, base64.StdEncoding.EncodeToString([]byte(buildScheduledBackupRunner())))
}

func managedTaskForCronFields(fields []string) (managedCronTask, bool, bool) {
	if len(fields) < 7 || fields[5] != "/bin/bash" {
		return managedCronTask{}, false, false
	}
	for _, task := range managedCronTasks {
		if fields[6] == task.Script {
			return task, false, true
		}
		if len(fields) >= 8 && fields[6] == scheduledBackupRunnerPath && fields[7] == task.ID && (task.ID == "backup-site" || task.ID == "backup-database") {
			return task, true, true
		}
	}
	return managedCronTask{}, false, false
}

// New records carry an ID. Legacy records without IDs remain readable, while
// only one final result per ID can contribute to the chart.
const backupRunActivityAwk = `
  $1 == "MONITOR_RUN" && ($2 == "backup-site" || $2 == "backup-database") && ($5 == "success" || $5 == "failed") && $3 ~ /^[0-9]{4}-[0-9]{2}-[0-9]{2}$/ && $4 ~ /^[0-9]{2}:[0-9]{2}:[0-9]{2}$/ && $6 ~ /^[0-9]+$/ {
    if (NF >= 7 && seen[$7]++) next
    name = ($2 == "backup-site") ? "Lần chạy sao lưu website" : "Lần chạy sao lưu cơ sở dữ liệu"
    printf "{\"kind\":\"run\",\"name\":\"%s\",\"date\":\"%s\",\"time\":\"%s\",\"duration\":%s,\"status\":\"%s\"},", name, $3, $4, $6, $5
  }
`

func backupRunActivityCommand() string {
	return fmt.Sprintf(`{
  tail -n 1000 /root/backup-site.log /root/backup-database.log 2>/dev/null | awk -F'|' 'NF == 6 && $1 == "MONITOR_RUN"'
  if [ -f %q ] && [ ! -L %q ]; then tail -n 10000 %q; fi
} | awk -F'|' '%s' | sed 's/,$//'`, backupRunHistoryPath, backupRunHistoryPath, backupRunHistoryPath, backupRunActivityAwk)
}
