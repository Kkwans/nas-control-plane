#!/usr/bin/env python3
"""仅复制白名单 UI 证据，按稳定路径分为最多十六个有界制品。"""

import argparse
from pathlib import Path
import shutil

PART_BYTES = 24 * 1024 * 1024
MAX_PARTS = 16
# 为 ZIP 条目头、目录和 UTF-8 路径另留保守预算，避免大量空文件绕过大小限制。
METADATA_BYTES = 1024 * 1024


def partition(source, destination, *, part_bytes=PART_BYTES, max_parts=MAX_PARTS):
    source, destination = Path(source), Path(destination)
    if part_bytes <= 0 or max_parts <= 0:
        raise ValueError("分片大小和数量必须为正数")
    if destination.exists():
        raise ValueError(f"输出目录已存在，拒绝混入旧证据: {destination}")
    if source.is_symlink():
        raise ValueError(f"源目录不能是符号链接: {source}")
    if source.resolve() == destination.resolve() or source.resolve() in destination.resolve().parents:
        raise ValueError("输出目录不能位于源目录中")

    if source.exists() and not source.is_dir():
        raise ValueError(f"源路径必须是目录: {source}")

    selected = []
    for path in source.rglob("*"):
        relative = path.relative_to(source)
        if any(part.startswith(".") for part in relative.parts):
            continue
        if path.parent.name != "ui-captures" or not (
            path.name.endswith(".png") or path.name.endswith("-capture-state.json")
        ):
            continue
        if path.is_symlink() or any(parent.is_symlink() for parent in path.parents if parent != source):
            raise ValueError(f"证据不能使用符号链接: {relative}")
        if path.is_file():
            selected.append((relative, path.stat().st_size))

    parts = []
    size = metadata = 0
    for relative, file_size in sorted(selected):
        overhead = 1024 + 4 * len(relative.as_posix().encode("utf-8"))
        if file_size > part_bytes or overhead > METADATA_BYTES:
            raise ValueError(f"单个证据超过分片预算: {relative} ({file_size} bytes)")
        if not parts or size + file_size > part_bytes or metadata + overhead > METADATA_BYTES:
            if len(parts) >= max_parts:
                raise ValueError(f"证据超过 {max_parts} 个分片；请调整捕获范围或分片配置")
            parts.append([])
            size = metadata = 0
        parts[-1].append(relative)
        size += file_size
        metadata += overhead

    # 完整预检成功后才复制；源测试结果不移动、不删除、不改写。
    destination.mkdir(parents=True)
    for number, paths in enumerate(parts, 1):
        for relative in paths:
            target = destination / f"part-{number}" / relative
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(source / relative, target)
    return len(parts)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source", type=Path, required=True)
    parser.add_argument("--destination", type=Path, required=True)
    parser.add_argument("--github-output", type=Path)
    args = parser.parse_args()
    try:
        count = partition(args.source, args.destination)
    except (OSError, ValueError) as error:
        parser.exit(1, f"UI 制品分片失败: {error}\n")
    if args.github_output:
        with args.github_output.open("a", encoding="utf-8") as output:
            output.write(f"parts={count}\n")
    print(f"UI 证据已分为 {count} 个分片，每片最多 24 MiB 文件内容。")


if __name__ == "__main__":
    main()
