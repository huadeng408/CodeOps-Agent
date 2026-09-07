from __future__ import annotations

import json
import threading

import pytest

from orchestrator.context import (
    ContextStore,
    RedisContextStore,
    SQLiteContextStore,
    build_context_store,
)

_CREDENTIAL_LABEL = "OPENAI_" + "API_KEY"


class _FakeRedisLock:
    def __init__(self, lock: threading.RLock) -> None:
        self._lock = lock

    def acquire(
        self, blocking: bool = True, blocking_timeout: float | None = None
    ) -> bool:
        del blocking_timeout
        return self._lock.acquire(blocking)

    def release(self) -> None:
        self._lock.release()


class _FakeRedis:
    def __init__(self) -> None:
        self.values: dict[str, list[str] | str] = {}
        self._lock = threading.RLock()

    def ping(self) -> bool:
        return True

    def lock(self, _name: str, timeout: float | None = None) -> _FakeRedisLock:
        del timeout
        return _FakeRedisLock(self._lock)

    def rpush(self, key: str, value: str) -> int:
        values = self.values.setdefault(key, [])
        assert isinstance(values, list)
        values.append(value)
        return len(values)

    def incr(self, key: str) -> int:
        value = int(self.values.get(key, "0")) + 1
        self.values[key] = str(value)
        return value

    def lrange(self, key: str, start: int, end: int) -> list[str]:
        values = self.values.get(key, [])
        assert isinstance(values, list)
        stop = None if end == -1 else end + 1
        return values[start:stop]


def test_sqlite_context_store_implements_backend_contract(tmp_path) -> None:
    store = SQLiteContextStore(tmp_path / "context.sqlite")

    assert isinstance(store, ContextStore)

    store.close()


def test_redis_context_store_round_trips_events_and_memory() -> None:
    store = RedisContextStore(_FakeRedis(), prefix="test-context")

    first = store.append("session-1", "plan", {"step": "inspect"})
    second = store.append("session-1", "tool_call", {"tool": "Read"})
    memory = store.add_reflection("session-1", "checkpoint recovery", ["workflow"])

    events = store.events("session-1")
    assert [event.sequence for event in events] == [1, 2, 3]
    assert events[1].previous_checksum == first.checksum
    assert events[2].kind == "reflection"
    assert store.search_memory("workflow")[0].id == memory.id
    assert second.checksum != first.checksum

    page = store.events_after("session-1", first.sequence, limit=1)
    assert [event.sequence for event in page] == [second.sequence]
    assert page[0].previous_checksum == first.checksum

    store.close()


def test_redis_events_after_rejects_tampered_prefix() -> None:
    client = _FakeRedis()
    store = RedisContextStore(client, prefix="test-integrity")
    store.append("session-1", "plan", {"step": "one"})
    store.append("session-1", "tool_call", {"tool": "Read"})
    key = "test-integrity:events:session-1"
    raw = json.loads(client.values[key][0])
    raw["payload"] = {"step": "tampered"}
    client.values[key][0] = json.dumps(raw, ensure_ascii=False, sort_keys=True, separators=(",", ":"))

    with pytest.raises(ValueError, match="checksum"):
        store.events_after("session-1", 1, limit=1)
    store.close()


def test_redis_events_after_uses_global_sequence_cursor() -> None:
    store = RedisContextStore(_FakeRedis(), prefix="test-global-sequence")
    first = store.append("session-1", "plan", {"step": 1})
    store.append("session-2", "plan", {"step": "other"})
    second = store.append("session-1", "tool_call", {"tool": "Read"})

    page = store.events_after("session-1", 2)

    assert [event.sequence for event in page] == [second.sequence]
    assert second.sequence == 3
    assert first.sequence == 1
    store.close()


def test_sqlite_events_after_rejects_tampered_prefix(tmp_path) -> None:
    store = SQLiteContextStore(tmp_path / "context-integrity.sqlite")
    store.append("session-1", "plan", {"step": "one"})
    store.append("session-1", "tool_call", {"tool": "Read"})
    with store._lock:
        store._connection.execute(
            "UPDATE context_events SET payload_json = ? WHERE session_id = ? AND sequence = ?",
            (json.dumps({"step": "tampered"}), "session-1", 1),
        )
        store._connection.commit()

    with pytest.raises(ValueError, match="checksum"):
        store.events_after("session-1", 1, limit=1)
    store.close()


def test_redis_context_store_rejects_sensitive_memory_without_writing() -> None:
    client = _FakeRedis()
    store = RedisContextStore(client, prefix="test-sensitive")

    with pytest.raises(ValueError, match="sensitive"):
        store.add_memory("session-1", f"{_CREDENTIAL_LABEL}=fixture-secret", ["secret"])

    assert client.values == {}
    store.close()


def test_context_store_factory_defaults_to_sqlite(tmp_path) -> None:
    store = build_context_store("sqlite", project_root=tmp_path)

    assert isinstance(store, SQLiteContextStore)
    store.close()


def test_context_store_factory_requires_explicit_remote_configuration(tmp_path) -> None:
    with pytest.raises(ValueError, match="redis URL"):
        build_context_store("redis", project_root=tmp_path)

    with pytest.raises(ValueError, match="MySQL DSN"):
        build_context_store("mysql", project_root=tmp_path)


def test_context_store_factory_rejects_unknown_backend(tmp_path) -> None:
    with pytest.raises(ValueError, match="unsupported context backend"):
        build_context_store("memory", project_root=tmp_path)


def test_server_config_exposes_context_backend_without_secret_values() -> None:
    from orchestrator.server import ServerConfig, build_parser

    config = ServerConfig(context_backend="redis")
    assert config.context_backend == "redis"
    parsed = build_parser().parse_args(["--context-backend", "mysql"])
    assert parsed.context_backend == "mysql"
