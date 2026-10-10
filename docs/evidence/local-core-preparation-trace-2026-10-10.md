# Local preparation and trace export — 2026-10-10

Engineering state: `IMPLEMENTED`; full Issue #4 and real backend readback:
`BLOCKED`. Baseline: `c93fc36aa4a16f6e977e78bcfa311244a181eae1`.
This slice keeps the approved A interface, authentication, Go permissions,
Session Ledger, workspace copying and lease contracts unchanged.

## Change and reuse

- `cmd/server/local_core.go` initializes the existing `genai.NewTelemetry`
  exporter for the local entry point and flushes it with a five-second shutdown
  context. Configuring an exporter does not turn the trace capability ready.
- `internal/telemetry/genai/tracer.go` and `logging.go` retain fixed diagnostic
  classes instead of raw endpoint URLs, SDK headers, error objects or collector
  response bodies. This applies to the existing CLI and full-stack callers too.
- `startup_test.go` covers invalid endpoints, malformed authentication headers
  and a collector 401 response echoing a fixture credential. Child test processes
  isolate all OTEL variables; no real credential is used in these tests.
- `tests/e2e/local_core_trace_e2e_test.go` exercises real Go processes through
  authenticated HTTP preparation and receives an attributable, ended OTLP span.
  The existing process/browser fixtures now filter environment variable names
  without case sensitivity, including mixed-case Windows OTEL variables.
- `tests/e2e/local_core_browser.mjs` adds a pinned public-source preparation
  input and records its selection in the reproduction command. The original
  controlled-input and full-stack modes remain available.
- `go mod tidy` marks the already installed logr and OTLP schema dependencies
  direct. Versions and go.sum are unchanged; no new execution framework is added.

## Inputs and source boundary

Public input: [go-chi/chi v5.2.3](https://github.com/go-chi/chi/tree/9b9fb55def404397748a9fc7e044efe9db1d618e),
commit `9b9fb55def404397748a9fc7e044efe9db1d618e`, MIT. The test checks the
fetched SHA, license and module identity before adding controlled dirty, staged,
new, empty and deleted files in its own checkout. Those mutations are test
inputs, not a solved code task. No external repository script is executed.
Original HEAD/status/index and canary hashes are recorded before and after
the product operations. The server's PATH is empty.

Runs below record the parent HEAD plus exact dirty-source hashes. A subsequent
commit binding must compare those hashes to the committed source before they
can be cited for that source. Original receipts are not rewritten.

## Checks

Targeted RED/GREEN and related checks:

```text
go test ./internal/telemetry/genai -count=1
go test ./cmd/server ./internal/telemetry/genai ./internal/middleware -count=1
CODE_AGENT_RUN_LOCAL_CORE_E2E=1 go test ./tests/e2e -run TestProductionLocalCoreExportsWorkspacePreparationTrace -count=1 -v
go vet ./...
node --check tests/e2e/local_core_browser.mjs
```

Final runs of these commands exit 0. The process trace check explicitly injects
lowercase/mixed-case OTEL settings and verifies they do not reach the controlled
runtime. Its receiver is an integration fixture, not Phoenix.

Production HTTP entry checks:

```text
CODE_AGENT_RUN_LOCAL_CORE_E2E=1 go test ./tests/e2e -run TestProductionLocalCore -count=1 -json
```

**9 passed / 0 skipped / 0 failed**, one package, exit 0; **834** source and
dependency hashes unchanged. Run `local-core-trace-http-0baf73c2-ba57-40b8-8993-17c850097f48`.
Receipt: `output/playwright/<run>/receipt.json`, SHA-256
`5338008d40c26c2de8baff00e9f7127110268dd41ee1ced0b89c5823f0b5f79e`;
log SHA-256 `784db6669f3688be48c717ab08b7e621c8ef14acd39c42a505121d09b3aa0f3d`.
Scope: real process/HTTP behavior with controlled repository and OTLP fixtures.

Public-source browser checks:

```text
CODE_AGENT_RUN_LOCAL_CORE_E2E=1 CODE_AGENT_E2E_WORKSPACE=1 CODE_AGENT_E2E_PUBLIC_INPUT=chi-v5.2.3 CODE_AGENT_BROWSER_HEADLESS=1 node tests/e2e/local_core_browser.mjs
```

**13/13, exit 0**, run `local-core-6bd53964-2b77-4553-96f4-db8c211810d6`.
Go PIDs **32584/36480**; **463** source hashes and all browser assets unchanged.
The displayed baseline contains 100 entries and three exclusions (`.env` and
the upstream test certificate/key paths). The expanded screenshot was inspected.
The checks cover actual login/preparation, current working-copy states, original
repository/index preservation, authorization, duplicate refusal, restart,
changed-copy retention, capability uncertainty and logout. Model calls: 0;
Trace backend: unknown. Receipt: `output/playwright/<run>/receipt.json`, SHA-256
`eeec6f9ff1bd89b2f87769f5a9de7377179337efd8174621573c9d646ff4bdd0`.
Screenshots and both process outcomes are pinned in that receipt.

Complete `go test ./... -count=1 -json`: **1,356 passed / 55 skipped / 0 failed
test actions**, 43 passing packages, exit 0; all **834** source/dependency hashes
unchanged. Run `workspace-rooted-go-06199f71-31db-4fd0-9676-bacaef9792fd`;
receipt at `output/playwright/<run>/receipt.json`. Log SHA-256
`364169de02a0c4bc3072f147bb1e43f224b04bebe6d6f9639f282fa8e755240a`.
Receipt SHA-256 `60c69940c02c44ef2c1586f58dd4c9fcae2a762dd0f7f6c348786711440f7a16`.
The skipped explicit runtime lanes are not passed by this regression.

## Failures, review and remaining work

Failed attempts remain: invalid endpoint diagnostics **1/1 failed**, SDK header/
response diagnostics **2/2 failed**, and production preparation span export
**1/1 failed**, all exit 1 before their respective fixes. All later directed
checks pass. These are distinct failures; passing subsets do not erase them.

An intermediate full Go run passed 1,353 / skipped 55 / failed 0 test actions,
43 packages, command exit 0, but its source changed during review. Its validator
exited 1, so it is rejected as final-source evidence. Run
`workspace-rooted-go-0e769247-faa3-4c07-bccf-abf2f01c5fd0`; receipt SHA-256
`3bede3509fc6892d55c72049e2854004d4fad5a09e31de413393f61918321243`.
Intermediate browser runs `local-core-55eb38ba-baa7-429a-a600-dd8b90bc8284`
and `local-core-a392e64f-2519-4f54-bb49-92804a26cdee` pass 13/13, exit 0;
the first omitted the public-input flag from its reproduction command, and both
precede the final SDK diagnostic corrections. Neither is current-source proof.
Tool-output excerpts, not complete runtime receipts, remain at
`output/playwright/local-core-preparation-review/failure-excerpts.json`.
Excerpt SHA-256 `089da892250a15da27811cfc0cb59096979996994e0f8c17ff26fdbba7f3ef29`.

Standards: the two confirmed logging/isolation findings were fixed; re-review
found no remaining definite finding. Spec: no remaining definite issue in this
slice; the incomplete backend readback is explicit. Both are `AI_REVIEWED`.

Real backend blocker: the shared Docker readiness helper exited 1 after 300
seconds (`docker probe timed out after 3000ms`); Phoenix at loopback port 6006
refused connection (`WinError 10061`). No containers, volumes or business data
were deleted. OTLP receiver success cannot replace backend readback.

Full #4 remains OPEN/`BLOCKED`. Real code tasks, model conversations, controlled
dependency execution and complete Go/Python/tool Trace remain later acceptance
work. Paid calls this slice: **0**; the shared 75,975 / 100,000,000 token batch
was not reset. Python/frontend were not modified or rerun locally; fresh source
CI is recorded independently against the pushed SHA.
