# Shared safety checks inserted into generated scripts (not executed alone).
set -Eeuo pipefail
umask 077
export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
MANAGER=/usr/local/libexec/backup-monitor/backup-manager
[[ "$(uname -s)" == Linux && "$EUID" == 0 ]] || { echo 'Yêu cầu root trên Linux.' >&2; exit 1; }
safe_path() {
    local p="$1"
    [[ "$p" =~ ^/[A-Za-z0-9_./-]+$ && "$p" != / && "$p" != *'/../'* && "$p" != *'/./'* ]] || return 1
    while [[ "$p" != / ]]; do
        [[ ! -L "$p" ]] || return 1
        p=${p%/*}; [[ -n "$p" ]] || p=/
    done
}
require() { command -v "$1" >/dev/null || { printf 'Thiếu công cụ: %s\n' "$1" >&2; exit 1; }; }
for tool in flock date find sort mktemp stat; do require "$tool"; done
[[ -f "$MANAGER" && -x "$MANAGER" && ! -L "$MANAGER" ]] || { echo 'Thiếu helper backup-manager.' >&2; exit 1; }
