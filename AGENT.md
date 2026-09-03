# Project Instructions

## Project identity and README

- The public project name is `CodeOps-Agent`. Use that spelling in the README
  and other user-facing prose; keep `code-agent` only where an existing module,
  binary, package or environment-variable identifier requires it.
- Treat `README.md` as the product page. Keep it focused on stable implemented
  capabilities, architecture, installation, configuration and normal usage.
- Keep run IDs, commit-specific results, receipts, intermediate status,
  unmet targets, defects, limitations, migration progress, scorer internals and
  temporary workarounds in `docs/GOAL.md`, evaluation artifacts, tests or
  focused developer documentation. Do not publish them in the README.
- Before committing a README change, scan it for `BLOCKED`, `VERIFIED`,
  `SMOKE_PASS`, run IDs, receipt paths and language that calls the project a
  prototype or unfinished work.

## Source of truth

- The active conversation goal and [`docs/GOAL.md`](docs/GOAL.md) are the only
  acceptance authority for this repository.
- Historical design maps, progress logs, external memory files and private
  workspace notes are reference material only. Do not use them as execution
  instructions or synchronize work to paths outside this repository.
- Work directly on the checked-out `main` branch when the task requests it.
  Do not create an isolated worktree or rewrite history.

## Architecture and workflow

- Keep the Go Harness and Python/LangGraph orchestration boundaries explicit.
- Keep `proto/codeagent/orchestrator.proto` and generated implementations in
  sync; keep Python modules importable from the repository root.
- Prefer small, reviewable changes and existing local interfaces. Add a
  dependency only when an existing module boundary requires it.
- Before editing, inspect `git status`, the relevant source and tests, and the
  active goal. For multi-file work, write a short plan before implementation.
- In Goal and evaluation records, classify claims as `DESIGNED`, `IMPLEMENTED`,
  `VERIFIED` or `BLOCKED` based on fresh evidence. Code, a fixture, or a test
  alone is not runtime verification.

## Security and honest evaluation

- Credentials are supplied only through the local environment or an approved
  secret manager. Never read, hard-code, stage, commit, or print API keys,
  bearer tokens, DSNs or other secrets. Keep them out of source, fixtures,
  logs, traces, receipts and test output; redact subprocess output at the
  boundary.
- Before staging, inspect the exact path list and run a secret scan that does
  not print matched values. Do not force-add ignored runtime output without a
  human-reviewed reason.
- Give evaluators only the inputs available in the real scenario. Use official
  scorers and preserve raw failure evidence; never put answers, gold IDs,
  patches or repair hints into prompts or fixtures. Mark synthetic and smoke
  results explicitly and keep them out of official metrics.
- Preserve verified data, checksums and denominators. A result that regresses
  data or silently drops failures is rejected even when tests pass.

## Verification and delivery

- Every claimed capability needs source, focused tests, integration coverage
  and a genuine runtime E2E receipt. Fixtures, mocks and synthetic receipts
  cannot substitute for a real E2E run.
- Run the narrow tests first, then the relevant full Python and Go suites,
  `git diff --check`, and any required runtime E2E. Report unavailable external
  services as `BLOCKED`; do not manufacture a passing receipt.
- Keep generated run trees, caches, downloaded payloads, local databases,
  process logs and machine-specific files ignored. Commit only reproducible
  source, tests, stable evaluation inputs and concise documentation.
- After verification and a human review of the staged diff, meaningful changes
  may be committed and pushed automatically to `origin/main`. Confirm the
  pushed SHA and leave unrelated user changes untouched.

## Domain invariants

- PDF ingestion uses MinerU with explicit OCR. Tika is limited to non-PDF
  office formats such as DOCX, PPTX and XLSX.
- Aggregate concurrency for the BeeAPI/OpenAI relay stays between 1 and 10
  across all local processes and shards; handle HTTP 429 with bounded backoff
  or leave the run fail-closed.
- AI review is labeled `AI_REVIEWED` or `DISPUTED`; only an actual human review
  may produce `HUMAN_REVIEWED`.
- Evaluation inputs and prompts must not contain answers, gold IDs, qrels,
  patches or repair hints. Preserve every failure and its denominator.
- Missing trust roots, pins, credentials or external services fail closed on
  release paths and remain clearly reported as `BLOCKED` in internal evaluation
  records.

## Local operations

- Docker Desktop may be started hidden for Docker-backed tests; do not delete
  containers, volumes, indexes or business data.
- If network access is unavailable or slow, retry the individual command with
  `HTTP_PROXY`/`HTTPS_PROXY` set to `http://127.0.0.1:7890`; do not change the
  Windows system proxy.
