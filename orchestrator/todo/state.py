"""Versioned plan and todo state used by one orchestrator run."""

from __future__ import annotations

from collections.abc import Mapping, Sequence
from dataclasses import dataclass
from typing import Any

STATE_SCHEMA_VERSION = 1
_VALID_STATUSES = frozenset({"pending", "in_progress", "completed"})
_VALID_PLAN_MODES = frozenset({"chat", "plan"})


class StateValidationError(ValueError):
    """Raised when a durable plan/todo snapshot is malformed."""


class StateConflictError(ValueError):
    """Raised when a state write uses an obsolete revision."""


@dataclass(frozen=True, slots=True)
class TodoStateItem:
    content: str
    active_form: str
    status: str


@dataclass(frozen=True, slots=True)
class PlanStateSnapshot:
    steps: tuple[str, ...] = ()
    current_index: int = 0
    mode: str = "chat"


@dataclass(frozen=True, slots=True)
class PlanTodoSnapshot:
    revision: int = 0
    plan: PlanStateSnapshot = PlanStateSnapshot()
    todos: tuple[TodoStateItem, ...] = ()
    schema_version: int = STATE_SCHEMA_VERSION

    def to_wire(self) -> dict[str, Any]:
        return {
            "schema_version": self.schema_version,
            "revision": self.revision,
            "plan": {
                "steps": list(self.plan.steps),
                "current_index": self.plan.current_index,
                "mode": self.plan.mode,
            },
            "todos": [
                {
                    "content": item.content,
                    "active_form": item.active_form,
                    "status": item.status,
                }
                for item in self.todos
            ],
        }


def _string(value: Any, field: str) -> str:
    if not isinstance(value, str):
        raise StateValidationError(f"{field} must be a string")
    return value.strip()


def _parse_snapshot(value: PlanTodoSnapshot | Mapping[str, Any] | None) -> PlanTodoSnapshot:
    if value is None:
        return PlanTodoSnapshot()
    if isinstance(value, PlanTodoSnapshot):
        try:
            value = value.to_wire()
        except Exception as exc:
            raise StateValidationError("state snapshot is malformed") from exc
    if not isinstance(value, Mapping):
        raise StateValidationError("state snapshot must be an object")
    schema_version = value.get("schema_version", STATE_SCHEMA_VERSION)
    revision = value.get("revision", 0)
    if not isinstance(schema_version, int) or isinstance(schema_version, bool):
        raise StateValidationError("schema_version must be an integer")
    if schema_version != STATE_SCHEMA_VERSION:
        raise StateValidationError("unsupported state schema_version")
    if not isinstance(revision, int) or isinstance(revision, bool) or revision < 0:
        raise StateValidationError("revision must be a non-negative integer")
    raw_plan = value.get("plan", {})
    if isinstance(raw_plan, PlanStateSnapshot):
        raw_plan = {
            "steps": list(raw_plan.steps),
            "current_index": raw_plan.current_index,
            "mode": raw_plan.mode,
        }
    if not isinstance(raw_plan, Mapping):
        raise StateValidationError("plan must be an object")
    raw_steps = raw_plan.get("steps", [])
    if not isinstance(raw_steps, Sequence) or isinstance(raw_steps, (str, bytes, bytearray)):
        raise StateValidationError("plan.steps must be a list")
    steps = tuple(_string(step, "plan step") for step in raw_steps)
    if any(not step for step in steps):
        raise StateValidationError("plan steps must not be empty")
    current_index = raw_plan.get("current_index", 0)
    if not isinstance(current_index, int) or isinstance(current_index, bool):
        raise StateValidationError("plan.current_index must be an integer")
    if steps and not 0 <= current_index < len(steps):
        raise StateValidationError("plan.current_index is outside steps")
    if not steps and current_index != 0:
        raise StateValidationError("empty plan must use current_index 0")
    mode = _string(raw_plan.get("mode", "chat"), "plan.mode") or "chat"
    if mode not in _VALID_PLAN_MODES:
        raise StateValidationError("plan.mode must be chat or plan")
    raw_todos = value.get("todos", [])
    if not isinstance(raw_todos, Sequence) or isinstance(raw_todos, (str, bytes, bytearray)):
        raise StateValidationError("todos must be a list")
    todos: list[TodoStateItem] = []
    seen: set[str] = set()
    for raw in raw_todos:
        if isinstance(raw, TodoStateItem):
            raw = {
                "content": raw.content,
                "active_form": raw.active_form,
                "status": raw.status,
            }
        if not isinstance(raw, Mapping):
            raise StateValidationError("todo item must be an object")
        content = _string(raw.get("content", ""), "todo.content")
        if not content:
            raise StateValidationError("todo.content must not be empty")
        key = content.casefold()
        if key in seen:
            raise StateValidationError("duplicate todo content")
        seen.add(key)
        active_form = _string(raw.get("active_form", ""), "todo.active_form") or content
        status = _string(raw.get("status", "pending"), "todo.status") or "pending"
        if status not in _VALID_STATUSES:
            raise StateValidationError("todo.status is invalid")
        todos.append(TodoStateItem(content, active_form, status))
    snapshot = PlanTodoSnapshot(
        revision=revision,
        plan=PlanStateSnapshot(steps, current_index, mode),
        todos=tuple(todos),
        schema_version=schema_version,
    )
    if snapshot.schema_version != STATE_SCHEMA_VERSION:
        raise StateValidationError("unsupported state schema_version")
    if snapshot.revision < 0:
        raise StateValidationError("revision must be non-negative")
    return snapshot


class PlanTodoStateMachine:
    """Own one request's validated, revisioned whole-value state."""

    def __init__(
        self,
        snapshot: PlanTodoSnapshot | Mapping[str, Any] | None = None,
        *,
        allow_parallel: bool = False,
    ) -> None:
        self._snapshot = _parse_snapshot(snapshot)
        self._allow_parallel = bool(allow_parallel)
        self._validate_parallel(self._snapshot.todos)

    @classmethod
    def from_wire(
        cls,
        value: PlanTodoSnapshot | Mapping[str, Any],
        *,
        allow_parallel: bool = False,
    ) -> "PlanTodoStateMachine":
        return cls(value, allow_parallel=allow_parallel)

    def snapshot(self) -> PlanTodoSnapshot:
        return self._snapshot

    def replace_plan(
        self,
        steps: Sequence[str],
        *,
        current_index: int = 0,
        mode: str = "plan",
        expected_revision: int | None = None,
    ) -> PlanTodoSnapshot:
        self._check_revision(expected_revision)
        if isinstance(steps, (str, bytes, bytearray)):
            raise StateValidationError("plan steps must be a list")
        clean_steps = tuple(_string(step, "plan step") for step in steps)
        if not clean_steps or any(not step for step in clean_steps):
            raise StateValidationError("plan steps must not be empty")
        if not isinstance(current_index, int) or isinstance(current_index, bool):
            raise StateValidationError("plan.current_index must be an integer")
        if not 0 <= current_index < len(clean_steps):
            raise StateValidationError("plan.current_index is outside steps")
        clean_mode = _string(mode, "plan.mode") or "chat"
        if clean_mode not in _VALID_PLAN_MODES:
            raise StateValidationError("plan.mode must be chat or plan")
        self._snapshot = PlanTodoSnapshot(
            revision=self._snapshot.revision + 1,
            plan=PlanStateSnapshot(clean_steps, current_index, clean_mode),
            todos=self._snapshot.todos,
        )
        return self._snapshot

    def replace_todos(
        self,
        todos: Sequence[TodoStateItem | Mapping[str, Any]],
        *,
        allow_parallel: bool | None = None,
        expected_revision: int | None = None,
    ) -> PlanTodoSnapshot:
        self._check_revision(expected_revision)
        raw = {
            "schema_version": STATE_SCHEMA_VERSION,
            "revision": self._snapshot.revision,
            "plan": {
                "steps": list(self._snapshot.plan.steps),
                "current_index": self._snapshot.plan.current_index,
                "mode": self._snapshot.plan.mode,
            },
            "todos": list(todos),
        }
        candidate = _parse_snapshot(raw)
        self._validate_parallel(candidate.todos, allow_parallel=allow_parallel)
        self._snapshot = PlanTodoSnapshot(
            revision=self._snapshot.revision + 1,
            plan=self._snapshot.plan,
            todos=candidate.todos,
        )
        return self._snapshot

    def prompt_context(self) -> str:
        plan = self._snapshot.plan
        lines = [
            f"Plan/todo state revision: {self._snapshot.revision}",
            f"Plan mode: {plan.mode}",
        ]
        if plan.steps:
            lines.append(f"Plan step: {plan.current_index + 1}/{len(plan.steps)}: {plan.steps[plan.current_index]}")
        if self._snapshot.todos:
            lines.append("Todos:")
            lines.extend(
                f"- [{item.status}] {item.active_form or item.content}" for item in self._snapshot.todos
            )
        else:
            lines.append("Todos: none")
        return "\n".join(lines)

    def _check_revision(self, expected_revision: int | None) -> None:
        if expected_revision is not None and expected_revision != self._snapshot.revision:
            raise StateConflictError(
                f"state revision conflict: expected {expected_revision}, current {self._snapshot.revision}"
            )

    def _validate_parallel(
        self,
        todos: Sequence[TodoStateItem],
        *,
        allow_parallel: bool | None = None,
    ) -> None:
        active = sum(item.status == "in_progress" for item in todos)
        if not (self._allow_parallel if allow_parallel is None else bool(allow_parallel)) and active > 1:
            raise StateValidationError("only one todo may be in_progress")


__all__ = [
    "PlanStateSnapshot",
    "PlanTodoSnapshot",
    "PlanTodoStateMachine",
    "StateConflictError",
    "StateValidationError",
    "TodoStateItem",
]
