# Release Gate

`python -m eval.release_gate` is the single fail-closed release check for
CodeOps-Agent. It is read-only and emits a machine-readable report with
`--json`.

The GitHub Actions release workflow runs this same check only when manually
dispatched. It performs the Go/Python regressions and audits curated evidence;
it does not provision provider credentials or generate missing runtime/scorer
receipts. Missing evidence returns `BLOCKED` (exit 3) and fails the manual run.
Ordinary push/PR CI remains the separate Go, Python and frontend workflow.
This is release-scope validation. Ordinary fixes report their declared unit,
integration and product checks separately under [AGENTS.md](../AGENTS.md);
unrequested release or model/Trace checks do not block those fixes.

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
artifact root and SHA-256 evidence pin. The source pin may equal `HEAD` or an
ancestor whose runtime inputs have identical final Git content and file modes.
The gate compares that source with the current tree, rather than requiring a
particular number of intervening commits. JSON directly under a supported
`data/eval/<lane>/receipts/` directory and regular,
non-executable explanatory Markdown (`README.md` and ordinary `docs/` pages)
may differ. Loaded instructions, Goal/acceptance policy, design/agent guidance,
prompts/templates, source, configuration, dependencies and task inputs remain
protected; symlink/executable-mode or unknown changes refuse inheritance.
Existing runtime symlinks are resolved in the Git tree; changed targets or
unconfirmed external/dangling/cyclic targets also refuse inheritance.
The same pin still requires confirmed inputs. Submodule pins and dirty or
untracked child files are checked recursively regardless of local Git ignore
settings; uninitialized or unconfirmed dependencies refuse release.
Dependency trees receive the same link confirmation; their file layouts do
not inherit the explanatory-document exemptions of this repository.
The report records current HEAD and the original equivalent source for each
accepted older receipt. Original receipts and their artifact hashes are not
rewritten. `SMOKE_PASS`, missing
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

The gate remains `BLOCKED` until every release lane has valid real runtime
evidence for the current runtime inputs. Unchanged inputs can retain their
original verified run after the binding check; changed or unconfirmed inputs
require new evidence. Fixtures and development smoke runs are not eligible
evidence.
