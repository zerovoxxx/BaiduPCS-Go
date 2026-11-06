#!/usr/bin/env python3
"""Archive completed SPECs by metadata date and repair references (Python 3.9+)."""
from __future__ import annotations

import argparse
from pathlib import Path
import subprocess
import sys

sys.dont_write_bytecode = True
sys.path.insert(0, str(Path(__file__).resolve().parent))
import spec_workflow as w

THRESHOLD = 10
KEEP_COMPLETED = 3


def archive(root: Path, *, dry_run: bool, force: bool) -> int:
    root = root.resolve()
    git_root = subprocess.run(['git', '-C', str(root), 'rev-parse', '--show-toplevel'], check=True, capture_output=True, text=True).stdout.strip()
    if not root.is_relative_to(Path(git_root).resolve()):
        raise w.WorkflowError('--root 必须是 Git 仓库内的 Harness 项目目录')
    w.reject_legacy_indexes(root)
    documents = w.collect_documents(root)
    specs = [d for d in documents if d.kind == 'SPEC' and not d.archived]
    if len(specs) <= THRESHOLD and not force:
        print(f'no-op：version/ 下 {len(specs)} 个 SPEC（阈值 {THRESHOLD}）')
        return 0
    completed = [d for d in specs if d.info['文档状态'] == '已完成']
    completed.sort(key=lambda d: d.identity)
    completed.sort(key=lambda d: d.info['创建日期'], reverse=True)
    candidates = completed[KEEP_COMPLETED:]
    if not candidates:
        print('no-op：已完成 SPEC 不足以触发归档')
        return 0
    moves = {d.path: w.DOCS / 'version/archive' / d.path.name for d in candidates}
    for destination in moves.values():
        if w.safe_path(root, destination).exists():
            raise w.WorkflowError(f'归档目标已存在：{destination}')
    changes = {}
    for path in sorted(set(w.project_files(root)) | {d.path for d in documents}):
        if not (root / path).is_file() or (root / path).is_symlink():
            continue
        raw = (root / path).read_bytes()
        if b'\0' in raw:
            continue
        try:
            content = raw.decode('utf-8')
        except UnicodeError:
            continue
        destination = moves.get(path, path)
        updated = w.rewrite_references(content, path, destination, moves).encode('utf-8')
        if destination != path:
            changes[path] = None
        if updated != raw or destination != path:
            changes[destination] = updated
    w.collect_documents(root, changes)  # moved SPECs and PLAN source links must stay valid
    print(f'归档 {len(candidates)} 个已完成 SPEC（保留最新 {KEEP_COMPLETED} 个）：')
    for source, destination in moves.items():
        print(f'  {source} -> {destination}')
    if not dry_run:
        modes = {destination: (root / source).stat().st_mode & 0o777 for source, destination in moves.items()}
        w.apply_changes(root, changes, modes)
        print('归档完成，引用已修正；未修改 Git 暂存区。')
    return 0


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--root', type=Path, default=Path.cwd())
    parser.add_argument('--dry-run', action='store_true')
    parser.add_argument('--force', action='store_true')
    args = parser.parse_args()
    try:
        return archive(args.root, dry_run=args.dry_run, force=args.force)
    except (w.WorkflowError, OSError, UnicodeError, subprocess.CalledProcessError) as error:
        print(f'ERROR: {error}', file=sys.stderr)
        return 1


if __name__ == '__main__':
    raise SystemExit(main())
