package main

import (
	"fmt"
	"sort"
	"strings"
)

// Only configured summary logs are read, not verbose rclone logs, old recovery
// copies or a directory-wide glob. Paths are POSIX even when the API is Windows.
func driveHistoryPaths() []string {
	cfg := backupSystemSettings()
	seen := map[string]bool{}
	paths := []string{}
	add := func(filename string) {
		if filename != "" && !seen[filename] {
			seen[filename] = true
			paths = append(paths, filename)
		}
	}
	add(cfg.DriveHistoryLog)
	keys := []string{}
	for key, loc := range cfg.CronLocations {
		if loc.ID == "drive-sync" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		if local, err := remoteCronSettings(cfg, "drive-sync", key); err == nil {
			add(local.DriveHistoryLog)
		}
	}
	return paths
}

// Validate the four legacy CSV fields before constructing JSON. Normalize
// exact duplicate records retained by migrations, not merely their timestamp.
const driveHistoryRecordsAwk = `
function valid_date(value, a, year, month, day, days) {
  if (value !~ /^20[0-9][0-9]-[0-9][0-9]-[0-9][0-9]$/) return 0
  split(value, a, "-"); year=a[1]+0; month=a[2]+0; day=a[3]+0
  if (month < 1 || month > 12 || day < 1) return 0
  days = month == 2 ? 28 + (year%4 == 0 && (year%100 != 0 || year%400 == 0)) : (month == 4 || month == 6 || month == 9 || month == 11 ? 30 : 31)
  return day <= days
}
NF == 4 {
  for (i=1; i<=NF; i++) { sub(/^[[:space:]]+/, "", $i); sub(/[[:space:]]+$/, "", $i) }
  if (!valid_date($1) || $2 !~ /^[0-2][0-9]:[0-5][0-9]:[0-5][0-9]$/ || substr($2,1,2)+0 > 23) next
  if (length($3) > 24 || $3 !~ /^[0-9]+([.][0-9]+)?$/ || $3+0 > 315360000) next
  status=tolower($4)
  if (status == "successful" || status == "success") status="Successful"
  else if (status == "failed" || status == "failure") status="Failed"
  else if (status == "cancelled" || status == "canceled") status="Cancelled"
  else next
  key=$1 SUBSEP $2 SUBSEP ($3+0) SUBSEP status
  if (!seen[key]++) print $1 "|" $2 "|" $3 "|" status
}
`

const driveHistoryJSONAwk = `
BEGIN { printf "["; separator="" }
{
  printf "%s{\"name\":\"Lưu trữ bản backup lên Google Drive\",\"date\":\"%s\",\"time\":\"%s\",\"duration\":\"%s\",\"status\":\"%s\"}", separator, $1, $2, $3, $4
  separator=","
}
END { print "]" }
`

func driveActivityCommand() string {
	var command strings.Builder
	command.WriteString("{\n")
	for _, filename := range driveHistoryPaths() {
		quoted := activityShellQuote(filename)
		fmt.Fprintf(&command, "if [ -f %s ] && [ ! -L %s ]; then cat -- %s; printf '\\n'; fi\n", quoted, quoted, quoted)
	}
	// An empty configuration still produces a valid empty JSON array.
	command.WriteString(":\n} | awk -F'|' " + activityShellQuote(driveHistoryRecordsAwk))
	command.WriteString(" | LC_ALL=C sort -t'|' -k1,1r -k2,2r -k3,3n -k4,4 | awk -F'|' " + activityShellQuote(driveHistoryJSONAwk))
	return command.String()
}

const driveHistoryClearAwk = `
BEGIN { n=split(keys, selected, ";"); for (i=1; i<=n; i++) wanted[selected[i]]=1 }
{
  date=$1; time=$2
  sub(/^[[:space:]]+/, "", date); sub(/[[:space:]]+$/, "", date)
  sub(/^[[:space:]]+/, "", time); sub(/[[:space:]]+$/, "", time)
  if (!((date "|" time) in wanted)) print
}
`

func driveActivityClearCommand(entries []string) string {
	// Scheduled wrappers and direct Drive scripts use these same locks. Refuse
	// while a run is appending history, instead of replacing its open log inode.
	lock := remoteInstallation().LockPrefix + "job-drive-sync.lock"
	prefix := fmt.Sprintf("set -e\numask 077\n[ ! -L %s ] && [ ! -L /run/backup-monitor-drive.lock ] || exit 1\nexec 8>%s\nflock -n 8 || { printf 'Drive job is running; retry after it finishes\\n' >&2; exit 75; }\nexec 7>/run/backup-monitor-drive.lock\nflock -n 7 || { printf 'Drive job is running; retry after it finishes\\n' >&2; exit 75; }\n", activityShellQuote(lock), activityShellQuote(lock))
	paths := driveHistoryPaths()
	// Validate every source before changing any file; an unsafe later source
	// must not leave the earlier shared history partially cleared.
	for _, filename := range paths {
		quoted := activityShellQuote(filename)
		prefix += fmt.Sprintf("[ ! -L %s ] && { [ ! -e %s ] || [ -f %s ]; } || { printf 'Unsafe Drive history source\\n' >&2; exit 1; }\n", quoted, quoted, quoted)
	}
	return prefix + activityClearCommandWithFilter(entries, paths, false, "-F'|' ", driveHistoryClearAwk)
}
