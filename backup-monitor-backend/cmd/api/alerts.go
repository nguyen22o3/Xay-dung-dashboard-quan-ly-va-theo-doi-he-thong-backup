package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/mail"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
)

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
