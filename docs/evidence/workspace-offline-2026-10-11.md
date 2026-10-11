# Workspace preparation and offline Runner evidence — 2026-10-11

Source: `cc9e36d8af987e98bd9f20de26c50e4ac1be4120`. Issue #4 preparation is
`VERIFIED` within the existing Windows/SHA-1 and resource limits. Issue #6's
offline image boundary is `IMPLEMENTED`; the complete ticket remains `BLOCKED`.
These results do not verify a model coding task, complete Go/Python/tool Trace,
dependency preparation, portable packaging, or release.

## Changes and scope

- `tests/e2e/local_core_browser.mjs`: opt-in actual Phoenix readback of the
  current Session's completed Go root `workspace.prepare`; validate lease,
  baseline, status, time, IDs, credentials and both Windows path spellings.
  Preserve its minimal span artifact and include the check in the denominator.
  Also test allocated-lease preparation failure and recovery through the browser.
  Compare the injected source/index before restoring the controlled fixture.
- `internal/sandbox/docker.go`: shared Docker/WSL Run/Start argv now contains
  `--pull never`, so execution cannot download a missing image through the
  host daemon. Existing network, filesystem, user and resource restrictions remain.
- `internal/sandbox/docker_test.go`, `selector_test.go`: both command builders
  require the flag; an opt-in real Go Runner test requires missing-image exit 125
  and the specific local `No such image` result.
- `docs/GOAL.md`, `docs/workspace-baseline.md` and this evidence document record
  current preparation status, the offline boundary, receipts and remaining gates.

No protocol, provider, frontend production source or dependency changed.
Existing operator repositories, identity and business data were not overwritten.
The user's existing `AGENTS.md` change is excluded from commits.
Review is `AI_REVIEWED`: Standards and Spec both have zero remaining findings;
this is not a `HUMAN_REVIEWED` data label.

## Final verification

| Command / seam | Complete result | Exit |
| --- | --- | ---: |
| `node --check tests/e2e/local_core_browser.mjs` | Syntax valid | 0 |
| `go test ./internal/worktree ./internal/session ./internal/handler ./internal/telemetry/genai -count=1` | Four related packages pass | 0 |
| `go test ./internal/sandbox ./internal/tools ./internal/cli ./internal/config -count=1` | Four related packages pass; final sandbox package checked again | 0 |
| `go test ./... -count=1 -json` | 1,356 passed / 56 skipped / 0 failed test actions; 43 passing test packages | 0 |
| `go vet ./...` | Pass | 0 |
| Explicit production browser + Phoenix | 16/16 passed, 0 not run; two Go processes | 0 |
| Explicit compiled Go Docker Runner | 3/3 passed, 0 skipped/failed; image inventory unchanged | 0 |
| Scoped diff checks and exact staged-path secret scan | Four approved source files; zero secret findings | 0 |

Browser command: `CODE_AGENT_RUN_LOCAL_CORE_E2E=1 CODE_AGENT_E2E_WORKSPACE=1
CODE_AGENT_E2E_PUBLIC_INPUT=chi-v5.2.3 CODE_AGENT_E2E_PHOENIX=1
CODE_AGENT_BROWSER_HEADLESS=1 node tests/e2e/local_core_browser.mjs`.
The failed direct public fetch was retried with per-command `HTTP_PROXY` and
`HTTPS_PROXY` at the repository-approved local proxy; system settings were not changed.

Input: MIT-licensed go-chi/chi revision
`9b9fb55def404397748a9fc7e044efe9db1d618e`, plus controlled
staged/dirty/new/empty/deleted/excluded states. This is a public-repository
preparation check, with zero model calls. The real local core serves the
existing A layout with no native Git in its PATH. Source files, original index,
Git status and browser assets match their initial hashes.

The new failure case inserts credential-shaped fixture content, creates a
Session in the browser and attempts preparation. The Ledger retains creation,
intent and failed outcome (three events), the allocated lease and baseline;
no copy or executing task appears. A different request cannot replace the
failure. Another Go process restores the same blocked lease/state. The test
checks source preservation before restoring its fixture, then checks the
original baseline again. Existing checks cover owner/root refusal, successful
lease recovery and retention of a modified isolated copy.

Phoenix backend image: installed
`arizephoenix/phoenix@sha256:3092f5543a3ddd35db7390cf971027c33be6be1f171274d57f3c8658c2193d67`.
Actual readback: project `default`, Trace ID
`5616322ce2d0c1eb0fb3ecc0173abddd`, Span ID `9679f68c017e4ba1`.
The short exporter batch interval is explicit in this test; it waits for backend
readback before the Windows test deliberately terminates its server process.
Product Trace readiness remains unknown until its own end-to-end contract passes.

Docker validation first dot-sources `scripts/rag-agent-e2e-runtime.ps1` and calls
`Wait-DockerDaemonReady`. The actual test binary is built with `go test -c` and
run through `go tool test2json` with `CODE_AGENT_RUN_DOCKER_SANDBOX_TESTS=1` and
`CODE_AGENT_RUN_SANDBOX_ROUTING_E2E=1`. It covers local-image execution,
read-only/no-network refusal and local missing-image refusal through Go.
WSL argv is checked by source tests; this run does not establish actual WSL execution.

## Immutable artifacts

| Receipt under `output/playwright/` | SHA-256 |
| --- | --- |
| `local-core-310dd414-824b-4da5-b82c-b9a3a27a38a1/receipt.json` | `793102885961562f526fca644c1b68584911ba40a9060093d9e67006f8b3910c` |
| `workspace-phoenix-regressions-f5d2e01f-f51d-4550-aa84-ac59909bf8f3/receipt.json` | `c01b8a082a35f6e66943bf454f5c2a147f576bc8431978c0cabab073b2a09a8a` |
| `offline-sandbox-34e00f2a-c8e7-4e84-841d-a459d3078669/receipt.json` | `a7936c937e085f907dff7772b0cfc0f1561e26d3eeafcbb98f5938a750112eff` |
| `workspace-offline-source-binding-cc7d4324-8f87-41f4-828d-4282d090d15d/receipt.json` | `b0d9c7548bc2a0a7bec85f44a66ee7e3877158e449553fc42086f9d773baa7a9` |

The final binding verifies all 463 browser, 441 regression and 438 Runner
source files against committed source, normalizing only Git CRLF conversion.
Original receipts record parent `d77af911` plus raw working-file hashes; the
separate binding attributes those exact tested bytes to `cc9e36d8`. Neither
original receipts nor source hashes were rewritten. Browser assets and every
listed screenshot/log/span hash were also checked.

Server binary SHA-256:
`ccaf126a7354a4731e6c2896b08a4189c2dc1b90cbeef7099c5c93063fc0a032`.
Runner test binary SHA-256:
`91b2148297682d16432e3ffe61a193443e963493c263087dc1eb0f45a21702b6`.
`workspace-trace.json` SHA-256:
`9e01a233a489a5c581919078b38872d5e303bc50fc4257631f9bdd2e4a599ec1`.

## Retained attempts and failures

The first bootstrap `local-core-a97100f9-f2ba-4a88-b4f7-d53265250371` failed
before browser execution: public `git fetch` exited 128 and the script exited 1;
13 checks were not run. It did not emit a browser receipt; the retained directory
and command failure remain a setup failure, not a successful denominator.

Subsequent immutable browser receipts are retained: `local-core-2762472d...`
13/13 (old script, Trace unknown), `local-core-fb239635...` 14/14 (before the
Windows path check fix), and `local-core-59bf5a48...` 16/16 (before the
pre-restoration preservation assertion). They do not replace the final 16/16.
Three intermediate full Go/vet receipts are also retained; only the final
unchanged-source run above is used for the current conclusion.

| Explicit RED artifact under `output/playwright/` | Result / SHA-256 |
| --- | --- |
| `local-core-2762472d-a747-4ce1-bce1-ad4090401be6/trace-red.log` | Exit 1; missing real readback; `1d05cde3b55fbafd454c34c9ee3335c7609bc19bfc060b7d68ff129261eb5efb` |
| `local-core-fb239635-9d8e-4cfb-9c81-f4411508d9ac/windows-path-red.log` | Exit 1; escaped Windows path missed; `80cf9a896e5d5fe8dcfaa1245d423de895f3497431b9d4b6d6a961449c3cd14a` |
| `local-core-fb239635-9d8e-4cfb-9c81-f4411508d9ac/failure-recovery-red.log` | Exit 1; allocated-lease failure/restart evidence absent; `76480b62b1a5da28a7bb108ac7932641927be8536b8e417fe7792d5ae656314c` |
| `local-core-59bf5a48-1120-4969-91fd-dff6b39b3b81/offline-image-red.log` | Exit 1; both Docker/WSL guard tests failed; `c86ee6b59ceb6675657347219caa3a2513999ac427d74956916689a5acf99817` |

## Remaining boundaries

#4 can close on its five preparation criteria. Its prior broader coding-task
and complete Trace blockers belong to #5–#19 and remain in the Goal.
#6 stays open: pinned Go/Python/Node task images, approved dependency sources
and preparation, product Runner admission and test output are still outstanding.
`--pull never` requires execution images to have been prepared locally; it does
not itself approve mutable image tags or provide a preparation stage.

No paid API calls occurred; the existing paid batch was not opened or reset.
Python/full frontend suites and release gate were not rerun for this Go/E2E-only
change. Existing browser assets were exercised and hashed. Raw artifacts remain
ignored and local; no credentials, machine paths or run trees are committed.
