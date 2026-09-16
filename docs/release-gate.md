# Release Gate

`python -m eval.release_gate` is the single fail-closed release check for
CodeOps-Agent. It is read-only and emits a machine-readable report with
`--json`.

The gate requires all of the following:

- a resolvable Git `HEAD` and a clean worktree;
- successful Python and Go test commands;
- a source-bound extension-onboarding receipt comparing the same module scope,
  with a baseline of at least 16 engineer-hours and plugin-based delivery in at
  most 8 engineer-hours, including targeted tests and runtime E2E;
- a source-bound workflow receipt with 8 workers, 200 three-stage dependency
  pipelines, 30 real process faults, all 600 stage checkpoints completed, at
  least 30 durable recovery events and task recovery of at least 98.5%;
- a source-bound context receipt with at least 60% input-token reduction and
  no outcome regression under one pinned task, verified model revision and
  fixed per-arm budget;
- a source-bound Skill receipt with 40 runnable Skills, 1,000 locked cases,
  at least 948 correct selections, a verified model revision and a passing
  production discovery/lazy-load matrix for every Skill;
- a provider-backed independent-Agent receipt proving Agent Card, text/file/JSON
  Message, Task lifecycle, pinned Artifact, isolated child context, lazy Skill,
  Harness-authorized MCP and sandbox use, reflection, restart recall, Ledger
  integrity and one backend-read OpenTelemetry trace;
- a source-bound Terminal-Bench receipt with at least one provider-backed task
  resolved by the official `terminal_bench.Harness`, including official-runner
  trace parentage and backend readback;
- a source-bound official SWE-bench receipt with the official scorer and at
  least 18 resolved instances out of the locked 20, plus the pinned 8/20
  baseline receipt.

Every accepted receipt must include a run ID, trace ID, source Git pin,
artifact root and SHA-256 evidence pin. The source pin may equal `HEAD`, or it
may equal the direct parent of `HEAD` when the checked-in commit contains only
curated JSON receipts under `data/eval/*/receipts/`. `SMOKE_PASS`, missing
fingerprints, missing official scorer output, stale source pins and malformed
receipts are refusals. The pinned source tree must also have an empty dirty hash
and zero untracked files. The command never prints receipt payloads or raw
subprocess output; failed test checks may include a short, credential-redacted
tail so the gate report identifies the failing boundary without promoting
secrets.

The curated receipt lanes are `extension-onboarding`, `workflow`,
`context-token`, `skills`, `agent-e2e`, `terminalbench`, and `swebench`. A lane
may be promoted only from its complete ignored run tree; hand-authored summary
fields without the raw checksum pins do not satisfy the shared receipt base.

Exit codes:

- `0`: `ELIGIBLE`;
- `3`: `BLOCKED` or a failed test/evidence check;
- `2`: gate invocation error.

The gate intentionally remains `BLOCKED` until fresh real runtime receipts are
generated against the current source commit (and then recorded in an
evidence-only receipt commit). Fixtures and development smoke runs are not
eligible evidence.
