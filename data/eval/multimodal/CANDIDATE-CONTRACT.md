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
