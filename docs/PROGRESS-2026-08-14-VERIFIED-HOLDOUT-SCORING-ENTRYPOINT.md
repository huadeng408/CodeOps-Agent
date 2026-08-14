# Verified Holdout Scoring Entrypoint - 2026-08-14

## Implemented

`orchestrator.eval.runner.run_verified_holdout_eval()` is the only new
external-holdout scoring entrypoint. It accepts external paths for the sealed
manifest, questions, qrels, attestation, and predictions. Before it scores,
it calls `load_verified_holdout_qrels()`, which revalidates the full seal and
returns labels in memory. The runner then scores those rows without reopening
the qrels path. This prevents a generic path-based evaluator from claiming a
holdout score against substituted labels after the seal check.

The existing runner CLI now supports the three opt-in arguments:

```text
--holdout-manifest-path
--holdout-questions-path
--holdout-attestation-path
```

They are all-or-nothing. When present, CLI execution must use the privileged
path. The report is redacted and records
`evaluation_kind=verified_external_holdout`, the holdout manifest SHA-256,
`holdout_status=SEALED_NOT_SCORED`, and
`holdout_semantic_unseen=HUMAN_ATTESTED_NOT_MACHINE_PROVABLE`.

## Fresh Verification

```powershell
$env:PYTEST_DISABLE_PLUGIN_AUTOLOAD='1'
C:\Python312\python.exe -m pytest tests\test_eval_runner.py tests\eval\test_holdout_scoring.py tests\eval\test_holdout_intake.py -q
```

Result: `23 passed in 1.01s`.

The tests prove successful scoring through the sealed path, report binding,
CLI selection, and fail-closed rejection of a qrels file changed after sealing.

## Not Yet Verified

No external holdout metric was emitted. The current Docker composition has no
running retrieval services, and no real predictions have been produced for the
200 external questions. Producing a score with empty or hand-written
predictions would be invalid. The next gate is a real, pinned retrieval run,
followed by the full contamination scan and an artifact-bound report.

## Rollback

Rollback is limited to the runner, its new tests, and this progress record. It
does not alter external holdout inputs, qrels, PDF/OCR artifacts, indexes,
Docker volumes, or secrets.
