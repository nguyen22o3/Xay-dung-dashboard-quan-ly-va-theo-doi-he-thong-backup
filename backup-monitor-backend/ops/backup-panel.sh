#!/usr/bin/env bash
# backup-monitor clean archive names v1
# Standalone aaPanel backup. Rotates tagged same-day versions; no cron edits.
set -Eeuo pipefail
# backup-monitor daily versions v1
BACKUP_MONITOR_ORIGIN=${BACKUP_MONITOR_ORIGIN:-manual}
if [[ "$BACKUP_MONITOR_ORIGIN" != cron && "$BACKUP_MONITOR_ORIGIN" != manual ]]; then
    printf 'Invalid backup origin; use cron or manual.\n' >&2
    exit 2
fi
umask 077
export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin

MODE=${1:---run}
if (( $# > 1 )) || [[ "$MODE" != --run && "$MODE" != --dry-run && "$MODE" != --help ]]; then
    printf 'Usage: backup-panel.sh [--run|--dry-run|--help]\n' >&2
    exit 2
fi
if [[ "$MODE" == --help ]]; then
    printf '%s\n' \
        'Back up aaPanel data/config/vhost to BACKUP_ROOT/YYYY-MM-DD/panel/.' \
        'Default: --run. --dry-run validates dependencies/paths without writing.' \
        'Optional root-only overrides: PANEL_ROOT, BACKUP_ROOT, LOG_FILE, LOCK_ROOT.' \
        'Otherwise backupRoot/logsDir come from the installed Go configuration helper.' \
        'Keeps 1 cron + 2 manual versions/day after validation; untagged archives stay.' \
        'Does not install cron, upload to Drive, disable aaPanel, or prune old dates.'
    exit 0
fi
if [[ "$(uname -s)" != Linux ]] || (( EUID != 0 )); then
    printf 'This script requires root on Linux.\n' >&2
    exit 1
fi

PANEL_ROOT=${PANEL_ROOT:-/www/server/panel}
LOCK_ROOT=${LOCK_ROOT:-/run}
MANAGER=/usr/local/libexec/backup-monitor/backup-manager
MAX_RAW_DB_BYTES=104857600
WORK_DIR=''
PENDING_FILE=''
PUBLISHED_FILE=''
LOG_READY=0
START_NS=$(date +%s%N)

fail() { printf 'ERROR: %s\n' "$*" >&2; exit 1; }

safe_path() {
    local value="$1" component
    [[ "$value" =~ ^/[A-Za-z0-9_./-]+$ && "$value" != / && "$value" != */ && "$value" != *//* ]] || fail 'Unsafe absolute path.'
    [[ "/${value#/}/" != *'/../'* && "/${value#/}/" != *'/./'* ]] || fail 'Path traversal is not permitted.'
    component="$value"
    while [[ "$component" != / ]]; do
        [[ ! -L "$component" ]] || fail 'Symbolic links in source/destination paths are not permitted.'
        component=${component%/*}
        [[ -n "$component" ]] || component=/
    done
}

private_parent() {
    local parent="$1" permissions
    safe_path "$parent"
    while [[ ! -e "$parent" ]]; do
        parent=${parent%/*}
        [[ -n "$parent" ]] || parent=/
    done
    [[ -d "$parent" && "$(stat -c %u -- "$parent")" == 0 ]] || fail 'Destination parent must be a root-owned directory.'
    permissions=$(stat -c %a -- "$parent")
    (( (8#$permissions & 0022) == 0 )) || fail 'Destination parent must not be group/world writable.'
}

for tool in jq tar sqlite3 zip unzip flock stat find mktemp install cmp head touch od tr ln readlink date tee chmod rm; do
    command -v "$tool" >/dev/null 2>&1 || fail "Missing dependency: $tool"
done

# Match the existing Go controller's locks: configuration changes and panel
# cleanup cannot race publication. Dry runs do not create lock/log/directories.
safe_path "$LOCK_ROOT"
private_parent "$LOCK_ROOT"
if [[ "$MODE" == --run ]]; then
    [[ -d "$LOCK_ROOT" ]] || fail 'Lock directory does not exist.'
    for lock in backup-monitor-layout.lock backup-monitor-panel-retention.lock; do
        safe_path "$LOCK_ROOT/$lock"
        [[ ! -e "$LOCK_ROOT/$lock" || -f "$LOCK_ROOT/$lock" ]] || fail 'Lock path is not a regular file.'
    done
    exec 7>"$LOCK_ROOT/backup-monitor-layout.lock"
    flock -s 7
    exec 9>"$LOCK_ROOT/backup-monitor-panel-retention.lock"
    flock -n 9 || { printf 'Panel backup/cleanup already running.\n' >&2; exit 75; }
fi

if [[ -z "${BACKUP_ROOT:-}" || -z "${LOG_FILE:-}" ]]; then
    safe_path "$MANAGER"
    [[ -f "$MANAGER" && -x "$MANAGER" && "$(stat -c %u -- "$MANAGER")" == 0 ]] || fail 'Go configuration helper is unavailable.'
    configuration=$(printf '%s\n' '{"operation":"get"}' | "$MANAGER" config)
    configured_root=$(jq -er '.config.backupRoot | select(type == "string" and length > 0)' <<< "$configuration") || fail 'Cannot read backupRoot from Go configuration.'
    configured_logs=$(jq -er '.config.logsDir | select(type == "string" and length > 0)' <<< "$configuration") || fail 'Cannot read logsDir from Go configuration.'
    BACKUP_ROOT=${BACKUP_ROOT:-$configured_root}
    LOG_FILE=${LOG_FILE:-$configured_logs/backup-panel.log}
    unset configuration configured_root configured_logs
fi

safe_path "$PANEL_ROOT"
safe_path "$BACKUP_ROOT"
safe_path "$LOG_FILE"
private_parent "$BACKUP_ROOT"
private_parent "${LOG_FILE%/*}"
[[ ! -e "$LOG_FILE" || -f "$LOG_FILE" ]] || fail 'Log destination is not a regular file.'
[[ ! -e "$LOG_FILE" || "$(stat -c %u -- "$LOG_FILE")" == 0 ]] || fail 'Existing log must be root owned.'
[[ "$BACKUP_ROOT/" != "$PANEL_ROOT/"* && "$PANEL_ROOT/" != "$BACKUP_ROOT/"* ]] || fail 'Backup and panel directories must not contain one another.'
[[ "$LOG_FILE" != "$PANEL_ROOT/"* && "$LOG_FILE" != "$BACKUP_ROOT/"* ]] || fail 'Keep the log outside panel source and backup storage.'
for name in data config vhost; do
    safe_path "$PANEL_ROOT/$name"
    [[ -d "$PANEL_ROOT/$name" && -r "$PANEL_ROOT/$name" ]] || fail "Missing aaPanel directory: $name"
done
for name in default.db system.db; do
    safe_path "$PANEL_ROOT/data/$name"
    [[ -f "$PANEL_ROOT/data/$name" ]] || fail "Missing aaPanel database: $name"
done

STAMP=$(date +%Y%m%d_%H%M%S)
DAY="${STAMP:0:4}-${STAMP:4:2}-${STAMP:6:2}"
DAY_DIR="$BACKUP_ROOT/$DAY/panel"
safe_path "$DAY_DIR"
private_parent "$DAY_DIR"
if [[ "$MODE" == --dry-run ]]; then
    printf 'DRY_RUN: source=%s/{data,config,vhost}; destination=%s; log=%s\n' "$PANEL_ROOT" "$DAY_DIR" "$LOG_FILE"
    printf 'Each run creates a distinct ZIP; SQLite snapshots will be verified during --run. No files written or backups deleted.\n'
    exit 0
fi

install -d -m 700 -- "${LOG_FILE%/*}" "$DAY_DIR"
: >> "$LOG_FILE"
chmod 600 -- "$LOG_FILE"
LOG_READY=1
log() {
    # The dashboard wrapper already captures stdout into this log. Direct runs
    # keep tee so they still write a log without duplicating dashboard entries.
    if [[ "${BACKUP_PANEL_CAPTURED_LOG:-0}" == 1 ]]; then
        printf '[%s] %s\n' "$(date '+%F %T')" "$*"
    else
        printf '[%s] %s\n' "$(date '+%F %T')" "$*" | tee -a "$LOG_FILE"
    fi
}

cleanup() {
    local result=$? end_ns duration resolved
    trap - EXIT
    # Delete only this invocation's exclusive pending file/private staging.
    if [[ -n "$PENDING_FILE" && "$PENDING_FILE" == "$DAY_DIR/.pending-panel-"* && -f "$PENDING_FILE" && ! -L "$PENDING_FILE" ]]; then
        rm -f -- "$PENDING_FILE"
    fi
    if [[ -n "$WORK_DIR" && "${WORK_DIR%/*}" == /root && "${WORK_DIR##*/}" == .backup-panel.* && -d "$WORK_DIR" && ! -L "$WORK_DIR" ]]; then
        resolved=$(readlink -e -- "$WORK_DIR") || resolved=''
        [[ "$resolved" != "$WORK_DIR" ]] || rm -rf -- "$WORK_DIR"
    fi
    end_ns=$(date +%s%N)
    duration=$(( (end_ns - START_NS) / 1000000 ))
    if (( LOG_READY )); then
        if (( result == 0 )); then
            log "SUCCESS: archive=$PUBLISHED_FILE duration_ms=$duration"
        else
            log "FAILED: exit=$result duration_ms=$duration; check preceding validation/rotation messages"
        fi
    fi
    exit "$result"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM HUP
log 'START: aaPanel configuration backup'

# Staging is outside the watched backup tree to avoid false deletion alarms.
WORK_DIR=$(mktemp -d /root/.backup-panel.XXXXXXXX)
install -d -m 700 -- "$WORK_DIR/payload" "$WORK_DIR/snapshots"
for name in data config vhost; do
    install -d -m 700 -- "$WORK_DIR/payload/$name"
    # Never copy changing SQLite main/WAL files as though they were snapshots.
    database_exclusions=()
    if [[ "$name" == data ]]; then
        database_exclusions=(--exclude='*.db' --exclude='*.db-wal' --exclude='*.db-shm' --exclude='*.db-journal')
    fi
    tar --exclude='*.sock' --exclude='wp_package_checksums' --exclude='wp_packages' \
        --exclude='maillog' --exclude='mail' --exclude='hids_data' \
        "${database_exclusions[@]}" \
        -C "$PANEL_ROOT" -cf - -- "$name" | tar -C "$WORK_DIR/payload" -xf -
done

find "$PANEL_ROOT/data" \
    \( -type d \( -name wp_package_checksums -o -name wp_packages -o -name maillog -o -name mail -o -name hids_data \) -prune \) \
    -o \( -type f -name '*.db' -print0 \) > "$WORK_DIR/databases.list"
database_count=0
while IFS= read -r -d '' source; do
    safe_path "$source"
    relative=${source#"$PANEL_ROOT/data/"}
    snapshot="$WORK_DIR/snapshots/$relative"
    safe_path "$snapshot"
    install -d -m 700 -- "${snapshot%/*}"
    if ! head -c 16 -- "$source" | cmp -s - <(printf 'SQLite format 3\0'); then
        [[ "$relative" != default.db && "$relative" != system.db ]] || fail 'Core aaPanel database is not SQLite.'
        # Preserve non-SQLite .db files without attempting to interpret them.
        target="$WORK_DIR/payload/data/$relative"
        safe_path "$target"
        install -d -m 700 -- "${target%/*}"
        install -m 600 -- "$source" "$target"
        continue
    fi
    sqlite3 -batch -bail -readonly "$source" '.timeout 10000' ".backup '$snapshot'" 2>"$WORK_DIR/sqlite-error" || fail 'SQLite snapshot failed; private error details are not exposed.'
    check=$(sqlite3 -batch -bail -readonly "$snapshot" 'PRAGMA quick_check;' 2>"$WORK_DIR/sqlite-error") || fail 'SQLite verification failed.'
    [[ "$check" == ok ]] || fail 'SQLite snapshot did not pass quick_check.'
    dump="$WORK_DIR/payload/data/db_backups/${relative%.db}.sql"
    [[ "$relative" != system.db ]] || dump="$WORK_DIR/payload/data/system.sql"
    safe_path "$dump"
    install -d -m 700 -- "${dump%/*}"
    # Dumps are produced from the consistent snapshot, never the live DB.
    sqlite3 -batch -bail -readonly "$snapshot" '.dump' > "$dump" 2>"$WORK_DIR/sqlite-error" || fail 'SQLite SQL export failed.'
    if (( $(stat -c %s -- "$snapshot") <= MAX_RAW_DB_BYTES )); then
        target="$WORK_DIR/payload/data/$relative"
        safe_path "$target"
        install -d -m 700 -- "${target%/*}"
        install -m 600 -- "$snapshot" "$target"
        touch -r "$source" -- "$target"
    else
        log "INFO: large SQLite database exported as SQL only: $relative"
    fi
    database_count=$((database_count + 1))
done < "$WORK_DIR/databases.list"
(( database_count >= 2 )) || fail 'Core SQLite snapshots are missing.'

(cd "$WORK_DIR/payload" && zip -q -r -y "$WORK_DIR/archive.zip" data config vhost)
unzip -tqq "$WORK_DIR/archive.zip" >/dev/null 2>&1 || fail 'ZIP validation failed.'
nonce=$(od -An -N8 -tx1 /dev/urandom | tr -d ' \n')
[[ "$nonce" =~ ^[0-9a-f]{16}$ ]] || fail 'Cannot generate unique archive name.'
PUBLISHED_FILE="$DAY_DIR/${DAY}_${STAMP:9:6}_${BACKUP_MONITOR_ORIGIN}_${nonce}.zip"
safe_path "$PUBLISHED_FILE"
PENDING_FILE=$(mktemp "$DAY_DIR/.pending-panel-XXXXXXXX")
install -m 600 -- "$WORK_DIR/archive.zip" "$PENDING_FILE"
cmp -s -- "$WORK_DIR/archive.zip" "$PENDING_FILE" || fail 'Staged archive copy verification failed.'
# Exclusive hard link is atomic within the destination filesystem: never mv -f.
ln -- "$PENDING_FILE" "$PUBLISHED_FILE" || fail 'Archive destination already exists; nothing overwritten.'
PUBLISHED_FILE=$("$MANAGER" versions finalize panel "$PUBLISHED_FILE")
printf 'SOURCE=aaPanel data/config/vhost\nBACKUP=%s\n' "$PUBLISHED_FILE"
# Existing cleanup/Drive cron handles retention and upload separately.
