from __future__ import annotations

import json
from dataclasses import dataclass, field
from datetime import datetime, timezone
from pathlib import Path


def _now() -> datetime:
    return datetime.now(timezone.utc)


def _parse_datetime(value: str) -> datetime:
    return datetime.fromisoformat(value)


@dataclass(slots=True)
class Memory:
    id: str
    content: str
    tags: list[str] = field(default_factory=list)
    created_at: datetime = field(default_factory=_now)
    updated_at: datetime = field(default_factory=_now)


@dataclass(slots=True)
class MemoryStats:
    count: int
    path: str


class MemoryManager:
    def __init__(self, memory_dir: str) -> None:
        self.memory_dir = Path(memory_dir or ".agent/memory")
        self.memory_dir.mkdir(parents=True, exist_ok=True)
        self.file = self.memory_dir / "memory.jsonl"
        self._items: list[Memory] = []
        self._load()

    def add(self, content: str, tags: list[str] | None = None) -> Memory:
        item = Memory(
            id=_now().strftime("%Y%m%dT%H%M%S.%f"),
            content=content,
            tags=list(tags or []),
        )
        self._items.append(item)
        self._append(item)
        return item

    def load_relevant(self, context: str) -> list[Memory]:
        query = context.lower().strip()
        if not query:
            return list(self._items)

        matches: list[Memory] = []
        for item in self._items:
            if query in item.content.lower():
                matches.append(item)
                continue
            if any(query in tag.lower() for tag in item.tags):
                matches.append(item)
        return matches

    def save(self, memory: Memory) -> None:
        self._items.append(memory)
        self._append(memory)

    def snapshot(self) -> MemoryStats:
        return MemoryStats(count=len(self._items), path=str(self.file))

    def _load(self) -> None:
        if not self.file.exists():
            return
        for line in self.file.read_text(encoding="utf-8").splitlines():
            if not line.strip():
                continue
            payload = json.loads(line)
            self._items.append(
                Memory(
                    id=payload["id"],
                    content=payload["content"],
                    tags=list(payload.get("tags", [])),
                    created_at=_parse_datetime(payload["created_at"]),
                    updated_at=_parse_datetime(payload["updated_at"]),
                )
            )

    def _append(self, memory: Memory) -> None:
        payload = {
            "id": memory.id,
            "content": memory.content,
            "tags": memory.tags,
            "created_at": memory.created_at.isoformat(),
            "updated_at": memory.updated_at.isoformat(),
        }
        with self.file.open("a", encoding="utf-8") as handle:
            handle.write(json.dumps(payload, ensure_ascii=False))
            handle.write("\n")
