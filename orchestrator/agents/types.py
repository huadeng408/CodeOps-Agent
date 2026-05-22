from __future__ import annotations

from dataclasses import dataclass, field
from enum import Enum
from typing import Any


class AgentKind(str, Enum):
    DEEP = "deep"
    EXPLORE = "explore"
    GENERAL = "general"
    PLAN = "plan"
    BACKGROUND = "background"
    REVIEW = "review"
    SECURITY = "security"


@dataclass(slots=True)
class AgentTask:
    title: str
    objective: str
    context: dict[str, Any] = field(default_factory=dict)


@dataclass(slots=True)
class AgentResult:
    summary: str
    status: str = "completed"
    artifacts: list[str] = field(default_factory=list)
    notes: list[str] = field(default_factory=list)
