# ScreenSpot-Pro Bbox Eligibility Audit

## Decision

`likaixin/ScreenSpot-Pro` is an eligible candidate for a separate GUI
grounding/localization evaluation lane. It is not a DocVQA replacement and
must never be combined with frozen DocVQA qrels, report scores, or the
document-RAG visual-alias gate.

This is a source and metadata audit only. No project data, production code,
benchmark adapter, score, index, or visual alias was created or changed.

## Verified Pin, Access, And License

| Field | Verified value |
| --- | --- |
| Dataset | `https://huggingface.co/datasets/likaixin/ScreenSpot-Pro` |
| Revision | `210e78d3844251110bff86c95835ebd37a6930fa` |
| Upstream API access | `private=false`, `gated=false` |
| Dataset-card license | `mit` |
| Official code | `https://github.com/likaixin2000/ScreenSpot-Pro-GUI-Grounding` |
| Code revision inspected | `dbe00114bc53a32c61c1a267786da85967710da8` |
| Code license | MIT |

At the pinned dataset revision, upstream lists 26 annotation files. Its
`eval.yaml` states 1,585 annotated screenshots across 26 professional tools.
Candidate eligibility relies on the dataset card's own MIT and ungated claims,
not merely on the code repository's MIT license. A future importer must
re-check revision, license, and gating before it materializes any assets.

## Schema Evidence

Read-only probe: `annotations/vscode_macos.json` at the dataset pin.

- 55 records; each raw field count is 55 for `id`, `img_filename`, `bbox`, and
  `img_size`.
- Record shape: `id`, `img_filename`, `bbox` (`[x1,y1,x2,y2]`), `instruction`,
  `application`, `platform`, `img_size`, `ui_type`, and `group`.
- The reference `vscode_macos_0` names
  `vscode_mac/screenshot_2024-12-03_15-15-02.png`, with bbox
  `[473,183,503,219]` and page size `[2560,1664]`.
- One read-only image accessibility check fetched that referenced image.
  Downloaded SHA-256 matched upstream LFS object SHA-256:
  `e2aba1cb9e3b31e3500178d0cebda30615e93fd74d1a3c3b353cc1dd5230cd1d`.
- The downloaded annotation SHA-256 was
  `55f9b91d3986d8fb027c76b0518b2eb8aa94139601014c4125f81bf839a4667a`.

Honest field mapping:

| Project field | Source field | Meaning |
| --- | --- | --- |
| query | `instruction` | Natural-language UI target request |
| page id | `img_filename` | One screenshot only |
| evidence box | `bbox` | Ground-truth rectangle in `img_size` pixels |
| provenance | `id`, `application`, `platform`, `ui_type`, `group` | Identity and stratification |

It provides no document answer, OCR evidence span, section, multi-page relation,
or relevance set over a shared document corpus. `instruction` must not be
relabelled as a QA qrel and its screenshot must not be represented as a
retrieved DocVQA page.

## Separation Rules

1. Use a distinct lane, for example `screenspot_pro_gui_grounding`.
2. Score only its own grounding metrics, such as point-in-box and IoU. Never
   aggregate them into DocVQA, ViDoRe, document retrieval, or RAG answer
   metrics.
3. Never use its labels to fill missing DocVQA bbox labels, claim document
   evidence localization, or pass the visual-alias gate.
4. Keep its cache outside production corpus/index aliases and retain revision,
   source URL, annotation SHA-256, image SHA-256, and score receipt separately.
5. If revision, license, gating, image availability, or hashes change, report
   `BLOCKED`; do not substitute historical artifacts or a zero score.

## Rejected And Blocked Sources

- `NTT-hil-insight/VisualMRC` has compatible public schema fields
  (`question`, `relevant`, `bounding_boxes`) but reports `gated="auto"`. Its
  access endpoint returned HTTP 401 without authentication; its evaluation
  license limits internal evaluation and prohibits transfer/distribution. It
  remains `LICENSE_BLOCKED`; no terms were accepted and no data bytes were
  downloaded.
- `docling-project/DocLayNet` has layout boxes, not question-to-evidence
  retrieval qrels. It cannot be joined to DocVQA to manufacture bbox scores.

## Next Non-Destructive Steps

1. Define and test a separate benchmark contract before adding a manifest.
2. Materialize only a small fixed, stratified public sample into an isolated
   evaluation cache; check image dimensions and hashes before scoring.
3. Emit a separate GUI receipt with its grounding metric plus explicit
   `document_retrieval=NOT_APPLICABLE` and `answer_quality=NOT_APPLICABLE`.
4. Continue the distinct search for a public, jointly licensed document source
   with question, document/page, evidence, and bbox labels. Existing DocVQA
   bbox state remains `NOT_APPLICABLE`.

## Audit Boundary

This establishes source eligibility and schema compatibility for an isolated
GUI localization experiment only. It does not establish model, retrieval, RAG,
human-review, production-index, or release-gate quality.
