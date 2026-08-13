# 2026-08-14 Multimodal Human Evidence Workflow

## Status

`IMPLEMENTED`: a fail-closed workflow now carries validated multimodal
evidence candidates through an externally controlled human-review receipt.
`BLOCKED`: no real reviewer, trusted public key, signed receipt, or
document-native question-to-element/bbox gold data exists. This work cannot
claim completed human review or scoreable qrels.

## Boundary

- Candidate inputs remain `AI_CANDIDATE` with source/revision/license, MinerU
  explicit OCR provenance, page-image hash, and `page_1000_xyxy` geometry.
- A worksheet adds blank `ACCEPT`/`CORRECT`/`REJECT` decision fields. Every
  candidate must receive exactly one external decision.
- Freezing verifies an Ed25519 receipt bound to exact candidate bytes, exact
  decision bytes, and a configured signer key id. Missing trust roots,
  duplicate/missing/foreign decisions, invalid geometry, receipt replay, and
  invalid signatures fail closed.
- The frozen manifest includes only accepted/corrected evidence and redacts
  reviewer identity. It declares `scoreable=false` and `qrels=false`.

## Verification

Test-first coverage verifies: worksheet export preserves the candidate
boundary; a valid signed receipt includes only accepted evidence; a changed
decision digest is rejected as `RECEIPT_BINDING_MISMATCH`. The commit gate must
run the fresh focused regression suite before this is declared verified.

## Not Completed

This does not create qrels, scores, visual aliases, or production index input.
GPT-5.6 Sol cannot issue `HUMAN_REVIEWED`. Page-only DocVQA, WebSRC, and
ScreenSpot-Pro cannot replace document-native PDF evidence gold.

## Rollback

Rollback is limited to this workflow module, its tests, and its contract
documentation. No qrels, indexes, aliases, Docker data, or secrets change.
