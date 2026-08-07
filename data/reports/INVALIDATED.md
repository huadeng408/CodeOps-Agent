# INVALIDATED Reports

The following retrieval evaluation reports were produced before the scorer
correctness fixes in Phase 2 and are no longer valid quality evidence.

## Invalid Reports

| Run | File | nDCG@10 | Root Cause |
|---|---|---|---|
| BM25 | `results/retrieval/bm25-report.json` | 1.0727 | nDCG > 1 — relevance≤0 counted as relevant; duplicate chunks double-counted in DCG |
| BGE-M3 | `results/retrieval/bge_m3-report.json` | 1.3104 | nDCG > 1 — 86/180 queries exceeded 1.0, max 2.43 |
| Hybrid RRF | `results/retrieval/hybrid_rrf-report.json` | 0.9034 | Unreliable — same scoring bugs; nDCG happened to be < 1 by accident |

## Fix Applied

- `_score_query()` in `orchestrator/eval/runner.py`: deduplicates ranked hits
  by document-level key `(document_id)`, excludes relevance≤0 qrels from
  relevant set, IDCG, and recall denominator
- Commit: see git log for Phase 2 scorer fix
- Post-fix guarantee: all nDCG values finite in [0,1]; fail closed on violation

## Replacement Reports

Re-run with corrected scorer and commit new reports to `data/reports/eval-2026-08-07/`.
The old files in `results/retrieval/` are preserved as historical artifacts only.
