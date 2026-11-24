#!/bin/bash
set -euo pipefail

if [[ $# -lt 2 || $# -gt 3 || $(id -u) != 0 ]]; then
    echo 'Usage: sudo bash upgrade-center.sh RELEASE_DIR HTTPS_BASE_URL [CA_FILE]' >&2
    exit 2
fi
release=$(realpath -- "$1")
health="${2%/}/healthz"
[[ "$health" == https://* ]]
curl_options=(--fail --silent --max-time 3 --proto '=https' --noproxy '*')
if [[ $# == 3 ]]; then curl_options+=(--cacert "$3"); fi

install_dir=/opt/ownpanel-center
state=/var/lib/ownpanel-center
service=ownpanel-center.service
[[ -x "$release/ownpanel-server" && -s "$release/web/index.html" ]]
[[ -x "$install_dir/ownpanel-server" && -d "$state" ]]
exec 9>/run/lock/ownpanel-center-upgrade.lock
flock -n 9

ready() {
    local response
    for ((i=0;i<15;i++)); do
        if systemctl is-active --quiet "$service" &&
           response=$(curl "${curl_options[@]}" "$health") &&
           [[ "$response" == '{"ready":true}' ]]; then return 0; fi
        sleep 1
    done
    return 1
}

ready || { echo 'Existing center is not ready' >&2; exit 1; }
app_backup=$(mktemp -d /opt/.ownpanel-center-upgrade.XXXXXXXX)
state_backup=$(mktemp -d /var/lib/.ownpanel-center-upgrade.XXXXXXXX)
install -m 0755 "$release/ownpanel-server" "$app_backup/new-server"
cp -R -- "$release/web" "$app_backup/new-web"
chmod -R a+rX "$app_backup/new-web"
backup_tool="$(dirname -- "$(realpath -- "$0")")/center-backup.py"

systemctl stop "$service"
if ! python3 "$backup_tool" "$state" "$state_backup/snapshot" ||
   ! chown -R "$(stat -c '%u:%g' "$state")" "$state_backup/snapshot"; then
    systemctl start "$service"
    exit 1
fi

rollback() {
    echo "Upgrade failed, restoring previous center" >&2
    systemctl stop "$service"
    if [[ -e "$app_backup/server" ]]; then
        mv -Tf -- "$app_backup/server" "$install_dir/ownpanel-server"
    fi
    if [[ -d "$app_backup/web" ]]; then
        if [[ -d "$install_dir/web" ]]; then mv -T -- "$install_dir/web" "$app_backup/failed-web"; fi
        mv -T -- "$app_backup/web" "$install_dir/web"
    fi
    mv -T -- "$state" "$state_backup/failed-state"
    mv -T -- "$state_backup/snapshot" "$state"
    systemctl reset-failed "$service" || true
    systemctl start "$service"
    ready || { echo 'Restored service needs manual inspection' >&2; return 1; }
    echo "Previous center restored. Backup directories: $app_backup $state_backup" >&2
}

if mv -T -- "$install_dir/ownpanel-server" "$app_backup/server" &&
   mv -T -- "$install_dir/web" "$app_backup/web" &&
   mv -T -- "$app_backup/new-server" "$install_dir/ownpanel-server" &&
   mv -T -- "$app_backup/new-web" "$install_dir/web" &&
   systemctl start "$service" && ready; then
    echo "Upgrade ready. Previous center files: $app_backup. State snapshot: $state_backup"
else
    rollback
    exit 1
fi
