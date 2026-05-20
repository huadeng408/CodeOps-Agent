from __future__ import annotations

from dataclasses import dataclass


@dataclass(slots=True)
class Todo:
    content: str
    active_form: str
    status: str


class TodoManager:
    def __init__(self) -> None:
        self.todos: list[Todo] = []

    def update(self, todos: list[Todo]) -> None:
        self.todos = list(todos)

    def mark_complete(self, index: int) -> None:
        self.todos[index].status = "completed"

    def mark_in_progress(self, index: int) -> None:
        for todo in self.todos:
            if todo.status == "in_progress":
                todo.status = "pending"
        self.todos[index].status = "in_progress"

    def snapshot(self) -> list[Todo]:
        return list(self.todos)
