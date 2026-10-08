package main

import (
	"fmt"
	"regexp"
	"strings"

	"backup-monitor-backend/internal/backupops"
)

// Activity log selection and cleanup.
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

// Local backup activity readers.
// Read legacy category-first and date-first storage without scanning working SQL
// files, backup_restore or symlink targets.
const localBackupFindCommand = `find /www/backup -regextype posix-extended -type f \( -regex '/www/backup/((site|database)/.*|20[0-9]{2}-[0-9]{2}-[0-9]{2}/(site|database)/.*)\.(tar\.gz|sql\.gz|sql|zip)' -o -regex '/www/backup/(panel|20[0-9]{2}-[0-9]{2}-[0-9]{2}/panel)/[^/]+\.zip' \)`

// Parse CREATED records from both legacy and tagged daily-version archives.
// File timestamps refer to the start of the backup, not the log file's mtime.
const customLocalActivityAwk = `
  function json_escape(value, i, ch, escaped) {
    escaped = ""
    for (i = 1; i <= length(value); i++) {
      ch = substr(value, i, 1)
      if (ch == "\\") escaped = escaped "\\\\"
      else if (ch == "\"") escaped = escaped "\\\""
      else if (ch == "\r") escaped = escaped "\\r"
      else if (ch == "\t") escaped = escaped "\\t"
      else escaped = escaped ch
    }
    return escaped
  }
  $1 == "CREATED" {
    path = $2
    count = split(path, parts, "/")
    name = parts[count]
    label = ""
    if (path ~ /^\/www\/backup\/(20[0-9]{2}-[0-9]{2}-[0-9]{2}\/)?site\// &&
        match(name, /^web_([^\/[:space:]]+)_([0-9]{4})([0-9]{2})([0-9]{2})_([0-9]{2})([0-9]{2})([0-9]{2})(_(cron|manual)_[0-9a-f]{16})?_site\.tar\.gz$/, dt)) {
      label = "Backup Website: " dt[1]
    } else if (path ~ /^\/www\/backup\/(20[0-9]{2}-[0-9]{2}-[0-9]{2}\/)?database\// &&
        match(name, /^db_([^\/[:space:]]+)_([0-9]{4})([0-9]{2})([0-9]{2})_([0-9]{2})([0-9]{2})([0-9]{2})(_(cron|manual)_[0-9a-f]{16})?_mysql_data\.sql\.gz$/, dt)) {
      label = "Backup Database: " dt[1]
    }
    if (label != "") {
      duration = ($3 == "DURATION" && $4 ~ /^[0-9]+([.][0-9]+)?$/) ? $4 : "null"
      printf "{\"kind\":\"file\",\"name\":\"%s\",\"date\":\"%s-%s-%s\",\"time\":\"%s:%s:%s\",\"duration\":%s,\"status\":\"Successful\",\"path\":\"%s\"},", json_escape(label), dt[2], dt[3], dt[4], dt[5], dt[6], dt[7], duration, json_escape(path)
    }
  }
`

func customLocalActivityCommand() string {
	return fmt.Sprintf(`tail -n 500 %q %q 2>/dev/null | awk '%s' | sed 's/,$//'`,
		configuredLog("backup-site.log"), configuredLog("backup-database.log"), customLocalActivityAwk)
}

// Prefer the wrapper's whole-run result. Direct script runs have their own
// START/result pair; never invent a result from an archive or incomplete log.
const panelActivityAwk = `
function escape(value, i, ch, result) {
  result = ""
  for (i = 1; i <= length(value); i++) {
    ch = substr(value, i, 1)
    if (ch == "\\") result = result "\\\\"
    else if (ch == "\"") result = result "\\\""
    else if (ch == "\r") result = result "\\r"
    else if (ch == "\t") result = result "\\t"
    else result = result ch
  }
  return result
}
function emit(date, time, duration, status, id) {
  printf "{\"kind\":\"run\",\"name\":\"Sao lưu cấu hình aaPanel\",\"date\":\"%s\",\"time\":\"%s\",\"duration\":%.3f,\"status\":\"%s\",\"runId\":\"%s\"},", date, time, duration, status, id
}
function file(date, time, duration, id, archive, key, name) {
  if (archive !~ /^\// || archive !~ /[.]zip$/) return
  name = archive
  sub(/^.*\//, "", name)
  key = date "|" time "|" id "|" archive
  if (files[key]++) return
  printf "{\"kind\":\"file\",\"name\":\"Backup aaPanel: %s\",\"date\":\"%s\",\"time\":\"%s\",\"duration\":%.3f,\"status\":\"success\",\"runId\":\"%s\",\"path\":\"%s\"},", escape(name), date, time, duration, id, escape(archive)
}
/^MONITOR_RUN\|backup-panel\|/ {
  count = split($0, run, "|")
  if (run[3] !~ /^20[0-9]{2}-[0-9]{2}-[0-9]{2}$/ || run[4] !~ /^[0-9]{2}:[0-9]{2}:[0-9]{2}$/) next
  if (run[5] == "running") {
    wrapped = 1; date = ""
    wrapped_date = run[3]; wrapped_time = run[4]
    wrapped_id = count >= 7 && run[7] ~ /^[0-9]+-[0-9]+$/ ? run[7] : ""
    next
  }
  if ((run[5] == "success" || run[5] == "failed") && run[6] ~ /^[0-9]+$/) {
    key = count >= 7 && run[7] != "" ? run[7] : $0
    id = count >= 7 && run[7] ~ /^[0-9]+-[0-9]+$/ ? run[7] : ""
    if (!seen[key]++) emit(run[3], run[4], run[6], run[5], id)
    wrapped = 0
    wrapped_date = ""; wrapped_time = ""; wrapped_id = ""
    date = ""
  }
  next
}
/^\[20[0-9]{2}-[0-9]{2}-[0-9]{2} [0-9]{2}:[0-9]{2}:[0-9]{2}\] START: aaPanel configuration backup$/ {
  date = substr($0, 2, 10)
  time = substr($0, 13, 8)
  next
}
(date != "" || wrapped) && /^\[20[0-9]{2}-[0-9]{2}-[0-9]{2} [0-9]{2}:[0-9]{2}:[0-9]{2}\] (SUCCESS: archive=|FAILED: exit=)/ {
  if (match($0, / duration_ms=([0-9]+)(;|$)/, elapsed)) {
    if (!wrapped && date != "") emit(date, time, elapsed[1] / 1000, $0 ~ /\] SUCCESS:/ ? "success" : "failed")
    # Only a SUCCESS log proves that a ZIP was produced. Inventory, BACKUP=
    # lines and failed runs alone must never be presented as successful files.
    if (match($0, /\] SUCCESS: archive=(.*) duration_ms=[0-9]+$/, output)) {
      file(wrapped ? wrapped_date : date, wrapped ? wrapped_time : time,
           elapsed[1] / 1000, wrapped ? wrapped_id : "", output[1])
    }
    date = ""
  }
}
`

func panelActivityCommand() string {
	path := configuredLog("backup-panel.log")
	return fmt.Sprintf(`if [ -f %q ] && [ ! -L %q ]; then tail -n 10000 %q | awk '%s' | sed 's/,$//'; fi`, path, path, path, panelActivityAwk)
}

// Native date-only aaPanel ZIPs are inventory, not evidence of a successful run.
const panelArchiveActivityAwk = `
{
  count = split($0, parts, "/")
  name = parts[count]
  if (name !~ /^20[0-9]{2}-[0-9]{2}-[0-9]{2}[.]zip$/) next
  day = substr(name, 1, 10)
  if (!((count == 2 && parts[1] == "panel") || (count == 3 && parts[1] == day && parts[2] == "panel"))) next
  if (seen[day]++) next
  printf "{\"kind\":\"archive\",\"name\":\"Backup aaPanel: %s\",\"date\":\"%s\",\"time\":\"\",\"duration\":null,\"status\":\"unknown\"},", name, day
}
`

func panelArchiveActivityCommand() string {
	root := backupSystemSettings().BackupRoot
	return fmt.Sprintf(`if [ -d %q ] && [ ! -L %q ]; then find %q -maxdepth 3 -type f -name '*.zip' -printf '%%P\n' | awk '%s' | sed 's/,$//'; fi`, root, root, root, panelArchiveActivityAwk)
}
