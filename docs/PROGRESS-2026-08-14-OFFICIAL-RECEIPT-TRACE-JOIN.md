# 2026-08-14 Official Receipt Trace Join

## Scope And Decision

This increment closes the receipt-level Phoenix evidence gap for one new,
fixed, public Terminal-Bench task. It does not claim a passing benchmark
result, a complete Agent trace, a human review, or a tau2 official run.

`eval/harness/official_receipt_trace.py` owns only the local work actually
performed by this repository: `eval.run`, `eval.instance`, and
`scorer.official`. It explicitly classifies absent real `invoke_agent` and
`chat` spans as `INCOMPLETE_AGENT_TRACE`; no receipt code hand-creates those
upstream execution spans.

## Implemented

- `IMPLEMENTED`: receipt trace writer records in-process spans, then replaces
  its assertion source only when Phoenix API readback returns spans for the
  specific `eval.run_id` and instance. Empty or failed readback is fail-closed:
  `phoenix_verified=false` and `collector=otlp-http-unverified`.
- `IMPLEMENTED`: `scripts/run-terminalbench-official-receipt.ps1` accepts a
  Phoenix URL, passes its OTLP endpoint only to the launched receipt process,
  records a UTC readback lower bound, flushes the provider, and retries
  readback at most five times with one-second gaps.
- `IMPLEMENTED`: outer finalizer regenerates `checksums.sha256` only after the
  Python driver and its redirected stdout/stderr have reached their final
  state. This prevents a late stdout write from invalidating a receipt.

## Verification Evidence

Focused verification on this HEAD:

```text
C:\Python312\python.exe -m pytest tests\eval\test_official_receipt_trace.py tests\eval\test_terminalbench_official_runner.py -q
19 passed, 1 warning in 7.17s
```

The warning is Terminal-Bench's upstream SQLAlchemy deprecation. PowerShell
parser, embedded Python-driver compilation, `py_compile`, and `git diff
--check` also succeeded.

Phoenix preflight at `http://127.0.0.1:6006` returned HTTP 200 for `/healthz`
and `/v1/projects`; no service was started or replaced.

## New Real Receipt

| Field | Evidence |
| --- | --- |
| Receipt | `eval_results/terminalbenchofficial/current-head-20260814-phoenix-readback` |
| Task | public `break-filter-js-from-html` |
| Model concurrency | 1 |
| Official result | `OFFICIAL_FAILURE`, 0 resolved / 1 unresolved |
| Official output SHA-256 | `d9b90970b6059b56d3f3ddf5f7883042154f5872956a9e6b64a0357328f060cb` |
| Phoenix project | `default` |
| Phoenix readback | `phoenix_verified=true`, three spans, trace `6da47f1f10929c473dd748387c53f91e` |
| Trace assertion | `INCOMPLETE_AGENT_TRACE` |
| Receipt checksum audit | 25 entries, 0 mismatches |

The three Phoenix-returned spans are a real parent chain:
`eval.run -> eval.instance -> scorer.official`. `invoke_agent` and `chat`
are absent, so the result cannot be treated as a complete Agent/Chat trace
join. The official task failure is preserved as a model outcome; no task,
test, official scorer, or prompt was changed after the run.

## Bug Found And Corrected

The first audit found one mismatch: `stdout.log`. Root cause: checksum refresh
ran in the Python driver before the outer PowerShell redirection received the
driver's final JSON output. The corrected finalizer writes the exit code and
then refreshes all checksums after the driver exits. The existing receipt was
re-pinned after it had fully stopped; this changes only the manifest, not raw
official output or trace evidence.

## Remaining Work

- `BLOCKED` on complete Terminal-Bench Agent tracing: upstream
  `DeepSeekTBAgent` has not emitted genuine Agent/Chat OTel spans. Add
  instrumentation around its real provider requests and task execution before
  any later receipt, then run one new fixed public receipt. Do not retrofit
  the existing receipt.
- `DESIGNED`: migrate tau2 evaluation to pinned upstream `tau2-bench v1.0.1`
  in an isolated environment. Legacy `tau_bench 0.1.0` outputs remain
  development evidence only.
- `BLOCKED`: human-reviewed document-native bbox qrels and a clean holdout;
  ScreenSpot-Pro is a separate GUI grounding track and does not substitute for
  this gate.

## Four Workstreams

- 自研 Harness 90% `[#########-]`: one additional official receipt boundary is
  verified; the pinned tau2 official adapter and a passing independent sample
  remain.
- 多模态 RAG 84% `[########--]`: unchanged; MinerU plus explicit OCR remains
  the only PDF route.
- 评测集 74% `[#######---]`: this is a public development receipt, not a
  holdout; real human bbox review and new holdout are still missing.
- 可观测性 89% `[#########-]`: Phoenix now readbacks receipt boundaries, but
  official Terminal-Bench Agent/Chat and long-term metrics/alerts remain.

Most needed next: genuine OTel instrumentation of the real benchmark agents,
then an untouched public official sample that passes. The least certain point
is whether either upstream agent exposes a sufficient provider boundary without
altering official task behavior. The largest likely omission is still the
absence of license-clear, genuinely human-reviewed document-native bbox qrels.

## tau2 Official Receipt Extension

`IMPLEMENTED`: `Tau2OfficialRunner` now runs an isolated checkout of
`sierra-research/tau2-bench` `v1.0.1` at
`fc0055dc4e0a316c3f83133267fbd6faaa770992` through the upstream `tau2` CLI.
The legacy `tau_bench 0.1.0` adapter and historical scores remain development
evidence only. `collect_receipt()` persists `receipt.json`, and the source
data pin comes from `git ls-tree -r HEAD data`, so generated
`data/simulations/` output cannot alter an input-data hash.

`VERIFIED`: the isolated checkout completed `uv run tau2 --help` and
`uv run tau2 check-data`, followed by exactly one public `mock/create_task_1`
task with one trial, seed `42`, and model concurrency `1`. Receipt
`eval_results/tau2official/current-head-20260814-phoenix-readback-source-pin`
records source-data SHA-256
`df29afa3d8fbce072dae983c75b166548e1cea48c5e58737e0d5b29349c0441d`.
The official CLI exited `0`, while the official score was `OFFICIAL_FAILURE`
with reward `0.0`, `action_match=false`, and `db_match=false`. This is a
scored model outcome, not a runner failure. No public task, scorer, or prompt
was altered after the result.

Phoenix readback is `phoenix_verified=true` for trace
`ed86b6d750dd0a74d90bf897b91f8198`, but it contains only
`eval.run -> eval.instance -> scorer.official`. The assertion is correctly
`INCOMPLETE_AGENT_TRACE`: real `invoke_agent` and `chat` spans were not
emitted. The receipt has 11 checksum entries with zero mismatches. LiteLLM's
relay-model cost-map warning did not prevent execution and is not treated as a
scoring error.

Focused verification:

```text
C:\Python312\python.exe -m pytest tests\eval\test_tau2_official_runner.py tests\eval\test_official_receipt_trace.py -q
11 passed in 0.59s
```

The tau2 adapter and one real official receipt increase Harness evidence, but
do not increase the reported percentage: an independent passing sample and
real Agent/Chat spans remain required. No historical receipt is being
retroactively reclassified.
