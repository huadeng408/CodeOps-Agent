# Release Gate

`python -m eval.release_gate` is the single fail-closed release check for
CodeOps-Agent. It is read-only and emits a machine-readable report with
`--json`.

The gate requires all of the following:

- a resolvable Git `HEAD` and a clean worktree;
- successful Python and Go test commands;
- a source-bound workflow receipt with 8 workers, 200 tasks, 30 real
  process faults and recovery of at least 98.5%;
- a source-bound context receipt with at least 60% input-token reduction and
  no outcome regression;
- a source-bound Skill receipt with 40 runnable Skills, 1,000 locked cases,
  at least 948 correct selections and a verified model revision;
- a source-bound official SWE-bench receipt with the official scorer and at
  least 18 resolved instances out of the locked 20.

Every accepted receipt must include a run ID, trace ID, source Git pin,
artifact root and SHA-256 evidence pin. The source pin may equal `HEAD`, or it
may equal the direct parent of `HEAD` when the checked-in commit contains only
curated JSON receipts under `data/eval/*/receipts/`. `SMOKE_PASS`, missing
fingerprints, missing official scorer output, stale source pins and malformed
receipts are refusals. The command never prints receipt payloads or subprocess
output, so credentials cannot be promoted through the gate report.

Exit codes:

- `0`: `ELIGIBLE`;
- `3`: `BLOCKED` or a failed test/evidence check;
- `2`: gate invocation error.

The gate intentionally remains `BLOCKED` until fresh real runtime receipts are
generated against the current source commit (and then recorded in an
evidence-only receipt commit). Fixtures and development smoke runs are not
eligible evidence.
