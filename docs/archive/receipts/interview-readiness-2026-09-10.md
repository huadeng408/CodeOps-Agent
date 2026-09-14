# Interview Readiness: 2026-09-10

Status: BLOCKED on a working external model route. No successful real-model
conversation or long-context retention claim is supported by this run.

## Implemented

- Natural-language submission starts a durable SessionRunner run with an
  automatic recovery anchor and stable request ID (in the current working tree).
- Run failures and pending execution are visible in the main conversation.
- The default conversation filter omits lifecycle/heartbeat noise; All retains
  the full execution record. Message counts count user/assistant messages only.
- Provider stream error events fail instead of being silently converted to done.
- Public run errors use fixed categories without embedding provider bodies.
- `scripts/start-interview.ps1` loads local configuration, starts existing project
  containers and production services, and checks workbench readiness. Credentials
  remain in child-process environments. Existing listeners are reused; stop the
  relevant process before using changed provider configuration.

## Verified This Run

- Browser registration and session creation through the UI.
- Message submission, automatic run start, and retry via Continue Task.
- Page refresh and Go backend restart retain the same session and user message.
- Failed-run feedback and re-enabled input, with heartbeat noise filtered out.
- `go test ./internal/session ./internal/handler -count=1`: PASS.
- `python -m pytest tests/test_llm_providers.py -q`: 47 passed.
- Frontend production build: PASS including the final filter-reset correction.
- Startup script executed successfully against existing project containers.

## Remaining

- `.env.local` model route rejected the existing credential with HTTP 401.
- Previously supplied alternate provider configuration listed models, but
  `gpt-5.6-sol` returned an upstream_error or empty response, and
  `gpt-5.6-terra` / `gpt-5.4-mini` probes timed out without usable text.
- The configured local proxy port 7890 was not accepting connections.
- Obtain a working authorized model route, then validate actual assistant replies,
  multiple follow-ups, facts revised mid-conversation, refresh/restart recall,
  tools/approval results, and long-context compaction through the real browser.
- Latest full-repository Go/Python/race gates, release receipts, staged secret
  scan, commit and push are not complete for this working tree.

Local runtime logs and screenshots stay under ignored `.tmp/` and
`output/playwright/`; they are not source artifacts or production receipts.
