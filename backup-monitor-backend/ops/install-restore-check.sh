#!/usr/bin/env bash
set -euo pipefail
umask 077

[[ $# == 0 && $EUID == 0 && -f /etc/almalinux-release ]] || {
  echo 'Run install-restore-check.sh as root on AlmaLinux.' >&2
  exit 1
}
script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
unit_dir=/etc/systemd/system
units=(backup-monitor-restore-check.service backup-monitor-restore-check.timer)
for unit in "${units[@]}"; do
  source_file=$script_dir/$unit
  [[ -f $source_file && ! -L $source_file && $(stat -c %u "$source_file") == 0 &&
    $((8#$(stat -c %a "$source_file") & 8#022)) == 0 && ! -L $unit_dir/$unit ]] || {
    echo "Missing or unsafe restore-check unit: $unit" >&2
    exit 1
  }
done
installation=$(/usr/local/libexec/backup-monitor/backup-manager installation)
command -v jq >/dev/null || dnf install -y jq
config_dir=$(jq -er '.configDir | strings | select(length > 0)' <<< "$installation")
[[ -d $config_dir && ! -L $config_dir && $(stat -c %u "$config_dir") == 0 &&
  $((8#$(stat -c %a "$config_dir") & 8#022)) == 0 ]] || {
  echo 'Restore-check requires a root-owned backup-monitor configuration directory.' >&2
  exit 1
}
config=$config_dir/restore-check-images.json
[[ ! -L $config ]] || { echo 'Unsafe restore-check image configuration.' >&2; exit 1; }
lock=$config_dir/.restore-check-install.lock
[[ ! -L $lock ]] || { echo 'Unsafe restore-check installation lock.' >&2; exit 1; }
exec 9>"$lock"
flock -n 9 || { echo 'Another restore-check installation is running.' >&2; exit 1; }
[[ -x /usr/bin/podman ]] || dnf install -y podman

pull_image() {
  local image=docker.io/library/$1:$2 digest
  /usr/bin/podman pull "$image" >&2 || return
  digest=$(/usr/bin/podman image inspect --format '{{.Digest}}' "$image") || return
  printf 'docker.io/library/%s@%s\n' "$1" "$digest"
}

if [[ -e $config ]]; then
  [[ -f $config && $(stat -c %u "$config") == 0 && $((8#$(stat -c %a "$config") & 8#077)) == 0 ]] || {
    echo 'Restore-check image configuration must be private and root-owned.' >&2
    exit 1
  }
  mysql=$(jq -er '.mysql | strings' "$config")
  archive=$(jq -er '.archive | strings' "$config")
else
  mysql=$(pull_image mariadb 10.11)
  archive=$(pull_image python 3.12-slim)
fi
[[ $mysql =~ ^docker\.io/library/mariadb@sha256:[a-f0-9]{64}$ &&
   $archive =~ ^docker\.io/library/python@sha256:[a-f0-9]{64}$ ]] || {
  echo 'Restore-check requires official MariaDB and Python images pinned by digest.' >&2
  exit 1
}
for image in "$mysql" "$archive"; do
  /usr/bin/podman image exists "$image" || /usr/bin/podman pull "$image"
  /usr/bin/podman image exists "$image"
done
if [[ ! -e $config ]]; then
  stage=$(mktemp "$config_dir/.restore-check-images.XXXXXXXX")
  trap 'rm -f -- "$stage"' EXIT
  jq -n --arg mysql "$mysql" --arg archive "$archive" '{mysql: $mysql, archive: $archive}' > "$stage"
  mv -n -- "$stage" "$config"
fi
for unit in "${units[@]}"; do
  install -o root -g root -m 644 "$script_dir/$unit" "$unit_dir/$unit"
done
systemctl daemon-reload
systemctl enable --now "${units[1]}"
systemctl is-active --quiet "${units[1]}"
systemctl list-timers "${units[1]}" --all --no-pager
