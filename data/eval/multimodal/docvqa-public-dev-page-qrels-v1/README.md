# DocVQA Public Development Page Qrels v1

This immutable, tracked copy contains 120 page associations from the public
`vidore/docvqa_test_subsampled` dataset at revision
`49bf8f13e13c41dd8cdb0cae5314e31c1da1e0d6` (MIT). It is a fixed public
development set for page-retrieval experiments.

`qrels.jsonl` intentionally preserves only the upstream query, document, and
page identifiers needed to score page retrieval. It is normalized to UTF-8/LF
for repository hygiene. `source-run-manifest.json`, `source-checksums.json`,
and `source-qrels.jsonl` are the minimal immutable source receipt promoted from
the ignored run tree. Machine-local runtime metadata (platform, device,
timestamps and physical index names) is intentionally excluded;
`manifest.json` binds every retained file by SHA-256.

## Strict limits

- This dataset is public development data, not a hidden holdout.
- Its upstream labels are not project human-review decisions and must not be
  reported as `HUMAN_REVIEWED`.
- It has no source evidence-element or bounding-box labels. It cannot satisfy
  the document-native bbox gate and must not be used for bbox metrics.
- The source pages are images, not PDF inputs. The PDF rule remains unchanged:
  every project PDF must use MinerU with explicit OCR, never Tika.

Verify the tracked copy with:

```powershell
C:\Python312\python.exe -m pytest tests\eval\test_docvqa_public_dev_page_qrels.py -q
```
