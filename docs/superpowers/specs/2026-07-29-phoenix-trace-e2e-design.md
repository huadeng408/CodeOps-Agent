# Phoenix Cross-Language Trace E2E Design

Date: 2026-07-29

## Status

Approved for implementation.

## Context

The Go CLI creates the root `invoke_agent code-agent` span and tool execution
spans. It forwards W3C trace context to the Python orchestrator through gRPC
metadata. The Python orchestrator extracts that context and creates `chat`
spans around model calls. Both processes export OTLP/HTTP traces to Phoenix.

Existing unit tests cover these pieces independently, but they do not prove
that a real CLI turn produces a single persisted Phoenix trace containing
spans from both runtimes. This design adds an explicit Docker-backed
integration test for that boundary.

The test must call the real DeepSeek OpenAI-compatible API with
`deepseek-v4-pro`. It must not use Tika, a fake model server, or a mocked
orchestrator.

## Goals

- Exercise the real Go CLI, its real process manager, the real Python
  orchestrator, a real DeepSeek model request, and a real Go `Read` tool call.
- Prove that Go and Python spans are persisted under one Phoenix trace ID.
- Prove parent-child propagation, not only the presence of similarly named
  spans.
- Keep the test opt-in so default Go and Python test suites remain fast and do
  not require Docker, network access, or paid API credentials.
- Keep credentials out of the repository, command-line arguments, logs,
  temporary files, and telemetry.
- Produce actionable diagnostics when an external service or trace assertion
  fails.

## Non-Goals

- Testing the Phoenix web UI.
- Testing model answer quality beyond the deterministic tool-use contract.
- Exercising a real OpenAI-hosted model. "OpenAI" in this test refers to the
  OpenAI-compatible client protocol used with the real DeepSeek endpoint.
- Adding this integration test to the default CI workflow.
- Changing production tracing behavior unless implementation exposes a defect
  that prevents the approved scenario from working.

## Approaches Considered

### Real DeepSeek API and real local processes

This is the selected approach. It provides the strongest evidence that the
actual client protocol, streaming tool call, gRPC context propagation, tool
execution, OTLP exporters, and Phoenix persistence work together.

The cost is external variability: the test requires a valid credential,
network access, an available model, and a bounded amount of API usage. The
runner therefore performs explicit preflight checks and applies strict
timeouts.

### Deterministic local OpenAI-compatible fake

This would make the test cheaper and more deterministic, but it would not meet
the approved requirement to call a real model API. It is rejected for this
test.

### Mocked tracer or in-process integration

This would duplicate existing unit coverage and could not prove that spans
arrive in Phoenix. It is rejected.

## Architecture

The implementation has two entry points:

- `scripts/test-trace-e2e.ps1` is the user-facing orchestration command. It
  owns preflight checks, temporary workspace creation, environment isolation,
  process lifecycle, timeouts, and cleanup.
- `tests/integration/trace_e2e.py` is a standard-library-only helper for
  checking model availability and querying/asserting full Phoenix span data.
  It is not collected by the default pytest suite as a test module.

The explicit invocation is:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/test-trace-e2e.ps1 `
  -ApiKeyFile '<path-to-private-api-key-markdown>' `
  -Model deepseek-v4-pro
```

`OPENAI_API_KEY` may be supplied instead of `-ApiKeyFile`. The base URL
defaults to `https://api.deepseek.com`, and the model defaults to
`deepseek-v4-pro`.

## Credential Handling

The runner obtains the credential in this order:

1. A non-empty `OPENAI_API_KEY` inherited by the runner.
2. The DeepSeek-labelled token in the file supplied with `-ApiKeyFile`.

The file path has no repository-specific default. The Markdown parser selects
the single line labelled for DeepSeek and extracts one `sk-...` token. Missing
or ambiguous matches fail before any model request.

The key is injected only into the environment blocks of the model preflight
and agent child processes. It is never passed as a command-line argument,
written into the temporary workspace, included in errors, or printed. Child
output is scrubbed for the exact loaded secret before diagnostics are emitted.

The runner also supplies these child environment values:

- `LLM_PROVIDER=openai`
- `OPENAI_BASE_URL=https://api.deepseek.com` unless overridden
- `OPENAI_MODEL=deepseek-v4-pro` unless overridden
- no inherited `OTEL_EXPORTER_OTLP_ENDPOINT`; Go therefore uses its existing
  `http://localhost:6006` service-root default while Python uses its existing
  `http://localhost:6006/v1/traces` exporter default
- `OTEL_SERVICE_NAME=code-agent-orchestrator` for Python
- `PYTHONPATH=<repository-root>` so the temporary workspace can launch the
  real orchestrator module

The endpoint variable is deliberately removed from the child environment
because the current Go and Python implementations interpret the same variable
differently: Go accepts a service root and appends the trace path, while Python
passes the value to its exporter as a complete trace URL. Their checked-in
defaults already point to the same Phoenix instance correctly.

## Process And Data Flow

1. The PowerShell runner validates `docker`, `docker compose`, `go`, and
   Python. It confirms that Docker is responsive and that required local ports
   can be used.
2. The runner records whether the Compose `phoenix` service is already
   running. It starts only that service when necessary and polls the Phoenix
   REST API until ready.
3. The helper calls the DeepSeek `/v1/models` endpoint with the loaded key and
   verifies that the requested model is currently available. Authentication,
   network, and model errors fail at this stage.
4. The runner creates a fresh temporary workspace, selects a free loopback
   orchestrator port, and writes only non-secret test configuration under the
   temporary `.agent` directory. The fast model is disabled so the test makes
   only the model calls required by the turn.
5. The workspace receives a uniquely named fixture such as
   `trace-e2e-<run_id>.txt`. Its contents include `TRACE_E2E_FIXTURE:<run_id>`.
6. The current Go source is built to a binary inside the temporary directory.
   The repository working tree and any existing `agent.exe` are not modified.
7. The runner starts the binary with redirected stdin, stdout, and stderr. It
   keeps stdin open while the turn executes. The Go process auto-starts the
   real Python orchestrator through the existing `ProcessManager`.
8. The prompt tells DeepSeek to use `Read` exactly once on the named fixture
   and then include `TRACE_E2E_OK:<run_id>` in its final response. The real
   streaming OpenAI-compatible path must produce the tool call.
9. When the success marker appears in agent output, the runner keeps both
   runtimes alive and polls the Phoenix full-span endpoint. This allows the Go
   and Python batch span processors to export ended spans before the
   orchestrator is stopped.
10. After trace verification succeeds, the runner closes agent stdin. The CLI
    exits normally and flushes its tracer. Abnormal paths terminate only the
    process tree created by this test.

## Trace Isolation And Selection

Historical Phoenix data must not create a false positive. Immediately before
the agent turn, the runner records a UTC lower bound. The verifier queries:

`GET /v1/projects/default/spans`

with the recorded `start_time`, a bounded result limit, pagination when
needed, and polling for eventual persistence. The trace-list endpoint is not
used because its `include_spans=true` representation intentionally omits span
attributes, including the tool result required for per-run isolation.

A candidate trace must contain an `execute_tool Read` span whose tool result
attribute includes the exact `TRACE_E2E_FIXTURE:<run_id>` value. Because the
run ID exists in a newly created file and reaches telemetry only through the
real tool result, this identifies the current run without production tracing
changes or a dedicated Phoenix project.

Once identified, all remaining assertions are applied to that exact trace ID.

## Required Trace Assertions

The selected trace must contain:

- exactly one relevant root span named `invoke_agent code-agent`;
- at least one `execute_tool Read` span;
- at least two `chat` spans, covering the Python model call before the tool
  request and the model call after the tool result;
- the unique fixture marker in the successful `Read` tool result;
- no error status on the required tool span.

All required spans must share the selected trace ID. Each required `chat` span
and the relevant `execute_tool Read` span must have a parent chain that reaches
the selected `invoke_agent code-agent` span. This verifies W3C context
propagation across the gRPC boundary rather than merely matching service and
span names.

The Go runtime is identified by its `invoke_agent code-agent` and
`execute_tool Read` instrumentation, while the Python runtime is identified by
its `chat` instrumentation. Phoenix currently uses
`openinference.project.name` to route OTLP data and does not persist arbitrary
OTLP resource attributes in its REST span representation. Consequently,
`service.name` remains a resource-level unit-test assertion and is not an E2E
REST assertion. The test does not add duplicate production span attributes
solely to expose that resource value through Phoenix.

The CLI output must also contain `TRACE_E2E_OK:<run_id>`, proving that the
second real model response consumed the tool result and completed the turn.

## Timeouts And Failure Handling

Each stage has a bounded deadline:

- Phoenix readiness: 60 seconds.
- DeepSeek model preflight: 30 seconds.
- Go build: 120 seconds.
- Orchestrator startup and model turn: 120 seconds.
- Phoenix trace visibility: 45 seconds after the final assistant marker.
- Overall runner deadline: approximately 5 minutes.

Provider retries are disabled or limited to one retry for transient failures
so a failing run cannot create uncontrolled API usage. Authentication errors,
an unavailable requested model, malformed provider responses, failure to call
`Read`, a missing output marker, and missing or incorrectly parented spans are
hard failures. There is no fake-response or log-only fallback.

Failure output reports the current stage, child exit codes, scrubbed combined
agent/orchestrator output, the requested model, and Phoenix candidate traces
with trace IDs, span names, parent IDs, and service names. It never reports the
credential or authorization headers.

## Cleanup Ownership

Cleanup runs in `finally` regardless of success or failure:

- Close agent stdin first to request a normal exit.
- On timeout, terminate the recorded agent process tree and wait for it to be
  reaped.
- Delete the temporary binary, configuration, session database, and fixture
  workspace.
- Stop the Compose `phoenix` service only if this run started it.

The runner never uses `docker compose down`, never removes the Phoenix volume,
and never stops a Phoenix instance that was already running.

## Test Placement And Default Suites

The test is intentionally excluded from default test discovery. It runs only
through:

```powershell
powershell -NoProfile -ExecutionPolicy Bypass -File scripts/test-trace-e2e.ps1
```

with credentials supplied by environment or `-ApiKeyFile`. `go test ./...`
and the default pytest suite remain credential-free and Docker-free. Existing
unit tests continue to own exporter configuration, traceparent injection and
extraction, and individual span attributes.

No default CI job is added. A later opt-in job may run this command with a
managed DeepSeek secret and Docker service.

## Success Output

A successful run exits with status zero and prints only non-secret summary
data:

- run ID;
- selected Phoenix trace ID;
- requested model;
- a compact table of required span name, inferred runtime, span ID, and parent
  ID;
- total elapsed time.

## Acceptance Criteria

- The documented explicit command completes successfully against Docker
  Phoenix and the real `deepseek-v4-pro` API.
- The real model invokes the real Go `Read` tool on the per-run fixture.
- Phoenix persists the Go and Python spans under one trace ID with the required
  parent chain.
- Historical traces cannot satisfy the test because selection requires the
  per-run fixture marker.
- Credentials are absent from repository changes, child command lines,
  temporary files, telemetry, and printed diagnostics.
- Failure paths clean up only resources owned by the test and preserve
  pre-existing Phoenix state.
