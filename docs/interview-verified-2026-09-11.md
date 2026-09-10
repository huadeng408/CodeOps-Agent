# Interview Conversation Verification

Verified on 2026-09-11 (Asia/Shanghai), using the production Go server, Python
orchestrator, real provider and browser UI. Scope: the interview conversation
path below; the broader production Harness Goal remains BLOCKED.

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
