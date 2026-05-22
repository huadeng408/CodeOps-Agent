from __future__ import annotations

import json
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
        files = _context_files(task.context)
        if files:
            root = Path(project_root).resolve()
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
        if not findings:
            findings.append("no additional context supplied")
        return findings


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
