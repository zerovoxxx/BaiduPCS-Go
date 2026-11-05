#!/usr/bin/env python3
"""Stopped-service backup/restore, using the server's POSIX directory lock.

The entire private service directory (key, SQLite and journals, artifacts,
staging/receiving bytes and identity files) is one recovery unit. Destination
must be new. Startup configuration, external token files and local roots are
outside this unit and must be backed up separately while stopped.
"""
import argparse
import contextlib
import fcntl
import hashlib
import json
import os
from pathlib import Path, PurePosixPath
import shutil
import sqlite3
import stat
import sys
import tempfile
from urllib.parse import quote

MANIFEST = 'backup-manifest.json'
MAX_ENTRIES = 100000


def require(condition, message):
    if not condition:
        raise ValueError(message)


def directory(path):
    path = Path(path).absolute()
    for part in [path, *path.parents]:
        require(not part.is_symlink(), 'symlink directories are not allowed')
    require(path.is_dir(), 'source must be an existing directory')
    require(stat.S_IMODE(path.stat().st_mode) & 0o077 == 0, 'source directory must be private (0700)')
    return path


@contextlib.contextmanager
def stopped(path):
    fd = os.open(path / 'service.lock', os.O_CREAT | os.O_RDWR | os.O_NOFOLLOW, 0o600)
    try:
        require(stat.S_ISREG(os.fstat(fd).st_mode), 'invalid lock file')
        try:
            fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            raise ValueError('service directory is locked; stop the service before backup/restore') from None
        yield
    finally:
        os.close(fd)


def entries(root):
    found = []
    def walk(base):
        for p in sorted(base.iterdir()):
            relative = p.relative_to(root).as_posix()
            if relative in ('service.lock', MANIFEST):
                continue
            info = p.lstat()
            require(stat.S_ISDIR(info.st_mode) or stat.S_ISREG(info.st_mode), 'symlink or special file in service data')
            row = {'path': relative, 'type': 'directory' if p.is_dir() else 'file'}
            if row['type'] == 'file':
                row.update(size=info.st_size, sha256=digest(p))
            found.append(row)
            require(len(found) <= MAX_ENTRIES, 'backup entry limit exceeded')
            if row['type'] == 'directory':
                walk(p)
    walk(root)
    return sorted(found, key=lambda r: r['path'])


def digest(path):
    fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
    with os.fdopen(fd, 'rb') as f:
        require(stat.S_ISREG(os.fstat(f.fileno()).st_mode), 'expected regular data file')
        h = hashlib.sha256()
        for block in iter(lambda: f.read(1 << 20), b''):
            h.update(block)
        return h.hexdigest()


def database_schema(root):
    require((root / 'master.key').is_file() and not (root / 'master.key').is_symlink(), 'master.key is required')
    require((root / 'master.key').stat().st_size == 32, 'invalid master.key length')
    require((root / 'service.db').is_file() and not (root / 'service.db').is_symlink(), 'service.db is required')
    # mode=ro reads the corresponding WAL as well; never set immutable or change
    # journal mode, checkpoint, or lower user_version to force compatibility.
    # Inspect a private scratch copy: reading a WAL can update its shared-memory
    # index even with mode=ro. The archived DB and journal bytes stay untouched.
    with tempfile.TemporaryDirectory(prefix='baidupcs-backup-check-') as tmp:
        tmp = Path(tmp)
        tmp.chmod(0o700)
        for name in ('service.db', 'service.db-wal'):
            if (root / name).exists():
                require(not (root / name).is_symlink() and (root / name).is_file(), 'invalid database journal')
                with (root / name).open('rb') as src, (tmp / name).open('xb') as dst:
                    os.fchmod(dst.fileno(), 0o600)
                    shutil.copyfileobj(src, dst, 1 << 20)
        db = sqlite3.connect('file:' + quote(str(tmp / 'service.db')) + '?mode=ro', uri=True)
        try:
            require(db.execute('PRAGMA quick_check').fetchone() == ('ok',), 'database quick_check failed')
            version = db.execute('PRAGMA user_version').fetchone()[0]
            require(version >= 1, 'uninitialized service database')
            return version
        finally:
            db.close()


def sync_directory(path):
    fd = os.open(path, os.O_RDONLY | os.O_DIRECTORY)
    try:
        os.fsync(fd)
    finally:
        os.close(fd)


def new_destination(source, destination):
    dest = Path(destination).absolute()
    for part in [dest, *dest.parents]:
        require(not part.is_symlink(), 'symlink destinations are not allowed')
    require(not dest.exists(), 'destination must not exist')
    require(dest.parent.is_dir(), 'destination parent must exist')
    require(source not in dest.parents and dest not in source.parents, 'source and destination must be separate trees')
    dest.mkdir(mode=0o700)
    dest.chmod(0o700)
    return dest


def copy_tree(source, dest, rows):
    for row in sorted(rows, key=lambda r: (len(PurePosixPath(r['path']).parts), r['path'])):
        src, target = source / row['path'], dest / row['path']
        if row['type'] == 'directory':
            target.mkdir(mode=0o700)
            target.chmod(0o700)
        else:
            fd = os.open(src, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
            with os.fdopen(fd, 'rb') as incoming, target.open('xb') as outgoing:
                require(stat.S_ISREG(os.fstat(incoming.fileno()).st_mode), 'expected regular source file')
                os.fchmod(outgoing.fileno(), 0o600)
                shutil.copyfileobj(incoming, outgoing, 1 << 20)
                outgoing.flush()
                os.fsync(outgoing.fileno())
    for row in sorted(rows, key=lambda r: len(PurePosixPath(r['path']).parts), reverse=True):
        if row['type'] == 'directory':
            sync_directory(dest / row['path'])
    sync_directory(dest)


def validate_manifest(value):
    require(isinstance(value, dict), 'invalid manifest object')
    require(value.get('format') == 'baidupcs-stopped-backup-v1', 'unsupported backup manifest')
    rows = value.get('entries')
    require(isinstance(rows, list) and len(rows) <= MAX_ENTRIES, 'invalid manifest entry count')
    seen = set()
    for row in rows:
        require(isinstance(row, dict), 'invalid manifest entry')
        name = row.get('path', '')
        require(isinstance(name, str), 'invalid manifest path')
        p = PurePosixPath(name)
        require(name and not p.is_absolute() and '..' not in p.parts and str(p) == name and '\\' not in name and '\x00' not in name, 'unsafe manifest path')
        require(name not in seen and name not in (MANIFEST, 'service.lock'), 'duplicate or reserved manifest path')
        require(row.get('type') in ('file', 'directory'), 'invalid manifest entry type')
        seen.add(name)
    return rows


def backup(source, destination):
    source = directory(source)
    with stopped(source):
        schema = database_schema(source)
        rows = entries(source)
        dest = new_destination(source, destination)
        try:
            copy_tree(source, dest, rows)
            require(entries(dest) == rows, 'data changed during backup')
            require(database_schema(dest) == schema, 'copied database schema mismatch')
            value = {'format': 'baidupcs-stopped-backup-v1', 'sqlite_schema': schema, 'entries': rows}
            with (dest / MANIFEST).open('x') as f:
                os.fchmod(f.fileno(), 0o600)
                json.dump(value, f, indent=2)
                f.write('\n')
                f.flush()
                os.fsync(f.fileno())
            sync_directory(dest)
            sync_directory(dest.parent)
        except BaseException:
            shutil.rmtree(dest)
            raise
    return schema


def restore(source, destination):
    source = directory(source)
    with stopped(source):
        manifest = source / MANIFEST
        require(manifest.is_file() and not manifest.is_symlink() and manifest.stat().st_size <= 64 << 20, 'invalid backup manifest')
        value = json.loads(manifest.read_text())
        rows = validate_manifest(value)
        require(entries(source) == rows, 'backup content/hash mismatch')
        schema = database_schema(source)
        require(schema == value.get('sqlite_schema'), 'backup schema mismatch')
        dest = new_destination(source, destination)
        try:
            copy_tree(source, dest, rows)
            require(entries(dest) == rows, 'restored content/hash mismatch')
            require(database_schema(dest) == schema, 'restored database mismatch')
            sync_directory(dest)
            sync_directory(dest.parent)
        except BaseException:
            shutil.rmtree(dest)
            raise
    return schema


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('action', choices=['backup', 'restore'])
    parser.add_argument('source', type=Path)
    parser.add_argument('destination', type=Path)
    args = parser.parse_args()
    try:
        schema = (backup if args.action == 'backup' else restore)(args.source, args.destination)
    except (ValueError, OSError, sqlite3.Error) as error:
        # Do not print file contents, keys, config, or exception traces.
        print('ERROR: ' + str(error), file=sys.stderr)
        return 1
    print(json.dumps({'action': args.action, 'sqlite_schema': schema, 'status': 'passed'}))
    return 0


if __name__ == '__main__':
    sys.exit(main())
