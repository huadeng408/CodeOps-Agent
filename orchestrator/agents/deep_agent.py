from __future__ import annotations

import hashlib
import json
import re
from dataclasses import dataclass
from pathlib import Path
from typing import Any

from .types import AgentKind, AgentResult, AgentTask


@dataclass(slots=True)
class DeepAgentManager:
    project_root: str
    working_dir: str

    def run(self, kind: str, title: str, objective: str, context: dict[str, Any] | None = None) -> AgentResult:
        task = AgentTask(title=title.strip(), objective=objective.strip(), context=context or {})
        agent = DeepAgent(name=kind.strip().lower() or AgentKind.DEEP.value, kind=_agent_kind(kind))
        return agent.execute(task, self.project_root, self.working_dir)


@dataclass(slots=True)
class DeepAgent:
    name: str
    kind: AgentKind = AgentKind.DEEP

    def execute(self, task: AgentTask, project_root: str, working_dir: str) -> AgentResult:
        findings = self._findings(task, project_root, working_dir)
        notes = [
            f"kind: {self.kind.value}",
            f"objective: {task.objective}",
            f"working_dir: {working_dir}",
        ]
        return AgentResult(
            summary=f"{self.name} completed: {task.title}",
            artifacts=findings,
            notes=notes,
        )

    def _findings(self, task: AgentTask, project_root: str, working_dir: str) -> list[str]:
        findings: list[str] = []
        created = _apply_explicit_create_file(task.objective, working_dir)
        if created:
            findings.append(created)
        files = _context_files(task.context)
        if files:
            # Relative context paths are resolved in the Harness-assigned
            # checkout so a child cannot read or report files from its parent
            # working tree. Absolute paths still undergo the same containment
            # check below.
            root = Path(working_dir).resolve()
            existing = []
            missing = []
            for value in files[:20]:
                path = (root / value).resolve() if not Path(value).is_absolute() else Path(value).resolve()
                try:
                    path.relative_to(root)
                except ValueError:
                    missing.append(f"{value} (outside workspace)")
                    continue
                if path.exists():
                    existing.append(value)
                else:
                    missing.append(value)
            if existing:
                findings.append("existing files: " + ", ".join(existing))
            if missing:
                findings.append("missing files: " + ", ".join(missing))
        if task.context:
            findings.append("context: " + json.dumps(task.context, ensure_ascii=False, sort_keys=True))
        required = _required_artifacts(task.context)
        if required:
            findings.extend(_artifact_receipts(required, working_dir))
        if not findings:
            findings.append("no additional context supplied")
        return findings


_CREATE_FILE_PATTERNS = (
    re.compile(
        r"\bcreate\s+file\s+(?P<path>[^\s,，。]+)\s+with\s+content\s+(?P<content>.+?)\s*$",
        re.IGNORECASE,
    ),
    re.compile(
        r"(?:创建文件)\s+(?P<path>[^\s,，。]+)\s*[，,]\s*(?:内容只写|内容仅写)\s+(?P<content>.+?)\s*$",
    ),
)


def _apply_explicit_create_file(objective: str, working_dir: str) -> str | None:
    """Apply only an explicit, single-file creation objective in the child checkout.

    The objective is intentionally narrow: arbitrary shell/code execution is not part
    of the process-agent contract. Paths are resolved beneath the Harness-assigned
    working directory and the receipt contains metadata only.
    """
    objective_text = str(objective or "")
    match = next(
        (candidate for pattern in _CREATE_FILE_PATTERNS if (candidate := pattern.search(objective_text)) is not None),
        None,
    )
    if match is None:
        return None
    raw_path = match.group("path").strip().strip('\"\'')
    content = match.group("content").strip()
    if not raw_path or not content:
        return None
    candidate = (Path(working_dir).resolve() / Path(raw_path)).resolve()
    root = Path(working_dir).resolve()
    try:
        relative = candidate.relative_to(root)
    except ValueError as exc:
        raise ValueError("create-file objective path is outside workspace") from exc
    if candidate == root or relative.as_posix().startswith(".git/") or relative.parts[:1] == (".git",):
        raise ValueError("create-file objective cannot target git metadata")
    candidate.parent.mkdir(parents=True, exist_ok=True)
    candidate.write_text(content, encoding="utf-8")
    digest = hashlib.sha256(candidate.read_bytes()).hexdigest()
    return f"created artifact: {relative.as_posix()} (bytes={candidate.stat().st_size}, sha256={digest})"


def _agent_kind(value: str) -> AgentKind:
    normalized = (value or "").strip().lower()
    for kind in AgentKind:
        if kind.value == normalized:
            return kind
    return AgentKind.DEEP


def _context_files(context: dict[str, Any]) -> list[str]:
    raw = context.get("files") or context.get("paths") or []
    if isinstance(raw, str):
        return [raw]
    if not isinstance(raw, list):
        return []
    return [str(item) for item in raw if str(item).strip()]


def _required_artifacts(context: dict[str, Any]) -> list[str]:
    raw = context.get("required_artifacts", [])
    if isinstance(raw, str):
        raw = [raw]
    if not isinstance(raw, list):
        raise ValueError("required_artifacts must be a list")
    return [str(item).strip() for item in raw if str(item).strip()][:32]


def _artifact_receipts(paths: list[str], working_dir: str) -> list[str]:
    root = Path(working_dir).resolve()
    receipts: list[str] = []
    for value in paths:
        candidate = (root / value).resolve() if not Path(value).is_absolute() else Path(value).resolve()
        try:
            relative = candidate.relative_to(root).as_posix()
        except ValueError as exc:
            raise ValueError(f"required artifact path outside workspace: {value}") from exc
        if not candidate.is_file():
            raise RuntimeError(f"required artifact missing: {relative}")
        digest = hashlib.sha256(candidate.read_bytes()).hexdigest()
        receipts.append(f"artifact: {relative} (bytes={candidate.stat().st_size}, sha256={digest})")
    return receipts
