from __future__ import annotations

from dataclasses import dataclass
import threading


@dataclass(slots=True)
class Todo:
    content: str
    active_form: str
    status: str


class TodoManager:
    def __init__(self) -> None:
        self._lock = threading.Lock()
        self.todos: list[Todo] = []

    def update(self, todos: list[Todo]) -> None:
        normalized: list[Todo] = []
        seen_in_progress = False
        for todo in todos:
            content = todo.content.strip()
            active_form = todo.active_form.strip() or content
            status = todo.status.strip().lower() or "pending"
            if status not in {"pending", "in_progress", "completed"}:
                status = "pending"
            if status == "in_progress":
                if seen_in_progress:
                    status = "pending"
                else:
                    seen_in_progress = True
            normalized.append(
                Todo(
                    content=content,
                    active_form=active_form,
                    status=status,
                )
            )
        with self._lock:
            self.todos = normalized

    def mark_complete(self, index: int) -> None:
        with self._lock:
            self.todos[index].status = "completed"

    def mark_in_progress(self, index: int) -> None:
        with self._lock:
            for todo in self.todos:
                if todo.status == "in_progress":
                    todo.status = "pending"
            self.todos[index].status = "in_progress"

    def snapshot(self) -> list[Todo]:
        with self._lock:
            return list(self.todos)

    def to_lines(self) -> list[str]:
        items = self.snapshot()
        if not items:
            return ["no active tasks"]
        lines = []
        for index, todo in enumerate(items, start=1):
            prefix = {
                "completed": "[x]",
                "in_progress": "[~]",
                "pending": "[ ]",
            }.get(todo.status, "[ ]")
            label = todo.active_form or todo.content
            lines.append(f"{index}. {prefix} {label}")
        return lines
