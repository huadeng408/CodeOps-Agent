from __future__ import annotations

from collections.abc import Callable
from dataclasses import dataclass, field
from datetime import datetime, timezone
from pathlib import Path
import hashlib
import json
import re
import threading

from orchestrator.security.credentials import redact_credential_shapes

_SENSITIVE_NAME = re.compile(
    r"^(?:(?:openai|anthropic|deepseek|azure|aws|github|gitlab|db|database|mysql|postgres|redis)[_-])?"
    r"(?:api[_-]?key|access[_-]?token|refresh[_-]?token|authorization(?:[_-]?token)?|private[_-]?key|password|passwd|secret|credential|credentials)"
    r"(?:$|[_-](?:token|key|value|credential|credentials))",
    re.IGNORECASE,
)


def _now() -> datetime:
    return datetime.now(timezone.utc)


def _normalize_datetime(value: datetime | str | None) -> datetime:
    """Return the UTC instant used by both the file format and checksum."""
    if isinstance(value, str):
        return _parse_datetime(value)
    if not isinstance(value, datetime):
        return _now()
    if value.tzinfo is None:
        value = value.replace(tzinfo=timezone.utc)
    return value.astimezone(timezone.utc)


def _format_datetime(value: datetime) -> str:
    """Format an instant like Go's RFC3339Nano, using a canonical ``Z``."""
    text = _normalize_datetime(value).isoformat(timespec="microseconds")
    if "." in text:
        head, fraction_and_zone = text.split(".", 1)
        fraction, zone = fraction_and_zone.split("+", 1)
        fraction = fraction.rstrip("0")
        text = head + (f".{fraction}" if fraction else "") + "+" + zone
    return text.replace("+00:00", "Z")


def _parse_datetime(value: str) -> datetime:
    text = str(value or "").strip()
    if not text:
        return _now()
    if text.endswith(("Z", "z")):
        text = text[:-1] + "+00:00"
    try:
        return _normalize_datetime(datetime.fromisoformat(text))
    except (TypeError, ValueError):
        return _now()


def _snapshot_files(
    paths: tuple[Path, ...] | list[Path] | set[Path],
) -> dict[Path, bytes | None]:
    """Capture exact file bytes/existence for a small filesystem transaction."""
    snapshot: dict[Path, bytes | None] = {}
    for path in set(paths):
        try:
            snapshot[path] = path.read_bytes()
        except FileNotFoundError:
            snapshot[path] = None
    return snapshot


def _restore_files(snapshot: dict[Path, bytes | None]) -> None:
    """Restore a snapshot, including removing files created by a failed write."""
    for path, data in snapshot.items():
        if data is None:
            path.unlink(missing_ok=True)
            continue
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_bytes(data)


@dataclass(slots=True)
class Memory:
    id: str
    name: str
    content: str
    tags: list[str] = field(default_factory=list)
    # Keep these two fields in their historical positional slots.  New
    # metadata belongs after them so existing callers remain source-compatible.
    created_at: datetime = field(default_factory=_now)
    updated_at: datetime = field(default_factory=_now)
    namespace: str = "user"
    kind: str = "default"
    detail: str = "full"
    source_uri: str = ""
    source_checksum: str = ""
    session_id: str = ""
    checksum: str = ""


@dataclass(slots=True)
class MemoryStats:
    count: int
    path: str
    index_path: str


@dataclass(slots=True)
class RecallOptions:
    namespace: str = ""
    kind: str = ""
    detail: str = ""
    limit: int = 0
    max_tokens: int = 0


@dataclass(slots=True)
class RecallEntry:
    memory: Memory
    score: int
    estimated_tokens: int


@dataclass(slots=True)
class RecallStats:
    candidates: int
    returned: int
    dropped: int
    max_tokens: int
    used_tokens: int


@dataclass(slots=True)
class RecallResult:
    entries: list[RecallEntry]
    stats: RecallStats


@dataclass(frozen=True, slots=True)
class MemoryEvent:
    action: str
    memory_id: str
    memory_name: str
    namespace: str
    kind: str
    detail: str
    session_id: str
    source_checksum: str
    memory_checksum: str
    occurred_at: datetime


class MemoryManager:
    def __init__(
        self,
        memory_dir: str,
        audit_sink: Callable[[MemoryEvent], None] | None = None,
    ) -> None:
        self.memory_dir = Path(memory_dir or ".agent/memory")
        self.memory_dir.mkdir(parents=True, exist_ok=True)
        self.index_file = self.memory_dir / "MEMORY.md"
        self._lock = threading.Lock()
        self._audit_sink = audit_sink
        self._items: list[Memory] = []
        self._load()
        self._write_index()

    def add(self, content: str, tags: list[str] | None = None) -> Memory:
        with self._lock:
            now = _now()
            safe_content, safe_tags = _validate_memory_fields("", content, tags or [])
            item = Memory(
                id=now.strftime("%Y%m%dT%H%M%S.%f"),
                name=self._unique_name(safe_content, now),
                content=safe_content,
                tags=safe_tags,
                created_at=now,
                updated_at=now,
            )
            item = _normalize_memory(item)
            item.checksum = _memory_checksum(item)
            return self._save_locked(item)

    def save(self, memory: Memory) -> Memory:
        with self._lock:
            # Work on a detached value so a failed transaction cannot leave a
            # caller-owned Memory partially normalized or timestamp-mutated.
            memory = _clone_memory(memory)
            now = _now()
            safe_content, safe_tags = _validate_memory_fields(
                memory.name, memory.content, memory.tags
            )
            if not memory.id:
                memory.id = now.strftime("%Y%m%dT%H%M%S.%f")
            if not memory.name:
                memory.name = self._unique_name(safe_content, now)
            else:
                memory.name = _slugify(memory.name)
                if not memory.name:
                    memory.name = self._unique_name(safe_content, now)
            if memory.created_at is None:
                memory.created_at = now
            memory.updated_at = now
            memory.content = safe_content
            memory.tags = safe_tags
            memory = _normalize_memory(memory)
            memory.checksum = _memory_checksum(memory)
            return self._save_locked(memory)

    def delete(self, name: str) -> None:
        with self._lock:
            needle = name.strip()
            slug = _slugify(needle)
            for index, item in enumerate(self._items):
                if item.name not in {needle, slug} and item.id != needle:
                    continue
                path = self.memory_dir / f"{item.name}.md"
                old_items = [_clone_memory(existing) for existing in self._items]
                snapshot = _snapshot_files((path, self.index_file))
                try:
                    path.unlink(missing_ok=True)
                    self._items = [
                        existing
                        for position, existing in enumerate(self._items)
                        if position != index
                    ]
                    self._write_index()
                    # Emit only after all local writes have prepared
                    # successfully.  A sink error restores the full snapshot.
                    self._emit_audit("delete", item)
                except BaseException:
                    self._items = old_items
                    _restore_files(snapshot)
                    raise
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
        try:
            return [entry.memory for entry in self.recall(context).entries]
        except Exception:
            # The legacy API has no error channel; fail closed rather than
            # allowing malformed filters or a corrupted in-memory state to
            # become prompt context.
            return []

    def recall(
        self, query: str, options: RecallOptions | None = None
    ) -> RecallResult:
        options = options or RecallOptions()
        namespace = str(options.namespace or "").strip().lower()
        kind = str(options.kind or "").strip().lower()
        detail = str(options.detail or "").strip().lower()
        if options.limit < 0:
            raise ValueError("memory recall limit must not be negative")
        if options.max_tokens < 0:
            raise ValueError("memory recall token budget must not be negative")
        if namespace and not _MEMORY_SCOPE.fullmatch(namespace):
            raise ValueError("invalid memory recall namespace")
        if kind and not _MEMORY_SCOPE.fullmatch(kind):
            raise ValueError("invalid memory recall kind")
        if detail and detail not in {"abstract", "overview", "full"}:
            raise ValueError("invalid memory recall detail")
        with self._lock:
            items = [_clone_memory(item) for item in self._items]

        tokens = _query_tokens(query)
        candidates: list[tuple[Memory, int]] = []
        for item in items:
            if namespace and item.namespace != namespace:
                continue
            if kind and item.kind != kind:
                continue
            if detail and item.detail != detail:
                continue
            score = _score_memory(item, query, tokens)
            if tokens and score == 0:
                continue
            candidates.append((item, score))
        candidates.sort(
            key=lambda value: (
                -value[1],
                -value[0].updated_at.timestamp(),
                value[0].name,
            )
        )

        entries: list[RecallEntry] = []
        used_tokens = 0
        for item, score in candidates:
            if options.limit and len(entries) >= options.limit:
                break
            estimated = _estimate_memory_tokens(item)
            if options.max_tokens and used_tokens + estimated > options.max_tokens:
                continue
            entries.append(
                RecallEntry(
                    memory=item,
                    score=score,
                    estimated_tokens=estimated,
                )
            )
            used_tokens += estimated
        return RecallResult(
            entries=entries,
            stats=RecallStats(
                candidates=len(candidates),
                returned=len(entries),
                dropped=len(candidates) - len(entries),
                max_tokens=options.max_tokens,
                used_tokens=used_tokens,
            ),
        )

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

    def _save_locked(self, item: Memory) -> Memory:
        item = _normalize_memory(_clone_memory(item))
        item.checksum = _memory_checksum(item)
        stale_paths = {
            self.memory_dir / f"{existing.name}.md"
            for existing in self._items
            if existing.id == item.id and existing.name != item.name
        }
        target = self.memory_dir / f"{item.name}.md"
        old_items = [_clone_memory(existing) for existing in self._items]
        snapshot = _snapshot_files((*stale_paths, target, self.index_file))
        try:
            _write_memory_file(target, item)
            for path in stale_paths:
                path.unlink(missing_ok=True)
            next_items = [
                existing
                for existing in self._items
                if existing.name != item.name and existing.id != item.id
            ]
            next_items.append(item)
            self._items = _sort_memories(next_items)
            self._write_index()
            # Audit is the commit gate: if the Session Ledger sink rejects the
            # event, restore files and the prior in-memory projection.
            self._emit_audit("save", item)
        except BaseException:
            self._items = old_items
            _restore_files(snapshot)
            raise
        return _clone_memory(item)

    def _emit_audit(self, action: str, item: Memory) -> None:
        if self._audit_sink is None:
            return
        self._audit_sink(
            MemoryEvent(
                action=action,
                memory_id=item.id,
                memory_name=item.name,
                namespace=item.namespace,
                kind=item.kind,
                detail=item.detail,
                session_id=item.session_id,
                source_checksum=item.source_checksum,
                memory_checksum=item.checksum,
                occurred_at=_now(),
            )
        )

    def _write_index(self) -> None:
        lines = ["# Memory Index", ""]
        if not self._items:
            lines.append("_No memories saved._")
        else:
            for item in self._items:
                tags = ", ".join(item.tags) or "none"
                updated = _format_datetime(item.updated_at)
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
    name = meta.get("name", path.stem)
    content = "\n".join(lines[body_start:]).strip()
    content, tags = _validate_memory_fields(
        name, content, meta.get("tags", "").split(",")
    )
    item = Memory(
        id=meta.get("id", ""),
        name=name,
        content=content,
        tags=tags,
        namespace=meta.get("namespace", "user"),
        kind=meta.get("kind", "default"),
        detail=meta.get("detail", "full"),
        source_uri=meta.get("source_uri", ""),
        source_checksum=meta.get("source_checksum", ""),
        session_id=meta.get("session_id", ""),
        checksum=meta.get("checksum", ""),
        created_at=_parse_datetime(meta.get("created_at", "")),
        updated_at=_parse_datetime(meta.get("updated_at", "")),
    )
    item = _normalize_memory(item)
    expected = _memory_checksum(item)
    if item.checksum and item.checksum != expected:
        raise ValueError(f"memory checksum mismatch: {path}")
    item.checksum = expected
    return item


def _write_memory_file(path: Path, item: Memory) -> None:
    body = [
        "---",
        f"id: {item.id}",
        f"name: {item.name}",
        f"tags: {', '.join(item.tags)}",
        f"namespace: {item.namespace}",
        f"kind: {item.kind}",
        f"detail: {item.detail}",
    ]
    if item.source_uri:
        body.extend(
            [
                f"source_uri: {item.source_uri}",
                f"source_checksum: {item.source_checksum}",
            ]
        )
    if item.session_id:
        body.append(f"session_id: {item.session_id}")
    body.extend([
        f"checksum: {item.checksum}",
        f"created_at: {_format_datetime(item.created_at)}",
        f"updated_at: {_format_datetime(item.updated_at)}",
        "---",
        item.content.strip(),
    ])
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


def _validate_memory_fields(
    name: str, content: str, tags: list[str]
) -> tuple[str, list[str]]:
    safe_name = str(name).strip()
    if _is_sensitive_memory_name(safe_name) or redact_credential_shapes(safe_name) != safe_name:
        raise ValueError("sensitive name is not allowed in memory")
    safe_content = str(content).strip()
    if redact_credential_shapes(safe_content) != safe_content:
        raise ValueError("sensitive content is not allowed in memory")
    normalized_tags = _normalize_tags(tags)
    if any(
        _is_sensitive_memory_name(tag) or redact_credential_shapes(tag) != tag
        for tag in normalized_tags
    ):
        raise ValueError("sensitive tags are not allowed in memory")
    return safe_content, normalized_tags


def _is_sensitive_memory_name(value: str) -> bool:
    return bool(_SENSITIVE_NAME.fullmatch(str(value).strip()))


def _clone_memory(item: Memory) -> Memory:
    return Memory(
        id=item.id,
        name=item.name,
        content=item.content,
        tags=list(item.tags),
        namespace=item.namespace,
        kind=item.kind,
        detail=item.detail,
        source_uri=item.source_uri,
        source_checksum=item.source_checksum,
        session_id=item.session_id,
        checksum=item.checksum,
        created_at=item.created_at,
        updated_at=item.updated_at,
    )


_MEMORY_SCOPE = re.compile(r"^[a-z0-9]+(?:[._/-][a-z0-9]+)*$")
_SHA256 = re.compile(r"^[0-9a-f]{64}$")


def _normalize_memory(item: Memory) -> Memory:
    item.content, item.tags = _validate_memory_fields(item.name, item.content, item.tags)
    item.namespace = str(item.namespace or "").strip().lower() or "user"
    item.kind = str(item.kind or "").strip().lower() or "default"
    item.detail = str(item.detail or "").strip().lower() or "full"
    item.source_uri = str(item.source_uri or "").strip()
    item.source_checksum = str(item.source_checksum or "").strip().lower()
    item.session_id = str(item.session_id or "").strip()
    item.checksum = str(item.checksum or "").strip().lower()
    item.created_at = _normalize_datetime(item.created_at)
    item.updated_at = _normalize_datetime(item.updated_at)
    if not _MEMORY_SCOPE.fullmatch(item.namespace) or not _MEMORY_SCOPE.fullmatch(item.kind):
        raise ValueError("invalid memory namespace or kind")
    if item.detail not in {"abstract", "overview", "full"}:
        raise ValueError("invalid memory detail")
    if item.source_checksum and not _SHA256.fullmatch(item.source_checksum):
        raise ValueError("invalid memory source checksum")
    if bool(item.source_uri) != bool(item.source_checksum):
        raise ValueError("memory source URI and checksum must be provided together")
    for value in (item.namespace, item.kind, item.source_uri, item.session_id):
        if any(character in value for character in "\r\n\t") or redact_credential_shapes(value) != value:
            raise ValueError("sensitive or invalid memory metadata is not allowed")
    if item.checksum and not _SHA256.fullmatch(item.checksum):
        raise ValueError("invalid memory checksum")
    return item


def _memory_checksum(item: Memory) -> str:
    payload = {
        "id": item.id,
        "name": item.name,
        "content": item.content,
        "tags": item.tags,
        "namespace": item.namespace,
        "kind": item.kind,
        "detail": item.detail,
        "source_uri": item.source_uri,
        "source_checksum": item.source_checksum,
        "session_id": item.session_id,
        "created_at": _format_datetime(item.created_at),
        "updated_at": _format_datetime(item.updated_at),
    }
    canonical = json.dumps(payload, ensure_ascii=False, sort_keys=True, separators=(",", ":"))
    return hashlib.sha256(canonical.encode("utf-8")).hexdigest()


def _sort_memories(items: list[Memory]) -> list[Memory]:
    return sorted(items, key=lambda item: (-item.updated_at.timestamp(), item.name))


def _score_memory(item: Memory, query: str, tokens: list[str]) -> int:
    phrase = query.strip().lower()
    name = re.sub(r"[-_./]+", " ", item.name.lower())
    tags = " ".join(item.tags).lower()
    metadata = " ".join(
        (item.namespace, item.kind, item.detail, item.source_uri, item.session_id)
    ).lower()
    content = item.content.lower()
    score = 0
    if phrase:
        for value, weight in ((name, 24), (tags, 16), (metadata, 8), (content, 4)):
            if phrase in value:
                score += weight
    for token in tokens:
        for value, weight in ((name, 6), (tags, 4), (metadata, 2), (content, 1)):
            if token in value:
                score += weight
    return score


def _estimate_memory_tokens(item: Memory) -> int:
    text = "\n".join(
        (
            item.name,
            item.content,
            " ".join(item.tags),
            item.namespace,
            item.kind,
            item.detail,
            item.source_uri,
        )
    )
    return max(1, (len(text.encode("utf-8")) + 3) // 4)


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
