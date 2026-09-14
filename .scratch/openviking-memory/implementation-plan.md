# OpenViking Memory Phase One Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extend the existing Markdown memory managers with compatible namespace, kind, detail, provenance, deterministic budgeted recall, and Session-Ledger audit sinks.

**Architecture:** Go and Python retain their current filesystem stores and public CRUD APIs. New metadata is normalized at the manager boundary, Recall is a deterministic read model over current records, and lifecycle events are sent through an injected sink rather than persisted in a competing ledger.

**Tech Stack:** Go standard library, Python 3.11+, pytest, Markdown YAML-like frontmatter, SHA-256.

**Spec:** `.scratch/openviking-memory/spec.md`

## Global Constraints

- Session Ledger remains the only writable source of Session facts; do not create `memory-events.jsonl` or another event database.
- Existing Markdown records without new fields load as `namespace=user`, `kind=default`, `detail=full`.
- Do not execute scripts from either OpenViking source directory.
- All sensitive fields fail closed before files or audit events are written.
- Stage exact paths only, scan the staged diff for secrets, commit, push, and verify the remote SHA at each meaningful phase.

---

### Task 1: Compatible Memory Metadata

**Files:**
- Modify: `internal/memory/manager.go`
- Modify: `orchestrator/memory/manager.py`
- Modify: `orchestrator/memory/__init__.py`
- Test: `tests/go/memory_test.go`
- Test: `tests/test_context_memory.py`

**Interfaces:**
- Produces: Go `Memory` fields `Namespace`, `Kind`, `Detail`, `SourceURI`, `SourceChecksum`, `SessionID`, `Checksum`.
- Produces: Python `Memory` fields `namespace`, `kind`, `detail`, `source_uri`, `source_checksum`, `session_id`, `checksum`.
- Produces: normalized defaults `user/default/full` and SHA-256 memory checksum.

- [ ] **Step 1: Write failing legacy/default and metadata round-trip tests**

```go
if got.Namespace != "user" || got.Kind != "default" || got.Detail != "full" {
    t.Fatalf("legacy defaults missing: %+v", got)
}
```

```python
assert (loaded.namespace, loaded.kind, loaded.detail) == ("user", "default", "full")
assert loaded.source_checksum == hashlib.sha256(source).hexdigest()
```

- [ ] **Step 2: Run tests and verify RED**

Run: `go test ./tests/go -run 'Memory.*(Metadata|Legacy)' -count=1`

Run: `python -m pytest -q tests/test_context_memory.py -k 'memory_manager and (metadata or legacy_defaults)'`

Expected: FAIL because the new fields and normalization do not exist.

- [ ] **Step 3: Implement minimal metadata normalization and frontmatter compatibility**

Use literals `user`, `default`, and `full`; accept detail only from `abstract`, `overview`, `full`; require source URI and checksum together; require lowercase 64-character hexadecimal SHA-256.

- [ ] **Step 4: Run focused tests and verify GREEN**

Run the commands from Step 2; expect PASS.

### Task 2: Deterministic Budgeted Recall

**Files:**
- Modify: `internal/memory/manager.go`
- Modify: `orchestrator/memory/manager.py`
- Test: `tests/go/memory_test.go`
- Test: `tests/test_context_memory.py`

**Interfaces:**
- Produces: Go `RecallOptions`, `RecallEntry`, `RecallStats`, `RecallResult`, and `Manager.Recall(query string, options RecallOptions) (RecallResult, error)`.
- Produces: Python `RecallOptions`, `RecallEntry`, `RecallStats`, `RecallResult`, and `MemoryManager.recall(query, options=None)`.
- Consumes: normalized Memory metadata from Task 1.

- [ ] **Step 1: Write failing filtering, ranking, limit, and max-token tests**

```go
result, err := manager.Recall("workflow recovery", memory.RecallOptions{Namespace: "project", Limit: 1})
if err != nil || len(result.Entries) != 1 || result.Entries[0].Memory.Name != "exact-match" {
    t.Fatalf("unexpected recall: %+v %v", result, err)
}
```

```python
result = manager.recall("workflow recovery", RecallOptions(namespace="project", limit=1))
assert [entry.memory.name for entry in result.entries] == ["exact-match"]
assert result.stats.returned == 1
```

- [ ] **Step 2: Run tests and verify RED**

Run: `go test ./tests/go -run 'MemoryRecall' -count=1`

Run: `python -m pytest -q tests/test_context_memory.py -k 'memory_recall'`

Expected: FAIL because Recall types and methods do not exist.

- [ ] **Step 3: Implement deterministic scoring and whole-entry budget trimming**

Score exact phrase and token occurrences across name, tags, metadata, then content; sort by score descending, updated time descending, name ascending. Estimate tokens as `max(1, ceil(utf8_bytes/4))`; never truncate a Memory body.

- [ ] **Step 4: Preserve `LoadRelevant/load_relevant` through default Recall**

Legacy calls return the Memory values from Recall entries with no filters or budget.

- [ ] **Step 5: Run focused tests and verify GREEN**

Run the commands from Step 2 plus existing memory-manager tests; expect PASS.

### Task 3: Session-Ledger Audit Sink

**Files:**
- Modify: `internal/memory/manager.go`
- Modify: `orchestrator/memory/manager.py`
- Modify: `orchestrator/memory/__init__.py`
- Test: `tests/go/memory_test.go`
- Test: `tests/test_context_memory.py`

**Interfaces:**
- Produces: Go `MemoryEvent`, `AuditSink`, and `NewManagerWithAudit(dir string, sink AuditSink)`.
- Produces: Python `MemoryEvent` and optional `audit_sink: Callable[[MemoryEvent], None]` constructor argument.
- Event actions: `save` and `delete`; an Add is a Save whose previous record is absent.

- [ ] **Step 1: Write failing sanitized-event and sink-failure tests**

```python
with pytest.raises(RuntimeError, match="ledger unavailable"):
    manager.add("safe derived knowledge")
assert manager.list() == []
assert list(memory_dir.glob("*.md")) == [memory_dir / "MEMORY.md"]
```

- [ ] **Step 2: Run tests and verify RED**

Run: `go test ./tests/go -run 'MemoryAudit' -count=1`

Run: `python -m pytest -q tests/test_context_memory.py -k 'memory_audit'`

Expected: FAIL because audit sinks and events do not exist.

- [ ] **Step 3: Implement fail-closed event emission**

Build the sanitized event before mutation. For save, write a temporary file, emit the sink event, atomically replace the target, remove a stale renamed path, then update the index. For delete, emit before removal. Events contain no Memory content or tags.

- [ ] **Step 4: Run focused tests and verify GREEN**

Run the commands from Step 2; expect PASS.

### Task 4: Conversation Integration and Verification

**Files:**
- Modify: `orchestrator/runtime/conversation.py`
- Test: `tests/test_context_memory.py`
- Test: `tests/test_server.py`

**Interfaces:**
- Consumes: `MemoryManager.recall` from Task 2.
- Produces: prompt-time recall capped to five entries and the existing Memory rendering shape.

- [ ] **Step 1: Write a failing test proving conversation recall is bounded**

```python
messages = runner._initial_messages("workflow", 1, session_id="session-1")
assert rendered_memory_count(messages) == 5
```

- [ ] **Step 2: Run the test and verify RED**

Run: `python -m pytest -q tests/test_context_memory.py -k 'conversation_memory_recall_budget'`

Expected: FAIL because conversation still calls unbounded `load_relevant`.

- [ ] **Step 3: Switch conversation assembly to Recall with an explicit limit and token budget**

Use `RecallOptions(limit=5, max_tokens=1200)` and pass only the returned Memory values to the existing renderer.

- [ ] **Step 4: Run targeted and broader verification**

Run: `go test ./internal/memory ./tests/go -count=1`

Run: `python -m pytest -q tests/test_context_memory.py tests/test_agent_loop_plugins.py tests/test_server.py`

Run: `git diff --check`

Expected: all PASS; no new warnings attributable to this change.

- [ ] **Step 5: Stage exact paths, scan, commit, push, and verify**

Stage only Memory implementation/tests, `CONTEXT.md`, and `.scratch/openviking-memory/**`. Scan `git diff --cached --binary` for credential shapes without printing values. Commit as `feat(memory): add namespaced budgeted recall`, push `origin main`, and require remote SHA to equal local HEAD.
