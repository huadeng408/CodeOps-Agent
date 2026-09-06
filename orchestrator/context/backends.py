"""Pluggable persistence backends for event-sourced agent context.

The SQLite implementation remains the local default.  Redis and MySQL are
explicit adapters: selecting either backend without its dependency, endpoint,
or a reachable service is an error and never silently falls back to SQLite.
"""

from __future__ import annotations

import hashlib
import json
import os
import re
import threading
import uuid
from collections.abc import Iterable, Iterator, Mapping
from contextlib import contextmanager
from pathlib import Path
from typing import Any, Protocol, runtime_checkable
from urllib.parse import unquote, urlparse
from datetime import UTC, datetime, timedelta

from .memory import (
    _EVENT_KINDS,
    ContextEvent,
    LongTermMemory,
    SQLiteContextStore,
    _canonical_memory,
    _canonical_payload,
    _now,
    _redact,
    _redact_text,
    _session_id,
)


@runtime_checkable
class ContextStore(Protocol):
    """Persistence contract consumed by ``LayeredContext`` and runners."""

    def append(
        self, session_id: str, kind: str, payload: dict[str, Any]
    ) -> ContextEvent: ...

    def events(
        self, session_id: str, limit: int | None = None
    ) -> list[ContextEvent]: ...

    def events_after(
        self, session_id: str, after_sequence: int, limit: int | None = None
    ) -> list[ContextEvent]: ...

    def add_memory(
        self, session_id: str, content: str, tags: Iterable[str] = (), **metadata: Any
    ) -> LongTermMemory: ...

    def add_reflection(
        self, session_id: str, content: str, tags: Iterable[str] = (), **metadata: Any
    ) -> LongTermMemory: ...

    def search_memory(self, query: str, limit: int = 20) -> list[LongTermMemory]: ...

    def close(self) -> None: ...


def _normalise_tags(tags: Iterable[str]) -> tuple[str, ...]:
    normalized = tuple(
        dict.fromkeys(tag.strip().lower().lstrip("#") for tag in tags if tag.strip())
    )
    if any(_redact_text(tag) != tag for tag in normalized):
        raise ValueError("sensitive tags are not allowed in long-term memory")
    return normalized


def _memory_parts(
    session_id: str, content: str, tags: Iterable[str]
) -> tuple[str, tuple[str, ...]]:
    session_id = _session_id(session_id)
    original = content.strip()
    safe_content = _redact_text(original)
    if safe_content != original:
        raise ValueError("sensitive content is not allowed in long-term memory")
    if not safe_content:
        raise ValueError("memory content must not be empty")
    return session_id, _normalise_tags(tags)


def _memory_metadata(metadata: Mapping[str, Any]) -> tuple[str, str, str, int, str]:
    source_type = str(metadata.get("source_type", "") or "").strip()
    source_id = str(metadata.get("source_id", "") or "").strip()
    source_revision = str(metadata.get("source_revision", "") or "").strip()
    try:
        revision = int(metadata.get("revision", 1))
    except (TypeError, ValueError) as exc:
        raise ValueError("memory revision must be a positive integer") from exc
    if revision < 1:
        raise ValueError("memory revision must be a positive integer")
    expires_at = str(metadata.get("expires_at", "") or "").strip()
    if metadata.get("ttl_seconds") is not None:
        try:
            ttl = float(metadata["ttl_seconds"])
        except (TypeError, ValueError) as exc:
            raise ValueError("memory ttl_seconds must be positive") from exc
        if ttl <= 0:
            raise ValueError("memory ttl_seconds must be positive")
        expires_at = (datetime.now(UTC) + timedelta(seconds=ttl)).isoformat()
    if expires_at:
        try:
            datetime.fromisoformat(expires_at)
        except ValueError as exc:
            raise ValueError("memory expires_at must be an ISO-8601 timestamp") from exc
    return source_type, source_id, source_revision, revision, expires_at


def _expired(expires_at: str) -> bool:
    if not expires_at:
        return False
    try:
        expiry = datetime.fromisoformat(expires_at)
    except ValueError:
        return True
    if expiry.tzinfo is None:
        expiry = expiry.replace(tzinfo=UTC)
    return expiry <= datetime.now(UTC)


def _new_memory(
    session_id: str,
    content: str,
    tags: Iterable[str],
    metadata: Mapping[str, Any] | None = None,
) -> LongTermMemory:
    session_id, normalized = _memory_parts(session_id, content, tags)
    source_type, source_id, source_revision, revision, expires_at = _memory_metadata(metadata or {})
    created_at = _now()
    memory_id = uuid.uuid4().hex
    checksum = hashlib.sha256(
            _canonical_memory(
            memory_id, session_id, content.strip(), normalized, created_at,
            source_type, source_id, source_revision, revision, expires_at,
        ).encode("utf-8")
    ).hexdigest()
    return LongTermMemory(
        memory_id, session_id, content.strip(), normalized, created_at, checksum,
        source_type, source_id, source_revision, revision, expires_at,
    )


def _verify_memory(row: Mapping[str, Any]) -> LongTermMemory:
    tags_value = row["tags"]
    if isinstance(tags_value, str):
        tags = tuple(str(item) for item in json.loads(tags_value))
    else:
        tags = tuple(str(item) for item in tags_value)
    memory = LongTermMemory(
        str(row["id"]),
        str(row["session_id"]),
        str(row["content"]),
        tags,
        str(row["created_at"]),
        str(row["checksum"]),
        str(row.get("source_type", "") or ""),
        str(row.get("source_id", "") or ""),
        str(row.get("source_revision", "") or ""),
        int(row.get("revision", 1) or 1),
        str(row.get("expires_at", "") or ""),
    )
    expected = hashlib.sha256(
        _canonical_memory(
            memory.id, memory.session_id, memory.content, memory.tags, memory.created_at,
            memory.source_type, memory.source_id, memory.source_revision, memory.revision, memory.expires_at,
        ).encode("utf-8")
    ).hexdigest()
    if expected != memory.checksum:
        raise ValueError(f"memory checksum mismatch for {memory.id}")
    return memory


def _rank_memories(
    rows: Iterable[Mapping[str, Any]], query: str, limit: int
) -> list[LongTermMemory]:
    tokens = [token.casefold() for token in re.split(r"\W+", query) if len(token) >= 2]
    phrase = " ".join(str(query).casefold().split())
    ranked: list[tuple[int, str, str, LongTermMemory]] = []
    for row in rows:
        memory = _verify_memory(row)
        if _expired(memory.expires_at):
            continue
        haystack = " ".join((memory.content, *memory.tags)).casefold()
        if tokens and not any(token in haystack for token in tokens):
            continue
        content_tokens = set(re.split(r"\W+", memory.content.casefold()))
        tag_tokens = set(re.split(r"\W+", " ".join(memory.tags).casefold()))
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
        ranked.append((score, memory.created_at, memory.id, memory))
    ranked.sort(key=lambda item: (item[0], item[1], item[2]), reverse=True)
    return [item[3] for item in ranked[: max(1, int(limit))]]


def _event_from_wire(
    raw: str | bytes, *, fallback_sequence: int | None = None
) -> ContextEvent:
    value = json.loads(raw.decode("utf-8") if isinstance(raw, bytes) else raw)
    if not isinstance(value, dict):
        raise TypeError("invalid context event payload")
    payload = value.get("payload")
    if not isinstance(payload, dict):
        raise TypeError("invalid context event payload")
    return ContextEvent(
        event_id=str(value.get("event_id", "")),
        session_id=str(value["session_id"]),
        sequence=int(value.get("sequence") or fallback_sequence or 0),
        kind=str(value["kind"]),
        payload=payload,
        created_at=str(value["created_at"]),
        checksum=str(value["checksum"]),
        previous_checksum=str(value.get("previous_checksum", "")),
    )


def _verify_event(event: ContextEvent) -> None:
    if event.event_id:
        canonical = _canonical_payload(
            event.event_id,
            event.kind,
            event.session_id,
            event.payload,
            event.created_at,
            event.previous_checksum,
        )
    else:
        canonical = json.dumps(
            {
                "kind": event.kind,
                "session_id": event.session_id,
                "payload": event.payload,
                "created_at": event.created_at,
            },
            ensure_ascii=False,
            sort_keys=True,
            separators=(",", ":"),
        )
    expected = hashlib.sha256(canonical.encode("utf-8")).hexdigest()
    if expected != event.checksum:
        raise ValueError(
            f"context event checksum mismatch at sequence {event.sequence}"
        )


def _verify_event_chain(events: list[ContextEvent], *, anchor: str = "") -> None:
    previous = anchor
    for index, event in enumerate(events):
        _verify_event(event)
        if event.event_id and event.previous_checksum != previous:
            raise ValueError(
                f"context event chain mismatch at sequence {event.sequence}"
            )
        previous = event.checksum


class RedisContextStore:
    """Redis-backed append-only context store.

    Values are JSON records in per-session lists.  A distributed Redis lock
    serialises each session append so the checksum chain remains deterministic.
    """

    def __init__(
        self,
        client: Any,
        *,
        prefix: str = "codeops:context",
        lock_timeout: float = 30.0,
        owns_client: bool = False,
    ) -> None:
        self.client = client
        self.prefix = prefix.strip(":") or "codeops:context"
        self.lock_timeout = float(lock_timeout)
        self._owns_client = owns_client
        self._local_lock = threading.RLock()
        try:
            self.client.ping()
        except Exception as exc:
            raise RuntimeError("Redis context backend is unavailable") from exc

    def _events_key(self, session_id: str) -> str:
        return f"{self.prefix}:events:{session_id}"

    def _memory_key(self) -> str:
        return f"{self.prefix}:memories"

    def _lock_key(self, session_id: str) -> str:
        return f"{self.prefix}:lock:{session_id}"

    @contextmanager
    def _locked(self, session_id: str) -> Iterator[None]:
        with self._local_lock:
            lock = self.client.lock(
                self._lock_key(session_id), timeout=self.lock_timeout
            )
            acquired = lock.acquire(blocking=True, blocking_timeout=self.lock_timeout)
            if not acquired:
                raise RuntimeError("Redis context lock acquisition timed out")
            try:
                yield
            finally:
                lock.release()

    def append(
        self, session_id: str, kind: str, payload: dict[str, Any]
    ) -> ContextEvent:
        session_id = _session_id(session_id)
        kind = kind.strip()
        if kind not in _EVENT_KINDS:
            raise ValueError(f"unsupported context event kind: {kind}")
        safe_payload = _redact(payload)
        created_at = _now()
        with self._locked(session_id):
            return self._append_locked(session_id, kind, safe_payload, created_at)

    def _append_locked(
        self, session_id: str, kind: str, safe_payload: dict[str, Any], created_at: str
    ) -> ContextEvent:
        raw = self.client.lrange(self._events_key(session_id), -1, -1)
        previous = _event_from_wire(raw[0]).checksum if raw else ""
        event_id = uuid.uuid4().hex
        sequence = len(self.client.lrange(self._events_key(session_id), 0, -1)) + 1
        encoded = _canonical_payload(
            event_id, kind, session_id, safe_payload, created_at, previous
        )
        checksum = hashlib.sha256(encoded.encode("utf-8")).hexdigest()
        event = ContextEvent(
            event_id,
            session_id,
            sequence,
            kind,
            safe_payload,
            created_at,
            checksum,
            previous,
        )
        wire = json.dumps(
            {
                "event_id": event.event_id,
                "session_id": event.session_id,
                "sequence": event.sequence,
                "kind": event.kind,
                "payload": event.payload,
                "created_at": event.created_at,
                "checksum": event.checksum,
                "previous_checksum": event.previous_checksum,
            },
            ensure_ascii=False,
            sort_keys=True,
            separators=(",", ":"),
        )
        self.client.rpush(self._events_key(session_id), wire)
        return event

    def events(self, session_id: str, limit: int | None = None) -> list[ContextEvent]:
        session_id = _session_id(session_id)
        raw = list(self.client.lrange(self._events_key(session_id), 0, -1))
        all_events = [
            _event_from_wire(value, fallback_sequence=index + 1)
            for index, value in enumerate(raw)
        ]
        selected = all_events if limit is None else all_events[-max(1, int(limit)) :]
        anchor = (
            all_events[len(all_events) - len(selected) - 1].checksum
            if selected and len(selected) < len(all_events)
            else ""
        )
        _verify_event_chain(selected, anchor=anchor)
        return selected

    def events_after(
        self, session_id: str, after_sequence: int, limit: int | None = None
    ) -> list[ContextEvent]:
        session_id = _session_id(session_id)
        cursor = max(0, int(after_sequence))
        raw = list(self.client.lrange(self._events_key(session_id), 0, -1))
        all_events = [
            _event_from_wire(value, fallback_sequence=index + 1)
            for index, value in enumerate(raw)
        ]
        selected = [event for event in all_events if event.sequence > cursor]
        if limit is not None:
            selected = selected[: max(1, int(limit))]
        anchor = ""
        if selected:
            predecessor = next(
                (event for event in all_events if event.sequence == selected[0].sequence - 1),
                None,
            )
            anchor = predecessor.checksum if predecessor is not None else ""
        _verify_event_chain(selected, anchor=anchor)
        return selected

    def _add_memory(
        self, session_id: str, content: str, tags: Iterable[str], *, reflection: bool, metadata: Mapping[str, Any] | None = None
    ) -> LongTermMemory:
        memory = _new_memory(session_id, content, tags, metadata)
        wire = json.dumps(
            {
                "id": memory.id,
                "session_id": memory.session_id,
                "content": memory.content,
                "tags": list(memory.tags),
                "created_at": memory.created_at,
                "checksum": memory.checksum,
                "source_type": memory.source_type,
                "source_id": memory.source_id,
                "source_revision": memory.source_revision,
                "revision": memory.revision,
                "expires_at": memory.expires_at,
            },
            ensure_ascii=False,
            sort_keys=True,
            separators=(",", ":"),
        )
        with self._locked(memory.session_id):
            self.client.rpush(self._memory_key(), wire)
            if reflection:
                self._append_locked(
                    memory.session_id,
                    "reflection",
                    {"memory_id": memory.id, "tags": list(memory.tags)},
                    memory.created_at,
                )
        return memory

    def add_memory(
        self, session_id: str, content: str, tags: Iterable[str] = (), **metadata: Any
    ) -> LongTermMemory:
        return self._add_memory(session_id, content, tags, reflection=False, metadata=metadata)

    def add_reflection(
        self, session_id: str, content: str, tags: Iterable[str] = (), **metadata: Any
    ) -> LongTermMemory:
        return self._add_memory(session_id, content, tags, reflection=True, metadata=metadata)

    def search_memory(self, query: str, limit: int = 20) -> list[LongTermMemory]:
        raw = self.client.lrange(self._memory_key(), 0, -1)
        rows = []
        for value in raw:
            item = json.loads(
                value.decode("utf-8") if isinstance(value, bytes) else value
            )
            rows.append(item)
        return _rank_memories(rows, query, limit)

    def close(self) -> None:
        if self._owns_client:
            close = getattr(self.client, "close", None)
            if callable(close):
                close()


class MySQLContextStore:
    """MySQL-backed context store using the same wire/checksum contract."""

    def __init__(self, connection: Any, *, owns_connection: bool = False) -> None:
        self.connection = connection
        self._owns_connection = owns_connection
        self._lock = threading.RLock()
        try:
            self._initialise_schema()
        except Exception as exc:
            if owns_connection:
                self.close()
            raise RuntimeError("MySQL context backend is unavailable") from exc

    def _initialise_schema(self) -> None:
        with self.connection.cursor() as cursor:
            cursor.execute(
                """CREATE TABLE IF NOT EXISTS context_sessions (
                    session_id VARCHAR(255) PRIMARY KEY,
                    last_checksum CHAR(64) NOT NULL DEFAULT ''
                ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4"""
            )
            cursor.execute(
                """CREATE TABLE IF NOT EXISTS context_events (
                    sequence BIGINT NOT NULL AUTO_INCREMENT PRIMARY KEY,
                    event_id VARCHAR(64) NOT NULL UNIQUE,
                    session_id VARCHAR(255) NOT NULL,
                    kind VARCHAR(64) NOT NULL,
                    payload_json LONGTEXT NOT NULL,
                    created_at VARCHAR(64) NOT NULL,
                    checksum CHAR(64) NOT NULL UNIQUE,
                    previous_checksum CHAR(64) NOT NULL DEFAULT '',
                    INDEX context_events_session_sequence (session_id, sequence)
                ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4"""
            )
            cursor.execute(
                """CREATE TABLE IF NOT EXISTS long_term_memory (
                    id VARCHAR(64) PRIMARY KEY,
                    session_id VARCHAR(255) NOT NULL,
                    content LONGTEXT NOT NULL,
                    tags_json TEXT NOT NULL,
                    created_at VARCHAR(64) NOT NULL,
                    checksum CHAR(64) NOT NULL UNIQUE,
                    source_type VARCHAR(128) NOT NULL DEFAULT '',
                    source_id VARCHAR(255) NOT NULL DEFAULT '',
                    source_revision VARCHAR(255) NOT NULL DEFAULT '',
                    revision BIGINT NOT NULL DEFAULT 1,
                    expires_at VARCHAR(64) NOT NULL DEFAULT '',
                    INDEX long_term_memory_session (session_id, created_at)
                ) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4"""
            )
            # Existing installations may have been created before the
            # provenance and hash-chain columns were introduced.  ALTER is
            # intentionally additive and duplicate-column errors are safe to
            # ignore, so startup upgrades the same tables in place.
            for table, column, definition in (
                ("context_events", "event_id", "VARCHAR(64) NULL"),
                ("context_events", "previous_checksum", "CHAR(64) NOT NULL DEFAULT ''"),
                ("long_term_memory", "source_type", "VARCHAR(128) NOT NULL DEFAULT ''"),
                ("long_term_memory", "source_id", "VARCHAR(255) NOT NULL DEFAULT ''"),
                ("long_term_memory", "source_revision", "VARCHAR(255) NOT NULL DEFAULT ''"),
                ("long_term_memory", "revision", "BIGINT NOT NULL DEFAULT 1"),
                ("long_term_memory", "expires_at", "VARCHAR(64) NOT NULL DEFAULT ''"),
            ):
                try:
                    cursor.execute(
                        f"ALTER TABLE {table} ADD COLUMN {column} {definition}"
                    )
                except Exception as exc:
                    if "duplicate" not in str(exc).lower() and "exists" not in str(exc).lower():
                        raise
        self.connection.commit()

    def append(
        self, session_id: str, kind: str, payload: dict[str, Any]
    ) -> ContextEvent:
        session_id = _session_id(session_id)
        kind = kind.strip()
        if kind not in _EVENT_KINDS:
            raise ValueError(f"unsupported context event kind: {kind}")
        safe_payload = _redact(payload)
        created_at = _now()
        with self._lock:
            try:
                with self.connection.cursor() as cursor:
                    event = self._append_cursor(
                        cursor, session_id, kind, safe_payload, created_at
                    )
                self.connection.commit()
            except Exception:
                self.connection.rollback()
                raise
        return event

    def _append_cursor(
        self,
        cursor: Any,
        session_id: str,
        kind: str,
        safe_payload: dict[str, Any],
        created_at: str,
    ) -> ContextEvent:
        cursor.execute(
            "INSERT INTO context_sessions(session_id, last_checksum) VALUES (%s, '') "
            "ON DUPLICATE KEY UPDATE session_id=session_id",
            (session_id,),
        )
        cursor.execute(
            "SELECT last_checksum FROM context_sessions WHERE session_id=%s FOR UPDATE",
            (session_id,),
        )
        row = cursor.fetchone()
        previous = str(row[0] if row else "")
        event_id = uuid.uuid4().hex
        encoded = _canonical_payload(
            event_id, kind, session_id, safe_payload, created_at, previous
        )
        checksum = hashlib.sha256(encoded.encode("utf-8")).hexdigest()
        cursor.execute(
            "INSERT INTO context_events(event_id, session_id, kind, payload_json, created_at, checksum, previous_checksum) VALUES (%s,%s,%s,%s,%s,%s,%s)",
            (
                event_id,
                session_id,
                kind,
                json.dumps(safe_payload, ensure_ascii=False, sort_keys=True),
                created_at,
                checksum,
                previous,
            ),
        )
        sequence = int(getattr(cursor, "lastrowid", 0) or 0)
        cursor.execute(
            "UPDATE context_sessions SET last_checksum=%s WHERE session_id=%s",
            (checksum, session_id),
        )
        return ContextEvent(
            event_id,
            session_id,
            sequence,
            kind,
            safe_payload,
            created_at,
            checksum,
            previous,
        )

    def events(self, session_id: str, limit: int | None = None) -> list[ContextEvent]:
        session_id = _session_id(session_id)
        if limit is None:
            query = "SELECT event_id, session_id, sequence, kind, payload_json, created_at, checksum, previous_checksum FROM context_events WHERE session_id=%s ORDER BY sequence"
            params = (session_id,)
        else:
            query = "SELECT event_id, session_id, sequence, kind, payload_json, created_at, checksum, previous_checksum FROM context_events WHERE session_id=%s ORDER BY sequence DESC LIMIT %s"
            params = (session_id, max(1, int(limit)))
        with self._lock, self.connection.cursor() as cursor:
            cursor.execute(query, params)
            rows = list(cursor.fetchall())
            if limit is not None:
                rows.reverse()
            anchor = ""
            if rows and limit is not None:
                cursor.execute(
                    "SELECT checksum FROM context_events WHERE session_id=%s AND sequence < %s ORDER BY sequence DESC LIMIT 1",
                    (session_id, rows[0][2]),
                )
                anchor_row = cursor.fetchone()
                anchor = str(anchor_row[0]) if anchor_row else ""
        result = []
        for row in rows:
            payload = json.loads(str(row[4]))
            event = ContextEvent(
                str(row[0]),
                str(row[1]),
                int(row[2]),
                str(row[3]),
                payload,
                str(row[5]),
                str(row[6]),
                str(row[7] or ""),
            )
            result.append(event)
        _verify_event_chain(result, anchor=anchor)
        return result

    def events_after(
        self, session_id: str, after_sequence: int, limit: int | None = None
    ) -> list[ContextEvent]:
        session_id = _session_id(session_id)
        after = max(0, int(after_sequence))
        query = (
            "SELECT event_id, session_id, sequence, kind, payload_json, created_at, "
            "checksum, previous_checksum FROM context_events "
            "WHERE session_id=%s AND sequence>%s ORDER BY sequence"
        )
        params: tuple[Any, ...] = (session_id, after)
        if limit is not None:
            query += " LIMIT %s"
            params += (max(1, int(limit)),)
        with self._lock, self.connection.cursor() as cursor:
            cursor.execute(query, params)
            rows = list(cursor.fetchall())
            anchor = ""
            if rows:
                cursor.execute(
                    "SELECT checksum FROM context_events WHERE session_id=%s AND sequence<%s ORDER BY sequence DESC LIMIT 1",
                    (session_id, rows[0][2]),
                )
                anchor_row = cursor.fetchone()
                anchor = str(anchor_row[0]) if anchor_row else ""
        result = []
        for row in rows:
            result.append(
                ContextEvent(
                    str(row[0]), str(row[1]), int(row[2]), str(row[3]),
                    json.loads(str(row[4])), str(row[5]), str(row[6]), str(row[7] or ""),
                )
            )
        _verify_event_chain(result, anchor=anchor)
        return result

    def _insert_memory(
        self, memory: LongTermMemory, *, cursor: Any | None = None
    ) -> None:
        owns_cursor = cursor is None
        if owns_cursor:
            cursor = self.connection.cursor()
        try:
            cursor.execute(
                "INSERT INTO long_term_memory(id, session_id, content, tags_json, created_at, checksum, source_type, source_id, source_revision, revision, expires_at) VALUES (%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s)",
                (
                    memory.id,
                    memory.session_id,
                    memory.content,
                    json.dumps(memory.tags),
                    memory.created_at,
                    memory.checksum,
                    memory.source_type,
                    memory.source_id,
                    memory.source_revision,
                    memory.revision,
                    memory.expires_at,
                ),
            )
        finally:
            if owns_cursor:
                cursor.close()

    def add_memory(
        self, session_id: str, content: str, tags: Iterable[str] = (), **metadata: Any
    ) -> LongTermMemory:
        memory = _new_memory(session_id, content, tags, metadata)
        with self._lock:
            try:
                self._insert_memory(memory)
                self.connection.commit()
            except Exception:
                self.connection.rollback()
                raise
        return memory

    def add_reflection(
        self, session_id: str, content: str, tags: Iterable[str] = (), **metadata: Any
    ) -> LongTermMemory:
        memory = _new_memory(session_id, content, tags, metadata)
        with self._lock:
            try:
                with self.connection.cursor() as cursor:
                    self._insert_memory(memory, cursor=cursor)
                    self._append_cursor(
                        cursor,
                        memory.session_id,
                        "reflection",
                        {"memory_id": memory.id, "tags": list(memory.tags)},
                        memory.created_at,
                    )
                self.connection.commit()
            except Exception:
                self.connection.rollback()
                raise
        return memory

    def search_memory(self, query: str, limit: int = 20) -> list[LongTermMemory]:
        with self._lock, self.connection.cursor() as cursor:
            cursor.execute(
                "SELECT id, session_id, content, tags_json, created_at, checksum, source_type, source_id, source_revision, revision, expires_at FROM long_term_memory ORDER BY created_at DESC, id DESC"
            )
            rows = list(cursor.fetchall())
        mappings = [
            {
                "id": row[0],
                "session_id": row[1],
                "content": row[2],
                "tags": row[3],
                "created_at": row[4],
                "checksum": row[5],
                "source_type": row[6],
                "source_id": row[7],
                "source_revision": row[8],
                "revision": row[9],
                "expires_at": row[10],
            }
            for row in rows
        ]
        return _rank_memories(mappings, query, limit)

    def close(self) -> None:
        if self._owns_connection:
            self.connection.close()


def _read_first_env(names: tuple[str, ...], environ: Mapping[str, str]) -> str:
    for name in names:
        value = str(environ.get(name, "")).strip()
        if value:
            return value
    return ""


def _redis_client(url: str) -> Any:
    try:
        import redis
    except ImportError as exc:
        raise RuntimeError(
            "Redis context backend requires the 'redis' package"
        ) from exc
    try:
        client = redis.Redis.from_url(url, decode_responses=True)
        client.ping()
        return client
    except Exception as exc:
        raise RuntimeError("Redis context backend is unavailable") from exc


def _mysql_connection(dsn: str) -> Any:
    try:
        import pymysql
    except ImportError as exc:
        raise RuntimeError(
            "MySQL context backend requires the 'pymysql' package"
        ) from exc
    parsed = urlparse(dsn)
    if parsed.scheme not in {"mysql", "mysql+pymysql"} or not parsed.hostname:
        raise ValueError("MySQL DSN must use mysql:// or mysql+pymysql://")
    database = parsed.path.lstrip("/")
    if not database:
        raise ValueError("MySQL DSN must include a database name")
    kwargs: dict[str, Any] = {
        "host": parsed.hostname,
        "port": parsed.port or 3306,
        "user": unquote(parsed.username or ""),
        "password": unquote(parsed.password or ""),
        "database": database,
        "charset": "utf8mb4",
        "autocommit": False,
    }
    try:
        return pymysql.connect(**kwargs)
    except Exception as exc:
        raise RuntimeError("MySQL context backend is unavailable") from exc


def build_context_store(
    backend: str | None = None,
    *,
    project_root: str | Path = ".",
    redis_url: str | None = None,
    mysql_dsn: str | None = None,
    environ: Mapping[str, str] | None = None,
) -> ContextStore:
    """Build a context store without implicit remote-to-local fallback."""

    env = os.environ if environ is None else environ
    selected = (
        (
            backend
            or _read_first_env(("CODE_AGENT_CONTEXT_BACKEND", "CONTEXT_BACKEND"), env)
            or "sqlite"
        )
        .strip()
        .lower()
    )
    if selected == "sqlite":
        return SQLiteContextStore(Path(project_root) / ".agent" / "context.sqlite")
    if selected == "redis":
        url = (
            redis_url
            or _read_first_env(("CODE_AGENT_CONTEXT_REDIS_URL", "REDIS_URL"), env)
        ).strip()
        if not url:
            raise ValueError("Redis context backend requires a redis URL")
        return RedisContextStore(_redis_client(url), owns_client=True)
    if selected in {"mysql", "mysql+pymysql"}:
        dsn = (
            mysql_dsn
            or _read_first_env(("CODE_AGENT_CONTEXT_MYSQL_DSN", "MYSQL_DSN"), env)
        ).strip()
        if not dsn:
            raise ValueError("MySQL context backend requires a MySQL DSN")
        return MySQLContextStore(_mysql_connection(dsn), owns_connection=True)
    raise ValueError(f"unsupported context backend: {selected}")


__all__ = [
    "ContextStore",
    "MySQLContextStore",
    "RedisContextStore",
    "build_context_store",
]
