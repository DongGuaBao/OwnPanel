#!/bin/bash
set -euo pipefail

if [[ $# != 4 || $(id -u) != 0 ]]; then
    echo 'Usage: sudo bash install-center.sh SERVER_BINARY WEB_DIST FULLCHAIN_PEM PRIVATE_KEY' >&2
    exit 2
fi

binary=$(realpath -- "$1")
frontend=$(realpath -- "$2")
certificate=$(realpath -- "$3")
private_key=$(realpath -- "$4")
install_dir=/opt/ownpanel-center
config_dir=/etc/ownpanel-center
unit=/etc/systemd/system/ownpanel-center.service
account=ownpanel-center

[[ -f "$binary" && -x "$binary" && -f "$frontend/index.html" ]]
[[ -f "$certificate" && -f "$private_key" ]]
if [[ -e "$install_dir" || -e "$config_dir" || -e "$unit" ]]; then
    echo 'Center installation already exists. Update its files and restart the service.' >&2
    exit 1
fi
cert_public=$(openssl x509 -in "$certificate" -noout -pubkey)
key_public=$(openssl pkey -in "$private_key" -passin pass: -pubout)
if [[ "$cert_public" != "$key_public" ]]; then
    echo 'HTTPS certificate and key do not match' >&2
    exit 1
fi

if ! id "$account" >/dev/null 2>&1; then
    useradd --system --user-group --home-dir /var/lib/ownpanel-center --shell /usr/sbin/nologin "$account"
fi
group=$(id -gn "$account")
install -d -m 0755 "$install_dir/web"
install -d -m 0750 -g "$group" "$config_dir"
install -m 0755 "$binary" "$install_dir/ownpanel-server"
cp -R --no-preserve=mode,ownership -- "$frontend/." "$install_dir/web/"
chmod -R a+rX "$install_dir/web"
install -m 0640 -g "$group" "$certificate" "$config_dir/fullchain.pem"
install -m 0640 -g "$group" "$private_key" "$config_dir/privkey.pem"

cat > "$config_dir/center.env" <<'EOF'
PANEL_LISTEN=127.0.0.1:7080
AGENT_LISTEN=127.0.0.1:7443
AGENT_ADVERTISE=https://localhost:7443
AGENT_HOSTS=localhost,127.0.0.1
EOF

cat > "$unit" <<'EOF'
[Unit]
Description=OwnPanel management center
Wants=network-online.target
After=network-online.target

[Service]
Type=simple
User=ownpanel-center
StateDirectory=ownpanel-center
StateDirectoryMode=0700
UMask=0077
EnvironmentFile=/etc/ownpanel-center/center.env
ExecStart=/opt/ownpanel-center/ownpanel-server -listen ${PANEL_LISTEN} -agent-listen ${AGENT_LISTEN} -agent-advertise ${AGENT_ADVERTISE} -agent-hosts ${AGENT_HOSTS} -data /var/lib/ownpanel-center -web /opt/ownpanel-center/web -tls-cert /etc/ownpanel-center/fullchain.pem -tls-key /etc/ownpanel-center/privkey.pem
Restart=on-failure
RestartSec=3
NoNewPrivileges=true
PrivateTmp=true
ProtectSystem=strict
ProtectHome=true

[Install]
WantedBy=multi-user.target
EOF

systemd-analyze verify "$unit"
systemctl daemon-reload
echo 'Edit /etc/ownpanel-center/center.env for your network, then run:'
echo '  systemctl enable --now ownpanel-center'
