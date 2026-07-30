# RAG Foundation Plan 1 Implementation Record

Date: 2026-07-30
Branch: `feature/complete-design-implementation`
Status: `VERIFIED`

## Delivered

1. `39f93e1` adds stable `SourceVersionID` and `DocumentID` helpers, source/document lifecycle models, GORM tags, and contract tests.
2. `fea1177` adds `StructuredChunk.Validate`, JSON array round-trip tests, and provenance fields to `DocumentVector` and `EsDocument` while retaining legacy fields.
3. `88f3831` adds idempotent source/document repositories, status/list/count operations, metadata AutoMigrate, and guarded additive `document_vectors` migration statements.
4. `c752754` adds corpus defaults, configuration tests, and read-only `scripts/rag_snapshot.ps1` with recursive redaction.

## Verification

- Foundation Go tests and vet passed for `internal/model`, `internal/repository`, `pkg/database`, `internal/serverconfig`, and `pkg/es`.
- Explicit MySQL integration passed with `CODE_AGENT_RUN_DB_INTEGRATION=1`; repeated source/document creation remained one row and cleanup removed only test IDs.
- Read-only Docker snapshot passed with MySQL `knowledge_source=0`, `knowledge_document=0`, `document_vectors=4`; Elasticsearch old index count `9`, v2 index `404/not found`; MinIO `uploads` listed `8` objects.
- Snapshot output was scanned for API keys/passwords and was clean.

## Boundaries

No v2 index was created, no read or visual alias was switched, and no corpus was imported. Existing knowledge index, smoke/E2E rows, model caches, and Docker volumes were preserved. Plans 2-7 remain pending; Plan 2 must implement the MinerU OCR `content_list.json`/`middle.json` adapter and parent/child structured chunking.

