"""Event-sourced context and long-term memory for durable agent runs.

The module deliberately keeps the persistence boundary small.  Events are
append-only and can be replayed after a process restart; context loading is a
read model that exposes cheap summaries first and raw files only on demand.
"""

from __future__ import annotations

import hashlib
import json
import os
import re
import sqlite3
import threading
import uuid
from collections.abc import Iterable
from contextlib import contextmanager
from dataclasses import dataclass
from datetime import UTC, datetime
from pathlib import Path
from typing import Any

from orchestrator.security.credentials import redact_credential_text

_EVENT_KINDS = {
    "plan",
    "tool_call",
    "file_diff",
    "execution_result",
    "reflection",
    "compaction/start",
    "compaction/summary",
    "compaction/end",
    "tool_result/prune",
    "actor/authorized",
}
_SKIP_DIRS = {
    ".git",
    ".agent",
    ".pytest_cache",
    "__pycache__",
    ".venv",
    "node_modules",
    "eval_results",
}
_SENSITIVE_KEY = re.compile(
    r"(?:api[_-]?key|token|password|passwd|secret|authorization|private[_-]?key)",
    re.IGNORECASE,
)
_SENSITIVE_FILES = {".env", ".env.local", ".env.production", "credentials.json", "id_rsa", "id_ed25519"}
_READ_CHUNK_BYTES = 64 * 1024
_MAX_EVENT_VALUE_CHARS = 16_000
_MAX_EVENT_COLLECTION_ITEMS = 256


def _now() -> str:
    return datetime.now(UTC).isoformat()


def _estimate_tokens(value: str) -> int:
    # A deterministic lower-bound proxy works for providers that do not expose
    # a tokenizer and is used only for relative before/after comparisons.
    return max(1, (len(value.encode("utf-8")) + 3) // 4)


def _redact_text(value: str) -> str:
    return redact_credential_text(value)


def _redact(value: Any, key: str = "") -> Any:
    if _SENSITIVE_KEY.search(key):
        return "[REDACTED]"
    if isinstance(value, dict):
        return {
            str(name): _redact(item, str(name))
            for name, item in list(value.items())[:_MAX_EVENT_COLLECTION_ITEMS]
        }
    if isinstance(value, list):
        return [_redact(item, key) for item in value[:_MAX_EVENT_COLLECTION_ITEMS]]
    if isinstance(value, tuple):
        return [_redact(item, key) for item in value[:_MAX_EVENT_COLLECTION_ITEMS]]
    if isinstance(value, str):
        safe = _redact_text(value)
        if len(safe) > _MAX_EVENT_VALUE_CHARS:
            return safe[:_MAX_EVENT_VALUE_CHARS] + "\n[event value truncated]"
        return safe
    return value


def _session_id(value: str) -> str:
    session_id = value.strip()
    if not session_id:
        raise ValueError("session_id must not be empty")
    if len(session_id) > 256 or _redact_text(session_id) != session_id:
        raise ValueError("session_id contains sensitive or invalid content")
    return session_id


def _canonical_payload(
    event_id: str,
    kind: str,
    session_id: str,
    payload: dict[str, Any],
    created_at: str,
    previous_checksum: str,
) -> str:
    return json.dumps(
        {
            "event_id": event_id,
            "kind": kind,
            "session_id": session_id,
            "payload": payload,
            "created_at": created_at,
            "previous_checksum": previous_checksum,
        },
        ensure_ascii=False,
        sort_keys=True,
        separators=(",", ":"),
    )


def _canonical_memory(
    memory_id: str,
    session_id: str,
    content: str,
    tags: tuple[str, ...],
    created_at: str,
) -> str:
    return json.dumps(
        {
            "id": memory_id,
            "session_id": session_id,
            "content": content,
            "tags": tags,
            "created_at": created_at,
        },
        ensure_ascii=False,
        sort_keys=True,
        separators=(",", ":"),
    )


@dataclass(frozen=True, slots=True)
class ContextEvent:
    event_id: str
    session_id: str
    sequence: int
    kind: str
    payload: dict[str, Any]
    created_at: str
    checksum: str
    previous_checksum: str


@dataclass(frozen=True, slots=True)
class LongTermMemory:
    id: str
    session_id: str
    content: str
    tags: tuple[str, ...]
    created_at: str
    checksum: str


@dataclass(frozen=True, slots=True)
class ContextSnapshot:
    p0: str
    p1: str
    p3: dict[str, str]
    events_text: str
    estimated_input_tokens: int
    estimated_baseline_tokens: int


class SQLiteContextStore:
    """Append-only event and memory store with checksum-verified reads."""

    def __init__(self, path: str | Path) -> None:
        self.path = Path(path)
        self.path.parent.mkdir(parents=True, exist_ok=True)
        self._connection = sqlite3.connect(self.path, timeout=30.0, check_same_thread=False)
        self._connection.row_factory = sqlite3.Row
        self._lock = threading.RLock()
        with self._connection:
            self._connection.execute("PRAGMA busy_timeout = 30000")
            self._connection.execute("PRAGMA journal_mode = WAL")
            self._connection.execute("PRAGMA synchronous = NORMAL")
            self._connection.executescript(
                """
                CREATE TABLE IF NOT EXISTS context_events (
                    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
                    event_id TEXT,
                    session_id TEXT NOT NULL,
                    kind TEXT NOT NULL,
                    payload_json TEXT NOT NULL,
                    created_at TEXT NOT NULL,
                    checksum TEXT NOT NULL UNIQUE,
                    previous_checksum TEXT NOT NULL DEFAULT ''
                );
                CREATE INDEX IF NOT EXISTS context_events_session_sequence
                    ON context_events(session_id, sequence);
                CREATE TABLE IF NOT EXISTS long_term_memory (
                    id TEXT PRIMARY KEY,
                    session_id TEXT NOT NULL,
                    content TEXT NOT NULL,
                    tags_json TEXT NOT NULL,
                    created_at TEXT NOT NULL,
                    checksum TEXT NOT NULL UNIQUE
                );
                CREATE INDEX IF NOT EXISTS long_term_memory_session
                    ON long_term_memory(session_id, created_at);
                """
            )
            columns = {
                str(row["name"])
                for row in self._connection.execute("PRAGMA table_info(context_events)")
            }
            if "event_id" not in columns:
                self._connection.execute("ALTER TABLE context_events ADD COLUMN event_id TEXT")
            if "previous_checksum" not in columns:
                self._connection.execute(
                    "ALTER TABLE context_events ADD COLUMN previous_checksum TEXT NOT NULL DEFAULT ''"
                )
            self._connection.execute(
                "CREATE UNIQUE INDEX IF NOT EXISTS context_events_event_id "
                "ON context_events(event_id) WHERE event_id IS NOT NULL"
            )

    def append(self, session_id: str, kind: str, payload: dict[str, Any]) -> ContextEvent:
        session_id = _session_id(session_id)
        kind = kind.strip()
        if kind not in _EVENT_KINDS:
            raise ValueError(f"unsupported context event kind: {kind}")
        safe_payload = _redact(payload)
        created_at = _now()
        with self._lock, self._write_transaction():
            return self._append_locked(session_id, kind, safe_payload, created_at)

    def events(self, session_id: str, limit: int | None = None) -> list[ContextEvent]:
        parameters: tuple[object, ...]
        if limit is None:
            query = (
                "SELECT sequence, event_id, session_id, kind, payload_json, created_at, "
                "checksum, previous_checksum FROM context_events "
                "WHERE session_id = ? ORDER BY sequence"
            )
            parameters = (session_id,)
        else:
            query = (
                "SELECT * FROM (SELECT sequence, event_id, session_id, kind, payload_json, "
                "created_at, checksum, previous_checksum FROM context_events "
                "WHERE session_id = ? ORDER BY sequence DESC LIMIT ?) ORDER BY sequence"
            )
            parameters = (session_id, max(1, int(limit)))
        with self._lock:
            rows = self._connection.execute(query, parameters).fetchall()
            anchor_checksum = ""
            if rows:
                anchor = self._connection.execute(
                    "SELECT checksum FROM context_events WHERE session_id = ? AND sequence < ? "
                    "ORDER BY sequence DESC LIMIT 1",
                    (session_id, int(rows[0]["sequence"])),
                ).fetchone()
                anchor_checksum = str(anchor["checksum"]) if anchor else ""
        result: list[ContextEvent] = []
        for index, row in enumerate(rows):
            payload = json.loads(str(row["payload_json"]))
            if not isinstance(payload, dict):
                raise TypeError(f"invalid context payload at sequence {row['sequence']}")
            event_id = str(row["event_id"] or "")
            previous_checksum = str(row["previous_checksum"] or "")
            if event_id:
                canonical = _canonical_payload(
                    event_id,
                    str(row["kind"]),
                    str(row["session_id"]),
                    payload,
                    str(row["created_at"]),
                    previous_checksum,
                )
            else:
                canonical = json.dumps(
                    {
                        "kind": str(row["kind"]),
                        "session_id": str(row["session_id"]),
                        "payload": payload,
                        "created_at": str(row["created_at"]),
                    },
                    ensure_ascii=False,
                    sort_keys=True,
                    separators=(",", ":"),
                )
            expected = hashlib.sha256(canonical.encode("utf-8")).hexdigest()
            if expected != str(row["checksum"]):
                raise ValueError(f"context event checksum mismatch at sequence {row['sequence']}")
            if index == 0 and event_id and previous_checksum != anchor_checksum:
                raise ValueError(f"context event chain mismatch at sequence {row['sequence']}")
            if index and event_id and previous_checksum != str(rows[index - 1]["checksum"]):
                raise ValueError(f"context event chain mismatch at sequence {row['sequence']}")
            result.append(
                ContextEvent(
                    event_id=event_id,
                    session_id=str(row["session_id"]),
                    sequence=int(row["sequence"]),
                    kind=str(row["kind"]),
                    payload=payload,
                    created_at=str(row["created_at"]),
                    checksum=str(row["checksum"]),
                    previous_checksum=previous_checksum,
                )
            )
        return result

    def _append_locked(
        self,
        session_id: str,
        kind: str,
        safe_payload: dict[str, Any],
        created_at: str,
    ) -> ContextEvent:
        row = self._connection.execute(
            "SELECT checksum FROM context_events WHERE session_id = ? ORDER BY sequence DESC LIMIT 1",
            (session_id,),
        ).fetchone()
        previous_checksum = str(row["checksum"]) if row else ""
        event_id = uuid.uuid4().hex
        encoded = _canonical_payload(
            event_id,
            kind,
            session_id,
            safe_payload,
            created_at,
            previous_checksum,
        )
        checksum = hashlib.sha256(encoded.encode("utf-8")).hexdigest()
        cursor = self._connection.execute(
            "INSERT INTO context_events(event_id, session_id, kind, payload_json, created_at, "
            "checksum, previous_checksum) VALUES (?, ?, ?, ?, ?, ?, ?)",
            (
                event_id,
                session_id,
                kind,
                json.dumps(safe_payload, ensure_ascii=False, sort_keys=True),
                created_at,
                checksum,
                previous_checksum,
            ),
        )
        return ContextEvent(
            event_id,
            session_id,
            int(cursor.lastrowid),
            kind,
            safe_payload,
            created_at,
            checksum,
            previous_checksum,
        )

    @contextmanager
    def _write_transaction(self):
        self._connection.execute("BEGIN IMMEDIATE")
        try:
            yield
        except BaseException:
            self._connection.rollback()
            raise
        else:
            self._connection.commit()

    def add_memory(self, session_id: str, content: str, tags: Iterable[str] = ()) -> LongTermMemory:
        session_id = _session_id(session_id)
        safe_content = _redact_text(content.strip())
        if safe_content != content.strip():
            raise ValueError("sensitive content is not allowed in long-term memory")
        if not safe_content:
            raise ValueError("memory content must not be empty")
        normalized = tuple(dict.fromkeys(tag.strip().lower().lstrip("#") for tag in tags if tag.strip()))
        if any(_redact_text(tag) != tag for tag in normalized):
            raise ValueError("sensitive tags are not allowed in long-term memory")
        created_at = _now()
        memory_id = uuid.uuid4().hex
        checksum = hashlib.sha256(
            _canonical_memory(
                memory_id, session_id, safe_content, normalized, created_at
            ).encode("utf-8")
        ).hexdigest()
        with self._lock, self._write_transaction():
            self._connection.execute(
                "INSERT INTO long_term_memory(id, session_id, content, tags_json, created_at, checksum) VALUES (?, ?, ?, ?, ?, ?)",
                (memory_id, session_id, safe_content, json.dumps(normalized), created_at, checksum),
            )
        return LongTermMemory(memory_id, session_id, safe_content, normalized, created_at, checksum)

    def add_reflection(
        self, session_id: str, content: str, tags: Iterable[str] = ()
    ) -> LongTermMemory:
        session_id = _session_id(session_id)
        safe_content = _redact_text(content.strip())
        if not safe_content:
            raise ValueError("memory content must not be empty")
        if safe_content != content.strip():
            raise ValueError("sensitive content is not allowed in long-term memory")
        normalized = tuple(dict.fromkeys(tag.strip().lower().lstrip("#") for tag in tags if tag.strip()))
        if any(_redact_text(tag) != tag for tag in normalized):
            raise ValueError("sensitive tags are not allowed in long-term memory")
        memory_id = uuid.uuid4().hex
        created_at = _now()
        checksum = hashlib.sha256(
            _canonical_memory(
                memory_id, session_id, safe_content, normalized, created_at
            ).encode("utf-8")
        ).hexdigest()
        with self._lock, self._write_transaction():
            self._connection.execute(
                "INSERT INTO long_term_memory(id, session_id, content, tags_json, created_at, checksum) "
                "VALUES (?, ?, ?, ?, ?, ?)",
                (memory_id, session_id, safe_content, json.dumps(normalized), created_at, checksum),
            )
            self._append_locked(
                session_id,
                "reflection",
                {"memory_id": memory_id, "tags": list(normalized)},
                created_at,
            )
        return LongTermMemory(memory_id, session_id, safe_content, normalized, created_at, checksum)

    def search_memory(self, query: str, limit: int = 20) -> list[LongTermMemory]:
        tokens = [token.casefold() for token in re.split(r"\W+", query) if len(token) >= 2]
        phrase = " ".join(str(query).casefold().split())
        with self._lock:
            rows = self._connection.execute(
                "SELECT id, session_id, content, tags_json, created_at, checksum FROM long_term_memory "
                "ORDER BY created_at DESC, id DESC",
            ).fetchall()
        ranked: list[tuple[int, str, str, LongTermMemory]] = []
        for row in rows:
            tags = tuple(str(item) for item in json.loads(str(row["tags_json"])))
            expected = hashlib.sha256(
                _canonical_memory(
                    str(row["id"]),
                    str(row["session_id"]),
                    str(row["content"]),
                    tags,
                    str(row["created_at"]),
                ).encode("utf-8")
            ).hexdigest()
            if expected != str(row["checksum"]):
                raise ValueError(f"memory checksum mismatch for {row['id']}")
            content = str(row["content"])
            haystack = " ".join((content, *tags)).casefold()
            if tokens and not any(token in haystack for token in tokens):
                continue
            content_tokens = set(re.split(r"\W+", content.casefold()))
            tag_tokens = set(re.split(r"\W+", " ".join(tags).casefold()))
            score = 0
            for token in tokens:
                if token in content_tokens:
                    score += 3
                elif token in haystack:
                    score += 1
                if token in tag_tokens:
                    score += 2
            if phrase and phrase in haystack:
                score += 2
            memory = LongTermMemory(
                str(row["id"]),
                str(row["session_id"]),
                content,
                tags,
                str(row["created_at"]),
                str(row["checksum"]),
            )
            ranked.append((score, str(row["created_at"]), str(row["id"]), memory))
        ranked.sort(key=lambda item: (item[0], item[1], item[2]), reverse=True)
        return [item[3] for item in ranked[: max(1, int(limit))]]

    def close(self) -> None:
        with self._lock:
            self._connection.close()


class LayeredContext:
    """Build P0/P1/P3 context from a repository and persisted session events."""

    def __init__(
        self,
        store: SQLiteContextStore,
        project_root: str | Path,
        max_files: int = 256,
        max_raw_bytes: int = 16_000,
        max_events: int = 64,
        max_event_chars: int = 12_000,
    ) -> None:
        self.store = store
        self.project_root = Path(project_root).resolve()
        self.max_files = max(1, int(max_files))
        self.max_raw_bytes = max(1, int(max_raw_bytes))
        self.max_events = max(1, int(max_events))
        self.max_event_chars = max(256, int(max_event_chars))

    def load(self, session_id: str, raw_paths: Iterable[str] = ()) -> ContextSnapshot:
        files = self._files()
        events = self.store.events(session_id, limit=self.max_events)
        p0_lines = ["P0 directory summary"]
        for path in files:
            relative = path.relative_to(self.project_root).as_posix()
            try:
                size = path.stat().st_size
            except OSError:
                continue
            p0_lines.append(f"{relative} ({size} bytes)")
        p0 = "\n".join(p0_lines)

        requested = list(dict.fromkeys(str(path) for path in raw_paths))
        if not requested:
            requested = self._event_paths(events)
        resolved: list[Path] = []
        for raw_path in requested:
            candidate = self._resolve(raw_path)
            if candidate is None:
                continue
            try:
                if candidate.is_file():
                    resolved.append(candidate)
            except OSError:
                continue

        p1_lines = ["P1 node summaries"]
        unavailable: set[Path] = set()
        for path in resolved:
            relative = path.relative_to(self.project_root).as_posix()
            try:
                size, line_count, digest = self._file_summary(path)
            except OSError as exc:
                unavailable.add(path)
                p1_lines.append(f"{relative} [unavailable: {type(exc).__name__}]")
                continue
            p1_lines.append(
                f"{relative} ({size} bytes, {line_count} lines, sha256={digest})"
            )
        p1 = "\n".join(p1_lines)

        p3: dict[str, str] = {}
        for path in resolved:
            relative = path.relative_to(self.project_root).as_posix()
            if path in unavailable:
                p3[relative] = "[file unavailable]"
                continue
            lower_name = path.name.lower()
            if lower_name in _SENSITIVE_FILES or lower_name.startswith(".env."):
                p3[relative] = "[sensitive file omitted]"
                continue
            try:
                with path.open("rb") as handle:
                    data = handle.read(self.max_raw_bytes + 1)
                truncated = len(data) > self.max_raw_bytes
                text = _redact_text(data[: self.max_raw_bytes].decode("utf-8"))
                if truncated:
                    text += "\n[raw content truncated]"
                p3[relative] = text
            except UnicodeDecodeError:
                p3[relative] = "[binary file omitted]"
            except OSError:
                p3[relative] = "[file unavailable]"

        event_lines = [
            f"#{event.sequence} {event.kind}: "
            f"{json.dumps(self._prompt_event_payload(event), ensure_ascii=False, sort_keys=True)}"
            for event in events
            if not (
                event.kind.startswith("compaction/")
                or event.kind.startswith("tool_result/")
            )
        ]
        events_text = "\n".join(event_lines)
        if len(events_text) > self.max_event_chars:
            events_text = "[older event content truncated]\n" + events_text[-self.max_event_chars :]
        baseline_bytes = sum(self._safe_size(path) for path in files) + len(events_text.encode("utf-8"))
        rendered = "\n".join((p0, p1, events_text, *p3.values()))
        return ContextSnapshot(
            p0=p0,
            p1=p1,
            p3=p3,
            events_text=events_text,
            estimated_input_tokens=_estimate_tokens(_redact_text(rendered)),
            estimated_baseline_tokens=max(1, (baseline_bytes + 3) // 4),
        )

    def reflect(self, session_id: str, content: str, tags: Iterable[str] = ()) -> LongTermMemory:
        return self.store.add_reflection(session_id, content, tags)

    def search_memory(self, query: str, limit: int = 20) -> list[LongTermMemory]:
        return self.store.search_memory(query, limit)

    def _files(self) -> list[Path]:
        files: list[Path] = []
        for root, directories, names in os.walk(
            self.project_root,
            topdown=True,
            onerror=lambda _error: None,
            followlinks=False,
        ):
            root_path = Path(root)
            directories.sort()
            directories[:] = [
                name
                for name in directories
                if name not in _SKIP_DIRS and not self._is_link(root_path / name)
            ]
            for name in sorted(names):
                candidate = root_path / name
                if self._is_link(candidate):
                    continue
                try:
                    if candidate.is_file():
                        files.append(candidate)
                        if len(files) >= self.max_files:
                            return files
                except OSError:
                    continue
        return files

    def _resolve(self, raw_path: str) -> Path | None:
        relative_path = Path(raw_path)
        if (
            relative_path.is_absolute()
            or "\x00" in raw_path
            or any(":" in part for part in relative_path.parts)
        ):
            raise ValueError("invalid project-relative path")
        unresolved = self.project_root / relative_path
        try:
            relative_unresolved = unresolved.absolute().relative_to(self.project_root)
        except ValueError as exc:
            raise ValueError("path outside project root") from exc
        current = self.project_root
        for part in relative_unresolved.parts:
            current /= part
            if self._is_link(current):
                return None
        try:
            candidate = unresolved.resolve()
        except OSError:
            return None
        try:
            candidate.relative_to(self.project_root)
        except ValueError as exc:
            raise ValueError("path outside project root") from exc
        return candidate

    @staticmethod
    def _is_link(path: Path) -> bool:
        try:
            if path.is_symlink():
                return True
            is_junction = getattr(path, "is_junction", None)
            return bool(is_junction and is_junction())
        except OSError:
            return True

    @staticmethod
    def _safe_size(path: Path) -> int:
        try:
            return int(path.stat().st_size)
        except OSError:
            return 0

    @staticmethod
    def _file_summary(path: Path) -> tuple[int, int, str]:
        digest = hashlib.sha256()
        size = 0
        newlines = 0
        last_byte = b""
        with path.open("rb") as handle:
            while chunk := handle.read(_READ_CHUNK_BYTES):
                digest.update(chunk)
                size += len(chunk)
                newlines += chunk.count(b"\n")
                last_byte = chunk[-1:]
        line_count = 0 if size == 0 else newlines + (0 if last_byte == b"\n" else 1)
        return size, line_count, digest.hexdigest()

    @staticmethod
    def _event_paths(events: Iterable[ContextEvent]) -> list[str]:
        paths: list[str] = []
        for event in events:
            value = event.payload.get("path")
            if isinstance(value, str) and value not in paths:
                paths.append(value)
        return paths

    @staticmethod
    def _prompt_event_payload(event: ContextEvent) -> dict[str, Any]:
        allowed_by_kind = {
            "plan": {"steps", "current_index", "mode"},
            "tool_call": {"tool_call_id", "tool_name", "path", "argument_keys"},
            "file_diff": {
                "tool_call_id",
                "operation",
                "path",
                "change_sha256",
                "git_diff_sha256",
            },
            "execution_result": {
                "tool_call_id",
                "tool_name",
                "status",
                "success",
                "exit_code",
                "truncated",
                "turn",
                "result_sha256",
                "request_sha256",
                "response_sha256",
            },
            "reflection": {"memory_id", "tags", "status", "turn"},
            # Compaction lifecycle events are durable audit markers only.  The
            # current surface, not this log metadata, is what reaches a model.
            "compaction/start": {"range", "trigger", "attempt"},
            "compaction/summary": {"replaced_tokens", "summary_tokens", "summary_sha256", "attempt"},
            "compaction/end": {"status", "replaced_tokens", "summary_tokens"},
            "tool_result/prune": {"count", "original_chars", "pruned_chars", "threshold"},
        }
        allowed = allowed_by_kind.get(event.kind, set())
        return {key: value for key, value in event.payload.items() if key in allowed}


__all__ = ["ContextEvent", "ContextSnapshot", "LayeredContext", "LongTermMemory", "SQLiteContextStore"]
