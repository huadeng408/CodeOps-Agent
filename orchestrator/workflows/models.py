from __future__ import annotations

from dataclasses import dataclass, field
from enum import Enum
from typing import Any


class WorkerState(str, Enum):
    PENDING = "pending"
    RUNNING = "running"
    COMPLETED = "completed"
    FAILED = "failed"
    BLOCKED = "blocked"


@dataclass(frozen=True, slots=True)
class WorkerSpec:
    id: str
    title: str
    objective: str
    provider: str = "default"
    depends_on: tuple[str, ...] = ()
    context: dict[str, Any] = field(default_factory=dict)


@dataclass(frozen=True, slots=True)
class WorkflowSpec:
    id: str
    workers: list[WorkerSpec]


@dataclass(slots=True)
class WorkerResult:
    id: str
    provider: str
    state: WorkerState = WorkerState.PENDING
    output: str = ""
    error: str = ""
    attempts: int = 0

    @classmethod
    def completed(cls, worker_id: str, provider: str, output: str) -> WorkerResult:
        return cls(id=worker_id, provider=provider, state=WorkerState.COMPLETED, output=str(output))

    @classmethod
    def failed(cls, worker_id: str, provider: str, error: str, attempts: int = 0) -> WorkerResult:
        return cls(id=worker_id, provider=provider, state=WorkerState.FAILED, error=str(error), attempts=attempts)

    @classmethod
    def blocked(cls, worker_id: str, provider: str, error: str) -> WorkerResult:
        return cls(id=worker_id, provider=provider, state=WorkerState.BLOCKED, error=str(error))

    def to_dict(self) -> dict[str, object]:
        return {
            "id": self.id,
            "provider": self.provider,
            "state": self.state.value,
            "output": self.output,
            "error": self.error,
            "attempts": self.attempts,
        }

    @classmethod
    def from_dict(cls, data: dict[str, object]) -> WorkerResult:
        state = WorkerState(str(data.get("state", WorkerState.PENDING.value)))
        return cls(
            id=str(data.get("id", "")),
            provider=str(data.get("provider", "default")),
            state=state,
            output=str(data.get("output", "")),
            error=str(data.get("error", "")),
            attempts=int(data.get("attempts", 0) or 0),
        )


@dataclass(slots=True)
class WorkflowRun:
    id: str
    workers: dict[str, WorkerResult]
    state: str = "running"

    def to_dict(self) -> dict[str, object]:
        return {
            "id": self.id,
            "state": self.state,
            "workers": {worker_id: result.to_dict() for worker_id, result in self.workers.items()},
        }

    @classmethod
    def from_dict(cls, data: dict[str, object]) -> WorkflowRun:
        raw_workers = data.get("workers", {})
        workers: dict[str, WorkerResult] = {}
        if isinstance(raw_workers, dict):
            for worker_id, raw_result in raw_workers.items():
                if isinstance(raw_result, dict):
                    workers[str(worker_id)] = WorkerResult.from_dict(raw_result)
        return cls(id=str(data.get("id", "")), state=str(data.get("state", "running")), workers=workers)
