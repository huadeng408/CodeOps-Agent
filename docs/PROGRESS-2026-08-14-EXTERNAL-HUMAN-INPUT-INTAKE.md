# External Human Input Intake - 2026-08-14

## Holdout

`D:\human-eval-inputs\2026-08-14\holdout` supplied 200 external questions
and 200 qrels. The repository's
`orchestrator.eval.holdout.verify_sealed_holdout()` revalidated the existing
seal, including current input hashes, the agent-safe question-view hash,
development qid lock, exact development-question guard, and development
document-overlap guard. It returned `SEALED_NOT_SCORED` with
`HUMAN_ATTESTED_NOT_MACHINE_PROVABLE` unchanged. This is a valid sealed input
for a future privileged holdout scorer; it is not a score, a pass, or a
machine proof that the human-authored claim is true.

## Signed MinerU Page Qrels

`D:\human-eval-inputs\2026-08-14\multimodal-page-qrels` supplied 20 signed
human element decisions, all `ACCEPT`, and 10 separately signed query links.
Before materialization, every submitted candidate line was verified as an
exact byte-for-byte member of the original 180-record local MinerU candidate
file at `D:\eval-data\dude-sample-20260814\mineru-ocr-3e823-20260814`.
The fail-closed materializer then verified both Ed25519 receipts, public key
ids, the per-PDF license allowlist, and the hash-bound explicit MinerU OCR
receipt. It wrote the external derived artifact:

```text
D:\human-eval-inputs\2026-08-14\multimodal-page-qrels\materialized-human-reviewed-page-qrels-20260814
```

The artifact contains 10 `HUMAN_REVIEWED` page-Qrels rows over one PDF, but
its manifest is deliberately `HUMAN_REVIEWED_PAGE_QRELS_NOT_RELEASED` with
`scoreable=false`. It cannot enter release metrics, production indexing, or a
claim of benchmark readiness until a versioned split freeze, contamination
scan, and independent scorer receipt are completed.

## Fresh Verification

```powershell
C:\Python312\python.exe -m pytest tests\eval\test_holdout_intake.py tests\eval\test_multimodal_page_qrels.py -q
```

Result: `11 passed in 0.84s`.

No repository dataset, PDF, image, OCR output, external qrels, key, Docker
resource, index, or database was modified. Rollback of this commit removes
only this documentation addition; it does not delete the external human input
or derived artifact.
