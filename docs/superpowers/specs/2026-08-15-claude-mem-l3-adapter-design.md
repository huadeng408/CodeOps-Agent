# Claude-Mem L3 Thin Adapter Design

**Status:** approved architecture; implementation requires this written-spec review

## Purpose

Add a bounded, optional L3 memory read to the project without making
Claude-Mem an application datastore. L1 remains Redis session/TTL state. L2
remains MySQL, MinIO, and Elasticsearch application and RAG truth. L3 is the
locally installed `thedotmack/claude-mem` 13.15.0 sidecar, used only to recall
project-scoped developer/agent observations across sessions.

This design closes the current deliberate stub in
`orchestrator/rag/claude_mem.py`: enabled mode currently raises
`Claude-Mem progressive read is not configured`. It does not make any claim
that a three-layer end-to-end flow already exists.

## Selected Approach

Three integration choices were considered:

1. Read Claude-Mem's SQLite/Chroma storage directly. Rejected: it couples this
   repository to unversioned plugin internals, bypasses the plugin's privacy
   and progressive-read boundary, and would expose unrelated projects.
2. Copy or fork Claude-Mem storage and search code. Rejected: it duplicates a
   mature upstream component while making security, migration, and resource
   ownership this project's responsibility.
3. Use the running loopback worker's supported HTTP/MCP surface as a thin,
   fail-open client. Selected: the installed plugin documents a progressive
   `search -> get_observations` sequence and its MCP server maps that to
   `GET /api/search` followed by `POST /api/observations/batch`.

The adapter owns request validation, bounded projection, and fail-open
behavior. Claude-Mem owns indexing, storage, search, and observation format.

## Scope And Non-Goals

In scope:

- Read only from `http://127.0.0.1:37777`; a constructor may accept another
  URL only when it resolves to loopback.
- Search with an exact project filter, then fetch only selected observation IDs
  through the worker's batch endpoint.
- Return a sanitized, citation-bearing context string for an enabled,
  non-evaluation call.
- Emit only safe observability attributes: backend, elapsed milliseconds,
  result count, citation IDs, and failure category.

Out of scope:

- Writes to Claude-Mem, direct SQLite/Chroma access, cloud synchronization,
  sharing Redis, or importing L3 data into L2/RAG indexes.
- Changing Claude-Mem privacy filtering, its model provider, or its process
  lifecycle.
- Making benchmark performance claims or sending L3 context to any benchmark,
  qrel, holdout, scorer, contamination, or official-harness command.
- Adding a new browser/API service. The first project-facing caller remains
  the existing Python orchestration boundary; the Go CLI read-only status
  command is a later, separately tested task.

## Data Flow

```text
enabled project call
  -> validate loopback URL, project, and query
  -> GET /api/search?project=<project>&query=<query>&limit=3
  -> parse only observation IDs from worker-rendered index
  -> POST /api/observations/batch { ids: [...], project: <project> }
  -> reject suspicious payloads; project-check returned records
  -> project citation IDs plus bounded safe text (maximum 4096 UTF-8 bytes)
  -> caller

disabled/evaluation call -> "" with no network request
timeout, malformed payload, 4xx/5xx, privacy hit -> "" with safe failure metadata
```

The public method evolves compatibly to:

```python
async def context(
    self,
    project: str,
    *,
    query: str = "*",
    memory_mode: str = "disabled",
) -> str:
    ...
```

Only the literal value `enabled` permits a network call. The default remains
disabled. Call sites for official evaluation must pass `disabled` explicitly
or retain the default; a benchmark mode never silently falls back to enabled.

## Worker Contract

The adapter uses only these observed, local worker routes:

| Operation | Method and endpoint | Bound |
| --- | --- | --- |
| Health acceptance probe | `GET /health` | manual acceptance only; not needed before every read |
| Stage one | `GET /api/search` with `query`, `project`, and `limit=3` | index only; no full timeline |
| Stage two | `POST /api/observations/batch` with `ids` and `project` | only IDs selected from stage one |

Both requests share a one-second monotonic deadline. A second request receives
only the remaining time. The adapter must not retry: retrying hides an
unhealthy sidecar, amplifies load, and violates the caller's latency budget.
Only HTTP URLs whose host parses as `127.0.0.1`, `::1`, or `localhost` are
accepted. The client must reject all other hosts before opening a connection.

`project` must match `^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`. `query` is
trimmed, nonempty, at most 256 characters, and transmitted as data rather than
interpolated into a URL string. Invalid inputs return empty context without a
request. The result cap is three observations and the output cap is 4096 UTF-8
bytes, truncated only on a UTF-8 boundary.

## Sanitization And Privacy Boundary

The client treats L3 output as untrusted. It must return `""` for the entire
call, rather than a partially redacted result, when either worker response
contains:

- API-key, bearer-token, PEM/private-key, password/secret assignment, or
  connection-string shapes;
- an absolute user/profile path such as `C:\\Users\\...`, `/home/...`, or a
  UNC profile path;
- a project mismatch or an observation record without a stable numeric ID.

The same detector is applied before output construction and before telemetry.
No raw observation title, narrative, query, project path, secret candidate, or
response body enters Phoenix. The output may contain only sanitized observation
text plus explicit citation labels such as `[claude-mem:385]`; telemetry may
contain only `memory.backend=claude_mem`, `memory.latency_ms`,
`memory.result_count`, `memory.citation_ids`, and one enumerated failure
category (`disabled`, `invalid_input`, `timeout`, `http_error`,
`malformed_response`, `privacy_rejected`).

L3 text is not persisted by this project. In particular, it is never written
to Redis, MySQL, MinIO, Elasticsearch, a prompt/evaluation artifact, or a
Phoenix attribute.

## Failure Policy

This adapter is intentionally fail-open. A connection error, timeout, invalid
JSON, unexpected worker schema, 4xx/5xx response, non-loopback configuration,
or privacy detector hit yields `""`. It must not throw into normal RAG or
agent execution. Programmer errors in local validation may be unit-tested but
the public `context()` method still converts them to the empty result boundary.

The worker's `activeSessions=0` health value does not make L3 unavailable; it
only means no plugin generation is currently active. Acceptance requires real
written and recalled content in separate sessions, not merely this health
response.

## Test-First Acceptance Criteria

Focused tests must be written and observed failing before production changes:

1. `memory_mode="disabled"` returns empty and the fake transport records zero
   calls.
2. An enabled call performs the search then batch sequence, returns citation
   IDs, strips output to the 4096-byte bound, and uses the project filter on
   both stages.
3. Timeout, connection error, 5xx, malformed payload, project mismatch, and
   sensitive output each return empty without leaking raw content.
4. A benchmark/official-evaluation caller using disabled mode records zero
   worker calls even if a worker is available.
5. Telemetry projection contains only the listed allowlist fields.

After focused tests pass, run the repository's relevant Python regression
suite. Then perform a real local acceptance: a permitted Claude/Codex session
creates an allowed observation; a fresh session retrieves it through the two
worker endpoints and the adapter; a private/secrets fixture remains absent;
disabled benchmark mode makes no request; and a receipt records worker version,
loopback-only endpoint, command hashes, elapsed time, result count, and local
CPU/RAM without recording observation text or secrets. Record that receipt in
the repository and the corresponding Obsidian progress note.

## Rollback

The implementation is reversible by retaining `memory_mode="disabled"` or
removing the adapter call site. It creates no database schema, cache, index,
model, worker process, or data migration. No rollback action may delete
Claude-Mem observations, Chroma data, containers, volumes, existing receipts,
or user-local plugin configuration.
