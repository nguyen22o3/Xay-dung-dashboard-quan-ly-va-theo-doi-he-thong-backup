package main

import (
	"backup-monitor-backend/backupops"
	"backup-monitor-backend/hostagent"
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/joho/godotenv"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"net/mail"
	"net/url"
	"os"
	"os/signal"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
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
	for _, task := range managedCronTasks() {
		if !seen[task.Log] {
			paths = append(paths, task.Log)
			seen[task.Log] = true
		}
	}
	return activityClearCommand(entries, paths, true)
}

func activityClearCommand(entries, paths []string, includeCron bool) string {
	return activityClearCommandWithFilter(entries, paths, includeCron, "", activityLogFilterAwk)
}

func activityClearCommandWithFilter(entries, paths []string, includeCron bool, awkOptions, filter string) string {
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
		fmt.Fprintf(&command, "  awk %s-v keys=%s %s \"$recovery\" > \"$tmp\"\n", awkOptions, activityShellQuote(strings.Join(entries, ";")), activityShellQuote(filter))
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

func configuredLocalBackupRoots() []string {
	cfg := backupSystemSettings()
	roots := map[string]bool{cfg.BackupRoot: true}
	for _, loc := range cfg.CronLocations {
		if loc.Options != nil {
			roots[loc.Options.BackupRoot] = true
		}
	}
	result := []string{}
	for root := range roots {
		if root != "" {
			result = append(result, root)
		}
	}
	// Longest prefix first also handles pre-upgrade configurations defensively.
	sort.Slice(result, func(i, j int) bool {
		if len(result[i]) != len(result[j]) {
			return len(result[i]) > len(result[j])
		}
		return result[i] < result[j]
	})
	return result
}

func configuredLocalInventoryCommand() string {
	commands := []string{}
	for _, root := range configuredLocalBackupRoots() {
		quoted := activityShellQuote(root)
		command := strings.ReplaceAll(localBackupFindCommand, "/www/backup", regexp.QuoteMeta(root))
		command = strings.Replace(command, "find "+regexp.QuoteMeta(root), "find "+quoted, 1)
		commands = append(commands, "if [ -d "+quoted+" ] && [ ! -L "+quoted+" ]; then "+command+` -printf '%TY-%Tm-%Td %TH:%TM:%TS|%s|%p\n'; fi`)
	}
	return strings.Join(commands, "; ")
}

// Parse CREATED records from both legacy and tagged daily-version archives.
// File timestamps refer to the start of the backup, not the log file's mtime.
const customLocalActivityAwk = `
  BEGIN { storage_count = split(roots, storage, "[|]") }
  function backup_relative(value, i, prefix) {
    for (i = 1; i <= storage_count; i++) {
      prefix = storage[i] "/"
      if (index(value, prefix) == 1) return substr(value, length(prefix) + 1)
    }
    # Copied historical logs still name the original archive destination.
    # Preserve their evidence without adding the old directory to live inventory.
    if (value ~ /^\// && value ~ /\/web_[^\/]+[.]tar[.]gz$/ && value ~ /\/site\//) {
      sub(/^.*\/site\//, "site/", value); return value
    }
    if (value ~ /^\// && value ~ /\/db_[^\/]+[.]sql[.]gz$/ && value ~ /\/database\//) {
      sub(/^.*\/database\//, "database/", value); return value
    }
    return ""
  }
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
    relative = backup_relative(path)
    count = split(path, parts, "/")
    name = parts[count]
    label = ""
    if (relative ~ /^(20[0-9]{2}-[0-9]{2}-[0-9]{2}\/)?site\// &&
        match(name, /^web_([^\/[:space:]]+)_([0-9]{4})([0-9]{2})([0-9]{2})_([0-9]{2})([0-9]{2})([0-9]{2})(_(cron|manual)_[0-9a-f]{16})?_site\.tar\.gz$/, dt)) {
      label = "Backup Website: " dt[1]
    } else if (relative ~ /^(20[0-9]{2}-[0-9]{2}-[0-9]{2}\/)?database\// &&
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
	return fmt.Sprintf(`tail -q -n 500 %s 2>/dev/null | awk -v roots=%s '%s' | sed 's/,$//'`, managedLogArguments("backup-site", "backup-database"), activityShellQuote(strings.Join(configuredLocalBackupRoots(), "|")), customLocalActivityAwk)
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
	return fmt.Sprintf(`tail -q -n 10000 %s 2>/dev/null | awk '%s' | sed 's/,$//'`, managedLogArguments("backup-panel"), panelActivityAwk)
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
	commands := []string{}
	for _, root := range configuredLocalBackupRoots() {
		quoted := activityShellQuote(root)
		commands = append(commands, fmt.Sprintf(`if [ -d %s ] && [ ! -L %s ]; then find %s -maxdepth 3 -type f -name '*.zip' -printf '%%P\n'; fi`, quoted, quoted, quoted))
	}
	return "{ " + strings.Join(commands, "; ") + fmt.Sprintf(`; } | awk '%s' | sed 's/,$//'`, panelArchiveActivityAwk)
}

func resolvedBackupStatusCommand(template string) string {
	// Rewrite the legacy template once, then insert already-resolved private paths.
	command := configuredBackupCommand(template)
	for _, pair := range [][2]string{
		{"__DRIVE_ACTIVITY_COMMAND__", driveActivityCommand()},
		{"__CUSTOM_LOCAL_ACTIVITY_COMMAND__", customLocalActivityCommand()},
		{"__BACKUP_RUN_ACTIVITY_COMMAND__", backupRunActivityCommand()},
		{"__PANEL_ACTIVITY_COMMAND__", panelActivityCommand()},
		{"__PANEL_ARCHIVE_ACTIVITY_COMMAND__", panelArchiveActivityCommand()},
	} {
		command = strings.ReplaceAll(command, pair[0], pair[1])
	}
	return command
}

const adminPasswordCost = 12

var errAdminExists = errors.New("administrator already exists")
var adminUsernamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{2,63}$`)
var adminHashPattern = regexp.MustCompile(`^\$2[aby]\$(12|13|14)\$[./A-Za-z0-9]{53}$`)

type adminAccount struct {
	Version      int    `json:"version"`
	Username     string `json:"username"`
	PasswordHash string `json:"passwordHash"`
	CreatedAt    string `json:"createdAt"`
}

// One account per installation, independent of the image.
// A malformed/missing mounted state is never replaced by an env password.
type adminAccountStore struct {
	mu      sync.RWMutex
	path    string
	account *adminAccount
}

func openAdminAccount(path string) (*adminAccountStore, error) {
	s := &adminAccountStore{path: path}
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Size() > 4096 {
		return nil, errors.New("cannot read regular administrator account file")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("administrator account file must have permissions 0600")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, errors.New("cannot open administrator account file")
	}
	defer f.Close()
	var a adminAccount
	d := json.NewDecoder(io.LimitReader(f, 4097))
	d.DisallowUnknownFields()
	if err := d.Decode(&a); err != nil {
		return nil, errors.New("invalid administrator account file")
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return nil, errors.New("invalid administrator account file")
	}
	cost, hashErr := bcrypt.Cost([]byte(a.PasswordHash))
	_, dateErr := time.Parse(time.RFC3339, a.CreatedAt)
	if a.Version != 1 || strings.TrimSpace(a.Username) == "" || len(a.Username) > 256 || !adminHashPattern.MatchString(a.PasswordHash) || hashErr != nil || cost < adminPasswordCost || cost > 14 || dateErr != nil {
		return nil, errors.New("invalid administrator account metadata/hash")
	}
	s.account = &a
	return s, nil
}

func (s *adminAccountStore) username() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.account == nil {
		return ""
	}
	return s.account.Username
}

func (s *adminAccountStore) verify(username, password string) bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	a := s.account
	s.mu.RUnlock()
	if a == nil || len(password) > 72 {
		return false
	}
	// Run bcrypt even for an incorrect username to avoid a username timing oracle.
	passwordOK := bcrypt.CompareHashAndPassword([]byte(a.PasswordHash), []byte(password)) == nil
	return constantTimeEqual(username, a.Username) && passwordOK
}

func (s *adminAccountStore) create(username, password string, legacy bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.account != nil {
		return errAdminExists
	}
	if (!legacy && !adminUsernamePattern.MatchString(username)) || strings.TrimSpace(username) == "" || len(username) > 256 {
		return errors.New("Tên đăng nhập cần 3–64 ký tự: chữ, số, dấu chấm, gạch dưới hoặc gạch ngang")
	}
	// bcrypt has a 72-byte limit; do not silently truncate UTF-8 passwords.
	if len(password) < 12 || len(password) > 72 || strings.TrimSpace(password) == "" {
		return errors.New("Mật khẩu cần từ 12 đến 72 byte (ký tự có dấu có thể chiếm nhiều byte)")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), adminPasswordCost)
	if err != nil {
		return errors.New("cannot hash administrator password")
	}
	a := adminAccount{Version: 1, Username: username, PasswordHash: string(hash), CreatedAt: time.Now().UTC().Format(time.RFC3339)}
	data, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), ".admin-account-*")
	if err != nil {
		return errors.New("cannot create administrator account; check persistent directory permissions")
	}
	temp := f.Name()
	defer os.Remove(temp)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(append(data, '\n'))
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return errors.New("cannot persist administrator account")
	}
	// Publish a fully written file atomically WITHOUT overwriting. The hard-link
	// also makes simultaneous setup requests from separate processes safe.
	if err := os.Link(temp, s.path); err != nil {
		if os.IsExist(err) {
			// Another instance won setup. Adopt its completed state, not ours.
			if existing, loadErr := openAdminAccount(s.path); loadErr == nil {
				s.account = existing.account
			} else {
				return loadErr
			}
			return errAdminExists
		}
		return errors.New("cannot publish administrator account; persistent filesystem must support hard links")
	}
	s.account = &a
	return nil
}

func initializeAdminAccount() (*adminAccountStore, error) {
	path := strings.TrimSpace(os.Getenv("ADMIN_ACCOUNT_FILE"))
	if path == "" {
		path = "admin_account.json"
	}
	s, err := openAdminAccount(path)
	if err != nil {
		return nil, err
	}
	user, password := os.Getenv("ADMIN_USER"), os.Getenv("ADMIN_PASSWORD")
	if s.username() == "" && (user != "" || password != "") {
		if user == "" || password == "" {
			return nil, errors.New("legacy ADMIN_USER and ADMIN_PASSWORD must both be set, or both absent for first-run setup")
		}
		if err := s.create(user, password, true); err != nil {
			return nil, fmt.Errorf("migrate legacy administrator: %w", err)
		}
	}
	removeLegacy, err := explicitBoolEnv("AUTH_REMOVE_LEGACY_ENV")
	if err != nil {
		return nil, err
	}
	if removeLegacy && s.username() != "" {
		if err := removeLegacyAdminEnv(); err != nil {
			return nil, err
		}
	}
	os.Unsetenv("ADMIN_USER")
	os.Unsetenv("ADMIN_PASSWORD")
	return s, nil
}

// Opt-in credential migration. Preserve other settings and write the account first.
// A failed rewrite stops startup; the saved account still permits a safe retry.
func removeLegacyAdminEnv() error {
	info, err := os.Lstat(".env")
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("cannot sanitize regular runtime .env")
	}
	values, err := godotenv.Read(".env")
	if err != nil {
		return errors.New("cannot parse runtime .env for administrator migration")
	}
	_, hasUser := values["ADMIN_USER"]
	_, hasPassword := values["ADMIN_PASSWORD"]
	if !hasUser && !hasPassword {
		return nil
	}
	delete(values, "ADMIN_USER")
	delete(values, "ADMIN_PASSWORD")
	contents, err := godotenv.Marshal(values)
	if err != nil {
		return errors.New("cannot serialize runtime .env")
	}
	f, err := os.CreateTemp(".", ".admin-env-*")
	if err != nil {
		return errors.New("cannot sanitize runtime .env")
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err == nil {
		_, err = f.WriteString(contents + "\n")
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil || closeErr != nil || os.Rename(f.Name(), ".env") != nil {
		return errors.New("cannot replace runtime .env after administrator migration")
	}
	return nil
}

type adminCredentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

func readAdminCredentials(c *gin.Context) (adminCredentials, error) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
	d := json.NewDecoder(c.Request.Body)
	d.DisallowUnknownFields()
	var req adminCredentials
	if err := d.Decode(&req); err != nil {
		return req, err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return req, errors.New("unexpected trailing data")
	}
	return req, nil
}

func registerAdminSetup(r *gin.Engine, cfg AppConfig, limiter *loginRateLimiter) {
	r.GET("/api/auth/status", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusOK, gin.H{"setupRequired": cfg.Account.username() == ""})
	})
	r.POST("/api/auth/setup", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		// CORS alone does not block simple cross-site POSTs. Require the browser's
		// exact configured origin, even when the proxy considers it same-origin.
		allowed := false
		for _, origin := range cfg.CORSAllowOrigins {
			allowed = allowed || c.GetHeader("Origin") == origin
		}
		if !allowed {
			c.JSON(http.StatusForbidden, gin.H{"error": "Hãy tạo tài khoản từ địa chỉ dashboard đã cấu hình"})
			return
		}
		if cfg.Account.username() != "" {
			c.JSON(http.StatusConflict, gin.H{"error": "Quản trị viên đã được tạo. Hãy đăng nhập"})
			return
		}
		key, now := remoteAddressKey(c.Request.RemoteAddr), time.Now()
		if !limiter.allowed(key, now) {
			c.Header("Retry-After", "300")
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "Thử quá nhiều lần, vui lòng chờ 5 phút"})
			return
		}
		req, err := readAdminCredentials(c)
		if err == nil {
			err = cfg.Account.create(req.Username, req.Password, false)
		}
		if errors.Is(err, errAdminExists) {
			c.JSON(http.StatusConflict, gin.H{"error": "Quản trị viên đã được tạo. Hãy đăng nhập"})
			return
		}
		if err != nil {
			limiter.recordFailure(key, now)
			// No request values/passwords are included in errors or logs.
			c.JSON(http.StatusBadRequest, gin.H{"error": "Không thể tạo tài khoản. Tên đăng nhập: 3–64 chữ/số/._-. Mật khẩu: 12–72 byte. Kiểm tra quyền ghi thư mục dữ liệu nếu vẫn lỗi"})
			return
		}
		limiter.reset(key)
		// Login separately: retrying a lost setup response cannot create a second
		// account or overwrite the completed first one.
		c.JSON(http.StatusCreated, gin.H{"status": "created"})
	})
}

type alertScriptUpdate struct {
	Task   managedCronTask
	Script string
}

// Alert settings apply to every existing integrity-check instance, without
// replacing the destination or wrapper selected when its cron was created.
func integrityAlertScriptUpdates(settings AlertSettings) []alertScriptUpdate {
	updates := []alertScriptUpdate{}
	cfg := backupSystemSettings()
	for _, task := range managedCronTasks() {
		if task.ID != "integrity-check" || task.InstanceKey == "" {
			continue
		}
		local, err := remoteCronSettings(cfg, task.ID, task.InstanceKey)
		if err != nil {
			continue
		}
		script := buildIntegrityAlertScript(settings, local)
		script = strings.Replace(script, "#!/bin/bash\n", "#!/bin/bash\nexport BACKUP_MONITOR_CRON="+activityShellQuote(task.selector())+"\n", 1)
		updates = append(updates, alertScriptUpdate{Task: task, Script: script})
	}
	return updates
}

func stageIntegrityAlertUpdates(updates []alertScriptUpdate) string {
	var command strings.Builder
	for _, update := range updates {
		// Read cron only. Both paused entries and instance-specific wrappers
		// retain their schedules; removed tasks must not be recreated by Save.
		fmt.Fprintf(&command, `if printf '%%s\n' "$CURRENT_CRON" | awk -v script=%s -v wrapper=%s -v id=%s '
  { if (index($0, "# backup-monitor-paused ") == 1) sub(/^# backup-monitor-paused /, "") }
  NF >= 7 && $1 !~ /^#/ && $6 == "/bin/bash" && ($7 == script || ($7 == wrapper && $8 == id)) { found = 1 }
  END { exit !found }
'; then
  stage_alert_script %s %s 1
fi
`, activityShellQuote(update.Task.Script), activityShellQuote(update.Task.wrapper()), activityShellQuote(update.Task.ID),
			activityShellQuote(update.Task.Script), activityShellQuote(encodeForShell(update.Script)))
	}
	return command.String()
}

// Alert settings are stored locally. Secret values are accepted on writes but are
// never serialized in API responses.
type AlertSettings struct {
	TelegramToken   string   `json:"telegramToken"`
	TelegramChat    string   `json:"telegramChat"`
	DiscordWebhook  string   `json:"discordWebhook"`
	SmtpEmail       string   `json:"smtpEmail,omitempty"`
	SmtpPassword    string   `json:"smtpPassword,omitempty"`
	TargetEmail     string   `json:"targetEmail"`
	Threshold       int      `json:"threshold"`
	MonitorPaths    []string `json:"monitorPaths,omitempty"`
	TelegramEnabled *bool    `json:"telegramEnabled,omitempty"`
	DiscordEnabled  *bool    `json:"discordEnabled,omitempty"`
	SmtpEnabled     *bool    `json:"smtpEnabled,omitempty"`
}

type AlertSettingsResponse struct {
	TelegramChat         string   `json:"telegramChat"`
	TargetEmail          string   `json:"targetEmail"`
	Threshold            int      `json:"threshold"`
	MonitorPaths         []string `json:"monitorPaths"`
	TelegramConfigured   bool     `json:"telegramConfigured"`
	DiscordConfigured    bool     `json:"discordConfigured"`
	SmtpConfigured       bool     `json:"smtpConfigured"`
	SmtpSenderConfigured bool     `json:"smtpSenderConfigured"`
	TelegramEnabled      bool     `json:"telegramEnabled"`
	DiscordEnabled       bool     `json:"discordEnabled"`
	SmtpEnabled          bool     `json:"smtpEnabled"`
}

func alertChannelEnabled(value *bool, configured bool) bool {
	if value == nil {
		return configured
	}
	return *value && configured
}

func effectiveMonitorPaths(settings AlertSettings) []string {
	if settings.MonitorPaths != nil {
		return append([]string{}, settings.MonitorPaths...)
	}
	return configuredLocalBackupRoots()
}

var alertMonitorPathRe = regexp.MustCompile(`^/[A-Za-z0-9_./-]+$`)

func validateAlertMonitorPaths(paths []string) error {
	return validateAlertMonitorPathsWithin(paths, configuredLocalBackupRoots())
}

func validateAlertMonitorPathsWithin(paths, allowed []string) error {
	if len(paths) == 0 || len(paths) > 8 {
		return fmt.Errorf("chọn từ 1 đến 8 thư mục backup để giám sát")
	}
	seen := map[string]bool{}
	for _, selected := range paths {
		if len(selected) > 256 || selected == "/" || !alertMonitorPathRe.MatchString(selected) || path.Clean(selected) != selected {
			return fmt.Errorf("đường dẫn giám sát không hợp lệ: %q", selected)
		}
		permitted := false
		for _, root := range allowed {
			if selected == root || strings.HasPrefix(selected, strings.TrimRight(root, "/")+"/") {
				permitted = true
				break
			}
		}
		if !permitted {
			return fmt.Errorf("thư mục giám sát phải nằm trong nơi lưu backup đã cấu hình: %q", selected)
		}
		if seen[selected] {
			return fmt.Errorf("thư mục giám sát bị chọn trùng: %q", selected)
		}
		seen[selected] = true
	}
	return nil
}

var (
	telegramTokenRe = regexp.MustCompile(`^[0-9]{5,}:[A-Za-z0-9_-]{20,}$`)
	telegramChatRe  = regexp.MustCompile(`^(?:-?[0-9]{1,20}|@[A-Za-z0-9_]{5,32})$`)
)

func emailAlertConfigured(settings AlertSettings) bool {
	return settings.SmtpEmail != "" && settings.SmtpPassword != "" && settings.TargetEmail != ""
}

func safeAlertSettings(settings AlertSettings) AlertSettingsResponse {
	emailConfigured := emailAlertConfigured(settings)
	return AlertSettingsResponse{
		TelegramChat:         settings.TelegramChat,
		TargetEmail:          settings.TargetEmail,
		Threshold:            settings.Threshold,
		MonitorPaths:         effectiveMonitorPaths(settings),
		TelegramConfigured:   settings.TelegramToken != "",
		DiscordConfigured:    settings.DiscordWebhook != "",
		SmtpConfigured:       emailConfigured,
		SmtpSenderConfigured: settings.SmtpEmail != "" && settings.SmtpPassword != "",
		TelegramEnabled:      alertChannelEnabled(settings.TelegramEnabled, settings.TelegramToken != ""),
		DiscordEnabled:       alertChannelEnabled(settings.DiscordEnabled, settings.DiscordWebhook != ""),
		SmtpEnabled:          alertChannelEnabled(settings.SmtpEnabled, emailConfigured),
	}
}

// SMTP credentials belong to the deployment, not to a browser request. Keep
// existing JSON credentials as a fallback until the operator configures env.
func loadSMTPSenderFromEnv() (string, string, error) {
	email := strings.TrimSpace(os.Getenv("SMTP_EMAIL"))
	password := strings.ReplaceAll(os.Getenv("SMTP_PASSWORD"), " ", "")
	if email == "" && password == "" {
		return "", "", nil
	}
	if email == "" || password == "" {
		return "", "", fmt.Errorf("configure SMTP_EMAIL and SMTP_PASSWORD together on the backend")
	}
	if validateEmailAddress(email) != nil {
		return "", "", fmt.Errorf("invalid backend SMTP_EMAIL")
	}
	if strings.ContainsAny(password, "\x00\r\n") || len(password) > 1024 {
		return "", "", fmt.Errorf("invalid backend SMTP_PASSWORD")
	}
	return email, password, nil
}

func effectiveAlertSettings(settings AlertSettings) AlertSettings {
	if runtimeConfig.SMTPEmail != "" && runtimeConfig.SMTPPassword != "" {
		settings.SmtpEmail = runtimeConfig.SMTPEmail
		settings.SmtpPassword = runtimeConfig.SMTPPassword
	}
	return settings
}

func validateEmailAddress(value string) error {
	parsed, err := mail.ParseAddress(value)
	if err != nil || parsed.Address != value {
		return fmt.Errorf("invalid email address")
	}
	return nil
}

func validateDiscordWebhook(value string) error {
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("invalid Discord webhook URL")
	}
	host := strings.ToLower(u.Hostname())
	if host != "discord.com" && host != "discordapp.com" {
		return fmt.Errorf("Discord webhook must use discord.com")
	}
	if !strings.HasPrefix(u.EscapedPath(), "/api/webhooks/") {
		return fmt.Errorf("invalid Discord webhook path")
	}
	return nil
}

func validateAlertSettings(settings AlertSettings) error {
	if settings.Threshold < 1 || settings.Threshold > 365 {
		return fmt.Errorf("threshold must be between 1 and 365")
	}
	if err := validateAlertMonitorPaths(effectiveMonitorPaths(settings)); err != nil {
		return err
	}
	if len(settings.TelegramToken) > 256 || (settings.TelegramToken != "" && !telegramTokenRe.MatchString(settings.TelegramToken)) {
		return fmt.Errorf("invalid Telegram token")
	}
	if len(settings.TelegramChat) > 64 || (settings.TelegramChat != "" && !telegramChatRe.MatchString(settings.TelegramChat)) {
		return fmt.Errorf("invalid Telegram chat")
	}
	if (settings.TelegramToken == "") != (settings.TelegramChat == "") {
		return fmt.Errorf("Telegram token and chat must be configured together")
	}
	if len(settings.DiscordWebhook) > 2048 {
		return fmt.Errorf("Discord webhook is too long")
	}
	if settings.DiscordWebhook != "" {
		if err := validateDiscordWebhook(settings.DiscordWebhook); err != nil {
			return err
		}
	}
	if strings.ContainsAny(settings.SmtpPassword, "\x00\r\n") || len(settings.SmtpPassword) > 1024 {
		return fmt.Errorf("invalid SMTP password")
	}
	if settings.SmtpEmail != "" {
		if err := validateEmailAddress(settings.SmtpEmail); err != nil {
			return fmt.Errorf("invalid SMTP sender email")
		}
	}
	if settings.TargetEmail != "" {
		if err := validateEmailAddress(settings.TargetEmail); err != nil {
			return fmt.Errorf("invalid target email")
		}
	}
	if (settings.SmtpEmail == "") != (settings.SmtpPassword == "") {
		return fmt.Errorf("SMTP sender and password must be configured together on the backend")
	}
	if settings.SmtpEnabled != nil && *settings.SmtpEnabled {
		if settings.SmtpEmail == "" || settings.SmtpPassword == "" {
			return fmt.Errorf("Quản trị viên chưa cấu hình email gửi trên máy chủ")
		}
		if settings.TargetEmail == "" {
			return fmt.Errorf("Nhập email nhận cảnh báo trước khi bật thông báo email")
		}
	}
	return nil
}

func readAlertSettings(path string) (AlertSettings, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return AlertSettings{Threshold: 14}, nil
	}
	if err != nil {
		return AlertSettings{}, err
	}
	var settings AlertSettings
	if err := json.Unmarshal(data, &settings); err != nil {
		return AlertSettings{}, fmt.Errorf("decode alert settings: %w", err)
	}
	return settings, nil
}

func mergeAlertSecrets(current, incoming AlertSettings) AlertSettings {
	if incoming.MonitorPaths == nil {
		incoming.MonitorPaths = current.MonitorPaths
	}
	if incoming.TelegramEnabled == nil {
		incoming.TelegramEnabled = current.TelegramEnabled
	}
	if incoming.DiscordEnabled == nil {
		incoming.DiscordEnabled = current.DiscordEnabled
	}
	if incoming.SmtpEnabled == nil {
		incoming.SmtpEnabled = current.SmtpEnabled
	}
	if strings.TrimSpace(incoming.TelegramToken) == "" {
		incoming.TelegramToken = current.TelegramToken
	}
	if strings.TrimSpace(incoming.DiscordWebhook) == "" {
		incoming.DiscordWebhook = current.DiscordWebhook
	}
	// Ignore legacy sender fields even if a client submits them. Only server
	// configuration (or the existing on-disk fallback) may select the sender.
	incoming.SmtpEmail = current.SmtpEmail
	incoming.SmtpPassword = current.SmtpPassword
	incoming.TelegramToken = strings.TrimSpace(incoming.TelegramToken)
	incoming.TelegramChat = strings.TrimSpace(incoming.TelegramChat)
	incoming.DiscordWebhook = strings.TrimSpace(incoming.DiscordWebhook)
	incoming.SmtpEmail = strings.TrimSpace(incoming.SmtpEmail)
	incoming.TargetEmail = strings.TrimSpace(incoming.TargetEmail)
	return incoming
}

func writeFileAtomic0600(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".alert-settings-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if err := tmp.Chmod(0600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	return os.Chmod(path, 0600)
}

func encodeForShell(value string) string {
	return base64.StdEncoding.EncodeToString([]byte(value))
}

const localRetentionCopies = 14

// A deletion is expected only when at least keep newer backup artifacts for
// the same target still exist. The timestamp comes from the backup filename,
// not mtime, so copying an old backup back cannot make it look new.
const rotationDecisionShell = `backup_key() {
    local name="${1##*/}"
    if [[ "$name" =~ ^(.*)(20[0-9]{2}-?[0-9]{2}-?[0-9]{2})([_-]([0-9]{6}))? ]]; then
        local prefix="${BASH_REMATCH[1]}"
        local day="${BASH_REMATCH[2]//-/}"
        local backup_time="${BASH_REMATCH[4]:-000000}"
        printf '%s|%s%s\n' "$prefix" "$day" "$backup_time"
        return 0
    fi
    return 1
}

is_expected_rotation() {
    local deleted="$1" keep="$2"
    local dir="${deleted%/*}" name="${deleted##*/}"
    local key prefix stamp candidate other other_prefix other_stamp
    local newer=0
    key=$(backup_key "$name") || return 1
    IFS='|' read -r prefix stamp <<< "$key"

    for candidate in "$dir"/*; do
        [[ -f "$candidate" && -s "$candidate" && ! -L "$candidate" ]] || continue
        case "$candidate" in
            *.zip|*.tar.gz|*.sql.gz|*.gz|*.sql) ;;
            *) continue ;;
        esac
        other=$(backup_key "${candidate##*/}") || continue
        IFS='|' read -r other_prefix other_stamp <<< "$other"
        if [[ "$other_prefix" == "$prefix" && "$other_stamp" > "$stamp" ]]; then
            ((newer += 1))
            if ((newer >= keep)); then
                return 0
            fi
        fi
    done
    return 1
}

# Only the local backup script writes these path-specific, short-lived markers.
# Consume the marker once so an unrelated later deletion still raises an alert.
is_script_rotation() {
    local deleted="$1" digest marker issued now
    digest=$(printf '%s' "$deleted" | sha256sum)
    digest="${digest%% *}"
    marker="${BACKUP_MONITOR_ROTATION_DIR:-/run/backup-monitor-rotations}/$digest"
    [[ -f "$marker" && ! -L "$marker" ]] || return 1
    issued=$(<"$marker")
    rm -f -- "$marker"
    [[ "$issued" =~ ^[0-9]+$ ]] || return 1
    now=$(date +%s)
    (( now >= issued && now - issued <= 120 ))
}`

func buildAlertScripts(settings AlertSettings) (string, string) {
	return buildAlertScriptsForRoots(settings, effectiveMonitorPaths(settings))
}

func buildAlertScriptsForRoots(settings AlertSettings, roots []string) (string, string) {
	return buildIntegrityAlertScript(settings, backupSystemSettings()), buildRealtimeAlertScript(settings, roots)
}

func buildRealtimeAlertScript(settings AlertSettings, roots []string) string {
	quotedRoots := make([]string, 0, len(roots))
	for _, root := range roots {
		root = strings.TrimRight(root, "/")
		if root != "" {
			quotedRoots = append(quotedRoots, activityShellQuote(root))
		}
	}
	return fmt.Sprintf(`#!/bin/bash
set -u
%s
%s
WATCH_ROOT_CANDIDATES=(%s)
WATCH_ROOTS=()
for ROOT in "${WATCH_ROOT_CANDIDATES[@]}"; do
    [[ -d "$ROOT" && ! -L "$ROOT" ]] && WATCH_ROOTS+=("$ROOT")
done
if (( ${#WATCH_ROOTS[@]} == 0 )); then
    echo 'No configured backup directory is available to monitor' >&2
    exit 1
fi
send_delete_alert() {
    local file="$1" msg
    msg="THONG BAO: File backup [$file] vua bi XOA khoi may chu! Thoi gian: $(date)"
    if [ "$DISCORD_ENABLED" = 1 ] && [ -n "$DISCORD_WEBHOOK" ]; then curl -fsS -H "Content-Type: application/json" --data "{\"content\":\"$msg\"}" "$DISCORD_WEBHOOK" >/dev/null; fi
    if [ "$TELEGRAM_ENABLED" = 1 ] && [ -n "$TELEGRAM_TOKEN" ]; then curl -fsS -X POST "https://api.telegram.org/bot${TELEGRAM_TOKEN}/sendMessage" --data-urlencode "chat_id=$TELEGRAM_CHAT" --data-urlencode "text=$msg" >/dev/null; fi
    if [ "$SMTP_ENABLED" = 1 ] && [ -n "$SMTP_PASSWORD" ]; then printf 'From: %%s\nTo: %%s\nSubject: [URGENT] File Deleted\n\n%%s\n' "$SMTP_EMAIL" "$TARGET_EMAIL" "$msg" | curl -fsS --url 'smtps://smtp.gmail.com:465' --ssl-reqd --mail-from "$SMTP_EMAIL" --mail-rcpt "$TARGET_EMAIL" --user "$SMTP_EMAIL:$SMTP_PASSWORD" -T - >/dev/null; fi
}

check_panel_zip_replacement() {
    local file="$1" deleted_at="$2" attempt modified
    # Do not keep the main monitor lock or inotify pipe open while checking.
    exec 9>&-
    for ((attempt = 0; attempt < 12; attempt++)); do
        sleep 5
        if [[ -f "$file" && -s "$file" && ! -L "$file" ]]; then
            modified=$(stat -c %%Y -- "$file" 2>/dev/null || true)
            if [[ "$modified" =~ ^[0-9]+$ ]] && (( modified >= deleted_at )) && unzip -tqq "$file" >/dev/null 2>&1; then
                return 0
            fi
        fi
    done
    send_delete_alert "$file"
}

exec 9>/run/backup-monitor-inotify.lock
flock -n 9 || exit 0
inotifywait -m -r -e delete --format '%%w%%f' "${WATCH_ROOTS[@]}" 2>/dev/null | while IFS= read -r FILE
do
    if [[ ! "$FILE" =~ \.(zip|tar\.gz|gz|sql)$ ]]; then
        continue
    fi

    RELATIVE_FILE=''
    for ROOT in "${WATCH_ROOTS[@]}"; do
        if [[ "$FILE" == "$ROOT"/* ]]; then
            RELATIVE_FILE="${FILE#"$ROOT"/}"
            break
        fi
    done
    [[ -n "$RELATIVE_FILE" ]] || continue

    # aaPanel removes SQL work files after packing its dated ZIP.
    if [[ "$RELATIVE_FILE" =~ ^panel/[0-9]{4}-[0-9]{2}-[0-9]{2}/data/ ]]; then
        continue
    fi

    # Script-controlled same-day replacement or aaPanel's count-based rotation.
    if is_script_rotation "$FILE" || is_expected_rotation "$FILE" %d; then
        continue
    fi

    if [[ "$RELATIVE_FILE" == "panel/$(date +%%F).zip" ]]; then
        check_panel_zip_replacement "$FILE" "$(date +%%s)" </dev/null >/dev/null 2>&1 &
        continue
    fi

    send_delete_alert "$FILE"
 done`, alertScriptVariables(settings), rotationDecisionShell, strings.Join(quotedRoots, " "), localRetentionCopies)
}

func alertScriptVariables(settings AlertSettings) string {
	variables := fmt.Sprintf(`decode_value() { printf '%%s' "$1" | base64 -d; }
DISCORD_WEBHOOK=$(decode_value '%s')
TELEGRAM_TOKEN=$(decode_value '%s')
TELEGRAM_CHAT=$(decode_value '%s')
SMTP_EMAIL=$(decode_value '%s')
SMTP_PASSWORD=$(decode_value '%s')
TARGET_EMAIL=$(decode_value '%s')`,
		encodeForShell(settings.DiscordWebhook),
		encodeForShell(settings.TelegramToken),
		encodeForShell(settings.TelegramChat),
		encodeForShell(settings.SmtpEmail),
		encodeForShell(settings.SmtpPassword),
		encodeForShell(settings.TargetEmail),
	)
	if alertChannelEnabled(settings.DiscordEnabled, settings.DiscordWebhook != "") {
		variables += "\nDISCORD_ENABLED=1"
	} else {
		variables += "\nDISCORD_ENABLED=0"
	}
	if alertChannelEnabled(settings.TelegramEnabled, settings.TelegramToken != "") {
		variables += "\nTELEGRAM_ENABLED=1"
	} else {
		variables += "\nTELEGRAM_ENABLED=0"
	}
	if alertChannelEnabled(settings.SmtpEnabled, emailAlertConfigured(settings)) {
		variables += "\nSMTP_ENABLED=1"
	} else {
		variables += "\nSMTP_ENABLED=0"
	}
	return variables
}

func buildIntegrityAlertScript(settings AlertSettings, cfg BackupSystemSettings) string {
	return fmt.Sprintf(`#!/bin/bash
set -u
%s
if ! BACKUP_DIRS=$(/usr/bin/rclone lsd %s --config %s); then
    echo "[$(date '+%%F %%T')] Không thể đọc Google Drive; chưa xác nhận số thư mục" >&2
    exit 1
fi
BACKUP_COUNT=$(printf '%%s\n' "$BACKUP_DIRS" | sed '/^$/d' | wc -l)
echo "[$(date '+%%F %%T')] Da kiem tra Drive: $BACKUP_COUNT thu muc Backup"
if [ "$BACKUP_COUNT" -lt %d ]; then
    MSG="CANH BAO THIEU THU MUC BACKUP: Google Drive hien co $BACKUP_COUNT/%d thu muc. Vui long kiem tra!"
	echo "[$(date '+%%F %%T')] CẢNH BÁO: $MSG"
    if [ "$DISCORD_ENABLED" = 1 ] && [ -n "$DISCORD_WEBHOOK" ]; then curl -fsS -H "Content-Type: application/json" --data "{\"content\":\"$MSG\"}" "$DISCORD_WEBHOOK" >/dev/null; fi
    if [ "$TELEGRAM_ENABLED" = 1 ] && [ -n "$TELEGRAM_TOKEN" ]; then curl -fsS -X POST "https://api.telegram.org/bot${TELEGRAM_TOKEN}/sendMessage" --data-urlencode "chat_id=$TELEGRAM_CHAT" --data-urlencode "text=$MSG" >/dev/null; fi
    if [ "$SMTP_ENABLED" = 1 ] && [ -n "$SMTP_PASSWORD" ]; then printf 'From: %%s\nTo: %%s\nSubject: [ALERT] Backup Monitor\n\n%%s\n' "$SMTP_EMAIL" "$TARGET_EMAIL" "$MSG" | curl -fsS --url 'smtps://smtp.gmail.com:465' --ssl-reqd --mail-from "$SMTP_EMAIL" --mail-rcpt "$TARGET_EMAIL" --user "$SMTP_EMAIL:$SMTP_PASSWORD" -T - >/dev/null; fi
    exit 1
fi
exit 0`, alertScriptVariables(settings), activityShellQuote(cfg.DriveRemote+":"+strings.TrimRight(cfg.DriveFolder, "/")+"/"),
		activityShellQuote(remoteInstallation().RcloneConfig), settings.Threshold, settings.Threshold)
}

func buildAlertInstallCommand(integrityScript, realtimeScript string, monitorPaths []string, updates ...alertScriptUpdate) string {
	quotedPaths := make([]string, 0, len(monitorPaths))
	for _, selected := range monitorPaths {
		quotedPaths = append(quotedPaths, activityShellQuote(selected))
	}
	return fmt.Sprintf(`set -eu
umask 077
command -v inotifywait >/dev/null 2>&1 || { dnf install epel-release -y >/dev/null 2>&1 && dnf install inotify-tools -y >/dev/null 2>&1; }
MONITOR_PATHS=(%s)
for ROOT in "${MONITOR_PATHS[@]}"; do
  [ -d "$ROOT" ] && [ ! -L "$ROOT" ] && [ "$(realpath -e -- "$ROOT")" = "$ROOT" ] || { printf 'Invalid or missing monitor directory: %%s\n' "$ROOT" >&2; exit 1; }
done
SCRIPTS_DIR=/root/scripts
[ ! -L "$SCRIPTS_DIR" ] || { printf 'Scripts directory must not be a symlink\n' >&2; exit 1; }
install -d -m 700 "$SCRIPTS_DIR"
[ "$(stat -c '%%u' "$SCRIPTS_DIR")" = 0 ] || { printf 'Scripts directory must be root-owned\n' >&2; exit 1; }
exec 8>/root/.backup-monitor-crontab.lock
flock -x 8
CURRENT_CRON=$(crontab -l 2>/dev/null || true)
STAGED_FILES=()
SCRIPT_TARGETS=()
trap 'if (( ${#STAGED_FILES[@]} )); then rm -f -- "${STAGED_FILES[@]}"; fi' EXIT
stage_alert_script() {
  local target="$1" content="$2" required="$3" dir tmp
  dir=$(dirname -- "$target")
  [ -d "$dir" ] && [ "$(realpath -e -- "$dir")" = "$dir" ] && [ "$(stat -c '%%u' "$dir")" = 0 ] || { printf 'Invalid scripts directory\n' >&2; return 1; }
  [ ! -L "$target" ] || { printf 'Script must not be a symlink\n' >&2; return 1; }
  if [ -e "$target" ]; then
    [ -f "$target" ] && [ "$(stat -c '%%u' "$target")" = 0 ] || { printf 'Script must be a root-owned regular file\n' >&2; return 1; }
  elif [ "$required" = 1 ]; then
    printf 'Scheduled integrity-check script is missing; restore it in Cron before saving settings\n' >&2
    return 1
  fi
  tmp=$(mktemp "$dir/.alert-script.XXXXXXXX")
  STAGED_FILES+=("$tmp")
  SCRIPT_TARGETS+=("$target")
  printf '%%s' "$content" | base64 -d > "$tmp"
  chmod 600 "$tmp"
  bash -n "$tmp"
}
stage_alert_script "$SCRIPTS_DIR/check_integrity.sh" '%s' 0
stage_alert_script "$SCRIPTS_DIR/realtime_monitor.sh" '%s' 0
%s
# Publish only after every selected script has passed validation. Never write
# crontab here: schedules (including @reboot) belong to installation/Cron.
for ((i = 0; i < ${#STAGED_FILES[@]}; i++)); do
  mv -f -- "${STAGED_FILES[$i]}" "${SCRIPT_TARGETS[$i]}"
done
flock -u 8
exec 8>&-
OLD_MONITORS=$(pgrep -f '%s' || true)
for PID in $OLD_MONITORS; do
  pkill -TERM -P "$PID" 2>/dev/null || true
  kill -TERM "$PID" 2>/dev/null || true
done
for attempt in 1 2 3 4 5; do
  if flock -n /run/backup-monitor-inotify.lock -c true; then
    nohup /bin/bash /root/scripts/realtime_monitor.sh >/dev/null 2>&1 &
    exit 0
  fi
  sleep 1
done
printf 'Previous backup monitor still holds the lock\n' >&2
exit 1
`, strings.Join(quotedPaths, " "), encodeForShell(integrityScript), encodeForShell(realtimeScript), stageIntegrityAlertUpdates(updates), "^(/bin/)?bash ("+regexp.QuoteMeta(configuredScript("realtime_monitor.sh"))+"|/root/(scripts/)?realtime_monitor[.]sh)$")
}

// ===== ALERT ROUTE HANDLERS =====

func registerAlertRoutes(auth *gin.RouterGroup) {
	// API alert settings: never return stored credentials.
	auth.GET("/alert-settings", func(c *gin.Context) {
		settings, err := readAlertSettings("alert_settings.json")
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể đọc cấu hình cảnh báo"})
			return
		}
		c.JSON(http.StatusOK, safeAlertSettings(effectiveAlertSettings(settings)))
	})

	auth.POST("/alert-settings", func(c *gin.Context) {
		var incoming AlertSettings
		if err := c.ShouldBindJSON(&incoming); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Dữ liệu không hợp lệ"})
			return
		}
		current, err := readAlertSettings("alert_settings.json")
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể đọc cấu hình cảnh báo hiện tại"})
			return
		}
		settings := mergeAlertSecrets(current, incoming)
		effective := effectiveAlertSettings(settings)
		if err := validateAlertSettings(effective); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		integrityScript, realtimeScript := buildAlertScripts(effective)

		if _, err := executeSSHCommand(buildAlertInstallCommand(integrityScript, realtimeScript, effectiveMonitorPaths(settings), integrityAlertScriptUpdates(effective)...)); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}

		data, err := json.Marshal(settings)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể mã hóa cấu hình cảnh báo"})
			return
		}
		if err := writeFileAtomic0600("alert_settings.json", data); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể lưu cấu hình cảnh báo"})
			return
		}

		invalidate("cron-jobs", "server-status")
		c.JSON(http.StatusOK, safeAlertSettings(effective))
	})
}

// ===== AUTH - RATE LIMITER & JWT =====

type loginAttempt struct {
	firstFailure time.Time
	failures     int
}

type loginRateLimiter struct {
	mu          sync.Mutex
	attempts    map[string]loginAttempt
	maxFailures int
	window      time.Duration
}

func newLoginRateLimiter(maxFailures int, window time.Duration) *loginRateLimiter {
	return &loginRateLimiter{
		attempts:    make(map[string]loginAttempt),
		maxFailures: maxFailures,
		window:      window,
	}
}

func remoteAddressKey(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err == nil {
		return host
	}
	return remoteAddr
}

func (limiter *loginRateLimiter) allowed(key string, now time.Time) bool {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	attempt, ok := limiter.attempts[key]
	if !ok {
		return true
	}
	if now.Sub(attempt.firstFailure) >= limiter.window {
		delete(limiter.attempts, key)
		return true
	}
	return attempt.failures < limiter.maxFailures
}

func (limiter *loginRateLimiter) recordFailure(key string, now time.Time) {
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	attempt, ok := limiter.attempts[key]
	if !ok || now.Sub(attempt.firstFailure) >= limiter.window {
		limiter.attempts[key] = loginAttempt{firstFailure: now, failures: 1}
		return
	}
	attempt.failures++
	limiter.attempts[key] = attempt
}

func (limiter *loginRateLimiter) reset(key string) {
	limiter.mu.Lock()
	delete(limiter.attempts, key)
	limiter.mu.Unlock()
}

func generateToken(username string, secret []byte) (string, error) {
	claims := jwt.MapClaims{
		"sub": username,
		"exp": time.Now().Add(8 * time.Hour).Unix(),
		"iat": time.Now().Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(secret)
}

func authMiddleware(secret []byte, account *adminAccountStore) gin.HandlerFunc {
	return func(c *gin.Context) {
		if account.username() == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Chưa tạo quản trị viên"})
			return
		}
		authHeader := c.GetHeader("Authorization")
		if ticket := strings.TrimSpace(c.Query("ticket")); ticket != "" && c.Request.Method == http.MethodGet && c.Request.URL.Path == "/api/download-snapshot" {
			path, ok := consumeDownloadTicket(ticket)
			if ok {
				c.Set("download_ticket_path", path)
				c.Next()
				return
			}
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Link tải đã hết hạn hoặc đã được sử dụng"})
			return
		}
		if !strings.HasPrefix(authHeader, "Bearer ") || len(authHeader) <= len("Bearer ") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Chưa đăng nhập"})
			return
		}
		tokenStr := strings.TrimSpace(strings.TrimPrefix(authHeader, "Bearer "))
		token, err := jwt.Parse(tokenStr, func(t *jwt.Token) (interface{}, error) {
			if t.Method != jwt.SigningMethodHS256 {
				return nil, fmt.Errorf("unexpected signing method")
			}
			return secret, nil
		}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}), jwt.WithExpirationRequired())
		if err != nil || !token.Valid {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Token không hợp lệ hoặc đã hết hạn"})
			return
		}
		subject, subjectErr := token.Claims.GetSubject()
		if subjectErr != nil || !constantTimeEqual(subject, account.username()) {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Token không thuộc quản trị viên của bản cài này"})
			return
		}
		c.Set("actor", subject)
		c.Next()
	}
}

func constantTimeEqual(left, right string) bool {
	leftHash := sha256.Sum256([]byte(left))
	rightHash := sha256.Sum256([]byte(right))
	return subtle.ConstantTimeCompare(leftHash[:], rightHash[:]) == 1
}

type BackupSystemSettings = backupops.Settings

type backupSystemResponse struct {
	Config       BackupSystemSettings         `json:"config"`
	Version      string                       `json:"version"`
	Installation *backupops.InstallationPaths `json:"installation,omitempty"`
}

var activeBackupSystem atomic.Value
var activeInstallation atomic.Value
var backupSystemLoaded atomic.Bool
var backupSystemMutation sync.RWMutex

// Accessed under backupSystemMutation by HTTP handlers.
var lastBackupSystemRead time.Time

func storageReadRoute(route string) bool {
	return route == "/api/server-status" || route == "/api/backup-status" || route == "/api/local-snapshots" || route == "/api/recovery-snapshots" || route == "/api/cron-jobs" || strings.HasPrefix(route, "/api/cron-jobs/")
}

func refreshBackupSystemIfDue(read func() ([]byte, error), now time.Time) error {
	if !backupSystemReadDue(now) {
		return nil
	}
	data, err := read()
	if err != nil {
		return err
	}
	return acceptBackupSystemConfig(data)
}

// The caller holds backupSystemMutation for reading or writing.
func backupSystemReadDue(now time.Time) bool {
	return !backupSystemLoaded.Load() || lastBackupSystemRead.IsZero() || now.Sub(lastBackupSystemRead) >= 30*time.Second
}

func defaultBackupSystem() BackupSystemSettings {
	return backupops.DefaultSettings()
}

func backupSystemSettings() BackupSystemSettings {
	if config, ok := activeBackupSystem.Load().(BackupSystemSettings); ok {
		return config
	}
	return defaultBackupSystem()
}

func configuredScript(name string) string { return path.Join(backupSystemSettings().ScriptsDir, name) }
func configuredLog(name string) string    { return path.Join(backupSystemSettings().LogsDir, name) }

func remoteInstallation() backupops.InstallationPaths {
	if current, ok := activeInstallation.Load().(backupops.InstallationPaths); ok {
		return current
	}
	return backupops.LegacyInstallation()
}

func configuredRuntimeCommand(command string) string {
	return backupops.Rewrite(command, backupops.InstallationPairs(backupops.LegacyInstallation(), remoteInstallation()))
}

// These paths belong to the Linux host, even when the API runs on Windows.
// CronSettings is also used by native helper filesystem operations/tests, so
// retain its validation/options but explicitly join remote paths with POSIX path.
func remoteCronSettings(cfg BackupSystemSettings, id, key string) (BackupSystemSettings, error) {
	local, err := backupops.CronSettings(cfg, id, key)
	if err != nil || key == "" {
		return local, err
	}
	loc := cfg.CronLocations[key]
	if loc.FileLayout == "files" {
		local.ScriptsDir, local.LogsDir = loc.ScriptsDir, loc.LogsDir
	} else {
		local.ScriptsDir = path.Join(loc.ScriptsDir, "cron-"+key)
		local.LogsDir = path.Join(loc.LogsDir, "cron-"+key)
	}
	if loc.Options != nil {
		local.DriveHistoryLog = path.Join(local.LogsDir, backupops.CronFileName(local, "aapanel_backup.log"))
	}
	return local, nil
}

func snapshotDownloadCommand(relative string) (string, error) {
	if !validSnapshotPath(relative) {
		return "", fmt.Errorf("Đường dẫn không hợp lệ")
	}
	cfg := backupSystemSettings()
	remotePath := cfg.DriveRemote + ":" + cfg.DriveFolder + "/" + relative
	return "/usr/bin/rclone cat " + activityShellQuote(remotePath) + " --config " + activityShellQuote(remoteInstallation().RcloneConfig), nil
}

// Paths are restricted to shell-safe characters by the remote validator. One
// pass avoids re-replacing a destination nested under a legacy directory.
func configuredBackupCommand(command string) string {
	cfg, defaults := backupSystemSettings(), defaultBackupSystem()
	if reflect.DeepEqual(cfg, defaults) && remoteInstallation().DataDir == "" && !strings.Contains(command, "/tmp/rclone_about") {
		return command
	}
	pairs := [][2]string{{defaults.ScriptsDir, cfg.ScriptsDir}, {defaults.BackupRoot, cfg.BackupRoot},
		{defaults.DriveRemote + ":" + defaults.DriveFolder, cfg.DriveRemote + ":" + cfg.DriveFolder},
		{defaults.DriveRemote + ":", cfg.DriveRemote + ":"}, {defaults.DriveHistoryLog, cfg.DriveHistoryLog}}
	pairs = append(pairs, backupops.InstallationPairs(backupops.LegacyInstallation(), remoteInstallation())...)
	if strings.Contains(command, "/tmp/rclone_about") {
		// Account quota is shared by folders on one remote, never by different remotes.
		// A new cache namespace also prevents reusing the old default-account cache.
		cachePrefix := fmt.Sprintf("/tmp/backup-monitor-rclone-about-%x", sha256.Sum256([]byte(cfg.DriveRemote)))
		pairs = append(pairs, [2]string{"/tmp/rclone_about", cachePrefix})
	}
	for _, name := range backupops.LogNames {
		if cfg.LogsDir != defaults.LogsDir {
			pairs = append(pairs, [2]string{"/root/" + name, path.Join(cfg.LogsDir, name)})
		}
	}
	mapping := map[string]string{}
	for _, pair := range pairs {
		if pair[0] == pair[1] {
			continue
		}
		mapping[pair[0]] = pair[1]
		if escaped := strings.ReplaceAll(pair[0], "/", `\/`); escaped != pair[0] {
			mapping[escaped] = strings.ReplaceAll(regexp.QuoteMeta(pair[1]), "/", `\/`)
		}
	}
	// Protect already-configured prefixes, including paths nested below an old
	// prefix. Commands can pass through both a generator and the SSH executor.
	for _, value := range mapping {
		mapping[value] = value
	}
	for key, loc := range cfg.CronLocations {
		local, err := remoteCronSettings(cfg, loc.ID, key)
		if err != nil {
			continue
		}
		for _, value := range []string{local.ScriptsDir, local.LogsDir} {
			mapping[value] = value
			escaped := strings.ReplaceAll(value, "/", `\/`)
			mapping[escaped] = escaped
		}
	}
	keys := make([]string, 0, len(mapping))
	for key := range mapping {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	if len(keys) == 0 {
		return command
	}
	for i, key := range keys {
		keys[i] = regexp.QuoteMeta(key)
	}
	return regexp.MustCompile(strings.Join(keys, "|")).ReplaceAllStringFunc(command, func(value string) string { return mapping[value] })
}

func backupSystemOperation(operation string, fields map[string]interface{}) ([]byte, error) {
	if fields == nil {
		fields = map[string]interface{}{}
	}
	fields["operation"] = operation
	payload, err := json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	// Configuration may contain OAuth tokens or script credentials. Never put
	// payloads in process argv, including on the local-agent transport.
	timeout := 60 * time.Second
	if operation == "preview" || operation == "apply" {
		timeout = 10 * time.Minute
	}
	out, err := executeSSHCommandInputRawTimeout("'"+backupops.BinaryPath+"' config", payload, timeout)
	if err != nil {
		return nil, fmt.Errorf("Không thể hoàn tất thao tác cấu hình trên máy chủ; hãy kiểm tra đường dẫn hoặc thử lại")
	}
	var result map[string]interface{}
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		return nil, fmt.Errorf("Phản hồi cấu hình không hợp lệ")
	}
	if message, ok := result["error"].(string); ok {
		return nil, fmt.Errorf("%s", message)
	}
	return []byte(out), nil
}

func acceptBackupSystemConfig(data []byte) error {
	var response backupSystemResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return err
	}
	if response.Config.BackupRoot == "" || response.Config.ScriptsDir == "" || response.Version == "" {
		return fmt.Errorf("cấu hình máy chủ không hợp lệ")
	}
	installation := backupops.LegacyInstallation()
	if response.Installation != nil && response.Installation.DataDir != "" {
		if backupops.ValidateInstallationDirectory(response.Installation.DataDir) != nil {
			return fmt.Errorf("vị trí cài đặt máy chủ không hợp lệ")
		}
		installation = backupops.InstallationAt(response.Installation.DataDir)
		if !reflect.DeepEqual(installation, *response.Installation) {
			return fmt.Errorf("đường dẫn cài đặt máy chủ không nhất quán")
		}
	}
	previous := backupSystemSettings()
	changed := !backupSystemLoaded.Load() || !reflect.DeepEqual(previous, response.Config) || !reflect.DeepEqual(remoteInstallation(), installation)
	activeInstallation.Store(installation)
	activeBackupSystem.Store(response.Config)
	backupSystemLoaded.Store(true)
	lastBackupSystemRead = time.Now()
	if changed {
		invalidate("config", "cron-jobs", "server-status", "backup-status", "local-snapshots", "recovery-snapshots")
		if previous.DriveRemote != response.Config.DriveRemote || previous.DriveFolder != response.Config.DriveFolder {
			clearDriveStatusSnapshot()
		}
	}
	return nil
}

func initializeBackupSystem() {
	data, err := backupSystemOperation("get", nil)
	if err == nil {
		err = acceptBackupSystemConfig(data)
	}
	if err != nil {
		log.Print("Backup system configuration unavailable; remote mutations are disabled until configuration loads")
	} else if current := backupSystemSettings(); current.DriveRemote != defaultBackupSystem().DriveRemote || current.DriveFolder != defaultBackupSystem().DriveFolder {
		// The cache predates configurable destinations; never display the old
		// destination's counters as a fallback for a different Drive folder.
		clearDriveStatusSnapshot()
	}
}

func registerBackupSystemRoutes(auth *gin.RouterGroup) {
	auth.GET("/backup-system/drive/status", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		data, err := backupSystemOperation("drive-status", nil)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		c.Data(http.StatusOK, "application/json", data)
	})
	auth.GET("/backup-system", func(c *gin.Context) {
		backupSystemMutation.Lock()
		defer backupSystemMutation.Unlock()
		data, err := backupSystemOperation("get", nil)
		if err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": err.Error()})
			return
		}
		c.Header("Cache-Control", "no-store")
		if err := acceptBackupSystemConfig(data); err != nil {
			c.JSON(http.StatusBadGateway, gin.H{"error": "Cấu hình máy chủ không hợp lệ"})
			return
		}
		c.Data(http.StatusOK, "application/json", data)
	})
	for _, route := range []struct{ URL, Operation string }{
		{"/backup-system/directories/list", "list"}, {"/backup-system/directories/create", "create"},
		{"/backup-system/preview", "preview"}, {"/backup-system/apply", "apply"},
		{"/backup-system/scripts/read", "script-read"}, {"/backup-system/scripts/save", "script-save"},
		{"/backup-system/scripts/options", "script-options"},
		{"/backup-system/drive/connect", "drive-connect"},
	} {
		operation := route.Operation
		auth.POST(route.URL, func(c *gin.Context) {
			c.Header("Cache-Control", "no-store")
			limit := int64(131072)
			if operation == "script-save" {
				limit = 1024 * 1024
			}
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
			var fields map[string]interface{}
			if err := c.ShouldBindJSON(&fields); err != nil {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Dữ liệu cấu hình không hợp lệ"})
				return
			}
			if operation == "apply" || operation == "script-save" || operation == "drive-connect" {
				backupSystemMutation.Lock()
				defer backupSystemMutation.Unlock()
			}
			data, err := backupSystemOperation(operation, fields)
			if err != nil {
				c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
				return
			}
			if operation == "apply" {
				previous := backupSystemSettings()
				if err := acceptBackupSystemConfig(data); err != nil {
					c.JSON(http.StatusBadGateway, gin.H{"error": "Đã gửi cấu hình nhưng chưa xác nhận được trạng thái; hãy tải lại"})
					return
				}
				invalidate("config", "cron-jobs", "server-status", "backup-status", "local-snapshots", "recovery-snapshots")
				current := backupSystemSettings()
				if previous.DriveRemote != current.DriveRemote || previous.DriveFolder != current.DriveFolder {
					clearDriveStatusSnapshot()
				}
			}
			if operation == "script-save" {
				invalidate("cron-jobs")
			}
			if operation == "drive-connect" {
				invalidate("backup-status", "config", "recovery-snapshots")
				clearDriveStatusSnapshot()
			}
			c.Data(http.StatusOK, "application/json", data)
		})
	}
}

// ===== CACHE NẰM TRONG RAM CHO CÁC API ĐỌC (Giảm tải SSH & Chống nghẽn) =====

type cacheEntry struct {
	value     []byte
	expiresAt time.Time
}

type flightCall struct {
	done chan struct{}
	val  []byte
	err  error
}

var (
	cacheMu  sync.Mutex
	cache    = map[string]cacheEntry{}
	inFlight = map[string]*flightCall{}
)

func cachedGet(key string, ttl time.Duration, producer func() ([]byte, error)) ([]byte, error) {
	return cachedGetWithPolicy(key, ttl, producer, nil, nil)
}

func cachedGetWithPolicy(key string, ttl time.Duration, producer func() ([]byte, error), ttlForValue func([]byte) time.Duration, staleValue func([]byte) []byte) ([]byte, error) {
	return cachedRead(key, ttl, producer, ttlForValue, staleValue, true)
}

func cachedGetFreshOrError(key string, ttl time.Duration, producer func() ([]byte, error)) ([]byte, error) {
	return cachedRead(key, ttl, producer, nil, nil, false)
}

func cachedRead(key string, ttl time.Duration, producer func() ([]byte, error), ttlForValue func([]byte) time.Duration, staleValue func([]byte) []byte, allowStale bool) ([]byte, error) {
	cacheMu.Lock()
	entry := cache[key]
	if time.Now().Before(entry.expiresAt) {
		cacheMu.Unlock()
		return entry.value, nil
	}
	call, shared := inFlight[key]
	if !shared {
		call = &flightCall{done: make(chan struct{})}
		inFlight[key] = call
	}
	cacheMu.Unlock()

	serveStale := allowStale && len(entry.value) > 0
	if !shared {
		load := func() {
			value, err := producer()
			cacheMu.Lock()
			defer cacheMu.Unlock()
			if inFlight[key] == call {
				if err == nil {
					if ttlForValue != nil {
						ttl = ttlForValue(value)
					}
					cache[key] = cacheEntry{value: value, expiresAt: time.Now().Add(ttl)}
				}
				delete(inFlight, key)
			}
			call.val, call.err = value, err
			close(call.done)
		}
		if serveStale {
			go load()
		} else {
			load()
		}
	}
	if serveStale {
		if staleValue != nil {
			if marked := staleValue(entry.value); len(marked) > 0 {
				return marked, nil
			}
		}
		return entry.value, nil
	}
	<-call.done
	return call.val, call.err
}

func invalidate(keys ...string) {
	// Keep callers independent of the shared internal read-cache layout.
	for _, key := range keys {
		switch key {
		case "backup-status":
			keys = append(keys, "drive-inventory", "backup-activity")
		case "recovery-snapshots":
			keys = append(keys, "drive-inventory")
		case "local-snapshots":
			keys = append(keys, "server-status")
		}
	}
	cacheMu.Lock()
	defer cacheMu.Unlock()
	for _, k := range keys {
		delete(cache, k)
		// Old reads may finish later, but cannot refill the new configuration's cache.
		delete(inFlight, k)
	}
}

func refreshReadCaches() {
	cacheMu.Lock()
	defer cacheMu.Unlock()
	for key, entry := range cache {
		entry.expiresAt = time.Time{}
		cache[key] = entry
	}
	// Detach prior producers so they cannot make pre-refresh values fresh again.
	clear(inFlight)
}

// ===== CONFIGURATION =====

type SSHSettings struct {
	Host                 string
	User                 string
	Password             string
	PrivateKeyPath       string
	PrivateKeyPassphrase string
	KnownHostsPath       string
	InsecureHostKey      bool
}

type AppConfig struct {
	Account          *adminAccountStore
	JWTSecret        []byte
	APIBindAddr      string
	CORSAllowOrigins []string
	SSH              SSHSettings
	HostAgent        *hostagent.Client
	HostIdentity     string
	SMTPEmail        string
	SMTPPassword     string
}

var runtimeConfig AppConfig

func requiredEnv(key string) (string, error) {
	value := os.Getenv(key)
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("%s is required", key)
	}
	return value, nil
}

func explicitBoolEnv(key string) (bool, error) {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if value == "" {
		return false, nil
	}
	if value == "true" {
		return true, nil
	}
	if value != "false" {
		return false, fmt.Errorf("%s must be true or false", key)
	}
	return false, nil
}

func parseCORSOrigins(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, fmt.Errorf("CORS_ALLOWED_ORIGINS is required")
	}

	seen := make(map[string]struct{})
	origins := make([]string, 0)
	for _, item := range strings.Split(raw, ",") {
		origin := strings.TrimSuffix(strings.TrimSpace(item), "/")
		if origin == "" || origin == "*" {
			return nil, fmt.Errorf("CORS_ALLOWED_ORIGINS must contain explicit http(s) origins, not wildcards")
		}
		u, err := url.Parse(origin)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
			return nil, fmt.Errorf("invalid CORS origin %q", item)
		}
		if _, ok := seen[origin]; !ok {
			seen[origin] = struct{}{}
			origins = append(origins, origin)
		}
	}
	return origins, nil
}

func loadConfigFromEnv() (AppConfig, error) {
	smtpEmail, smtpPassword, err := loadSMTPSenderFromEnv()
	if err != nil {
		return AppConfig{}, err
	}
	mode := strings.TrimSpace(os.Getenv("HOST_CONNECTION_MODE"))
	if mode == "" {
		mode = "ssh"
	}
	if mode != "ssh" && mode != "local-agent" {
		return AppConfig{}, fmt.Errorf("unsupported HOST_CONNECTION_MODE")
	}
	jwtSecret, err := loadJWTSecret(mode)
	if err != nil {
		return AppConfig{}, err
	}
	if len(jwtSecret) < 32 {
		return AppConfig{}, fmt.Errorf("JWT_SECRET must be at least 32 characters")
	}

	var sshSettings SSHSettings
	var agent *hostagent.Client
	identity := ""
	if mode == "local-agent" {
		agent, err = loadLocalAgent()
		identity = "local-agent"
	} else {
		sshSettings, err = loadSSHSettingsFromEnv()
		identity = sshSettings.Host
	}
	if err != nil {
		return AppConfig{}, err
	}

	bindAddr := strings.TrimSpace(os.Getenv("API_BIND_ADDR"))
	if bindAddr == "" {
		bindAddr = "127.0.0.1:8080"
	}
	if _, _, err := net.SplitHostPort(bindAddr); err != nil {
		return AppConfig{}, fmt.Errorf("API_BIND_ADDR must use host:port format: %w", err)
	}
	corsOrigins, err := parseCORSOrigins(os.Getenv("CORS_ALLOWED_ORIGINS"))
	if err != nil {
		return AppConfig{}, err
	}
	cfg := AppConfig{JWTSecret: []byte(jwtSecret), APIBindAddr: bindAddr, CORSAllowOrigins: corsOrigins,
		SSH: sshSettings, HostAgent: agent, HostIdentity: identity, SMTPEmail: smtpEmail, SMTPPassword: smtpPassword}
	cfg.Account, err = initializeAdminAccount()
	if err != nil {
		return AppConfig{}, err
	}
	return cfg, nil
}

func loadSSHSettingsFromEnv() (SSHSettings, error) {
	sshHost, err := requiredEnv("SSH_HOST")
	if err != nil {
		return SSHSettings{}, err
	}
	if _, _, err := net.SplitHostPort(sshHost); err != nil {
		return SSHSettings{}, fmt.Errorf("SSH_HOST must use host:port format: %w", err)
	}
	sshUser, err := requiredEnv("SSH_USER")
	if err != nil {
		return SSHSettings{}, err
	}
	allowRoot, err := explicitBoolEnv("ALLOW_ROOT_SSH")
	if err != nil {
		return SSHSettings{}, err
	}
	if sshUser == "root" && !allowRoot {
		return SSHSettings{}, fmt.Errorf("SSH_USER=root requires explicit ALLOW_ROOT_SSH=true")
	}

	insecureHostKey, err := explicitBoolEnv("SSH_INSECURE_IGNORE_HOST_KEY")
	if err != nil {
		return SSHSettings{}, err
	}
	knownHostsPath := strings.TrimSpace(os.Getenv("SSH_KNOWN_HOSTS"))
	if !insecureHostKey && knownHostsPath == "" {
		return SSHSettings{}, fmt.Errorf("SSH_KNOWN_HOSTS is required unless SSH_INSECURE_IGNORE_HOST_KEY=true")
	}

	sshPassword := os.Getenv("SSH_PASSWORD")
	privateKeyPath := strings.TrimSpace(os.Getenv("SSH_PRIVATE_KEY_PATH"))
	if sshPassword == "" && privateKeyPath == "" {
		return SSHSettings{}, fmt.Errorf("set SSH_PASSWORD or SSH_PRIVATE_KEY_PATH")
	}
	settings := SSHSettings{
		Host:                 sshHost,
		User:                 sshUser,
		Password:             sshPassword,
		PrivateKeyPath:       privateKeyPath,
		PrivateKeyPassphrase: os.Getenv("SSH_PRIVATE_KEY_PASSPHRASE"),
		KnownHostsPath:       knownHostsPath,
		InsecureHostKey:      insecureHostKey,
	}
	if _, err := buildSSHClientConfig(settings); err != nil {
		return SSHSettings{}, fmt.Errorf("invalid SSH configuration: %w", err)
	}
	return settings, nil
}

// ===== CRON JOB COMMAND DISPATCHERS =====

var runJobSSHCommand = executeSSHCommand

// Only allowlisted scripts still scheduled in root's crontab may be started.
// The client never supplies a command or path.
func commandForCronJob(jobID string) (string, bool) {
	if task, ok := managedCronTaskByID(jobID); ok {
		return commandForManagedCronJob(task), true
	}
	return "", false
}

func commandForJob(jobID string) (string, bool) {
	switch jobID {
	case "drive-sync":
		return commandForCronJob("drive-sync")
	case "site-backup":
		return commandForCronJob("backup-site")
	case "database-backup":
		return commandForCronJob("backup-database")
	default:
		return "", false
	}
}

// ===== CRON JOB ROUTE HANDLERS =====

func registerCronRoutes(auth *gin.RouterGroup) {
	// Kích hoạt một loại backup cố định bằng script được quản lý.
	auth.POST("/run-job", func(c *gin.Context) {
		var req struct {
			JobID string `json:"jobId"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Dữ liệu không hợp lệ"})
			return
		}
		command, ok := commandForJob(req.JobID)
		if !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": "jobId không hợp lệ"})
			return
		}
		output, err := runJobSSHCommand(command)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể khởi chạy backup"})
			return
		}
		if strings.TrimSpace(output) != "STARTED" {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Không xác nhận được backup đã khởi chạy"})
			return
		}
		invalidate("cron-jobs")
		c.JSON(http.StatusAccepted, gin.H{"status": "started"})
	})

	// Chạy thủ công đúng script được quản lý còn hiện diện trong crontab.
	var cronRunMu sync.Mutex
	lastCronRun := make(map[string]time.Time)
	auth.POST("/run-cron-job", func(c *gin.Context) {
		var req struct {
			JobID string `json:"jobId"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Dữ liệu không hợp lệ"})
			return
		}
		command, ok := commandForCronJob(req.JobID)
		if !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Mã cronjob không hợp lệ"})
			return
		}
		cronRunMu.Lock()
		if time.Since(lastCronRun[req.JobID]) < time.Minute {
			cronRunMu.Unlock()
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "Cronjob vừa được khởi chạy; hãy đợi ít nhất 1 phút"})
			return
		}
		lastCronRun[req.JobID] = time.Now()
		cronRunMu.Unlock()
		releaseReservation := func() {
			cronRunMu.Lock()
			delete(lastCronRun, req.JobID)
			cronRunMu.Unlock()
		}
		output, err := runJobSSHCommand(command)
		if err != nil {
			releaseReservation()
			if strings.Contains(err.Error(), "CRON_ALREADY_RUNNING") {
				c.JSON(http.StatusConflict, gin.H{"error": "Tác vụ đang chạy; hãy đợi hoàn tất và xem log"})
				return
			}
			if strings.Contains(err.Error(), "CRON_NOT_SCHEDULED") {
				c.JSON(http.StatusConflict, gin.H{"error": "Cronjob không còn trong lịch chạy"})
				return
			}
			if strings.Contains(err.Error(), "CRON_SCRIPT_UNAVAILABLE") {
				c.JSON(http.StatusConflict, gin.H{"error": "Script cronjob không còn khả dụng"})
				return
			}
			if strings.Split(req.JobID, "~")[0] == "cleanup-panel" {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Dọn dẹp aaPanel chưa hoàn tất; hãy kiểm tra log cronjob"})
				return
			}
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể khởi chạy cronjob"})
			return
		}
		if strings.Split(req.JobID, "~")[0] == "cleanup-panel" {
			deleted, ok := parsePanelCleanupResult(output)
			if !ok {
				releaseReservation()
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Không xác nhận được kết quả dọn dẹp aaPanel"})
				return
			}
			invalidate("cron-jobs", "server-status", "local-snapshots", "backup-activity")
			c.JSON(http.StatusOK, gin.H{"status": "completed", "deletedCount": deleted})
			return
		}
		if strings.TrimSpace(output) != "STARTED" {
			releaseReservation()
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Không xác nhận được cronjob đã khởi chạy"})
			return
		}
		// Triggering is not completion: keep the expensive Drive/snapshot caches
		// available until their normal TTL expires, so dashboards stay responsive.
		invalidate("cron-jobs")
		c.JSON(http.StatusAccepted, gin.H{"status": "started"})
	})

	// Liệt kê các script được quản lý trong root crontab.
	auth.POST("/cron-tracking", func(c *gin.Context) {
		output, err := executeSSHCommand(buildCronTrackingInstallCommand())
		if err != nil {
			switch {
			case strings.Contains(err.Error(), "CRON_NOT_SCHEDULED"):
				c.JSON(http.StatusConflict, gin.H{"error": "Không tìm thấy cron sao lưu website, cơ sở dữ liệu hoặc cấu hình aaPanel để bật ghi nhận"})
			case strings.Contains(err.Error(), "CRON_DUPLICATE"):
				c.JSON(http.StatusConflict, gin.H{"error": "Có lịch sao lưu trùng; cần kiểm tra crontab trước khi bật ghi nhận"})
			case strings.Contains(err.Error(), "CRON_WRAPPER_CONFLICT"):
				c.JSON(http.StatusConflict, gin.H{"error": "File bộ ghi nhận đã tồn tại nhưng không thuộc dashboard; chưa ghi đè"})
			case strings.Contains(err.Error(), "CRON_SCRIPT_UNAVAILABLE"):
				c.JSON(http.StatusConflict, gin.H{"error": "Script sao lưu không còn khả dụng; chưa đổi crontab"})
			default:
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể bật ghi nhận cron tự động; lịch cũ được lưu dự phòng nếu đã cập nhật"})
			}
			return
		}
		if strings.TrimSpace(output) != "TRACKING_ENABLED" {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Không xác nhận được việc bật ghi nhận cron tự động"})
			return
		}
		invalidate("cron-jobs", "server-status", "backup-status")
		c.JSON(http.StatusOK, gin.H{"status": "enabled"})
	})

	auth.GET("/cron-jobs", func(c *gin.Context) {
		output, err := cachedGetFreshOrError("cron-jobs", 3*time.Second, func() ([]byte, error) {
			return fetchManagedCronJobs()
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể đọc tiến trình định kỳ", "detail": err.Error()})
			return
		}
		c.Data(http.StatusOK, "application/json", output)
	})

	auth.GET("/cron-jobs/:id/log", func(c *gin.Context) {
		task, ok := managedCronTaskByID(c.Param("id"))
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "Cronjob không hợp lệ"})
			return
		}
		c.Header("Cache-Control", "no-store")
		result, err := fetchManagedCronJobLog(task)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể đọc log cronjob"})
			return
		}
		c.JSON(http.StatusOK, result)
	})

	auth.PUT("/cron-jobs/:id/schedule", func(c *gin.Context) {
		task, ok := managedCronTaskByID(c.Param("id"))
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "Cronjob không hợp lệ"})
			return
		}
		var req struct {
			Time             string `json:"time"`
			ExpectedSchedule string `json:"expectedSchedule"`
			ExpectedEnabled  *bool  `json:"expectedEnabled"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.ExpectedEnabled == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Dữ liệu không hợp lệ"})
			return
		}
		command, valid := commandForCronScheduleUpdate(task, req.Time, req.ExpectedSchedule, *req.ExpectedEnabled)
		if !valid {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Giờ chạy hoặc lịch hiện tại không hợp lệ"})
			return
		}
		output, err := executeSSHCommand(command)
		if err != nil {
			switch {
			case strings.Contains(err.Error(), "CRON_NOT_SCHEDULED"):
				c.JSON(http.StatusConflict, gin.H{"error": "Cronjob không còn trong lịch chạy"})
			case strings.Contains(err.Error(), "CRON_DUPLICATE"):
				c.JSON(http.StatusConflict, gin.H{"error": "Có dòng cron cùng loại và cùng giờ/chu kỳ; hãy chọn lịch khác hoặc xử lý dòng trùng"})
			case strings.Contains(err.Error(), "CRON_SCHEDULE_CHANGED"):
				c.JSON(http.StatusConflict, gin.H{"error": "Lịch chạy đã thay đổi; hãy làm mới danh sách và thử lại"})
			default:
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể lưu lịch chạy cronjob"})
			}
			return
		}
		if !strings.HasPrefix(strings.TrimSpace(output), "UPDATED|"+remoteInstallation().ConfigDir+"/cron-recovery/schedule-"+task.ID+".") {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Không xác nhận được lịch chạy đã lưu"})
			return
		}
		invalidate("cron-jobs", "server-status")
		c.JSON(http.StatusOK, gin.H{"schedule": fmt.Sprintf("%s %s %s", req.Time[3:], req.Time[:2], strings.Join(strings.Fields(req.ExpectedSchedule)[2:], " "))})
	})

	auth.DELETE("/cron-jobs/:id", func(c *gin.Context) {
		task, ok := managedCronTaskByID(c.Param("id"))
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "Cronjob không hợp lệ"})
			return
		}
		var req struct {
			ExpectedSchedule string `json:"expectedSchedule"`
			ExpectedEnabled  *bool  `json:"expectedEnabled"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.ExpectedEnabled == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Dữ liệu không hợp lệ"})
			return
		}
		command, valid := commandForCronDeletion(task, req.ExpectedSchedule, *req.ExpectedEnabled)
		if !valid {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Lịch hiện tại không hợp lệ"})
			return
		}
		output, err := executeSSHCommand(command)
		// Refresh even after an uncertain SSH outcome: installation may have
		// completed before the connection failed.
		invalidate("cron-jobs", "server-status", "backup-status")
		if err != nil {
			switch {
			case strings.Contains(err.Error(), "CRON_NOT_SCHEDULED"):
				c.JSON(http.StatusConflict, gin.H{"error": "Cronjob không còn trong lịch chạy; hãy làm mới danh sách"})
			case strings.Contains(err.Error(), "CRON_DUPLICATE"):
				c.JSON(http.StatusConflict, gin.H{"error": "Có dòng cron cùng loại và cùng giờ/chu kỳ; hãy chọn lịch khác hoặc xử lý dòng trùng"})
			case strings.Contains(err.Error(), "CRON_SCHEDULE_CHANGED"):
				c.JSON(http.StatusConflict, gin.H{"error": "Lịch chạy đã thay đổi; hãy đóng xác nhận, làm mới danh sách và thử lại"})
			case strings.Contains(err.Error(), "CRON_ALREADY_RUNNING"):
				c.JSON(http.StatusConflict, gin.H{"error": "Tác vụ đang chạy; hãy chờ kết thúc rồi xóa lịch"})
			default:
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể xóa lịch cron; không xóa script, log hay backup"})
			}
			return
		}
		backupPath, ok := strings.CutPrefix(strings.TrimSpace(output), "DELETED|")
		if !ok || !strings.HasPrefix(backupPath, remoteInstallation().ConfigDir+"/cron-recovery/delete-"+task.ID+".") || !strings.HasSuffix(backupPath, "/crontab.txt") {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Không xác nhận được kết quả xóa; hãy làm mới danh sách trước khi thử lại"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "deleted", "backupPath": backupPath})
	})

	auth.PUT("/cron-jobs/:id/state", func(c *gin.Context) {
		task, ok := managedCronTaskByID(c.Param("id"))
		if !ok {
			c.JSON(http.StatusNotFound, gin.H{"error": "Cronjob không hợp lệ"})
			return
		}
		var req struct {
			Enabled          *bool  `json:"enabled"`
			ExpectedSchedule string `json:"expectedSchedule"`
			ExpectedEnabled  *bool  `json:"expectedEnabled"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || req.Enabled == nil || req.ExpectedEnabled == nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Dữ liệu không hợp lệ"})
			return
		}
		command, valid := commandForCronStateUpdate(task, req.ExpectedSchedule, *req.ExpectedEnabled, *req.Enabled)
		if !valid {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Lịch hiện tại không hợp lệ"})
			return
		}
		output, err := executeSSHCommand(command)
		invalidate("cron-jobs", "server-status", "backup-status")
		if err != nil {
			switch {
			case strings.Contains(err.Error(), "CRON_NOT_SCHEDULED"):
				c.JSON(http.StatusConflict, gin.H{"error": "Cronjob không còn trong danh sách; hãy làm mới"})
			case strings.Contains(err.Error(), "CRON_DUPLICATE"):
				c.JSON(http.StatusConflict, gin.H{"error": "Có dòng cron cùng loại và cùng giờ/chu kỳ; hãy chọn lịch khác hoặc xử lý dòng trùng"})
			case strings.Contains(err.Error(), "CRON_SCHEDULE_CHANGED"):
				c.JSON(http.StatusConflict, gin.H{"error": "Lịch hoặc trạng thái đã thay đổi; hãy đóng xác nhận và làm mới danh sách"})
			case strings.Contains(err.Error(), "CRON_ALREADY_RUNNING"):
				c.JSON(http.StatusConflict, gin.H{"error": "Tác vụ đang chạy; hãy chờ kết thúc rồi đổi trạng thái"})
			default:
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể cập nhật trạng thái cron"})
			}
			return
		}
		operation := "pause"
		if *req.Enabled {
			operation = "enable"
		}
		backupPath, ok := strings.CutPrefix(strings.TrimSpace(output), "UPDATED|")
		if !ok || !strings.HasPrefix(backupPath, remoteInstallation().ConfigDir+"/cron-recovery/"+operation+"-"+task.ID+".") || !strings.HasSuffix(backupPath, "/crontab.txt") {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Không xác nhận được trạng thái đã lưu; hãy làm mới danh sách"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"enabled": *req.Enabled, "backupPath": backupPath})
	})
}

type managedCronTask struct {
	InstanceKey string `json:"instanceKey,omitempty"`
	WrapperPath string `json:"-"`
	ID          string `json:"id"`
	Name        string `json:"name"`
	Script      string `json:"script"`
	Log         string `json:"-"`
}

func (task managedCronTask) selector() string {
	if task.InstanceKey != "" {
		return task.ID + "~" + task.InstanceKey
	}
	return task.ID
}
func (task managedCronTask) wrapper() string {
	if task.WrapperPath != "" {
		return task.WrapperPath
	}
	return path.Join(path.Dir(task.Script), "backup-monitor-run.sh")
}

func (task managedCronTask) runStatePath() string {
	name := ".backup-monitor-" + task.ID + ".run.json"
	if task.InstanceKey != "" {
		if local, err := remoteCronSettings(backupSystemSettings(), task.ID, task.InstanceKey); err == nil {
			name = backupops.CronFileName(local, name)
		}
	}
	return path.Join(path.Dir(task.Log), name)
}

func managedLogArguments(ids ...string) string {
	allowed := map[string]bool{}
	for _, id := range ids {
		allowed[id] = true
	}
	seen := map[string]bool{}
	paths := []string{}
	for _, task := range managedCronTasks() {
		if (len(ids) == 0 || allowed[task.ID]) && !seen[task.Log] {
			paths = append(paths, activityShellQuote(task.Log))
			seen[task.Log] = true
		}
	}
	return strings.Join(paths, " ")
}

var defaultManagedCronTasks = []managedCronTask{
	{ID: "cleanup-panel", Name: "Dọn dẹp thư mục panel", Script: "/root/scripts/cleanup-panel-backups.sh", Log: "/root/cleanup-panel-backups.log"},
	{ID: "backup-site", Name: "Sao lưu website", Script: "/root/scripts/backup-site.sh", Log: "/root/backup-site.log"},
	{ID: "backup-database", Name: "Sao lưu database", Script: "/root/scripts/backup-database.sh", Log: "/root/backup-database.log"},
	{ID: "backup-panel", Name: "Sao lưu cấu hình aaPanel", Script: "/root/scripts/backup-panel.sh", Log: "/root/backup-panel.log"},
	{ID: "drive-sync", Name: "Đồng bộ Google Drive", Script: "/root/scripts/auto_backup.sh", Log: "/root/auto_backup.log"},
	{ID: "integrity-check", Name: "Kiểm tra số thư mục sao lưu", Script: "/root/scripts/check_integrity.sh", Log: "/root/check_integrity.log"},
}

func managedCronTasks() []managedCronTask {
	tasks := make([]managedCronTask, len(defaultManagedCronTasks))
	copy(tasks, defaultManagedCronTasks)
	for i := range tasks {
		tasks[i].Script = configuredScript(strings.TrimPrefix(tasks[i].Script, "/root/scripts/"))
		tasks[i].Log = configuredLog(strings.TrimPrefix(tasks[i].Log, "/root/"))
	}
	cfg := backupSystemSettings()
	keys := []string{}
	for key := range cfg.CronLocations {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		loc := cfg.CronLocations[key]
		local, err := remoteCronSettings(cfg, loc.ID, key)
		if err != nil {
			continue
		}
		for _, task := range defaultManagedCronTasks {
			if task.ID == loc.ID {
				task.InstanceKey = key
				task.Script = path.Join(local.ScriptsDir, backupops.CronFileName(local, path.Base(task.Script)))
				task.Log = path.Join(local.LogsDir, backupops.CronFileName(local, path.Base(task.Log)))
				task.WrapperPath = path.Join(local.ScriptsDir, backupops.CronFileName(local, "backup-monitor-run.sh"))
				tasks = append(tasks, task)
				break
			}
		}
	}
	return tasks
}

var cronClockRe = regexp.MustCompile(`^(?:[01][0-9]|2[0-3]):[0-5][0-9]$`)

func validExpectedCronSchedule(schedule string) bool {
	fields := strings.Fields(schedule)
	return len(schedule) <= 100 && len(fields) == 5 && strings.Join(fields, " ") == schedule
}

func commandForCronScheduleUpdate(task managedCronTask, clock, expectedSchedule string, expectedEnabled bool) (string, bool) {
	if !cronClockRe.MatchString(clock) || !validExpectedCronSchedule(expectedSchedule) {
		return "", false
	}
	hour, _ := strconv.Atoi(clock[:2])
	minute, _ := strconv.Atoi(clock[3:])
	return commandForCronMutation(task, expectedSchedule, expectedEnabled, "schedule", hour, minute)
}

const pausedCronPrefix = "# backup-monitor-paused "

// Only our explicit pause marker is editable; ordinary comments stay untouched.
// Snapshot guards cover both the clock and the enabled state.
const managedCronMutationAwk = `
  { original=$0; paused=0; if (index($0, "# backup-monitor-paused ") == 1) { sub(/^# backup-monitor-paused /, ""); paused=1 } }
  NF >= 7 && $1 !~ /^#/ && $6 == "/bin/bash" && ($7 == script || ($7 == wrapper && $8 == id)) {
    actual=$1 " " $2 " " $3 " " $4 " " $5
    if (actual != expected) {
      other_schedules[actual]=1
      print original
      next
    }
    matches++
    if ((1-paused) != expected_enabled) stale=1
    if (operation == "enable") print $0
    else if (operation == "pause") print "# backup-monitor-paused " $0
    else if (operation == "schedule") {
      proposed=minute " " hour " " $3 " " $4 " " $5
      match($0, /^[[:space:]]*[^[:space:]]+[[:space:]]+[^[:space:]]+[[:space:]]+[^[:space:]]+[[:space:]]+[^[:space:]]+[[:space:]]+[^[:space:]]+[[:space:]]+/)
      print (paused ? "# backup-monitor-paused " : "") minute " " hour " " $3 " " $4 " " $5 " " substr($0, RLENGTH + 1)
    }
    next
  }
  { print original }
  END {
    if (matches == 0) { print "CRON_NOT_SCHEDULED" > "/dev/stderr"; exit 1 }
    if (matches > 1) { print "CRON_DUPLICATE" > "/dev/stderr"; exit 1 }
    if (stale) { print "CRON_SCHEDULE_CHANGED" > "/dev/stderr"; exit 1 }
    if (operation == "schedule" && other_schedules[proposed]) { print "CRON_DUPLICATE" > "/dev/stderr"; exit 1 }
  }
`

func commandForCronDeletion(task managedCronTask, expectedSchedule string, expectedEnabled bool) (string, bool) {
	return commandForCronMutation(task, expectedSchedule, expectedEnabled, "delete", 0, 0)
}

func commandForCronStateUpdate(task managedCronTask, expectedSchedule string, expectedEnabled, enabled bool) (string, bool) {
	operation := "pause"
	if enabled {
		operation = "enable"
	}
	return commandForCronMutation(task, expectedSchedule, expectedEnabled, operation, 0, 0)
}

func commandForCronMutation(task managedCronTask, expectedSchedule string, expectedEnabled bool, operation string, hour, minute int) (string, bool) {
	allowed, ok := managedCronTaskByID(task.selector())
	if !ok || allowed.Script != task.Script || !validExpectedCronSchedule(expectedSchedule) || (operation != "delete" && operation != "pause" && operation != "enable" && operation != "schedule") {
		return "", false
	}
	expectedState := 0
	if expectedEnabled {
		expectedState = 1
	}
	result := "UPDATED"
	if operation == "delete" {
		result = "DELETED"
	}
	return fmt.Sprintf(`set -eu
umask 077
SCRIPT=%q
TASK_ID=%q
WRAPPER=%q
EXPECTED_ENABLED=%d
OPERATION=%q
HOUR=%d
MINUTE=%d
EXPECTED=$(printf '%%s' '%s' | base64 -d)
exec 9>/root/.backup-monitor-crontab.lock
flock -x 9
exec 8>/root/.backup-monitor-job-%s.lock
if ! flock -n 8; then
  printf 'CRON_ALREADY_RUNNING\n' >&2
  exit 1
fi
TMP_DIR=$(mktemp -d /tmp/backup-monitor-cron.XXXXXXXX)
trap 'rm -f -- "$TMP_DIR/current" "$TMP_DIR/updated"; rmdir -- "$TMP_DIR"' EXIT
if ! crontab -l > "$TMP_DIR/current" 2>/dev/null; then
  printf 'CRON_NOT_SCHEDULED\n' >&2
  exit 1
fi
if ! awk -v script="$SCRIPT" -v id="$TASK_ID" -v wrapper="$WRAPPER" -v expected="$EXPECTED" -v expected_enabled="$EXPECTED_ENABLED" -v operation="$OPERATION" -v hour="$HOUR" -v minute="$MINUTE" '%s' "$TMP_DIR/current" > "$TMP_DIR/updated"; then
  exit 1
fi
RECOVERY_ROOT=/root/backup-monitor/cron-recovery
for dir in /root/backup-monitor "$RECOVERY_ROOT"; do
  if [ -L "$dir" ] || { [ -e "$dir" ] && [ ! -d "$dir" ]; }; then
    printf 'CRON_RECOVERY_UNAVAILABLE\n' >&2
    exit 1
  fi
  mkdir -p -- "$dir"
done
RECOVERY_DIR=$(mktemp -d "$RECOVERY_ROOT/$OPERATION-$TASK_ID.XXXXXXXX")
cp -- "$TMP_DIR/current" "$RECOVERY_DIR/crontab.txt"
chmod 600 "$RECOVERY_DIR/crontab.txt"
crontab "$TMP_DIR/updated"
printf '%s|%%s/crontab.txt\n' "$RECOVERY_DIR"
`, task.Script, task.ID, task.wrapper(), expectedState, operation, hour, minute, base64.StdEncoding.EncodeToString([]byte(expectedSchedule)), task.ID, managedCronMutationAwk, result), true
}

func managedCronTaskByID(id string) (managedCronTask, bool) {
	for _, task := range managedCronTasks() {
		if task.selector() == id {
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
  { if (index($0, "# backup-monitor-paused ") == 1) sub(/^# backup-monitor-paused /, "") }
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
if [ "$TASK_ID" = "cleanup-panel" ]; then
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
nohup setsid /bin/bash -c "$RUNNER" >> "$LOG" 2>&1 </dev/null &
printf 'STARTED\n'
`, task.Script, task.Log, task.ID, task.wrapper(), task.ID, base64.StdEncoding.EncodeToString([]byte(runner)))
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
	InstanceKey     string `json:"instanceKey,omitempty"`
	ID              string `json:"id"`
	Name            string `json:"name"`
	Script          string `json:"script"`
	Schedule        string `json:"schedule"`
	Enabled         bool   `json:"enabled"`
	LastRun         string `json:"last_run"`
	LogUpdatedAt    string `json:"log_updated_at"`
	TrackedAt       string `json:"tracked_at,omitempty"`
	Status          string `json:"status"`
	ScheduleTracked bool   `json:"schedule_tracked"`
	TaskControlled  bool   `json:"task_controlled"`
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
	jobs := make([]managedCronJobResponse, 0, len(managedCronTasks()))
	logs := make(map[string]string)
	runs := make(map[string][]string)
	lines := strings.Split(output, "\n")
	section := ""
	controlled := strings.Contains(output, "__TASK_CONTROL_V1__")
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
		if section != "__CRONTAB__" || line == "" {
			continue
		}
		enabled := !strings.HasPrefix(line, pausedCronPrefix)
		if !enabled {
			line = strings.TrimPrefix(line, pausedCronPrefix)
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if task, tracked, ok := managedTaskForCronFields(fields); ok {
			jobs = append(jobs, managedCronJobResponse{
				ID: task.ID, InstanceKey: task.InstanceKey, Name: task.Name, Script: task.Script,
				Schedule: strings.Join(fields[:5], " "), Enabled: enabled, Status: "unknown", ScheduleTracked: tracked,
			})
		}
	}
	for i := range jobs {
		selector := jobs[i].ID
		if jobs[i].InstanceKey != "" {
			selector += "~" + jobs[i].InstanceKey
		}
		task, _ := managedCronTaskByID(selector)
		jobs[i].LogUpdatedAt = logs[task.Log]
		jobs[i].TaskControlled = jobs[i].ScheduleTracked && (controlled || task.InstanceKey != "")
		if jobs[i].LogUpdatedAt == "" {
			jobs[i].Status = "never"
		}
		if run, ok := runs[task.selector()]; ok {
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
	output, err := executeSSHCommandRaw(managedCronJobsReadCommand())
	if err != nil {
		return nil, err
	}
	return json.Marshal(parseManagedCronJobs(output))
}

func managedCronJobsReadCommand() string {
	var files, pairs []string
	for _, task := range managedCronTasks() {
		files = append(files, fmt.Sprintf("%q", task.Log))
		pairs = append(pairs, fmt.Sprintf("%q", task.selector()+":"+task.Log+":"+task.runStatePath()))
	}
	command := fmt.Sprintf("if [ -f %q ] && [ ! -L %q ] && grep -qx '# backup-monitor task control v1' %q; then printf '__TASK_CONTROL_V1__\\n'; fi\n", scheduledBackupRunnerPath(), scheduledBackupRunnerPath(), scheduledBackupRunnerPath()) + `printf '__CRONTAB__\n'
crontab -l 2>/dev/null || true
printf '__LOGS__\n'
for file in ` + strings.Join(files, " ") + `; do
  if [ -f "$file" ] && [ ! -L "$file" ]; then
    printf '%s|' "$file"
    date -r "$file" '+%Y-%m-%d %H:%M:%S'
  fi
done
printf '__RUNS__\n'
for pair in ` + strings.Join(pairs, " ") + `; do
  selector=${pair%%:*}
  id=${selector%%~*}
  rest=${pair#*:}
  file=${rest%%:*}
  state=${rest#*:}
  [ -f "$file" ] && [ ! -L "$file" ] || continue
  marker=$(tail -n 200 "$file" | awk -F'|' -v id="$id" '$1 == "MONITOR_RUN" && $2 == id {last=$0} END {print last}')
  active=inactive
  lock=/root/.backup-monitor-job-$id.lock
  if [ -f "$lock" ] && ! flock -n "$lock" -c true; then active=active; fi
  if [ -z "$marker" ] && [ "$active" = active ]; then marker="MONITOR_RUN|$id|||running|0"; fi
  case "$marker" in *'|running|'*) ;; *) active=inactive ;; esac
  if [ "$selector" != "$id" ] || [ -e "$state" ]; then
    if [ ! -f "$state" ] || [ -L "$state" ]; then active=inactive
    else pid=$(sed -n 's/.*"pid":\([0-9]*\).*/\1/p' "$state"); [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null || active=inactive; fi
  fi
  [ -z "$marker" ] || printf '%s|%s\n' "$marker" "$active" | awk -F'|' -v selector="$selector" 'BEGIN {OFS="|"} {$2=selector;print}'
done`
	return command
}

func scheduledBackupRunnerPath() string { return configuredScript("backup-monitor-run.sh") }
func backupRunHistoryPath() string      { return configuredLog("backup-monitor-runs.log") }

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
export RCLONE_USE_JSON_LOG=true RCLONE_STATS=5s RCLONE_STATS_LOG_LEVEL=NOTICE
CANCELLED=0
if [ -n "${RUN_STATE:-}" ]; then
  if [ -L "$RUN_STATE" ] || { [ -e "$RUN_STATE" ] && [ ! -f "$RUN_STATE" ]; }; then exit 1; fi
  START_TICKS=$(awk '{print $22}' "/proc/$$/stat")
  STATE_TMP=$(mktemp "$RUN_STATE.tmp.XXXXXXXX") || exit 1
  printf '{"jobId":"%s","runId":"%s","pid":%s,"startTicks":"%s","script":"%s"}\n' "$TASK_ID" "$RUN_ID" "$$" "$START_TICKS" "$SCRIPT" > "$STATE_TMP" || exit 1
  mv -f -- "$STATE_TMP" "$RUN_STATE" || exit 1
fi
finish_run() {
  code=$?
  trap - EXIT
  status=success
  [ "$code" -eq 0 ] || status=failed
  marker=$(printf 'MONITOR_RUN|%s|%s|%s|%s|%s|%s' "$TASK_ID" "$START_DATE" "$START_TIME" "$status" "$(( $(date +%s) - START_SECONDS ))" "$RUN_ID")
  printf '%s\n' "$marker"
  [ "$CANCELLED" -eq 0 ] || printf 'MONITOR_CANCEL|%s|%s\n' "$TASK_ID" "$RUN_ID"
  if [ -n "$RUN_HISTORY" ]; then
    printf '%s\n' "$marker" >> "$RUN_HISTORY" || printf 'CRON_HISTORY_WRITE_FAILED\n' >&2
  fi
  exit "$code"
}
trap finish_run EXIT
printf 'MONITOR_RUN|%s|%s|%s|running|0|%s\n' "$TASK_ID" "$START_DATE" "$START_TIME" "$RUN_ID"
/bin/bash "$SCRIPT" "$@" &
child=$!
trap 'CANCELLED=1; kill -TERM "$child" 2>/dev/null || true; wait "$child" 2>/dev/null || true; exit 143' TERM HUP
trap 'CANCELLED=1; kill -INT "$child" 2>/dev/null || true; wait "$child" 2>/dev/null || true; exit 130' INT
wait "$child"
result=$?
trap - TERM HUP INT
exit "$result"
`

func trackedRunnerForTask(task managedCronTask) string {
	history := ""
	if task.ID == "backup-site" || task.ID == "backup-database" {
		history = backupRunHistoryPath()
	}
	prefix := fmt.Sprintf("SCRIPT=%q\nTASK_ID=%q\nRUN_HISTORY=%q\nRUN_STATE=%q\n", task.Script, task.ID, history, task.runStatePath())
	if task.ID == "backup-site" || task.ID == "backup-database" || task.ID == "backup-panel" {
		prefix += "export BACKUP_MONITOR_ORIGIN=manual\n"
	}
	if task.ID == "backup-panel" {
		prefix += "export BACKUP_PANEL_CAPTURED_LOG=1\n"
	}
	return prefix + trackedRunShell
}

func buildScheduledBackupRunner() string {
	var script strings.Builder
	script.WriteString("#!/bin/bash\n# backup-monitor scheduled tracking v1\n# backup-monitor task control v1\nset -u\numask 077\nif [ \"$(ps -o pgid= -p $$ | tr -d ' ')\" != \"$$\" ]; then exec setsid /bin/bash \"$0\" \"$@\"; fi\nTASK_ID=${1:-}\ncase \"$TASK_ID\" in\n")
	for _, id := range []string{"backup-site", "backup-database", "backup-panel", "drive-sync", "cleanup-panel", "integrity-check"} {
		task, _ := managedCronTaskByID(id)
		fmt.Fprintf(&script, "  %s) SCRIPT=%q; LOG=%q ;;\n", task.ID, task.Script, task.Log)
	}
	script.WriteString("  *) printf 'CRON_INVALID_JOB\\n' >&2; exit 1 ;;\nesac\nshift\nexport BACKUP_MONITOR_ORIGIN=cron\n")
	fmt.Fprintf(&script, "RUN_HISTORY=%q\n", backupRunHistoryPath())
	fmt.Fprintf(&script, "RUN_STATE=%q/.backup-monitor-$TASK_ID.run.json\n", backupSystemSettings().LogsDir)
	script.WriteString("if [ \"$TASK_ID\" = backup-panel ]; then\n  RUN_HISTORY=''\n  export BACKUP_PANEL_CAPTURED_LOG=1\nfi\n")
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
	return configuredBackupCommand(script.String())
}

// Only two allowlisted command forms are managed. Scripts, arguments, times,
// comments, environment variables and unrelated cron entries stay intact.
const trackingCronAwk = `
  { original=$0; paused_prefix=""; if (index($0, "# backup-monitor-paused ") == 1) { sub(/^# backup-monitor-paused /, ""); paused_prefix="# backup-monitor-paused " } }
  NF >= 7 && $1 !~ /^#/ && $6 == "/bin/bash" {
    id=""
    if ($7 == "/root/scripts/backup-site.sh") id="backup-site"
    else if ($7 == "/root/scripts/backup-database.sh") id="backup-database"
    else if ($7 == "/root/scripts/backup-panel.sh") id="backup-panel"
    else if ($7 == "/root/scripts/auto_backup.sh") id="drive-sync"
    else if ($7 == "/root/scripts/cleanup-panel-backups.sh") id="cleanup-panel"
    else if ($7 == "/root/scripts/check_integrity.sh") id="integrity-check"
    else if ($7 == wrapper && ($8 == "backup-site" || $8 == "backup-database" || $8 == "backup-panel" || $8 == "drive-sync" || $8 == "cleanup-panel" || $8 == "integrity-check")) id=$8
    if (id != "") {
      counts[id SUBSEP $1 " " $2 " " $3 " " $4 " " $5]++
      total++
      if ($7 != wrapper) {
        match($0, /^[[:space:]]*[^[:space:]]+[[:space:]]+[^[:space:]]+[[:space:]]+[^[:space:]]+[[:space:]]+[^[:space:]]+[[:space:]]+[^[:space:]]+[[:space:]]+/)
        prefix=substr($0, 1, RLENGTH)
        command=substr($0, RLENGTH+1)
        match(command, /^\/bin\/bash[[:space:]]+[^[:space:]]+/)
        print paused_prefix prefix "/bin/bash " wrapper " " id substr(command, RLENGTH+1)
        next
      }
    }
  }
  { print original }
  END {
    if (!total) { print "CRON_NOT_SCHEDULED" > "/dev/stderr"; exit 1 }
    for (id in counts) if (counts[id] > 1) { print "CRON_DUPLICATE" > "/dev/stderr"; exit 1 }
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
    backup-panel) script=/root/scripts/backup-panel.sh ;;
    drive-sync) script=/root/scripts/auto_backup.sh ;;
    cleanup-panel) script=/root/scripts/cleanup-panel-backups.sh ;;
    integrity-check) script=/root/scripts/check_integrity.sh ;;
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
`, scheduledBackupRunnerPath(), backupRunHistoryPath(), configuredBackupCommand(trackingCronAwk), base64.StdEncoding.EncodeToString([]byte(buildScheduledBackupRunner())))
}

func managedTaskForCronFields(fields []string) (managedCronTask, bool, bool) {
	if len(fields) < 7 || fields[5] != "/bin/bash" {
		return managedCronTask{}, false, false
	}
	for _, task := range managedCronTasks() {
		if fields[6] == task.Script {
			return task, false, true
		}
		if len(fields) >= 8 && fields[6] == task.wrapper() && fields[7] == task.ID {
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
    id = (NF >= 7 && $7 ~ /^[0-9]+-[0-9]+$/) ? $7 : ""
    printf "{\"kind\":\"run\",\"name\":\"%s\",\"date\":\"%s\",\"time\":\"%s\",\"duration\":%s,\"status\":\"%s\",\"runId\":\"%s\"},", name, $3, $4, $6, $5, id
  }
`

func backupRunActivityCommand() string {
	return fmt.Sprintf(`{
  tail -q -n 1000 %s 2>/dev/null | awk -F'|' 'NF == 6 && $1 == "MONITOR_RUN"'
  if [ -f %q ] && [ ! -L %q ]; then tail -n 10000 %q; fi
} | awk -F'|' '%s' | sed 's/,$//'`, managedLogArguments("backup-site", "backup-database"), backupRunHistoryPath(), backupRunHistoryPath(), backupRunHistoryPath(), backupRunActivityAwk)
}

const localInventoryTTL = 2 * time.Second
const driveInventoryTTL = 10 * time.Second

var driveDateFolderPattern = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)

// One listing supplies both the dashboard counters and the recovery file picker.
// Cache failures as values as well, so opening both views cannot bypass Drive's
// retry/quota backoff. Listing remains the last confirmed listing on failure.
type driveInventory struct {
	Summary    json.RawMessage `json:"summary"`
	Listing    string          `json:"listing"`
	HasListing bool            `json:"hasListing"`
}

func (d driveInventory) driveError() string {
	var status struct {
		Error string `json:"driveError"`
	}
	_ = json.Unmarshal(d.Summary, &status)
	return status.Error
}

const driveInventoryReadCommand = `
TODAY=$(date +"%Y-%m-%d")
LSF_OUT=$(timeout -k 5s 40s /usr/bin/rclone lsf -R --format "pst" gdrive:Backup --config /root/.config/rclone/rclone.conf --log-level ERROR --fast-list --timeout 15s --contimeout 5s 2>&1)
LSF_STATUS=$?
ABOUT=""
ABOUT_CACHE=/tmp/rclone_about.json
if [ -f "$ABOUT_CACHE" ] && [ ! -L "$ABOUT_CACHE" ]; then
  CACHE_TIME=$(stat -c '%Y' "$ABOUT_CACHE" 2>/dev/null || printf '0')
  CACHE_AGE=$(( $(date +%s) - CACHE_TIME ))
  if [ "$CACHE_AGE" -ge 0 ] && [ "$CACHE_AGE" -lt 86400 ]; then
    ABOUT=$(cat "$ABOUT_CACHE")
  fi
fi
if [ "$LSF_STATUS" -eq 0 ] && [ -z "$ABOUT" ]; then
  ABOUT=$(timeout -k 5s 10s /usr/bin/rclone about gdrive: --config /root/.config/rclone/rclone.conf --log-level ERROR --fast-list --json 2>/dev/null)
  if [ -n "$ABOUT" ]; then
    ABOUT_TMP=$(mktemp /tmp/rclone_about.XXXXXXXX)
    printf '%s\n' "$ABOUT" > "$ABOUT_TMP"
    mv -f -- "$ABOUT_TMP" "$ABOUT_CACHE"
  fi
fi
if [ -z "$ABOUT" ] && [ -f "$ABOUT_CACHE" ] && [ ! -L "$ABOUT_CACHE" ]; then
  ABOUT=$(cat "$ABOUT_CACHE")
fi
if [ -z "$ABOUT" ]; then ABOUT="{}"; fi
printf '%s\n%s\n__DRIVE_ABOUT__\n%s\n__DRIVE_LISTING__\n%s' "$TODAY" "$LSF_STATUS" "$ABOUT" "$LSF_OUT"
`

type backupHistoryDay struct {
	Date  string `json:"date"`
	Bytes int64  `json:"bytes"`
	Files int64  `json:"files"`
}

// Aggregate the exact same path/size fields that the previous awk reader used.
// The third lsf field is retained untouched for recovery-snapshots.
func parseDriveInventory(output string, fetchedAt time.Time) (driveInventory, error) {
	meta, listing, ok := strings.Cut(output, "\n__DRIVE_LISTING__\n")
	if !ok {
		return driveInventory{}, fmt.Errorf("Thiếu danh sách Google Drive")
	}
	header, about, ok := strings.Cut(meta, "\n__DRIVE_ABOUT__\n")
	fields := strings.Split(header, "\n")
	if !ok || len(fields) != 2 || !json.Valid([]byte(about)) {
		return driveInventory{}, fmt.Errorf("Thông số Google Drive không hợp lệ")
	}
	exitCode, err := strconv.Atoi(fields[1])
	if err != nil {
		return driveInventory{}, err
	}
	driveError := ""
	if exitCode != 0 {
		lower := strings.ToLower(listing)
		if strings.Contains(lower, "ratelimitexceeded") || strings.Contains(lower, "quota") && strings.Contains(lower, "exceeded") || strings.Contains(lower, "rate") && strings.Contains(lower, "limit") && strings.Contains(lower, "exceeded") {
			driveError = "Google Drive API đang giới hạn truy vấn; cần kiểm tra quota và cấu hình client ID riêng cho rclone"
		} else {
			driveError = fmt.Sprintf("rclone mã lỗi %d; kiểm tra DNS, mạng và cấu hình rclone trên máy chủ", exitCode)
		}
		listing = ""
	}
	days := map[string]*backupHistoryDay{}
	todayDirs := map[string]bool{}
	todayCounts := map[string]int64{"site": 0, "database": 0, "panel": 0}
	todayBytes := map[string]int64{"site": 0, "database": 0, "panel": 0}
	var totalBytes, totalFiles int64
	for _, line := range strings.Split(listing, "\n") {
		parts := strings.SplitN(line, ";", 3)
		if len(parts) < 2 {
			continue
		}
		size, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil || size < 0 {
			continue
		}
		totalBytes += size
		totalFiles++
		path := strings.Split(parts[0], "/")
		day := path[0]
		if !driveDateFolderPattern.MatchString(day) {
			continue
		}
		if days[day] == nil {
			days[day] = &backupHistoryDay{Date: day}
		}
		days[day].Bytes += size
		days[day].Files++
		if day == fields[0] && len(path) >= 2 && path[1] != "" {
			todayDirs[path[1]] = true
			if _, known := todayCounts[path[1]]; known {
				todayCounts[path[1]]++
				todayBytes[path[1]] += size
			}
		}
	}
	history := make([]backupHistoryDay, 0, len(days))
	for _, day := range days {
		history = append(history, *day)
	}
	sort.Slice(history, func(i, j int) bool { return history[i].Date < history[j].Date })
	dirs := make([]string, 0, len(todayDirs))
	for dir := range todayDirs {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	dirsValue := strings.Join(dirs, ",")
	if len(dirs) > 0 {
		dirsValue += ","
	}
	status := map[string]interface{}{
		"about": json.RawMessage(about), "size": map[string]int64{"bytes": totalBytes, "count": totalFiles},
		"dirs": dirsValue, "totalFolders": len(days), "history": history,
		"driveError": driveError, "todayBreakdown": todayCounts, "todayBreakdownBytes": todayBytes,
		"driveDay": fields[0],
	}
	if driveError != "" {
		status["size"] = nil
	} else {
		status["driveDataAt"] = fetchedAt.UTC().Format(time.RFC3339)
		status["driveStale"] = false
	}
	summary, err := json.Marshal(status)
	return driveInventory{Summary: summary, Listing: listing, HasListing: exitCode == 0}, err
}

func markDriveInventoryStale(data []byte) []byte {
	var inventory driveInventory
	if json.Unmarshal(data, &inventory) != nil {
		return nil
	}
	if stale := markDriveStatusStale(inventory.Summary); len(stale) > 0 {
		inventory.Summary = stale
	}
	marked, _ := json.Marshal(inventory)
	return marked
}

func readDriveInventory() (driveInventory, error) {
	// Capture the resolved destination before the background refresh is launched.
	command := configuredBackupCommand(driveInventoryReadCommand)
	data, err := cachedGetWithPolicy("drive-inventory", driveInventoryTTL, func() ([]byte, error) {
		output, err := executeSSHCommandRaw(command)
		if err != nil {
			return nil, err
		}
		inventory, err := parseDriveInventory(output, time.Now())
		if err != nil {
			return nil, err
		}
		if !inventory.HasListing {
			cacheMu.Lock()
			previous := cache["drive-inventory"].value
			cacheMu.Unlock()
			var good driveInventory
			if json.Unmarshal(previous, &good) == nil && good.HasListing {
				inventory.Listing, inventory.HasListing = good.Listing, true
			}
		}
		return json.Marshal(inventory)
	}, func(data []byte) time.Duration {
		var inventory driveInventory
		if json.Unmarshal(data, &inventory) != nil {
			return driveInventoryTTL
		}
		return backupStatusTTL(inventory.Summary)
	}, markDriveInventoryStale)
	var inventory driveInventory
	if err == nil {
		err = json.Unmarshal(data, &inventory)
	}
	return inventory, err
}

const localActivityReadCommand = `
ACTIVITY=$(__DRIVE_ACTIVITY_COMMAND__)
LOCAL_LOGS=$(awk '
    /start backup\[/ {
        match($0, /\[(.*)\]/, arr)
        dt = arr[1]
        split(dt, d, " ")
        date = d[1]
        time = d[2]
    }
    /Backup site: / {
        match($0, /site: (.*)/, arr)
        name = "Backup Website: " arr[1]
    }
    /Backup .*database: / {
        match($0, /database: (.*)/, arr)
        name = "Backup Database: " arr[1]
    }
    /Compression completed, took / {
        match($0, /took ([0-9.]*) seconds/, arr)
        duration = arr[1]
        status = "Successful"
        printf "{\"name\":\"%s\",\"date\":\"%s\",\"time\":\"%s\",\"duration\":\"%s\",\"status\":\"%s\"},", name, date, time, duration, status
    }
    /Database backup completed, taking / {
        match($0, /taking ([0-9.]*) seconds/, arr)
        duration = arr[1]
        status = "Successful"
        printf "{\"name\":\"%s\",\"date\":\"%s\",\"time\":\"%s\",\"duration\":\"%s\",\"status\":\"%s\"},", name, date, time, duration, status
    }
    ' /www/server/cron/*.log 2>/dev/null | sed 's/,$//')
CUSTOM_LOCAL_LOGS=$(__CUSTOM_LOCAL_ACTIVITY_COMMAND__)
RUN_LOGS=$(__BACKUP_RUN_ACTIVITY_COMMAND__)
PANEL_LOGS=$(__PANEL_ACTIVITY_COMMAND__)
PANEL_ARCHIVES=$(__PANEL_ARCHIVE_ACTIVITY_COMMAND__)
LOCAL_ACTIVITY="["
SEP=""
for ENTRIES in "$LOCAL_LOGS" "$CUSTOM_LOCAL_LOGS" "$RUN_LOGS" "$PANEL_LOGS" "$PANEL_ARCHIVES"; do
  if [ -n "$ENTRIES" ]; then LOCAL_ACTIVITY="$LOCAL_ACTIVITY$SEP$ENTRIES"; SEP=","; fi
done
LOCAL_ACTIVITY="$LOCAL_ACTIVITY]"
echo "{\"activity\": $ACTIVITY, \"localActivity\": $LOCAL_ACTIVITY}"
`

type backupActivity struct {
	Drive json.RawMessage `json:"activity"`
	Local json.RawMessage `json:"localActivity"`
}

func parseBackupActivity(data []byte) (backupActivity, error) {
	var activity backupActivity
	if err := json.Unmarshal(data, &activity); err != nil {
		return backupActivity{}, err
	}
	for _, entries := range []json.RawMessage{activity.Drive, activity.Local} {
		if len(entries) == 0 || entries[0] != '[' {
			return backupActivity{}, fmt.Errorf("Nhật ký sao lưu không hợp lệ")
		}
	}
	return activity, nil
}

func readBackupActivity() ([]byte, error) {
	command := resolvedBackupStatusCommand(localActivityReadCommand)
	return cachedGetFreshOrError("backup-activity", localInventoryTTL, func() ([]byte, error) {
		output, err := executeSSHCommandRaw(command)
		if err == nil {
			_, err = parseBackupActivity([]byte(output))
		}
		return []byte(output), err
	})
}

func readDriveSummary() ([]byte, error) {
	cacheMu.Lock()
	_, hasInventory := cache["drive-inventory"]
	cacheMu.Unlock()
	good := currentGoodDriveStatus()
	if !hasInventory && len(good) > 0 {
		// Keep the persistent last-good summary immediately available at startup.
		go func() { _, _ = readDriveInventory() }()
		return markDriveStatusStale(good), nil
	}
	inventory, err := readDriveInventory()
	if err != nil {
		log.Printf("drive inventory: %v", err)
		return mergeDriveStatus([]byte(`{"driveError":"Không thể đọc Google Drive"}`), good)
	}
	summary := inventory.Summary
	if inventory.driveError() != "" {
		return mergeDriveStatus(summary, good)
	}
	if !bytes.Equal(summary, good) {
		var status struct {
			Stale bool `json:"driveStale"`
		}
		if json.Unmarshal(summary, &status) == nil && !status.Stale {
			rememberGoodDriveStatus(summary)
		}
	}
	return summary, nil
}

func readBackupStatus() ([]byte, error) {
	type result struct {
		data []byte
		err  error
	}
	activityResult := make(chan result, 1)
	go func() {
		data, err := readBackupActivity()
		activityResult <- result{data, err}
	}()
	summary, err := readDriveSummary()
	if err != nil {
		log.Printf("drive summary: %v", err)
		summary = []byte(`{"driveError":"Không thể đọc Google Drive"}`)
	}
	activity := <-activityResult
	if activity.err != nil {
		log.Printf("backup activity: %v", activity.err)
	}
	return combineBackupStatus(summary, activity.data, activity.err)
}

func combineBackupStatus(summary, activityData []byte, activityErr error) ([]byte, error) {
	var status map[string]json.RawMessage
	if err := json.Unmarshal(summary, &status); err != nil || status == nil {
		status = map[string]json.RawMessage{"driveError": json.RawMessage(`"Không thể đọc Google Drive"`)}
	}
	activity, err := parseBackupActivity(activityData)
	if activityErr != nil || err != nil {
		status["activityError"] = json.RawMessage(`"Không thể đọc nhật ký sao lưu"`)
	} else {
		status["activity"], status["localActivity"] = activity.Drive, activity.Local
	}
	return json.Marshal(status)
}

func readLocalInventory() ([]byte, error) {
	command := configuredLocalInventoryCommand()
	return cachedGetFreshOrError("local-snapshots", localInventoryTTL, func() ([]byte, error) {
		output, err := executeSSHCommandRaw(command)
		return []byte(output), err
	})
}

func readServerStatus() ([]byte, error) {
	command := serverStatusReadCommand()
	return cachedGetFreshOrError("server-status", localInventoryTTL, func() ([]byte, error) {
		type result struct {
			output string
			err    error
		}
		metrics := make(chan result, 1)
		go func() {
			output, err := executeSSHCommandRaw(command)
			metrics <- result{output, err}
		}()
		inventory, err := readLocalInventory()
		status := <-metrics
		if status.err != nil {
			return nil, status.err
		}
		if err != nil {
			log.Printf("local backup inventory: %v", err)
			return parseServerStatus(status.output)
		}
		return parseServerStatus(status.output + "\n__LOCAL_INVENTORY__\n" + string(inventory))
	})
}

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

var (
	driveGoodMu     sync.Mutex
	driveGoodStatus []byte
)

func driveStatusCachePath() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "backup-monitor", "drive-status.json")
}

func readGoodDriveStatus(path string) []byte {
	if path == "" {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var status struct {
		DriveError string          `json:"driveError"`
		Size       json.RawMessage `json:"size"`
		History    json.RawMessage `json:"history"`
	}
	if json.Unmarshal(data, &status) != nil || status.DriveError != "" || len(status.Size) == 0 || string(status.Size) == "null" || len(status.History) == 0 || string(status.History) == "null" {
		return nil
	}
	return data
}

func markDriveStatusStale(good []byte) []byte {
	var status map[string]json.RawMessage
	if json.Unmarshal(good, &status) != nil {
		return nil
	}
	if len(status["size"]) == 0 || string(status["size"]) == "null" || len(status["history"]) == 0 || string(status["history"]) == "null" {
		return nil
	}
	status["driveStale"] = json.RawMessage("true")
	data, err := json.Marshal(status)
	if err != nil {
		return nil
	}
	return data
}

// The Drive listing is separate from the local backup logs. A failed Drive
// refresh must not replace the last confirmed Drive counts and history with 0.
func mergeDriveStatus(raw, good []byte) ([]byte, error) {
	var status map[string]json.RawMessage
	if err := json.Unmarshal(raw, &status); err != nil {
		return nil, err
	}
	var driveError string
	if field := status["driveError"]; len(field) == 0 || json.Unmarshal(field, &driveError) != nil || driveError == "" {
		return nil, errors.New("invalid Drive error response")
	}
	fields := []string{"about", "size", "dirs", "totalFolders", "history", "todayBreakdown", "todayBreakdownBytes", "driveDataAt", "driveDay"}
	var previous map[string]json.RawMessage
	hasGood := json.Unmarshal(markDriveStatusStale(good), &previous) == nil
	for _, field := range fields {
		delete(status, field)
		if hasGood {
			if value, ok := previous[field]; ok {
				status[field] = value
			}
		}
	}
	if hasGood {
		status["driveStale"] = json.RawMessage("true")
	}
	return json.Marshal(status)
}

func backupStatusTTL(data []byte) time.Duration {
	var status struct {
		DriveError string `json:"driveError"`
	}
	if json.Unmarshal(data, &status) == nil && status.DriveError != "" {
		if strings.Contains(status.DriveError, "giới hạn truy vấn") {
			return 5 * time.Minute
		}
		return 30 * time.Second
	}
	return driveInventoryTTL
}

func currentGoodDriveStatus() []byte {
	driveGoodMu.Lock()
	defer driveGoodMu.Unlock()
	return driveGoodStatus
}

func rememberGoodDriveStatus(data []byte) {
	driveGoodMu.Lock()
	driveGoodStatus = data
	driveGoodMu.Unlock()
	path := driveStatusCachePath()
	if path == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		log.Printf("cannot create Drive status cache directory: %v", err)
		return
	}
	if err := writeFileAtomic0600(path, data); err != nil {
		log.Printf("cannot save Drive status cache: %v", err)
	}
}

func warmDriveStatusCache() {
	good := readGoodDriveStatus(driveStatusCachePath())
	if len(good) == 0 {
		return
	}
	driveGoodMu.Lock()
	driveGoodStatus = good
	driveGoodMu.Unlock()
}

func clearDriveStatusSnapshot() {
	driveGoodMu.Lock()
	driveGoodStatus = nil
	driveGoodMu.Unlock()
	if cachePath := driveStatusCachePath(); cachePath != "" {
		// Only the app-owned cached summary is removed, never backup data.
		_ = os.Remove(cachePath)
	}
}

func loadJWTSecret(mode string) (string, error) {
	if value := os.Getenv("JWT_SECRET"); value != "" {
		return value, nil
	}
	if mode != "local-agent" {
		return requiredEnv("JWT_SECRET")
	}
	path := strings.TrimSpace(os.Getenv("JWT_SECRET_FILE"))
	if path == "" {
		path = "/data/jwt-secret"
	}
	if data, err := readPrivateSecret(path); err == nil {
		return string(data), nil
	} else if !os.IsNotExist(err) {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", err
	}
	key := make([]byte, 48)
	if _, err := rand.Read(key); err != nil {
		return "", err
	}
	secret := base64.RawURLEncoding.EncodeToString(key)
	// A hard-link publishes only a completed key, never overwrites another
	// instance's persisted key and does not expose a partial secret at startup.
	f, err := os.CreateTemp(filepath.Dir(path), ".jwt-secret-*")
	if err != nil {
		return "", err
	}
	defer os.Remove(f.Name())
	err = f.Chmod(0600)
	if err == nil {
		_, err = f.WriteString(secret)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return "", err
	}
	if closeErr != nil {
		return "", closeErr
	}
	if err = os.Link(f.Name(), path); err != nil && !os.IsExist(err) {
		return "", err
	}
	data, err := readPrivateSecret(path)
	return string(data), err
}
func readPrivateSecret(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 4096 {
		return nil, errors.New("unsafe persisted JWT secret")
	}
	data, err := os.ReadFile(path)
	if err == nil && len(data) < 32 {
		return nil, errors.New("invalid persisted JWT secret")
	}
	return data, err
}
func loadLocalAgent() (*hostagent.Client, error) {
	socket := strings.TrimSpace(os.Getenv("HOST_AGENT_SOCKET"))
	if socket == "" {
		socket = "/run/host-agent/agent.sock"
	}
	tokenPath := strings.TrimSpace(os.Getenv("HOST_AGENT_TOKEN_FILE"))
	if tokenPath == "" {
		tokenPath = "/run/host-agent-secrets/agent-token"
	}
	info, err := os.Lstat(tokenPath)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0027 != 0 || info.Size() > 4096 {
		return nil, errors.New("invalid local agent credential file")
	}
	data, err := os.ReadFile(tokenPath)
	if err != nil {
		return nil, errors.New("cannot read local agent credential")
	}
	return hostagent.NewClient(socket, strings.TrimSpace(string(data)))
}

// Installer creates the first account before opening the LAN listener. Neither
// passwords nor their hashes are passed via argv, environment or logs.
func adminInstallCommand(operation string) error {
	account, err := initializeAdminAccount()
	if err != nil {
		return err
	}
	if operation == "account-status" {
		if account.username() == "" {
			fmt.Println("empty")
		} else {
			fmt.Println("exists")
		}
		return nil
	}
	reader := bufio.NewReader(io.LimitReader(os.Stdin, 4096))
	username, err := reader.ReadString('\n')
	if err != nil {
		return errors.New("username required on stdin")
	}
	password, err := reader.ReadString('\n')
	if err != nil {
		return errors.New("password required on stdin")
	}
	if _, err := reader.ReadByte(); err != io.EOF {
		return errors.New("unexpected administrator input")
	}
	if err := account.create(strings.TrimSuffix(username, "\n"), strings.TrimSuffix(password, "\n"), false); err != nil {
		return errors.New("cannot create administrator: username 3-64 characters, password 12-72 bytes; account must not exist")
	}
	fmt.Println("Administrator created")
	return nil
}

// ===== ROUTER SETUP =====

func setupRouter(cfg AppConfig) *gin.Engine {
	runtimeConfig = cfg

	r := gin.Default()
	_ = r.SetTrustedProxies(nil)
	loginLimiter := newLoginRateLimiter(5, 5*time.Minute)

	// Cấu hình CORS để Frontend gọi được API
	r.Use(cors.New(cors.Config{
		AllowOrigins:     cfg.CORSAllowOrigins,
		AllowMethods:     []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowHeaders:     []string{"Origin", "Content-Type", "Authorization"},
		ExposeHeaders:    []string{"Content-Length"},
		AllowCredentials: false,
	}))

	// Container readiness only; no credentials or remote server details.
	r.GET("/api/health", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	registerAdminSetup(r, cfg, loginLimiter)

	// LOGIN - không cần auth
	r.POST("/api/login", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		clientKey := remoteAddressKey(c.Request.RemoteAddr)
		now := time.Now()
		if !loginLimiter.allowed(clientKey, now) {
			c.Header("Retry-After", "300")
			c.JSON(http.StatusTooManyRequests, gin.H{"error": "Đăng nhập thất bại quá nhiều lần, vui lòng thử lại sau"})
			return
		}
		req, err := readAdminCredentials(c)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Dữ liệu không hợp lệ"})
			return
		}
		if !cfg.Account.verify(req.Username, req.Password) {
			loginLimiter.recordFailure(clientKey, now)
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Tên đăng nhập hoặc mật khẩu không đúng"})
			return
		}
		loginLimiter.reset(clientKey)
		token, err := generateToken(req.Username, cfg.JWTSecret)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể tạo token"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"token": token})
	})

	// Tất cả route bên dưới đều yêu cầu đăng nhập
	auth := r.Group("/api")
	auth.Use(authMiddleware(cfg.JWTSecret, cfg.Account))
	auth.Use(func(c *gin.Context) {
		if c.Request.Method == http.MethodGet && c.Request.URL.Path == "/api/backup-system" {
			c.Next() // The handler accepts remote configuration under its write lock.
			return
		}
		if c.Request.Method == http.MethodPost {
			switch c.Request.URL.Path {
			case "/api/backup-system/apply", "/api/backup-system/scripts/save", "/api/backup-system/drive/connect":
				// These handlers take the write lock themselves. Holding the read
				// lock here would deadlock configuration writes.
				c.Next()
				return
			}
		}
		backupSystemMutation.RLock()
		if c.Request.Method == http.MethodGet && storageReadRoute(c.Request.URL.Path) && backupSystemReadDue(time.Now()) {
			backupSystemMutation.RUnlock()
			backupSystemMutation.Lock()
			err := refreshBackupSystemIfDue(func() ([]byte, error) { return backupSystemOperation("get", nil) }, time.Now())
			backupSystemMutation.Unlock()
			if err != nil {
				c.AbortWithStatusJSON(http.StatusBadGateway, gin.H{"error": "Chưa xác nhận được nơi lưu hiện tại từ máy chủ; hãy thử lại"})
				return
			}
			backupSystemMutation.RLock()
		}
		defer backupSystemMutation.RUnlock()
		if c.Request.Method != http.MethodGet && !strings.HasPrefix(c.Request.URL.Path, "/api/backup-system/") && !backupSystemLoaded.Load() {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"error": "Chưa tải được cấu hình sao lưu từ máy chủ; hãy tải lại mục Hệ thống sao lưu trước khi thực hiện thay đổi"})
			return
		}
		c.Next()
	})

	// Đăng ký các nhóm route theo từng phân hệ chức năng
	registerBackupSystemRoutes(auth)
	registerOperationRoutes(auth)
	registerSnapshotRoutes(auth)
	registerCronRoutes(auth)
	registerServerRoutes(auth)
	registerAlertRoutes(auth)

	return r
}

// ===== APPLICATION ENTRYPOINT =====

func main() {
	if len(os.Args) == 2 && os.Args[1] == "check-host" {
		cfg, err := loadConfigFromEnv()
		if err != nil {
			log.Fatal(err)
		}
		if cfg.HostAgent == nil {
			log.Fatal("check-host requires local-agent mode")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if _, err := cfg.HostAgent.Execute(ctx, "'/usr/local/libexec/backup-monitor/backup-manager' version", nil); err != nil {
			log.Fatal(err)
		}
		fmt.Println("Local host agent connected")
		return
	}
	if len(os.Args) == 2 && (os.Args[1] == "account-status" || os.Args[1] == "setup-admin") {
		if err := adminInstallCommand(os.Args[1]); err != nil {
			log.Fatal(err)
		}
		return
	}
	if len(os.Args) != 1 {
		log.Fatal("API accepts no arguments; retired collector commands are not supported")
	}
	if err := godotenv.Load(); err != nil && !os.IsNotExist(err) {
		log.Fatalf("load .env: %v", err)
	}
	cfg, err := loadConfigFromEnv()
	if err != nil {
		log.Fatalf("invalid configuration: %v", err)
	}

	warmDriveStatusCache()
	runtimeConfig = cfg
	initializeBackupSystem()
	apiCtx, cancelAPI := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancelAPI()
	if err := initializeRestoreState(); err != nil {
		log.Fatalf("Restore JSON state: %v", err)
	}
	defer activeRestores.lock.Close()
	router := setupRouter(cfg)
	log.Printf("Backend listening on %s", cfg.APIBindAddr)
	if err := serveAPI(apiCtx, cfg.APIBindAddr, router); err != nil {
		log.Fatalf("start API server: %v", err)
	}
}

// Drain active downloads and requests before the process exits.
func serveAPI(ctx context.Context, addr string, handler http.Handler) error {
	server := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       90 * time.Second,
	}
	done := make(chan struct{})
	stopped := make(chan error, 1)
	go func() {
		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer cancel()
			err := server.Shutdown(shutdownCtx)
			if err != nil {
				_ = server.Close()
			}
			stopped <- err
		case <-done:
			stopped <- nil
		}
	}()
	err := server.ListenAndServe()
	close(done)
	shutdownErr := <-stopped
	if !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return shutdownErr
}

func requireRestoreState(c *gin.Context) *restoreStore {
	if activeRestores == nil {
		c.JSON(503, gin.H{"error": "Không đọc được trạng thái khôi phục JSON"})
	}
	return activeRestores
}
func registerOperationRoutes(auth *gin.RouterGroup) {
	auth.GET("/cron-jobs/:id/log-page", func(c *gin.Context) {
		task, ok := managedCronTaskByID(c.Param("id"))
		if !ok {
			c.JSON(404, gin.H{"error": "Tác vụ không hợp lệ"})
			return
		}
		raw, err := backupSystemOperation("log-read", map[string]interface{}{"log": backupops.LogRequest{ID: task.selector(), Cursor: c.Query("cursor")}})
		if err != nil {
			c.JSON(409, gin.H{"error": "Không đọc được trang log; hãy làm mới hoặc kiểm tra helper v12"})
			return
		}
		c.Data(200, "application/json", raw)
	})
	auth.GET("/cron-jobs/:id/process", func(c *gin.Context) {
		task, ok := managedCronTaskByID(c.Param("id"))
		if !ok {
			c.JSON(404, gin.H{"error": "Tác vụ không hợp lệ"})
			return
		}
		raw, err := backupSystemOperation("task-status", map[string]interface{}{"task": backupops.TaskRequest{ID: task.selector()}})
		if err != nil {
			c.JSON(409, gin.H{"error": "Không xác minh được tiến trình; cần helper v12 và wrapper có ghi nhận PID"})
			return
		}
		c.Data(200, "application/json", raw)
	})
	auth.POST("/cron-jobs/:id/cancel", func(c *gin.Context) {
		task, ok := managedCronTaskByID(c.Param("id"))
		if !ok {
			c.JSON(404, gin.H{"error": "Tác vụ không hợp lệ"})
			return
		}
		var req struct {
			RunID   string `json:"runId"`
			Confirm bool   `json:"confirm"`
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
		if c.ShouldBindJSON(&req) != nil || !req.Confirm || !regexpRunID(req.RunID) {
			c.JSON(400, gin.H{"error": "Cần xác nhận đúng lần chạy trước khi hủy"})
			return
		}
		raw, err := backupSystemOperation("task-cancel", map[string]interface{}{"task": backupops.TaskRequest{ID: task.selector(), RunID: req.RunID}})
		if err != nil {
			c.JSON(409, gin.H{"error": "Không hủy được hoặc lần chạy đã thay đổi; hãy làm mới"})
			return
		}
		invalidate("cron-jobs")
		c.Data(202, "application/json", raw)
	})

	auth.GET("/operations/restore-check-status", func(c *gin.Context) {
		raw, err := backupSystemOperation("restore-check-status", nil)
		if err != nil {
			c.JSON(503, gin.H{"error": "Không đọc được trạng thái kiểm tra"})
			return
		}
		c.Data(200, "application/json", raw)
	})
	registerRestoreRoutes(auth)
}
func regexpRunID(id string) bool {
	return len(id) <= 128 && strings.Contains(id, "-") && strings.Trim(id, "0123456789-") == ""
}
func registerRestoreRoutes(auth *gin.RouterGroup) {
	auth.POST("/restore/preview", func(c *gin.Context) {
		db := requireRestoreState(c)
		if db == nil {
			return
		}
		var req struct {
			Path   string `json:"path"`
			Parent string `json:"parent"`
			Name   string `json:"name"`
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
		if c.ShouldBindJSON(&req) != nil || !validSnapshotPath(req.Path) {
			c.JSON(400, gin.H{"error": "Tệp backup không hợp lệ"})
			return
		}
		var destination string
		if req.Parent != "" || req.Name != "" {
			var err error
			destination, err = backupops.RestoreDestination(req.Parent, req.Name)
			if err != nil {
				c.JSON(400, gin.H{"error": err.Error()})
				return
			}
		}
		raw, err := backupSystemOperation("restore-preview", map[string]interface{}{"restore": backupops.RestoreRequest{Path: req.Path, Parent: req.Parent, Name: req.Name}})
		if err != nil {
			c.JSON(409, gin.H{"error": "Không kiểm tra được bản sao lưu/nơi lưu (cần helper v24 trên AlmaLinux): " + err.Error()})
			return
		}
		var plan backupops.RestorePlan
		if json.Unmarshal(raw, &plan) == nil && destination != "" && (plan.Directory != destination || plan.Parent != req.Parent || plan.Name != req.Name) {
			c.JSON(409, gin.H{"error": "Helper chưa hỗ trợ nơi lưu/tên đã chọn; cập nhật helper v24 trên AlmaLinux. Chưa bắt đầu tải."})
			return
		}
		if json.Unmarshal(raw, &plan) != nil || plan.Status != "preview" || plan.Path != req.Path || db.saveRestore(c.Request.Context(), plan) != nil {
			c.JSON(503, gin.H{"error": "Không lưu được kế hoạch khôi phục; chưa bắt đầu tải hoặc giải nén"})
			return
		}
		c.Data(200, "application/json", raw)
	})
	auth.POST("/restore/start", func(c *gin.Context) {
		db := requireRestoreState(c)
		if db == nil {
			return
		}
		var req struct {
			Token   string `json:"token"`
			Confirm bool   `json:"confirm"`
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4096)
		if c.ShouldBindJSON(&req) != nil || !req.Confirm || !validRestoreToken(req.Token) {
			c.JSON(400, gin.H{"error": "Cần kiểm tra và xác nhận bản sao lưu trước"})
			return
		}
		if db.claimRestore(c.Request.Context(), req.Token) != nil {
			c.JSON(409, gin.H{"error": "Không tìm thấy kế hoạch chờ xác nhận của máy chủ này"})
			return
		}
		raw, err := backupSystemOperation("restore-start", map[string]interface{}{"restore": backupops.RestoreRequest{Token: req.Token}})
		if err != nil {
			c.JSON(409, gin.H{"error": "Không bắt đầu được tải/giải nén; hãy kiểm tra lại: " + err.Error()})
			return
		}
		var plan backupops.RestorePlan
		if json.Unmarshal(raw, &plan) != nil || plan.Token != req.Token || plan.Status != "queued" {
			c.JSON(503, gin.H{"error": "Chưa xác nhận được yêu cầu khôi phục"})
			return
		}
		if db.saveRestore(c.Request.Context(), plan) != nil {
			c.Header("X-Restore-History", "unconfirmed")
		}
		c.Data(202, "application/json", raw)
	})
	auth.GET("/restore/:token", func(c *gin.Context) {
		db := requireRestoreState(c)
		if db == nil {
			return
		}
		if !validRestoreToken(c.Param("token")) {
			c.JSON(400, gin.H{"error": "Mã yêu cầu không hợp lệ"})
			return
		}
		if !db.knowsRestore(c.Param("token")) {
			c.JSON(404, gin.H{"error": "Không có yêu cầu khôi phục của máy chủ này"})
			return
		}
		raw, err := backupSystemOperation("restore-status", map[string]interface{}{"restore": backupops.RestoreRequest{Token: c.Param("token")}})
		if err != nil {
			c.JSON(404, gin.H{"error": "Không đọc được trạng thái khôi phục"})
			return
		}
		var plan backupops.RestorePlan
		if json.Unmarshal(raw, &plan) != nil || plan.Token != c.Param("token") || !validRestorePlan(plan) {
			c.JSON(503, gin.H{"error": "Trạng thái khôi phục không hợp lệ"})
			return
		}
		if db.saveRestore(c.Request.Context(), plan) != nil {
			c.Header("X-Restore-History", "unconfirmed")
		}
		c.Data(200, "application/json", raw)
	})
}

// Durable ownership of staged restores. Alert rules, notification queues and
// monitoring history are not stored or processed here.
const restoreStateLimit = 8 << 20

var activeRestores *restoreStore

type restoreRecord struct {
	Source         string                `json:"source"`
	Plan           backupops.RestorePlan `json:"plan"`
	StartRequested bool                  `json:"startRequested"`
}
type restoreState struct {
	Version  int                      `json:"version"`
	Restores map[string]restoreRecord `json:"restores"`
}
type restoreStore struct {
	mu    sync.Mutex
	path  string
	lock  *os.File
	state restoreState
}

func openRestoreStore(path string) (*restoreStore, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err = backupops.SafeParents(path); err != nil {
		return nil, err
	}
	// Require an existing private installation directory, not arbitrary mkdirs.
	info, err := os.Stat(filepath.Dir(path))
	if err != nil || !info.IsDir() {
		return nil, errors.New("state directory unavailable")
	}
	lock, err := lockRestoreState(path + ".lock")
	if err != nil {
		return nil, err
	}
	p := &restoreStore{path: path, lock: lock, state: restoreState{Version: 1, Restores: map[string]restoreRecord{}}}
	failed := true
	defer func() {
		if failed {
			lock.Close()
		}
	}()
	info, err = os.Lstat(path)
	if os.IsNotExist(err) {
		// Import only restore ownership from the former shared JSON file. Keep
		// that file untouched for recovery; never import or replay alerts.
		legacyPath := filepath.Join(filepath.Dir(path), "operations_state.json")
		if legacyPath != path {
			if _, e := os.Lstat(legacyPath); e == nil {
				legacyLock, e := lockRestoreState(legacyPath + ".lock")
				if e != nil {
					return nil, e
				}
				p.state, e = readRestoreState(legacyPath)
				legacyLock.Close()
				if e != nil {
					return nil, e
				}
			} else if !os.IsNotExist(e) {
				return nil, e
			}
		}
		err = p.persist(p.state)
	} else if err == nil {
		p.state, err = readRestoreState(path)
	}
	if err != nil {
		return nil, fmt.Errorf("cannot load restore state; existing file preserved: %w", err)
	}
	failed = false
	return p, nil
}
func (s restoreState) validate() error {
	bad := errors.New("invalid restore state")
	if s.Version != 1 || s.Restores == nil || len(s.Restores) > 1000 {
		return bad
	}
	for token, r := range s.Restores {
		if token != r.Plan.Token || !validRestorePlan(r.Plan) || r.Source == "" {
			return bad
		}
	}
	return nil
}
func (p *restoreStore) persist(s restoreState) error {
	if err := backupops.SafeParents(p.path); err != nil {
		return err
	}
	if info, err := os.Lstat(p.path); err == nil && !info.Mode().IsRegular() {
		return errors.New("unsafe state target")
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if len(raw) > restoreStateLimit {
		return errors.New("restore state full")
	}
	f, err := os.CreateTemp(filepath.Dir(p.path), ".restore-state-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(raw)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(name, p.path)
}
func (p *restoreStore) update(ctx context.Context, edit func(*restoreState) error) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	// Copy-on-write: failed writes never mutate the last confirmed in-memory state.
	raw, _ := json.Marshal(p.state)
	var next restoreState
	if err := json.Unmarshal(raw, &next); err != nil {
		return err
	}
	if err := edit(&next); err != nil {
		return err
	}
	if err := next.validate(); err != nil {
		return err
	}
	if err := p.persist(next); err != nil {
		return err
	}
	p.state = next
	return nil
}
func (p *restoreStore) snapshot() restoreState {
	p.mu.Lock()
	defer p.mu.Unlock()
	raw, _ := json.Marshal(p.state)
	var s restoreState
	_ = json.Unmarshal(raw, &s)
	return s
}
func restoreSource() string {
	cfg := backupSystemSettings()
	identity := runtimeConfig.HostIdentity
	if identity == "" {
		identity = runtimeConfig.SSH.Host
	}
	raw, _ := json.Marshal([]string{identity, cfg.BackupRoot, cfg.ScriptsDir, cfg.LogsDir, cfg.DriveRemote, cfg.DriveFolder})
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func validRestoreToken(token string) bool {
	return len(token) == 32 && strings.Trim(token, "0123456789abcdef") == ""
}
func validRestorePlan(p backupops.RestorePlan) bool {
	if p.Parent != "" || p.Name != "" {
		dir, err := backupops.RestoreDestination(p.Parent, p.Name)
		if err != nil || dir != p.Directory {
			return false
		}
	}
	return validRestoreToken(p.Token) && validSnapshotPath(p.Path) && !p.CreatedAt.IsZero() && (p.Status == "preview" || p.Status == "queued" || p.Status == "running" || p.Status == "ready" || p.Status == "failed")
}
func (p *restoreStore) saveRestore(ctx context.Context, plan backupops.RestorePlan) error {
	if !validRestorePlan(plan) {
		return errors.New("invalid restore plan")
	}
	source := restoreSource()
	return p.update(ctx, func(s *restoreState) error {
		old, exists := s.Restores[plan.Token]
		if exists {
			if old.Source != source || old.Plan.Path != plan.Path || old.Plan.Directory != plan.Directory || old.Plan.Parent != plan.Parent || old.Plan.Name != plan.Name {
				return errors.New("restore source changed")
			}
			if old.Plan.Status == "ready" || old.Plan.Status == "failed" || old.Plan.Status == "running" && (plan.Status == "preview" || plan.Status == "queued") || old.Plan.Status == "queued" && plan.Status == "preview" {
				return nil
			}
		} else {
			for token, r := range s.Restores {
				age := time.Since(r.Plan.CreatedAt)
				if r.Plan.Status == "preview" && age > 24*time.Hour || (r.Plan.Status == "ready" || r.Plan.Status == "failed") && age > 30*24*time.Hour {
					delete(s.Restores, token)
				}
			}
			if len(s.Restores) >= 1000 {
				return errors.New("restore state full")
			}
		}
		s.Restores[plan.Token] = restoreRecord{Source: source, Plan: plan, StartRequested: old.StartRequested}
		return nil
	})
}
func (p *restoreStore) claimRestore(ctx context.Context, token string) error {
	source := restoreSource()
	return p.update(ctx, func(s *restoreState) error {
		r, ok := s.Restores[token]
		if !ok || r.Source != source || r.Plan.Status != "preview" || r.StartRequested || time.Since(r.Plan.CreatedAt) > 10*time.Minute {
			return errors.New("restore unavailable")
		}
		r.StartRequested = true
		s.Restores[token] = r
		return nil
	})
}
func (p *restoreStore) knowsRestore(token string) bool {
	r, ok := p.snapshot().Restores[token]
	return ok && r.Source == restoreSource()
}
func initializeRestoreState() error {
	path := strings.TrimSpace(os.Getenv("RESTORE_STATE_FILE"))
	if path == "" {
		path = "restore_state.json"
	}
	p, err := openRestoreStore(path)
	if err != nil {
		return err
	}
	activeRestores = p
	return nil
}

func readRestoreState(path string) (restoreState, error) {
	var state restoreState
	if err := backupops.SafeParents(path); err != nil {
		return state, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return state, err
	}
	if !info.Mode().IsRegular() || info.Size() > restoreStateLimit {
		return state, errors.New("unsafe or oversized restore state")
	}
	file, err := os.Open(path)
	if err != nil {
		return state, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, restoreStateLimit+1))
	if err != nil || len(raw) > restoreStateLimit {
		return state, errors.New("cannot read restore state")
	}
	if err = json.Unmarshal(raw, &state); err != nil {
		return state, err
	}
	return state, state.validate()
}

// ===== SERVER, STATUS & SYSTEM ROUTE HANDLERS =====

func registerServerRoutes(auth *gin.RouterGroup) {
	// API 1: Lấy thông số hệ thống máy chủ (CPU, RAM, Disk)
	auth.GET("/server-status", func(c *gin.Context) {
		output, err := readServerStatus()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể lấy thông số máy chủ", "detail": err.Error()})
			return
		}

		c.Data(http.StatusOK, "application/json", output)
	})

	// API 2: Kiểm tra các file Backup trên Google Drive và Dung lượng
	auth.GET("/backup-status", func(c *gin.Context) {
		output, err := readBackupStatus()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể đọc trạng thái sao lưu"})
			return
		}

		c.Data(http.StatusOK, "application/json", output)
	})

	// API 4: Giám sát Uptime các website (động theo thư mục /www/wwwroot)
	auth.GET("/websites-status", func(c *gin.Context) {
		output, err := cachedGet("websites-status", 15*time.Second, func() ([]byte, error) {
			return collectWebsiteStatuses(executeSSHCommandRaw)
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể kiểm tra website", "detail": err.Error()})
			return
		}

		c.Data(http.StatusOK, "application/json", output)
	})

	// API 5: Đọc Log hệ thống Real-time
	auth.GET("/logs", func(c *gin.Context) {
		logType := c.Query("type")
		cmd := ""

		switch logType {
		case "backup":
			// Kết hợp: Log cron + Chi tiết file backup trên Google Drive
			cmd = `
echo "══════════════════════════════════════════════════════"
echo "  LỊCH SỬ CHẠY SCRIPT (Cron Execution Log)"
echo "══════════════════════════════════════════════════════"
tail -n 15 /root/backup-site.log /root/backup-database.log /root/auto_backup.log /root/cleanup-panel-backups.log /root/check_integrity.log /www/server/cron/*.log 2>/dev/null
echo ""
echo "══════════════════════════════════════════════════════"
echo "  CHI TIẾT BẢN SAO LƯU TRÊN GOOGLE DRIVE"
echo "══════════════════════════════════════════════════════"
for DAY in $(/usr/bin/rclone lsf --dirs-only gdrive:Backup --config /root/.config/rclone/rclone.conf --timeout 10s --contimeout 5s 2>/dev/null | sort | tail -n 3); do
  DAYNAME=$(echo "$DAY" | tr -d '/')
  echo ""
  echo "📅 [$DAYNAME]"
  echo "  📁 site/"
  /usr/bin/rclone lsf -R --format "ps" gdrive:Backup/$DAYNAME/site/ --config /root/.config/rclone/rclone.conf --timeout 10s --contimeout 5s 2>/dev/null | awk -F';' '{printf "     %-50s %s\n", $1, $2" bytes"}'
  echo "  📁 database/"
  /usr/bin/rclone lsf -R --format "ps" gdrive:Backup/$DAYNAME/database/ --config /root/.config/rclone/rclone.conf --timeout 10s --contimeout 5s 2>/dev/null | awk -F';' '{printf "     %-50s %s\n", $1, $2" bytes"}'
done
`
		case "system":
			cmd = "tail -n 50 /var/log/messages"
		case "secure":
			cmd = "tail -n 50 /var/log/secure"
		default:
			cmd = "tail -n 50 /var/log/messages"
		}
		if logType == "backup" {
			cmd = strings.Replace(cmd, "tail -n 15 /root/backup-site.log /root/backup-database.log /root/auto_backup.log /root/cleanup-panel-backups.log /root/check_integrity.log", "tail -n 15 "+managedLogArguments(), 1)
		}

		key := "logs-" + logType
		ttl := 10 * time.Second
		if logType == "backup" {
			ttl = 30 * time.Second
		}
		output, err := cachedGet(key, ttl, func() ([]byte, error) {
			out, e := executeSSHCommand(cmd)
			return []byte(out), e
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể đọc log", "detail": err.Error()})
			return
		}

		c.String(http.StatusOK, string(output))
	})

	// API 7: Đọc cấu hình hệ thống (lưu trên máy ảo)
	auth.GET("/config", func(c *gin.Context) {
		cmds := `
CONFIG=/root/backup-monitor/config.json
if [ -f "$CONFIG" ]; then
  cat "$CONFIG"
else
  echo '{"retention_days":7,"notify_enabled":false,"notify_token":"","notify_chat_id":"","email_recipient":""}'
fi
`
		output, err := cachedGet("config", 10*time.Second, func() ([]byte, error) {
			out, e := executeSSHCommand(cmds)
			return []byte(out), e
		})
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể đọc cấu hình", "detail": err.Error()})
			return
		}
		c.Data(http.StatusOK, "application/json", output)
	})

	// API 8: Ghi cấu hình hệ thống (validate JSON rồi base64 truyền qua SSH)
	auth.PUT("/config", func(c *gin.Context) {
		payload, err := c.GetRawData()
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Body không hợp lệ"})
			return
		}
		var cfg map[string]interface{}
		if err := json.Unmarshal(payload, &cfg); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "JSON không hợp lệ"})
			return
		}
		// Chỉ chấp nhận các trường đã biết
		allowed := map[string]bool{
			"retention_days":  true,
			"notify_enabled":  true,
			"notify_token":    true,
			"notify_chat_id":  true,
			"email_recipient": true,
		}
		clean := map[string]interface{}{}
		for k, v := range cfg {
			if allowed[k] {
				clean[k] = v
			}
		}
		normalized, _ := json.Marshal(clean)
		b64 := base64.StdEncoding.EncodeToString(normalized)

		cmds := fmt.Sprintf(`mkdir -p /root/backup-monitor && echo %s | base64 -d > /root/backup-monitor/config.json && echo OK`, b64)
		output, err := executeSSHCommand(cmds)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể ghi cấu hình", "detail": err.Error()})
			return
		}
		invalidate("config")
		c.JSON(http.StatusOK, gin.H{"status": "success", "message": strings.TrimSpace(output), "config": clean})
	})

	auth.POST("/refresh", func(c *gin.Context) {
		refreshReadCaches()
		c.JSON(http.StatusOK, gin.H{"status": "success", "message": "Cache cleared"})
	})

	auth.POST("/clear-log", func(c *gin.Context) {
		var req struct {
			Target  string   `json:"target"`
			Entries []string `json:"entries"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Lỗi tham số"})
			return
		}
		if req.Target != "drive" && req.Target != "server" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Target không hợp lệ"})
			return
		}
		for _, entry := range req.Entries {
			if !activityLogKeyPattern.MatchString(entry) || (req.Target == "drive" && strings.Count(entry, "|") != 1) {
				c.JSON(http.StatusBadRequest, gin.H{"error": "Nhật ký được chọn không hợp lệ"})
				return
			}
		}
		if req.Target == "drive" {
			out, err := executeSSHCommandRaw(driveActivityClearCommand(req.Entries))
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể xóa nhật ký Drive; nếu cron đang chạy, hãy chờ tác vụ kết thúc", "detail": err.Error()})
				return
			}
			invalidate("backup-status")
			if len(req.Entries) > 0 && strings.Contains(out, "LOG_FILES_CHANGED=0\n") {
				c.JSON(http.StatusConflict, gin.H{"error": "Không tìm thấy nhật ký đã chọn. Hãy tải lại danh sách và thử lại."})
				return
			}
			c.JSON(http.StatusOK, gin.H{"message": "Đã xóa nhật ký upload Google Drive trên máy chủ; các tệp backup được giữ nguyên"})
			return
		}
		out, err := executeSSHCommandRaw(serverActivityClearCommand(req.Entries))
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể xóa nhật ký máy chủ", "detail": err.Error()})
			return
		}
		invalidate("backup-status", "server-status")
		if len(req.Entries) > 0 && strings.Contains(out, "LOG_FILES_CHANGED=0\n") {
			c.JSON(http.StatusConflict, gin.H{"error": "Không tìm thấy nhật ký đã chọn trên máy chủ. Hãy tải lại danh sách và thử lại."})
			return
		}
		c.JSON(http.StatusOK, gin.H{"message": "Đã xóa nhật ký trên máy chủ"})
	})
}

// Metrics are read alongside the shared local inventory in readServerStatus.
func serverStatusReadCommand() string {
	return `DISK=$(df -h / | awk 'NR==2 {printf "{\"total\":\"%s\",\"used\":\"%s\",\"free\":\"%s\",\"usage\":\"%s\"}", $2, $3, $4, $5}')
RAM=$(free -m | awk 'NR==2 {printf "{\"total\":\"%sMB\",\"used\":\"%sMB\",\"free\":\"%sMB\",\"usage\":\"%.1f%%\"}", $2, $3, $4, $3*100/$2}')
CPU=$(vmstat 1 2 | tail -1 | awk '{printf "%.1f%%",100-$15}')
UPTIME=$(uptime -p | sed 's/up //')
SERVER_TIME=$(date -Iseconds)
WEBSITES=$(find /www/wwwroot -mindepth 1 -maxdepth 1 -type d 2>/dev/null | grep -v '/default$' | wc -l)
DATABASES=$(find /www/server/data -mindepth 1 -maxdepth 1 -type d 2>/dev/null | grep -vE '/(mysql|performance_schema|sys|phpmyadmin)$' | wc -l)
printf '__METRICS__\n'
printf '{"server_time":"%s","disk":%s,"ram":%s,"cpu":"%s","uptime":"%s","websites":%s,"databases":%s}\n' "$SERVER_TIME" "$DISK" "$RAM" "$CPU" "$UPTIME" "$WEBSITES" "$DATABASES"
printf '__CRONTAB__\n'
crontab -l 2>/dev/null || true
`
}

func parseServerStatus(output string) ([]byte, error) {
	metrics := map[string]interface{}{}
	var inventory, cron strings.Builder
	hasInventory := false
	section := ""
	for _, line := range strings.Split(output, "\n") {
		switch line {
		case "__LOCAL_INVENTORY__":
			hasInventory = true
			section = line
			continue
		case "__METRICS__", "__CRONTAB__":
			section = line
			continue
		}
		switch section {
		case "__METRICS__":
			if strings.TrimSpace(line) != "" {
				if err := json.Unmarshal([]byte(line), &metrics); err != nil {
					return nil, fmt.Errorf("Thông số máy chủ không hợp lệ")
				}
			}
		case "__LOCAL_INVENTORY__":
			inventory.WriteString(line + "\n")
		case "__CRONTAB__":
			cron.WriteString(line + "\n")
		}
	}
	if len(metrics) == 0 {
		return nil, fmt.Errorf("Thiếu thông số máy chủ")
	}
	serverTime, _ := metrics["server_time"].(string)
	if hasInventory {
		metrics["local_backup"] = localBackupSummary(inventory.String(), serverTime)
	} else {
		metrics["local_backup_error"] = "Không thể đọc bản sao lưu trên máy chủ"
	}
	counts := map[string]int{"all": 0, "local": 0, "drive": 0}
	times := map[string][]string{}
	for _, job := range parseManagedCronJobs("__CRONTAB__\n" + cron.String()) {
		if !job.Enabled {
			continue
		}
		kind := "other"
		if strings.HasPrefix(job.ID, "backup-") {
			kind = "local"
		} else if job.ID == "drive-sync" {
			kind = "drive"
		}
		counts["all"]++
		counts[kind]++
		fields := strings.Fields(job.Schedule)
		if len(fields) < 2 {
			continue
		}
		minute, me := strconv.Atoi(fields[0])
		hour, he := strconv.Atoi(fields[1])
		if me == nil && he == nil && minute >= 0 && minute < 60 && hour >= 0 && hour < 24 {
			clock := fmt.Sprintf("%02d:%02d", hour, minute)
			times["all"] = append(times["all"], clock)
			times[kind] = append(times[kind], clock)
		}
	}
	for _, kind := range []string{"all", "local", "drive"} {
		sort.Strings(times[kind])
		prefix := ""
		if kind != "all" {
			prefix = kind + "_"
		}
		metrics[prefix+"crons"] = counts[kind]
		metrics[prefix+"cron_times"] = strings.Join(times[kind], ", ")
	}
	return json.Marshal(metrics)
}

func localBackupSummary(inventory, serverTime string) map[string]interface{} {
	files := parseLocalInventory(inventory)
	now, timeErr := time.Parse(time.RFC3339, serverTime)
	firstDay, lastDay := now.AddDate(0, 0, -13).Format(time.DateOnly), now.Format(time.DateOnly)
	days := map[string]*backupHistoryDay{}
	var total int64
	for _, file := range files {
		total += file.Size
		day, _, _ := strings.Cut(file.Date, " ")
		if timeErr != nil || day < firstDay || day > lastDay {
			continue
		}
		if days[day] == nil {
			days[day] = &backupHistoryDay{Date: day}
		}
		days[day].Bytes += file.Size
		days[day].Files++
	}
	latestName, latestDate := "", ""
	if len(files) > 0 {
		latestName, latestDate = files[0].Name, strings.SplitN(files[0].Date, " ", 2)[0]
	}
	localBackup := map[string]interface{}{"count": len(files), "bytes": total, "size": backupSizeLabel(total), "latest_name": latestName, "latest_date": latestDate}
	if timeErr == nil {
		history := make([]backupHistoryDay, 0, len(days))
		for _, day := range days {
			history = append(history, *day)
		}
		sort.Slice(history, func(i, j int) bool { return history[i].Date < history[j].Date })
		localBackup["history"] = history
	}
	return localBackup
}

func backupSizeLabel(bytes int64) string {
	if bytes < 1024 {
		return fmt.Sprintf("%d B", bytes)
	}
	value := float64(bytes)
	for _, unit := range []string{"KB", "MB", "GB", "TB", "PB"} {
		value /= 1024
		if value < 1024 || unit == "PB" {
			return fmt.Sprintf("%.2f %s", value, unit)
		}
	}
	return "0 B"
}

// ===== SNAPSHOT MODELS & HELPERS =====

type snapshotFile struct {
	Path     string `json:"path"`
	Date     string `json:"date"`
	Category string `json:"category"`
	Name     string `json:"name"`
	Size     int64  `json:"size"`
	Modified string `json:"modified"`
}

type localSnapshot struct {
	LocalPath string `json:"localPath"`
	Date      string `json:"date"`
	Category  string `json:"category"`
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	Modified  string `json:"modified"`
}

var backupDateInName = regexp.MustCompile(`(?:^|[^0-9])(20[0-9]{2})-?([0-9]{2})-?([0-9]{2})(?:[_-]([0-9]{2})([0-9]{2})([0-9]{2}))?`)

func backupTimestamp(name, modified string) string {
	match := backupDateInName.FindStringSubmatch(name)
	if match == nil {
		return modified
	}
	day := match[1] + "-" + match[2] + "-" + match[3]
	if _, err := time.Parse("2006-01-02", day); err != nil {
		return modified
	}
	if match[4] != "" {
		stamp := day + " " + match[4] + ":" + match[5] + ":" + match[6]
		if _, err := time.Parse("2006-01-02 15:04:05", stamp); err == nil {
			return stamp
		}
	}
	return day + " 00:00:00"
}

func parseLocalSnapshots(output string) []localSnapshot {
	snapshots := parseLocalInventory(output)
	if len(snapshots) > 500 {
		return snapshots[:500]
	}
	return snapshots
}

func parseLocalInventory(output string) []localSnapshot {
	snapshots := make([]localSnapshot, 0)
	seen := map[string]bool{}
	roots := configuredLocalBackupRoots()
	for _, line := range strings.Split(output, "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), "|", 3)
		if len(parts) != 3 {
			continue
		}
		size, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil || size < 0 || seen[parts[2]] {
			continue
		}
		prefix := ""
		for _, root := range roots {
			if strings.HasPrefix(parts[2], root+"/") {
				prefix = root + "/"
				break
			}
		}
		if prefix == "" {
			continue
		}
		path := strings.TrimPrefix(parts[2], prefix)
		segments := strings.Split(path, "/")
		if len(segments) < 2 {
			continue
		}
		category := segments[0]
		if len(segments) >= 3 {
			if _, err := time.Parse("2006-01-02", segments[0]); err == nil {
				category = segments[1]
			}
		}
		if category != "site" && category != "database" && category != "panel" {
			continue
		}
		name := segments[len(segments)-1]
		modified := parts[0]
		if len(modified) > 19 {
			modified = modified[:19]
		}
		snapshots = append(snapshots, localSnapshot{
			LocalPath: parts[2],
			Date:      backupTimestamp(name, modified), Category: category,
			Name: name, Size: size, Modified: modified,
		})
		seen[parts[2]] = true
	}
	sort.Slice(snapshots, func(i, j int) bool {
		if snapshots[i].Date != snapshots[j].Date {
			return snapshots[i].Date > snapshots[j].Date
		}
		return snapshots[i].Modified > snapshots[j].Modified
	})
	return snapshots
}

func parseSnapshotList(output string) []snapshotFile {
	snapshots := []snapshotFile{}
	for _, line := range strings.Split(output, "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), ";", 3)
		if len(parts) != 3 || parts[0] == "" || parts[1] == "-1" {
			continue
		}
		size, err := strconv.ParseInt(parts[1], 10, 64)
		if err != nil {
			continue
		}
		path := strings.TrimSuffix(parts[0], "/")
		segments := strings.Split(path, "/")
		category := "root"
		if len(segments) >= 2 {
			category = segments[1]
		}
		snapshots = append(snapshots, snapshotFile{
			Path: path, Date: segments[0], Category: category,
			Name: segments[len(segments)-1], Size: size, Modified: parts[2],
		})
	}
	return snapshots
}

type downloadTicket struct {
	path      string
	expiresAt time.Time
}

var downloadTickets = struct {
	sync.Mutex
	items map[string]downloadTicket
}{items: make(map[string]downloadTicket)}

func issueDownloadTicket(path string) (string, error) {
	raw := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, raw); err != nil {
		return "", err
	}
	ticket := base64.RawURLEncoding.EncodeToString(raw)
	downloadTickets.Lock()
	downloadTickets.items[ticket] = downloadTicket{path: path, expiresAt: time.Now().Add(2 * time.Minute)}
	downloadTickets.Unlock()
	return ticket, nil
}

func consumeDownloadTicket(ticket string) (string, bool) {
	downloadTickets.Lock()
	defer downloadTickets.Unlock()
	entry, ok := downloadTickets.items[ticket]
	if !ok || time.Now().After(entry.expiresAt) {
		delete(downloadTickets.items, ticket)
		return "", false
	}
	delete(downloadTickets.items, ticket)
	return entry.path, true
}

var snapshotPathRe = regexp.MustCompile(`^[A-Za-z0-9._\-\/]+$`)

func validSnapshotPath(p string) bool {
	if p == "" || strings.Contains(p, "..") || !snapshotPathRe.MatchString(p) {
		return false
	}
	for _, part := range strings.Split(p, "/") {
		if part == "" || part == "." {
			return false
		}
	}
	return true
}

// ===== SNAPSHOT ROUTE HANDLERS =====

func registerSnapshotRoutes(auth *gin.RouterGroup) {
	auth.POST("/download-ticket", func(c *gin.Context) {
		var req struct {
			Path string `json:"path"`
		}
		if err := c.ShouldBindJSON(&req); err != nil || !validSnapshotPath(req.Path) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Đường dẫn không hợp lệ"})
			return
		}
		ticket, err := issueDownloadTicket(req.Path)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể tạo link tải"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"ticket": ticket})
	})

	auth.GET("/recovery-snapshots", func(c *gin.Context) {
		inventory, err := readDriveInventory()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể liệt kê bản sao lưu", "detail": err.Error()})
			return
		}

		if !inventory.HasListing {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể liệt kê bản sao lưu", "detail": inventory.driveError()})
			return
		}
		c.JSON(http.StatusOK, parseSnapshotList(inventory.Listing))
	})

	auth.GET("/local-snapshots", func(c *gin.Context) {
		output, err := readLocalInventory()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể liệt kê bản sao lưu cục bộ", "detail": err.Error()})
			return
		}

		c.JSON(http.StatusOK, parseLocalSnapshots(string(output)))
	})

	auth.GET("/download-snapshot", func(c *gin.Context) {
		rel := c.Query("path")
		if ticketPath, exists := c.Get("download_ticket_path"); exists {
			rel = ticketPath.(string)
		}
		cmd, err := snapshotDownloadCommand(rel)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Đường dẫn không hợp lệ"})
			return
		}
		if runtimeConfig.HostAgent != nil {
			c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", filepath.Base(rel)))
			c.Header("Content-Type", "application/octet-stream")
			rc := http.NewResponseController(c.Writer)
			fw := &flushWriter{w: c.Writer, f: func() { _ = rc.Flush() }}
			if err := runtimeConfig.HostAgent.Stream(c.Request.Context(), cmd, fw); err != nil {
				if !c.Writer.Written() {
					c.JSON(http.StatusBadGateway, gin.H{"error": "Không tải được bản sao lưu"})
				} else {
					c.Abort()
				}
			}
			return
		}

		session, err := newSSHSession()
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Không thể mở phiên SSH", "detail": err.Error()})
			return
		}
		defer session.Close()

		name := filepath.Base(rel)
		c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s\"", name))
		c.Header("Content-Type", "application/octet-stream")

		rc := http.NewResponseController(c.Writer)
		fw := &flushWriter{w: c.Writer, f: func() { _ = rc.Flush() }}
		session.Stdout = fw

		var errBuf bytes.Buffer
		session.Stderr = &errBuf

		if err := rc.Flush(); err != nil {
			return
		}
		if err := session.Run(cmd); err != nil {
			fmt.Println("Download error:", err, errBuf.String())
		}
	})
}

// ===== SSH CONNECTION POOL =====
// Tái sử dụng kết nối SSH thay vì tạo mới mỗi lần gọi (tiết kiệm ~300ms/request)
var (
	sshClient   *ssh.Client
	sshClientMu sync.Mutex
)

func buildSSHClientConfig(settings SSHSettings) (*ssh.ClientConfig, error) {
	authMethods := make([]ssh.AuthMethod, 0, 2)
	if settings.PrivateKeyPath != "" {
		keyData, err := os.ReadFile(settings.PrivateKeyPath)
		if err != nil {
			return nil, fmt.Errorf("read SSH private key: %w", err)
		}
		var signer ssh.Signer
		if settings.PrivateKeyPassphrase != "" {
			signer, err = ssh.ParsePrivateKeyWithPassphrase(keyData, []byte(settings.PrivateKeyPassphrase))
		} else {
			signer, err = ssh.ParsePrivateKey(keyData)
		}
		if err != nil {
			return nil, fmt.Errorf("parse SSH private key: %w", err)
		}
		authMethods = append(authMethods, ssh.PublicKeys(signer))
	}
	if settings.Password != "" {
		authMethods = append(authMethods, ssh.Password(settings.Password))
	}
	if len(authMethods) == 0 {
		return nil, fmt.Errorf("no SSH authentication method configured")
	}

	var hostKeyCallback ssh.HostKeyCallback
	if settings.InsecureHostKey {
		hostKeyCallback = ssh.InsecureIgnoreHostKey() //nolint:gosec // explicit opt-in for temporary development only
	} else {
		if settings.KnownHostsPath == "" {
			return nil, fmt.Errorf("known_hosts path is required")
		}
		callback, err := knownhosts.New(settings.KnownHostsPath)
		if err != nil {
			return nil, fmt.Errorf("load known_hosts: %w", err)
		}
		hostKeyCallback = callback
	}

	return &ssh.ClientConfig{
		User:            settings.User,
		Auth:            authMethods,
		HostKeyCallback: hostKeyCallback,
		Timeout:         15 * time.Second,
	}, nil
}

func dialSSH() (*ssh.Client, error) {
	config, err := buildSSHClientConfig(runtimeConfig.SSH)
	if err != nil {
		return nil, err
	}
	client, err := ssh.Dial("tcp", runtimeConfig.SSH.Host, config)
	if err != nil {
		return nil, err
	}

	// Gửi keepalive mỗi 30 giây để server không đóng kết nối idle
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			_, _, err := client.SendRequest("keepalive@openssh.com", true, nil)
			if err != nil {
				// Kết nối đã chết, xóa khỏi pool
				sshClientMu.Lock()
				if sshClient == client {
					sshClient = nil
				}
				sshClientMu.Unlock()
				client.Close()
				return
			}
		}
	}()

	return client, nil
}

func getSSHClient() (*ssh.Client, error) {
	sshClientMu.Lock()
	defer sshClientMu.Unlock()

	// The actual NewSession below checks the pooled connection and retries once.
	if sshClient != nil {
		return sshClient, nil
	}

	client, err := dialSSH()
	if err != nil {
		return nil, err
	}
	sshClient = client
	return sshClient, nil
}

func newSSHSession() (*ssh.Session, error) {
	client, err := getSSHClient()
	if err != nil {
		return nil, err
	}
	session, err := client.NewSession()
	if err == nil {
		return session, nil
	}

	// A stale pooled connection gets one reconnect without a separate probe session.
	sshClientMu.Lock()
	if sshClient == client {
		sshClient = nil
	}
	sshClientMu.Unlock()
	_ = client.Close()
	client, err = getSSHClient()
	if err != nil {
		return nil, err
	}
	return client.NewSession()
}

// Hàm thực thi lệnh SSH trên máy ảo (tái sử dụng kết nối pool, timeout 60s)
func executeSSHCommand(command string) (string, error) {
	return executeSSHCommandRaw(configuredBackupCommand(command))
}

func executeSSHCommandRaw(command string) (string, error) {
	return executeSSHCommandInputRaw(command, nil)
}

func executeSSHCommandInputRaw(command string, input []byte) (string, error) {
	return executeSSHCommandInputRawTimeout(command, input, 60*time.Second)
}

func executeSSHCommandInputRawTimeout(command string, input []byte, timeout time.Duration) (string, error) {
	command = configuredRuntimeCommand(command)
	if runtimeConfig.HostAgent != nil {
		ctx, cancel := context.WithTimeout(context.Background(), timeout+5*time.Second)
		defer cancel()
		return runtimeConfig.HostAgent.Execute(ctx, command, input)
	}
	session, err := newSSHSession()
	if err != nil {
		return "", err
	}
	defer session.Close()

	var stdoutBuf bytes.Buffer
	var stderrBuf bytes.Buffer
	session.Stdout = &stdoutBuf
	session.Stderr = &stderrBuf
	if input != nil {
		session.Stdin = bytes.NewReader(input)
	}

	type runResult struct {
		err error
	}
	done := make(chan runResult, 1)
	go func() {
		done <- runResult{err: session.Run(command)}
	}()

	select {
	case <-time.After(timeout):
		session.Signal(ssh.SIGKILL)
		return "", fmt.Errorf("SSH command timed out after %s; reload state before retrying", timeout)
	case res := <-done:
		if res.err != nil {
			return "", fmt.Errorf("Lệnh lỗi: %v, Chi tiết: %s", res.err, stderrBuf.String())
		}
		return stdoutBuf.String(), nil
	}
}

// ===== HELPERS =====
type flushWriter struct {
	w io.Writer
	f func()
}

func (fw *flushWriter) Write(p []byte) (int, error) {
	n, err := fw.w.Write(p)
	if err == nil && fw.f != nil {
		fw.f()
	}
	return n, err
}

const websiteProbeTimeoutSeconds = 15
const websiteDiscoveryCommand = `set -o pipefail
find /www/wwwroot -mindepth 1 -maxdepth 1 -type d -printf '%f\n' 2>/dev/null | LC_ALL=C sort`

var websiteHostname = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9.-]*[A-Za-z0-9])?$`)

type websiteProbeResult struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Code   string `json:"code"`
	Time   string `json:"time"`
	Reason string `json:"reason,omitempty"`
}

func parseWebsiteProbe(name, output string) websiteProbeResult {
	result := websiteProbeResult{Name: name, Status: "UNKNOWN", Code: "000", Time: "", Reason: "probe_failed"}
	fields := strings.Split(strings.TrimSpace(output), ",")
	if len(fields) != 3 || len(fields[0]) != 3 {
		return result
	}
	code, codeErr := strconv.Atoi(fields[0])
	seconds, timeErr := strconv.ParseFloat(fields[1], 64)
	exit, exitErr := strconv.Atoi(fields[2])
	if codeErr != nil || timeErr != nil || exitErr != nil || code < 0 || code > 599 || seconds < 0 || math.IsNaN(seconds) || math.IsInf(seconds, 0) {
		return result
	}
	result.Code, result.Time = fields[0], fmt.Sprintf("%.0fms", seconds*1000)
	switch {
	case exit == 28:
		// A deadline is not proof of an outage, even if no headers arrived.
		result.Status, result.Reason = "TIMEOUT", "timeout"
	case exit == 6 || exit == 7:
		result.Status, result.Reason = "OFFLINE", "connection_failed"
	case exit != 0 || code == 0:
		return result
	case (code >= 200 && code < 400) || code == 401 || code == 403:
		// Availability is independent of response speed; keep the timing as detail.
		result.Status, result.Reason = "ONLINE", ""
	default:
		result.Status, result.Reason = "ERROR", "http_error"
	}
	return result
}

func websiteProbeCommand(name string) string {
	// Callers validate the hostname; never interpolate arbitrary folder names.
	return fmt.Sprintf(`result=$(curl --noproxy '*' -o /dev/null -s -w '%%{http_code},%%{time_total}' --resolve '%s:80:127.0.0.1' --connect-timeout 3 --max-time %d 'http://%s/' 2>/dev/null)
exit_code=$?
printf '%%s,%%s\n' "$result" "$exit_code"`, name, websiteProbeTimeoutSeconds, name)
}

func collectWebsiteStatuses(run func(string) (string, error)) ([]byte, error) {
	out, err := run(websiteDiscoveryCommand)
	if err != nil {
		return nil, err
	}
	names, seen := []string{}, map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		name := strings.TrimSpace(line)
		if name == "default" || !websiteHostname.MatchString(name) || seen[name] {
			continue
		}
		seen[name] = true
		names = append(names, name)
		if len(names) == 10 {
			break
		}
	}
	results := make([]websiteProbeResult, len(names))
	// At most two active requests: keep latency bounded without flooding PHP.
	jobs := make(chan int)
	var workers sync.WaitGroup
	for range 2 {
		workers.Go(func() {
			for i := range jobs {
				response, err := run(websiteProbeCommand(names[i]))
				if err != nil {
					response = ""
				}
				results[i] = parseWebsiteProbe(names[i], response)
			}
		})
	}
	for i := range names {
		jobs <- i
	}
	close(jobs)
	workers.Wait()
	return json.Marshal(results)
}
