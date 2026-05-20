from __future__ import annotations

from pathlib import Path

from orchestrator.context.compactor import Compactor


def load_agent_instructions(
    project_root: str | Path | None = None,
    working_dir: str | Path | None = None,
) -> str:
    paths: list[Path] = []

    home_agent = Path.home() / ".agent" / "AGENT.md"
    if home_agent.exists():
        paths.append(home_agent)

    for directory in _instruction_directories(project_root, working_dir):
        path = directory / "AGENT.md"
        if path.exists() and path not in paths:
            paths.append(path)

    if not paths:
        return ""

    compactor = Compactor(max_chars=8_000)
    chunks: list[str] = []
    for path in paths:
        try:
            content = path.read_text(encoding="utf-8")
        except OSError:
            continue
        content = compactor.compact_text(content).strip()
        if not content:
            continue
        chunks.append(f"[{path}]\n{content}")
    return "\n\n".join(chunks)


def _instruction_directories(
    project_root: str | Path | None,
    working_dir: str | Path | None,
) -> list[Path]:
    root = _normalize_dir(project_root)
    work = _normalize_dir(working_dir)

    if root is None and work is None:
        return []
    if root is None:
        return [work] if work is not None else []
    if work is None or work == root:
        return [root]

    if _is_ancestor(root, work):
        chain: list[Path] = []
        current = work
        while True:
            chain.append(current)
            if current == root:
                break
            current = current.parent
        return list(reversed(chain))

    return [root, work]


def _normalize_dir(value: str | Path | None) -> Path | None:
    if value is None:
        return None
    path = Path(value).expanduser()
    try:
        resolved = path.resolve()
    except OSError:
        resolved = path.absolute()
    if resolved.is_file():
        return resolved.parent
    return resolved


def _is_ancestor(candidate: Path, path: Path) -> bool:
    try:
        path.relative_to(candidate)
        return True
    except ValueError:
        return False
