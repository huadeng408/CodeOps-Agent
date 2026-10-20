# Local core profile

The local core serves the built browser UI, authentication and Session history
without MySQL, Redis or MinIO. It binds to loopback and checks browser origins,
persisted identity, Session ownership and single-use WebSocket tickets.

Build the UI with `npm run build` from `frontend/`. From the repository root,
set `CODEAGENT_CONFIG=./configs/local-core.yaml`, supply `JWT_SECRET` through the
process environment or an approved secret manager, then run `go run ./cmd/server`.
Open <http://127.0.0.1:8081>. If changing the port, also set
`CODE_AGENT_ALLOWED_ORIGINS` to the browser's exact origin.

For first identity initialization, supply `CODE_AGENT_LOCAL_SETUP_USER` and
`CODE_AGENT_LOCAL_SETUP_PASSWORD` through the same approved process boundary.
Use the matching credentials on the browser's Register form. The password must
contain at least 12 characters. A different or second registration is refused.
After setup, these two variables can be removed; login uses the persisted
password hash. Keep the same JWT secret across process restarts to retain cookies.
Do not put these values in command history, tracked config or screenshots.

Identity and token-revocation facts live in a dedicated private SQLite directory;
Windows restricts its ACL to the process user. Session events continue to use the
canonical Session Ledger. These paths must be distinct, must not contain symlinks
or reparse redirects, and must not repurpose a legacy database. The core does not
guess or remap identities from the full-stack service. Existing full-stack data
stays on the existing `configs/server.yaml` path; migrate only with an explicit,
reviewed identity mapping and import procedure.

`GET /api/v1/capabilities` requires authentication and reports history as ready,
execution as blocked, optional RAG as degraded, and unverified trace export as
unknown. Missing execution prerequisites return 503 and retain the browser draft.
Optional service routes also return authenticated 503 responses.

The browser's right-hand review panel shows these statuses and their reasons,
with a button to recheck the current configuration. Current acceptance and
evidence belong in [GOAL.md](GOAL.md).

Run the real process/browser checks after building the UI:

```powershell
$env:CODE_AGENT_RUN_LOCAL_CORE_E2E = '1'
go test ./tests/e2e -run TestProductionLocalCore -count=1 -v
node tests/e2e/local_core_browser.mjs
```

`tests/e2e/full_stack_startup.ps1` checks the legacy entry using independent
cached-image service containers. It stops and preserves only the containers it
created. Browser receipts and screenshots stay under ignored `output/playwright/`.
These checks exercise authentication and history; real model execution and the
paid coding-task acceptance are separate tasks.
