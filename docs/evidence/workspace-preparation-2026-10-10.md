# Task Workspace preparation evidence — 2026-10-10

Engineering state: `IMPLEMENTED`; full Issue #4 acceptance: `BLOCKED`.
User approved layout A before frontend changes. Original baseline evidence
and its failures remain in `workspace-baseline-2026-10-10.md`.

The implementation reaches `internal/worktree`, canonical Session Workbench,
the authenticated local-core HTTP routes and `frontend/src/TaskWorkspacePanel.tsx`.
It reuses Git hardening, rooted reads, existing lease IDs, identity ACLs,
Ledger/CAS and the existing browser test entry. No dependency, second mutable
Session store, automatic cleanup or Python filesystem side effect was added.

## Public checks

Target command:

```text
go test ./internal/worktree ./internal/session ./internal/handler ./internal/serverconfig ./internal/localidentity ./internal/safety -run 'TestTaskWorkspace|TestCaptureBaseline|TestPin|TestGitEnvironment|TestLocalCoreRejectsUnsafeConfiguration|Test.*Identity|Test.*Private' -count=1
```

Exit 0 across all six packages. A subsequent public TaskWorkspace recovery
regression, including changed task index rejection, also exits 0.
Checks cover current-copy existence/content/modes, missing vs empty, source
index/status preservation, stale baselines, owner/cursor/repository denial,
idempotency, incomplete intent, malformed Git link, new/changed task files,
task index digest, credential shapes, junction storage, checkout filters,
directory/file replacement and a local HTTP observer seeing no partial-clone
fetch. `go vet ./...` and `git diff --check` exit 0. Frontend tests: 12 passed,
0 skipped, exit 0; `npm run build` typechecks and builds, exit 0.

Fresh full Go command: `go test ./... -count=1 -json`, 1,301 test actions
passed, 54 skipped, 0 failed, 43 packages, exit 0, source hashes unchanged.
Receipt: `output/playwright/gateway-final-go-ad6e8e09-d2bb-439a-8e18-87486232ff20/receipt.json`;
its source manifest records all modified/new Go/Python/protobuf files.
This is source regression, not a runtime or release receipt. Python source,
protobuf and dependencies were unchanged; the Python suite was not rerun.
Receipt SHA-256: `dd997b00fff7d8bf4ef1a8ee13b5f4a8bf0090764bc55b350e7b0da1f60aea59`.
The final control-file whitespace regression was added afterwards: RED
exit 1, then complete worktree and session suites exit 0. Final committed-source
verification follows this parser change rather than inheriting the earlier run.

## Production entry, controlled input

```text
CODE_AGENT_RUN_LOCAL_CORE_E2E=1 CODE_AGENT_E2E_WORKSPACE=1 CODE_AGENT_BROWSER_HEADLESS=1 node tests/e2e/local_core_browser.mjs
```

Run `local-core-7c0b76ab-8bc5-4465-90f5-b97764bd9077`:
13/13 checks, exit 0, Go PIDs 52452 and 4880. Receipt:
`output/playwright/local-core-7c0b76ab-8bc5-4465-90f5-b97764bd9077/receipt.json`.
SHA-256: `1047c22399e732e626fd8b402a8fc55d26abe61f2cebdff171a2567126247560`.
Starting Git SHA: `8b94b22747437a8044e9f69693a6b3342141d1a3`;
the receipt lists exact modified/new source hashes and built asset hashes.
Neither source nor assets changed during this run. This is pre-commit evidence;
it does not alone bind the final commit or establish `VERIFIED` acceptance.

The real built server and browser initialize a random durable local identity,
prepare a real registered detached Git worktree from controlled dirty/new/
empty/deleted files, exclude the credential file and compare original index,
HEAD, status and content hashes. They reject another request/unapproved root,
restart Go, recover the same lease/baseline, and report modified isolated
content blocked while retaining it. The other nine checks retain the original
auth/history/token/refusal/disconnection/logout scope. Screenshots and process
outcomes are hashed in the receipt. Model calls: 0; Trace backend: unknown.
This is product preparation runtime with a fixture input, not a coding task,
ten-turn conversation, real reflection or official scorer run.

## Failures and review

All failures remain; successful cases do not replace their denominators:

| Run | Result | Cause |
| --- | --- | --- |
| `local-core-b7286ed2-33ac-4b17-b9f9-9c095666a85b` | 2/13, exit 1 | Incorrect test selector for the existing working-directory input |
| `local-core-9f5d9ad6-d5a5-4d36-b459-135376e895e9` | 3/13, exit 1 | Product owner was hardcoded to 1; actual local identity uses a random ID; source also changed during this failed run |
| `local-core-07cce4c2-3e31-4963-8247-cc30184569ff` | 8/13, exit 1 | Old global state-label assertion counted the new workspace label; scoped to the capability panel |

Post-commit `a0d37a29` browser run
`local-core-28107d9b-a00f-4669-83e7-a6c6987a93e9` passed 13/13, exit 0.
However `go vet ./...` exited 1 when Go discovered deliberately invalid generated
fixture code retained under `output`. Full Go run
`gateway-final-go-69adbd6a-5d77-4fce-95fc-76ce9fbac813` retained 1,301 passed/
54 skipped test actions but **exit 1**, so it is not a passing full regression.
No failure directory or receipt was deleted. An ignored local `output/go.mod`
isolates these generated trees without modifying their contents. New browser
fixtures use retained OS temporary storage outside the project module, and the
mutation case remains syntactically valid Go. `go vet ./...` then exits 0;
the new input-isolated runtime run `local-core-e06acd99-6b49-4fd7-b774-dc637ba23112`
passes 13/13, exit 0. Final source-bound verification follows this harness fix.

Both `code-review` axes are `AI_REVIEWED`, not `HUMAN_REVIEWED`.
Confirmed findings fixed: real owner wiring, mandatory Git-file header,
post-pin metadata revalidation, and partial-clone implicit network access.
The final review also caught whitespace normalization of damaged Git control
files. Only CR/LF terminators are now trimmed; public malformed-link, HEAD,
commondir, lock and backlink regressions cover this variant.
Public RED/GREEN also caught insufficient Windows handle access for replacement
protection, overly restrictive sharing that blocked Git directory flush,
relative `os.Root.OpenRoot.Name()` use and missing index provenance. Test and
runtime failures were retained rather than worked around with cleanup/sleep.

## Remaining boundary

Pins cover existing entries, not the absence of optional metadata. Concurrent
insertion of an alternate or include-related entry has not been proven unable
to affect a native Git read. Keep the full path/race criterion `BLOCKED`; do
not attach tool execution or claim a completed code task on this evidence.
Unix native preparation remains refused. Complete production CLI/HTTP failure
recovery, authorized reconciliation, real model tasks and full Trace readback
remain outstanding. #4 stays open.
