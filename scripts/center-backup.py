"""Create or restore an OwnPanel center state snapshot."""

import argparse
from contextlib import closing
from pathlib import Path
import shutil
import sqlite3
import tempfile


REQUIRED = ("password.hash", "credentials.key", "pki/ca.pem", "pki/ca.key")


def snapshot(source: Path, target: Path) -> None:
    source = source.resolve(strict=True)
    target = target.absolute()
    if target.exists() or target.is_symlink():
        raise ValueError("Destination already exists")
    database = (source / "control.db").resolve(strict=True)
    for name in REQUIRED:
        if not (source / name).is_file():
            raise ValueError(f"Missing required state file: {name}")
    if (source / "credentials.key").stat().st_size != 32:
        raise ValueError("Invalid credentials.key size")

    with tempfile.TemporaryDirectory(prefix=".ownpanel-", dir=target.parent) as staging:
        state = Path(staging) / "state"
        state.mkdir(mode=0o700)
        (state / "pki").mkdir(mode=0o700)
        with closing(sqlite3.connect(database.as_uri() + "?mode=ro", uri=True)) as db:
            db.execute("VACUUM INTO ?", (str(state / "control.db"),))
        with closing(sqlite3.connect(state / "control.db")) as db:
            if db.execute("PRAGMA quick_check").fetchall() != [("ok",)]:
                raise ValueError("SQLite snapshot integrity check failed")
            db.execute("SELECT id, data, secret FROM nodes LIMIT 0")
        for name in REQUIRED + ("initial-password",):
            original = source / name
            if name == "initial-password" and not original.exists():
                continue
            shutil.copyfile(original, state / name)
            (state / name).chmod(0o600)
        (state / "control.db").chmod(0o600)
        if target.exists() or target.is_symlink():
            raise ValueError("Destination appeared during snapshot")
        state.rename(target)


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("source", type=Path, help="Stopped center state or snapshot directory")
    parser.add_argument("destination", type=Path, help="New directory under an existing parent")
    args = parser.parse_args()
    try:
        snapshot(args.source, args.destination)
    except (OSError, ValueError, sqlite3.Error) as exc:
        parser.exit(1, f"Snapshot failed: {exc}\n")
    print(f"Snapshot created: {args.destination}")


if __name__ == "__main__":
    main()
