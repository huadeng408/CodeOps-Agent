from __future__ import annotations

from dataclasses import dataclass, field

from .types import AgentKind, AgentResult, AgentTask


@dataclass(slots=True)
class DeepAgent:
    name: str
    kind: AgentKind = AgentKind.DEEP
    tools: list[str] = field(default_factory=list)

    def plan(self, task: AgentTask) -> list[str]:
        return [task.title, task.objective]

    def execute(self, task: AgentTask) -> AgentResult:
        summary = f"{self.name} accepted: {task.title}"
        return AgentResult(summary=summary)

    def verify(self, result: AgentResult) -> AgentResult:
        return result
