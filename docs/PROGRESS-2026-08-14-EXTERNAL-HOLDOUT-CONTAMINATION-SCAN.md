# External Holdout Contamination Scan - 2026-08-14

## Result

The sealed external TechDocs holdout at
`D:\human-eval-inputs\2026-08-14\holdout\questions.jsonl` was scanned against
the persisted `knowledge_base_v2_bge_m3` index before any score was emitted.

```text
C:\Python312\python.exe scripts\corpus\scan_contamination.py \
  --es http://127.0.0.1:9200 \
  --index knowledge_base_v2_bge_m3 \
  --queries D:\human-eval-inputs\2026-08-14\holdout\questions.jsonl \
  --embedding-url http://127.0.0.1:8009/embeddings \
  --out D:\human-eval-inputs\2026-08-14\holdout\contamination-report-20260814.jsonl \
  --manifest D:\human-eval-inputs\2026-08-14\holdout\contamination-manifest-20260814.json \
  --chunk-vector-field vector
```

The scan covered 200 queries and 24,832 usable chunks (24,887 index documents
observed). It completed all four layers in 69.5 seconds:

- exact: 0
- containment: 0
- MinHash: 0
- embedding: 8 unreviewed candidates above threshold
- degenerate fragments counted: 985

Exit status was `BLOCKING`, not clean. The authoritative external evidence is
the report and manifest paths above. No question text, qrels, candidate text,
or candidate decision is copied into the repository.

## Consequence

The verified holdout scoring entrypoint must not be run for a release metric
until an independent reviewer processes all eight external candidates and a
new immutable contamination receipt records the outcomes. Automated semantic
similarity cannot substitute for that review; accepting, rejecting, or
relabeling the candidates here would create false holdout evidence.

## Runtime And Rollback

Only Elasticsearch and the local BGE-M3 embedding container were started for
this read-only scan. Both were stopped after completion. No index, alias,
volume, database, qrels, external input, or secret changed. Rollback of this
commit removes only the progress record; it does not remove the external scan
evidence.
