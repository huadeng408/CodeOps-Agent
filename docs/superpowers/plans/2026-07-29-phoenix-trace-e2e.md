# Phoenix Cross-Language Trace E2E Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an opt-in Docker-backed test that drives the real Go CLI through the real Python orchestrator and real DeepSeek API, then proves the cross-language trace and parent chain were persisted in Phoenix.

**Architecture:** A standard-library Python helper owns model discovery plus Phoenix REST parsing, polling, and assertions. A PowerShell runner owns credentials, Docker lifecycle, a temporary agent workspace, the live agent process, timeouts, diagnostics, and cleanup. Default pytest and Go suites remain network-free; only `scripts/test-trace-e2e.ps1` performs the real integration.

**Tech Stack:** Windows PowerShell 5.1+, Python 3.11 standard library, pytest, Go, gRPC, OpenTelemetry OTLP/HTTP, Docker Compose, Phoenix REST v1, DeepSeek OpenAI-compatible API.

---

## File Map

- Create `tests/integration/__init__.py`: make the helper importable by unit tests.
- Create `tests/integration/trace_e2e.py`: REST clients, span model, assertions, polling, and CLI.
- Create `tests/test_trace_e2e.py`: credential-free tests for parsing, isolation, ancestry, and diagnostics.
- Create `scripts/test-trace-e2e.ps1`: explicit external integration runner.
- Modify `README.md`: document prerequisites and safe invocation.

## Task 1: Deterministic Phoenix Assertion Core

**Files:**
- Create: `tests/integration/__init__.py`
- Create: `tests/integration/trace_e2e.py`
- Create: `tests/test_trace_e2e.py`

- [ ] **Step 1: Write the failing trace-selection tests**

Use this public model and test fixture contract:

```python
from tests.integration.trace_e2e import SpanRecord, select_run_trace

RUN_ID = "run-123"
TRACE_ID = "a" * 32
ROOT_ID = "1" * 16

def span(name, span_id, parent_id, *, attributes=None, status_code="OK"):
    return SpanRecord(
        trace_id=TRACE_ID,
        span_id=span_id,
        parent_id=parent_id,
        name=name,
        status_code=status_code,
        attributes=attributes or {},
    )

def valid_spans():
    return [
        span("invoke_agent code-agent", ROOT_ID, None),
        span("chat", "2" * 16, ROOT_ID),
        span(
            "execute_tool Read",
            "3" * 16,
            ROOT_ID,
            attributes={
                "gen_ai": {
                    "tool": {
                        "call": {"result": "TRACE_E2E_FIXTURE:run-123"}
                    }
                }
            },
        ),
        span("chat", "4" * 16, ROOT_ID),
    ]

def test_select_run_trace_requires_fixture_result_and_tree():
    result = select_run_trace(valid_spans(), RUN_ID)
    assert result.trace_id == TRACE_ID
    assert [item.runtime for item in result.required_spans] == [
        "go", "go", "python", "python"
    ]
```

Add separate tests that reject a missing run marker, fewer than two `chat` spans, an `ERROR` tool span, and a chat whose parent chain cannot reach the `invoke_agent` root. Add one passing test where the tool result uses the flattened key `gen_ai.tool.call.result`.

- [ ] **Step 2: Run the focused tests and confirm they fail**

Run `pytest -q tests/test_trace_e2e.py`.

Expected: import failure because the helper interfaces do not exist.

- [ ] **Step 3: Implement the minimal span model and assertions**

Implement these exact public types:

```python
@dataclass(frozen=True, slots=True)
class SpanRecord:
    trace_id: str
    span_id: str
    parent_id: str | None
    name: str
    status_code: str
    attributes: Mapping[str, Any]

@dataclass(frozen=True, slots=True)
class RequiredSpan:
    name: str
    runtime: str
    span_id: str
    parent_id: str | None

@dataclass(frozen=True, slots=True)
class TraceResult:
    trace_id: str
    required_spans: tuple[RequiredSpan, ...]
```

Implement `nested_attribute(attributes, dotted_key)` with flattened-key-first lookup, then nested mapping traversal. Implement `select_run_trace(spans, run_id)` to:

1. Find one trace through an `execute_tool Read` result containing `TRACE_E2E_FIXTURE:<run_id>`.
2. Require exactly one `invoke_agent code-agent` in that trace.
3. Require the marker-bearing successful Read span and at least two `chat` spans.
4. Follow parent IDs through a span-ID map and require each tool/chat span to reach the root.
5. Return ordered rows: root as Go, marker-bearing tool as Go, chats as Python.

- [ ] **Step 4: Run tests and commit**

Run `pytest -q tests/test_trace_e2e.py`; expect all focused tests to pass.

```powershell
git add -- tests/integration/__init__.py tests/integration/trace_e2e.py tests/test_trace_e2e.py
git commit -m "test: add Phoenix trace assertion core"
```

## Task 2: DeepSeek And Phoenix REST Commands

**Files:**
- Modify: `tests/integration/trace_e2e.py`
- Modify: `tests/test_trace_e2e.py`

- [ ] **Step 1: Write failing URL and payload tests**

Add these assertions:

```python
assert build_model_url("https://api.deepseek.com") == (
    "https://api.deepseek.com/v1/models"
)
assert build_model_url("https://api.deepseek.com/v1") == (
    "https://api.deepseek.com/v1/models"
)

url = build_spans_url(
    "http://127.0.0.1:6006",
    "default",
    "2026-07-29T01:02:03+00:00",
    None,
)
assert "/v1/projects/default/spans?" in url
assert "start_time=2026-07-29T01%3A02%3A03%2B00%3A00" in url
assert parse_model_ids({"data": [{"id": "deepseek-v4-pro"}]}) == {
    "deepseek-v4-pro"
}
```

Add a Phoenix page fixture containing `name`, `context.trace_id`, `context.span_id`, `parent_id`, `status_code`, and nested `attributes`; require `parse_spans_page` to return a `SpanRecord` and `next_cursor`.

- [ ] **Step 2: Run tests and confirm the new imports fail**

Run `pytest -q tests/test_trace_e2e.py`.

Expected: import errors for URL/payload functions.

- [ ] **Step 3: Implement dependency-free REST helpers**

Implement:

```python
def build_model_url(base_url: str) -> str:
    base = base_url.rstrip("/")
    return f"{base}/models" if base.endswith("/v1") else f"{base}/v1/models"

def build_spans_url(
    phoenix_url: str,
    project: str,
    start_time: str,
    cursor: str | None,
) -> str:
    query = {"start_time": start_time, "limit": 100}
    if cursor:
        query["cursor"] = cursor
    encoded_project = urllib.parse.quote(project, safe="")
    return (
        f"{phoenix_url.rstrip('/')}/v1/projects/{encoded_project}/spans?"
        f"{urllib.parse.urlencode(query)}"
    )
```

`parse_model_ids` must reject a missing/non-list `data`. `parse_spans_page` must reject malformed context objects and preserve both nested and flat attributes. A single internal JSON request helper sets `Accept: application/json`, optionally adds a bearer header, and converts HTTP failures into status plus sanitized response detail without request headers or credentials.

- [ ] **Step 4: Implement stable CLI subcommands**

Support:

```text
python tests/integration/trace_e2e.py check-model --base-url URL --model MODEL
python tests/integration/trace_e2e.py verify-phoenix --phoenix-url URL --project default --start-time ISO --run-id ID --timeout 45
```

`check-model` reads `OPENAI_API_KEY` only from the environment, requires the requested ID from `/v1/models`, and prints JSON `{"model":"deepseek-v4-pro","available":true}`.

`verify-phoenix` paginates `/v1/projects/default/spans`, calls `select_run_trace`, and polls until success/deadline. Success JSON contains `run_id`, `trace_id`, and `spans` with `name`, inferred `runtime`, `span_id`, and `parent_id`. Timeout diagnostics contain only trace/span IDs, names, parents, and statuses.

- [ ] **Step 5: Unit-test polling and sanitization**

Define `poll_for_trace(fetch_pages, run_id, timeout, monotonic=time.monotonic, sleep=time.sleep)`. Test immediate success and deterministic timeout with injected clock/sleep functions. Supply a fake secret to the HTTP error sanitizer and assert the exception string does not contain it.

- [ ] **Step 6: Run Python tests and commit**

```powershell
pytest -q tests/test_trace_e2e.py
pytest -q
git add -- tests/integration/trace_e2e.py tests/test_trace_e2e.py
git commit -m "test: add DeepSeek and Phoenix trace helpers"
```

Expected: both pytest commands pass without network access.

## Task 3: Explicit PowerShell Runner

**Files:**
- Create: `scripts/test-trace-e2e.ps1`

- [ ] **Step 1: Add parameters and help**

Start with:

```powershell
#Requires -Version 5.1
[CmdletBinding()]
param(
    [string]$ApiKeyFile,
    [string]$Model = 'deepseek-v4-pro',
    [string]$OpenAIBaseUrl = 'https://api.deepseek.com',
    [string]$PhoenixUrl = 'http://127.0.0.1:6006',
    [string]$PhoenixProject = 'default',
    [int]$TurnTimeoutSeconds = 120,
    [int]$TraceTimeoutSeconds = 45
)
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
```

Add comment-based help with environment-key and `C:\secure\api-key.md` examples. Commit no personal path or token.

- [ ] **Step 2: Implement secret-safe preflight**

Add `Get-DeepSeekApiKey`, `Assert-Command`, `Get-FreeTcpPort`, `Wait-Phoenix`, `Invoke-PythonHelper`, and `Get-ScrubbedText`.

`Get-DeepSeekApiKey` prefers inherited `OPENAI_API_KEY`; otherwise it reads the supplied Markdown as UTF-8, selects exactly one DeepSeek-labelled line, and extracts exactly one `sk-[A-Za-z0-9_-]+` token. `Invoke-PythonHelper` uses `ProcessStartInfo.Environment`; the key never appears in `ArgumentList`. All errors scrub the exact secret.

Before starting Docker, invoke the helper's `check-model` command through `Invoke-PythonHelper` with the selected base URL/model and a child environment containing the key. Fail immediately on authentication, network, or unavailable-model errors so no local service or workspace is created unnecessarily.

- [ ] **Step 3: Implement owned Phoenix and workspace setup**

Record whether `docker compose ps --status running --services` includes `phoenix`. Start only `docker compose up -d phoenix` when absent, bound the Compose command to 300 seconds for a first image pull, and poll `$PhoenixUrl/v1/projects?limit=1` for 60 seconds.

Create one GUID temp directory containing:

```text
.agent/settings.json
trace-e2e-<run_id>.txt
code-agent-e2e.exe
```

The fixture contains `TRACE_E2E_FIXTURE:<run_id>`. Settings use a free loopback orchestrator port, enable autostart, run `python -m orchestrator.server`, disable `model_fast`, set 30/120-second startup/conversation timeouts, and allow `Read` with pattern `**`.

- [ ] **Step 4: Build and run the real agent**

Build `./cmd/agent` into the temp binary with a 120-second process deadline. Set only child environment values:

```text
LLM_PROVIDER=openai
OPENAI_API_KEY=in-memory-value
OPENAI_BASE_URL=https://api.deepseek.com
OPENAI_MODEL=deepseek-v4-pro
OPENAI_MAX_RETRIES=1
OPENAI_TIMEOUT=90
THINKING_ENABLED=false
OTEL_SERVICE_NAME=code-agent-orchestrator
PYTHONPATH=repository-root
```

Remove inherited `OTEL_EXPORTER_OTLP_ENDPOINT` so Go uses its service-root default and Python uses its full trace-path default. Redirect all standard streams, send one prompt requiring exactly one `Read` of the fixture followed by `TRACE_E2E_OK:<run_id>`, keep stdin open, consume stderr asynchronously, and consume stdout via `ReadLineAsync()` until the success marker or deadline.

- [ ] **Step 5: Verify Phoenix before process shutdown**

While the agent is waiting for its next input, call `verify-phoenix` with the UTC timestamp recorded immediately before the turn. Pass no API key to this helper. Parse success JSON, close stdin, wait 15 seconds for normal exit, and print run ID, model, trace ID, span name, inferred runtime, span ID, and parent ID.

- [ ] **Step 6: Implement exact cleanup ownership**

Use outer `try/finally`: close stdin, kill only the recorded agent tree if still alive, wait for reaping, remove the exact GUID temp directory, and stop Phoenix only if this run started it. Never run `docker compose down`, remove a volume, or enumerate unrelated processes. Failure output includes stage, exit code, scrubbed child logs, model, and verifier diagnostics.

- [ ] **Step 7: Syntax-check, inspect help, and commit**

```powershell
$tokens = $null
$errors = $null
[System.Management.Automation.Language.Parser]::ParseFile(
    (Resolve-Path scripts/test-trace-e2e.ps1),
    [ref]$tokens,
    [ref]$errors
) | Out-Null
if ($errors.Count) { throw ($errors | Out-String) }
Get-Help ./scripts/test-trace-e2e.ps1 -Detailed
git add -- scripts/test-trace-e2e.ps1
git commit -m "test: add explicit Phoenix trace E2E runner"
```

Expected: no parser errors; help performs no network or Docker mutation.

## Task 4: Documentation And Credential-Free Regression Gate

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Document the opt-in command**

Add a Chinese subsection under development/testing stating that the test uses Docker, real DeepSeek, the real Go agent, and the real Python orchestrator, and is excluded from default test discovery. Include:

```powershell
$env:OPENAI_API_KEY = '<从安全存储加载>'
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/test-trace-e2e.ps1 -Model deepseek-v4-pro
```

Document `-ApiKeyFile` for an external Markdown file and state that the key is never printed or written.

- [ ] **Step 2: Run all default tests**

```powershell
pytest -q
go test ./...
```

Expected: both suites pass.

- [ ] **Step 3: Check scope and secrets**

```powershell
git diff --check
git diff --stat HEAD~1
rg -n "sk-[A-Za-z0-9_-]{12,}|D:\\Obsidian" scripts tests README.md docs/superpowers
```

Expected: no real token and no personal path. Regex documentation may contain the literal `sk-` pattern but no token value.

- [ ] **Step 4: Commit documentation**

```powershell
git add -- README.md
git commit -m "docs: document Phoenix trace E2E test"
```

## Task 5: Real DeepSeek And Phoenix Acceptance

**Files:**
- Modify only if a reproducible repository defect is demonstrated.

- [ ] **Step 1: Verify prerequisites and clean tracked state**

```powershell
docker info --format '{{.ServerVersion}}'
git status --short
```

Expected: Docker reports a version and there are no uncommitted tracked changes.

- [ ] **Step 2: Run the explicit test**

Invoke `scripts/test-trace-e2e.ps1` with `-Model deepseek-v4-pro` and the user-approved external key file through `-ApiKeyFile`. Do not echo the path contents or token.

Expected: exit zero, one run ID, one trace ID, Go `invoke_agent` and `execute_tool Read`, at least two Python `chat` spans, and valid ancestry.

- [ ] **Step 3: Confirm cleanup**

```powershell
git status --short
docker compose ps phoenix
```

Expected: no temp test files. Phoenix remains running only if it was running before the test.

- [ ] **Step 4: Re-run final tests**

```powershell
pytest -q
go test ./...
```

Expected: both suites pass.

- [ ] **Step 5: Handle evidence-driven defects**

If the real run exposes a reproducible repository defect, stop, add a failing credential-free regression test, apply the smallest fix, rerun Task 5, and commit the correction separately. If there is no defect, create no extra commit.
