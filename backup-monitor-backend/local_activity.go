package main

import "fmt"

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
      printf "{\"kind\":\"file\",\"name\":\"%s\",\"date\":\"%s-%s-%s\",\"time\":\"%s:%s:%s\",\"duration\":%s,\"status\":\"Successful\"},", json_escape(label), dt[2], dt[3], dt[4], dt[5], dt[6], dt[7], duration
    }
  }
`

func customLocalActivityCommand() string {
	return fmt.Sprintf(`tail -n 500 %q %q 2>/dev/null | awk '%s' | sed 's/,$//'`,
		configuredLog("backup-site.log"), configuredLog("backup-database.log"), customLocalActivityAwk)
}
