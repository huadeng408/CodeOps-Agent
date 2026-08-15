# Claude-Mem L3 Thin Adapter Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the enabled-mode stub with a loopback-only, bounded, fail-open progressive-read adapter while keeping every evaluation path offline from L3.

**Architecture:** `ClaudeMemClient` owns the L3 boundary. Its production path uses one short-lived `httpx.AsyncClient`; offline tests inject `httpx.MockTransport`. An enabled request performs a project-filtered index search then a batch fetch of at most three selected IDs. The installed worker returns one raw observation object for one ID and a raw object list for multiple IDs, so parsing accepts both proven shapes plus an upstream `observations` wrapper only after strict ID/project validation. Disabled mode returns before client construction.

**Tech Stack:** Python 3.12, `httpx>=0.27,<1`, pytest, pytest-asyncio, Claude-Mem 13.15.0 loopback worker, existing OpenTelemetry/Phoenix conventions.

---

## File Map

- Modify `orchestrator/rag/claude_mem.py`: validation, progressive reads, sanitization, output cap, safe outcome.
- Modify `tests/test_claude_mem.py`: fully offline behavior tests using `httpx.MockTransport`.
- Create `scripts/verify-claude-mem-l3.ps1`: a read-only real-worker acceptance helper.
- Create `tests/test_verify_claude_mem_l3_script.py`: static guardrails for the helper.
- Modify evidence-only after a real run: `docs/PROGRESS-2026-08-15.md` and `D:\Obsidian\code-autogrowth\私人\localcode\PROGRESS-2026-08-15.md`.

No Go, Redis, MySQL, MinIO, Elasticsearch, Claude-Mem SQLite, or Chroma change is in scope.

### Task 1: Prove Disabled Mode Makes Zero Requests

**Files:** Modify `tests/test_claude_mem.py`; modify `orchestrator/rag/claude_mem.py`.

- [ ] **Step 1: Write the failing test**

```python
import httpx

@pytest.mark.asyncio
async def test_disabled_mode_returns_empty_without_calling_worker() -> None:
    requests: list[httpx.Request] = []
    def handler(request: httpx.Request) -> httpx.Response:
        requests.append(request)
        raise AssertionError("disabled mode must not request worker")
    client = ClaudeMemClient("http://127.0.0.1:37777", transport=httpx.MockTransport(handler))
    assert await client.context("localcode", memory_mode="disabled") == ""
    assert requests == []
```

- [ ] **Step 2: Verify RED**

Run `C:\Python312\python.exe -m pytest tests\test_claude_mem.py::test_disabled_mode_returns_empty_without_calling_worker -q`.

Expected: `FAIL` because `ClaudeMemClient.__init__` has no `transport` parameter.

- [ ] **Step 3: Implement the smallest seam**

```python
class ClaudeMemClient:
    def __init__(self, worker_url: str, *, transport: httpx.AsyncBaseTransport | None = None, timeout_seconds: float = 1.0) -> None:
        self.worker_url = worker_url.rstrip("/")
        self._transport = transport
        self._timeout_seconds = timeout_seconds

    async def context(self, project: str, *, query: str = "*", memory_mode: str = "disabled") -> str:
        if memory_mode != "enabled":
            return ""
        return ""
```

- [ ] **Step 4: Verify GREEN**

Run `C:\Python312\python.exe -m pytest tests\test_claude_mem.py -q`.

Expected: all adapter tests pass; enabled mode remains empty until Task 3.

- [ ] **Step 5: Commit**

Run `git add orchestrator/rag/claude_mem.py tests/test_claude_mem.py` then `git commit -m "test(memory): prove Claude-Mem disabled mode is offline"`.

### Task 2: Validate Boundary Inputs Before Any Request

**Files:** Modify `tests/test_claude_mem.py`; modify `orchestrator/rag/claude_mem.py`.

- [ ] **Step 1: Write failing invalid-input tests**

```python
@pytest.mark.asyncio
@pytest.mark.parametrize(
    ("worker_url", "project", "query"),
    [
        ("http://10.0.0.8:37777", "localcode", "*"),
        ("https://example.test", "localcode", "*"),
        ("http://127.0.0.1:37777", "../../other", "*"),
        ("http://127.0.0.1:37777", "localcode", "x" * 257),
    ],
)
async def test_invalid_boundary_returns_empty_without_request(worker_url: str, project: str, query: str) -> None:
    transport = httpx.MockTransport(lambda _: (_ for _ in ()).throw(AssertionError("must not request")))
    assert await ClaudeMemClient(worker_url, transport=transport).context(project, query=query, memory_mode="enabled") == ""
```

- [ ] **Step 2: Verify RED**

Run `C:\Python312\python.exe -m pytest tests\test_claude_mem.py -q`.

Expected: `FAIL` because enabled mode has no boundary validator.

- [ ] **Step 3: Implement only validation**

```python
_PROJECT_RE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$")
_LOOPBACK_HOSTS = {"127.0.0.1", "::1", "localhost"}

def _is_loopback_worker_url(value: str) -> bool:
    parsed = urllib.parse.urlparse(value)
    return parsed.scheme == "http" and parsed.hostname in _LOOPBACK_HOSTS and not parsed.username and not parsed.password

def _valid_request(project: str, query: str) -> bool:
    return bool(_PROJECT_RE.fullmatch(project)) and bool(query.strip()) and len(query) <= 256
```

Call the validators before `httpx.AsyncClient` allocation; invalid values return empty.

- [ ] **Step 4: Verify GREEN and commit**

Run `C:\Python312\python.exe -m pytest tests\test_claude_mem.py -q`, then commit only the adapter and test as `feat(memory): validate Claude-Mem loopback boundary`.

### Task 3: Add the Two-Stage Progressive Read

**Files:** Modify `tests/test_claude_mem.py`; modify `orchestrator/rag/claude_mem.py`.

- [ ] **Step 1: Write the failing protocol test**

```python
@pytest.mark.asyncio
async def test_enabled_mode_searches_then_fetches_selected_project_ids() -> None:
    requests: list[httpx.Request] = []
    def handler(request: httpx.Request) -> httpx.Response:
        requests.append(request)
        if request.url.path == "/api/search":
            assert dict(request.url.params) == {"query": "adapter design", "project": "localcode", "limit": "3"}
            return httpx.Response(200, json={"content": [{"type": "text", "text": "| #385 | design |"}]})
        assert request.url.path == "/api/observations/batch"
        assert json.loads(request.content) == {"ids": [385], "project": "localcode"}
        return httpx.Response(200, json={"id": 385, "project": "localcode", "title": "L3 design"})
    client = ClaudeMemClient("http://127.0.0.1:37777", transport=httpx.MockTransport(handler))
    assert await client.context("localcode", query="adapter design", memory_mode="enabled") == "[claude-mem:385] L3 design"
    assert [request.url.path for request in requests] == ["/api/search", "/api/observations/batch"]
```

- [ ] **Step 2: Verify RED**

Run `C:\Python312\python.exe -m pytest tests\test_claude_mem.py::test_enabled_mode_searches_then_fetches_selected_project_ids -q`.

Expected: `FAIL` with an empty result because the worker protocol is missing.

- [ ] **Step 3: Implement minimal progressive reads under one deadline**

```python
started = time.monotonic()
async with httpx.AsyncClient(base_url=self.worker_url, transport=self._transport, timeout=httpx.Timeout(self._timeout_seconds), trust_env=False) as http:
    search = await http.get("/api/search", params={"query": query, "project": project, "limit": 3})
    search.raise_for_status()
    ids = _observation_ids(search.json())[:3]
    if not ids:
        return ""
    remaining = self._timeout_seconds - (time.monotonic() - started)
    if remaining <= 0:
        return ""
    batch = await http.post("/api/observations/batch", json={"ids": ids, "project": project}, timeout=httpx.Timeout(remaining))
    batch.raise_for_status()
    return _render_safe_context(batch.json(), project, ids)
```

Extract only numeric IDs from `content[].text`. Normalize the batch JSON to a sequence only when it is a raw list, one object with an integer `id`, or an `observations` list; reject all other shapes. Then require every rendered record to have one requested numeric ID and the requested project. Catch `httpx.HTTPError`, malformed JSON, and deadline exhaustion in `context()` and return empty. Do not call `/api/context/inject`, `/api/sessions/*`, SQLite, Chroma, or any write route.

- [ ] **Step 4: Verify GREEN and commit**

Run `C:\Python312\python.exe -m pytest tests\test_claude_mem.py -q`, then commit only this slice as `feat(memory): add bounded Claude-Mem progressive read`.

Expected: the fixture makes exactly two requests.

### Task 4: Reject Unsafe, Malformed, and Oversized Results

**Files:** Modify `tests/test_claude_mem.py`; modify `orchestrator/rag/claude_mem.py`.

- [ ] **Step 1: Write failing privacy and byte-limit tests**

```python
@pytest.mark.asyncio
@pytest.mark.parametrize("unsafe_text", ["OPENAI_API_KEY=sk-secret-value", "Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.payload.signature", "C:\\Users\\private\\notes.txt", "-----BEGIN PRIVATE KEY-----"])
async def test_sensitive_worker_result_fails_open(unsafe_text: str) -> None:
    assert await client_returning_observation(unsafe_text).context("localcode", memory_mode="enabled") == ""

@pytest.mark.asyncio
async def test_context_ends_at_utf8_boundary_under_4096_bytes() -> None:
    context = await client_returning_observation("中" * 3000).context("localcode", memory_mode="enabled")
    assert len(context.encode("utf-8")) <= 4096
    assert context.encode("utf-8").decode("utf-8") == context
```

Add separate fixtures for `httpx.ReadTimeout`, HTTP 500, invalid JSON, project mismatch, missing ID, and no selected IDs. Each returns empty without leaking the response body.

- [ ] **Step 2: Verify RED**

Run `C:\Python312\python.exe -m pytest tests\test_claude_mem.py -q`.

Expected: `FAIL` because raw worker output is not yet rejected or bounded.

- [ ] **Step 3: Implement all-or-empty rendering**

```python
def _contains_sensitive_content(value: str) -> bool:
    return any(pattern.search(value) for pattern in _SENSITIVE_PATTERNS)

def _truncate_utf8(value: str, maximum_bytes: int = 4096) -> str:
    encoded = value.encode("utf-8")[:maximum_bytes]
    while encoded:
        try:
            return encoded.decode("utf-8")
        except UnicodeDecodeError:
            encoded = encoded[:-1]
    return ""
```

Require matching project and numeric ID for every rendered record. Scan complete output before returning it; any sensitive match returns empty. Never partially redact, log, persist, or attach raw body to an exception.

- [ ] **Step 4: Verify GREEN and commit**

Run `C:\Python312\python.exe -m pytest tests\test_claude_mem.py tests\test_audio_rag.py tests\test_trace_schema.py -q`, then commit only this slice as `fix(memory): fail open on unsafe Claude-Mem results`.

### Task 5: Add Safe Observability Metadata

**Files:** Modify `tests/test_claude_mem.py`; modify `orchestrator/rag/claude_mem.py`.

- [ ] **Step 1: Write the failing allowlist test**

```python
def test_memory_outcome_attributes_exclude_content_query_and_path() -> None:
    outcome = MemoryReadOutcome("claude_mem", 17, (385, 404), None)
    assert outcome.telemetry_attributes() == {
        "memory.backend": "claude_mem",
        "memory.latency_ms": 17,
        "memory.result_count": 2,
        "memory.citation_ids": "385,404",
    }
```

- [ ] **Step 2: Verify RED**

Run `C:\Python312\python.exe -m pytest tests\test_claude_mem.py::test_memory_outcome_attributes_exclude_content_query_and_path -q`.

Expected: collection/import `FAIL` because `MemoryReadOutcome` does not exist.

- [ ] **Step 3: Implement the pure allowlisted object**

```python
@dataclass(frozen=True)
class MemoryReadOutcome:
    backend: str
    latency_ms: int
    citation_ids: tuple[int, ...]
    failure_category: str | None = None

    def telemetry_attributes(self) -> dict[str, str | int]:
        result: dict[str, str | int] = {
            "memory.backend": self.backend,
            "memory.latency_ms": self.latency_ms,
            "memory.result_count": len(self.citation_ids),
            "memory.citation_ids": ",".join(map(str, self.citation_ids)),
        }
        if self.failure_category:
            result["memory.failure_category"] = self.failure_category
        return result
```

The adapter exposes this only to its immediate caller; it does not emit a span or persist context. Test the exact key set and absence of input strings.

- [ ] **Step 4: Verify GREEN and commit**

Run `C:\Python312\python.exe -m pytest tests\test_claude_mem.py -q`, then commit only this slice as `feat(memory): expose safe Claude-Mem telemetry outcome`.

### Task 6: Add Read-Only Acceptance Tooling

**Files:** Create `scripts/verify-claude-mem-l3.ps1`; create `tests/test_verify_claude_mem_l3_script.py`.

- [ ] **Step 1: Write failing static guard tests**

```python
def test_l3_acceptance_helper_is_read_only() -> None:
    script = Path("scripts/verify-claude-mem-l3.ps1").read_text(encoding="utf-8")
    assert "/health" in script
    assert "/api/search" in script
    assert "/api/observations/batch" in script
    assert "/api/sessions/" not in script
    assert "sqlite" not in script.lower()
    assert "chroma" not in script.lower()

def test_l3_receipt_excludes_content_and_secrets() -> None:
    script = Path("scripts/verify-claude-mem-l3.ps1").read_text(encoding="utf-8")
    assert "worker_version" in script and "elapsed_ms" in script and "citation_ids" in script
    assert "observation_text" not in script
```

- [ ] **Step 2: Verify RED**

Run `C:\Python312\python.exe -m pytest tests\test_verify_claude_mem_l3_script.py -q`.

Expected: `FAIL` because the script is missing.

- [ ] **Step 3: Implement only a read-only helper**

Accept `-Project`, `-Query`, `-WorkerUrl` (default `http://127.0.0.1:37777`), and `-ReceiptDirectory`. Validate loopback and project before requesting. Call `/health`, search with `limit=3`, and conditionally batch fetch IDs. Write a receipt containing only `worker_version`, `endpoint`, `elapsed_ms`, `result_count`, `citation_ids`, and `result`. Never create an observation, fetch a full timeline, include query/body content, or access local storage.

- [ ] **Step 4: Verify GREEN and commit**

Run `C:\Python312\python.exe -m pytest tests\test_verify_claude_mem_l3_script.py tests\test_claude_mem.py -q`. Then run `pwsh -File scripts\verify-claude-mem-l3.ps1 -Project localcode -Query "L3 adapter" -ReceiptDirectory eval_results\memory\claude-mem-l3-acceptance`. Commit only the script and test as `test(memory): add read-only Claude-Mem acceptance helper`.

Expected: static tests pass. A zero-result receipt remains `BLOCKED`, not `PASS`.

### Task 7: Real Cross-Session Acceptance And Evidence

**Files:** Modify both progress notes only after a real worker run; create ignored receipt under `eval_results/memory/claude-mem-l3-acceptance/`.

- [ ] **Step 1: Establish the plugin-owned write precondition**

Use the installed plugin in a normal permitted Claude/Codex session to generate one ordinary non-secret `localcode` observation. Do not invoke `/api/sessions/*`, seed SQLite/Chroma, or use repository code to fabricate that write. Record only a numeric ID and a non-sensitive marker outside its body.

- [ ] **Step 2: Verify a new-session read through the adapter**

Run `C:\Python312\python.exe -m pytest tests\test_claude_mem.py tests\test_verify_claude_mem_l3_script.py -q` and `pwsh -File scripts\verify-claude-mem-l3.ps1 -Project localcode -Query "<non-secret marker>" -ReceiptDirectory eval_results\memory\claude-mem-l3-acceptance`.

Expected: receipt includes citation IDs, elapsed time, result count, version, and loopback endpoint, but no observation content.

- [ ] **Step 3: Prove privacy and evaluation-disable gates**

Execute the secret-shaped fixture and assert empty. Execute official harness, scorer, qrel, holdout, and contamination call paths with disabled mode plus an instrumented fake transport; assert zero worker calls. Source search is not evidence.

- [ ] **Step 4: Record accurate status and verify final scope**

Write exact commands, exits, receipt paths, L3 data impact (`none`), rollback, and any blocker to both progress notes. Mark L3 `IMPLEMENTED` only after normal-session write and fresh-session adapter read; otherwise preserve `BLOCKED`. Run `C:\Python312\python.exe -m pytest tests\test_claude_mem.py tests\test_verify_claude_mem_l3_script.py tests\test_audio_rag.py tests\test_trace_schema.py -q`, `git diff --check`, and `git status --short`; stage only task-owned files. Never commit runtime caches, plugin data, worker databases, models, raw observations, or secret-bearing receipts.

## Plan Self-Review

- Covers loopback-only access, two-stage reads, one-second deadline, 4 KB cap, all-or-empty sensitive handling, disabled evaluation, allowlisted metadata, read-only real-worker acceptance, evidence, rollback, and Obsidian mirror.
- Every production slice starts with a focused failing test, states the expected failure, provides an implementation shape, requires focused verification, and has a bounded commit.
- It never directly reads SQLite/Chroma, writes L3, copies L3 into L2, or allows L3 into benchmark, qrel, holdout, scorer, or contamination inputs.
