# Official Technical Corpus Loader Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 以官方 Git 仓库锁定 commit、许可证哈希和路径 allowlist 获取 Go/Python/Git/Docker/Kubernetes/PostgreSQL 文档，并通过正常 ingestion pipeline 导入。

**Architecture:** Python loader 分为 manifest、fetch、license audit、结构化转换和 pilot/import 五个边界。原始 checkout 只存在 gitignored staging；仓库只提交 source lock、转换规则、脱敏报告和 qrels，不提交第三方全文或模型二进制。

**Tech Stack:** Python 3.11+, PyYAML/Pydantic, markdown-it-py, docutils, asciidoctor executable, Git CLI, existing internal knowledge-ingest HTTP API。

---

### Task 1: Define source manifest and lock format

**Files:**
- Create: configs/corpus/sources.yaml
- Create: configs/corpus/sources.lock.json
- Create: orchestrator/corpus/manifest.py
- Create: tests/test_corpus_manifest.py

- [ ] Step 1: Write failing manifest tests

~~~python
def test_manifest_requires_commit_license_and_allowlist():
    manifest = SourceManifest.model_validate({
        "source_id": "python",
        "repository_url": "https://github.com/python/cpython",
        "source_commit": "0123456789abcdef0123456789abcdef01234567",
        "license_spdx": "PSF-2.0",
        "license_path": "LICENSE",
        "include": ["Doc/**/*.rst"],
        "exclude": ["Doc/**/tests/**"],
        "language": "en",
    })
    assert len(manifest.source_commit) == 40
    assert manifest.include
~~~

- [ ] Step 2: Run and verify failure

Run: python -m pytest tests/test_corpus_manifest.py -q

Expected: FAIL because the manifest package does not exist.

- [ ] Step 3: Implement locked source definitions

sources.yaml contains the six official repository URLs, document include/exclude rules, expected license IDs and license paths. The loader consumes only sources.lock.json; the lock command resolves each configured ref to a 40-character commit, records fetched_at, license SHA-256, corpus_generation, file limits, and manifest version. A lock with a missing commit, unexpected license, or empty allowlist is rejected before fetch.

- [ ] Step 4: Verify and commit

Run: python -m pytest tests/test_corpus_manifest.py -q; inspect the JSON schema with python -m orchestrator.corpus.cli validate-manifest configs/corpus/sources.lock.json.

~~~powershell
git add configs/corpus orchestrator/corpus/manifest.py tests/test_corpus_manifest.py
git commit -m "feat(rag): add locked official corpus manifest"
~~~

### Task 2: Fetch safely and audit licenses

**Files:**
- Create: orchestrator/corpus/fetch.py
- Create: orchestrator/corpus/license.py
- Create: tests/test_corpus_fetch.py
- Create: scripts/corpus_fetch.ps1

- [ ] Step 1: Write failing path and license tests

The test rejects a symlink/junction escaping staging, a file outside include rules, a commit mismatch, and a license SHA mismatch. It accepts a fixture repository with exactly the locked commit and license hash.

- [ ] Step 2: Run and verify failure

Run: python -m pytest tests/test_corpus_fetch.py -q

Expected: FAIL because checkout and license auditors do not exist.

- [ ] Step 3: Implement bounded fetch

Use git clone/fetch with the repository URL from the lock, checkout the exact commit, resolve every path before reading, and reject symlink/junction escapes. Enforce per-file, total-file, and total-byte limits from the lock. Verify the license file content SHA-256 and SPDX allowlist before any conversion. Retry only the same official URL; do not rewrite global proxy settings or substitute mirrors.

- [ ] Step 4: Verify and commit

Run: python -m pytest tests/test_corpus_fetch.py -q. The explicit command scripts/corpus_fetch.ps1 -Manifest configs/corpus/sources.lock.json -SourceId python -Pilot must stop before import when the license check fails.

~~~powershell
git add orchestrator/corpus/fetch.py orchestrator/corpus/license.py tests/test_corpus_fetch.py scripts/corpus_fetch.ps1
git commit -m "feat(rag): add bounded source fetch and license audit"
~~~

### Task 3: Convert Markdown, RST, and AsciiDoc to normalized elements

**Files:**
- Create: orchestrator/corpus/convert.py
- Create: orchestrator/corpus/cli.py
- Create: tests/test_corpus_convert.py
- Modify: pyproject.toml
- Modify: orchestrator/rag/requirements.txt

- [ ] Step 1: Write parser retention tests

~~~python
def test_converter_retains_heading_code_table_and_source_url():
    result = convert_document("docs/example.md", "# API\n\n~~~go\nfmt.Println()\n~~~\n\n| A | B |\n|---|---|\n| 1 | 2 |")
    assert result.heading_path == ["API"]
    assert "fmt.Println" in result.code
    assert result.table_html.startswith("<table")
    assert result.source_url.endswith("/docs/example.md")
~~~

- [ ] Step 2: Run and verify failure

Run: python -m pytest tests/test_corpus_convert.py -q

Expected: FAIL because the typed converter is absent.

- [ ] Step 3: Implement structured conversion

Use markdown-it-py tokens for Markdown, docutils doctree for RST, and the asciidoctor executable with a machine-readable backend for AsciiDoc. Normalize to the internal Element Schema, remove navigation/footer/generated markers/empty sections, preserve heading paths, fenced code, command examples, tables, relative paths, source URLs, and internal cross-reference text. Do not use regex concatenation as the parser.

- [ ] Step 4: Verify and commit

Run: python -m pytest tests/test_corpus_convert.py -q. Missing parser binaries or malformed documents must return a named source/path error and never emit a partial ACTIVE document.

~~~powershell
git add orchestrator/corpus/convert.py orchestrator/corpus/cli.py tests/test_corpus_convert.py pyproject.toml orchestrator/rag/requirements.txt
git commit -m "feat(rag): normalize official docs into typed elements"
~~~

### Task 4: Add pilot and normal-pipeline import

**Files:**
- Modify: orchestrator/corpus/cli.py
- Create: orchestrator/corpus/importer.py
- Create: tests/test_corpus_importer.py
- Modify: internal/handler/knowledge_ingest_handler.go
- Modify: internal/service/upload_service.go

- [ ] Step 1: Write importer idempotency tests

A fixture document with the same source commit/path/content hash is imported twice and results in one document row and one active generation record. A changed content hash creates a new document version. A failure after MinIO upload remains FAILED and is retryable from its checkpoint.

- [ ] Step 2: Run and verify failure

Run: python -m pytest tests/test_corpus_importer.py -q

Expected: FAIL because the loader does not yet call the authenticated ingestion endpoint with source metadata.

- [ ] Step 3: Implement pilot-first import

The importer authenticates as the dedicated corpus-loader account using process environment variables, sends source metadata and normalized file bytes to the internal endpoint, records checkpoint/count/failure/time data, and calls the structured parse/chunk/embed/index path. It accepts --pilot-files 20-50 per source and --pilot-pages 500-2000 for complex PDFs; any source failure blocks full import. Public documents set is_public=true and never reuse E2E users 3/4.

- [ ] Step 4: Verify and commit

Run: python -m pytest tests/test_corpus_importer.py -q. The explicit pilot command must output a redacted report with source_id, source_path, commit, status, counts, failures, and elapsed time.

~~~powershell
git add orchestrator/corpus/cli.py orchestrator/corpus/importer.py tests/test_corpus_importer.py internal/handler/knowledge_ingest_handler.go internal/service/upload_service.go
git commit -m "feat(rag): add pilot-first official corpus import"
~~~

## Rollback Boundary

A failed source remains STAGED or FAILED and can resume from its last checkpoint. Do not mark incomplete generations ACTIVE, delete source checkouts, remove old indexes, or write benchmark qrels into the production corpus.
