# Official Receipt Trace Join Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Attach real OpenTelemetry capture and Phoenix readback to future pinned Terminal-Bench and tau2 official receipts without representing historical or external runner steps as project-generated agent evidence.

**Architecture:** A small receipt-bound trace module owns only `eval.run`, `eval.instance`, and `scorer.official` scopes that execute in this repository. It explicitly reports absent `invoke_agent` and `chat` spans as incomplete. Receipt runners install capture before starting their official runner, persist trace summary/assertion before checksums, and may claim Phoenix only after source readback.

**Tech Stack:** Python 3.12, OpenTelemetry SDK, existing `TraceCapture`, Phoenix HTTP API, pytest, PowerShell receipt runners.

---

### Task 1: Honest receipt trace contract

**Files:**
- Create: `eval/harness/official_receipt_trace.py`
- Create: `tests/eval/test_official_receipt_trace.py`

- [x] **Step 1: Write failing tests for base receipt spans and missing agent spans**

```python
with OfficialReceiptTrace(run_id, instance_id) as trace:
    with trace.scorer():
        pass
assert evaluate_official_receipt_trace(capture.spans(), run_id, instance_id)["verdict"] == "INCOMPLETE_AGENT_TRACE"
```

- [x] **Step 2: Verify the test fails because the module is missing**

Run: `C:\Python312\python.exe -m pytest tests/eval/test_official_receipt_trace.py -q`

Observed: `ModuleNotFoundError: eval.harness.official_receipt_trace`.

- [x] **Step 3: Implement only real OTel scopes and fail-closed classification**

`OfficialReceiptTrace` creates no synthetic spans. The evaluator returns
`FAIL` when run/instance/scorer scopes are absent and
`INCOMPLETE_AGENT_TRACE` when Agent/Chat evidence was not actually emitted.

- [x] **Step 4: Verify the focused contract**

Run: `C:\Python312\python.exe -m pytest tests/eval/test_official_receipt_trace.py -q`

Observed: `2 passed`.

### Task 2: Terminal-Bench receipt integration

**Files:**
- Modify: `scripts/run-terminalbench-official-receipt.ps1`
- Modify: `tests/eval/test_terminalbench_official_runner.py`
- Modify: `eval/harness/official_receipt_trace.py`

- [x] **Step 1: Add failing trace-artifact and readback contract tests**

The contract fixture asserts `traces/trace-summary.json` and
`traces/span-assertion.json` exist before checksums, checks that missing real
Agent/Chat spans remain `INCOMPLETE_AGENT_TRACE`, and proves a configured OTLP
endpoint cannot set `phoenix_verified` unless Phoenix readback returns spans.

- [x] **Step 2: Verify RED for receipt writer/readback behavior**

Run: `C:\Python312\python.exe -m pytest tests/eval/test_official_receipt_trace.py -q`

Observed: two tests failed with `TypeError` because
`write_official_receipt_trace()` did not yet accept Phoenix readback inputs.

- [x] **Step 3: Add a reusable receipt trace writer**

The helper must install `TraceCapture` before entering `OfficialReceiptTrace`,
write capture-mode metadata and the trace assertion, flush the provider, and
write checksums only after every trace artifact. It must not mark Phoenix
verified without `read_run_spans` readback from a configured endpoint.

- [x] **Step 4: Wrap real Terminal-Bench execution and collection**

Modify the generated Python driver to open receipt scopes around the official
`Harness.run()` and raw-result collection. Keep model credentials process-only;
use no synthetic Agent/Chat scopes.

- [x] **Step 5: Verify unit/integration behavior**

Run: `C:\Python312\python.exe -m pytest tests/eval/test_official_receipt_trace.py tests/eval/test_terminalbench_official_runner.py -q`

Observed: `19 passed, 1 warning` (the warning is Terminal-Bench's upstream
SQLAlchemy deprecation).

### Task 3: Live current-HEAD receipt

**Files:**
- Create: `docs/PROGRESS-2026-08-14-OFFICIAL-RECEIPT-TRACE-JOIN.md`
- Create: `D:\Obsidian\code-autogrowth\私人\localcode\PROGRESS-2026-08-14-OFFICIAL-RECEIPT-TRACE-JOIN.md`

- [x] **Step 1: Validate infrastructure without model invocation**

Run Phoenix health and API schema checks; start no duplicate service. Confirm
the script has a unique receipt id and uses model concurrency one.

- [x] **Step 2: Run exactly one pinned public Terminal-Bench receipt**

Run the existing PowerShell script with a new id, scoped verifier proxy, and
one task. Do not modify task assets, official tests, scorer output, Docker
settings, or global proxy.

- [x] **Step 3: Verify evidence mechanically**

Verify receipt status, raw official output checksum, recursive checksums,
trace assertion, and Phoenix readback. Record `INCOMPLETE_AGENT_TRACE` if the
upstream agent does not emit the required spans; do not call it a complete
trace join.

- [ ] **Step 4: Commit and push only project files**

### Task 4: tau2 official migration

- [ ] Keep the legacy `tau_bench 0.1.0` adapter and receipts classified as
  development evidence only. Create a separate `tau2-bench v1.0.1`
  (`fc0055dc4e0a316c3f83133267fbd6faaa770992`) adapter from a pinned isolated
  checkout, preserve raw `tau2` output and checksums, and apply this same
  receipt trace contract without synthesizing Agent/Chat spans.

Stage only source/tests/docs; preserve existing untracked WIP.

## Review

This plan does not turn old receipts into current trace evidence, does not
promote an official failure, does not generate Agent/Chat spans by hand, and
does not address the separate document-native human-review gate. It is scoped
to the observable gap in official receipt execution.
