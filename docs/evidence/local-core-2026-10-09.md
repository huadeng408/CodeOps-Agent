# Issue #2: local core authentication and history

Status: `VERIFIED` for Issue #2 only. Source commit:
`42ef664a0b00ddb343485b2123bd91c00875df83`.
The Windows coding milestone and release gates remain `BLOCKED`.

The new `local-core` profile starts without optional service credentials, serves
the actual browser UI, initializes one approved local identity, and retains
identity, history and token revocation across separate Go processes. Go enforces
loopback, browser origin, persisted identity/owner binding and single-use tickets.
Identity storage has a private Windows ACL and rejects reparse redirects and
unrelated SQLite schemas. Session facts remain in the canonical Ledger.

The selected right sidebar reads actual capability responses. Missing execution
conditions refuse submit and legacy continuation with 503; no synthetic running
event is appended. A stalled capability endpoint becomes `UNKNOWN`; the draft
survives refused execution. The legacy full-stack entry was checked separately
with isolated cached-image MySQL, Redis and MinIO containers.

| Current-source command | Result | Artifact / run |
| --- | --- | --- |
| `CODE_AGENT_RUN_LOCAL_CORE_E2E=1 node tests/e2e/local_core_browser.mjs` | exit 0, 8/8, no unattempted cases | `output/playwright/local-core-6d8d7725-74d1-4eb8-8000-12214ed8d322/receipt.json` |
| `CODE_AGENT_RUN_LOCAL_CORE_E2E=1 pwsh -File tests/e2e/full_stack_startup.ps1` | exit 0, 8/8, no unattempted cases | `output/playwright/full-stack-b0c0c935-1ead-49dc-9d8f-b375e1968f15/receipt.json` |
| `CODE_AGENT_RUN_LOCAL_CORE_E2E=1 go test ./tests/e2e -run TestProductionLocalCore -count=1 -v` | exit 0, 7/7, no skips | `output/playwright/core-commit-1791545097957/production-process.log` |

Both browser receipts bind the source commit, all Go/protocol/UI source hashes,
compiled binary, served HTML/assets, run IDs, commands, full denominator,
screenshots/log hashes, build exit and each server exit code/signal. The source
and assets remained unchanged throughout both runs. Binary SHA-256:
`710dda70b85c5b19df3c7f5343902551d1c0c4fc751dd6dc873d9b965ca3843d`.

Receipt SHA-256 (core):
`44980ec92668f66bc0af0bb0b9101cda5addbefddeb8f87d6250fc90aedb377e`.
Receipt SHA-256 (legacy):
`5bf326fcabb32aa409e1a64b565adf62aa756cbc81b0cbe795039e5c6fa14985`.

Local related regressions: `go test ./... -count=1` with the core opt-in enabled
returned 0: 1,242 pass test/subtest actions, 43 skips, 41 passing packages.
`go vet ./...`, frontend `npm test` (12/12, zero skips), and `npm run build`
returned 0. [Current-commit CI](https://github.com/huadeng408/CodeOps-Agent/actions/runs/37923392797)
passed Go, Python (2,426 passed, 2 skipped) and frontend source checks.

Earlier failed runs remain local, including missing capability UI
`local-core-6dba2148-07c7-412b-b5de-02ed6fed94cf` (3/7 passed) and incorrect RAG
readiness `full-stack-037014de-ec2e-4cbc-8b93-3fbed45a0c95` (3/8 passed).
These attempts retain their complete denominators and were fixed before the
current-source runs; they are not replaced with a successful subset.

Review: `AI_REVIEWED`; Standards 0 remaining hard violations / 0 smells,
Spec 0 remaining findings. No added dependency, protocol change, legacy identity
remapping, or deletion of business data. Owned service containers were stopped
with each stop exit 0 and preserved; their image IDs and outcomes are in
`output/playwright/full-stack-services-d0f7d1d19d9b4570a188a25af871bc51/services.json`.

This evidence covers authentication, history, capabilities and startup
compatibility. Model calls: 0. Trace backend: `unknown`. It does not establish
paid budget enforcement, real code modifications, reflection/recall, long
conversation, portable packaging or official scorer readiness.
