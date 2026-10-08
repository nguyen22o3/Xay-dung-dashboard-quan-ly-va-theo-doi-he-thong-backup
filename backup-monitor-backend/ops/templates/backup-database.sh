#!/usr/bin/env bash
# backup-monitor daily layout v1
# backup-monitor daily versions v1
# backup-monitor clean archive names v1
# COMMON
SOURCE_ROOT=/www/server/data
BACKUP_ROOT=/www/backup
DATABASES=()
# Credentials are deliberately not embedded. Configure a root-owned mode 600
# MySQL client option file with [client], user/password/socket as appropriate.
MYSQL_OPTIONS_FILE=${MYSQL_OPTIONS_FILE:-/root/.my.cnf}
BACKUP_MONITOR_ORIGIN=${BACKUP_MONITOR_ORIGIN:-manual}
[[ "$BACKUP_MONITOR_ORIGIN" == cron || "$BACKUP_MONITOR_ORIGIN" == manual ]] || exit 2
for tool in mysqldump gzip od tr ln rm; do require "$tool"; done
safe_path "$SOURCE_ROOT" && safe_path "$BACKUP_ROOT" && safe_path "$MYSQL_OPTIONS_FILE" || exit 1
[[ -f "$MYSQL_OPTIONS_FILE" && "$(stat -c %u "$MYSQL_OPTIONS_FILE")" == 0 && "$(stat -c %a "$MYSQL_OPTIONS_FILE")" == 600 ]] || { echo 'Cần cấu hình MySQL tại /root/.my.cnf (root, quyền 600); không ghi mật khẩu vào script.' >&2; exit 1; }
exec 7>/run/backup-monitor-layout.lock
flock -s 7
exec 9>/run/backup-monitor-database.lock
flock -n 9 || exit 75
PENDING=''
trap '[[ -z "$PENDING" ]] || rm -f -- "$PENDING"' EXIT
backup_one() {
    local database="$1" stamp day dir nonce final start duration_ms
    [[ -d "$SOURCE_ROOT/$database" && ! -L "$SOURCE_ROOT/$database" ]] || { printf 'Database source missing: %s\n' "$database" >&2; return 1; }
    start=$(date +%s%N)
    stamp=$(date +%Y%m%d_%H%M%S)
    day="${stamp:0:4}-${stamp:4:2}-${stamp:6:2}"
    dir="$BACKUP_ROOT/$day/database/mysql/crontab_backup/$database"
    safe_path "$dir" || return 1
    install -d -m 700 -- "$dir" || return 1
    PENDING=$(mktemp "$dir/.pending-database-XXXXXXXX") || return 1
    # Logical dump only. Never tar/copy the live MySQL data directory.
    mysqldump --defaults-extra-file="$MYSQL_OPTIONS_FILE" --single-transaction --routines --events --triggers --hex-blob --databases "$database" | gzip -c > "$PENDING" || return 1
    [[ -s "$PENDING" ]] && gzip -t "$PENDING" || return 1
    nonce=$(od -An -N8 -tx1 /dev/urandom | tr -d ' \n') || return 1
    [[ "$nonce" =~ ^[0-9a-f]{16}$ ]] || return 1
    final="$dir/db_${database}_${stamp}_${BACKUP_MONITOR_ORIGIN}_${nonce}_mysql_data.sql.gz"
    ln -- "$PENDING" "$final" || return 1
    rm -f -- "$PENDING"; PENDING=''
    final=$("$MANAGER" versions finalize database "$final") || return 1
    duration_ms=$(( ($(date +%s%N)-start)/1000000 ))
    printf 'CREATED %s DURATION %d.%03d\n' "$final" "$((duration_ms/1000))" "$((duration_ms%1000))"
}
failures=0
for database in "${DATABASES[@]}"; do
    if ! backup_one "$database"; then printf 'FAILED: sao lưu database %s\n' "$database" >&2; failures=1; fi
    [[ -z "$PENDING" ]] || { rm -f -- "$PENDING"; PENDING=''; }
done
if (( failures == 0 )); then "$MANAGER" layout prune database --delete || failures=1; fi
exit "$failures"
