#!/bin/bash
# Run as a regular user with sudo access for installing the build output.
set -euo pipefail
[[ ( $# == 2 || $# == 3 ) && $2 =~ ^[a-fA-F0-9]{64}$ ]] || {
    echo 'Usage: bash build-agent-nginx.sh NGINX_SOURCE_TAR_GZ SHA256 [USER_WRITABLE_PREFIX]' >&2
    exit 1
}
[[ $(id -u) != 0 ]] || { echo 'Run the build as a regular user' >&2; exit 1; }
archive=$(realpath -- "$1")
expected=${2,,}
destination=/opt/ownpanel-nginx
installer=(sudo)
if [[ $# == 3 ]]; then
    destination=$(realpath -m -- "$3")
    mkdir -p "$destination"
    [[ -w "$destination" ]] || { echo 'Prefix must be writable without sudo' >&2; exit 1; }
    installer=()
fi
work=$(mktemp -d "$(dirname "$archive")/nginx-build.XXXXXX")
trap 'rm -rf -- "$work"' EXIT
cp -- "$archive" "$work/source.tar.gz"
cd "$work"
printf '%s  source.tar.gz\n' "$expected" | sha256sum --check --status
mkdir source
tar --extract --gzip --file source.tar.gz --directory source --strip-components=1 --no-same-owner --no-same-permissions
cd source
./configure --prefix="$destination" --with-http_ssl_module --with-http_v2_module --with-http_v3_module \
    --with-http_realip_module --with-stream --with-stream_ssl_module --with-stream_ssl_preread_module \
    --error-log-path=stderr --pid-path=nginx.pid --lock-path=nginx.lock
make -j "$(getconf _NPROCESSORS_ONLN)"
objs/nginx -V

"${installer[@]}" install -d -m 0755 "$destination/sbin" "$destination/conf"
"${installer[@]}" install -m 0755 objs/nginx "$destination/sbin/nginx.new"
"${installer[@]}" install -m 0644 conf/mime.types "$destination/conf/mime.types.new"
"${installer[@]}" mv -fT "$destination/conf/mime.types.new" "$destination/conf/mime.types"
"${installer[@]}" mv -fT "$destination/sbin/nginx.new" "$destination/sbin/nginx"
echo "Installed $destination/sbin/nginx"
