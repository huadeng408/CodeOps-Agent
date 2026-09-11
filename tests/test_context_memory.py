from __future__ import annotations

import ast
import hashlib
import threading
from pathlib import Path

import pytest

import orchestrator.context.memory as memory_module
from orchestrator.context.memory import LayeredContext, SQLiteContextStore
from orchestrator.memory.manager import Memory, MemoryManager

_CREDENTIAL_LABEL = "OPENAI_" + "API_KEY"


def test_context_module_parses_with_python_311_grammar() -> None:
    source = Path(memory_module.__file__).read_text(encoding="utf-8")
    ast.parse(source, feature_version=(3, 11))


def test_context_events_replay_after_process_restart(tmp_path: Path) -> None:
    database = tmp_path / "context.sqlite"
    first = SQLiteContextStore(database)
    first.append("session-1", "plan", {"steps": ["inspect", "patch"]})
    first.append("session-1", "tool_call", {"tool": "Read", "path": "src/app.py"})
    first.append("session-1", "file_diff", {"path": "src/app.py", "patch": "+return 1"})
    first.close()

    restarted = SQLiteContextStore(database)
    events = restarted.events("session-1")

    assert [event.kind for event in events] == ["plan", "tool_call", "file_diff"]
    assert events[-1].payload["path"] == "src/app.py"
    assert events[-1].checksum
    restarted.close()


def test_context_events_after_cursor_returns_verified_increment(tmp_path: Path) -> None:
    store = SQLiteContextStore(tmp_path / "context.sqlite")
    first = store.append("session-1", "plan", {"step": 1})
    second = store.append("session-1", "tool_call", {"tool": "Read"})
    third = store.append("session-1", "execution_result", {"status": "ok"})

    page = store.events_after("session-1", first.sequence, limit=1)

    assert [event.sequence for event in page] == [second.sequence]
    assert page[0].previous_checksum == first.checksum
    assert store.events_after("session-1", third.sequence) == []
    store.close()


def test_context_events_after_uses_sequence_cursor_when_other_sessions_interleave(tmp_path: Path) -> None:
    store = SQLiteContextStore(tmp_path / "context.sqlite")
    first = store.append("session-1", "plan", {"step": 1})
    store.append("session-2", "plan", {"step": "other"})
    second = store.append("session-1", "tool_call", {"tool": "Read"})

    page = store.events_after("session-1", 2)

    assert [event.sequence for event in page] == [second.sequence]
    store.close()


def test_layered_context_loads_p0_p1_and_explicit_p3_without_leaking_secrets(tmp_path: Path) -> None:
    source = tmp_path / "src"
    source.mkdir()
    (source / "app.py").write_text("def answer():\n    return 'ok'\n", encoding="utf-8")
    (source / "config.py").write_text(
        f"{_CREDENTIAL_LABEL} = 'fixture-secret'\n", encoding="utf-8"
    )

    store = SQLiteContextStore(tmp_path / "context.sqlite")
    store.append(
        "session-1",
        "execution_result",
        {"status": "passed", "output": f"{_CREDENTIAL_LABEL}=hidden"},
    )
    context = LayeredContext(store, tmp_path)

    summary = context.load("session-1", raw_paths=["src/app.py"])

    assert "src/app.py" in summary.p0
    assert "src/app.py" in summary.p1
    assert "def answer" in summary.p3["src/app.py"]
    assert "fixture-secret" not in summary.p0 + summary.p1
    assert "src/config.py" not in summary.p3
    assert "hidden" not in summary.events_text
    assert summary.estimated_input_tokens < summary.estimated_baseline_tokens * 0.4
    store.close()


def test_layered_context_rejects_paths_outside_project_root(tmp_path: Path) -> None:
    store = SQLiteContextStore(tmp_path / "context.sqlite")
    context = LayeredContext(store, tmp_path)

    with pytest.raises(ValueError, match="outside project root"):
        context.load("session-1", raw_paths=["../outside.txt"])
    store.close()


def test_reflection_is_searchable_and_privacy_filtered(tmp_path: Path) -> None:
    store = SQLiteContextStore(tmp_path / "context.sqlite")
    context = LayeredContext(store, tmp_path)

    record = context.reflect(
        "session-1",
        "Keep the SQLite checkpoint schema stable for workflow recovery.",
        tags=["workflow", "durable"],
    )
    assert record.id
    assert context.search_memory("workflow")[0].content.startswith("Keep the SQLite")

    with pytest.raises(ValueError, match="sensitive"):
        context.reflect(
            "session-1",
            f"{_CREDENTIAL_LABEL}=fixture-secret",
            tags=["secret"],
        )
    assert context.search_memory("fixture-secret") == []
    store.close()


def test_memory_manager_rejects_sensitive_name_before_write(tmp_path: Path) -> None:
    manager = MemoryManager(str(tmp_path / "memory"))

    with pytest.raises(ValueError, match="sensitive name"):
        manager.save(
            Memory(
                id="memory-1",
                name="openai-api-key",
                content="ordinary implementation note",
            )
        )

    assert manager.list() == []
    assert list((tmp_path / "memory").glob("*.md")) == [
        tmp_path / "memory" / "MEMORY.md"
    ]


def test_memory_manager_rejects_sensitive_name_from_disk(tmp_path: Path) -> None:
    memory_dir = tmp_path / "memory"
    memory_dir.mkdir()
    (memory_dir / "credential.md").write_text(
        "---\n"
        "id: memory-1\n"
        "name: authorization-token\n"
        "tags: workflow\n"
        "created_at: 2026-09-05T00:00:00+00:00\n"
        "updated_at: 2026-09-05T00:00:00+00:00\n"
        "---\n"
        "ordinary implementation note\n",
        encoding="utf-8",
    )

    with pytest.raises(ValueError, match="sensitive name"):
        MemoryManager(str(memory_dir))


def test_memory_manager_rejects_sensitive_filename_fallback(tmp_path: Path) -> None:
    memory_dir = tmp_path / "memory"
    memory_dir.mkdir()
    (memory_dir / "api-key.md").write_text(
        "---\n"
        "id: memory-1\n"
        "tags: workflow\n"
        "created_at: 2026-09-05T00:00:00+00:00\n"
        "updated_at: 2026-09-05T00:00:00+00:00\n"
        "---\n"
        "ordinary implementation note\n",
        encoding="utf-8",
    )

    with pytest.raises(ValueError, match="sensitive name"):
        MemoryManager(str(memory_dir))


def test_memory_manager_allows_ordinary_credential_discussion_names(tmp_path: Path) -> None:
    manager = MemoryManager(str(tmp_path / "memory"))

    for name in ("password-rotation-discussion", "secret-design-notes"):
        saved = manager.save(
            Memory(id="", name=name, content="Document the implementation tradeoffs.")
        )
        assert saved.name == name


def test_memory_manager_renaming_removes_stale_file_after_restart(tmp_path: Path) -> None:
    memory_dir = tmp_path / "memory"
    manager = MemoryManager(str(memory_dir))

    saved = manager.save(
        Memory(id="memory-1", name="original-note", content="Keep one durable note.")
    )
    renamed = manager.save(
        Memory(id=saved.id, name="renamed-note", content=saved.content, created_at=saved.created_at)
    )

    assert not (memory_dir / "original-note.md").exists()
    assert (memory_dir / "renamed-note.md").exists()
    assert [item.name for item in manager.list()] == ["renamed-note"]

    restarted = MemoryManager(str(memory_dir))
    assert [item.name for item in restarted.list()] == ["renamed-note"]
    assert restarted.get(saved.id).name == "renamed-note"


def test_memory_manager_accepts_absolute_paths_in_ordinary_notes(tmp_path: Path) -> None:
    manager = MemoryManager(str(tmp_path / "memory"))

    saved = manager.add(r"Changed C:\repo\src\agent.py and /var/lib/codeops/state.db")

    assert r"C:\repo\src\agent.py" in saved.content
    assert "/var/lib/codeops/state.db" in saved.content


def test_runner_reflection_persists_searchable_outcome_excerpt(tmp_path: Path) -> None:
    store = SQLiteContextStore(tmp_path / "context.sqlite")
    context = LayeredContext(store, tmp_path)
    from orchestrator.runtime.conversation import ConversationRunner

    runner = ConversationRunner.__new__(ConversationRunner)
    runner.layered_context = context
    runner._context_persistence_error = ""

    runner._persist_reflection(
        "session-1",
        "Implemented durable worker recovery and verified the checkpoint path.",
        4,
    )

    matches = context.search_memory("worker recovery")
    assert len(matches) == 1
    assert "Implemented durable worker recovery" in matches[0].content
    assert "turn 4" in matches[0].content
    assert "sha256=" in matches[0].content
    store.close()


def test_runner_reflection_omits_sensitive_response(tmp_path: Path) -> None:
    store = SQLiteContextStore(tmp_path / "context.sqlite")
    context = LayeredContext(store, tmp_path)
    from orchestrator.runtime.conversation import ConversationRunner

    runner = ConversationRunner.__new__(ConversationRunner)
    runner.layered_context = context
    runner._context_persistence_error = ""

    runner._persist_reflection(
        "session-1",
        f"Deployment completed with {_CREDENTIAL_LABEL}=fixture-secret",
        2,
    )

    matches = context.search_memory("Conversation outcome")
    assert len(matches) == 1
    assert "fixture-secret" not in matches[0].content
    assert "[sensitive response omitted]" in matches[0].content
    assert "turn 2" in matches[0].content
    assert "sha256=" in matches[0].content
    store.close()


def test_append_allows_repeated_events_when_clock_collides(tmp_path: Path, monkeypatch) -> None:
    store = SQLiteContextStore(tmp_path / "context.sqlite")
    monkeypatch.setattr(memory_module, "_now", lambda: "2026-09-02T00:00:00+00:00")

    first = store.append("session-1", "execution_result", {"status": "same"})
    second = store.append("session-1", "execution_result", {"status": "same"})

    assert first.sequence != second.sequence
    assert first.checksum != second.checksum
    assert len(store.events("session-1")) == 2
    store.close()


def test_concurrent_stores_serialize_same_session_event_chain(tmp_path: Path) -> None:
    database = tmp_path / "context.sqlite"
    stores = [SQLiteContextStore(database), SQLiteContextStore(database)]
    start = threading.Barrier(2)
    errors: list[BaseException] = []

    def append_many(store: SQLiteContextStore, worker: int) -> None:
        try:
            start.wait()
            for index in range(40):
                store.append("shared-session", "execution_result", {"worker": worker, "index": index})
        except BaseException as exc:
            errors.append(exc)

    threads = [
        threading.Thread(target=append_many, args=(store, worker))
        for worker, store in enumerate(stores)
    ]
    for thread in threads:
        thread.start()
    for thread in threads:
        thread.join(timeout=20)

    assert errors == []
    assert all(not thread.is_alive() for thread in threads)
    assert len(stores[0].events("shared-session")) == 80
    for store in stores:
        store.close()


def test_limited_event_replay_verifies_predecessor_anchor(tmp_path: Path) -> None:
    store = SQLiteContextStore(tmp_path / "context.sqlite")
    store.append("session-1", "plan", {"step": 1})
    second = store.append("session-1", "plan", {"step": 2})
    forged_previous = "f" * 64
    forged_checksum = hashlib.sha256(
        memory_module._canonical_payload(
            second.event_id,
            second.kind,
            second.session_id,
            second.payload,
            second.created_at,
            forged_previous,
        ).encode("utf-8")
    ).hexdigest()
    store._connection.execute(
        "UPDATE context_events SET previous_checksum = ?, checksum = ? WHERE event_id = ?",
        (forged_previous, forged_checksum, second.event_id),
    )
    store._connection.commit()

    with pytest.raises(ValueError, match="chain mismatch"):
        store.events("session-1", limit=1)
    store.close()


def test_search_memory_rejects_tampered_rows(tmp_path: Path) -> None:
    database = tmp_path / "context.sqlite"
    store = SQLiteContextStore(database)
    record = store.add_memory("session-1", "checkpoint recovery stays durable", ["workflow"])
    store._connection.execute(
        "UPDATE long_term_memory SET content = ? WHERE id = ?",
        ("tampered content", record.id),
    )
    store._connection.commit()

    with pytest.raises(ValueError, match="memory checksum mismatch"):
        store.search_memory("tampered")
    store.close()


def test_search_memory_rejects_tampered_created_at(tmp_path: Path) -> None:
    store = SQLiteContextStore(tmp_path / "context.sqlite")
    record = store.add_memory("session-1", "ordered durable memory", ["workflow"])
    store._connection.execute(
        "UPDATE long_term_memory SET created_at = ? WHERE id = ?",
        ("2099-01-01T00:00:00+00:00", record.id),
    )
    store._connection.commit()

    with pytest.raises(ValueError, match="memory checksum mismatch"):
        store.search_memory("ordered")
    store.close()


def test_migrate_legacy_memory_is_explicit_idempotent_and_preserves_source(tmp_path: Path) -> None:
    database = tmp_path / "context.sqlite"
    store = SQLiteContextStore(database)
    legacy_id = "legacy-1"
    session_id = "session-legacy"
    content = "legacy durable checkpoint"
    tags = ("workflow", "recovery")
    created_at = "2026-09-01T00:00:00+00:00"
    legacy_payload = {"id": legacy_id, "session_id": session_id, "content": content, "tags": tags}
    legacy_checksum = hashlib.sha256(
        __import__("json").dumps(legacy_payload, ensure_ascii=False, sort_keys=True, separators=(",", ":")).encode()
    ).hexdigest()
    store._connection.execute(
        "INSERT INTO long_term_memory(id, session_id, content, tags_json, created_at, checksum) VALUES (?, ?, ?, ?, ?, ?)",
        (legacy_id, session_id, content, __import__("json").dumps(tags), created_at, legacy_checksum),
    )
    store._connection.commit()

    report = store.migrate_legacy_memory()
    assert report.migrated == 1
    assert report.rejected == 0
    assert store.search_memory("legacy checkpoint")[0].content == content

    again = store.migrate_legacy_memory()
    assert again.migrated == 0
    assert again.skipped == 1
    source = store._connection.execute(
        "SELECT checksum, source_checksum FROM long_term_memory WHERE id = ?", (legacy_id,)
    ).fetchone()
    assert source["checksum"] != legacy_checksum
    assert source["source_checksum"] == legacy_checksum
    store.close()


def test_migrate_legacy_memory_fails_closed_without_partial_commit(tmp_path: Path) -> None:
    store = SQLiteContextStore(tmp_path / "context.sqlite")
    store._connection.execute(
        "INSERT INTO long_term_memory(id, session_id, content, tags_json, created_at, checksum) VALUES (?, ?, ?, ?, ?, ?)",
        ("legacy-bad", "session-legacy", "untrusted", "[]", "2026-09-01T00:00:00+00:00", "bad"),
    )
    store._connection.commit()

    with pytest.raises(ValueError, match="legacy memory checksum"):
        store.migrate_legacy_memory()
    count = store._connection.execute(
        "SELECT COUNT(*) AS n FROM long_term_memory WHERE id LIKE 'legacy-migrated:%'"
    ).fetchone()["n"]
    assert count == 0
    store.close()


def test_search_memory_rejects_tampered_legacy_source_checksum(tmp_path: Path) -> None:
    store = SQLiteContextStore(tmp_path / "context.sqlite")
    record = store.add_memory(
        "session-1",
        "migrated memory provenance",
        source_type="legacy-memory",
        source_id="legacy-1",
        source_revision="legacy-checksum",
    )
    store._connection.execute(
        "UPDATE long_term_memory SET source_checksum = ? WHERE id = ?",
        ("tampered-source", record.id),
    )
    store._connection.commit()

    with pytest.raises(ValueError, match="source checksum mismatch"):
        store.search_memory("provenance")
    store.close()


def test_search_memory_filters_before_applying_limit(tmp_path: Path) -> None:
    store = SQLiteContextStore(tmp_path / "context.sqlite")
    store.add_memory("session-1", "target durable workflow memory", ["target"])
    for index in range(4):
        store.add_memory("session-1", f"unrelated memory {index}", ["noise"])

    matches = store.search_memory("target", limit=1)

    assert len(matches) == 1
    assert matches[0].content.startswith("target durable")
    store.close()


def test_search_memory_ranks_multi_token_matches_before_recent_noise(tmp_path: Path) -> None:
    store = SQLiteContextStore(tmp_path / "context.sqlite")
    store.add_memory(
        "session-1",
        "SQLite checkpoint recovery stays durable",
        ["workflow", "recovery"],
    )
    store.add_memory("session-1", "SQLite migration notes", ["workflow"])

    matches = store.search_memory("SQLite recovery", limit=1)

    assert len(matches) == 1
    assert matches[0].content.startswith("SQLite checkpoint recovery")
    store.close()


def test_memory_provenance_revision_and_expiry_are_persisted_and_indexed(tmp_path: Path) -> None:
    store = SQLiteContextStore(tmp_path / "context.sqlite")
    record = store.add_memory(
        "session-1",
        "pin the deployment rollback procedure",
        ["release"],
        source_type="workflow",
        source_id="deploy-42",
        source_revision="r7",
        revision=3,
        ttl_seconds=3600,
    )

    assert record.source_type == "workflow"
    assert record.source_id == "deploy-42"
    assert record.source_revision == "r7"
    assert record.revision == 3
    assert record.expires_at
    assert store.search_memory("rollback")[0].id == record.id
    store.close()


def test_expired_memory_is_excluded_from_search(tmp_path: Path) -> None:
    store = SQLiteContextStore(tmp_path / "context.sqlite")
    store.add_memory(
        "session-1",
        "temporary incident note",
        source_type="incident",
        source_id="inc-1",
        expires_at="2020-01-01T00:00:00+00:00",
    )

    assert store.search_memory("incident note") == []
    store.close()


def test_initial_messages_bound_long_term_memory_injection(tmp_path: Path) -> None:
    from orchestrator.graph.main_graph import build_graph
    from orchestrator.memory.manager import MemoryManager
    from orchestrator.runtime.conversation import ConversationRunner
    from orchestrator.runtime.tools import ToolRegistry
    from orchestrator.skills.manager import SkillManager
    from orchestrator.todo.manager import TodoManager

    store = SQLiteContextStore(tmp_path / "context.sqlite")
    context = LayeredContext(store, tmp_path)
    for index in range(5):
        context.reflect(
            "session-1",
            f"durable checkpoint memory {index} " + ("x" * 1_200),
            tags=("durable",),
        )
    runner = ConversationRunner(
        graph=build_graph(),
        llm=None,
        tool_registry=ToolRegistry(str(tmp_path)),
        todo_manager=TodoManager(),
        memory_manager=MemoryManager(str(tmp_path / "memory")),
        skills=SkillManager(),
        project_root=str(tmp_path),
        working_dir=str(tmp_path),
        layered_context=context,
    )

    messages = runner._initial_messages("durable checkpoint", 1, session_id="session-1")
    rendered = "\n".join(str(message.content) for message in messages)
    section = rendered.split("Long-term memory:\n", 1)[1].split("\n\n", 1)[0]

    assert len(section) <= 4_096
    assert "older memories omitted" in section
    store.close()


def test_invalid_memory_does_not_discard_verified_event_context(tmp_path: Path) -> None:
    from orchestrator.graph.main_graph import build_graph
    from orchestrator.memory.manager import MemoryManager
    from orchestrator.runtime.conversation import ConversationRunner
    from orchestrator.runtime.tools import ToolRegistry
    from orchestrator.skills.manager import SkillManager
    from orchestrator.todo.manager import TodoManager

    store = SQLiteContextStore(tmp_path / "context.sqlite")
    store.append("session-1", "execution_result", {"status": "verified-event-marker"})
    record = store.add_memory("session-1", "valid memory")
    store._connection.execute(
        "UPDATE long_term_memory SET content = ? WHERE id = ?",
        ("untrusted-memory-marker", record.id),
    )
    store._connection.commit()
    runner = ConversationRunner(
        graph=build_graph(), llm=None, tool_registry=ToolRegistry(str(tmp_path)),
        todo_manager=TodoManager(), memory_manager=MemoryManager(str(tmp_path / "memory")),
        skills=SkillManager(), project_root=str(tmp_path), working_dir=str(tmp_path),
        layered_context=LayeredContext(store, tmp_path),
    )
    try:
        rendered = "\n".join(message.content for message in runner._initial_messages(
            "memory", 1, session_id="session-1",
        ))
        assert "execution_result" in rendered
        assert "untrusted-memory-marker" not in rendered
        assert "Long-term memory unavailable: ValueError" in rendered
        assert "Event-sourced context unavailable" not in rendered
        with pytest.raises(ValueError, match="checksum"):
            store.search_memory("memory")
    finally:
        store.close()


def test_layered_context_bounds_events_and_redacts_credential_shapes(tmp_path: Path) -> None:
    store = SQLiteContextStore(tmp_path / "context.sqlite")
    provider_key_prefix = "sk-" + "proj-"
    aws_key = "AKIA" + "1234567890ABCDEF"
    for index in range(5):
        store.append(
            "session-1",
            "execution_result",
            {
                "status": "ok",
                "output": f"event-{index} {provider_key_prefix}secretvalue{index} {aws_key}",
            },
        )
    context = LayeredContext(store, tmp_path, max_events=2, max_event_chars=100)

    summary = context.load("session-1")

    assert "#5 execution_result" in summary.events_text
    assert "#1 execution_result" not in summary.events_text
    assert provider_key_prefix + "secretvalue" not in summary.events_text
    assert aws_key not in summary.events_text
    assert len(summary.events_text) < 600
    store.close()


def test_layered_context_reads_only_bounded_raw_bytes_and_skips_symlink(tmp_path: Path) -> None:
    source = tmp_path / "source.txt"
    source.write_text("0123456789" * 1000, encoding="utf-8")
    linked = tmp_path / "linked.txt"
    try:
        linked.symlink_to(source)
    except (OSError, NotImplementedError):
        pytest.skip("symlink creation is unavailable")

    store = SQLiteContextStore(tmp_path / "context.sqlite")
    context = LayeredContext(store, tmp_path, max_raw_bytes=32)
    summary = context.load("session-1", raw_paths=["source.txt", "linked.txt"])

    assert len(summary.p3["source.txt"].encode("utf-8")) < 128
    assert "[raw content truncated]" in summary.p3["source.txt"]
    assert "linked.txt" not in summary.p0
    store.close()


def test_layered_context_preserves_utf8_when_raw_budget_splits_character(tmp_path: Path) -> None:
    source = tmp_path / "source.txt"
    source.write_text("你好世界", encoding="utf-8")
    store = SQLiteContextStore(tmp_path / "context.sqlite")
    context = LayeredContext(store, tmp_path, max_raw_bytes=5)

    summary = context.load("session-1", raw_paths=["source.txt"])

    raw = summary.p3["source.txt"]
    assert "你" in raw
    assert "\ufffd" not in raw
    assert "[raw content truncated]" in raw
    store.close()


def test_layered_context_omits_invalid_utf8_instead_of_silently_truncating(
    tmp_path: Path,
) -> None:
    source = tmp_path / "binary.dat"
    source.write_bytes(b"valid-prefix\xffinvalid-suffix")
    store = SQLiteContextStore(tmp_path / "context.sqlite")
    context = LayeredContext(store, tmp_path, max_raw_bytes=64)

    summary = context.load("session-1", raw_paths=["binary.dat"])

    assert summary.p3["binary.dat"] == "[binary file omitted]"
    store.close()


def test_layered_context_marks_unreadable_explicit_file_without_dropping_p0(
    tmp_path: Path, monkeypatch
) -> None:
    source = tmp_path / "source.txt"
    source.write_text("content", encoding="utf-8")
    store = SQLiteContextStore(tmp_path / "context.sqlite")
    context = LayeredContext(store, tmp_path)
    monkeypatch.setattr(
        context,
        "_file_summary",
        lambda _path: (_ for _ in ()).throw(OSError("denied")),
    )

    summary = context.load("session-1", raw_paths=["source.txt"])

    assert "source.txt" in summary.p0
    assert "source.txt [unavailable: OSError]" in summary.p1
    assert summary.p3["source.txt"] == "[file unavailable]"
    store.close()
