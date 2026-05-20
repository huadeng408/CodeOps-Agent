from __future__ import annotations

from dataclasses import dataclass
from pathlib import Path
import subprocess


@dataclass(frozen=True, slots=True)
class GitDiffSnapshot:
    status: str = ""
    diff_stat: str = ""
    changed_files: str = ""
    truncated: bool = False

    def is_empty(self) -> bool:
        return not (self.status or self.diff_stat or self.changed_files)

    def to_prompt(self) -> str:
        if self.is_empty():
            return "_No git changes detected._"

        sections: list[str] = ["Git diff context:"]
        if self.status:
            sections.extend(["", "Status:", self.status])
        if self.diff_stat:
            sections.extend(["", "Diff stat:", self.diff_stat])
        if self.changed_files:
            sections.extend(["", "Changed files:", self.changed_files])
        if self.truncated:
            sections.extend(["", "[diff context truncated]"])
        return "\n".join(sections).strip()


def load_git_diff_context(
    project_root: str | Path,
    working_dir: str | Path,
    max_chars: int = 6_000,
) -> str:
    snapshot = load_git_diff_snapshot(project_root, working_dir, max_chars=max_chars)
    return snapshot.to_prompt()


def load_git_diff_snapshot(
    project_root: str | Path,
    working_dir: str | Path,
    max_chars: int = 6_000,
) -> GitDiffSnapshot:
    cwd = _resolve_cwd(project_root, working_dir)
    if cwd is None:
        return GitDiffSnapshot()

    status = _run_git(cwd, "status", "--short")
    if status is None:
        return GitDiffSnapshot()

    diff_stat = "\n".join(
        part
        for part in (
            _run_git(cwd, "diff", "--stat") or "",
            _run_git(cwd, "diff", "--cached", "--stat") or "",
        )
        if part.strip()
    )
    changed_files = "\n".join(
        part
        for part in (
            _run_git(cwd, "diff", "--name-status") or "",
            _run_git(cwd, "diff", "--cached", "--name-status") or "",
        )
        if part.strip()
    )

    snapshot = GitDiffSnapshot(
        status=status.strip(),
        diff_stat=diff_stat.strip(),
        changed_files=changed_files.strip(),
    )
    return _truncate_snapshot(snapshot, max_chars)


def _resolve_cwd(project_root: str | Path, working_dir: str | Path) -> Path | None:
    for value in (working_dir, project_root):
        if value is None:
            continue
        path = Path(value).expanduser()
        try:
            path = path.resolve()
        except OSError:
            path = path.absolute()
        if path.exists():
            return path if path.is_dir() else path.parent
    return None


def _run_git(cwd: Path, *args: str) -> str | None:
    try:
        completed = subprocess.run(
            ["git", *args],
            cwd=str(cwd),
            check=False,
            capture_output=True,
            encoding="utf-8",
            errors="replace",
            timeout=2,
        )
    except (OSError, subprocess.SubprocessError):
        return None
    if completed.returncode != 0:
        return None
    return completed.stdout.strip()


def _truncate_snapshot(snapshot: GitDiffSnapshot, max_chars: int) -> GitDiffSnapshot:
    if max_chars <= 0:
        return snapshot
    prompt = snapshot.to_prompt()
    if len(prompt) <= max_chars:
        return snapshot

    budget = max(0, max_chars // 3)
    return GitDiffSnapshot(
        status=_truncate_text(snapshot.status, budget),
        diff_stat=_truncate_text(snapshot.diff_stat, budget),
        changed_files=_truncate_text(snapshot.changed_files, budget),
        truncated=True,
    )


def _truncate_text(value: str, max_chars: int) -> str:
    if len(value) <= max_chars:
        return value
    if max_chars <= 16:
        return value[:max_chars]
    return value[: max_chars - 16].rstrip() + "\n[truncated]"
