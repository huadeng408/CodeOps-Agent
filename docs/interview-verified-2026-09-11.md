# Interview Conversation Verification

Verified on 2026-09-11 (Asia/Shanghai), using the production Go server, Python
orchestrator, real provider and browser UI. Scope: the interview conversation
path below; the broader production Harness Goal remains BLOCKED.

## Follow-up: Memory Failure Isolation

Long-term memory search and event-context loading formerly shared one exception
handler: one invalid memory discarded an already verified event snapshot and
reported the wrong source as unavailable. These reads now fail independently.
Invalid memory is still rejected; its content and exception details are not
injected into the prompt. A regression using a real SQLite store, valid event,
and modified memory row fails against the previous `_initial_messages` function
and passes with the fix. Related checks: 46 passed, 1 skipped. Frozen-source
full regression: 2272 passed, 15 skipped, 3 warnings; Go tests, vet, and the
frontend production build also passed in that source state.

A read-only diagnostic of the local legacy memory table found four rows whose
checksums match a serialization excluding `created_at`; 705 rows matched the
current format at that observation. This fingerprint is not proof that timestamps
or provenance are trustworthy. No record was deleted, rewritten, re-signed, or
accepted under a weaker checksum algorithm. Legacy memory migration/recovery
remains open; the fix isolates its failure, not repairs the records themselves.

## Follow-up: Completed Checkpoint Retry

After deploying the retry fix, the previously failed request 203 was continued
through the browser's `继续任务` action. Run `run:0eac60bbb40ca95299131f2dfc8c05c7`
completed and appended `assistant/message` at sequence 1270, followed by
`session/run-completed` at 1271. It remained in the same session lineage; no new
session was created. Browser receipt is now 203 requests / 199 replies / 1272
events, with 8 recorded failed attempts retained for audit. Request 204 then
completed with the exact answer `青竹`; final browser receipt is 204 requests /
200 replies, with no visible alert. This meets the bounded 200-success target,
but does not prove arbitrary long-context retention.

## Follow-up: Anthropic Prefix Pressure Regression

The v8 server pressure probe used an 8192-token Python context window and the
configured real Anthropic-compatible provider. Request 121 failed closed:
Anthropic creates two runtime system messages, but compaction started at index
one and counted the second, unsourced system message as canonical history.
The regression in `tests/test_compaction_provenance.py` failed before the fix.
Compaction now preserves the complete unsourced system prefix while allowing
sourced system summaries to be compacted again. Go source validation is unchanged.

Request 122 completed through the real browser. Canonical events 763 and 764
contain two `context/compaction` replacements, covering 221 events and then the
previous summary respectively. Raw events remain in the ledger. At completion:
122 user requests, 119 assistant replies, 770 events, final sequence 769, hash
`2cdd3848677b6f41ead50dbb7cc27175cc7a97e4caf5c99357540db339aa1610`.
The answer retained the project marker, location, database, prohibited component,
acceptance phrase, and the full 7300 -> 8500 -> 8200 budget revision sequence.
Screenshot: `output/playwright/compaction-pressure-success.png` (local only).

The normal configuration was restored, both Go and Python restarted, and the
browser refreshed before continuing the same session. Full Python regression:
2271 passed, 15 skipped, 3 warnings. The preceding full Go race and vet processes
were polled to exit zero in this follow-up, not merely inferred from progress.

Post-restart requests 123-140 all completed, including another browser refresh
after request 125. Request 140 passed all ten literal recall checks without
including their answers in the prompt. Final receipt: 140 requests, 137 replies,
879 events, sequence 878, hash
`a29671cbd5ea303155a735cc56a4055d97deeb689a6a3ac874b8480e65e3b97e`.
The ledger retains four failed run attempts (three unanswered requests); these
are not counted as successful turns. Screenshot:
`output/playwright/interview-140-after-compaction-restart.png` (local only).
This is a repeated-constraint recall test, not proof of arbitrary retention.

## Follow-up: Browser Session CRUD Smoke

Using the real browser session, a temporary `临时验收会话` was created through
the `+ 新建` form, appeared in the project list with one event, and became the
selected session. Clicking `删除会话` showed the destructive-action confirmation
(`删除后会话只会标记删除，事件仍保留。继续吗？`). Confirming it removed the
session from the visible list with no alert. This is a smoke check for create,
select, delete and confirmation behavior; rename, status transitions, auth,
WebSocket ticket/reconnect and CAS coverage remain separate acceptance items.

The same browser session then renamed the long-dialogue session to
`真实多轮演示0911-续跑验收`, saved it, changed its status to `paused`, and
reloaded the page. Both the title and paused status persisted from the backend,
with no visible alert. This verifies the rename/status persistence path; auth,
WebSocket and CAS remain separate gates.

The browser WebSocket was also exercised across a real Go server restart. The
UI changed to `重连中` with `Bad Gateway` while the process was down, then the
standard helper restarted the configured server and the UI returned to `实时`.
The persisted event cursor stayed unchanged at 1279 and no duplicate event was
observed. A direct manual process launch without inherited provider environment
was intentionally discarded; the helper restored the authorized configuration.

## Follow-up: Browser Authentication

The browser completed the real registration form, automatically entered the
workbench, logged out to the login page, then logged in with the same test
account. A page reload retained the authenticated workbench and logout control.
No visible alert was observed. Credentials are local test artifacts and are not
included in this receipt.

## Follow-up: Browser API Boundary Checks

The original probe used two nonexistent identifiers:
`GET /api/v1/sessions/foreign-browser-check` and
`GET /api/v1/sessions/missing-browser-check` returned byte-identical `404`
responses (`session not found`). Despite its name, `foreign-browser-check` was
not a proven foreign session. That probe alone did not verify owner isolation.
A valid session returned `200` from `POST /api/v1/sessions/{id}/ws-ticket` with
a ticket field; the nonexistent session returned `404`. A stale status update
with `expectedSeq=1` returned `409` and reported the actual sequence 1280.
Ticket values were not printed or persisted in this receipt.

The subsequent real cross-owner probe verified `/api/v1/users/me` returned 200
with user ID 10. The existing session `dde16a9e3b2ca12420dcae4d0eb2a0b2` belongs
to owner 9 according to its canonical `session/created` event. Browser fetches
under user 10 compared that real foreign session with `missing-cross-owner-0911`.
Every endpoint below returned 404 for both, with byte-identical response bodies:

| Method | Session Endpoint Suffix | Foreign / Missing |
| --- | --- | --- |
| GET | (session detail) | 404 / 404 |
| GET | /events | 404 / 404 |
| GET | /runs | 404 / 404 |
| GET | /recovery-manifest | 404 / 404 |
| GET | /workspace-manifest | 404 / 404 |
| GET | /checkpoints | 404 / 404 |
| POST | /ws-ticket | 404 / 404 |

After these probes, the production Go ledger Verify and Surface projection both
passed: 1280 events, 200 assistant messages, 204 user messages, Surface size 188,
last sequence 1279, checksum
`fe06bc6f5554d89365da282765824589872aec678d69c5df91c1be7603998ae2`.
These are browser-origin API boundary checks, not UI-button interaction tests.

Boundaries: 200 successful replies were reached as recorded above. A read-only probe
found a checksum mismatch in the legacy long-term memory table; canonical
conversation history reads remain available. That memory record was neither
deleted nor accepted by bypassing validation. Markdown rendering was added later,
but the previous zero-table DOM observation did not verify a rendered table.
Neither long-term memory nor complete UI acceptance is claimed.

## Follow-up: Ledger-Backed Compaction

The compaction callback is now connected through SessionRunner. A summary is
accepted only when its reported event ID/checksum list exactly matches a
contiguous current Surface prefix, excludes the active input, and contains no
open tool call. The ledger appends a `context/compaction` Surface operation with
the verified source range; projection yields summary plus the untouched tail.
Repeated summaries retain nested source provenance, and continuation expands
only current-run summaries before prefix validation. Foreign runs, missing or
duplicate sources, orphan tool results, split tool pairs and invalid boundaries
fail closed. Provider payloads remain free of provenance metadata.

Regression coverage first failed on the unconnected callback and unsafe source
cases, then passed after the implementation. Full Go tests, vet, frontend
build, Python 2270 passed and full CGO race tests passed. The browser session
continued normally after deployment preparation; its latest verified receipt
is request 101 / 99 replies / 632 events. A forced-pressure browser probe was
previously fail-closed before persistence; a successful real-provider summary
replacement and post-restart compressed-history recall are still pending.

## Follow-up: Canonical Compaction Provenance Transport

ConversationMessage now carries optional event_id/event_checksum, and
CompactionUpdate carries source_events (event ID + checksum). Go derives these
from ledger events for message and tool history. Python retains references
through history conversion, pruning and summary recompaction, returning them
over gRPC. Provider adapters do not include this metadata in model payloads.
Legacy messages have no invented provenance; empty new fields do not change
legacy history digests. Protobuf code was regenerated using the repo script.

Tests cover ledger projection identity, Go gRPC round trip, Python gRPC
compaction, repeated summary provenance and provider-payload exclusion.
The initial source-loss tests failed before implementation and passed after it.
This is transport groundwork only: source references are NOT authorization,
and do not yet prove range completeness or that every selected message has a
canonical source. Summary replacement remains disabled/fail-closed pending
lease/CAS validation, complete-range checks, tool pairing, ordered Surface
projection and checkpoint/retry handling. No successful durable compaction is
claimed by this stage.

Browser dialogue reached request 100 with 98 replies, 626 events. Complete
budget recall and refresh at request 100 passed. After deploying the new Go and
Python protocol code, the first request-101 send was rejected before persistence
while Python was not listening; the draft remained intact. The startup helper
now requires the Python port as well as attached health before reporting ready.
Once services were available, clicking Send again completed request 101 with
correct budget history and constraints, with no duplicate user message.

Final API receipt: 101 requests, 99 replies, 632 events, sequence 631
session/run-completed. SHA-256 of UTF-8 JSON.stringify(response.data):
`8f291d0ec155dea47ea94ab1ff782ee186f619402a9ff02bcde5136d13dbeed2`.
Screenshot: output/playwright/interview-101-requests-desktop.png (local only).
Gates: full Go tests/race/vet, frontend build, Python 2270 passed / 15 skipped /
3 warnings, and diff checks passed. Startup helper ran successfully after its
readiness correction. The two earlier failed requests remain in the count;
200 successful paired turns and full production acceptance are not complete.

## Follow-up: Pressure Failure And New-Turn Recovery

Normal-window browser dialogue reached 80 user messages and 80 assistant
replies, 503 events. Refresh after turn 75 and full budget-history recall at
turn 80 passed. These topic-switch probes still do not prove pressure retention.

A real provider pressure probe temporarily configured Python's context window
to 8192 tokens. Request 81 asked for budget recall followed by a CONTEXT.md read.
It stopped at sequence 510 with the fixed public error
`context compaction could not be persisted; task stopped`. Tool-call count
remained 2 and assistant count remained 80: no subsequent Go tool executed.
This verifies fail-closed behavior, NOT working durable compaction. The missing
canonical summary replacement/range/recovery work below remains P0.

The gRPC regression first proved that a missing Compaction handler silently
allowed a later tool and success. It now rejects missing/failed persistence;
successful persistence still allows the tool. A second red/green case proves
a ledger connection error is not classified as retryable model transport.

After restoring the normal 256k window, request 82 exposed another real bug:
Python rejected a new user turn because an unfinished checkpoint belonged to
the earlier failed run. Go now derives NewTurn from the ledger's matching
user-message request identity and sends request-scoped metadata. Python only
discards a foreign run cursor for an explicit independent new turn with a new
Surface; ordinary retries and same-run recovery keep identity validation.
A real gRPC regression failed before this fix and passed after it. Same-surface
and retry/new-turn conflicts remain rejected.

After rebuilding Go, restarting Python and refreshing the browser, request 83
correctly recalled the entire budget process and all constraints. Final count:
83 user requests, 81 assistant replies, 523 events; successful reply sequence
521, run-completed sequence 522. Failed requests 81 and 82 remain in the ledger.
No claim of 83 successful turns is made. The demo service is back on 256k.

Local screenshots: output/playwright/compaction-pressure-fail-closed.png and
output/playwright/new-turn-after-pressure-recovered.png. Gates passed: full Go
tests, full Go race tests (CGO_ENABLED=1), go vet, frontend build, and Python
(2266 passed, 15 skipped, 3 warnings). The 200-turn and full Goal remain unmet.

## Follow-up: 60 Real Turns

On commit 0cf7f092, the same browser session completed turns 43 through 60 with
real provider replies. All sends used the UI textbox and Send button, waiting
for the assistant message before continuing. Refresh after turn 50 retained
all 100 user/assistant messages, and turn 51 continued normally.

Turn 60 asked for the complete budget revision history without supplying any
numbers. The answer retained Qingzhu, 7300 -> +1200 -> 8500 -> -300 -> 8200,
Hangzhou, SQLite, no Redis and MINT-482. This extends the corrected-history
evidence; it does not erase the failed turn 41 below.

The authenticated browser events API returned 383 events, 60 user messages and
60 assistant messages; last sequence 382 was session/run-completed. The final
answer was sequence 381. SHA-256 of UTF-8 JSON.stringify(response.data):
`f9879da4e50a78d8e9e5900071c18268800a2af467cef20219f237a4a4e422b0`.
Local screenshot: output/playwright/interview-60-turns-desktop.png.

No application code changed in this follow-up. It does not repeat the full
test gates from the preceding implementation stage. 200 turns remain pending.

### Next P0: Durable Compaction Integration

Current source inspection found that SessionRunner's ConversationHandlers only
sets Tool, leaving Compaction unset. The gRPC client only invokes the compaction
callback when non-nil, so the web path has no canonical summary replacement
write at that boundary. This is source evidence, not a forced-pressure browser
reproduction. The 256k-window dialogue above does not exercise this branch.

The existing CompactionUpdate protocol carries summary and message counts,
not canonical event boundaries or source Surface identity. Meanwhile
validateContinuationSurface requires the checkpoint prefix to remain intact.
Wiring the legacy CLI handler directly would therefore be insufficient.
The next change must establish exact source-range identity, leased/CAS
append-only replacement, and restart/retry validation of that replacement,
then prove them with failing-to-passing tests and forced-pressure browser E2E.

## Follow-up: Python History Loss Reproduced And Fixed

The same session now contains 42 user messages and 42 real assistant replies,
274 canonical events (last sequence 273, session/run-completed). This is not
42 successful memory probes: turn 41 failed to recall the original budget and
both adjustments. Turn 40 retained the current values but could not establish
their earlier history. Repeated current-value probes had masked this defect.

Python's legacy history conversion still capped message count, per-message
characters and total characters before model-aware compaction. Three real-runner
regression cases failed with a 256k context window, proving that facts were lost
before reaching the model even without context pressure. Durable resume now
bypasses those legacy caps; model-aware compaction remains responsible for
budgeting. Legacy non-resume callers keep their existing limits.

After restarting Python and refreshing the browser, turn 42 correctly recalled
7300 -> +1200 -> 8500 -> -300 -> 8200, without supplying those numbers again.
The failed answer is ledger sequence 265; the corrected answer is sequence 272.
Additional topic-switch turns covered idempotency, SQLite, WebSocket cursors,
lineage, approvals, summaries and Git diff; the page was also refreshed at turn 25.

Browser GET /api/v1/sessions/dde16a9e3b2ca12420dcae4d0eb2a0b2/events returned
HTTP 200. SHA-256 of UTF-8 JSON.stringify(response.data) at sequence 273:
`94b42ba7430f7045326b98e0803570b310decc599ae5ce63a33211b21704ed4a`.
This pins the local receipt, not a portable copy of the underlying ledger.

Local screenshots (not committed):
- output/playwright/history-truncation-before.png
- output/playwright/history-truncation-after-desktop.png (1440x1000)
- output/playwright/history-truncation-after-mobile.png (390x844)

Focused runner/server tests: 74 passed. The new three-case regression was run
red before the fix and green afterward. Follow-up gates passed: full Go tests,
go vet, frontend build, diff whitespace checks, and Python (2265 passed,
15 skipped, 3 warnings). Full Go race checks passed in the preceding stage;
this follow-up changes Python and documentation only and did not repeat race.
The 200-turn requirement and pressure-triggered real-provider compaction
retention remain unverified.

## Provider

The selected local configuration resolves to `https://beeapi.dev/anthropic`,
request URL `https://beeapi.dev/anthropic/v1/messages`, model `grok-4.6`.
Both Bearer and x-api-key probes returned OK (2.4s and 3.7s). Earlier upstream
failures do not establish current provider unavailability.

## Browser Evidence

Session: `dde16a9e3b2ca12420dcae4d0eb2a0b2`.

- 23 user messages and 23 actual assistant replies, 154 ledger events.
- Longest user input: 5226 characters. Two long background messages included.
- Original constraints: project Qingzhu, budget 7300, Hangzhou.
- Budget increased by 1200, then reduced by 300; final answer retained 8200.
- SQLite-only, no Redis, and test marker MINT-482 retained in later replies.
- Page refresh after turn 2 and turn 22 retained the correct current constraints.
- Go and Python restart after turn 3 retained conversation context; the page
  reconnected and an attempted send during downtime preserved its draft.
- Actual Glob and Read calls read CONTEXT.md and yielded successful tool results;
  the assistant correctly explained the Session Ledger definition.
- A long-message failure was reproduced and recovered with Continue Task using
  the same session. There was no duplicate user message after retry.
- Desktop 1440x1000 and mobile 390x844 screenshots are stored locally in
  `output/playwright/interview-23-turns-{desktop,mobile}.png`.

The repeated late-turn probes verify these specific facts; they do not prove
arbitrary knowledge retention, unlimited context, 200 turns or all Goal controls.

## Fixed During Verification

- Byte-based truncation split Chinese UTF-8 inside Go transport history, causing
  gRPC marshaling to fail before the model call. Legacy truncation now respects
  UTF-8 boundaries. Durable SessionRunner history bypasses transport truncation
  and is budgeted by Python context management.
- Tool events now expose tool names and persisted outputs to the browser.
- Conversation scrolling follows new replies when near the bottom, while leaving
  users inspecting older history in place.
- Mobile history scrolls separately with visible input and bottom clearance.

## Checks And Handoff

Full Go tests and go vet passed. Python: 2262 passed, 15 skipped, 3 warnings.
Frontend build passed. Full-repository race checks passed with
`CGO_ENABLED=1 go test -race ./... -count=1`.

Start from the repository root:

```powershell
powershell -NoProfile -File scripts/start-interview.ps1 -RestartOrchestrator
```

Open `http://127.0.0.1:3000/`. The helper uses the authorized local Desktop provider
file by default. It does not print credentials. Use your own registered account;
the verification account and browser session are local test artifacts.

For a concise demonstration, create a session, give it three constraints, revise
one constraint, refresh and ask it to recall the latest values, then ask it to
read CONTEXT.md. Normal messages require no manual checkpoint.

## Worktree Restore Atomicity Follow-up

`Manager.RestoreChecked` previously replaced its current map before validating
the complete snapshot. An invalid later entry discarded the previous state or
left a partial replacement. It now validates a candidate map under the existing
lock and installs it only after every entry passes.

- Red: `TestWorktreeManagerRestoreCheckedFailurePreservesState` failed in all
  eight cases before the implementation change (name, duplicate, base revision,
  path, agent identity, lease identity, expiry and status).
- Green: the same eight cases pass; successful replacement and an empty snapshot
  are separately covered.
- Passed: `go test ./tests/go ./internal/cli ./internal/worktree -run 'Worktree' -count=1`,
  `go vet ./internal/worktree ./internal/cli`, and `git diff --check`.
- Scope: metadata restoration only. No filesystem rollback, new persistence
  source or browser workflow was added. Browser Git/patch recovery acceptance
  remains BLOCKED; this result does not satisfy that broader goal item.
- The full-suite results above are historical, not a fresh full-suite run for
  this follow-up.

## Completed-response Replay Follow-up

The earlier completed-checkpoint test only called `load_checkpoint`. It did not
prove replay: `run` skipped done checkpoints, the normal checkpoint writer did
not retain response text, and the replay branch did not emit text to the Harness.
All three paths are now connected. The existing graph checkpoint is a pending
execution-result cache, not a writable replacement for canonical conversation
history. No new store or conversation-history writer was introduced.

- `test_completed_response_replays_after_reopen_without_model_call` first failed
  with one retry model call instead of zero. It now covers both same-run resume
  and a fresh retry run with the original predecessor, using the production
  checkpoint writer and reopened SQLite storage. Exact text and persisted root
  lineage are asserted.
- `test_completed_response_replays_across_python_processes` runs two independent
  processes: original model calls = 1, retry model calls = 0, identical response
  text, and original retry root. This uses a deterministic fake provider, not an
  upstream SLA or a browser transport-fault injection.
- Missing replay payloads fail closed for both unfinished and done checkpoints.
  Old response-less checkpoints cannot reconstruct the missing result. A normal
  new user turn remains separate from explicit checkpoint replay.
- Related tests: 47 passed. Frozen-source full gates: `go test ./... -count=1`,
  `go test -race ./... -count=1` (`CGO_ENABLED=1`), `go vet ./...`, and
  `npm --prefix frontend run build` all exited 0. `python -m pytest -q` exited 0:
  2277 passed, 15 skipped, 3 warnings in 265.57 seconds. No source edits or commits
  occurred while these gate processes were running.

Browser session `12c118f818cae371cc30a18a631c1cf4` was created using the actual UI
under the already authenticated test account. Three actual form submissions
returned provider-backed answers: project Baihua (Chinese name in the UI),
budget 9100 then 9700, deployment Suzhou, no Redis. Refresh retained the first
answer; after restarting the orchestrator with the standard launcher, the
ordered 13 pre-restart event IDs matched exactly (13 unique, last seq 12).
The third, mobile-viewport submission still returned budget 9700 and the other
constraints. The UI then showed 19 events / 6 messages and a completed run.

The two-turn desktop and 390x844 mobile screenshots were inspected:
`output/playwright/replay-followup-desktop.png` and
`output/playwright/replay-followup-mobile.png` (ignored local artifacts).
Message text and the composer did not overlap in these views. No claim is made
that every button, browser failure/retry lineage, or Git restoration passed in
this follow-up. Those broader acceptance items remain open.

## Fresh Browser Checkpoint/CAS Receipt (2026-09-11)

Using a newly registered browser account and the production launcher, Chromium
verified registration auto-login, session creation, natural-language submit,
WebSocket ticket/reconnect polling, and page refresh on a fresh disposable
session. The message produced a durable user/message fact and a queued/running
session/continued lineage without requiring a manual checkpoint for the
ordinary turn.

The visible checkpoint form listed only active surface events. Creating a
browser checkpoint appended one immutable checkpoint/create fact. Restoring a
missing hash returned one consistent 404. Restoring the same checkpoint with
stale expectedSeq=0 returned 409 and appended no event. Clicking the visible
restore control returned 200, appended exactly one session/rewind fact at the
current sequence, and left all prior event IDs and checksums intact.

The screenshot artifact is output/playwright/live-checkpoint-restore.png.
This receipt proves the browser checkpoint/CAS conversation-surface path only;
it does not prove filesystem/Git patch restoration, arbitrary 200-turn memory,
or every visible control. Those goal items remain BLOCKED pending fresh
evidence.

Fresh gates for this receipt: go test ./... -count=1 exit 0; python -m
pytest -q exit 0 (2277 passed, 15 skipped, 31 warnings); npm --prefix frontend
run build exit 0; go vet ./... exit 0; and git diff --check exit 0. Phoenix was
unavailable, so telemetry stayed degraded and no tracing success claim is made.

The final full race run, go test -race ./... -count=1, exited 0. Two earlier
full race attempts exposed a low-frequency failure in
TestSessionRunnerRecoversPendingApprovalAfterLedgerReopen (pending approval was
not observed once; the recovered run ended failed once). The isolated race test
then passed 20 consecutive runs and the final full run passed. This is recorded
as intermittent evidence rather than silently treated as a deterministic fix.

## Atomic Workspace Restore Primitive (2026-09-11)

Commit c77c4346 adds internal/worktree/restore.go with a small, deep restore
interface for file transitions. It validates every path beneath the workspace,
resolves existing parent symlinks, preflights every current file against its
expected After content, and performs no writes when any transition conflicts.
After a complete preflight it restores the previous Before contents, including
creation of nested parent directories. Regression coverage passes for successful
restore, multi-file conflict with zero partial writes, path traversal rejection,
and cancellation checks; the symlink case is skipped on this Windows runner
because creating symlinks requires an unavailable privilege.

Fresh checks for this primitive: go test ./internal/worktree ./internal/session
./internal/handler -count=1 exit 0 and the same packages under
go test -race ... -count=1 exit 0. This is a reusable filesystem primitive,
not proof of browser-visible Git/patch recovery: it is not yet wired to the
canonical Session ledger, checkpoint payloads, or a restore endpoint. The
broader filesystem/Git patch acceptance therefore remains BLOCKED.

The follow-up also keeps backend-only Before/After file contents in each
code/modified ledger payload, alongside the existing hashes. EventView still
returns only summary and digests, so the browser does not receive source
contents. A session-scoped `POST /sessions/:id/workspace/restore` endpoint now
binds one owner-scoped session, checkpoint, terminal run, and managed worktree;
it appends `workspace/restore-intent` and exactly one completed/failed outcome
to the same canonical ledger. The adapter reuses the atomic restore primitive
and never accepts file contents from the browser.

Focused session and handler regressions pass, including owner isolation,
foreign/missing 404 parity, stale CAS 409, successful file restoration, and
receipt ordering. Real Chromium verified registration, session creation,
natural-language submit, and execution-event growth; the disposable demo
session had no managed worktree or completed code-modification run, so the
visible file-restore click path remains `BLOCKED` pending a real agent
worktree receipt.

## Read Event Presentation Follow-up (2026-09-11)

The browser event stream no longer renders long Read payloads inline. Read
events show a single path/range summary by default; full request and tool
output are available only through collapsed native disclosure controls. This
keeps the execution timeline scannable while preserving the canonical ledger
and an explicit path to inspect details. A real Chromium refresh of the
interview session showed compact Read rows and no expanded code blocks by
default. Frontend TypeScript/Vite production build passed after the change.
