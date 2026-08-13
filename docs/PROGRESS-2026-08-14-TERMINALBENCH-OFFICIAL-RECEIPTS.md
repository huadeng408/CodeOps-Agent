# 2026-08-14 Terminal-Bench Official Receipts And Agent Boundary

## Scope

This record covers one fixed public Terminal-Bench task and the execution
boundary around it. It does not claim a benchmark score, a hidden-holdout
result, or a model identity attestation.

The task was `break-filter-js-from-html` from the local public
Terminal-Bench v2 snapshot. The raw input family SHA-256 was
`4bab83828d145cdb8378eea9f00c2fec5c90b32f3f4cd076c4111d6e4191c353`.
Every run used official `terminal-bench` `0.2.18`, one task, one attempt, and
one concurrent trial. The relay request limit therefore remained below the
global cap of 10.

## Implemented Receipt Boundary

- `IMPLEMENTED`: `eval.benchmarks.terminalbenchofficial` validates a pinned
  public input, the LiteLLM-style model name, and concurrency; copies official
  `results.json` and `run_metadata.json`; creates a SHA-256 manifest; and maps
  only an official `is_resolved=true` to `OFFICIAL_PASS`.
- `IMPLEMENTED`: `scripts/run-terminalbench-official-receipt.ps1` loads the
  BeeAPI key only into the child process, uses the independently verified
  `https://beeapi.ai/v1` Responses endpoint, writes separate stdout/stderr,
  and records the wrapper exit code without storing the credential.
- `VERIFIED`: targeted receipt and wire-selection tests, plus benchmark/run
  regression tests, passed as `38 passed` with one upstream SQLAlchemy
  deprecation warning.

## Real Official Runs

| HEAD | Run directory | Official result | What it established |
| --- | --- | --- | --- |
| `143d082d` | `eval_results/terminalbenchofficial/current-head-20260814-062700` | `OFFICIAL_FAILURE`, `unknown_agent_error`, raw SHA `825aa76bd1d3f8f521b97ebbb0b327f0f641b60674287faffd1c53537487b244` | Docker compose, image build, official agent session, verifier and cleanup all ran. The old chat-completions agent failed with a relay connection error. |
| `ba56e1f3` | `eval_results/terminalbenchofficial/current-head-20260814-063500` | `OFFICIAL_FAILURE`, verifier failed, official process exit `0` | BeeAPI Responses endpoint was reachable and the model was called (`4505` input and `649` output tokens), but a raw-task-only Responses prompt produced only a test command and no solution file. |
| `9f17b709` | `eval_results/terminalbenchofficial/current-head-20260814-064300` | `OFFICIAL_FAILURE`, `parse_error`, raw SHA `c275b07ab67c905bb5486f118bf967a00c4ac1bb07d2b28af0fa488160ca0b47` | The full command contract reached the model (`4739` input and `53` output tokens). It inspected `filter.py` and `test_outputs.py`, then stopped without producing `/app/out.html`. The verifier separately failed to download `uv` from GitHub, so it emitted no valid short test summary. |
| `1f1d6394` | `eval_results/terminalbenchofficial/current-head-20260814-082200-proxy` | `OFFICIAL_FAILURE`, official test `1 failed`, raw SHA `cdba9502dcb71b707de69fd5e4c23f30b4834ea883e3e56c34998765aec64c9a` | The bounded two-turn agent ran and emitted `/app/out.html`; the scoped verifier proxy allowed apt, `uv 0.9.5`, and pytest to complete. The official test rejected the payload, so this is a valid model failure rather than an infrastructure/parse failure. |

All four artifact trees have canonical `receipt.json`, copied upstream scorer
files, and `checksums.sha256`. The Docker compose projects were brought down;
no Terminal-Bench container remained after each run.

## Root Cause And Stop Rule

The first run was a provider-wire mismatch: the approved relay advertises the
Responses API, while the historical agent used Chat Completions. A minimal real
Responses health request subsequently returned text and a response id. The
second run established that the prompt contract also must be passed as a
developer instruction. The third run established that the remaining agent is
not multi-turn despite its historical comment: it can send a plan to tmux but
never reads terminal output back into another model turn.

After these three different real outcomes, a fourth identical one-shot run
would not test a new hypothesis. It is intentionally not started. The next
Harness change must be a bounded multi-turn terminal-feedback agent with a
separate command/response transcript artifact and explicit turn/token limits.

The verifier's `uv` download failure was resolved without patching the task,
its test, or the official scorer. `eval.swebench_work.terminalbench_proxy`
generates an ephemeral Compose overlay only when the receipt script receives
`-VerifierProxy`; it injects the host-reachable proxy into the verifier
container, rejects credential-bearing URLs, restores the parent process
environment, and writes `verifier-proxy-manifest.json` next to the receipt.
The default remains disabled. Windows path length is controlled separately by
a stable short internal Harness id (`tb-<12 hex>`), while the external receipt
directory retains the descriptive run id.

## Current Status

- Harness: `IMPLEMENTED` receipt boundary, bounded two-turn agent, scoped
  verifier proxy, and short Windows-safe Harness ids; `VERIFIED` official
  execution through a normal scorer verdict. A passing Terminal-Bench receipt
  remains unproven because the model's tested solution did not pass.
- Multimodal RAG: unchanged by this work. All PDF paths remain MinerU with
  explicit OCR; Tika remains restricted to non-PDF Office files.
- Evaluation set: the receipts are public development evidence only. They do
  not replace human qrel review or a net-new hidden holdout.
- Observability: the Terminal-Bench official runner does not yet emit the
  unified Phoenix chain. The existing O3 receipt remains the relevant current
  evidence for that track.

## Next Automatic Work

1. Preserve this fixed public development task as a single-item receipt only;
   do not optimize against it or recast it as a benchmark score.
2. Implement the separately audited, pinned `tau2-bench v1.0.1` official
   adapter in an isolated environment; old `tau_bench 0.1.0` results remain
   development evidence only.
3. Add the independently eligible ScreenSpot-Pro bbox benchmark as a GUI
   localization track, without conflating it with RAG or document QA.
