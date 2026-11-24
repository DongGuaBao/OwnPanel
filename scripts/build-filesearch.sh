#!/bin/bash
# Build without sudo; all artifacts remain under OwnPanel.
set -euo pipefail
root=$(cd -- "$(dirname -- "$0")/.." && pwd -P)
cd "$root"
if ! command -v go >/dev/null; then export PATH="$PATH:/usr/local/go/bin"; fi
[[ $(id -u) != 0 ]] || { echo 'Run without sudo' >&2; exit 1; }
export GOCACHE="$root/data/build/go-cache" GOMODCACHE="$root/data/build/go-mod" TMPDIR="$root/data/build/tmp"
mkdir -p "$TMPDIR" bin
build="$root/data/build/filesearch-review"
cmake -S FileSearch -B "$build" -DCMAKE_BUILD_TYPE=Release
cmake --build "$build" --target FileSearchServiceLinux linux_directory_sizes_test linux_size_store_test linux_size_scanner_test linux_size_catalog_test linux_query_cache_test linux_local_repair_test linux_mounts_test linux_fanotify_decode_test linux_name_index_test -j 2
ctest --test-dir "$build" -R '^(linux_directory_sizes|linux_size_store|linux_size_scanner|linux_size_catalog|linux_query_cache|linux_local_repair|linux_mounts|linux_fanotify_decode|linux_name_index)$' --output-on-failure
python3 FileSearch/tests/linux_query_startup_test.py "$build/FileSearchServiceLinux" --base-dir "$TMPDIR"
go test ./internal/panel -run 'Test(FileSearch|DirectorySizes|SSHDirectorySizes)' -count=1
go build -o bin/ownpanel-agent.new ./cmd/agent
mv -f bin/ownpanel-agent.new bin/ownpanel-agent
cp "$build/FileSearchServiceLinux" bin/ownpanel-filesearch.new
mv -f bin/ownpanel-filesearch.new bin/ownpanel-filesearch
npm run build --prefix web
printf '%s\n' 'Built bin/ownpanel-filesearch and bin/ownpanel-agent.'
