# SDD ledger — plan: docs/superpowers/plans/2026-08-02-real-corpus-recovery.md

## Task 1: complete (commits dce4b7f..67e9e80, review clean)

- Implementer: DONE, RED→GREEN verified, full `go test ./...` green, `go vet` clean.
- Files: internal/model/task_provenance.go(+test), internal/model/pipeline_task.go(+test, +RunID gorm varchar(96)), pkg/tasks/tasks.go(+test, FileProcessingTask.RunID/DocumentID/Provenance json omitempty).
- Decision: pkg/tasks imports internal/model directly (leaf pkg, no cycle). CorpusProvenance.Validate enforces lowercase-hex commit/sha (matches existing fixtures). RunID nullable for legacy compatibility; migration deferred to Task 4.
- Review: spec PASS, quality APPROVED, 0 fix rounds. 1 MINOR (gofmt regression) — fixed by controller in commit 67e9e80 (`gofmt -w`, no logic change, model+tasks tests re-green).
- Final head: 67e9e801.

## Task 2: complete (commits 4ea7dafd..f5aeed47, review clean)

- Implementer: DONE_WITH_CONCERNS, RED→GREEN verified, full `go test ./...` green, `go vet` clean, 0 fix rounds.
- Files: internal/service/corpus_ingest_service.go(+test), internal/service/upload_service.go (MergeChunks produce error now surfaced, redis cleanup moved before produce to avoid leak).
- Interface produced for Task 3: `CorpusIngestService.Ingest(ctx, CorpusIngestRequest) (*CorpusIngestResult, error)`; `CorpusIngestRequest{UserID, OrgTag, IsPublic, FileMD5, FileName, TotalSize, ObjectURL, ContentSHA256, RunID, Provenance model.CorpusProvenance, SourceVersionID, DocumentID}`; `ErrCorpusValidation` sentinel → handler maps to 400 via errors.Is.
- Implementer decisions (accepted): ObjectURL/RunID added to request (load-bearing); UploadRepository not injected (dead dep); DocumentLanguage defaults "auto"; MergeChunks fail-closed per R4 (initSeedFiles logs+continues, upload handler 500, knowledge_ingest_handler 5xx).

## Task 3: complete (commits f5aeed47..33329650, review clean)

- Implementer: DONE_WITH_CONCERNS, RED→GREEN, go test ./... green, go vet clean, 0 fix rounds.
- Files: internal/handler/knowledge_ingest_handler.go(+test), cmd/server/main.go (装配 NewCorpusIngestService + source/doc repo + cfg.MinIO; uploadService retained for initSeedFiles/UploadHandler).
- Key outcomes: handler reads 6 provenance fields + streaming md5/sha256 over raw bytes; anti-forgery check (contentSHA256==Provenance.SourceSHA256) before any storage/service call (zero put, zero ingest on mismatch); PutObject merged + presign via injectable putMerged/presign function fields (testable seam); calls CorpusIngestService.Ingest; errors.Is(ErrCorpusValidation)→400, other→500 with fixed sanitized body (GC6 verified, no token/kafka/minio leak); InternalAuthMiddleware retained; MergeChunks legacy produce fully bypassed (old KnowledgeIngestService interface deleted).
- Pre-existing python failures (4) in tests/corpus/test_import_docs.py are Task 8 WIP (import_docs.py), not caused by Task 3; GC7 pytest-green deferred to Task 8.

## Task 4: complete (commits 33329650..f1c6037a, review clean, R1 verified)

- Implementer: DONE, RED→GREEN, full go test green, go vet clean, 0 fix rounds.
- Files: internal/repository/pipeline_task_repository.go(+test, glebarez/sqlite in-memory), pkg/kafka/client.go(+test, shouldSkipByStatus pure fn), pkg/database/migration.go (tolerant ALTER pipeline_task ADD COLUMN run_id), go.mod/go.sum (added github.com/glebarez/sqlite test dep + go.mod tidy hygiene).
- R1 outcome: shouldSkipByStatus(prev, hasRunID) — legacy messages byte-identical behavior (SUCCESS skip via GetByKey/MarkProcessing old methods); run messages use `run:{runID}:{md5}:{stage}:{chunk}` namespace coexisting with legacy keys on same unique index; MarkProcessingRun idempotent (Create path RetryCount=0, found-path Save keeps RetryCount); no DELETE/UPDATE of legacy rows; RunID column guaranteed via AutoMigrate + belt-and-suspenders ALTER. TestLegacyMarkProcessingCoexistsWithRunRows proves coexistence.
- Note: commit amended once to strip stray '@' from PowerShell here-string leak (message text only).

## Task 5: complete (commits f1c6037a..3c4d5665, review clean)

- Implementer: DONE, RED→GREEN, full go test green, go vet/build clean, 0 fix rounds.
- Files: internal/repository/knowledge_document_repository.go(+test, GetDocumentByFileMD5 multi-version: non-FAILED preferred over newer FAILED, all-FAILED→latest), internal/service/admin_service.go(+test, ReplayPipelineTask(fileMD5, stage, runID)), internal/handler/admin_handler.go (runId field), cmd/server/main.go (NewAdminService +documentRepo), internal/service/corpus_ingest_service_test.go (carry-over from NewAdminService seam).
- Outcome: corpus docs (KnowledgeDocument exists) auto-stamped with Provenance+DocumentID+CorpusGeneration+RunID (SourceSHA256=doc.ContentSHA256, aligns with Task 6 raw-hash-first); non-corpus uploads keep legacy shape but now carry RunID so admin replay actually bypasses stale SUCCESS; empty runID auto-generates `replay-{unixnano}`; unknown stage errors; corpus-vs-legacy detection solely via KnowledgeDocument (GC2 respected — no path for ordinary upload to gain CorpusGeneration).

## Task 6: complete (commits 3c4d5665..93303d05, review clean)

- Implementer: DONE, RED→GREEN, full go test/vet/build green, 0 fix rounds.
- Files: internal/pipeline/processor.go (NewProcessor +documentRepo param; markDocumentActive on processIndex:437 + processIndexExternal:975 AFTER ES bulk success; markDocumentFailed on chunk BatchCreate failure:596; sourceSHA256ForChunk raw-hash-first R3 fix; processChunkExternalArtifact split + RDB/MinioClient nil-guards), cmd/server/main.go (documentRepo wiring), +4 test files (fake_document_repo, fake_ingestion_client, processor_lifecycle, processor_index).
- R3 fix: task.Provenance.SourceSHA256 wins when present; legacy/no-provenance byte-identical to old artifact-hash logic (MinerU PDF + client_fill preserved). Nil-safe (documentRepo==nil || DocumentID=="" → no-op). Bonus: fixed latent nil-deref in external index cleanup.

## Task 7: complete (commits 93303d05..ad39c7ae + gofmt c7417aad, review clean)

- Implementer: DONE, RED→GREEN, full go test/vet/build green, 0 fix rounds.
- Files: internal/handler/knowledge_document_handler.go(+test), internal/repository/knowledge_document_repository.go(+test, ListDocumentsByGenerationAndStatus), cmd/server/main.go (route under internalGroup), +2 fakeDocumentRepo updates.
- Outcome: GET /internal/orchestrator/knowledge-documents read-only; generation server-authoritative (mismatch/missing→400 zero repo calls); status whitelist STAGED/ACTIVE/FAILED strict; InternalAuth via internalGroup; GC6 sanitized 500 on repo error; cross-generation exposure double-guarded (handler gate + repo WHERE).
- Controller gofmt fix: c7417aad (only Task-7-introduced file). Pre-existing repo-wide gofmt noise (cli/corpus/handler/middleware/service/tools/etc) is baseline, NOT introduced by this plan — parked for final review triage, not bundled into a giant style commit.

## Task 8: complete (commits c7417aad..c5adc0ab, review clean)

- Implementer: DONE, RED→GREEN, python -m pytest -q = 299 passed (0 fail; the 4 prior import_docs failures cleared), 0 fix rounds.
- Files: scripts/corpus/import_docs.py (rebuilt from scratch), tests/corpus/test_import_docs.py (17 contract tests). Old half-finished draft moved to scripts/corpus/import_docs.py.broken-20260803 (untracked, preserved scene).
- Outcome: dedicated client for POST /internal/orchestrator/knowledge-ingest (X-Internal-Token + 6 provenance fields); NEVER calls fast-upload (legacy route blocked); pre-check ACTIVE (legit skip, no re-upload); poll ACTIVE/FAILED/timeout; exit 0/1/2; --report JSON all fields + no token; manifest include+exclude+formats + traversal rejection; --file narrows policy set; --limit counts attempts; sourceSha256 over raw staging bytes; userId from manifest loader_user.

## fix-runid (commit c5adc0ab..f5fcb20c, review clean) — Task 10 e2e blocker

- Defect found in Task 10 real run: handler CorpusIngestRequest omitted RunID; importer form omitted runId → task.RunID always empty → consumer legacy dedup → historical parse SUCCESS silently skipped new parse → doc stuck STAGED.
- Fix: handler reads form runId into req.RunID (knowledge_ingest_handler.go:218); importer upload_one/process_one require run_id kwarg; main generates `import-{unix}` and threads it through form + report.
- Reviewer: PASS/APPROVED, 0 rounds, full run-aware chain verified (importer form → handler req → task.RunID → consumer GetByRunKey + shouldSkipByStatus=false).

## Task 10: PASS (single-document real gate)

- asm.html (go/doc/asm.html, MD5 796c9a98...) imported via dedicated entry; importer exit=0 active=1.
- ES v2: 0 → 7 chunks. document_vectors=7 with full provenance. knowledge_document=ACTIVE, target_index=knowledge_base_v2_bge_m3. pipeline latest run parse/chunk/embed/index all SUCCESS.
- source_sha256 in ES == knowledge_document.content_sha256 (raw file hash, R3 fix proven). legacy=44 unchanged, alias=knowledge_base unchanged, no new DLQ.
- Runtime fix (not code): worker restarted with `PAISMART_EMBEDDING_BASE_URL=http://127.0.0.1:8009`, `PAISMART_EMBEDDING_MODEL=BAAI/bge-m3`, `PAISMART_EMBEDDING_DIMENSIONS=1024` (config.py defaulted to deepseek/512 when unset → embed 500). Worker embed endpoint now returns 200/1024 dims.

## Task 11: pilot (limit 2/source) — format compatibility CONFIRMED, 1 benign failure

- v2: 7 → 30 chunks. go (skip asm.html + active go_mem.html), python (2 rst ACTIVE), git (2 adoc ACTIVE), docker (2 md ACTIVE), postgresql (2 sgml ACTIVE), kubernetes (1 md ACTIVE + 1 FAILED).
- All formats verified end-to-end ACTIVE: md, html, rst, adoc, sgml.
- k8s `content/en/docs/_index.md` FAILED: 96-byte pure Hugo front matter (no body) → Tika parse returns empty → "parse: extracted text is empty". k8s has 173 `_index.md` files (mixed: `concepts/_index.md` has content+ACTIVE; root `_index.md` empty+FAILED).
- Decision pending: this is a data-quality issue (empty front-matter-only docs), not a code/format bug. Options: (A) manifest exclude empty `_index.md` (needs pathMatches `**` support or per-path), (B) processor graceful-skip empty parse (new status or ACTIVE-0-chunks), (C) accept as benign, classify in full-import report.

## fix-empty-skip (commit 51626c25..603d0fa2, review clean)

- Added `model.DocumentSkipped = "SKIPPED"`. processParseExternal: empty ParsedText + corpus task (DocumentID non-empty + documentRepo non-nil) → MarkDocumentStatus(SKIPPED, "parse: empty content after parse") + no chunk task + return nil (parse SUCCESS, no retry). Legacy tasks (empty DocumentID) keep original "extracted text is empty" error (backward compat). Handler whitelist accepts SKIPPED. importer poll queries ACTIVE→SKIPPED→FAILED; SKIPPED → success (exit 0).
- Verified: rebuilt server, re-ran k8s pilot — `content/en/docs/_index.md` now SKIPPED (was FAILED), importer exit=0 failed=0.

## Task 11: PILOT PASS (6 sources, format compatibility + empty-skip confirmed)

- limit-2/3 pilot across all 6 sources: go/python/git/docker/postgresql all ACTIVE; k8s ACTIVE for content docs + SKIPPED for empty `_index.md`. v2 grew 0→30+. All formats (md/html/rst/adoc/sgml) proven end-to-end. Empty front-matter-only docs gracefully skipped (not failed).
- Reviewer MINOR parked: empty-skip only in processParseExternal (internal-parser path still hard-errors — low risk, corpus uses external worker); redundant re-parse of previously-SKIPPED docs on importer re-run (is_document_active queries ACTIVE only — idempotent, minor waste).

## Parked (minor, deferred to final review)

- Task2: fakeSourceRepo does not simulate OnConflict-DoNothing idempotency (harmless; service never branches on new-vs-existing source).
- Task2: Ingest always enqueues parse even for existing document (safe-by-design via Task 4 run-aware dedup + stable vector_id re-index).
- Task2: storage/producer errors propagate raw via %w (in-spec; Task 3 handler must NOT echo err.Error() to HTTP body per GC6).
- Task3: handler buffers full upload in memory (256MB cap); acceptable for text corpus but a regression vs old streaming profile — revisit if large files.
- Task3: generation/loader_user/target_index validation lives in CorpusIngestService (not handler-side as plan literally stated); consistent with "server is authority" design, end-to-end covered.
