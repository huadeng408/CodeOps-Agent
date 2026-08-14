# Manual Review Portal Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a single offline HTML portal that lets a human complete and export review drafts without mutating qrels, signing artifacts, or communicating with a service.

**Architecture:** `docs/manual-review-portal.html` is self-contained: semantic HTML, embedded CSS, and a small JavaScript state module. It imports only user-selected JSONL, stores drafts under a worksheet-content hash in `localStorage`, and creates browser downloads. Small JSONL fixtures exercise the import contract; Playwright validates the static file through a temporary local server only for browser testing.

**Tech Stack:** HTML5, CSS, browser JavaScript, Web Crypto SHA-256, browser `localStorage`, Playwright MCP, PowerShell.

---

## File Structure

- Create: `docs/manual-review-portal.html` - the offline portal and its embedded styles and state logic.
- Create: `tests/fixtures/manual-review-worksheet.valid.jsonl` - two blind-review rows with real-shaped evidence fields.
- Create: `tests/fixtures/manual-review-worksheet.no-evidence.jsonl` - one syntactically valid row missing evidence, which import must refuse.
- Create: `tests/fixtures/manual-review-worksheet.invalid.jsonl` - malformed JSONL, which import must refuse.
- Create: `docs/MANUAL-REVIEW-PORTAL.md` - short, user-facing instructions and exact links to the authoritative inputs.
- Modify: `docs/DESIGN-MAP-2026-08-07-HARNESS-MULTIMODAL-RAG-EVAL-OBSERVABILITY.md` - record the portal as a non-authoritative manual-review aid after its verification succeeds.
- Modify: `docs/PROGRESS-2026-08-14.md` - record verified portal delivery and remaining human gates.

### Task 1: Add Blind-Worksheet Fixtures

**Files:**
- Create: `tests/fixtures/manual-review-worksheet.valid.jsonl`
- Create: `tests/fixtures/manual-review-worksheet.no-evidence.jsonl`
- Create: `tests/fixtures/manual-review-worksheet.invalid.jsonl`

- [ ] **Step 1: Write the valid two-row fixture**

Create two JSON objects, one per line. Each must contain only blind-review
fields: `row_hash`, `query_id`, `query`, `source_id`, `document_id`,
`language`, `query_type`, `section_path_claimed`, and a non-empty
`document_sections` list. Use deterministic IDs `fixture-row-001` and
`fixture-row-002`; do not include `relevance`, `pass_a_verdict`,
`pass_b_verdict`, `dispute_reason`, or `review_status`.

```json
{"row_hash":"fixture-row-001","query_id":"fixture-q-001","query":"How do I initialize the client?","source_id":"fixture","document_id":"fixture-doc-001","language":"en","query_type":"concept","section_path_claimed":["Client"],"document_sections":[{"chunk_id":"fixture-chunk-001","section_path":["Getting Started"],"text":"Initialize the client with the documented constructor.","truncated":false}]}
```

- [ ] **Step 2: Write failing import fixtures**

Write the evidence-free file as one JSON object containing identity fields but
no `document_sections`; write the invalid file as the literal text
`this is not JSON` followed by a newline. These are safe test inputs, not
production qrels.

- [ ] **Step 3: Verify the fixture contract locally**

Run:

```powershell
Get-Content tests/fixtures/manual-review-worksheet.valid.jsonl | ForEach-Object { $_ | ConvertFrom-Json | Out-Null }
```

Expected: exit code `0`. Then inspect that all forbidden answer-bearing
property names are absent:

```powershell
Select-String -Path tests/fixtures/manual-review-worksheet.valid.jsonl -Pattern 'relevance|pass_a_verdict|pass_b_verdict|dispute_reason|review_status'
```

Expected: no matches and exit code `1` from `Select-String`.

- [ ] **Step 4: Commit the fixtures**

```powershell
git add tests/fixtures/manual-review-worksheet.valid.jsonl tests/fixtures/manual-review-worksheet.no-evidence.jsonl tests/fixtures/manual-review-worksheet.invalid.jsonl
git commit -m "test: add manual review portal fixtures"
```

### Task 2: Create the Static Portal Shell and Truthful Statuses

**Files:**
- Create: `docs/manual-review-portal.html`
- Test: `tests/fixtures/manual-review-worksheet.valid.jsonl`

- [ ] **Step 1: Define the acceptance checks before markup**

The initial browser snapshot must contain exactly five task labels: `Text
arbitration`, `Document source declaration`, `Multimodal evidence review`,
`Holdout declaration`, and `Local signing and submission`. It must also expose
the facts `143` text disputes, `0` holdout rows, and `Blocked` for multimodal
review.

Use this Playwright MCP assertion after the shell exists:

```javascript
async (page) => {
  for (const label of [
    'Text arbitration', 'Document source declaration',
    'Multimodal evidence review', 'Holdout declaration',
    'Local signing and submission'
  ]) await page.getByText(label, { exact: true }).waitFor();
  await page.getByText('0 holdout rows', { exact: true }).waitFor();
  await page.getByText('Blocked', { exact: true }).first().waitFor();
  return 'shell visible';
}
```

- [ ] **Step 2: Run the check before the page exists**

Start a temporary static server from the repository root:

```powershell
python -m http.server 48123 --directory .
```

Navigate Playwright to `http://127.0.0.1:48123/docs/manual-review-portal.html`
and run the assertion from Step 1. Expected: page navigation returns `404` or
the task-label assertion fails because the file does not exist.

- [ ] **Step 3: Implement the semantic shell**

Create a full HTML document with `nav[aria-label="Manual review tasks"]`, one
button per task, a `main` region, a live `#status-message` element, and five
`section` elements. Use `data-panel` IDs to select panels with JavaScript. The
shell must include these factual status strings:

```html
<span class="status status-ready">Ready</span>
<span class="status status-needs-package">Needs source package</span>
<span class="status status-blocked">Blocked</span>
<p>There are no multimodal candidates, page images, PDFs, or multimodal qrels.</p>
<p>Current split: 180 dev questions, 0 holdout rows. Do not relabel the 180 dev questions as holdout.</p>
```

Use responsive CSS with a 15rem sidebar on desktop and a horizontal scrollable
navigation row at `max-width: 760px`. Use visible focus outlines and colors
that pair status words with their color. Do not add network requests, external
fonts, analytics, a private-key field, or a `form action` URL.

- [ ] **Step 4: Run the shell acceptance check**

Repeat the exact Playwright assertion from Step 1. Expected: `shell visible`.
Take desktop and `390x844` screenshots and verify text is neither clipped nor
overlapping.

- [ ] **Step 5: Commit the static shell**

```powershell
git add docs/manual-review-portal.html
git commit -m "feat: add manual review portal shell"
```

### Task 3: Implement Text Worksheet Import, Drafts, and Export

**Files:**
- Modify: `docs/manual-review-portal.html`
- Test: `tests/fixtures/manual-review-worksheet.valid.jsonl`
- Test: `tests/fixtures/manual-review-worksheet.no-evidence.jsonl`
- Test: `tests/fixtures/manual-review-worksheet.invalid.jsonl`

- [ ] **Step 1: Define the failing interaction test**

Use Playwright to upload the valid fixture. The initial state must show
`No worksheet loaded`, then show `1 of 2` after import, and keep the export
button disabled until both rows receive a `yes` or `no` selection.

```javascript
async (page) => {
  await page.getByText('No worksheet loaded', { exact: true }).waitFor();
  await page.setInputFiles('#worksheet-file', 'tests/fixtures/manual-review-worksheet.valid.jsonl');
  await page.getByText('1 of 2', { exact: true }).waitFor();
  if (!(await page.locator('#export-text-draft').isDisabled())) throw new Error('export enabled too early');
  return 'gating works';
}
```

- [ ] **Step 2: Run the interaction test before implementing import logic**

Expected: the file input or progress label is absent and the test fails.

- [ ] **Step 3: Implement import validation and state**

Add a file input `#worksheet-file` accepting `.jsonl` and an import handler
that reads UTF-8 text, splits non-blank lines, parses each object, and validates
these invariants before changing current state:

```javascript
function validateWorksheetRow(row, seen) {
  if (!row || typeof row !== 'object') throw new Error('Each JSONL line must be an object.');
  if (!row.row_hash || seen.has(row.row_hash)) throw new Error('Each row needs a unique row_hash.');
  if (!row.query || !row.document_id) throw new Error('Each row needs query and document_id.');
  if (!Array.isArray(row.document_sections) || row.document_sections.length === 0) {
    throw new Error('Evidence is required; regenerate the worksheet with Elasticsearch evidence.');
  }
  seen.add(row.row_hash);
}
```

Hash the exact imported text with `crypto.subtle.digest('SHA-256', ...)` and
use `manual-review-portal:<hex>` as the `localStorage` key. Persist only the
row hash, current row index, and verdict fields. Existing drafts must remain
unchanged when validation fails.

- [ ] **Step 4: Implement reviewer controls and export**

For the active row, render query, document identity, claimed-section warning,
and all section snippets using `textContent`. Render required radio buttons
named `verdict_relevant` with values `yes` and `no`; render optional selects
for the four named optional verdicts and a notes textarea. Previous/next
buttons save current fields before navigation. The incomplete filter shows only
rows whose required verdict is blank.

Enable `#export-text-draft` only when every imported row has `yes` or `no`.
Export a JSON object, not a false qrels JSONL release:

```javascript
const artifact = {
  artifact_type: 'UNSUBMITTED_REVIEW_DRAFT',
  worksheet_sha256: state.worksheetSha256,
  exported_at: new Date().toISOString(),
  decisions: state.rows.map(({ row_hash, verdict_relevant, verdict_answerable,
    verdict_language_correct, verdict_query_type_correct,
    verdict_evidence_sufficient, reviewer_notes }) => ({ row_hash,
    verdict_relevant, verdict_answerable, verdict_language_correct,
    verdict_query_type_correct, verdict_evidence_sufficient, reviewer_notes }))
};
```

Download it through `Blob` and `URL.createObjectURL`; never modify the imported
File, raw qrels, or any repository file.

- [ ] **Step 5: Verify positive and negative flows**

Run the Step 1 interaction test, answer two rows, trigger the download, and
inspect its captured content for `UNSUBMITTED_REVIEW_DRAFT`. Reload the page
and confirm the answered first row returns from `localStorage`. Upload each
invalid fixture and confirm the visible error mentions malformed JSONL or
required evidence while the valid draft remains present.

- [ ] **Step 6: Commit the text-review workflow**

```powershell
git add docs/manual-review-portal.html
git commit -m "feat: add offline text review drafts"
```

### Task 4: Implement Declaration Forms and Non-Interactive Gates

**Files:**
- Modify: `docs/manual-review-portal.html`

- [ ] **Step 1: Define failing form checks**

Before implementation, assert both declaration download buttons are disabled.
After filling source/revision/license and all five permissions, source export
must be enabled. The holdout export must contain the exact text
`UNSUBMITTED_HOLDOUT_INTAKE` and never expose a button labeled `Create holdout`.

```javascript
async (page) => {
  if (!(await page.locator('#export-source-declaration').isDisabled())) throw new Error('source export enabled too early');
  if (await page.getByText('Create holdout', { exact: true }).count()) throw new Error('portal creates holdout');
  return 'declaration gating present';
}
```

- [ ] **Step 2: Run the check before form implementation**

Expected: the controls do not exist and the check fails.

- [ ] **Step 3: Implement the forms and JSON downloads**

Create source and holdout forms with labels for `source_location`,
`source_revision`, `license`, `ocr_permission`, `page_render_permission`,
`vectorization_permission`, `internal_evaluation_permission`,
`public_display_permission`, and `notes`. Use `required` on identity and
license, and a required `yes` or `no` radio group for every permission. A
complete source form exports:

```javascript
{
  artifact_type: 'UNSUBMITTED_SOURCE_DECLARATION',
  submitted_at: new Date().toISOString(),
  declaration: collectDeclaration('source')
}
```

The holdout form uses the same fields but exports
`artifact_type: 'UNSUBMITTED_HOLDOUT_INTAKE'`. Keep the multimodal panel as an
information-only `Blocked` panel listing `ACCEPT`, `CORRECT`, `REJECT`, the
four-coordinate `0..1000` correction rule, and MinerU plus explicit OCR
requirements. Keep signing as instructions only with no `input[type=password]`
or key upload control.

- [ ] **Step 4: Run declaration and gate checks**

Fill every required source field in Playwright, capture its downloaded JSON,
and assert the artifact type. Repeat for holdout intake. Inspect the DOM for
no password field, no `fetch(` token in the page source, and the exact
multimodal blocked explanation.

- [ ] **Step 5: Commit the forms and gates**

```powershell
git add docs/manual-review-portal.html
git commit -m "feat: add review portal declarations"
```

### Task 5: Publish Instructions and Verify the Completed Artifact

**Files:**
- Create: `docs/MANUAL-REVIEW-PORTAL.md`
- Modify: `docs/DESIGN-MAP-2026-08-07-HARNESS-MULTIMODAL-RAG-EVAL-OBSERVABILITY.md`
- Modify: `docs/PROGRESS-2026-08-14.md`

- [ ] **Step 1: Write concise user instructions**

Document the direct-open path, the current qrels source, exact worksheet
generation command, imported-file prerequisite, all five task meanings, draft
storage behavior, and the fact that a draft cannot be imported/frozen as qrels
without the controlled workflow. Link to `manual-review-portal.html` using a
relative Markdown link.

- [ ] **Step 2: Record only verified project progress**

Add one dated entry to the design map and progress log: the portal exists,
generates only unsigned local drafts, and does not resolve the 143 human text
reviews, missing multimodal candidates, or 0-row holdout. Do not increase
evaluation-completion percentages on the strength of the UI alone.

- [ ] **Step 3: Execute final browser checks**

Use Playwright to test at `1440x960` and `390x844`: task navigation, valid
worksheet import, two required verdicts, text-draft download, source and
holdout declaration downloads, blocked multimodal panel, and reload restore.
Capture screenshots for both viewports and browser console errors. Expected:
zero console errors, no unexpected network requests, no clipped task labels.

- [ ] **Step 4: Run static guard checks**

```powershell
Select-String -Path docs/manual-review-portal.html -Pattern 'fetch\(|XMLHttpRequest|type="password"|HUMAN_REVIEWED|Tika'
```

Expected: only the explanatory `HUMAN_REVIEWED` and `Tika` text, and no
`fetch(`, `XMLHttpRequest`, or password input. Then run:

```powershell
git diff --check
git status --short
```

Expected: only the intended portal, fixtures, and documentation changes are
staged for the release commit.

- [ ] **Step 5: Commit and push the completed portal**

```powershell
git add docs/manual-review-portal.html docs/MANUAL-REVIEW-PORTAL.md tests/fixtures/manual-review-worksheet.valid.jsonl tests/fixtures/manual-review-worksheet.no-evidence.jsonl tests/fixtures/manual-review-worksheet.invalid.jsonl docs/DESIGN-MAP-2026-08-07-HARNESS-MULTIMODAL-RAG-EVAL-OBSERVABILITY.md docs/PROGRESS-2026-08-14.md
git commit -m "feat: add local manual review portal"
git push origin main
```

## Plan Self-Review

Spec coverage: Task 2 covers layout, accessibility, blocked statuses, and
offline boundaries; Task 3 covers safe text review import, evidence validation,
drafts, and export; Task 4 covers declarations, holdout, multimodal, and
signing boundaries; Task 5 covers user instructions, progress honesty, browser
verification, and release. Task 1 provides non-production test data so no live
qrels need to be modified during verification.

Consistency: all text decisions use `verdict_relevant`, all source permissions
use the names listed in Task 4, and every user-produced result is named with an
`UNSUBMITTED_*` artifact type. The only data mutation is browser-local draft
storage and user-selected downloads.
