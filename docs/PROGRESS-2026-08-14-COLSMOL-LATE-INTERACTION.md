# ColSmol Late-Interaction Receipt - 2026-08-14

## Scope

This phase closes only the previously missing late-interaction arm of the
public visual bake-off. It does not approve a visual alias, create an index,
change an alias, create qrels, or claim bbox localization.

## Implemented

- `eval/scripts/run_docvqa_colsmol_late_pilot.py` consumes a frozen public
  DocVQA page receipt plus its paired EasyOCR/BM25 receipt. It rejects a
  mismatched source-manifest digest, absent page artifacts, absent qrels, bad
  candidate page IDs, empty output reuse, and CUDA requests without CUDA.
- The runner loads a real late-interaction encoder in two fixed stages:
  `vidore/ColSmolVLM-Instruct-256M-base` at
  `99ca96f1f6b95b3a69e6abef74a2416cb738fed0`, then the MIT
  `vidore/colSmol-256M` adapter at
  `a59110fdf114638b8018e6c9a018907e12f14855`.
- Per query, the frozen EasyOCR/BM25 ranking supplies exactly the top 20 page
  candidates. ColSmol produces page and query multi-vectors and applies
  MaxSim only to those candidates. This is a late-interaction reranking arm,
  not an unrestricted visual retrieval claim.
- `pyproject.toml` now exposes an explicit `visual-eval` extra for the runtime
  libraries (`colpali-engine`, `datasets`, `peft`, and `pillow`). The default
  install and the existing `rag` extra are unchanged.

## Verified Receipt

The first background run did not inherit `HF_HOME`; with offline mode enabled
it could not see the already cached model and failed before encoding. It is
preserved as an ignored audit attempt and was not reused. `rerun02` uses an
explicit launcher that sets `HF_HOME` to the local cache and `HF_HUB_OFFLINE=1`.

```text
receipt: eval_results/multimodal/docvqa-colsmol-late-public-120-20260814-rerun02
queries: 120
candidate source: paired EasyOCR/BM25 ranking
candidates per query: 20
ranked records: 2400
interaction: ColSmol MaxSim, 128 dimensions
device: RTX 4060 Laptop GPU / cuda:0
peak VRAM: 2789.3 MB
elapsed: 98.11 seconds
alias_created=false
alias_switched=false
```

Receipt checksums were recomputed after the run and matched
`checksums.json`. The shared frozen DocVQA qrels have upstream human labels,
but their 120 selected rows carry no bbox labels, so bbox remains
`NOT_APPLICABLE`.

## Paired Results

All three paths used the same 120 public qrels.

| Path | State | nDCG@10 | Recall@5 | MRR@10 |
| --- | --- | ---: | ---: | ---: |
| EasyOCR + BM25 | `SCORED` | 0.0438 | 0.0417 | 0.0244 |
| CLIP page visual | `SCORED` | 0.2514 | 0.2500 | 0.1765 |
| ColSmol MaxSim over OCR top-20 | `SCORED` | 0.1337 | 0.1417 | 0.1251 |

The late-interaction result is materially below the independent CLIP page
retrieval result. This is not a reason to discard or relabel the receipt: the
OCR/BM25 candidate stage limits recall before ColSmol reranking. It means the
current late arm is evidence of executable capability, not alias eligibility.
The visual alias stays untouched.

## Bbox Investigation

- `docling-project/DocLayNet` has crowd-sourced page-layout boxes, but it is a
  layout-detection corpus rather than question-to-evidence retrieval qrels.
  It must not be merged with DocVQA and reported as a bbox retrieval score.
- `NTT-hil-insight/VisualMRC` is a visual QA corpus, but this investigation did
  not establish a compatible question-to-evidence bbox schema from its public
  metadata. It is not selected as a bbox evaluation source.
- A pinned public source with jointly licensed question/evidence/page/bbox
  labels remains required before running localization metrics.

## Verification

```text
pytest tests/test_project_metadata.py tests/eval/test_docvqa_colsmol_late_pilot.py -q
6 passed

pytest tests/eval/test_docvqa_colsmol_late_pilot.py \
  tests/eval/test_docvqa_clip_pilot.py \
  tests/eval/test_visual_pilot_bakeoff.py \
  tests/test_visual_artifacts.py -q
22 passed

python -m eval.scripts.visual_pilot_bakeoff ...
exit 0; 120-query, three-path report written
```

## Next Automatic Work

1. Select, pin, and inspect a compatible public bbox-labelled retrieval source
   before writing a bbox adapter or score.
2. Improve or replace the text candidate generator before treating ColSmol
   reranking as a production visual-retrieval candidate.
3. Keep the visual alias gated until visual quality, bbox evidence, and the
   design-map release conditions have separate passing receipts.
