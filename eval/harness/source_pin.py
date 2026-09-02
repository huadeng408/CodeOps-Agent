"""Reproducible Git and working-tree pins shared by evaluation runners."""

from __future__ import annotations

import hashlib
import subprocess
from pathlib import Path
from typing import Any


def source_pin(repo_root: str | Path) -> dict[str, Any]:
    """Pin HEAD plus staged, unstaged and untracked file content."""

    root = Path(repo_root).resolve()
    sha = subprocess.run(
        ["git", "rev-parse", "HEAD"],
        cwd=root,
        check=True,
        capture_output=True,
        text=True,
        encoding="utf-8",
        errors="replace",
    ).stdout.strip()
    diff = subprocess.run(
        ["git", "diff", "HEAD", "--no-ext-diff", "--binary"],
        cwd=root,
        check=True,
        capture_output=True,
    ).stdout
    untracked_raw = subprocess.run(
        ["git", "ls-files", "--others", "--exclude-standard", "-z"],
        cwd=root,
        check=True,
        capture_output=True,
    ).stdout
    untracked = sorted(
        path
        for path in untracked_raw.decode("utf-8", errors="replace").split("\x00")
        if path
    )
    digest = hashlib.sha256()
    digest.update(diff)
    for relative in untracked:
        path = root / relative
        if not path.is_file():
            continue
        digest.update(relative.replace("\\", "/").encode("utf-8"))
        digest.update(b"\0")
        digest.update(path.read_bytes())
    return {
        "git_sha": sha,
        "dirty_hash": digest.hexdigest(),
        "untracked_files": len(untracked),
    }


__all__ = ["source_pin"]
