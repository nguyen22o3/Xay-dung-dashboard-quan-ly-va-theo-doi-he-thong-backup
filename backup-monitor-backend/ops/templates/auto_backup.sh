#!/bin/bash
# backup-monitor daily layout v1
# backup-monitor daily versions v1
# backup-monitor clean archive names v1
# COMMON
BACKUP_ROOT=/www/backup
REMOTE=gdrive:Backup
RCLONE_CONFIG=/root/.config/rclone/rclone.conf
HISTORY_LOG=/var/log/aapanel_backup.log
require rclone
safe_path "$BACKUP_ROOT" && safe_path "$HISTORY_LOG" || exit 1
[[ ! -e "$HISTORY_LOG" || -f "$HISTORY_LOG" ]] || exit 1
install -d -m 700 -- "${HISTORY_LOG%/*}"
exec 7>/run/backup-monitor-layout.lock
flock -s 7
exec 6>/run/backup-monitor-drive.lock
flock -n 6 || exit 75
START_DATE=$(date +%F); START_TIME=$(date +%T); START_SECONDS=$(date +%s)
STATUS="Successful"
TODAY=$(date +%F)
filters=(--filter '- .backup-monitor-versions.json' --filter '+ *.tar.gz' --filter '+ *.sql.gz' --filter '+ *.zip' --filter '- **')
finish() {
    code=$?; trap - EXIT
    [[ "$code" == 0 ]] || STATUS="Failed"
    DURATION=$(( $(date +%s)-START_SECONDS ))
    printf '%s|%s|%s|%s\n' "$START_DATE" "$START_TIME" "$DURATION" "$STATUS" >> "$HISTORY_LOG" || { STATUS="Failed"; code=1; }
    printf '==== HOÀN TẤT với trạng thái: %s (%s giây) ====\n' "$STATUS" "$DURATION"
    [[ "$STATUS" == "Successful" ]] || code=1
    exit "$code"
}
trap finish EXIT
trap 'exit 130' INT
trap 'exit 143' TERM HUP
"$MANAGER" layout organize --apply || exit 1
for day_dir in "$BACKUP_ROOT"/20??-??-??; do
    [[ -d "$day_dir" && ! -L "$day_dir" ]] || continue
    day=${day_dir##*/}
    [[ "$(date -d "$day" +%F 2>/dev/null)" == "$day" && "$day" < "$TODAY" || "$day" == "$TODAY" ]] || continue
    safe_path "$day_dir" || exit 1
    printf '[%s] Đang tải ngày sao lưu: %s\n' "$(date '+%F %T')" "$day"
    if ! rclone copy "$day_dir" "$REMOTE/$day" --config "$RCLONE_CONFIG" "${filters[@]}" --stats 5s --use-json-log --stats-log-level NOTICE; then
        STATUS="Failed"
    elif ! "$MANAGER" versions drive "$day" --apply; then
        STATUS="Failed"
    fi
done
[[ "$STATUS" == "Successful" ]] || exit 1
# Retain 14 valid backup dates. List failures abort; unknown paths/files are not
# purged. Only archive extensions are deleted inside explicitly validated days.
listing=$(rclone lsf "$REMOTE" --config "$RCLONE_CONFIG" --dirs-only) || exit 1
days=()
while IFS= read -r entry; do
    day=${entry%/}
    [[ "$day" =~ ^20[0-9]{2}-[0-9]{2}-[0-9]{2}$ ]] || continue
    [[ "$(date -d "$day" +%F 2>/dev/null)" == "$day" && "$day" < "$TODAY" || "$day" == "$TODAY" ]] || continue
    files=$(rclone lsf "$REMOTE/$day" --config "$RCLONE_CONFIG" --files-only -R "${filters[@]}") || exit 1
    [[ -z "$files" ]] || days+=("$day")
done <<< "$listing"
if (( ${#days[@]} > 14 )); then
    mapfile -t days < <(printf '%s\n' "${days[@]}" | sort -r)
    for day in "${days[@]:14}"; do
        printf '[%s] Dọn bản sao lưu quá 14 ngày: %s\n' "$(date '+%F %T')" "$day"
        rclone delete "$REMOTE/$day" --config "$RCLONE_CONFIG" "${filters[@]}" || exit 1
        rclone rmdirs "$REMOTE/$day" --config "$RCLONE_CONFIG" || exit 1
    done
fi
