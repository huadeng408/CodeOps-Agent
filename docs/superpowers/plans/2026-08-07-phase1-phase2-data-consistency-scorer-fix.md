# Phase 1 + Phase 2: Data Consistency & Scorer Correctness — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:subagent-driven-development` to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix 40-missing-document MySQL/ES gap (Phase 1) and repair nDCG>1 scoring bug (Phase 2) — two P0 blockers from DESIGN-MAP.

**Architecture:** Phase 1 and Phase 2 are independent (no shared state) and can execute in parallel. Phase 1 adds `--force` flag to importer + credentials hardening + targeted 40-doc repair → audit exit 0. Phase 2 fixes `_score_query()` to deduplicate ranked hits by stable key and exclude relevance≤0 qrels from the relevant set → all nDCG ∈ [0,1].

**Tech Stack:** Python 3.12, pytest, pymysql, requests

## Global Constraints

- Protect existing WIP: `orchestrator/eval/runner.py`, `scripts/corpus/import_docs.py`, `_tmp_audit_analysis.json`, `data/reports/`
- Never `git reset --hard`, `git checkout --`, or overwrite dirty files
- No deletion of Docker containers/volumes, ES indices, MySQL rows, MinIO objects, or model caches
- API keys from env vars only; never in code, config, logs, traces, or git
- All nDCG values MUST be finite and in [0,1]; any violation → fail closed
- Document-level evaluation key: `(document_id, tuple())` — section_path globally ignored
- Relevance ≤ 0 qrels are NOT relevant; do not contribute to IDCG or recall denominator
- Old BM25/BGE-M3/Hybrid reports must be marked INVALIDATED, not deleted
- TDD: write failing test → see it fail → minimal fix → see it pass → regression suite
- `synthetic`/`mock` cannot impersonate official; all artifact claims need runtime evidence

---

## Phase 1: Data Consistency (Task Card A)

**Goal:** Importer credential hardening → `--force` flag → targeted 40-doc repair → consistency audit exit 0.

**Current state:** MySQL 3012 ACTIVE, ES 2972 unique document_ids. 40 missing documents (git 1, go 9, postgresql 28, python 2). `consistency_audit.py` exits 1. Importer WIP already has `--mysql-fast-poll` + `_mysql_poll_status()` + `_parse_mysql_dsn()`.

### Task 1.1: Remove hardcoded MySQL DSN from importer

**Files:**
- Modify: `scripts/corpus/import_docs.py:47` (DEFAULT_MYSQL_DSN constant)
- Modify: `scripts/corpus/import_docs.py:595-598` (main() DSN construction)
- Modify: `scripts/corpus/import_docs.py:570-600` (parse_args section)
- Test: `tests/corpus/test_import_docs.py` (add 1 test)

**Interfaces:**
- Consumes: existing `--mysql-fast-poll` flag, `_parse_mysql_dsn()` function
- Produces: `--mysql-dsn` CLI argument replaces hardcoded DEFAULT_MYSQL_DSN; MYSQL_DSN env var fallback

- [ ] **Step 1: Write failing test for credential safety**

```python
def test_mysql_dsn_not_hardcoded():
    """No real credentials in committed source code."""
    import inspect
    source = inspect.getsource(import_docs)
    assert "codeagent:codeagent" not in source, (
        "DEFAULT_MYSQL_DSN hardcodes real credentials — must use --mysql-dsn CLI arg"
    )
```

- [ ] **Step 2: Run test, verify it FAILS**

```powershell
C:\Python312\python.exe -m pytest -q tests/corpus/test_import_docs.py::test_mysql_dsn_not_hardcoded -v
```
Expected: FAIL — `codeagent:codeagent` found in source

- [ ] **Step 3: Replace hardcoded DSN with env/CLI pattern**

Change `DEFAULT_MYSQL_DSN` to a sentinel value (line 47):
```python
DEFAULT_MYSQL_DSN = ""  # must be set via --mysql-dsn or MYSQL_DSN env var
```

Add `--mysql-dsn` to `parse_args()` (after `--http-timeout` argument at ~line 570):
```python
parser.add_argument(
    "--mysql-dsn",
    default="",
    help="Go-style MySQL DSN user:pass@tcp(host:port)/db for --mysql-fast-poll "
    "(falls back to MYSQL_DSN env var)",
)
```

In `main()`, replace DSN construction (line ~595-598):
```python
import os
dsn_str = args.mysql_dsn or os.environ.get("MYSQL_DSN", "")
mysql_dsn: dict | None = None
if args.mysql_fast_poll:
    if not dsn_str:
        print("--mysql-fast-poll requires --mysql-dsn or MYSQL_DSN env var", file=sys.stderr)
        return 2
    if _HAS_PYMYSQL:
        mysql_dsn = _parse_mysql_dsn(dsn_str)
```

- [ ] **Step 4: Run credential safety test — PASSES**

```powershell
C:\Python312\python.exe -m pytest -q tests/corpus/test_import_docs.py::test_mysql_dsn_not_hardcoded -v
```
Expected: PASS

- [ ] **Step 5: Run full importer test suite (regression)**

```powershell
C:\Python312\python.exe -m pytest -q tests/corpus/test_import_docs.py -v
```
Expected: all existing tests pass (test suite uses FakeServer, never reaches MySQL)

- [ ] **Step 6: Commit**

```bash
git add scripts/corpus/import_docs.py tests/corpus/test_import_docs.py
git commit -m "fix(importer): remove hardcoded MySQL DSN, add --mysql-dsn CLI arg

Credentials now come from --mysql-dsn or MYSQL_DSN env var. The old
DEFAULT_MYSQL_DSN carried a plaintext password in source.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

### Task 1.2: Add `--force` flag for re-import of already-ACTIVE documents

**Files:**
- Modify: `scripts/corpus/import_docs.py:446-506` (process_one signature and body)
- Modify: `scripts/corpus/import_docs.py:570-600` (parse_args)
- Modify: `scripts/corpus/import_docs.py:660-670` (main, pass force to process_one)
- Test: `tests/corpus/test_import_docs.py` (add 1 test)

**Interfaces:**
- Consumes: `process_one()`, `is_document_active()`, `parse_args()`, `main()`
- Produces: `--force` CLI flag; `process_one(force=True)` skips `is_document_active()` early-return

- [ ] **Step 1: Write failing test**

```python
class FakeServerForce(FakeServer):
    """Server where a doc is already ACTIVE; --force re-uploads it."""

    def __init__(self):
        super().__init__()
        doc_id = import_docs.document_id("go", GO_COMMIT, "doc/README.md")
        self.active.append({"documentId": doc_id, "status": "ACTIVE"})
        self.force_upload_triggered = False

    def post(self, url, files=None, headers=None, timeout=None):
        self.force_upload_triggered = True
        return FakeResp(202, {"documentId": "test-doc-id"})


def test_force_reimports_already_active_document(tmp_path, monkeypatch):
    """--force re-uploads a document even when it's already ACTIVE."""
    source_dir = tmp_path / "staging" / "go"
    source_dir.mkdir(parents=True)
    (source_dir / "README.md").write_text("# Test Doc", encoding="utf-8")

    manifest = tmp_path / "manifest.yaml"
    write_manifest(manifest)
    (tmp_path / "token").write_text("test-token", encoding="utf-8")

    fake = FakeServerForce()
    monkeypatch.setattr(import_docs.requests, "get", fake.get)
    monkeypatch.setattr(import_docs.requests, "post", fake.post)
    monkeypatch.setattr(import_docs.time, "sleep", lambda *_a, **_k: None)

    rc = import_docs.main([
        "--source", "go",
        "--staging", str(tmp_path / "staging"),
        "--manifest", str(manifest),
        "--token-file", str(tmp_path / "token"),
        "--force",
        "--poll-interval-seconds", "0",
    ])
    assert rc == 0
    assert fake.force_upload_triggered, "expected re-upload via POST with --force"
```

- [ ] **Step 2: Run test — FAILS**

```powershell
C:\Python312\python.exe -m pytest -q tests/corpus/test_import_docs.py::test_force_reimports_already_active_document -v
```
Expected: FAIL — `unrecognized arguments: --force`

- [ ] **Step 3: Add `--force` flag and thread through**

In `parse_args()`:
```python
parser.add_argument(
    "--force",
    action="store_true",
    default=False,
    help="re-import documents even when already ACTIVE in MySQL",
)
```

In `process_one()`, add `force: bool = False` parameter. Change the early-return guard:
```python
def process_one(
    server: str,
    token: str,
    generation: str,
    source_id: str,
    source_commit: str,
    source_dir: Path,
    path: Path,
    target_index: str,
    poll_timeout: int,
    poll_interval: int,
    http_timeout: int,
    _mysql_dsn: dict | None = None,
    force: bool = False,
) -> dict:
    ...
    if not force and is_document_active(
        server, token, generation, base_record["documentId"],
        http_timeout=http_timeout, _mysql_dsn=_mysql_dsn,
    ):
        base_record["status"] = "skipped"
        return base_record
```

In `main()`, pass `force=args.force` to `process_one()` call.

- [ ] **Step 4: Run test — PASSES**

```powershell
C:\Python312\python.exe -m pytest -q tests/corpus/test_import_docs.py::test_force_reimports_already_active_document -v
```
Expected: PASS

- [ ] **Step 5: Run full importer test suite**

```powershell
C:\Python312\python.exe -m pytest -q tests/corpus/test_import_docs.py -v
```
Expected: all pass

- [ ] **Step 6: Commit**

```bash
git add scripts/corpus/import_docs.py tests/corpus/test_import_docs.py
git commit -m "feat(importer): add --force flag to re-import already-ACTIVE documents

Needed for Phase 1 repair of 40 missing documents. The importer normally
skips ACTIVE docs; --force bypasses the early-return so documents can be
re-uploaded after ES index repairs.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

### Task 1.3: Targeted 40-document repair & consistency audit (runtime)

**Files:**
- Evidence only: `data/reports/phase1-*.json`

**Precondition:** Go server `:8081` healthy, embedding `:8009` ready, MySQL accessible via MYSQL_DSN env var. This task is **runtime** — it uses the completed `--force --mysql-fast-poll --mysql-dsn` pipeline against the live server.

- [ ] **Step 1: Pre-repair audit snapshot**

```powershell
$env:MYSQL_DSN = "<from configs/server.yaml>"
C:\Python312\python.exe scripts/corpus/consistency_audit.py `
  --generation techdocs-2026-07-30-v1 --list-limit 0 > data/reports/phase1-pre-audit.json
```
Expected: exit 1, missingCount=40

- [ ] **Step 2: Extract missing document list and map to source groups**

```powershell
C:\Python312\python.exe -c "
import json
with open('data/reports/phase1-pre-audit.json') as f:
    report = json.load(f)
for doc_id in report['missing']:
    print(doc_id)
"
```

Group by source prefix (git@, go@, postgresql@, python@):

| Source | Count | Source prefix |
|--------|-------|--------------|
| git | 1 | `git@` |
| go | 9 | `go@` |
| postgresql | 28 | `postgresql@` |
| python | 2 | `python@` |

- [ ] **Step 3: Re-import per source with --force**

For each source with missing docs:

```powershell
# Example for the go source — repeat for git, postgresql, python
C:\Python312\python.exe scripts/corpus/import_docs.py `
  --source go `
  --staging <staging_dir> `
  --manifest <manifest_yaml_path> `
  --token-file <token_file_path> `
  --mysql-fast-poll --mysql-dsn "$env:MYSQL_DSN" `
  --force `
  --http-timeout 300
```

**Note:** Staging directory, manifest path, and token file paths depend on the deployment layout. The design map specifies reading these from existing configs — exact paths to be determined at runtime from `configs/server.yaml` and corpus manifest files.

- [ ] **Step 4: Post-repair consistency audit**

```powershell
C:\Python312\python.exe scripts/corpus/consistency_audit.py `
  --generation techdocs-2026-07-30-v1 --list-limit 0 > data/reports/phase1-post-audit.json
```
Expected: exit 0, CONSISTENT, missingCount=0, orphanCount=0, vectorGapCount=0

- [ ] **Step 5: Full regression**

```powershell
go test ./... -count=1
C:\Python312\python.exe -m pytest -q
```
Expected: all pass

- [ ] **Step 6: Commit evidence**

```bash
git add data/reports/phase1-pre-audit.json data/reports/phase1-post-audit.json
git commit -m "evidence(phase1): data consistency repair — 40 docs re-indexed, audit exit 0

Consistency audit now passes: es_docs N, mysql_active N, orphans 0,
missing 0, vector_gaps 0.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

---

## Phase 2: Scorer Correctness (Task Card B)

**Goal:** TDD fix nDCG>1, deduplicate ranked hits by document-level key, filter relevance≤0 qrels, mark old reports INVALIDATED.

**Current state (confirmed by explorer agent counterfactual computation):**
- 86 of 180 queries produce nDCG > 1 with current code, max 2.43
- nDCG=1.3104 overall for BGE-M3
- Root cause confirmed: ① duplicate-document chunks each contribute full relevance to DCG → DCG_actual > DCG_ideal ② relevance=0 qrels included in relevant set → negative-only queries get Recall=1.0, MRR>0
- Counterfactual with fixes: 163 queries (17 negative dropped), recall@5 0.7362, mrr@10 0.6021, nDCG@10 0.7230 — zero violations

### Task 2.1: Write failing contract tests

**Files:**
- Modify: `tests/test_eval_runner.py` (add 5 tests)

- [ ] **Step 1: Write 5 contract tests**

Add to end of `tests/test_eval_runner.py`:

```python
# ---------------------------------------------------------------------------
# Phase 2 contract tests — nDCG boundary, dedup, relevance=0 exclusion
# ---------------------------------------------------------------------------


def test_ndcg_never_exceeds_1_with_multi_chunk_same_document(tmp_path) -> None:
    """nDCG must be <= 1.0 when the same doc appears as multiple chunks in top-10."""
    qrels = [
        {"query_id": "q1", "document_id": "doc-a", "section_path": ["API"],
         "relevance": 2.0, "language": "en", "query_type": "concept", "source_id": "go"},
    ]
    # doc-a appears 3 times as different chunks — all in top 10
    hits = [
        {"query_id": "q1", "document_id": "doc-a", "section_path": ["API"], "score": 0.9},
        {"query_id": "q1", "document_id": "doc-a", "section_path": ["Overview"], "score": 0.8},
        {"query_id": "q1", "document_id": "doc-a", "section_path": ["Details"], "score": 0.7},
    ]
    summary, _ = _run(tmp_path, qrels, hits)
    ndcg = summary["overall"]["ndcg@10"]
    assert 0.0 <= ndcg <= 1.0, f"nDCG must be in [0,1], got {ndcg}"


def test_perfect_ranking_ndcg_equals_1(tmp_path) -> None:
    """All relevant docs at rank 1 → nDCG=1.0 (not >1)."""
    qrels = [
        {"query_id": "q1", "document_id": "doc-a", "section_path": [], "relevance": 2},
        {"query_id": "q1", "document_id": "doc-b", "section_path": [], "relevance": 1},
    ]
    hits = [
        {"query_id": "q1", "document_id": "doc-a", "section_path": [], "score": 0.9},
        {"query_id": "q1", "document_id": "doc-b", "section_path": [], "score": 0.8},
    ]
    summary, _ = _run(tmp_path, qrels, hits)
    assert summary["overall"]["ndcg@10"] == 1.0


def test_imperfect_ranking_ndcg_below_1(tmp_path) -> None:
    """Relevant doc at rank 2 instead of rank 1 → nDCG < 1.0."""
    qrels = [
        {"query_id": "q1", "document_id": "doc-a", "section_path": [], "relevance": 2},
    ]
    hits = [
        {"query_id": "q1", "document_id": "doc-x", "section_path": [], "score": 0.9},
        {"query_id": "q1", "document_id": "doc-a", "section_path": [], "score": 0.8},
    ]
    summary, _ = _run(tmp_path, qrels, hits)
    ndcg = summary["overall"]["ndcg@10"]
    assert ndcg < 1.0, f"nDCG should be < 1.0 for imperfect ranking, got {ndcg}"


def test_relevance_zero_excluded_from_scoring(tmp_path) -> None:
    """Qrels with relevance=0 are NOT relevant — don't count for Recall/DCG/IDCG."""
    qrels = [
        {"query_id": "q1", "document_id": "doc-a", "section_path": [], "relevance": 0.0,
         "language": "en", "query_type": "concept", "source_id": "go"},
    ]
    hits = [
        {"query_id": "q1", "document_id": "doc-a", "section_path": [], "score": 0.9},
    ]
    summary, _ = _run(tmp_path, qrels, hits)
    assert summary["overall"]["recall@5"] == 0.0, "relevance=0 should give 0 recall"
    assert summary["overall"]["ndcg@10"] == 0.0, "no relevant docs => nDCG=0"


def test_hybrid_dedup_by_document_id(tmp_path) -> None:
    """Duplicate document in hits counted once at first occurrence."""
    qrels = [
        {"query_id": "q1", "document_id": "doc-a", "section_path": [], "relevance": 2},
        {"query_id": "q1", "document_id": "doc-b", "section_path": [], "relevance": 1},
    ]
    # doc-a at rank 1 AND rank 2 (duplicate), doc-b at rank 3
    hits = [
        {"query_id": "q1", "document_id": "doc-a", "section_path": ["A"], "score": 0.9},
        {"query_id": "q1", "document_id": "doc-a", "section_path": ["B"], "score": 0.8},
        {"query_id": "q1", "document_id": "doc-b", "section_path": [], "score": 0.7},
    ]
    summary, _ = _run(tmp_path, qrels, hits)
    ndcg = summary["overall"]["ndcg@10"]
    assert 0.0 <= ndcg <= 1.0, f"nDCG must be in [0,1] with dedup, got {ndcg}"
    # doc-a (rel=2) at rank 1 → 2.0, duplicate skipped, doc-b (rel=1) at rank 3 → 1/log2(3)=0.631
    # IDCG = [2, 1] → 2.0 + 1/1.0 = 3.0.  DCG for [2,1] at ranks [1,3] = 2.0 + 1/log2(3) = 2.631
    # nDCG = 2.631/3.0 ≈ 0.877 < 1.0
    assert ndcg < 1.0, f"duplicate at rank 2 should reduce nDCG below 1.0, got {ndcg}"
```

- [ ] **Step 2: Run new tests — at least 3 MUST FAIL**

```powershell
C:\Python312\python.exe -m pytest -q tests/test_eval_runner.py -k "ndcg or dedup or relevance_zero" -v
```
Expected: `test_ndcg_never_exceeds_1_with_multi_chunk_same_document` FAIL (nDCG > 1), `test_hybrid_dedup_by_document_id` FAIL (nDCG > 1), `test_relevance_zero_excluded_from_scoring` FAIL (recall=1.0). `test_imperfect_ranking_ndcg_below_1` and `test_perfect_ranking_ndcg_equals_1` may pass by coincidence.

- [ ] **Step 3: Commit failing tests (contract capture)**

```bash
git add tests/test_eval_runner.py
git commit -m "test(scorer): add nDCG boundary, dedup, relevance-zero contract tests

5 tests define the Phase 2 acceptance contract:
- nDCG never exceeds 1.0 with duplicate-document chunks
- Perfect ranking → nDCG = 1.0 exactly
- Imperfect ranking → nDCG < 1.0
- Relevance=0 qrels excluded from relevant set
- Duplicate document hits deduplicated at first occurrence

Tests currently FAIL against the unfixed scorer.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

### Task 2.2: Fix _score_query — dedup + relevance filtering

**Files:**
- Modify: `orchestrator/eval/runner.py:137-166` (_score_query function)

**Interfaces:**
- Consumes: `_key()` (document-level, `(document_id, tuple())`), `_dcg()`
- Produces: corrected `_score_query()` with dedup + relevance>0 filter; all nDCG ∈ [0,1]

- [ ] **Step 1: Apply minimal fix**

Replace `_score_query()` body (lines 137-166 in current code, after the WIP `_key` change):

```python
def _score_query(
    qrel_records: list[dict[str, Any]], hits: list[dict[str, Any]]
) -> dict[str, Any]:
    """Per-query Recall@5, MRR@10, nDCG@10, empty and wrong-hit counts.

    Relevance ≤ 0 qrels are excluded from the relevant set (they mark
    documents that are NOT relevant). Ranked hits are deduplicated by
    (document_id) key — only the first occurrence contributes to DCG,
    preventing multi-chunk inflation.
    """
    # Build relevant set: only relevance > 0
    relevant: dict[tuple[str, tuple[str, ...]], float] = {}
    for q in qrel_records:
        if q["relevance"] <= 0:
            continue
        key = _key(q["document_id"], q["section_path"])
        relevant[key] = max(relevant.get(key, 0.0), q["relevance"])

    # Deduplicate ranked hits by stable key, preserving score order
    seen: set[tuple[str, tuple[str, ...]]] = set()
    deduped_hits: list[dict[str, Any]] = []
    for h in hits:
        key = _key(h["document_id"], h["section_path"])
        if key not in seen:
            seen.add(key)
            deduped_hits.append(h)

    hit_keys = [_key(h["document_id"], h["section_path"]) for h in deduped_hits]
    top5_keys = hit_keys[:5]
    top10_keys = hit_keys[:10]

    recall = len(set(top5_keys) & relevant.keys()) / len(relevant) if relevant else 0.0

    mrr = 0.0
    for i, key in enumerate(top10_keys):
        if key in relevant:
            mrr = 1.0 / (i + 1)
            break

    ideal_dcg = _dcg(sorted(relevant.values(), reverse=True), 10)
    actual = [relevant.get(key, 0.0) for key in top10_keys]
    ndcg = _dcg(actual, 10) / ideal_dcg if ideal_dcg > 0 else 0.0

    return {
        "recall@5": recall,
        "mrr@10": mrr,
        "ndcg@10": ndcg,
        "empty": len(hits) == 0,
        "wrong_hits": sum(1 for key in hit_keys if key not in relevant),
    }
```

- [ ] **Step 2: Run contract tests — ALL 5 PASS**

```powershell
C:\Python312\python.exe -m pytest -q tests/test_eval_runner.py -k "ndcg or dedup or relevance_zero" -v
```
Expected: 5 PASS

- [ ] **Step 3: Run ALL existing scorer tests (regression)**

```powershell
C:\Python312\python.exe -m pytest -q tests/test_eval_runner.py -v
```
Expected: ALL existing tests pass. Existing tests use relevance≥1 and no duplicate documents, so they are unaffected.

- [ ] **Step 4: Commit fix**

```bash
git add orchestrator/eval/runner.py
git commit -m "fix(scorer): deduplicate ranked hits + exclude relevance<=0 from scoring

_score_query changes:
- relevance<=0 qrels excluded from relevant set/IDCG/recall denominator
- ranked hits deduplicated by (document_id) key — first occurrence only
  contributes to DCG (prevents multi-chunk nDCG inflation)
- nDCG now guaranteed in [0,1] for all valid inputs

Confirmed: 86 of 180 queries previously had nDCG>1 (max 2.43).
Counterfactual: 163 queries, nDCG=0.7230, zero violations.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

### Task 2.3: Mark old reports INVALIDATED

**Files:**
- Create: `data/reports/INVALIDATED.md`

- [ ] **Step 1: Create INVALIDATED marker**

Write `data/reports/INVALIDATED.md`:

```markdown
# INVALIDATED Reports

The following retrieval evaluation reports were produced before the scorer
correctness fixes in Phase 2 (commit: `<hash from Task 2.2>`) and are no
longer valid quality evidence.

## Invalid Reports

| Run | File | nDCG@10 | Root Cause |
|---|---|---|---|
| BM25 | `results/retrieval/bm25-report.json` | 1.0727 | nDCG > 1 — relevance≤0 counted as relevant; duplicate chunks double-counted in DCG |
| BGE-M3 | `results/retrieval/bge_m3-report.json` | 1.3104 | nDCG > 1 — 86/180 queries exceeded 1.0, max 2.43 |
| Hybrid RRF | `results/retrieval/hybrid_rrf-report.json` | 0.9034 | Unreliable — same scoring bugs; nDCG happened to be < 1 by accident |

## Fix Applied

- `_score_query()` in `orchestrator/eval/runner.py`: deduplicates ranked hits
  by document-level key, excludes relevance≤0 qrels from relevant set
- Commit: `<hash from Task 2.2>`
- Post-fix: all nDCG values guaranteed finite in [0,1]

## Replacement Reports

Re-run with corrected scorer and commit new reports to `data/reports/eval-2026-08-07/`.
The old files in `results/retrieval/` are preserved as historical artifacts only.
```

- [ ] **Step 2: Commit**

```bash
git add data/reports/INVALIDATED.md
git commit -m "docs(reports): mark pre-Phase-2 retrieval reports INVALIDATED

Old nDCG values >1 caused by duplicate-chunk inflation and relevance=0
qrels counted as relevant. Fixed in parent commit.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

### Task 2.4: Re-run retrieval eval with corrected scorer (runtime)

**Precondition:** Go server healthy on :8081. Existing prediction files at `results/retrieval/{bm25,bge_m3,hybrid_rrf}-predictions.jsonl`.

- [ ] **Step 1: Run corrected scorer on all three prediction sets**

```powershell
C:\Python312\python.exe orchestrator/eval/runner.py `
  --qrels-path data/eval/techdocs/qrels.text.jsonl `
  --predictions-path results/retrieval/bm25-predictions.jsonl `
  --corpus-generation techdocs-2026-07-30-v1 `
  --index-alias knowledge_base_current `
  --output-path data/reports/eval-2026-08-07/bm25-report.json

C:\Python312\python.exe orchestrator/eval/runner.py `
  --qrels-path data/eval/techdocs/qrels.text.jsonl `
  --predictions-path results/retrieval/bge_m3-predictions.jsonl `
  --corpus-generation techdocs-2026-07-30-v1 `
  --index-alias knowledge_base_current `
  --output-path data/reports/eval-2026-08-07/bge-m3-report.json

C:\Python312\python.exe orchestrator/eval/runner.py `
  --qrels-path data/eval/techdocs/qrels.text.jsonl `
  --predictions-path results/retrieval/hybrid_rrf-predictions.jsonl `
  --corpus-generation techdocs-2026-07-30-v1 `
  --index-alias knowledge_base_current `
  --output-path data/reports/eval-2026-08-07/hybrid-report.json
```

- [ ] **Step 2: Verify all nDCG values ∈ [0,1]**

```powershell
C:\Python312\python.exe -c "
import json, sys
reports = {
    'bm25': 'data/reports/eval-2026-08-07/bm25-report.json',
    'bge-m3': 'data/reports/eval-2026-08-07/bge-m3-report.json',
    'hybrid': 'data/reports/eval-2026-08-07/hybrid-report.json',
}
ok = True
for name, path in reports.items():
    with open(path) as f:
        r = json.load(f)
    ndcg = r['overall']['ndcg@10']
    if not (0.0 <= ndcg <= 1.0):
        print(f'FAIL {name}: nDCG@10={ndcg:.4f} out of [0,1]')
        ok = False
    else:
        print(f'OK   {name}: nDCG@10={ndcg:.4f}')
if not ok:
    sys.exit(1)
print('All nDCG values in [0,1]')
"
```
Expected: all OK

- [ ] **Step 3: Commit evidence**

```bash
git add data/reports/eval-2026-08-07/
git commit -m "evidence(phase2): corrected retrieval eval — all nDCG in [0,1]

BM25/BGE-M3/Hybrid RRF re-scored with dedup + relevance>0 filter.
Old INVALIDATED reports superseded.

Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>"
```

### Task 2.5: Full regression suite

- [ ] **Step 1: Go tests**

```powershell
go test ./... -count=1
```
Expected: all pass

- [ ] **Step 2: Full Python test suite**

```powershell
C:\Python312\python.exe -m pytest -q
```
Expected: all pass (367+ new tests)

- [ ] **Step 3: Focused eval tests**

```powershell
C:\Python312\python.exe -m pytest -q tests/test_eval_runner.py tests/eval/test_retrieval_metrics.py -v
```
Expected: all pass

- [ ] **Step 4: Protect dirty WIP**

```powershell
git status --short --branch
git diff --check
```
Expected: `runner.py` and `import_docs.py` still M; `_tmp_audit_analysis.json`, `data/reports/` still untracked.

- [ ] **Step 5: Push to feature branch**

```bash
git push origin feature/complete-design-implementation
```

---

## Execution Order & Parallelism

```
Task 1.1 (DSN fix)  ─┬─> Task 1.2 (--force)  ──> Task 1.3 (40-doc repair)
                     │
Task 2.1 (tests)    ─┴─> Task 2.2 (fix) ──> Task 2.3 (INVALIDATED) ──> Task 2.4 (re-eval)
                                                                          │
                                                         Task 2.5 (regression) <──┘
```

- Phase 1 and Phase 2 are **independent** — can execute Task 1.1-1.2 in parallel with Task 2.1-2.3
- Task 1.3 (runtime repair) needs live services and the completed `--force` importer
- Task 2.4 (runtime re-eval) needs live server + corrected scorer
- Task 2.5 (full regression) is the final gate for both phases

## Post-Completion

After both phases achieve VERIFIED status:

1. Update Obsidian progress file: `D:\Obsidian\code-autogrowth\私人\localcode\PROGRESS-2026-08-07-PHASE1-2.md`
2. Mark Phase 1 and Phase 2 as VERIFIED in DESIGN-MAP
3. Next phases in DESIGN-MAP order: Phase 3 (Sol Reviewer) + Phase 4 (Harness Integration)
