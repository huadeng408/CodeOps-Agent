# Manual Review Portal Design

## Purpose

Provide one local, static HTML page at `docs/manual-review-portal.html` that
makes every remaining user-operated evaluation gate visible and actionable.
The page must work by opening the file directly in a browser. It is a local
drafting tool, never a qrels editor, release tool, signing tool, or service UI.

## Chosen Approach

Use one self-contained HTML file with embedded CSS and JavaScript. The portal
uses the browser file picker to load a generated text-review worksheet, stores
unfinished work in `localStorage`, and downloads explicit JSON artifacts. It
does not make network requests, execute shell commands, access Docker, or
write files other than browser downloads.

This avoids a local server and keeps the trust boundary clear: source data,
qrels, signed receipts, and release manifests stay outside the browser.

## Layout

Desktop uses a fixed left task list and a right work area. On narrow screens,
the task list becomes a top navigation strip. The five tasks are shown in this
order:

1. Text arbitration, currently actionable after importing a current evidence
   worksheet.
2. Document-source declaration, actionable now.
3. Multimodal evidence review, visibly blocked until real candidates and page
   images exist.
4. Holdout declaration, visibly blocked from creating a holdout from the
   existing 180 dev questions.
5. Local signing and submission instructions, informational only.

Each task card shows one of `Ready`, `Needs source package`, or `Blocked`.
Blocked cards explain the exact missing prerequisite and cannot produce a
submission artifact.

## Text Arbitration

### Inputs

The immutable current source is
`data/eval/techdocs/reviews/beeapi-openai-relay-recovery-20260813-01/qrels.sol-review-arbitrated.jsonl`.
Its paired query source is `data/eval/techdocs/queries.text.jsonl`. The portal
does not load either path automatically because a browser opened from disk
cannot safely read arbitrary local paths.

Before a person reviews text, they run this command from the repository root
with Elasticsearch available:

```powershell
python orchestrator/eval/dispute_worksheet.py `
  --qrels data/eval/techdocs/reviews/beeapi-openai-relay-recovery-20260813-01/qrels.sol-review-arbitrated.jsonl `
  --queries data/eval/techdocs/queries.text.jsonl `
  --out data/eval/techdocs/reviews/beeapi-openai-relay-recovery-20260813-01/human-review-worksheet.jsonl `
  --markdown data/eval/techdocs/reviews/beeapi-openai-relay-recovery-20260813-01/human-review-worksheet.md `
  --reviewer local-human-1
```

The command must fail if evidence cannot be read; `--no-evidence` is not a
valid path for meaningful human arbitration. The portal makes this requirement
visible, then accepts only a JSONL worksheet selected by the user.

### Per-row Form

The portal shows a query, source/document identity, claimed section warning,
and retrieved document sections. It never displays prior AI verdicts, AI
confidence, dispute reasons, or a qrels relevance label. For each row it
collects:

- required `verdict_relevant`: `yes` or `no`;
- optional `verdict_answerable`;
- optional `verdict_language_correct`;
- optional `verdict_query_type_correct`;
- optional `verdict_evidence_sufficient`;
- optional `reviewer_notes`.

The reviewer can move previous/next, filter incomplete rows, save local draft,
clear only the selected local draft after confirmation, and download a JSONL
decision draft. The download includes the original worksheet identity fields
and user answers but is headed with `artifact_type: UNSUBMITTED_REVIEW_DRAFT`.
It must never claim `HUMAN_REVIEWED`, include a reviewer hash, overwrite qrels,
or alter the imported worksheet.

### Validation and Failure Handling

Import rejects malformed JSONL, non-object rows, empty packages, duplicate
`row_hash`, and any row without a query, document id, or `document_sections`.
Export is disabled until all loaded rows have `yes` or `no` relevance verdicts.
Errors are rendered in the page and leave the existing local draft untouched.

## Document-Source Declaration

The source declaration form is available immediately. It collects local path
or URL, source/revision identifier, license, permission for OCR, permission
for page rendering, permission for vectorization, permission for internal
evaluation, permission for public display, and explanatory notes.

All five permission fields are explicit yes/no selections. Export is disabled
until identity, license, and all permissions are completed. The result is a
download named `UNSUBMITTED_SOURCE_DECLARATION.json` with its status set to
`UNSUBMITTED_SOURCE_DECLARATION`; it is evidence for later intake, not proof of
rights or a source allowlist entry.

## Multimodal Evidence Review

The page must display the factual current state: there are no candidates, page
images, PDFs, or multimodal qrels. The user cannot manufacture a candidate or
review it through this portal.

It lists the future human fields so that intake is unambiguous:

- `review_decision`: `ACCEPT`, `CORRECT`, or `REJECT`;
- `corrected_bbox`: `[x1, y1, x2, y2]`, required only for `CORRECT`, each
  finite value from 0 through 1000 with a positive-area rectangle;
- non-empty `review_note`, `reviewed_at`, and `reviewer_id`.

It also states that PDF evidence must be produced by MinerU plus explicit OCR,
never Tika. Once real candidates and images exist, a separate implementation
may add an import flow constrained by
`orchestrator.eval.multimodal_human_review`; that workflow is not part of this
portal release.

## Holdout

The page reads no files dynamically but declares the current immutable fact:
`data/eval/techdocs/splits/split-manifest.v1.json` has `dev_size=180`,
`holdout_size=0`, and `holdout_status=BLOCKED`. It prohibits relabeling those
180 questions as holdout.

The form collects only candidate source/revision, license, permissions, and
planning notes. Export labels it `UNSUBMITTED_HOLDOUT_INTAKE`; it cannot create
a split, qrels, or metric.

## Signing and Submission

The portal explains that final decision import and Ed25519 receipt creation
happen in the controlled local workflow. It has no private-key input, key
upload, signing API, or browser-side cryptography. The instructions link to
`orchestrator/eval/multimodal_human_review.py` and describe the later receipt
binding at a high level. The user supplies any private key only to the approved
local signer outside this portal.

## Privacy and Storage

Draft state is namespaced by the imported worksheet SHA-256 and stored in the
same browser profile's `localStorage`. The page displays the storage key and a
clear-draft control. Downloaded data stays in the browser download location.
No content is sent over the network.

## Accessibility and UX

Use semantic `nav`, `main`, headings, labels, fieldsets, status text, keyboard
reachable controls, and visible focus styles. Do not put instruction-only text
inside icon buttons. Keep status colors paired with words and avoid custom
icons when plain labels are clearer for this administrative workflow.

## Tests and Acceptance

The implementation is accepted only when:

1. Opening the file directly shows all five tasks and the real blocked states.
2. A valid fixture worksheet can be loaded, reviewed, restored from a reload,
   and exported only after every row has a required verdict.
3. Malformed or evidence-free worksheet input is rejected without destroying a
   prior draft.
4. Source and holdout forms validate required declarations and their downloads
   carry the exact `UNSUBMITTED_*` labels.
5. The page has no network requests and does not expose a private-key field.
6. Playwright verifies desktop and narrow mobile layouts, import/export
   controls, blocked controls, and browser console has no errors.

The implementation adds focused tests or a repeatable Playwright smoke script
that exercises this static artifact with a committed fixture, not live qrels or
private data.
