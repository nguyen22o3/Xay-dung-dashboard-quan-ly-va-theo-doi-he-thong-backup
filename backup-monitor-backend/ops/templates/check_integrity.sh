#!/usr/bin/env bash
# backup-monitor daily layout v1
# COMMON
REMOTE=gdrive:Backup
RCLONE_CONFIG=/root/.config/rclone/rclone.conf
require rclone
START_DATE=$(date +%F); START_TIME=$(date +%T); START_SECONDS=$(date +%s); RUN_ID="$(date +%s%N)-$$"
finish() {
    code=$?; trap - EXIT
    status=success; [[ "$code" == 0 ]] || status=failed
    printf 'MONITOR_RUN|integrity-check|%s|%s|%s|%s|%s\n' "$START_DATE" "$START_TIME" "$status" "$(( $(date +%s)-START_SECONDS ))" "$RUN_ID"
    exit "$code"
}
trap finish EXIT
listing=$(rclone lsf "$REMOTE" --config "$RCLONE_CONFIG" --dirs-only) || { echo 'FAILED: Không đọc được Google Drive; chưa xác nhận số thư mục.' >&2; exit 1; }
count=0
while IFS= read -r entry; do
    day=${entry%/}
    [[ "$day" =~ ^20[0-9]{2}-[0-9]{2}-[0-9]{2}$ ]] || continue
    [[ "$(date -d "$day" +%F 2>/dev/null)" == "$day" ]] || continue
    count=$((count+1))
done <<< "$listing"
printf '[%s] Đã kiểm tra Drive: %d thư mục Backup\n' "$(date '+%F %T')" "$count"
