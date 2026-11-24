#!/bin/bash
# Usage: sudo bash install-agent.sh ./ownpanel-agent ./ownpanel-enrollment.json [LEGACY_FILE_ROOT_IGNORED]
set -euo pipefail
[[ $(id -u) == 0 ]] || { echo 'Run as root'; exit 1; }
[[ $# -ge 2 ]] || { echo 'Usage: install-agent.sh BINARY ENROLLMENT_JSON [LEGACY_FILE_ROOT_IGNORED]'; exit 1; }
binary_path=$(realpath -- "$1")
enrollment_path=$(realpath -- "$2")
[[ -f "$binary_path" && -f "$enrollment_path" ]] || { echo 'Invalid arguments'; exit 1; }
command -v systemctl >/dev/null
command -v python3 >/dev/null || { echo 'Install python3 for firewall recovery'; exit 1; }
test -x /opt/ownpanel-nginx/sbin/nginx && test -r /opt/ownpanel-nginx/conf/mime.types || { echo 'Build private Nginx with build-agent-nginx.sh first'; exit 1; }
install -d -m 0755 /opt/ownpanel-agent /srv/ownpanel
install -d -m 0700 /var/lib/ownpanel-agent
if [[ -f /var/lib/ownpanel-agent/identity.json ]]; then
 echo 'Agent already enrolled. Re-enrollment requires an explicit identity rotation; installation stopped.'
 exit 1
fi
install -m 0755 "$binary_path" /opt/ownpanel-agent/ownpanel-agent.new
mv /opt/ownpanel-agent/ownpanel-agent.new /opt/ownpanel-agent/ownpanel-agent
if [[ "$enrollment_path" != /var/lib/ownpanel-agent/enrollment.json ]]; then
 install -m 0600 "$enrollment_path" /var/lib/ownpanel-agent/enrollment.json
else
 chmod 0600 /var/lib/ownpanel-agent/enrollment.json
fi
cat >/etc/systemd/system/ownpanel-agent.service <<'EOF'
[Unit]
Description=OwnPanel outbound management agent
After=network-online.target
Wants=network-online.target
[Service]
ExecStart=/opt/ownpanel-agent/ownpanel-agent -data /var/lib/ownpanel-agent -file-root / -nginx-prefix /var/lib/ownpanel-agent-nginx -nginx-binary /opt/ownpanel-nginx/sbin/nginx -nginx-mime-types /opt/ownpanel-nginx/conf/mime.types -nginx-service ownpanel-nginx.service -enrollment-file /var/lib/ownpanel-agent/enrollment.json
Restart=on-failure
RestartSec=5
[Install]
WantedBy=multi-user.target
EOF
cat >/etc/systemd/system/ownpanel-nginx.service <<'EOF'
[Unit]
Description=OwnPanel managed isolated Nginx
After=network.target
[Service]
Type=forking
PIDFile=/var/lib/ownpanel-agent-nginx/nginx.pid
ExecStartPre=/opt/ownpanel-nginx/sbin/nginx -t -p /var/lib/ownpanel-agent-nginx/ -c /var/lib/ownpanel-agent-nginx/nginx.conf
ExecStart=/opt/ownpanel-nginx/sbin/nginx -p /var/lib/ownpanel-agent-nginx/ -c /var/lib/ownpanel-agent-nginx/nginx.conf
ExecReload=/opt/ownpanel-nginx/sbin/nginx -p /var/lib/ownpanel-agent-nginx/ -c /var/lib/ownpanel-agent-nginx/nginx.conf -s reload
ExecStop=/opt/ownpanel-nginx/sbin/nginx -p /var/lib/ownpanel-agent-nginx/ -c /var/lib/ownpanel-agent-nginx/nginx.conf -s quit
TimeoutStopSec=10
[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload
systemctl enable --now ownpanel-agent
for attempt in {1..30}; do
 if [[ -f /var/lib/ownpanel-agent/identity.json ]]; then
  systemctl enable --now ownpanel-nginx
  echo 'Agent enrolled. Check connection status in the center.'
  exit 0
 fi
 sleep 1
done
echo 'Enrollment is not complete. Inspect: journalctl -u ownpanel-agent -n 50'
exit 1
