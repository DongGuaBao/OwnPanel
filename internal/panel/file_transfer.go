package panel

// Keep the source entry itself (including symlinks). Publish a staged copy
// only after it completes, without replacing a destination created meanwhile.
const fileTransferScript = `set -eu
action=$1 source=$2 target=$3
if [ -e "$target" ] || [ -L "$target" ]; then
 echo '目标已存在' >&2; exit 1
fi
if [ "$action" = move ]; then
 mv -nT -- "$source" "$target"
 if [ -e "$source" ] || [ -L "$source" ]; then echo '移动未完成：目标已存在' >&2; exit 1; fi
else
 stage=$(mktemp -d -- "$(dirname -- "$target")/.ownpanel-copy.XXXXXXXX")
 trap 'rm -rf -- "$stage"' EXIT
 cp -aT -- "$source" "$stage/entry"
 mv -nT -- "$stage/entry" "$target"
 if [ -e "$stage/entry" ] || [ -L "$stage/entry" ]; then echo '复制未完成：目标已存在' >&2; exit 1; fi
fi`
