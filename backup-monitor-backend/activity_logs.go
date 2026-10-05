package main

import (
	"fmt"
	"regexp"
	"strings"

	"backup-monitor-backend/internal/backupops"
)

var activityLogKeyPattern = regexp.MustCompile(`^20[0-9]{2}-[0-9]{2}-[0-9]{2}\|[0-9]{2}:[0-9]{2}:[0-9]{2}(\|(site|database|panel))?$`)

// Match the same records that supply localActivity, not arbitrary text containing
// a date. CREATED records carry their timestamp in the archive basename and may
// have no DURATION field. Native aaPanel logs are multi-line backup blocks.
const activityLogFilterAwk = `
BEGIN {
  n = split(keys, selected, ";")
  for (i = 1; i <= n; i++) wanted[selected[i]] = 1
}
function selected_record(date, time, kind) {
  return ((date "|" time) in wanted) || ((date "|" time "|" kind) in wanted)
}
function flush_block( i) {
  if (block_count && !selected_record(block_date, block_time, block_kind))
    for (i = 1; i <= block_count; i++) print block[i]
  delete block
  block_count = 0
  block_kind = ""
}
/start backup\[/ {
  flush_block()
  if (match($0, /\[(20[0-9]{2}-[0-9]{2}-[0-9]{2}) ([0-9]{2}:[0-9]{2}:[0-9]{2})\]/, stamp)) {
    block_date = stamp[1]
    block_time = stamp[2]
    block[++block_count] = $0
    next
  }
}
/^MONITOR_RUN\|/ {
  flush_block()
  split($0, run, "|")
  kind = run[2] == "backup-site" ? "site" : run[2] == "backup-database" ? "database" : run[2] == "backup-panel" ? "panel" : ""
  panel_date = kind == "panel" && run[5] == "running" ? run[3] : ""
  panel_time = kind == "panel" && run[5] == "running" ? run[4] : ""
  if (kind != "" && selected_record(run[3], run[4], kind)) next
  print
  next
}
/^\[20[0-9]{2}-[0-9]{2}-[0-9]{2} [0-9]{2}:[0-9]{2}:[0-9]{2}\] START: aaPanel configuration backup$/ {
  flush_block()
  block_date = panel_date != "" ? panel_date : substr($0, 2, 10)
  block_time = panel_date != "" ? panel_time : substr($0, 13, 8)
  block_kind = "panel"
  block[++block_count] = $0
  next
}
$1 == "CREATED" {
  flush_block()
  count = split($2, path_parts, "/")
  name = path_parts[count]
  kind = ""
  if (match(name, /^web_(.+)_(20[0-9]{2})([0-9]{2})([0-9]{2})_([0-9]{2})([0-9]{2})([0-9]{2})(_(cron|manual)_[0-9a-f]{16})?_site\.tar\.gz$/, stamp)) kind = "site"
  else if (match(name, /^db_(.+)_(20[0-9]{2})([0-9]{2})([0-9]{2})_([0-9]{2})([0-9]{2})([0-9]{2})(_(cron|manual)_[0-9a-f]{16})?_mysql_data\.sql\.gz$/, stamp)) kind = "database"
  if (kind != "" && selected_record(stamp[2] "-" stamp[3] "-" stamp[4], stamp[5] ":" stamp[6] ":" stamp[7], kind)) next
  print
  next
}
block_count {
  if ($0 ~ /Backup site: /) block_kind = "site"
  else if ($0 ~ /Backup .*database: /) block_kind = "database"
  block[++block_count] = $0
  next
}
{ print }
END { flush_block() }
`

func activityShellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

func serverActivityClearCommand(entries []string) string {
	seen := make(map[string]bool)
	var paths []string
	for _, name := range backupops.LogNames {
		for _, logPath := range []string{configuredLog(name), "/root/" + name} {
			if !seen[logPath] {
				paths = append(paths, logPath)
				seen[logPath] = true
			}
		}
	}
	return activityClearCommand(entries, paths, true)
}

func activityClearCommand(entries, paths []string, includeCron bool) string {
	var command strings.Builder
	command.WriteString("set -e\numask 077\nchanged=0\n")
	command.WriteString(`filter_log() {
  f="$1"
  [ ! -L "$f" ] || { printf 'Unsafe log path\n' >&2; return 1; }
  [ -f "$f" ] || return 0
  recovery=$(mktemp "${f}.before-clear.XXXXXX")
  cp -p -- "$f" "$recovery"
  tmp=$(mktemp "${f}.clear.XXXXXX")
  cp -p -- "$f" "$tmp"
`)
	if len(entries) == 0 {
		command.WriteString("  : > \"$tmp\"\n")
	} else {
		fmt.Fprintf(&command, "  awk -v keys=%s %s \"$recovery\" > \"$tmp\"\n", activityShellQuote(strings.Join(entries, ";")), activityShellQuote(activityLogFilterAwk))
	}
	// Keep a recoverable copy and detect writers before replacing a log. No backup
	// archive is touched. Only changed logs receive a recovery copy.
	command.WriteString(`  if cmp -s -- "$recovery" "$tmp"; then rm -- "$tmp" "$recovery"; return 0; fi
  if ! cmp -s -- "$f" "$recovery"; then
    rm -- "$tmp"
    printf 'Log changed during deletion; please retry\n' >&2
    return 1
  fi
  mv -- "$tmp" "$f"
  changed=$((changed + 1))
}
`)
	for _, logPath := range paths {
		fmt.Fprintf(&command, "filter_log %s\n", activityShellQuote(logPath))
	}
	if includeCron {
		command.WriteString("for f in /www/server/cron/*.log; do filter_log \"$f\"; done\n")
	}
	command.WriteString("printf 'LOG_FILES_CHANGED=%s\\n' \"$changed\"\n")
	return command.String()
}
