#!/usr/bin/env bash
# Reviewed local-host installer. Never run with xtrace: administrator input and
# root-equivalent agent credentials are intentionally not command-line values.
set +x
set -euo pipefail
umask 077

release=1.1.6
dashboard_host=
data_dir=
while (($#)); do
  case "$1" in
    --host) [[ $# -ge 2 ]] || { echo 'Missing --host address' >&2; exit 1; }; dashboard_host=$2; shift 2 ;;
    --version) [[ $# -ge 2 ]] || exit 1; release=$2; shift 2 ;;
    --data-dir) [[ $# -ge 2 ]] || exit 1; data_dir=$2; shift 2 ;;
    *) echo 'Usage: install-local.sh [--host PRIVATE_IPV4] [--version X.Y.Z] [--data-dir /opt/backup-monitor]' >&2; exit 1 ;;
  esac
done
[[ $EUID == 0 && $(uname -s) == Linux ]] || { echo 'Run with sudo on AlmaLinux, not on Windows.' >&2; exit 1; }
[[ $(uname -m) == x86_64 ]] || { echo 'This release supports x86_64 only.' >&2; exit 1; }
[[ $release =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo 'Invalid release' >&2; exit 1; }
[[ -f /etc/almalinux-release ]] || { echo 'This installer targets AlmaLinux.' >&2; exit 1; }
[[ -d /www/server/panel ]] || { echo 'Install aaPanel on this host first.' >&2; exit 1; }
command -v docker >/dev/null || { echo 'Install Docker Engine and its Compose plugin first.' >&2; exit 1; }
docker info >/dev/null
docker compose version >/dev/null
command -v flock >/dev/null || dnf install -y util-linux
[[ ! -L /run/backup-monitor-local-install.lock ]] || { echo 'Unsafe installer lock path' >&2; exit 1; }
exec 9>/run/backup-monitor-local-install.lock
flock -n 9 || { echo 'Another local installation is running.' >&2; exit 1; }
if getent passwd | awk -F: '$3 == 10001 || $4 == 10001 { found=1 } END { exit !found }' || getent group 10001 >/dev/null; then
  echo 'Host UID/GID 10001 is already allocated. Stop: local users must not inherit the privileged agent capability.' >&2
  exit 1
fi
[[ -n $dashboard_host ]] || dashboard_host=$(ip -4 route get 1.1.1.1 | awk '{for(i=1;i<=NF;i++) if($i=="src") {print $(i+1); exit}}')
[[ $dashboard_host =~ ^([0-9]{1,3}\.){3}[0-9]{1,3}$ ]] || { echo 'Specify the private LAN address with --host.' >&2; exit 1; }
IFS=. read -r a b c d <<< "$dashboard_host"
for octet in "$a" "$b" "$c" "$d"; do
  [[ $octet == 0 || $octet != 0* ]] && ((10#$octet <= 255)) || { echo 'Invalid IPv4 address' >&2; exit 1; }
done
((a == 10 || (a == 172 && b >= 16 && b <= 31) || (a == 192 && b == 168))) || { echo 'Only private LAN addresses are supported. Use an HTTPS deployment for public access.' >&2; exit 1; }
ip -4 -o addr show | awk '{sub(/\/.*/, "", $4); print $4}' | grep -Fxq "$dashboard_host" || { echo '--host must be an address of this AlmaLinux host.' >&2; exit 1; }

if [[ -z $data_dir && -f /etc/backup-monitor/installation.json ]]; then
  command -v jq >/dev/null || dnf install -y jq
  data_dir=$(/usr/local/libexec/backup-monitor/backup-manager installation | jq -r '.dataDir')
fi
if [[ -z $data_dir ]]; then
  read -r -p 'Application folder [/opt/backup-monitor]: ' data_dir </dev/tty
  data_dir=${data_dir:-/opt/backup-monitor}
fi
[[ $data_dir =~ ^/[A-Za-z0-9_./-]+$ && $data_dir != *..* ]] || { echo 'Invalid application path.' >&2; exit 1; }
install_dir=$data_dir/deployment
agent_dir=/var/lib/backup-monitor-agent
binary=/usr/local/libexec/backup-monitor/backup-manager
for target in "$install_dir" "$install_dir/compose.yaml" "$install_dir/install-local.sh" "$agent_dir" "$agent_dir/run" "$agent_dir/secrets" "$agent_dir/secrets/agent-token" /usr/local/libexec/backup-monitor "$binary" /usr/bin/rclone /etc/systemd/system/backup-monitor-agent.service; do
  check=$target
  while [[ $check != / ]]; do
    [[ ! -L $check ]] || { echo "Refusing symlink: $check" >&2; exit 1; }
    check=$(dirname "$check")
  done
done
# Stop before installing anything if an unrelated application owns the port.
if ss -ltnH 'sport = :8090' | grep -q . && [[ ! -f $install_dir/compose.yaml ]]; then
  echo 'Port 8090 is already in use; keep the existing deployment unchanged.' >&2; exit 1
fi
tools_image=nguyen22o3/backup-monitor:host-tools-$release
docker pull "$tools_image"
docker pull "nguyen22o3/backup-monitor:backend-$release"
docker pull "nguyen22o3/backup-monitor:frontend-$release"

task_tmp=$(mktemp -d /tmp/backup-monitor-install.XXXXXXXX)
container_id=
cleanup() {
  if [[ -n $container_id ]]; then docker rm "$container_id" >/dev/null 2>&1 || true; fi
  # Only this explicit, mktemp-created installation directory is removed.
  if [[ $task_tmp == /tmp/backup-monitor-install.* && -d $task_tmp && ! -L $task_tmp ]]; then rm -r -- "$task_tmp"; fi
}
trap cleanup EXIT
container_id=$(docker create "$tools_image")
docker cp "$container_id:/backup-manager" "$task_tmp/backup-manager"
docker cp "$container_id:/rclone" "$task_tmp/rclone"
docker cp "$container_id:/bundle/." "$task_tmp/"
chmod 700 "$task_tmp/backup-manager" "$task_tmp/rclone"
[[ $("$task_tmp/backup-manager" version) == backup-manager-go-v26 ]] || { echo 'This installer requires a release containing helper v26; older published images cannot select application storage.' >&2; exit 1; }
"$task_tmp/rclone" version
# No MySQL installation or credentials are changed. aaPanel supplies the DB
# client; existing root .my.cnf is copied privately, never changed. For a new
# installation configure config/mysql.cnf (root, 0600) for SQL backups.
if ! rpm -q coreutils >/dev/null 2>&1 && ! rpm -q coreutils-single >/dev/null 2>&1; then dnf install -y coreutils; fi
dnf install -y bash findutils gawk procps-ng util-linux cronie tar gzip zip unzip jq sqlite
if command -v getenforce >/dev/null && [[ $(getenforce) != Disabled ]]; then
  dnf install -y selinux-policy-devel policycoreutils-python-utils container-selinux make
  make -C "$task_tmp" -f /usr/share/selinux/devel/Makefile backup_monitor_agent.pp
  semodule -i "$task_tmp/backup_monitor_agent.pp"
  semanage fcontext -a -t backup_monitor_agent_exec_t '/usr/local/libexec/backup-monitor/backup-manager' 2>/dev/null ||
    semanage fcontext -m -t backup_monitor_agent_exec_t '/usr/local/libexec/backup-monitor/backup-manager'
  for label_dir in run secrets; do
    semanage fcontext -a -t container_file_t "$agent_dir/$label_dir(/.*)?" 2>/dev/null ||
      semanage fcontext -m -t container_file_t "$agent_dir/$label_dir(/.*)?"
  done
fi
"$task_tmp/backup-manager" storage-install --directory "$data_dir"
read -r -p 'Install/update using this application folder? Type yes: ' storage_answer </dev/tty
[[ $storage_answer == yes ]] || { echo 'Cancelled before changing application storage.'; exit 0; }
cron_was_active=0
agent_was_active=0
restart_services() {
  [[ $agent_was_active != 1 ]] || systemctl start backup-monitor-agent
  [[ $cron_was_active != 1 ]] || systemctl start crond
}
trap 'restart_services; cleanup' EXIT
if systemctl is-active --quiet crond; then cron_was_active=1; systemctl stop crond; fi
if systemctl is-active --quiet backup-monitor-agent; then agent_was_active=1; systemctl stop backup-monitor-agent; fi
install -d -o root -g root -m 755 /usr/local/libexec/backup-monitor
previous_binary=$binary.before-v26-$(date +%Y%m%d-%H%M%S)-$$.bak
if [[ -f $binary ]]; then cp -p -- "$binary" "$previous_binary"; fi
binary_tmp=$(mktemp /usr/local/libexec/backup-monitor/.backup-manager-install.XXXXXXXX)
install -o root -g root -m 700 "$task_tmp/backup-manager" "$binary_tmp"
mv -f -- "$binary_tmp" "$binary"
if command -v restorecon >/dev/null; then restorecon -F "$binary"; fi
if ! "$binary" storage-install --directory "$data_dir" --apply; then
  if [[ -f $previous_binary ]]; then
    rollback_tmp=$(mktemp /usr/local/libexec/backup-monitor/.backup-manager-rollback.XXXXXXXX)
    install -o root -g root -m 700 "$previous_binary" "$rollback_tmp"
    mv -f -- "$rollback_tmp" "$binary"
    if command -v restorecon >/dev/null; then restorecon -F "$binary"; fi
  fi
  echo 'Application migration failed; original data remains.' >&2
  exit 1
fi
install -d -o root -g root -m 700 "$install_dir"
install -d -o root -g 10001 -m 750 "$agent_dir" "$agent_dir/run" "$agent_dir/secrets"
if [[ ! -e /usr/bin/rclone ]]; then install -o root -g root -m 755 "$task_tmp/rclone" /usr/bin/rclone; fi
install -d -m 755 /usr/local/share/licenses/backup-monitor
install -m 644 "$task_tmp/rclone-COPYING.txt" /usr/local/share/licenses/backup-monitor/rclone-COPYING.txt
# Preserve the credential on every update. Never bake shared secrets into images.
token_file=$agent_dir/secrets/agent-token
if [[ ! -e $token_file ]]; then
  token_tmp=$(mktemp "$agent_dir/secrets/.agent-token.XXXXXXXX")
  head -c 48 /dev/urandom | base64 | tr -d '\n' > "$token_tmp"
  chown root:10001 "$token_tmp"
  chmod 440 "$token_tmp"
  mv -n -- "$token_tmp" "$token_file"
fi
[[ -f $token_file && ! -L $token_file && $(stat -c %u "$token_file") == 0 && $(stat -c %g "$token_file") == 10001 && $(stat -c %a "$token_file") == 440 ]] || { echo 'Unsafe existing agent credential; installation stopped.' >&2; exit 1; }
if [[ -f $install_dir/compose.yaml ]]; then cp -p -- "$install_dir/compose.yaml" "$install_dir/compose.before-$(date +%Y%m%d-%H%M%S).yaml"; fi
sed -e "s/__DASHBOARD_HOST__/$dashboard_host/g" -e "s/__RELEASE__/$release/g" "$task_tmp/compose.local.yaml" > "$task_tmp/compose.yaml"
install -m 600 "$task_tmp/compose.yaml" "$install_dir/compose.yaml"
if [[ -f /etc/systemd/system/backup-monitor-agent.service ]]; then cp -p -- /etc/systemd/system/backup-monitor-agent.service "$install_dir/backup-monitor-agent.before-$(date +%Y%m%d-%H%M%S).service"; fi
install -m 644 "$task_tmp/backup-monitor-agent.service" /etc/systemd/system/backup-monitor-agent.service
if command -v getenforce >/dev/null && [[ $(getenforce) != Disabled ]]; then
  restorecon -F "$binary"
  restorecon -RF "$agent_dir/run" "$agent_dir/secrets"
fi
cd "$install_dir"
docker compose config -q
# Configure the first administrator before exposing the dashboard. Read from
# /dev/tty so this works even when the installer itself is piped into bash.
status=$(docker compose run --rm --no-deps -T backend account-status)
if [[ $status == empty ]]; then
  read -r -p 'Administrator username: ' admin_user </dev/tty
  read -r -s -p 'Administrator password (12-72 bytes): ' admin_password </dev/tty; printf '\n'
  read -r -s -p 'Repeat password: ' confirmation </dev/tty; printf '\n'
  [[ $admin_password == "$confirmation" ]] || { echo 'Passwords do not match; no dashboard was started.' >&2; exit 1; }
  printf '%s\n%s\n' "$admin_user" "$admin_password" | docker compose run --rm --no-deps -T backend setup-admin
  unset admin_password confirmation admin_user
elif [[ $status != exists ]]; then echo 'Cannot verify administrator state.' >&2; exit 1; fi
systemctl daemon-reload
systemctl enable --now crond
systemctl enable backup-monitor-agent
systemctl restart backup-monitor-agent
for attempt in {1..20}; do [[ -S $agent_dir/run/agent.sock ]] && break; sleep 1; done
[[ -S $agent_dir/run/agent.sock ]] || { echo 'Host agent did not start; check journalctl -u backup-monitor-agent.' >&2; exit 1; }
docker compose run --rm --no-deps -T backend check-host
docker compose up -d --wait --wait-timeout 180
# Keep a reviewed installer on the host for subsequent one-command updates.
# Replace atomically so a running copy continues reading its original inode.
if [[ -f $install_dir/install-local.sh ]]; then cp -p -- "$install_dir/install-local.sh" "$install_dir/install-local.before-$(date +%Y%m%d-%H%M%S).sh"; fi
installer_tmp=$(mktemp "$install_dir/.install-local.XXXXXXXX")
install -o root -g root -m 700 "$task_tmp/install-local.sh" "$installer_tmp"
mv -f -- "$installer_tmp" "$install_dir/install-local.sh"
echo "Dashboard: http://$dashboard_host:8090"
echo "Application folder: $data_dir"
echo "Next update: sudo bash $install_dir/install-local.sh --host $dashboard_host --version X.Y.Z --data-dir $data_dir"
echo 'No .env, SSH password, known_hosts or SSH tunnel is required for this local-host installation.'
echo 'Connect your own Google Drive in Cron. No backup schedules were created or backups executed.'
echo 'This address is for trusted LAN use only. Use an HTTPS reverse proxy before Internet access.'
echo 'If LAN access is blocked, inspect the firewall; this installer does not disable it or add public rules.'
