# 100M-token admission module: source-bound evidence

Source: `db11083b25da5ee5677867a7b646cd4fde2b6479`.
Engineering status: `IMPLEMENTED`. Complete Issue #3 runtime status: `BLOCKED`.
The authorized limit is 100,000,000 cumulative input/output tokens, replacing
the earlier CNY100 limit. This record does not claim that all product model
callers already use the module. No paid model request was made in these runs.

## Checks

| Command | Result | Exit |
| --- | --- | ---: |
| `go test ./internal/admission -count=1 -json` | 13 passed, 1 subprocess-helper skip; real SQLite and independent Go processes, synthetic amounts | 0 |
| `go test ./... -count=1 -json` | 1,248 passing test/subtest actions, 51 skips, 42 passing packages | 0 |
| `go vet ./...` | no diagnostics | 0 |
| `npm test` in `frontend/` | 12 passed | 0 |
| `npm run build` in `frontend/` | TypeScript and Vite succeeded | 0 |
| `CODE_AGENT_RUN_LOCAL_CORE_E2E=1 node tests/e2e/local_core_browser.mjs` | 8/8 browser checks, 0 not-run | 0 |
| `python -m pytest -q` in current-source GitHub CI | 2,426 passed, 2 skipped, 3 warnings | 0 |
| staged secret scan / `git diff --cached --check` | exact 10 paths, 0 secret findings / clean diff | 0 |

The final local full-suite run preceded the source commit. Its eight changed
source-file hashes remained unchanged during the run and match the committed
working files. The subsequent admission and browser runs bind the exact source
commit above. [CI for that commit](https://github.com/huadeng408/CodeOps-Agent/actions/runs/37933397675)
completed successfully in all three jobs: Go, Python and Frontend.

## Local artifacts

Generated artifacts remain ignored. Paths below are relative to the checkout;
the receipts include commands, exits, run identifiers and artifact hashes.

| Evidence | Receipt | Receipt SHA-256 |
| --- | --- | --- |
| Ledger tests/process integration | `output/playwright/token-ledger-8866756e-5fc9-4173-80c0-ccc9265cf048/receipt.json` | `06dcaeb45a5af85d9653f4b9caeec6e7d4e315fb16c7342dbd8f826fbc94a56f` |
| Browser auth/history/blocked-state checks | `output/playwright/local-core-36acb505-db62-469f-8f0e-8791f5622b87/receipt.json` | `1d0481a8a0b91fde2d35751d44a878b56474f0ed96c7ab0b1991ad33b80dddf0` |
| Final local regressions | `output/playwright/budget-regressions-94f78934-2a7e-4d21-994d-b97aae138796/receipt.json` | `e8907bcc4a1c3763ea8f78994f36097be622f9a8149e7b9a0ba4a4b90cdb04d8` |

The browser run used two separate production Go processes. Both were stopped
with requested `SIGTERM`; Windows reports a null numeric exit for those
signal outcomes. The browser runner and build returned 0. Source and built
assets remained unchanged. Trace backend: `unknown`; model calls: 0. The
browser screenshot was inspected and shows the revised token-allowance text
and the blocked execution state.

## Failures retained

The initial full regression returned 1: 1,245 pass actions, 51 skips and one
failing action, `TestRegistryWaitTimeoutReturnsLiveStateAndKillIsIdempotent`.
Receipt: `output/playwright/budget-regressions-4216204d-2c81-4410-8c42-0267896c6652/receipt.json`;
SHA-256: `895fe353a7d455eed1c97494ba2e9f7a3b9d56d604cadaa3e81b2ae997b329c1`.
An eight-run narrow reproduction failed once because the child had already
reached `killed` before `Kill` returned. The assertion now accepts either
`stopping` or `killed`, preserving final termination and duplicate-kill
checks; the next eight runs passed. The failure receipt was not replaced.

Two `AI_REVIEWED` axes also found missing checksum verification and admission
stream compatibility. Failing regressions preceded the fixes: the module now
uses the existing verified immutable snapshot, and both session readers
recognize only the fixed admission ID plus its creation type. A mismatched ID
is still rejected. Final Standards findings: 0; final slice Spec findings: 0.
This is not a human-review claim.

## Remaining acceptance

Foreground, retry, reflection and Worker callers still need the shared Go
admission path, attributable provider usage with cache/reasoning counted once,
honest unknown-price handling, and real product/model/restart evidence. The
read-only provider catalog check succeeded, but it is not a paid-generation
receipt or a complete relay billing contract. The 10-round coding tasks,
full trace readback and wider release gates remain outstanding. Issue #3 stays
open; see [module boundaries and next integration](../token-budget.md).
