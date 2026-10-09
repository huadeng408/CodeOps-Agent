# Go model gateway evidence — 2026-10-10

Issue [#3](https://github.com/huadeng408/CodeOps-Agent/issues/3): invocation
migration **IMPLEMENTED**; complete product acceptance **BLOCKED**.
Implementation commit `626561fcb008453e101964d312547089de8caab0`; source-manifest
repair and final runtime source `bbdb5e17297723ad2fd8c3ac233c1732e3f5ddb4`.
This record does not close the ticket or satisfy the release gate.

## Delivered scope

In explicit `CODE_AGENT_MODEL_ADMISSION=required` mode, existing Go callers
bind Python foreground, compaction, reflection and Worker model calls to a
Go-owned gRPC gateway. Go owns credentials, transport, output limits and the
existing Ledger's reservation/settlement facts. Verified parent/child links
recover the root task scope. Missing capabilities cannot fall back to direct
Python provider clients. The aggregate gateway limit is one unresolved call;
unknown outcomes retain their reservation, including after restart.

The authenticated capabilities endpoint and approved right sidebar show
persisted native token amounts and specific missing prerequisites. Unknown
price remains unknown in Session metadata, CLI history and the browser.
CLI `/clear` releases only settled, idle root tasks; failure to persist the
replacement Session retains history and reports failure. No batch reset occurs.
Details and remaining migration limits: [token-budget.md](../token-budget.md).

## Source regression

| Command | Result / exit code | Evidence |
| --- | --- | --- |
| `scripts/generate-proto.ps1` | Generated both languages; 0 | Generated files committed with schema |
| `go test ./... -count=1 -json` | 1,266 test actions passed, 54 skipped, 0 failed, 42 packages; 0 | `output/playwright/gateway-final-go-724cfdad-32ee-4477-955a-a7c7317dd06c/receipt.json` |
| `go vet ./...` | 0 | Repeated after the final CLI guard |
| `python -m pytest -q` | 2,416 passed, 18 skipped, 31 warnings; 0 | `output/playwright/gateway-regressions-42760b74-f800-41c6-ac77-269e51e42fea/receipt.json` |
| `npm test` / `npm run build` in `frontend/` | 12 passed / build succeeded; both 0 | Same regression manifest |
| `go test ./tests/e2e -count=1` / `go vet ./tests/e2e` | Both 0 after source-manifest repair | No paid opt-in; committed-source regression passed |
| `git diff --check` / `git diff --cached --check` | Both 0 | Exact staged sources reviewed |

The complete suites ran before the source-manifest-only repair; final Go
source hashes cover the last CLI persistence guard, and Python/frontend files
were unchanged after their complete suites. The repair's affected package was
then rerun. Test actions include subtests, not 1,266 independent scenarios.
Full-Go manifest SHA-256:
`dea6995a8e6057428e3f1f9f6498b85165afa961816752a547e9b2315dfe4d07`.
Combined regression manifest SHA-256:
`3a5a6f29eb1d20c58b2d3ca41abeac7a99eee77d477ddfb4ccb2dd9a50ef558a`.
Fixtures and source regression make zero paid model calls and do not prove
production code-task recovery.

Standards and specification reviews reported no remaining findings after
fixing their credential-echo, cost-state, reflection-classification and CLI
persistence findings. The later manifest repair also received both reviews.
These are AI reviews, not `HUMAN_REVIEWED`.

## Real-provider process probe

Command: `CODE_AGENT_RUN_MODEL_GATEWAY_E2E=1 go test ./tests/e2e
-run TestProductionModelGatewayAcrossProcesses -count=1 -v`.
The approved profile is injected only into Go process memory; the child Python
processes receive a transient capability rather than provider credentials.

Final run `run-2511199469`: **5 paid calls, 8/8 checks, exit 0**. It invokes the
existing foreground client, reflection proposal generator, compaction
summarizer and Workflow Worker, then another foreground call in a fresh Python
process. Another Go OS process reopens the same SQLite Ledger and checks its
batch and consumption. Hash-chain and explicit unknown-price checks pass.
The receipt binds the final source SHA above and 810 Go/Python/proto file
hashes; sources were unchanged during the run.

Receipt: `.runtime/e2e/model-gateway-runs/run-2511199469/receipt.json`.
SHA-256: `c8d06c75a6360037b1963a4a7c3218025e1346d826b9ca4aaa8f1efdf785f835`.
Trace readback is `unknown`, and the receipt deliberately remains `BLOCKED`.
This probe does not execute a repository tool task or write reflection Memory
through Go; those are separate acceptance requirements.

All five probe runs used one canonical verification Ledger and batch
`9038f2bbae8b99b6de82f8f8c43fb922`. Across 25 paid calls, cumulative confirmed
usage is **75,975 / 100,000,000 tokens**, reserved 0, unknown usage false.
The final run added 15,581 tokens to the prior 60,394; price is unknown.
The configured BeeAPI Anthropic-compatible `grok-4.6` profile uses an explicit
500,000 input ceiling and 2,048 output tokens. The ceiling is an operator
contract assumption derived from upstream documentation plus observed usage,
not proof of every relay group or untested cache/reasoning variant.

Earlier runs remain intact: `run-1531612445` (+15,130), `run-3396347698`
(+15,152), `run-71642592` (+15,186), `run-912409865` (+14,926).
The last of those bound commit `626561fc` but had an empty source-hash map
because its helper enumerated only uncommitted files. It is retained as a real
usage record, not upgraded to complete source-manifest evidence. A failing
committed-source regression reproduced that defect before repair.

## Browser process check

Final browser command: `CODE_AGENT_RUN_LOCAL_CORE_E2E=1 node
tests/e2e/local_core_browser.mjs`, with the same canonical verification Ledger.
Run `local-core-7b4b5a98-18b8-4978-b0a0-474f6a3d16b5`: **9/9 checks, exit 0**,
two production Go OS processes, zero model calls. Actual browser login,
durable Session creation, persisted token display, refusal with retained draft,
unreachable capabilities, process restart/history and logout all pass. Source
and built assets stayed unchanged, and the receipt binds `bbdb5e17` plus 440
source hashes. Trace is unknown; this is not a browser model/code-task run.

Receipt: `output/playwright/local-core-7b4b5a98-18b8-4978-b0a0-474f6a3d16b5/receipt.json`.
SHA-256: `e654cfde2b7b7233c89b8bb2ee787b4105f93cf5f2517b48a85390006705f07c`.
Earlier passing browser runs, including
`local-core-e7856ca5-efec-4f9a-9d7a-92fb97f25f40` (9/9, source `626561fc`),
remain intact alongside failures below.

## Preserved failures and unresolved acceptance

- `gateway-regressions-ddd6d420-3eda-4a2f-95f3-7ab45a117835`: Python 30 failed,
  2,384 passed, 18 skipped. The test launcher incorrectly disabled the trace
  SDK globally; the corrected launcher preserves trace tests. Source also
  changed during that run, so its receipt remains failed.
- `gateway-regressions-82479d42-7fce-4989-9f7d-3749f9f98f68`: Go 1 failed,
  1,181 passed, 54 skipped; nil status-line restoration in an existing fixture
  was fixed, then the complete Go suite passed.
- Browser `local-core-96ad34cb-e6ea-40c1-bcbb-2baea7c1f1cc` failed after 4
  checks because a cost-text locator matched two elements; corrected to the
  explicit summary. `local-core-16a39d19-6d32-4468-89e4-4eb05dd4fa84` passed
  6/9 before an obsolete five-row expectation failed; the eight-row fixture
  was corrected. Neither failed run was removed.
- The cancellation fixture's unread HTTP body stalled its server cleanup;
  only that owned test process was stopped. The fixture now consumes the body
  before its cancellation barrier. Two public CLI persistence regression
  attempts timed out on the fixture's unnecessary sandbox readiness probe;
  the no-tool fixture now explicitly disables that probe and passes.

Complete #3 acceptance remains blocked on default product enablement, approved
sandbox/secret injection, HTTP task completion, accounted retry/reconciliation,
production CLI/HTTP model recovery, metered streaming and full trace readback.
The two public repository tasks with at least ten real browser turns each,
reflection writeback/empty-session recall, 200-turn and Worker/Skills matrices,
and official scorer receipts remain outstanding. Fail-closed safety and the
original full denominators are preserved.
