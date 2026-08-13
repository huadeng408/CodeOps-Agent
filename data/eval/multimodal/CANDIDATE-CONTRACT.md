# Multimodal Evidence Candidate Contract

This contract applies before human review. A candidate is not a qrel and must
not be scored, indexed, reported as a gold label, or marked `HUMAN_REVIEWED`.

Validate a candidate with:

```python
from orchestrator.eval.multimodal_candidate import validate_candidate

validate_candidate(candidate)
```

The validator accepts only `candidate_status=AI_CANDIDATE` and requires:

- allowlisted source license plus pinned source revision;
- stable document, page, and element identifiers;
- a positive-area `page_1000_xyxy` bbox and a page-image SHA-256;
- MinerU version, content SHA-256, and `ocr_mode=explicit` provenance.

It rejects qrel-only fields such as `relevance`, `review_status`,
`reviewer_hash`, scores, and metrics. GPT-5.6 Sol may provide a candidate or
an `AI_REVIEWED`/`DISPUTED` opinion, but only a real human review record can
create a new immutable `HUMAN_REVIEWED` qrels version.

This does not create `qrels.multimodal.jsonl`, alter a production index, or
change the blocked PDF document-native bbox gate.

## Human review boundary

`AI_CANDIDATE` records are immutable input to the separate human-review
workflow in `orchestrator.eval.multimodal_human_review`:

1. `export_review_worksheet()` copies validated candidates and adds blank
   `review_decision`, `corrected_bbox`, `review_note`, `reviewed_at`, and
   `reviewer_id` fields. It never promotes a candidate.
2. A controlled external reviewer returns exactly one `ACCEPT`, `CORRECT`, or
   `REJECT` decision per candidate. `CORRECT` must supply a positive
   `page_1000_xyxy` box.
3. `freeze_human_reviewed_evidence()` accepts a decision file only with an
   Ed25519 receipt that binds the exact candidate bytes, exact decision bytes,
   and configured reviewer key id. It emits only accepted/corrected evidence;
   rejected records remain absent. The frozen manifest redacts `reviewer_id`
   to a reviewer hash.

The result has `scoreable=false` and `qrels=false`. It is neither a production
index input nor a retrieval score artifact. Creating a scoreable multimodal
qrels release still requires a new immutable dataset version, split freeze,
contamination scan, independent scorer receipt, and the separate visual-index
gate.
