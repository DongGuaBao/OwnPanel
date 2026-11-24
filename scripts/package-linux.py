"""Archive a staged Linux release with explicit Unix permissions."""

from pathlib import Path
import sys
import tarfile


def linux_permissions(entry: tarfile.TarInfo) -> tarfile.TarInfo:
    executable = Path(entry.name).name in {"ownpanel-agent", "ownpanel-server"}
    entry.mode = 0o755 if entry.isdir() or executable else 0o644
    entry.uid = entry.gid = 0
    entry.uname = entry.gname = ""
    return entry


if __name__ == "__main__":
    bundle, destination = (Path(value) for value in sys.argv[1:])
    with tarfile.open(destination, "w:gz") as archive:
        archive.add(bundle, arcname=bundle.name, filter=linux_permissions)
