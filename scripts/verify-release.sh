#!/bin/bash
set -euo pipefail

if [[ $# != 1 ]]; then
    echo 'Usage: bash verify-release.sh RELEASE.tar.gz' >&2
    exit 2
fi
archive=$(realpath -- "$1")
name=$(basename -- "$archive" .tar.gz)
[[ "$name" =~ ^ownpanel-[A-Za-z0-9.-]+-linux-(amd64|arm64)$ ]]
(cd "$(dirname -- "$archive")" && sha256sum -c "$name.tar.gz.sha256")

scratch=$(mktemp -d)
server_pid=
cleanup() {
    if [[ -n "$server_pid" ]]; then
        kill "$server_pid" 2>/dev/null || true
        wait "$server_pid" 2>/dev/null || true
    fi
    rm -rf -- "$scratch"
}
trap cleanup EXIT
tar -xzf "$archive" -C "$scratch" --no-same-owner
bundle="$scratch/$name"
test -x "$bundle/ownpanel-agent"
test -x "$bundle/ownpanel-server"
for script in install-center.sh install-agent.sh upgrade-center.sh; do
    bash -n "$bundle/scripts/$script"
done
openssl req -x509 -newkey ed25519 -nodes -subj /CN=localhost \
    -addext subjectAltName=DNS:localhost -days 1 -keyout "$scratch/key" \
    -out "$scratch/cert" >/dev/null 2>&1
port=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1", 0)); print(s.getsockname()[1])')
base="https://localhost:$port"
"$bundle/ownpanel-server" -listen "127.0.0.1:$port" -agent-listen '' \
    -data "$scratch/state" -web "$bundle/web" -tls-cert "$scratch/cert" \
    -tls-key "$scratch/key" >"$scratch/server.log" 2>&1 &
server_pid=$!
request=(curl --silent --show-error --fail --max-time 3 --noproxy '*' --cacert "$scratch/cert")
for ((attempt=0;attempt<20;attempt++)); do
    if "${request[@]}" "$base/healthz" >"$scratch/health" 2>/dev/null; then break; fi
    if ! kill -0 "$server_pid" 2>/dev/null; then cat "$scratch/server.log" >&2; exit 1; fi
    sleep .2
done
test "$(cat "$scratch/health")" = '{"ready":true}'
"${request[@]}" "$base/" >"$scratch/index"
python3 - "$scratch/index" <<'PY' >"$scratch/assets"
from html.parser import HTMLParser
from pathlib import Path
import sys


class Assets(HTMLParser):
    def handle_starttag(self, tag, attrs):
        attrs = dict(attrs)
        if tag == "script":
            path = attrs.get("src", "")
        elif tag == "link" and attrs.get("rel") == "stylesheet":
            path = attrs.get("href", "")
        else:
            return
        if path.startswith("/assets/"):
            print(path)


Assets().feed(Path(sys.argv[1]).read_text(encoding="utf-8"))
PY
test -s "$scratch/assets"
while IFS= read -r asset; do
    "${request[@]}" "$base$asset" >"$scratch/asset"
    test -s "$scratch/asset"
done <"$scratch/assets"
IFS= read -r entry_asset <"$scratch/assets"
asset_file="$bundle/web$entry_asset"
test -f "$asset_file"
mv -- "$asset_file" "$scratch/held-asset"
negative_status=$(curl --silent --show-error --max-time 3 --noproxy '*' --cacert "$scratch/cert" \
    -o "$scratch/not-ready" -w '%{http_code}' "$base/healthz")
test "$negative_status" = 503
mv -- "$scratch/held-asset" "$asset_file"
test "$("${request[@]}" "$base/healthz")" = '{"ready":true}'
python3 - "$scratch/state/initial-password" <<'PY' |
from pathlib import Path
import json
import sys

print(json.dumps({"username": "admin", "password": Path(sys.argv[1]).read_text().strip()}))
PY
    "${request[@]}" -H "Origin: $base" -H 'Content-Type: application/json' \
        --data-binary @- -c "$scratch/session" "$base/api/login" >/dev/null
"${request[@]}" -b "$scratch/session" "$base/api/session" |
    python3 -c 'import json, sys; assert json.load(sys.stdin)["username"] == "admin"'
echo 'PASS: release TLS, readiness, missing asset detection, login and authenticated session'
