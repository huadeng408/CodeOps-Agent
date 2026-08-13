# Public Visual Baseline and Retrieval Health - 2026-08-14

This record supplements the authoritative design map and the same-date progress
log. It records a bounded public evaluation result, not a release decision.

## Implemented

- `orchestrator.rag.visual.CLIPVisualEncoder` is a concrete pinned CLIP page
  encoder. It accepts only a full immutable 40-character revision, validates
  images and queries before loading, and produces normalized 512-dimensional
  vectors on CUDA or CPU.
- `eval/scripts/run_docvqa_clip_pilot.py` materializes a deterministic public
  DocVQA subset, preserves upstream identifiers/qrels, and only creates a new
  physical pilot index. It never creates or switches a visual alias.
- `eval/scripts/run_docvqa_ocr_baseline.py` runs local EasyOCR over a frozen
  image receipt, then ranks OCR text with deterministic BM25. DocVQA supplies
  page images, not PDFs, so this path intentionally does not invoke MinerU or
  Tika. Project PDF entry points remain MinerU plus explicit OCR; Tika remains
  limited to non-PDF Office documents.
- The bake-off reports a missing late-interaction arm as `NOT_RUN` and zero
  bbox-labelled qrels as `NOT_APPLICABLE`, never as a zero-score result.
- Retrieval logging now emits aggregate health only: p95, timeout rate, sliding
  window size, total count, and state. `alert` requires 25 requests and either
  p95 above 5000 ms or rerank timeout rate above 10 percent. It exports no raw
  query, document, or identity content.

## Verified Evidence

Visual receipt:

```text
eval_results/multimodal/docvqa-clip-public-120-20260814-rerun01
index: knowledge_page_visual_pilot_clip_3d74acf9_20260814_public120
ES count: 120
alias_created=false; alias_switched=false
```

Dataset: `vidore/docvqa_test_subsampled` at
`49bf8f13e13c41dd8cdb0cae5314e31c1da1e0d6`, MIT, upstream human-labeled.
CLIP: `openai/clip-vit-base-patch32` at
`3d74acf9a28c67741b2f4f2ea7635f0aaf6f0268`, CUDA.

OCR receipt:
`eval_results/multimodal/docvqa-ocr-bm25-public-120-20260814`.
It used EasyOCR 1.7.2 on CUDA for the same 120 pages and BM25 with `k1=1.2`,
`b=0.75`. Its frozen visual-input manifest digest is
`7363c2942586b42e31c92ab471ca5080e0904119c407fbb9cdb860b32efc794b`.

Paired 120-query bake-off:

| Path | State | nDCG@10 | Recall@5 | MRR@10 |
| --- | --- | ---: | ---: | ---: |
| EasyOCR + BM25 text | `SCORED` | 0.0438 | 0.0417 | 0.0244 |
| CLIP page visual | `SCORED` | 0.2514 | 0.2500 | 0.1765 |
| late interaction | `NOT_RUN` | n/a | n/a | n/a |

The report marks bbox as `NOT_APPLICABLE` because this frozen qrel set has no
bbox labels. Fresh verification passed: `go test ./internal/service -count=1`,
the visual evaluation suites (`32 passed`), and unified Harness/scorer/contract
suites (`53 passed`).

## Boundary and Next Work

This proves only a public CLIP baseline on a fixed subset. It does not establish
ColQwen-style late interaction, bbox localization, production quality, or visual
alias eligibility. The visual alias remains untouched. The next automatic work
is a pinned late-interaction arm plus a bbox-labelled benchmark. Human
adjudication of valid `DISPUTED` rows, net-new holdout creation, and real human
review remain explicit non-automatic boundaries.

## Rollback Boundary

Revert only this phase's CLIP/OCR/bake-off scripts, tests, and retrieval-health
fields together. Do not delete ignored receipts, the physical pilot index,
aliases, model cache, Docker services, qrels, or user data.
