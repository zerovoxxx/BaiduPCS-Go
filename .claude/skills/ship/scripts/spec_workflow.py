#!/usr/bin/env python3
"""Stable SPEC identities, metadata validation and on-demand requirement lists (Python 3.9+)."""
from __future__ import annotations

import argparse
from dataclasses import dataclass
from datetime import date, datetime, timezone
import os
from pathlib import Path
import re
import secrets
import stat
import subprocess
import sys
import tempfile
from typing import Optional

ALPHABET = "0123456789abcdefghijklmnopqrstuvwxyz"
# New IDs are YYMMDD-xxx; the earlier YYMMDDHHmm-xxx (HHmm inserted after the date) stays valid but is no longer generated.
ID_PATTERN = r"[0-9]{6}(?:[0-9]{4})?-[0-9a-z]{3}"
NAME = re.compile(rf"(?P<id>{ID_PATTERN})_(?P<slug>[A-Z][A-Za-z0-9]*)_(?P<kind>SPEC|PLAN)\.md")
FIELDS = ("文档类型", "需求 ID", "文档状态", "发布版本", "创建日期", "最后更新", "作者", "关联文档", "一句话目标")
SPEC_STATES = {"待实施", "开发中", "已完成", "阻塞"}
ACTIVE_STATES = {"待实施", "开发中", "阻塞"}
DOCS = Path("docs/astack")
# Committed indexes are obsolete; only upgrade-harness.py reads them, as migration evidence.
LEGACY_INDEXES = (DOCS / "INDEX.md", DOCS / "version/archive/INDEX.md")


class WorkflowError(ValueError):
    pass


def new_id(timestamp_ms: Optional[int] = None) -> str:
    timestamp = datetime.now(timezone.utc) if timestamp_ms is None else datetime.fromtimestamp(timestamp_ms / 1000, timezone.utc)
    if not 2000 <= timestamp.year < 2100:
        raise WorkflowError("短 ID 日期支持 2000–2099 年")
    suffix = "".join(secrets.choice(ALPHABET) for _ in range(3))
    return f"{timestamp:%y%m%d}-{suffix}"


def unique_id(used: set[str], timestamp_ms: Optional[int] = None) -> str:
    for _ in range(100):
        identity = new_id(timestamp_ms)
        if identity not in used:
            used.add(identity)
            return identity
    raise WorkflowError("无法分配未占用的短 ID，请重试")


def spec_ids(docs: Path) -> set[str]:
    return {p.name.split("_")[0] for p in docs.rglob("*_SPEC.md")}


def worktree_ids(root: Path) -> set[str]:
    """IDs already used in any worktree of this repository, so parallel worktrees do not draw the same one.

    Best effort: outside Git, or when Git cannot list worktrees, nothing is added.
    """
    root = root.resolve()
    try:
        top = Path(os.fsdecode(subprocess.run(["git", "-C", str(root), "rev-parse", "--show-toplevel"], check=True, capture_output=True).stdout.strip())).resolve()
        relative = root.relative_to(top)  # a monorepo sub-project keeps its docs at the same relative path in every worktree
        for separator, flags in ((b"\0", ["-z"]), (b"\n", [])):  # -z stays unambiguous for a path containing a newline; Git without it falls back to the plain format
            listing = subprocess.run(["git", "-C", str(root), "worktree", "list", "--porcelain", *flags], capture_output=True)
            if listing.returncode == 0:
                break
        else:
            return set()
    except (OSError, ValueError, subprocess.CalledProcessError):
        return set()
    paths = [Path(os.fsdecode(field[len(b"worktree "):])) for field in listing.stdout.split(separator) if field.startswith(b"worktree ")]
    return {identity for path in paths for identity in spec_ids(path / relative / DOCS)}


def metadata(content: str) -> dict[str, str]:
    """Read only the opening document info, never historical tables in the body."""
    header = re.split(r"(?m)^##\s", content, maxsplit=1)[0]
    result: dict[str, str] = {}
    for line in header.splitlines():
        match = re.fullmatch(r"[>\s]*\|\s*([^|]+?)\s*\|\s*(.*?)\s*\|\s*", line)
        if match and match[1] in FIELDS:
            if match[1] in result:
                raise WorkflowError(f"文档信息字段重复：{match[1]}")
            result[match[1]] = match[2]
    return result


@dataclass(frozen=True)
class Document:
    path: Path  # relative to the project root
    identity: str
    slug: str
    kind: str
    title: str
    info: dict[str, str]

    @property
    def archived(self) -> bool:
        return self.path.parent == DOCS / "version/archive"


def parse_document(path: Path, content: str) -> Document:
    match = NAME.fullmatch(path.name)
    if not match:
        raise WorkflowError(f"{path}: 文件名必须为 <YYMMDD-随机三位>_<PascalSlug>_SPEC.md / _PLAN.md（既有 <YYMMDDHHmm-随机三位> 同样有效）；旧格式请先运行 upgrade-harness.py")
    stamp = match["id"].partition("-")[0]
    try:
        datetime.strptime("20" + stamp, "%Y%m%d" if len(stamp) == 6 else "%Y%m%d%H%M")
    except ValueError:
        raise WorkflowError(f"{path}: ID 中的日期无效") from None
    if re.search(r"(?m)^(<<<<<<< |=======\s*$|>>>>>>> )", content):
        raise WorkflowError(f"{path}: 来源文档仍有合并冲突标记")
    info = metadata(content)
    for field in FIELDS:
        if not info.get(field):
            raise WorkflowError(f"{path}: 缺少文档信息字段 {field}")
    if info["需求 ID"] != match["id"] or info["文档类型"] != match["kind"]:
        raise WorkflowError(f"{path}: 文件名与需求 ID / 文档类型不一致")
    states = SPEC_STATES if match["kind"] == "SPEC" else {"待执行", "已完成"}
    if info["文档状态"] not in states:
        raise WorkflowError(f"{path}: 非法文档状态 {info['文档状态']}")
    for field in ("创建日期", "最后更新"):
        try:
            value = date.fromisoformat(info[field])
            if value.isoformat() != info[field]:
                raise ValueError()
        except ValueError:
            raise WorkflowError(f"{path}: {field} 必须是 YYYY-MM-DD") from None
    if info["最后更新"] < info["创建日期"]:
        raise WorkflowError(f"{path}: 最后更新早于创建日期")
    title = re.search(r"(?m)^#\s+(.+?)\s*$", content)
    if not title:
        raise WorkflowError(f"{path}: 缺少一级标题")
    return Document(path, match["id"], match["slug"], match["kind"], title[1], info)


def collect_documents(root: Path, overlay: Optional[dict[Path, Optional[bytes]]] = None) -> list[Document]:
    overlay = overlay or {}
    paths = set()
    for folder, kind in ((DOCS / "version", "SPEC"), (DOCS / "plan", "PLAN")):
        paths.update(p.relative_to(root) for p in (root / folder).rglob("*.md") if p.name.lower().endswith((f"_{kind.lower()}.md", f"-{kind.lower()}.md")))
    paths.update(p for p in overlay if p.name.endswith(("_SPEC.md", "_PLAN.md")) and p.is_relative_to(DOCS))
    documents = []
    for path in sorted(paths):
        if path in overlay and overlay[path] is None:
            continue
        safe_path(root, path)
        content = overlay[path] if path in overlay else (root / path).read_bytes()
        doc = parse_document(path, content.decode("utf-8"))
        allowed = {DOCS / "version", DOCS / "version/archive"} if doc.kind == "SPEC" else {DOCS / "plan"}
        if path.parent not in allowed:
            raise WorkflowError(f"{path}: 不支持的文档目录")
        documents.append(doc)
    specs: dict[str, Document] = {}
    plans: dict[str, Document] = {}
    for doc in documents:
        group = specs if doc.kind == "SPEC" else plans
        if doc.identity in group:
            raise WorkflowError(f"重复需求 ID：{doc.path} 与 {group[doc.identity].path}")
        group[doc.identity] = doc
    for identity, plan in plans.items():
        spec = specs.get(identity)
        if not spec or spec.slug != plan.slug:
            raise WorkflowError(f"{plan.path}: PLAN 缺少同 ID / Slug 的来源 SPEC")
        targets = re.findall(r"\[[^\]]*\]\(([^)]+)\)", plan.info["关联文档"])
        if not any(Path(os.path.normpath(plan.path.parent / target)) == spec.path for target in targets):
            raise WorkflowError(f"{plan.path}: 关联文档未链接来源 SPEC")
    return documents


def cell(value: str) -> str:
    return value.replace("|", "&#124;").replace("\n", " ")


def render_list(documents: list[Document], *, active: bool = False, include_archived: bool = False) -> str:
    """Render the requirement table on demand; nothing is written to the project."""
    plans = {d.identity: d for d in documents if d.kind == "PLAN"}
    specs = [d for d in documents if d.kind == "SPEC" and (include_archived or not d.archived)
             and (not active or d.info["文档状态"] in ACTIVE_STATES)]
    if not specs:
        return "无匹配需求\n"
    specs.sort(key=lambda d: d.identity)
    specs.sort(key=lambda d: d.info["创建日期"], reverse=True)
    lines = ["| 需求 ID | 状态 | 发布版本 | 创建日期 | 标题 | 文档 |\n", "|---|---|---|---|---|---|\n"]
    for doc in specs:
        paths = doc.path.as_posix()
        if doc.identity in plans:
            paths += f" · {plans[doc.identity].path.as_posix()}"
        values = (doc.identity, doc.info["文档状态"], doc.info["发布版本"], doc.info["创建日期"], doc.title, paths)
        lines.append("| " + " | ".join(cell(v) for v in values) + " |\n")
    return "".join(lines)


def reject_legacy_indexes(root: Path) -> None:
    found = [path.as_posix() for path in LEGACY_INDEXES if (root / path).exists()]
    if found:
        raise WorkflowError(f"已废弃的 INDEX 仍存在：{'、'.join(found)}；运行 harness-init 的 upgrade-harness.py 升级")


def safe_path(root: Path, path: Path) -> Path:
    if path.is_absolute() or ".." in path.parts:
        raise WorkflowError(f"不安全的项目路径：{path}")
    target = root / path
    for candidate in (target, *target.parents):
        if candidate == root:
            break
        if candidate.is_symlink():
            raise WorkflowError(f"拒绝写入或读取软链路径：{candidate}")
    if target.exists() and not target.is_file():
        raise WorkflowError(f"目标不是普通文件：{path}")
    return target


def write_atomic(path: Path, content: bytes, mode: Optional[int] = None) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    mode = mode if mode is not None else (stat.S_IMODE(path.stat().st_mode) if path.exists() else 0o644)
    fd, temporary = tempfile.mkstemp(prefix=".astack-", dir=path.parent)
    try:
        with os.fdopen(fd, "wb") as stream:
            stream.write(content)
        os.chmod(temporary, mode)
        os.replace(temporary, path)
    finally:
        if os.path.exists(temporary):
            os.unlink(temporary)


def apply_changes(root: Path, changes: dict[Path, Optional[bytes]], modes: Optional[dict[Path, int]] = None) -> None:
    """Preflight all paths, preserve existing bytes/modes and roll back partial writes."""
    before = {}
    existing_dirs = set()
    for path in changes:
        target = safe_path(root, path)
        before[path] = (target.read_bytes(), stat.S_IMODE(target.stat().st_mode)) if target.exists() else (None, 0o644)
        existing_dirs.update(p for p in target.parents if p.exists())
    touched = []
    try:
        for path, content in changes.items():
            target = root / path
            touched.append(path)
            if content is None:
                target.unlink(missing_ok=True)
            else:
                write_atomic(target, content, (modes or {}).get(path, before[path][1]))
    except BaseException as error:
        failures = []
        for path in reversed(touched):
            content, mode = before[path]
            try:
                if content is None:
                    (root / path).unlink(missing_ok=True)
                else:
                    write_atomic(root / path, content, mode)
            except OSError as failure:
                failures.append(f"{path}: {failure}")
        for parent in sorted({p for path in changes for p in (root / path).parents if p not in existing_dirs}, key=lambda p: len(p.parts), reverse=True):
            if parent.exists() and not any(parent.iterdir()):
                parent.rmdir()
        if failures:
            raise WorkflowError("写入失败且恢复不完整：" + "; ".join(failures)) from error
        raise


def list_requirements(root: Path, *, active: bool = False, include_archived: bool = False) -> int:
    reject_legacy_indexes(root)
    print(render_list(collect_documents(root), active=active, include_archived=include_archived), end="")
    return 0


def lint(root: Path, target: Path) -> int:
    reject_legacy_indexes(root)
    documents = collect_documents(root)
    target = target.resolve()
    if not target.exists() or not target.is_relative_to(root):
        raise WorkflowError(f"校验目标不存在或不在项目内：{target}")
    selected = [d for d in documents if (root / d.path == target or (target.is_dir() and (root / d.path).is_relative_to(target))) and d.kind == "SPEC"]
    if target.is_file() and not selected:
        raise WorkflowError(f"校验目标不是合法 SPEC：{target}")
    warnings = 0
    for doc in selected:
        content = (root / doc.path).read_text(encoding="utf-8")
        for label, pattern in (("目标", r"目标|背景|In scope"), ("非目标", r"非目标|Out.of.scope"), ("验收标准", r"验收标准|Acceptance"), ("验证计划", r"验证计划|Verification plan|验证命令"), ("验证记录", r"验证记录|Verification record|校验结果"), ("变更日志", r"变更日志|变更记录|Changelog|Change log")):
            if not re.search(pattern, content, re.IGNORECASE):
                print(f"WARN: {doc.path}: 缺少{label}")
                warnings += 1
    print(f"Spec Lint PASS: {len(selected)} file(s), warnings: {warnings}")
    return 0


def project_files(root: Path) -> list[Path]:
    result = subprocess.run(["git", "-C", str(root), "ls-files", "-z", "--cached", "--others", "--exclude-standard"], check=True, capture_output=True)
    return sorted({Path(os.fsdecode(p)) for p in result.stdout.split(b"\0") if p and not os.fsdecode(p).startswith(".astack/")})


def rewrite_references(content: str, old_path: Path, new_path: Path, moves: dict[Path, Path]) -> str:
    """Rewrite local Markdown destinations, then exact file references in prose/code."""
    names: dict[str, set[str]] = {}
    for source, destination in moves.items():
        names.setdefault(source.name, set()).add(destination.name)

    def link(match: re.Match[str]) -> str:
        label, raw = match[1], match[2]
        if re.match(r"[a-zA-Z][\w+.-]*:|//|#", raw):
            return match[0]
        destination, separator, fragment = raw.partition("#")
        if "?" in destination or " " in destination:
            return match[0]
        absolute = Path(os.path.normpath(old_path.parent / destination))
        moved = moves.get(absolute, absolute)
        if moved == absolute and old_path.parent == new_path.parent:
            return match[0]
        relative = os.path.relpath(moved, new_path.parent)
        # A filename used as the link text follows its renamed target.
        if label == Path(destination).name and absolute in moves:
            label = moved.name
        return f"[{label}]({Path(relative).as_posix()}{separator}{fragment})"

    # Protect remote URLs, including immutable commit links in historical reports.
    parts = re.split(r"(https?://[^\s<>\)\]`]+)", content)
    for i in range(0, len(parts), 2):
        text = re.sub(r"\[([^\]\n]*)\]\(([^)\n]+)\)", link, parts[i])
        for source, destination in sorted(moves.items(), key=lambda pair: len(str(pair[0])), reverse=True):
            text = text.replace(source.as_posix(), destination.as_posix())
        for old, destinations in names.items():
            if old not in text:
                continue
            if len(destinations) != 1:
                raise WorkflowError(f"{old_path}: 旧文件名引用存在歧义：{old}")
            text = re.sub(r"(?<![A-Za-z0-9_])" + re.escape(old) + r"(?![A-Za-z0-9_])", next(iter(destinations)), text)
        parts[i] = text
    return "".join(parts)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    commands = parser.add_subparsers(dest="command", required=True)
    identity = commands.add_parser("id", help="生成 YYMMDD-随机三位 的 10 字符 ID（UTC）")
    identity.add_argument("--root", type=Path, default=Path.cwd())
    listing = commands.add_parser("list", help="按需列出需求（默认不含归档）")
    listing.add_argument("--root", type=Path, default=Path.cwd())
    listing.add_argument("--active", action="store_true", help="只列待实施 / 开发中 / 阻塞")
    listing.add_argument("--all", action="store_true", help="包含归档需求")
    validate = commands.add_parser("lint", help="校验新格式文档")
    validate.add_argument("target", type=Path, nargs="?", default=DOCS / "version")
    validate.add_argument("--root", type=Path, default=Path.cwd())
    args = parser.parse_args()
    try:
        if args.command == "id":
            used = spec_ids(args.root / DOCS) | worktree_ids(args.root)
            print(unique_id(used))
            return 0
        root = args.root.resolve()
        if args.command == "list":
            return list_requirements(root, active=args.active, include_archived=args.all)
        target = args.target if args.target.is_absolute() else root / args.target
        return lint(root, target)
    except (WorkflowError, OSError, UnicodeError, subprocess.CalledProcessError) as error:
        print(f"ERROR: {error}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
