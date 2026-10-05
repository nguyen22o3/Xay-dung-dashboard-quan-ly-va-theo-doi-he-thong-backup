package main

import "fmt"

// Prefer the wrapper's whole-run result when present. Direct script runs have
// their own START/result pair and millisecond duration; never invent a result
// from an archive name or an incomplete log.
const panelActivityAwk = `
function emit(date, time, duration, status) {
  printf "{\"kind\":\"run\",\"name\":\"Sao lưu cấu hình aaPanel\",\"date\":\"%s\",\"time\":\"%s\",\"duration\":%.3f,\"status\":\"%s\"},", date, time, duration, status
}
/^MONITOR_RUN\|backup-panel\|/ {
  count = split($0, run, "|")
  if (run[3] !~ /^20[0-9]{2}-[0-9]{2}-[0-9]{2}$/ || run[4] !~ /^[0-9]{2}:[0-9]{2}:[0-9]{2}$/) next
  if (run[5] == "running") { wrapped = 1; date = ""; next }
  if ((run[5] == "success" || run[5] == "failed") && run[6] ~ /^[0-9]+$/) {
    key = count >= 7 && run[7] != "" ? run[7] : $0
    if (!seen[key]++) emit(run[3], run[4], run[6], run[5])
    wrapped = 0
    date = ""
  }
  next
}
/^\[20[0-9]{2}-[0-9]{2}-[0-9]{2} [0-9]{2}:[0-9]{2}:[0-9]{2}\] START: aaPanel configuration backup$/ {
  date = substr($0, 2, 10)
  time = substr($0, 13, 8)
  next
}
date != "" && /^\[20[0-9]{2}-[0-9]{2}-[0-9]{2} [0-9]{2}:[0-9]{2}:[0-9]{2}\] (SUCCESS: archive=|FAILED: exit=)/ {
  if (match($0, / duration_ms=([0-9]+)(;|$)/, elapsed)) {
    if (!wrapped) emit(date, time, elapsed[1] / 1000, $0 ~ /\] SUCCESS:/ ? "success" : "failed")
    date = ""
  }
}
`

func panelActivityCommand() string {
	path := configuredLog("backup-panel.log")
	return fmt.Sprintf(`if [ -f %q ] && [ ! -L %q ]; then tail -n 10000 %q | awk '%s' | sed 's/,$//'; fi`, path, path, path, panelActivityAwk)
}

// Date-only native aaPanel ZIPs provide evidence that a file exists, not a
// successful run or its start time/duration. This is inventory, never a log.
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
