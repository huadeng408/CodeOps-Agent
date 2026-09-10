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
and passes with the fix. Related checks: 46 passed, 1 skipped.

A read-only diagnostic of the local legacy memory table found four rows whose
checksums match a serialization excluding `created_at`; 705 rows matched the
current format at that observation. This fingerprint is not proof that timestamps
or provenance are trustworthy. No record was deleted, rewritten, re-signed, or
accepted under a weaker checksum algorithm. Legacy memory migration/recovery
remains open; the fix isolates its failure, not repairs the records themselves.

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

Boundaries: 200 successful replies remain pending. A separate read-only probe
found a checksum mismatch in the legacy long-term memory table; canonical
conversation history reads remain available. That memory record was neither
deleted nor accepted by bypassing validation. The UI also renders Markdown tables
as plain text. Neither long-term memory nor complete UI acceptance is claimed.

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
