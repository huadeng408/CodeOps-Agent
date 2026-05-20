from __future__ import annotations

from dataclasses import dataclass, field
from datetime import datetime, timezone
from pathlib import Path
import re
import threading


def _now() -> datetime:
    return datetime.now(timezone.utc)


def _parse_datetime(value: str) -> datetime:
    try:
        return datetime.fromisoformat(value)
    except ValueError:
        return _now()


@dataclass(slots=True)
class Memory:
    id: str
    name: str
    content: str
    tags: list[str] = field(default_factory=list)
    created_at: datetime = field(default_factory=_now)
    updated_at: datetime = field(default_factory=_now)


@dataclass(slots=True)
class MemoryStats:
    count: int
    path: str
    index_path: str


class MemoryManager:
    def __init__(self, memory_dir: str) -> None:
        self.memory_dir = Path(memory_dir or ".agent/memory")
        self.memory_dir.mkdir(parents=True, exist_ok=True)
        self.index_file = self.memory_dir / "MEMORY.md"
        self._lock = threading.Lock()
        self._items: list[Memory] = []
        self._load()
        self._write_index()

    def add(self, content: str, tags: list[str] | None = None) -> Memory:
        with self._lock:
            now = _now()
            item = Memory(
                id=now.strftime("%Y%m%dT%H%M%S.%f"),
                name=self._unique_name(content, now),
                content=content.strip(),
                tags=_normalize_tags(tags or []),
                created_at=now,
                updated_at=now,
            )
            self._save_locked(item)
            return item

    def save(self, memory: Memory) -> Memory:
        with self._lock:
            now = _now()
            if not memory.id:
                memory.id = now.strftime("%Y%m%dT%H%M%S.%f")
            if not memory.name:
                memory.name = self._unique_name(memory.content, now)
            else:
                memory.name = _slugify(memory.name)
                if not memory.name:
                    memory.name = self._unique_name(memory.content, now)
            if memory.created_at is None:
                memory.created_at = now
            memory.updated_at = now
            memory.content = memory.content.strip()
            memory.tags = _normalize_tags(memory.tags)
            self._save_locked(memory)
            return memory

    def delete(self, name: str) -> None:
        with self._lock:
            needle = name.strip()
            slug = _slugify(needle)
            for index, item in enumerate(self._items):
                if item.name not in {needle, slug} and item.id != needle:
                    continue
                path = self.memory_dir / f"{item.name}.md"
                path.unlink(missing_ok=True)
                del self._items[index]
                self._write_index()
                return
            raise KeyError(f"memory not found: {name}")

    def get(self, name: str) -> Memory | None:
        needle = name.strip()
        slug = _slugify(needle)
        with self._lock:
            for item in self._items:
                if item.name in {needle, slug} or item.id == needle:
                    return _clone_memory(item)
        return None

    def list(self) -> list[Memory]:
        with self._lock:
            return [_clone_memory(item) for item in self._items]

    def load_relevant(self, context: str) -> list[Memory]:
        tokens = _query_tokens(context)
        with self._lock:
            if not tokens:
                return [_clone_memory(item) for item in self._items]

            matches: list[Memory] = []
            for item in self._items:
                if _matches(item, tokens):
                    matches.append(_clone_memory(item))
            return matches

    def snapshot(self) -> MemoryStats:
        with self._lock:
            return MemoryStats(
                count=len(self._items),
                path=str(self.memory_dir),
                index_path=str(self.index_file),
            )

    def _load(self) -> None:
        items: list[Memory] = []
        for path in self.memory_dir.glob("*.md"):
            if path.name == "MEMORY.md":
                continue
            items.append(_read_memory_file(path))
        self._items = _sort_memories(items)

    def _save_locked(self, item: Memory) -> None:
        _write_memory_file(self.memory_dir / f"{item.name}.md", item)
        for index, existing in enumerate(self._items):
            if existing.name == item.name or existing.id == item.id:
                self._items[index] = item
                break
        else:
            self._items.append(item)
        self._items = _sort_memories(self._items)
        self._write_index()

    def _write_index(self) -> None:
        lines = ["# Memory Index", ""]
        if not self._items:
            lines.append("_No memories saved._")
        else:
            for item in self._items:
                tags = ", ".join(item.tags) or "none"
                updated = item.updated_at.isoformat()
                lines.append(
                    f"- [{item.name}]({item.name}.md) | tags: {tags} | "
                    f"updated: {updated} | summary: {_summary(item.content, 96)}"
                )
        self.index_file.write_text("\n".join(lines) + "\n", encoding="utf-8")

    def _unique_name(self, content: str, now: datetime) -> str:
        base = _slugify(_first_line(content)) or "memory"
        base = base[:48].strip("-") or "memory"
        stamp = now.strftime("%Y%m%dT%H%M%S")
        name = f"{base}-{stamp}"
        suffix = 2
        while (self.memory_dir / f"{name}.md").exists():
            name = f"{base}-{stamp}-{suffix}"
            suffix += 1
        return name


def _read_memory_file(path: Path) -> Memory:
    lines = path.read_text(encoding="utf-8").splitlines()
    if not lines or lines[0].strip() != "---":
        raise ValueError(f"memory file missing frontmatter: {path}")

    meta: dict[str, str] = {}
    body_start = 0
    for index, line in enumerate(lines[1:], start=1):
        if line.strip() == "---":
            body_start = index + 1
            break
        if ":" in line:
            key, value = line.split(":", 1)
            meta[key.strip()] = value.strip()
    if body_start == 0:
        raise ValueError(f"memory file missing closing frontmatter: {path}")
    content = "\n".join(lines[body_start:]).strip()
    return Memory(
        id=meta.get("id", ""),
        name=meta.get("name", path.stem),
        content=content,
        tags=_normalize_tags(meta.get("tags", "").split(",")),
        created_at=_parse_datetime(meta.get("created_at", "")),
        updated_at=_parse_datetime(meta.get("updated_at", "")),
    )


def _write_memory_file(path: Path, item: Memory) -> None:
    body = [
        "---",
        f"id: {item.id}",
        f"name: {item.name}",
        f"tags: {', '.join(item.tags)}",
        f"created_at: {item.created_at.isoformat()}",
        f"updated_at: {item.updated_at.isoformat()}",
        "---",
        item.content.strip(),
    ]
    path.write_text("\n".join(body) + "\n", encoding="utf-8")


def _normalize_tags(tags: list[str]) -> list[str]:
    seen: set[str] = set()
    values: list[str] = []
    for tag in tags:
        value = tag.strip().strip("#").lower()
        if not value or value in seen:
            continue
        seen.add(value)
        values.append(value)
    return values


def _clone_memory(item: Memory) -> Memory:
    return Memory(
        id=item.id,
        name=item.name,
        content=item.content,
        tags=list(item.tags),
        created_at=item.created_at,
        updated_at=item.updated_at,
    )


def _sort_memories(items: list[Memory]) -> list[Memory]:
    return sorted(items, key=lambda item: (item.updated_at, item.name), reverse=True)


def _matches(item: Memory, tokens: list[str]) -> bool:
    haystacks = [
        item.name.lower(),
        item.content.lower(),
        " ".join(item.tags).lower(),
    ]
    for token in tokens:
        for haystack in haystacks:
            if token in haystack:
                return True
    return False


def _query_tokens(query: str) -> list[str]:
    query = query.lower().strip()
    if not query:
        return []
    tokens = [token for token in re.split(r"\W+", query, flags=re.UNICODE) if len(token) >= 2]
    if tokens:
        return tokens
    return [query]


def _first_line(content: str) -> str:
    for line in content.strip().splitlines():
        if line.strip():
            return line.strip()
    return ""


def _summary(content: str, max_chars: int) -> str:
    value = " ".join(content.strip().split()).replace("|", "/")
    if len(value) <= max_chars:
        return value
    return value[: max_chars - 3].strip() + "..."


def _slugify(value: str) -> str:
    value = value.lower().strip()
    value = re.sub(r"[^a-z0-9]+", "-", value)
    value = re.sub(r"-+", "-", value)
    return value.strip("-")
