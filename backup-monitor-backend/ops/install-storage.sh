#!/usr/bin/env bash
# Standalone updater for an SSH-connected Windows dashboard. No Docker required.
set +x
set -euo pipefail
umask 077
data_dir=
confirmed=0
while (($#)); do
  case "$1" in
    --data-dir) [[ $# -ge 2 ]] || exit 1; data_dir=$2; shift 2 ;;
    --yes) confirmed=1; shift ;;
    *) echo 'Usage: install-storage.sh [--data-dir /opt/backup-monitor] [--yes]' >&2; exit 1 ;;
  esac
done
[[ $EUID == 0 && $(uname -s) == Linux && $(uname -m) == x86_64 ]] || { echo 'Run as root on x86_64 AlmaLinux.' >&2; exit 1; }
if systemctl is-active --quiet backup-monitor-api; then echo 'Stop backup-monitor-api before migrating its configuration.' >&2; exit 1; fi
script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
candidate=$script_dir/backup-manager-linux-v26
checksum=$script_dir/backup-manager-linux-v26.sha256
binary=/usr/local/libexec/backup-monitor/backup-manager
for target in "$candidate" "$checksum" "$binary"; do
  [[ -f $target && ! -L $target && $(stat -c %u "$target") == 0 ]] || { echo "Missing/unsafe file: $target" >&2; exit 1; }
  [[ $((8#$(stat -c %a "$target") & 8#022)) == 0 ]] || { echo 'Package files must not be writable by other users.' >&2; exit 1; }
  check=$(dirname -- "$target")
  while [[ $check != / ]]; do
    [[ -d $check && ! -L $check && $(stat -c %u "$check") == 0 ]] || { echo 'Unsafe package/helper parent.' >&2; exit 1; }
    # /tmp itself is allowed for uploading, never for the installed data folder.
    [[ $check == /tmp || $((8#$(stat -c %a "$check") & 8#022)) == 0 ]] || { echo 'Unsafe writable package parent.' >&2; exit 1; }
    check=$(dirname -- "$check")
  done
done
cd -- "$script_dir"
[[ $(wc -l < "$checksum") == 1 ]] || { echo 'Invalid checksum file.' >&2; exit 1; }
read -r expected_hash expected_name < "$checksum"
[[ $expected_hash =~ ^[a-f0-9]{64}$ && $expected_name == backup-manager-linux-v26 ]] || { echo 'Invalid checksum entry.' >&2; exit 1; }
printf '%s  %s\n' "$expected_hash" "$candidate" | sha256sum -c -
chmod 700 "$candidate"
[[ $("$candidate" version) == backup-manager-go-v26 ]] || { echo 'Unexpected helper version.' >&2; exit 1; }
for tool in flock crontab jq; do command -v "$tool" >/dev/null || { echo "Missing $tool; install it before continuing." >&2; exit 1; }; done
[[ ! -L /run/backup-monitor-storage-install.lock ]] || { echo 'Unsafe installer lock.' >&2; exit 1; }
exec 9>/run/backup-monitor-storage-install.lock
flock -n 9 || { echo 'Another installation is running.' >&2; exit 1; }
installation_json=$("$candidate" installation)
current_dir=$(printf '%s' "$installation_json" | jq -r '.dataDir')
if [[ -z $data_dir ]]; then
  if [[ -n $current_dir ]]; then
    data_dir=$current_dir
  else
    read -r -p 'Application folder [/opt/backup-monitor]: ' data_dir </dev/tty
    data_dir=${data_dir:-/opt/backup-monitor}
  fi
fi
"$candidate" storage-install --directory "$data_dir"
if [[ $confirmed != 1 ]]; then
  echo 'Stop the Windows backend first. Cron will be paused briefly; old files will be retained.'
  read -r -p 'Install helper and switch application storage? Type yes: ' answer </dev/tty
  [[ $answer == yes ]] || { echo 'Cancelled; no installation changed.'; exit 0; }
fi
cron_was_active=0
agent_was_active=0
finish() {
  [[ $agent_was_active != 1 ]] || systemctl start backup-monitor-agent
  [[ $cron_was_active != 1 ]] || systemctl start crond
}
trap finish EXIT
if systemctl is-active --quiet crond; then cron_was_active=1; systemctl stop crond; fi
if systemctl is-active --quiet backup-monitor-agent; then agent_was_active=1; systemctl stop backup-monitor-agent; fi
previous=$binary.before-v26-$(date +%Y%m%d-%H%M%S)-$$.bak
cp -p -- "$binary" "$previous"
helper_stage=$(mktemp /usr/local/libexec/backup-monitor/.backup-manager-v26.XXXXXXXX)
install -o root -g root -m 700 "$candidate" "$helper_stage"
mv -f -- "$helper_stage" "$binary"
command -v restorecon >/dev/null && restorecon -F "$binary"
if ! "$binary" storage-install --directory "$data_dir" --apply; then
  # Failed migrations have not published their locator; keep the old helper.
  rollback_stage=$(mktemp /usr/local/libexec/backup-monitor/.backup-manager-rollback.XXXXXXXX)
  install -o root -g root -m 700 "$previous" "$rollback_stage"
  mv -f -- "$rollback_stage" "$binary"
  command -v restorecon >/dev/null && restorecon -F "$binary"
  echo 'Migration failed; inspect the message above. Original data remains.' >&2
  exit 1
fi
echo "Previous helper: $previous"
echo "Application folder: $data_dir"
echo 'Restart the Windows backend, then refresh the dashboard. Do not delete /root wholesale.'
