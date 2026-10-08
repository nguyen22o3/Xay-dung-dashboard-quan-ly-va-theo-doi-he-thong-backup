#!/usr/bin/env bash
# backup-monitor daily layout v1
# backup-monitor daily versions v1
# backup-monitor clean archive names v1
# COMMON
SOURCE_ROOT=/www/wwwroot
BACKUP_ROOT=/www/backup
SITES=()
BACKUP_MONITOR_ORIGIN=${BACKUP_MONITOR_ORIGIN:-manual}
[[ "$BACKUP_MONITOR_ORIGIN" == cron || "$BACKUP_MONITOR_ORIGIN" == manual ]] || exit 2
for tool in tar gzip od tr ln rm; do require "$tool"; done
safe_path "$SOURCE_ROOT" && safe_path "$BACKUP_ROOT" || exit 1
exec 7>/run/backup-monitor-layout.lock
flock -s 7
exec 9>/run/backup-monitor-site.lock
flock -n 9 || exit 75
PENDING=''
trap '[[ -z "$PENDING" ]] || rm -f -- "$PENDING"' EXIT
backup_one() {
    local site="$1" source_dir stamp day dir nonce final start duration_ms
    source_dir="$SOURCE_ROOT/$site"
    safe_path "$source_dir" && [[ -d "$source_dir" ]] || return 1
    start=$(date +%s%N)
    stamp=$(date +%Y%m%d_%H%M%S)
    day="${stamp:0:4}-${stamp:4:2}-${stamp:6:2}"
    dir="$BACKUP_ROOT/$day/site/$site"
    safe_path "$dir" || return 1
    install -d -m 700 -- "$dir" || return 1
    PENDING=$(mktemp "$dir/.pending-site-XXXXXXXX") || return 1
    tar -C "${source_dir%/*}" -czf "$PENDING" -- "${source_dir##*/}" || return 1
    gzip -t "$PENDING" || return 1
    nonce=$(od -An -N8 -tx1 /dev/urandom | tr -d ' \n') || return 1
    [[ "$nonce" =~ ^[0-9a-f]{16}$ ]] || return 1
    final="$dir/web_${site}_${stamp}_${BACKUP_MONITOR_ORIGIN}_${nonce}_site.tar.gz"
    ln -- "$PENDING" "$final" || return 1
    rm -f -- "$PENDING"; PENDING=''
    final=$("$MANAGER" versions finalize site "$final") || return 1
    duration_ms=$(( ($(date +%s%N)-start)/1000000 ))
    printf 'CREATED %s DURATION %d.%03d\n' "$final" "$((duration_ms/1000))" "$((duration_ms%1000))"
}
failures=0
for site in "${SITES[@]}"; do
    if ! backup_one "$site"; then printf 'FAILED: sao lưu website %s\n' "$site" >&2; failures=1; fi
    [[ -z "$PENDING" ]] || { rm -f -- "$PENDING"; PENDING=''; }
done
if (( failures == 0 )); then "$MANAGER" layout prune site --delete || failures=1; fi
exit "$failures"
